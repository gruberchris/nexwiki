package server

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Agent attribution for the activity log.
//
// The activity log has always carried an `agent` column, and the Activity Drawer renders it and
// offers it as a filter facet. It never held an agent's identity. It held this:
//
//	agent := "AI Agent"
//	if srvName := os.Getenv("NEXWIKI_NAME"); srvName != "" { agent = srvName }
//
// NEXWIKI_NAME is documented as the wiki's display title, and the recommended compose files set it
// to things like "My Personal Brain". So every agent write on a configured deployment was
// attributed to the wiki itself, and on an unconfigured one to the literal string "AI Agent". The
// column could not distinguish two agents — which is the only thing it exists to do.
//
// Meanwhile the identity was already arriving on the wire and being discarded: the modern era
// decodes `io.modelcontextprotocol/clientInfo` into requestMeta and nothing read it, and legacy
// `initialize` carries `clientInfo` that was never looked at.
//
// Attribution is self-reported by the client. It is a provenance hint, not an authentication
// claim, and nothing downstream may treat it as one — see SECURITY.md.

// DefaultAgentName is the attribution used when no client identifies itself and no name is
// configured. Unchanged from the previous behavior, so an anonymous caller still reads the same.
const DefaultAgentName = "AI Agent"

// maxAgentNameBytes bounds a client-supplied name before it reaches the activity log. The value is
// self-reported and lands in a durable append-only file that the UI renders, so it gets a ceiling
// rather than trust.
const maxAgentNameBytes = 120

// clientInfo is the MCP `clientInfo` shape, identical in both eras: legacy sends it inside
// `initialize` params, modern sends it per request under a reserved `_meta` key.
type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

// agentIdentity holds the identity captured from a single stdio connection.
//
// Stdio is the only transport where a handshake can be remembered: it is one process talking to
// one client for the life of the connection. HTTP is sessionless by design — §3.4 records that the
// 2026-07-28 revision removed sessions and that NexWiki correctly ignores Mcp-Session-Id — so a
// legacy `initialize` arriving over HTTP has nothing to attach to, and caching it there would
// attribute one client's writes to another.
type agentIdentity struct {
	mu   sync.RWMutex
	name string
}

func (a *agentIdentity) set(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.name = name
}

func (a *agentIdentity) get() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.name
}

// describeClient renders a clientInfo as an attribution string: "Claude Desktop 1.4.2", falling
// back to the bare name when no version is offered. Returns "" when there is nothing usable, so
// callers can fall through to the next source rather than recording an empty agent.
func describeClient(info clientInfo) string {
	name := strings.TrimSpace(info.Name)
	if name == "" {
		// `title` is the spec's human-facing display name; prefer `name`, but do not throw away a
		// client that only sent the other one.
		name = strings.TrimSpace(info.Title)
	}
	if name == "" {
		return ""
	}
	if version := strings.TrimSpace(info.Version); version != "" {
		name += " " + version
	}
	return truncateAgentName(name)
}

// truncateAgentName bounds a self-reported name and strips the characters that would corrupt the
// log's readability. Newlines matter most: the activity log is JSON Lines, and while encoding/json
// escapes them safely, a name containing one still renders as a broken row in the drawer.
func truncateAgentName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if len(name) > maxAgentNameBytes {
		// Trim on a rune boundary so a multi-byte name is not cut mid-character.
		trimmed := []rune(name)
		for len(string(trimmed)) > maxAgentNameBytes {
			trimmed = trimmed[:len(trimmed)-1]
		}
		name = string(trimmed)
	}
	return name
}

// parseClientInfo decodes a clientInfo blob, tolerating absence and malformation. A client that
// sends nonsense in this field gets no attribution, not an error: identity is advisory and must
// never fail a tool call.
func parseClientInfo(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var info clientInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return ""
	}
	return describeClient(info)
}

// rememberStdioClient captures the client identity from a legacy `initialize` on the stdio
// transport, for the life of the connection. Called only from the stdio path — see agentIdentity.
func (srv *Server) rememberStdioClient(params json.RawMessage) {
	if len(params) == 0 {
		return
	}
	var initParams struct {
		ClientInfo json.RawMessage `json:"clientInfo"`
	}
	if err := json.Unmarshal(params, &initParams); err != nil {
		return
	}
	if name := parseClientInfo(initParams.ClientInfo); name != "" {
		srv.stdioClient.set(name)
	}
}

// resolveAgent decides who to credit for the request in hand, most specific source first:
//
//  1. Per-request `_meta` clientInfo (modern era). The cleanest source and the reason this needs no
//     session state — 2026-07-28 puts client info on every request.
//  2. The identity captured from a legacy `initialize` on this stdio connection.
//  3. The configured NEXWIKI_AGENT_NAME / -agent-name. What a legacy HTTP client, a curl script, or
//     an SDK that sends no client info gets.
//  4. "AI Agent".
//
// NEXWIKI_NAME is deliberately absent from this list. It is the wiki's title and never meant
// anything about who was writing.
func (srv *Server) resolveAgent(env paramsEnvelope) string {
	if env.Meta != nil {
		if name := parseClientInfo(env.Meta.ClientInfo); name != "" {
			return name
		}
	}
	if name := srv.stdioClient.get(); name != "" {
		return name
	}
	if name := strings.TrimSpace(srv.AgentName); name != "" {
		return truncateAgentName(name)
	}
	return DefaultAgentName
}

// provenanceMatchWindow is how far a revision's timestamp may sit from an activity-log event and
// still be considered the same write.
//
// The two clocks are the same clock — the log event is published immediately after the save
// returns — so the real gap is milliseconds. The window is generous anyway because the cost of
// being wrong is asymmetric: a missed match shows no attribution, which is honest, while a window
// so tight that ordinary scheduling jitter breaks it would make the feature look broken.
const provenanceMatchWindow = 5 * time.Second

// provenanceScanLimit bounds how many events the join reads.
//
// §3.14 recorded a deliberate scope decision: the activity log's read path parses the active file
// end to end, bounded only by the 10 MB rotation threshold (~119 ms worst case). get_article_history
// is an interactive tool, so it does not get to pay that unbounded — the filter's Limit stops the
// walk once enough events for this slug are in hand.
const provenanceScanLimit = 500

// writeActions are the log actions that correspond to a stored revision. A "read" never produces
// one, and matching against reads would credit whoever last *looked* at the article.
var writeActions = map[string]bool{"create": true, "edit": true, "delete": true, "revert": true}

// attributeRevisions fills in Agent/Tool/Via on each revision by joining the activity log on slug
// and timestamp proximity.
//
// Matching is by nearest timestamp rather than by ordinal position. Ordinal matching — the k-th
// write event is the k-th revision — is tempting and wrong: the log rotates, can be pruned via
// NEXWIKI_ACTIVITY_MAX_ARCHIVES, and predates neither the wiki nor every article in it, so any
// missing event would silently shift every subsequent attribution by one. Getting attribution
// wrong is worse than leaving it blank.
//
// Revisions with no event in range are left unattributed, which is the normal case for an article
// older than the log.
func attributeRevisions(logPath string, slug string, versions []RevisionRef) []RevisionRef {
	if logPath == "" || len(versions) == 0 {
		return versions
	}

	events, err := ReadActivityLogFiltered(logPath, ActivityFilter{Slug: slug, Limit: provenanceScanLimit})
	if err != nil || len(events) == 0 {
		return versions
	}

	// Assignment is one-to-one and runs in chronological order. Both of those matter.
	//
	// Article timestamps are stored at RFC3339 second resolution, and an agent editing a document
	// produces several revisions well inside one second. Picking the "nearest" event independently
	// for each revision therefore hands the *same* event to several of them — measured: a create
	// and the edit that followed it 129 ms later were both credited to the create. Consuming each
	// event, and walking revisions oldest-first so the earliest revision claims the earliest event,
	// resolves the ambiguity that timestamps alone cannot.
	order := make([]int, len(versions))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return versions[order[a]].Timestamp.Before(versions[order[b]].Timestamp)
	})

	used := make([]bool, len(events))
	for _, idx := range order {
		best := -1
		var bestGap time.Duration
		for j, ev := range events {
			if used[j] || !writeActions[ev.Action] {
				continue
			}
			gap := ev.Timestamp.Sub(versions[idx].Timestamp)
			if gap < 0 {
				gap = -gap
			}
			if gap > provenanceMatchWindow {
				continue
			}
			// Strictly-less keeps the earliest event on a tie, which combined with the
			// oldest-first walk preserves the true order when several writes share a second.
			if best == -1 || gap < bestGap {
				best, bestGap = j, gap
			}
		}
		if best >= 0 {
			used[best] = true
			versions[idx].Agent = events[best].Agent
			versions[idx].Tool = events[best].Tool
			versions[idx].Via = events[best].Source
		}
	}
	return versions
}

// ResolveConfiguredAgentName returns the configured agent attribution, with the environment
// variable taking precedence over the flag — matching how NEXWIKI_NAME and NEXWIKI_THEME already
// behave, per the rule in README's configuration table.
func ResolveConfiguredAgentName(flagValue string) string {
	if env := strings.TrimSpace(os.Getenv("NEXWIKI_AGENT_NAME")); env != "" {
		return env
	}
	return strings.TrimSpace(flagValue)
}

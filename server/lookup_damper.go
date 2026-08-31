package server

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// The repeat-lookup damper.
//
// §0 of the seeded guidelines says a lookup returning nothing is a *completed* check and that
// reworded retries are forbidden. That is the rule an agent broke on 2026-08-18, alternating
// read_article and search_wiki for 31 minutes and ~170 MCP calls, and the remediation for it was
// more prose. Nothing in the server noticed the repetition — LogEvent records the tool name but
// not the query, so even the activity log could not see the same search run eleven times.
//
// This notices. It never blocks.
//
// # Why advisory
//
// A false positive that blocks retrieval breaks real work; one that adds a sentence costs nothing.
// The damper's job is to interrupt a loop the agent cannot see it is in, and a sentence in the
// result is enough to do that — the agent reads its own tool output.
//
// # Why the query text is never persisted
//
// The fingerprint lives in memory for two minutes and is dropped. Recording queries in the
// activity log was considered and rejected: that log is durable, append-only, rendered in the web
// UI, and governed by SECURITY.md. Query strings are free text that may carry anything a user
// typed. The damper needs a 120-second fingerprint, not a permanent record — so it hashes, and
// keeps nothing that could be read back as a query.

const (
	// damperRingSize is how many recent lookups are remembered per agent. Eight is comfortably
	// more than the two or three distinct questions a single task asks, and small enough that a
	// genuinely varied session evicts old entries before they can pair with a later repeat.
	damperRingSize = 8

	// damperMaxAgents bounds total memory. Beyond this the least-recently-used agent is dropped;
	// losing a damper ring costs nothing but a missed notice.
	damperMaxAgents = 64

	// damperWindow is how long a lookup stays comparable. Long enough to span the retry loop this
	// exists to catch, short enough that returning to a topic later in a session is not flagged —
	// re-asking a question ten minutes on is normal work, not a loop.
	damperWindow = 120 * time.Second
)

// queryStopWords are dropped before fingerprinting so a reworded query collides with the original.
//
// Deliberately narrower than titleStopWords, which also drops "nexwiki" — that word separates
// nothing in an article *title* in this wiki, but "nexwiki" in a search query is a real term and
// discarding it would make two genuinely different searches look identical.
var queryStopWords = map[string]bool{
	"a": true, "an": true, "and": true, "the": true, "for": true, "of": true,
	"to": true, "in": true, "on": true, "with": true, "or": true, "is": true,
	"are": true, "was": true, "were": true, "do": true, "does": true, "did": true,
	"how": true, "what": true, "why": true, "when": true, "where": true, "we": true,
}

// fingerprintQuery reduces a lookup to a value that is equal for reworded forms of the same
// question: lowercase, strip punctuation, drop stop words, sort, hash.
//
// The sort is the point. "docker build error" and "error building docker" produce the same
// fingerprint, and rewordings are exactly what the livelock consisted of — an agent that asks the
// identical question twice is easy to catch, and is not the failure mode that happened.
func fingerprintQuery(tool, query string) uint64 {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	kept := make([]string, 0, len(fields))
	for _, f := range fields {
		if !queryStopWords[f] {
			kept = append(kept, crudeStem(f))
		}
	}
	sort.Strings(kept)

	// The tool name is part of the fingerprint so the same words asked of search_wiki and of
	// list_agent_memories are two different questions — which they are.
	sum := sha256.Sum256([]byte(tool + "\x00" + strings.Join(kept, " ")))
	return binary.BigEndian.Uint64(sum[:8])
}

// crudeStem strips the handful of English suffixes that make a reworded query look different.
//
// Without it, sorting tokens is not enough: the motivating example for this whole feature is
// "docker build error" versus "error building docker", and those two share only two tokens out of
// three because `build` and `building` are distinct strings. Sorting fixes the order; nothing
// fixed the inflection.
//
// It is deliberately crude, and the risk profile is what licenses that. A stemmer used for
// *retrieval* returns wrong documents when it is wrong. This one only decides whether two lookups
// count as the same question, so a wrong stem is harmless as long as it is deterministic — both
// sides stem identically — and the worst case is one unnecessary advisory line. That is a very
// different trade from the one Phase 6 of the search plan has to make.
//
// The four-character floor on the remainder is what keeps it from mangling short words: "string"
// and "ring" keep their "ing" because stripping it would leave three characters or fewer.
func crudeStem(word string) string {
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if strings.HasSuffix(word, suffix) && len(word)-len(suffix) >= 4 {
			return word[:len(word)-len(suffix)]
		}
	}
	return word
}

// damperEntry is one remembered lookup.
type damperEntry struct {
	fingerprint uint64
	at          time.Time
	// count is how many times this fingerprint has been seen inside the window.
	count int
}

// lookupDamper tracks recent lookups per agent. The zero value is not usable; call newLookupDamper.
type lookupDamper struct {
	mu sync.Mutex
	// rings is keyed by resolved agent name. resolveAgent already yields a per-request identity on
	// both transports, which sidesteps the problem agentIdentity documents: stdio can remember a
	// handshake, HTTP is sessionless by design. No session state is invented here.
	rings map[string][]damperEntry
	// order tracks agent recency so eviction can drop the least recently active.
	order []string
	// now is injectable so tests can advance the clock without sleeping.
	now func() time.Time
}

func newLookupDamper() *lookupDamper {
	return &lookupDamper{rings: map[string][]damperEntry{}, now: time.Now}
}

// observe records a lookup and reports how many times this question has been asked inside the
// window, counting this one. A return of 1 means it is new.
func (d *lookupDamper) observe(agent, tool, query string) (occurrence int, sinceFirst time.Duration) {
	if d == nil {
		return 1, 0
	}
	fp := fingerprintQuery(tool, query)

	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()

	ring := d.rings[agent]
	kept := ring[:0]
	for _, e := range ring {
		if now.Sub(e.at) <= damperWindow {
			kept = append(kept, e)
		}
	}
	ring = kept

	for i := range ring {
		if ring[i].fingerprint == fp {
			ring[i].count++
			// The timestamp is deliberately NOT refreshed. Refreshing it would let a steady drip
			// of repeats keep an entry alive forever; the window is measured from the first ask,
			// so a loop expires on its own once it stops.
			d.rings[agent] = ring
			d.touch(agent)
			return ring[i].count, now.Sub(ring[i].at)
		}
	}

	ring = append(ring, damperEntry{fingerprint: fp, at: now, count: 1})
	if len(ring) > damperRingSize {
		ring = ring[len(ring)-damperRingSize:]
	}
	d.rings[agent] = ring
	d.touch(agent)
	d.evict()
	return 1, 0
}

// clear forgets an agent's lookups. Called on a successful write, because a write is progress and
// the loop being damped is read-only by nature: an agent that has just created something is not
// stuck in the retrieval cycle this exists to interrupt.
func (d *lookupDamper) clear(agent string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.rings, agent)
	for i, name := range d.order {
		if name == agent {
			d.order = append(d.order[:i], d.order[i+1:]...)
			break
		}
	}
}

// touch moves an agent to the most-recent end of the eviction order. Caller holds the lock.
func (d *lookupDamper) touch(agent string) {
	for i, name := range d.order {
		if name == agent {
			d.order = append(d.order[:i], d.order[i+1:]...)
			break
		}
	}
	d.order = append(d.order, agent)
}

// evict drops the least recently active agents once the cap is exceeded. Caller holds the lock.
func (d *lookupDamper) evict() {
	for len(d.order) > damperMaxAgents {
		oldest := d.order[0]
		d.order = d.order[1:]
		delete(d.rings, oldest)
	}
}

// damperNotice renders the escalating advisory for a repeated lookup, or "" when there is nothing
// to say.
//
// It escalates because the two cases are different. A second identical lookup is usually a
// harmless double-check and one line is enough. A third means the agent is not reading its own
// results, so the notice stops describing and starts instructing — naming §0, stating that the
// check is complete, and pointing at the action that ends the loop.
func damperNotice(occurrence int, sinceFirst time.Duration) string {
	switch {
	case occurrence < 2:
		return ""
	case occurrence == 2:
		return fmt.Sprintf("Note: this repeats a lookup you ran %d seconds ago. Its result has not "+
			"changed.\n\n", int(sinceFirst.Seconds()))
	default:
		return fmt.Sprintf("⚠️  You have run this same lookup %d times in the last %d seconds, "+
			"rewording it does not change the answer.\n"+
			"Per §0 of the agent guidelines: a lookup that returns nothing is a COMPLETED check, not a "+
			"failed one. Stop searching and act on what you have — if you were looking for something to "+
			"create, create it now with the relevant create_* tool.\n\n",
			occurrence, int(sinceFirst.Seconds()))
	}
}

// damperedLookupQuery returns the query text a tool call should be fingerprinted on, and whether
// this tool is a lookup the damper watches at all.
//
// Only the two tools that take a free-text question are watched. read_article is deliberately not:
// re-reading a page is sometimes correct, its slug is not a reworded question, and §0 already
// covers the guidelines-reread case with a rule the tool descriptions repeat.
func damperedLookupQuery(tool string, args []byte) (string, bool) {
	switch tool {
	case "search_wiki":
		var a struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(args, &a)
		return a.Query, true
	case "list_agent_memories":
		var a struct {
			MemoryType string `json:"memory_type"`
			MemoryKind string `json:"memory_kind"`
		}
		_ = json.Unmarshal(args, &a)
		// A listing has no free text, so its facets *are* the question. Repeating an identical
		// unfiltered listing is still a repeat, and fingerprints as the empty query.
		return strings.TrimSpace(a.MemoryType + " " + a.MemoryKind), true
	}
	return "", false
}

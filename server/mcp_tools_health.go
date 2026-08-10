package server

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// This file holds wiki_health, the maintenance tool. Everything it reports is something the wiki
// already knows but never volunteers: a page nothing links to, a link that goes nowhere, a memory
// with no provenance, a plan that has been "in progress" for a month.
//
// The checks are one tool rather than several because they answer one question — "what needs
// attention?" — and an agent that has to make six calls to ask it will make none. The memory
// hygiene checks (§6.8) live in mcp_tools_hygiene.go for the same reason they were added here
// rather than as a new tool.

// Default and ceiling values for the tool's arguments.
const (
	defaultStaleDays    = 30
	defaultHealthLimit  = 50
	maxHealthLimit      = 500
	minimumStaleDayspan = 1
)

// inFlightStatusTags are the lifecycle tags that say work is underway. Carrying one is not
// required for a plan to be stale — it only makes the report more specific about why.
var inFlightStatusTags = []string{"wip", "in-progress", "draft", "active", "todo", "pending", "review", "blocked"}

// finishedStatusTags are the tags that end a plan's life. A plan carrying one is never stale,
// however old: nagging about work the user has already marked done is worse than staying quiet.
// "superseded" counts as finished because a replaced plan is not waiting on anyone either.
var finishedStatusTags = []string{"completed", "done", "superseded"}

// parkedStatusTags mark work deliberately set aside. A parked plan is not stale and is not
// finished, and until now the tool had no way to say so.
//
// This gap was found by running the report against the real corpus: two of its five stale plans
// were `git-backed-storage-backend-for-nexwiki` and `hybrid-vector-semantic-search-for-nexwiki` —
// the two product bets this very code review deliberately deferred. They are not abandoned and
// they are not done, so they would have been reported as needing attention forever. A check that
// keeps naming things the user has already decided about is one they learn to ignore.
var parkedStatusTags = []string{"parked", "deferred", "tabled", "on-hold", "someday"}

// HealthFinding is one item needing attention, in whichever category found it.
type HealthFinding struct {
	Slug string `json:"slug"`
	// Title is the document's title, or for a broken link, the link text as written.
	Title string `json:"title"`
	// Type is the OKF document class of the document the finding is about.
	Type string `json:"type,omitempty"`
	// Detail explains the finding in a form an agent can act on directly.
	Detail string `json:"detail"`
}

// HealthOutput is the wiki_health payload. Counts are reported separately from the item lists
// because the lists are capped: a wiki with 400 orphans should say so without returning 400 items.
type HealthOutput struct {
	TotalDocuments  int             `json:"total_documents"`
	StaleDays       int             `json:"stale_days"`
	Limit           int             `json:"limit"`
	Truncated       bool            `json:"truncated"`
	OrphanCount     int             `json:"orphan_count"`
	Orphans         []HealthFinding `json:"orphans"`
	BrokenLinkCount int             `json:"broken_link_count"`
	BrokenLinks     []BrokenLinkRef `json:"broken_links"`
	UnsourcedCount  int             `json:"unsourced_memory_count"`
	UnsourcedMemory []HealthFinding `json:"unsourced_memories"`
	StalePlanCount  int             `json:"stale_plan_count"`
	StalePlans      []HealthFinding `json:"stale_plans"`

	// --- memory hygiene (§6.8) ---

	// ColdDays is the recency threshold applied to memories.
	ColdDays int `json:"cold_days"`
	// ColdMemoryScanRan is false when the activity log does not reach back ColdDays, in which case
	// the cold check is skipped rather than reporting every memory. See scanColdMemories.
	ColdMemoryScanRan bool                  `json:"cold_memory_scan_ran"`
	ColdMemorySkipped string                `json:"cold_memory_skipped_reason,omitempty"`
	ColdMemoryCount   int                   `json:"cold_memory_count"`
	ColdMemories      []HealthFinding       `json:"cold_memories"`
	DuplicateCount    int                   `json:"duplicate_memory_count"`
	DuplicateMemories []DuplicateMemoryPair `json:"duplicate_memories"`
	ParkedPlanCount   int                   `json:"parked_plan_count"`
}

func healthOutputSchema() map[string]interface{} {
	finding := schemaObject(map[string]interface{}{
		"slug":   schemaOf("string", "Slug of the document the finding concerns."),
		"title":  schemaOf("string", "Document title, or for a broken link, the link text as written."),
		"type":   schemaOf("string", "OKF document class of the document."),
		"detail": schemaOf("string", "What is wrong, phrased as something to act on."),
	}, "slug", "title", "detail")

	broken := schemaObject(map[string]interface{}{
		"from_slug":   schemaOf("string", "Document containing the broken link."),
		"target":      schemaOf("string", "Raw link target as written between the double brackets."),
		"target_slug": schemaOf("string", "Slug the target resolves to; this is the page to create."),
	}, "from_slug", "target", "target_slug")

	duplicate := schemaObject(map[string]interface{}{
		"slug":       schemaOf("string", "One memory of the pair."),
		"title":      schemaOf("string", "Title of that memory."),
		"other_slug": schemaOf("string", "The memory it resembles."),
		"scope":      schemaOf("string", "The memory-<scope> both share; absent for unscoped memories."),
		"detail":     schemaOf("string", "What to do about it."),
	}, "slug", "title", "other_slug", "detail")

	return schemaObject(map[string]interface{}{
		"total_documents":            schemaOf("integer", "Documents scanned, including the home dashboard."),
		"stale_days":                 schemaOf("integer", "Age threshold applied to in-flight plans."),
		"limit":                      schemaOf("integer", "Maximum items returned per category."),
		"truncated":                  schemaOf("boolean", "True when a category hit the limit and its list is shorter than its count."),
		"orphan_count":               schemaOf("integer", "Documents no other document links to."),
		"orphans":                    schemaArrayOf(finding, "Orphaned documents, up to the limit."),
		"broken_link_count":          schemaOf("integer", "WikiLinks with no destination."),
		"broken_links":               schemaArrayOf(broken, "Broken WikiLinks, up to the limit."),
		"unsourced_memory_count":     schemaOf("integer", "Agent memories recorded without a source."),
		"unsourced_memories":         schemaArrayOf(finding, "Memories missing provenance, up to the limit."),
		"stale_plan_count":           schemaOf("integer", "In-flight plans untouched for longer than stale_days. Excludes plans tagged finished or parked."),
		"stale_plans":                schemaArrayOf(finding, "Stale plans, up to the limit."),
		"cold_days":                  schemaOf("integer", "Recency threshold applied to memories."),
		"cold_memory_scan_ran":       schemaOf("boolean", "False when the activity log does not reach back cold_days, in which case the cold-memory check was skipped rather than reporting every memory."),
		"cold_memory_skipped_reason": schemaOf("string", "Why the cold-memory check did not run, when it did not."),
		"cold_memory_count":          schemaOf("integer", "Memories neither read nor edited within cold_days."),
		"cold_memories":              schemaArrayOf(finding, "Cold memories, up to the limit."),
		"duplicate_memory_count":     schemaOf("integer", "Pairs of memories in the same scope with closely matching titles."),
		"duplicate_memories":         schemaArrayOf(duplicate, "Near-duplicate memory pairs, up to the limit."),
		"parked_plan_count":          schemaOf("integer", "Plans deliberately set aside; reported as a count only, since they need no action."),
	}, "total_documents", "stale_days", "limit", "truncated",
		"orphan_count", "orphans", "broken_link_count", "broken_links",
		"unsourced_memory_count", "unsourced_memories", "stale_plan_count", "stale_plans",
		"cold_days", "cold_memory_scan_ran", "cold_memory_count", "cold_memories",
		"duplicate_memory_count", "duplicate_memories", "parked_plan_count")
}

var wikiHealthTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "wiki_health",
		"description": "Audit the knowledge base for maintenance work: orphan pages nothing links to, broken double-bracket WikiLinks, agent memories recorded without a 'source', in-flight plans that have gone stale, memories nothing has read or edited in months, and near-duplicate memories in the same scope that may have drifted apart. Use it at the start of a maintenance session, or before a big reorganization, to find what needs attention without reading every document.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"stale_days": map[string]interface{}{
					"type":        "integer",
					"description": "How many days an in-flight plan may go untouched before it counts as stale (default 30).",
				},
				"cold_days": map[string]interface{}{
					"type":        "integer",
					"description": "How many days a memory may go unread and unedited before it counts as cold (default 90). Skipped entirely when the activity log does not reach back that far.",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum items reported per category (default 50, maximum 500). Counts are always complete even when the lists are capped.",
				},
			},
		},
	},
	Output:   healthOutputSchema(),
	Handler:  (*Server).toolWikiHealth,
	Behavior: toolBehavior{Title: "Wiki Health", ReadOnly: true},
}

func (srv *Server) toolWikiHealth(args json.RawMessage) (interface{}, *JSONRPCError) {
	type HealthArgs struct {
		StaleDays int `json:"stale_days"`
		ColdDays  int `json:"cold_days"`
		Limit     int `json:"limit"`
	}
	var hArgs HealthArgs
	if e := decodeToolArgs(args, &hArgs); e != nil {
		return nil, e
	}

	staleDays := hArgs.StaleDays
	if staleDays <= 0 {
		staleDays = defaultStaleDays
	}
	if staleDays < minimumStaleDayspan {
		staleDays = minimumStaleDayspan
	}
	limit := hArgs.Limit
	if limit <= 0 {
		limit = defaultHealthLimit
	}
	if limit > maxHealthLimit {
		limit = maxHealthLimit
	}

	graph, err := srv.Storage.ScanLinkGraph()
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error scanning the wiki: %v", err)}}}, nil
	}

	slugs := make([]string, 0, len(graph.Meta))
	for slug := range graph.Meta {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	coldDays := hArgs.ColdDays
	if coldDays <= 0 {
		coldDays = defaultColdMemoryDays
	}
	if coldDays < minimumStaleDayspan {
		coldDays = minimumStaleDayspan
	}

	staleBefore := time.Now().AddDate(0, 0, -staleDays)
	orphans := []HealthFinding{}
	unsourced := []HealthFinding{}
	stalePlans := []HealthFinding{}
	memories := []Article{}
	parkedPlans := 0

	for _, slug := range slugs {
		doc := graph.Meta[slug]

		// Archived documents are deliberately out of scope for every check. Archiving is the
		// user saying "this is done"; reporting it as needing attention inverts that.
		if IsArchived(&doc) {
			continue
		}

		// Orphan detection applies to wiki articles only. Memories, plans, and skills are reached
		// through their own list tools, the search facets, and the context overview — nobody
		// WikiLinks a memory, so calling every one of them an orphan is noise. Measured on the
		// real 83-document corpus: reporting every type flagged 70 documents, 27 of which were
		// agent documents behaving exactly as designed.
		//
		// home is the dashboard, not a leaf page: nothing links to a front page.
		if doc.Type == ContentTypeWiki && slug != "home" && graph.InboundCount[slug] == 0 {
			orphans = append(orphans, HealthFinding{
				Slug: slug, Title: doc.Title, Type: doc.Type,
				Detail: "No article links here. Link it from a related page, or archive it if it is finished.",
			})
		}

		if doc.Type == ContentTypeMemory {
			memories = append(memories, doc)
			if strings.TrimSpace(doc.Source) == "" {
				unsourced = append(unsourced, HealthFinding{
					Slug: slug, Title: doc.Title, Type: doc.Type,
					Detail: "Memory has no 'source'. A fact with no provenance cannot be re-verified later; set source with edit_agent_memory.",
				})
			}
		}

		if doc.Type == ContentTypePlan && isParked(doc.Tags) && !isFinished(doc.Tags) {
			parkedPlans++
		}

		if doc.Type == ContentTypePlan && doc.Timestamp.Before(staleBefore) && !isFinished(doc.Tags) && !isParked(doc.Tags) {
			// An in-flight tag is not required. On the real corpus almost no plan carries one —
			// they hold a project tag and nothing else — so requiring "wip" made the check
			// incapable of ever firing. What actually matters is that the plan was never marked
			// finished and nobody has touched it since.
			since := doc.Timestamp.Format("2006-01-02")
			detail := fmt.Sprintf("Untouched since %s and never marked finished. Finish it, tag it 'completed', or archive it.", since)
			if status, inFlight := inFlightStatus(doc.Tags); inFlight {
				detail = fmt.Sprintf("Tagged '%s' but untouched since %s. Finish it, tag it 'completed', or archive it.", status, since)
			}
			stalePlans = append(stalePlans, HealthFinding{
				Slug: slug, Title: doc.Title, Type: doc.Type, Detail: detail,
			})
		}
	}

	// Memory hygiene (§6.8). Both checks look only at memories, which the loop above collected.
	cold := scanColdMemories(ActivityLogPath(srv.Storage.DataDir), memories, coldDays)
	duplicates := findDuplicateMemories(memories, graph.Outbound)

	out := HealthOutput{
		TotalDocuments:    len(graph.Meta),
		StaleDays:         staleDays,
		Limit:             limit,
		OrphanCount:       len(orphans),
		BrokenLinkCount:   len(graph.Broken),
		UnsourcedCount:    len(unsourced),
		StalePlanCount:    len(stalePlans),
		ColdDays:          coldDays,
		ColdMemoryScanRan: cold.Ran,
		ColdMemoryCount:   len(cold.Findings),
		DuplicateCount:    len(duplicates),
		ParkedPlanCount:   parkedPlans,
	}
	if !cold.Ran {
		out.ColdMemorySkipped = fmt.Sprintf("the activity log only reaches back %d days, less than the %d-day "+
			"threshold, so every memory would look cold", cold.LogSpanDays, coldDays)
	}

	// Counts stay complete while the lists are capped, so a wiki with 400 orphans reports 400
	// without returning 400 items and burying every other category.
	out.Orphans, out.Truncated = capFindings(orphans, limit, out.Truncated)
	out.UnsourcedMemory, out.Truncated = capFindings(unsourced, limit, out.Truncated)
	out.StalePlans, out.Truncated = capFindings(stalePlans, limit, out.Truncated)
	out.ColdMemories, out.Truncated = capFindings(cold.Findings, limit, out.Truncated)
	out.DuplicateMemories = duplicates
	if len(out.DuplicateMemories) > limit {
		out.DuplicateMemories = out.DuplicateMemories[:limit]
		out.Truncated = true
	}
	out.BrokenLinks = graph.Broken
	if len(out.BrokenLinks) > limit {
		out.BrokenLinks = out.BrokenLinks[:limit]
		out.Truncated = true
	}

	return ToolResponse{
		Content:           []ToolContent{{Type: "text", Text: renderHealthReport(out)}},
		StructuredContent: out,
	}, nil
}

// isFinished reports whether a plan's tags say its life is over. A plan tagged both "wip" and
// "completed" is finished: the terminal state wins.
func isFinished(tags []string) bool {
	return hasAnyTag(tags, finishedStatusTags)
}

// isParked reports whether a plan has been deliberately set aside. Parked is not finished — the
// work may still happen — but it is a decision, and re-reporting a decision is noise.
func isParked(tags []string) bool {
	return hasAnyTag(tags, parkedStatusTags)
}

func hasAnyTag(tags []string, candidates []string) bool {
	for _, tag := range tags {
		for _, candidate := range candidates {
			if strings.EqualFold(tag, candidate) {
				return true
			}
		}
	}
	return false
}

// inFlightStatus reports the first in-flight status tag a document carries, so the report can name
// it. Absence is not exoneration — see the stale-plan check.
func inFlightStatus(tags []string) (string, bool) {
	for _, tag := range tags {
		for _, inFlight := range inFlightStatusTags {
			if strings.EqualFold(tag, inFlight) {
				return strings.ToLower(tag), true
			}
		}
	}
	return "", false
}

// capFindings truncates a category to the limit, reporting whether anything was dropped.
func capFindings(findings []HealthFinding, limit int, truncated bool) ([]HealthFinding, bool) {
	if len(findings) > limit {
		return findings[:limit], true
	}
	return findings, truncated
}

// renderHealthReport writes the prose from the same value the structured payload carries, so the
// two halves cannot disagree. Clean categories are still listed: "0 broken links" is information,
// and omitting it makes an agent wonder whether the check ran.
func renderHealthReport(out HealthOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "NexWiki Health Report (%d documents scanned)\n\n", out.TotalDocuments)
	fmt.Fprintf(&b, "- Orphan pages: %d\n", out.OrphanCount)
	fmt.Fprintf(&b, "- Broken WikiLinks: %d\n", out.BrokenLinkCount)
	fmt.Fprintf(&b, "- Memories with no source: %d\n", out.UnsourcedCount)
	fmt.Fprintf(&b, "- Stale plans (unfinished, untouched for %d+ days): %d\n", out.StaleDays, out.StalePlanCount)
	if out.ColdMemoryScanRan {
		fmt.Fprintf(&b, "- Cold memories (not read or edited in %d+ days): %d\n", out.ColdDays, out.ColdMemoryCount)
	} else {
		fmt.Fprintf(&b, "- Cold memories: not checked — %s\n", out.ColdMemorySkipped)
	}
	fmt.Fprintf(&b, "- Possible duplicate memories: %d\n", out.DuplicateCount)
	if out.ParkedPlanCount > 0 {
		// Reported so the number is not mistaken for plans that vanished from the stale list by
		// accident. Parked is a decision, so there is nothing to act on and no list to print.
		fmt.Fprintf(&b, "- Parked plans (deliberately set aside, not reported as stale): %d\n", out.ParkedPlanCount)
	}

	needsAttention := out.OrphanCount + out.BrokenLinkCount + out.UnsourcedCount +
		out.StalePlanCount + out.ColdMemoryCount + out.DuplicateCount
	if needsAttention == 0 {
		b.WriteString("\nNothing needs attention — the wiki is healthy. 🎉\n")
		return b.String()
	}

	writeFindings := func(label string, count int, findings []HealthFinding) {
		if count == 0 {
			return
		}
		fmt.Fprintf(&b, "\n== %s (%d) ==\n", label, count)
		for _, f := range findings {
			fmt.Fprintf(&b, "- %s (%s) — %s\n", f.Title, f.Slug, f.Detail)
		}
		if len(findings) < count {
			fmt.Fprintf(&b, "  ... and %d more; raise 'limit' to see them.\n", count-len(findings))
		}
	}

	writeFindings("Orphan pages", out.OrphanCount, out.Orphans)

	if out.BrokenLinkCount > 0 {
		fmt.Fprintf(&b, "\n== Broken WikiLinks (%d) ==\n", out.BrokenLinkCount)
		for _, bl := range out.BrokenLinks {
			fmt.Fprintf(&b, "- '[[%s]]' in '%s' — target '%s' does not exist. Create it or fix the link.\n",
				bl.Target, bl.FromSlug, bl.TargetSlug)
		}
		if len(out.BrokenLinks) < out.BrokenLinkCount {
			fmt.Fprintf(&b, "  ... and %d more; raise 'limit' to see them.\n", out.BrokenLinkCount-len(out.BrokenLinks))
		}
	}

	writeFindings("Memories with no source", out.UnsourcedCount, out.UnsourcedMemory)
	writeFindings("Stale plans", out.StalePlanCount, out.StalePlans)
	writeFindings("Cold memories", out.ColdMemoryCount, out.ColdMemories)

	if out.DuplicateCount > 0 {
		fmt.Fprintf(&b, "\n== Possible duplicate memories (%d) ==\n", out.DuplicateCount)
		for _, d := range out.DuplicateMemories {
			scope := d.Scope
			if scope == "" {
				scope = "unscoped"
			}
			fmt.Fprintf(&b, "- '%s' (%s) and '%s' — same scope '%s'. %s\n",
				d.Title, d.Slug, d.OtherSlug, scope, d.Detail)
		}
		if len(out.DuplicateMemories) < out.DuplicateCount {
			fmt.Fprintf(&b, "  ... and %d more; raise 'limit' to see them.\n", out.DuplicateCount-len(out.DuplicateMemories))
		}
	}

	return b.String()
}

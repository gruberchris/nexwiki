package server

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// This file holds get_wiki_overview, the orientation call every agent session is told to make
// first. Because every session pays for it, its response is bounded: counts, the memories that
// apply to any task, the plans in flight, recent activity, and the status vocabularies. It is
// not an index — search_wiki lists documents without a query and finds them with one — and nothing in it grows
// with the number of documents in the wiki.

const (
	// maxOverviewEntries caps each list in the overview (pinned memories, active plans).
	maxOverviewEntries = 25
	// maxOverviewDescriptionRunes caps the description of each compact entry, so a single
	// pathological description cannot blow up the payload.
	maxOverviewDescriptionRunes = 200
	// maxOverviewTags caps the tags carried by each active-plan entry, for the same reason.
	maxOverviewTags = 10
	// maxOverviewEvents caps the recent activity.
	maxOverviewEvents = 20
)

// overviewNextSteps is the pointer to the rest of the wiki, carried in both the prose and the
// structured output.
const overviewNextSteps = "This overview does not list every document. For the full index call " +
	"search_wiki without a query, filtered by type ('articles', 'memories', 'plans', 'skills'), status, or tag and " +
	"paged with cursor. To find documents about a topic give search_wiki a query. Open any entry with read_article(slug)."

var getWikiOverviewTool = toolDef{
	Schema: map[string]interface{}{
		"name": "get_wiki_overview",
		"description": "Session orientation in one bounded call: document counts by type and plan status, the user and feedback memories that apply to any task, the plans in flight (implementing or blocked), recent activity since a duration, and the status vocabularies. " +
			"It does not list every document — search_wiki without a query is the full index (type/status/tag filters, cursor paging), and with one finds a topic. " +
			"Broken links and other link-graph problems are wiki_health's job.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"since": map[string]interface{}{
					"type":        "string",
					"description": "Optional filter for recent activity. Accepts a Go duration (e.g. '24h', '48h') or RFC3339 timestamp. Defaults to '48h'.",
				},
			},
		},
	},
	Output:   overviewOutputSchema(),
	Handler:  (*Server).toolGetWikiOverview,
	Behavior: toolBehavior{Title: "Get Wiki Overview", ReadOnly: true},
}

func (srv *Server) toolGetWikiOverview(args json.RawMessage) (interface{}, *JSONRPCError) {
	type OverviewArgs struct {
		Since string `json:"since"`
	}
	var oArgs OverviewArgs
	_ = json.Unmarshal(args, &oArgs)

	sinceStr := strings.TrimSpace(oArgs.Since)
	if sinceStr == "" {
		sinceStr = "48h"
	}
	var since time.Time
	if dur, err := time.ParseDuration(sinceStr); err == nil {
		since = time.Now().Add(-dur)
	} else if ts, err := time.Parse(time.RFC3339, sinceStr); err == nil {
		since = ts
	} else {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: invalid 'since' value '%s'. Use a Go duration (e.g. '48h') or an RFC3339 timestamp.", sinceStr)}}}, nil
	}

	articles, err := srv.Storage.ListArticles()
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: srv.clientError(err)}}}, nil
	}

	out := buildOverview(articles)
	out.RecentActivity = srv.overviewActivity(since)
	out.StatusTags = &StatusTagsOutput{
		StatusTags:      StatusTags,
		PlanStatusTags:  PlanStatusTags,
		SkillStatusTags: SkillStatusTags,
	}

	return ToolResponse{
		Content:           []ToolContent{{Type: "text", Text: renderOverviewText(out, sinceStr)}},
		StructuredContent: out,
	}, nil
}

// overviewActivity returns at most maxOverviewEvents events since the given time, oldest first:
// from the durable activity log, or from the in-memory ring when there is no log.
func (srv *Server) overviewActivity(since time.Time) []LogEvent {
	events, _ := ReadActivityLog(ActivityLogPath(srv.Storage.DataDir), since, maxOverviewEvents, "", "")
	if events == nil && srv.EventBus != nil {
		for _, ev := range srv.EventBus.GetHistory() {
			if !since.IsZero() && ev.Timestamp.Before(since) {
				continue
			}
			events = append(events, ev)
		}
		if len(events) > maxOverviewEvents {
			events = events[len(events)-maxOverviewEvents:]
		}
	}
	if events == nil {
		events = []LogEvent{}
	}
	return events
}

// buildOverview computes the document-derived half of the overview: counts, pinned memories, and
// active plans. Everything it returns is fixed-size or capped.
func buildOverview(articles []Article) OverviewOutput {
	out := OverviewOutput{
		TotalArticles:    len(articles),
		PlanStatusCounts: make(map[string]int, len(PlanStatusTags)+1),
		NextSteps:        overviewNextSteps,
	}
	for _, status := range PlanStatusTags {
		out.PlanStatusCounts[status] = 0
	}

	var pinned, active []Article
	for i := range articles {
		art := &articles[i]
		switch normalizeType(art.Type) {
		case ContentTypeMemory:
			out.Counts.Memories++
			if isPinnedMemoryKind(art.MemoryKind) && !IsArchived(art) {
				pinned = append(pinned, *art)
			}
		case ContentTypePlan:
			out.Counts.Plans++
			status := NormalizeStatus(art.Status)
			if _, known := out.PlanStatusCounts[status]; known && status != "" {
				out.PlanStatusCounts[status]++
			} else {
				out.PlanStatusCounts["other"]++
			}
			if status == "implementing" || status == "blocked" {
				active = append(active, *art)
			}
		case ContentTypeSkill:
			out.Counts.Skills++
		case ContentTypeComputation:
			out.Counts.Computations++
		default:
			out.Counts.Wiki++
		}
	}

	sortNewestFirst(pinned)
	sortNewestFirst(active)
	out.PinnedMemoryTotal = len(pinned)
	out.ActivePlanTotal = len(active)

	out.PinnedMemories = make([]OverviewMemory, 0, min(len(pinned), maxOverviewEntries))
	for _, art := range pinned[:min(len(pinned), maxOverviewEntries)] {
		out.PinnedMemories = append(out.PinnedMemories, OverviewMemory{
			Title:       art.Title,
			Slug:        art.Slug,
			Description: compactDescription(overviewSummary(art)),
			MemoryKind:  NormalizeMemoryKind(art.MemoryKind),
		})
	}

	out.ActivePlans = make([]OverviewPlan, 0, min(len(active), maxOverviewEntries))
	for _, art := range active[:min(len(active), maxOverviewEntries)] {
		tags := art.Tags
		if len(tags) > maxOverviewTags {
			tags = tags[:maxOverviewTags]
		}
		out.ActivePlans = append(out.ActivePlans, OverviewPlan{
			Title:       art.Title,
			Slug:        art.Slug,
			Status:      NormalizeStatus(art.Status),
			Description: compactDescription(overviewSummary(art)),
			Timestamp:   art.Timestamp,
			Tags:        tags,
		})
	}
	return out
}

// sortNewestFirst orders documents by last-modified time, newest first, breaking ties by slug so
// the order is stable across calls.
func sortNewestFirst(docs []Article) {
	sort.SliceStable(docs, func(i, j int) bool {
		if !docs[i].Timestamp.Equal(docs[j].Timestamp) {
			return docs[i].Timestamp.After(docs[j].Timestamp)
		}
		return docs[i].Slug < docs[j].Slug
	})
}

// overviewSummary is a document's one-line summary: its description, or the first line of its
// body when it has none.
func overviewSummary(art Article) string {
	if strings.TrimSpace(art.Description) != "" {
		return art.Description
	}
	return art.ContentPreview
}

// compactDescription collapses whitespace so an entry stays on one line, and truncates to
// maxOverviewDescriptionRunes runes with an ellipsis. It cuts on rune boundaries, never bytes, so
// the result is always valid UTF-8.
func compactDescription(s string) string {
	// Only a bounded prefix can survive the cut, so a huge description costs no more than a
	// short one. Four bytes per rune is the UTF-8 maximum; the slack covers collapsed spaces.
	if limit := maxOverviewDescriptionRunes * 8; len(s) > limit {
		s = s[:limit]
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= maxOverviewDescriptionRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimRight(string(runes[:maxOverviewDescriptionRunes]), " ") + "…"
}

// renderOverviewText renders the prose half of the overview from the same value the structured
// output carries, so the two cannot disagree.
func renderOverviewText(out OverviewOutput, sinceStr string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "NexWiki Knowledge Base Overview (%d articles total)\n", out.TotalArticles)
	fmt.Fprintf(&b, "Documents: %d wiki · %d memories · %d plans · %d skills", out.Counts.Wiki, out.Counts.Memories, out.Counts.Plans, out.Counts.Skills)
	if out.Counts.Computations > 0 {
		fmt.Fprintf(&b, " · %d computations", out.Counts.Computations)
	}
	b.WriteString("\n")

	var byStatus []string
	for _, status := range append(append([]string{}, PlanStatusTags...), "other") {
		if n := out.PlanStatusCounts[status]; n > 0 {
			byStatus = append(byStatus, fmt.Sprintf("%s %d", status, n))
		}
	}
	if len(byStatus) > 0 {
		fmt.Fprintf(&b, "Plans by status: %s\n", strings.Join(byStatus, " · "))
	}
	if out.StatusTags != nil {
		fmt.Fprintf(&b, "Plan statuses: %s\n", strings.Join(out.StatusTags.PlanStatusTags, ", "))
		fmt.Fprintf(&b, "Skill statuses: %s\n", strings.Join(out.StatusTags.SkillStatusTags, ", "))
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "== Pinned Memories (user and feedback: %d%s) ==\n", out.PinnedMemoryTotal, showingSuffix(out.PinnedMemoryTotal, len(out.PinnedMemories)))
	if len(out.PinnedMemories) == 0 {
		b.WriteString("No user or feedback memories.\n")
	}
	for _, m := range out.PinnedMemories {
		fmt.Fprintf(&b, "- %s (%s) <%s>", m.Title, m.Slug, m.MemoryKind)
		if m.Description != "" {
			b.WriteString(" — " + m.Description)
		}
		b.WriteString("\n")
	}
	if out.PinnedMemoryTotal > len(out.PinnedMemories) {
		fmt.Fprintf(&b, "%d more: search_wiki(type: \"memories\") lists every memory.\n", out.PinnedMemoryTotal-len(out.PinnedMemories))
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "== Active Plans (implementing or blocked: %d%s) ==\n", out.ActivePlanTotal, showingSuffix(out.ActivePlanTotal, len(out.ActivePlans)))
	if len(out.ActivePlans) == 0 {
		b.WriteString("No plans are implementing or blocked.\n")
	}
	for _, p := range out.ActivePlans {
		fmt.Fprintf(&b, "- %s (%s) [%s]", p.Title, p.Slug, p.Status)
		if p.Description != "" {
			b.WriteString(" — " + p.Description)
		}
		if len(p.Tags) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(p.Tags, ", "))
		}
		fmt.Fprintf(&b, " (updated %s)\n", p.Timestamp.Format("2006-01-02"))
	}
	if out.ActivePlanTotal > len(out.ActivePlans) {
		fmt.Fprintf(&b, "%d more: search_wiki(type: \"plans\", status: \"implementing\") or (status: \"blocked\").\n", out.ActivePlanTotal-len(out.ActivePlans))
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "== Recent Activity (%d events since %s) ==\n", len(out.RecentActivity), sinceStr)
	if len(out.RecentActivity) == 0 {
		b.WriteString("No activity in this window.\n")
	}
	for _, ev := range out.RecentActivity {
		toolStr := ev.Tool
		if toolStr == "" {
			toolStr = "web-ui"
		}
		fmt.Fprintf(&b, "- %s [%s/%s] %s", ev.Timestamp.Format("2006-01-02 15:04:05"), ev.Source, ev.Action, toolStr)
		if ev.Title != "" || ev.Slug != "" {
			fmt.Fprintf(&b, " → '%s' (%s)", ev.Title, ev.Slug)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	b.WriteString("== Next Steps ==\n")
	b.WriteString(out.NextSteps + "\n")
	return b.String()
}

// showingSuffix notes a capped list in a section heading: ", showing 25" when only some of the
// total are listed, nothing otherwise.
func showingSuffix(total, shown int) string {
	if total > shown {
		return fmt.Sprintf(", showing %d", shown)
	}
	return ""
}

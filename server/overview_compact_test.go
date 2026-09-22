package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// get_wiki_overview is the first call of every agent session, so its cost is paid on every
// session. It used to carry every document's full metadata in structuredContent and a line per
// document in the prose — close to a million characters on a wiki of a few hundred documents.
// These tests pin the replacement: a bounded orientation payload whose size does not depend on
// how many documents the wiki holds.

// overviewSizeBudget is the ceiling for a default get_wiki_overview response, structured and
// prose together. Both capped lists are full in the size test, so this is close to the worst
// case for well-formed documents.
const overviewSizeBudget = 60_000

// seedOverviewBatch adds one batch of 130 documents to srv. Every description is the same length
// and longer than the compact-entry cap, and every title and slug has the same shape, so two
// batches produce entries of identical size — any growth between them is growth in the listing
// itself. The files are written straight to the article directory: the overview reads the
// directory, not the search index, and going through the write path would make seeding a few
// hundred documents dominate the test run.
func seedOverviewBatch(t *testing.T, srv *Server, batch int) {
	t.Helper()
	desc := strings.Repeat("d", 300)
	// Batch 2 is newer than batch 1, and every document within a batch has its own second, so
	// "newest first" is observable.
	base := time.Now().Add(-48 * time.Hour).Add(time.Duration(batch) * time.Hour).Truncate(time.Second)
	n := 0
	write := func(title, typ, status, kind string) {
		t.Helper()
		n++
		slug := Slugify(title)
		ts := base.Add(time.Duration(n) * time.Second)
		a := &Article{
			Type: typ, Title: title, Slug: slug, Description: desc, Source: "test fixture",
			Tags: []string{"fixture"}, Status: status, MemoryKind: kind,
			CreatedAt: ts, Timestamp: ts, Version: 1, Content: "# body\n\nText.",
		}
		if err := os.WriteFile(filepath.Join(srv.Storage.ArticleDir, slug+".md"), []byte(serializeFrontMatter(a)+a.Content), 0644); err != nil {
			t.Fatalf("seeding %q: %v", title, err)
		}
	}

	for i := 0; i < 30; i++ {
		kind := "user"
		if i%2 == 1 {
			kind = "feedback"
		}
		write(fmt.Sprintf("Pinned %d-%03d", batch, i), ContentTypeMemory, "", kind)
	}
	for i := 0; i < 10; i++ {
		write(fmt.Sprintf("Projmem %d-%03d", batch, i), ContentTypeMemory, "", "project")
	}
	for i := 0; i < 30; i++ {
		status := "implementing"
		if i%3 == 0 {
			status = "blocked"
		}
		write(fmt.Sprintf("Active %d-%03d", batch, i), ContentTypePlan, status, "")
	}
	for i := 0; i < 10; i++ {
		write(fmt.Sprintf("Donepl %d-%03d", batch, i), ContentTypePlan, "completed", "")
	}
	for i := 0; i < 50; i++ {
		write(fmt.Sprintf("Wikipg %d-%03d", batch, i), ContentTypeWiki, "", "")
	}
}

// overviewResponseSize is the number of characters a client receives: the prose plus the
// serialized structured payload.
func overviewResponseSize(t *testing.T, resp ToolResponse) int {
	t.Helper()
	if resp.IsError {
		t.Fatalf("get_wiki_overview failed: %s", resp.Content[0].Text)
	}
	encoded, err := json.Marshal(resp.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return len(encoded) + len(resp.Content[0].Text)
}

// TestWikiOverviewOnALargeCorpus seeds the wiki once (seeding dominates the run time) and checks
// counts, caps, selection, schema conformance, and — after doubling the wiki — that the response
// size did not move.
func TestWikiOverviewOnALargeCorpus(t *testing.T) {
	srv := newMCPServer(t)
	seedOverviewBatch(t, srv, 1)

	t.Run("counts and caps", func(t *testing.T) {
		checkOverviewCountsAndCaps(t, srv)
	})
	for _, stats := range []bool{false, true} {
		t.Run(fmt.Sprintf("schema include_stats=%v", stats), func(t *testing.T) {
			resp := toolCall(t, srv, fmt.Sprintf(`{"name":"get_wiki_overview","arguments":{"include_stats":%v}}`, stats))
			assertMatchesOutputSchema(t, resp, toolsByName["get_wiki_overview"].Output)
			encoded, _ := json.Marshal(resp.StructuredContent)
			if strings.Contains(string(encoded), `"articles"`) {
				t.Errorf("structured output still carries the removed articles field")
			}
		})
	}

	first := overviewResponseSize(t, toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{}}`))
	seedOverviewBatch(t, srv, 2)
	second := overviewResponseSize(t, toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{}}`))

	t.Logf("default response: %d chars at 130 documents, %d chars at 260 documents", first, second)

	if second > overviewSizeBudget {
		t.Errorf("a default overview of 260 documents is %d characters, over the %d budget", second, overviewSizeBudget)
	}
	// Doubling the wiki may change only digits in the counts; the capped lists were already full.
	if diff := second - first; diff > 200 || diff < -200 {
		t.Errorf("the overview grew by %d characters when the wiki doubled (%d -> %d); it must not scale with document count", diff, first, second)
	}
}

func checkOverviewCountsAndCaps(t *testing.T, srv *Server) {
	resp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{}}`)
	var out OverviewOutput
	decodeStructured(t, resp, &out)

	// The seeded home page is not a listed document, so it is not counted.
	if out.TotalArticles != 130 {
		t.Errorf("total_articles = %d, want 130", out.TotalArticles)
	}
	want := OverviewCounts{Wiki: 50, Memories: 40, Plans: 40, Skills: 0}
	if out.Counts != want {
		t.Errorf("counts = %+v, want %+v", out.Counts, want)
	}
	if out.PlanStatusCounts["implementing"] != 20 || out.PlanStatusCounts["blocked"] != 10 || out.PlanStatusCounts["completed"] != 10 {
		t.Errorf("plan_status_counts = %v", out.PlanStatusCounts)
	}
	for _, status := range PlanStatusTags {
		if _, ok := out.PlanStatusCounts[status]; !ok {
			t.Errorf("plan_status_counts is missing %q; every vocabulary value is reported, zero included", status)
		}
	}

	if out.PinnedMemoryTotal != 30 || len(out.PinnedMemories) != maxOverviewEntries {
		t.Errorf("pinned memories: total %d, listed %d; want 30 and %d", out.PinnedMemoryTotal, len(out.PinnedMemories), maxOverviewEntries)
	}
	for _, m := range out.PinnedMemories {
		if m.MemoryKind != "user" && m.MemoryKind != "feedback" {
			t.Errorf("pinned memory %s has kind %q", m.Slug, m.MemoryKind)
		}
	}
	if len(out.PinnedMemories) > 0 && out.PinnedMemories[0].Slug != "pinned-1-029" {
		t.Errorf("pinned memories must be newest-updated first; first is %s, want pinned-1-029", out.PinnedMemories[0].Slug)
	}
	if len(out.ActivePlans) > 0 && out.ActivePlans[0].Slug != "active-1-029" {
		t.Errorf("active plans must be newest-updated first; first is %s, want active-1-029", out.ActivePlans[0].Slug)
	}
	if out.ActivePlanTotal != 30 || len(out.ActivePlans) != maxOverviewEntries {
		t.Errorf("active plans: total %d, listed %d; want 30 and %d", out.ActivePlanTotal, len(out.ActivePlans), maxOverviewEntries)
	}
	for i, p := range out.ActivePlans {
		if p.Status != "implementing" && p.Status != "blocked" {
			t.Errorf("active plan %s has status %q", p.Slug, p.Status)
		}
		if i > 0 && p.Timestamp.After(out.ActivePlans[i-1].Timestamp) {
			t.Errorf("active plans are not newest-updated first at %d: %s after %s", i, p.Timestamp, out.ActivePlans[i-1].Timestamp)
		}
	}

	text := resp.Content[0].Text
	for _, want := range []string{
		"NexWiki Knowledge Base Overview (130 articles total)",
		"Documents: 50 wiki · 40 memories · 40 plans · 0 skills",
		"Plans by status: implementing 20 · blocked 10 · completed 10",
		"== Pinned Memories (user and feedback: 30, showing 25) ==",
		"== Active Plans (implementing or blocked: 30, showing 25) ==",
		"search_wiki without a query",
		"search_wiki",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("prose is missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(out.NextSteps, "search_wiki without a query") || !strings.Contains(out.NextSteps, "give search_wiki a query") {
		t.Errorf("next_steps must point at search_wiki's index and its search: %q", out.NextSteps)
	}
	if !strings.Contains(text, out.NextSteps) {
		t.Errorf("the prose must carry the same next_steps as the structured output")
	}
	// No listing of every document: none of the wiki pages, project memories, or finished plans
	// is named anywhere.
	for _, absent := range []string{"wikipg-", "projmem-", "donepl-"} {
		if strings.Contains(text, absent) {
			t.Errorf("prose names %s documents; the overview must not list every document", absent)
		}
	}
}

// TestWikiOverviewSelectsPinnedAndActive checks selection and order on a small corpus: which
// memories are pinned, which plans are active, and that archived memories stay out.
func TestWikiOverviewSelectsPinnedAndActive(t *testing.T) {
	srv := newMCPServer(t)
	str := func(s string) *string { return &s }
	save := func(title, typ string, tags []string, o ArticleOverrides) {
		t.Helper()
		if _, err := srv.Storage.SaveArticleWithOverrides("", title, "# b", "summary of "+title, "src", "", "seed", tags, typ, o); err != nil {
			t.Fatalf("seeding %q: %v", title, err)
		}
	}

	// Timestamps are stored to the second, so the pause is what makes "newest first" observable.
	save("Operator Profile", ContentTypeMemory, nil, ArticleOverrides{MemoryKind: str("user")})
	time.Sleep(1100 * time.Millisecond)
	save("How Chris Reviews", ContentTypeMemory, nil, ArticleOverrides{MemoryKind: str("feedback")})
	save("Deploy Constraint", ContentTypeMemory, nil, ArticleOverrides{MemoryKind: str("project")})
	save("Legacy Unkinded", ContentTypeMemory, nil, ArticleOverrides{})
	save("Retired Feedback", ContentTypeMemory, []string{"archived"}, ArticleOverrides{MemoryKind: str("feedback")})
	save("Draft Plan", ContentTypePlan, nil, ArticleOverrides{Status: str("draft")})
	save("Blocked Plan", ContentTypePlan, []string{"infra"}, ArticleOverrides{Status: str("blocked")})
	time.Sleep(1100 * time.Millisecond)
	save("Running Plan", ContentTypePlan, []string{"nexwiki"}, ArticleOverrides{Status: str("implementing")})
	save("Finished Plan", ContentTypePlan, nil, ArticleOverrides{Status: str("completed")})

	resp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{}}`)
	var out OverviewOutput
	decodeStructured(t, resp, &out)

	var pinned []string
	for _, m := range out.PinnedMemories {
		pinned = append(pinned, m.Slug+"<"+m.MemoryKind+">")
	}
	if got := strings.Join(pinned, ","); got != "how-chris-reviews<feedback>,operator-profile<user>" {
		t.Errorf("pinned memories = %s; want feedback and user only, newest first, archived excluded", got)
	}
	if out.PinnedMemoryTotal != 2 {
		t.Errorf("pinned_memory_total = %d, want 2", out.PinnedMemoryTotal)
	}
	if out.PinnedMemories[0].Description != "summary of How Chris Reviews" {
		t.Errorf("pinned description = %q", out.PinnedMemories[0].Description)
	}

	var active []string
	for _, p := range out.ActivePlans {
		active = append(active, p.Slug+"["+p.Status+"]")
	}
	if got := strings.Join(active, ","); got != "running-plan[implementing],blocked-plan[blocked]" {
		t.Errorf("active plans = %s; want implementing and blocked only, newest first", got)
	}
	if len(out.ActivePlans) == 2 && strings.Join(out.ActivePlans[0].Tags, ",") != "nexwiki" {
		t.Errorf("active plan tags = %v, want [nexwiki]", out.ActivePlans[0].Tags)
	}
	if out.PlanStatusCounts["draft"] != 1 || out.PlanStatusCounts["completed"] != 1 {
		t.Errorf("plan_status_counts = %v", out.PlanStatusCounts)
	}

	text := resp.Content[0].Text
	for _, want := range []string{
		"- How Chris Reviews (how-chris-reviews) <feedback> — summary of How Chris Reviews",
		"- Operator Profile (operator-profile) <user>",
		"- Running Plan (running-plan) [implementing] — summary of Running Plan [nexwiki]",
		"- Blocked Plan (blocked-plan) [blocked]",
		"== Pinned Memories (user and feedback: 2) ==",
		"== Active Plans (implementing or blocked: 2) ==",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("prose is missing %q:\n%s", want, text)
		}
	}
	for _, absent := range []string{"deploy-constraint", "legacy-unkinded", "retired-feedback", "draft-plan", "finished-plan"} {
		if strings.Contains(text, absent) {
			t.Errorf("prose names %s, which is neither pinned nor active:\n%s", absent, text)
		}
	}
}

// TestWikiOverviewEmptySectionsSayNone covers a fresh wiki: the capped lists are empty arrays,
// not null, and the prose says so rather than printing an empty heading.
func TestWikiOverviewEmptySectionsSayNone(t *testing.T) {
	srv := newMCPServer(t)
	resp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{}}`)
	var out OverviewOutput
	decodeStructured(t, resp, &out)
	if out.PinnedMemories == nil || out.ActivePlans == nil {
		t.Errorf("empty lists must serialize as [], got %+v / %+v", out.PinnedMemories, out.ActivePlans)
	}
	text := resp.Content[0].Text
	for _, want := range []string{"No user or feedback memories", "No plans are implementing or blocked"} {
		if !strings.Contains(text, want) {
			t.Errorf("prose is missing %q:\n%s", want, text)
		}
	}
}

func TestCompactDescription(t *testing.T) {
	short := "A short summary."
	if got := compactDescription(short); got != short {
		t.Errorf("a short description must be unchanged, got %q", got)
	}

	exact := strings.Repeat("x", maxOverviewDescriptionRunes)
	if got := compactDescription(exact); got != exact {
		t.Errorf("a description of exactly the cap must be unchanged")
	}

	// Multi-byte runes: a byte-based cut would split one and produce invalid UTF-8.
	long := strings.Repeat("é日", 400)
	got := compactDescription(long)
	if !utf8.ValidString(got) {
		t.Fatalf("truncation produced invalid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n != maxOverviewDescriptionRunes+1 {
		t.Errorf("truncated description has %d runes, want %d plus the ellipsis", n, maxOverviewDescriptionRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated description must end with an ellipsis: %q", got)
	}

	if got := compactDescription("  line one\nline two  "); got != "line one line two" {
		t.Errorf("newlines must collapse so one entry stays one line, got %q", got)
	}
}

// TestWikiOverviewTruncatesPathologicalDescriptions: one enormous description must not blow up
// the payload.
func TestWikiOverviewTruncatesPathologicalDescriptions(t *testing.T) {
	srv := newMCPServer(t)
	huge := strings.Repeat("ü", 100_000)
	kind := "user"
	if _, err := srv.Storage.SaveArticleWithOverrides("", "Huge Memory", "# b", huge, "src", "", "seed", nil, ContentTypeMemory, ArticleOverrides{MemoryKind: &kind}); err != nil {
		t.Fatal(err)
	}
	resp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{}}`)
	var out OverviewOutput
	decodeStructured(t, resp, &out)
	if len(out.PinnedMemories) != 1 {
		t.Fatalf("want the one pinned memory, got %+v", out.PinnedMemories)
	}
	if n := utf8.RuneCountInString(out.PinnedMemories[0].Description); n > maxOverviewDescriptionRunes+1 {
		t.Errorf("description has %d runes; it must be truncated", n)
	}
	if size := overviewResponseSize(t, resp); size > 20_000 {
		t.Errorf("a single huge description produced a %d-character overview", size)
	}
}

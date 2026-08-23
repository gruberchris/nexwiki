package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSignificantTitleTokens(t *testing.T) {
	got := significantTitleTokens("The NexWiki Article Format & Template")
	for _, dropped := range []string{"the", "nexwiki"} {
		if got[dropped] {
			t.Errorf("%q should have been dropped as a stop word", dropped)
		}
	}
	for _, kept := range []string{"article", "format", "template"} {
		if !got[kept] {
			t.Errorf("%q should have been kept", kept)
		}
	}
}

func TestTitleOverlapUsesTheSmallerSet(t *testing.T) {
	short := significantTitleTokens("SQL Dialect Article Format")
	long := significantTitleTokens("SQL Dialect Article Format & Template Reference Guide For Everyone")

	// Measured against the smaller set, the short title is fully contained in the long one.
	// Against the union it would score far lower, punishing the longer title for being descriptive.
	if got := titleOverlap(short, long); got != 1.0 {
		t.Errorf("overlap = %.2f, want 1.00 — containment must score as a full match", got)
	}
	if got := titleOverlap(significantTitleTokens("Docker Deployment Notes"),
		significantTitleTokens("Bleve Search Index Tuning")); got != 0 {
		t.Errorf("overlap = %.2f, want 0 for unrelated titles", got)
	}
}

func TestFindDuplicateMemoriesRespectsScope(t *testing.T) {
	memories := []Article{
		{Slug: "a", Title: "Deployment Notes And Runbook", Type: ContentTypeMemory, Tags: []string{"memory-docker"}},
		// Same title shape, different scope: separate documents by design, not duplicates.
		{Slug: "b", Title: "Deployment Notes And Runbook", Type: ContentTypeMemory, Tags: []string{"memory-nexwiki"}},
		{Slug: "c", Title: "Deployment Notes Runbook Revised", Type: ContentTypeMemory, Tags: []string{"memory-docker"}},
		{Slug: "d", Title: "Bleve Search Index Tuning", Type: ContentTypeMemory, Tags: []string{"memory-docker"}},
	}

	pairs := findDuplicateMemories(memories, nil)
	if len(pairs) != 1 {
		t.Fatalf("got %d pairs, want exactly 1 (a↔c within memory-docker); pairs=%+v", len(pairs), pairs)
	}
	if pairs[0].Slug != "a" || pairs[0].OtherSlug != "c" {
		t.Errorf("paired %s↔%s, want a↔c", pairs[0].Slug, pairs[0].OtherSlug)
	}
	if pairs[0].Scope != "docker" {
		t.Errorf("scope = %q, want %q", pairs[0].Scope, "docker")
	}
}

// TestCrossLinkedMemoriesAreNotReportedAsDuplicates pins the suppression the real corpus argued
// for. The only pair the check found on the live wiki was the two format-template memories — and
// one of them ends with "See also: [[sql-dialect-article-format-template]]". An author who has
// linked two documents together has already decided to keep them separate.
func TestCrossLinkedMemoriesAreNotReportedAsDuplicates(t *testing.T) {
	memories := []Article{
		{Slug: "pl-format", Title: "Programming Language Article Format Template", Type: ContentTypeMemory, Tags: []string{"memory-rules"}},
		{Slug: "sql-format", Title: "SQL Dialect Article Format Template", Type: ContentTypeMemory, Tags: []string{"memory-rules"}},
	}

	// Without the link they pair up...
	if pairs := findDuplicateMemories(memories, nil); len(pairs) != 1 {
		t.Fatalf("got %d pairs without a cross-link, want 1 — the test premise no longer holds", len(pairs))
	}

	// ...and with it they do not.
	outbound := map[string][]LinkRef{
		"pl-format": {{Target: "sql-dialect-article-format-template", Slug: "sql-format"}},
	}
	if pairs := findDuplicateMemories(memories, outbound); len(pairs) != 0 {
		t.Errorf("got %d pairs, want 0 — documents that reference each other are a deliberate pair", len(pairs))
	}
}

// TestFindDuplicateMemoriesSkipsHugeScopes keeps the only O(n²) check in the tool from dominating
// its runtime on a large corpus — §13.8 put 10,000 documents within reach.
func TestFindDuplicateMemoriesSkipsHugeScopes(t *testing.T) {
	memories := make([]Article, maxDuplicateScopeSize+1)
	for i := range memories {
		memories[i] = Article{
			Slug: string(rune('a'+i%26)) + string(rune('a'+i/26)), Title: "Identical Deployment Runbook Notes",
			Type: ContentTypeMemory, Tags: []string{"memory-huge"},
		}
	}
	if pairs := findDuplicateMemories(memories, nil); len(pairs) != 0 {
		t.Errorf("got %d pairs, want 0 — a scope over the cap must be skipped, not compared", len(pairs))
	}
}

// TestColdMemoryScanSkipsWhenTheLogIsTooYoung is the guard that keeps this check honest.
//
// Recency comes from the activity log. On a fresh install, or after archive pruning, the log can be
// younger than the threshold — and then every memory looks untouched. Reporting all of them would
// not be a noisy check, it would be a false one.
func TestColdMemoryScanSkipsWhenTheLogIsTooYoung(t *testing.T) {
	dir := t.TempDir()
	al, err := OpenActivityLog(dir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })

	// A log that only reaches back a week.
	if err := al.Append(LogEvent{Timestamp: time.Now().AddDate(0, 0, -7), Action: "read", Slug: "something"}); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	memories := []Article{{Slug: "ancient", Title: "Ancient Memory", Type: ContentTypeMemory,
		Timestamp: time.Now().AddDate(0, 0, -400)}}

	got := scanColdMemories(ActivityLogPath(dir), memories, 90)
	if got.Ran {
		t.Error("the cold scan ran against a 7-day log with a 90-day threshold; every memory would be reported")
	}
	if len(got.Findings) != 0 {
		t.Errorf("got %d findings, want 0 when the scan cannot run", len(got.Findings))
	}
}

func TestColdMemoryScanEmptyLog(t *testing.T) {
	dir := t.TempDir()
	got := scanColdMemories(ActivityLogPath(dir), []Article{
		{Slug: "m", Title: "M", Type: ContentTypeMemory, Timestamp: time.Now().AddDate(0, 0, -400)},
	}, 90)
	if got.Ran || len(got.Findings) != 0 {
		t.Errorf("got %+v, want a skipped scan when there is no activity log at all", got)
	}
}

func TestColdMemoryScanFindsUntouchedMemories(t *testing.T) {
	dir := t.TempDir()
	al, err := OpenActivityLog(dir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })

	old := time.Now().AddDate(0, 0, -200)
	recent := time.Now().AddDate(0, 0, -3)

	for _, ev := range []LogEvent{
		{Timestamp: old, Action: "create", Slug: "cold-one"},   // long ago, never revisited
		{Timestamp: old, Action: "create", Slug: "kept-warm"},  // ...
		{Timestamp: recent, Action: "read", Slug: "kept-warm"}, // ...but recently read
	} {
		if err := al.Append(ev); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	memories := []Article{
		{Slug: "cold-one", Title: "Cold One", Type: ContentTypeMemory, Timestamp: old},
		{Slug: "kept-warm", Title: "Kept Warm", Type: ContentTypeMemory, Timestamp: old},
		// Written recently, no activity recorded: new, not cold.
		{Slug: "brand-new", Title: "Brand New", Type: ContentTypeMemory, Timestamp: recent},
	}

	got := scanColdMemories(ActivityLogPath(dir), memories, 90)
	if !got.Ran {
		t.Fatal("the scan should have run: the log reaches back 200 days")
	}
	if len(got.Findings) != 1 || got.Findings[0].Slug != "cold-one" {
		t.Errorf("findings = %+v, want only cold-one", got.Findings)
	}
}

// TestReadsKeepAMemoryWarm pins the design decision that a read counts as a touch. A memory the
// agent keeps consulting is alive even if nobody has edited it in a year — that is what a good
// memory looks like, and reporting it would train the user to ignore the check.
func TestReadsKeepAMemoryWarm(t *testing.T) {
	dir := t.TempDir()
	al, err := OpenActivityLog(dir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })

	old := time.Now().AddDate(0, 0, -300)
	for _, ev := range []LogEvent{
		{Timestamp: old, Action: "create", Slug: "consulted"},
		{Timestamp: time.Now().AddDate(0, 0, -2), Action: "read", Tool: "read_article", Slug: "consulted"},
	} {
		if err := al.Append(ev); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	got := scanColdMemories(ActivityLogPath(dir), []Article{
		{Slug: "consulted", Title: "Consulted Often", Type: ContentTypeMemory, Timestamp: old},
	}, 90)
	if len(got.Findings) != 0 {
		t.Errorf("a memory read two days ago was reported cold: %+v", got.Findings)
	}
}

// TestParkedPlansAreNotStale covers the gap the live corpus exposed: two of its five stale plans
// were product bets this code review deliberately deferred, so they would report forever.
func TestParkedPlansAreNotStale(t *testing.T) {
	srv := newTestServer(t)

	for _, tc := range []struct {
		title string
		tags  []string
	}{
		{"Genuinely Abandoned Plan", []string{"project", "implementing"}},
		{"Deliberately Parked Plan", []string{"project", "parked"}},
		{"Finished Plan", []string{"project", "completed"}},
	} {
		art, err := srv.Storage.SaveArticle("", tc.title, "# body", "d", "", "", "seed", tc.tags, ContentTypePlan)
		if err != nil {
			t.Fatalf("SaveArticle(%q) failed: %v", tc.title, err)
		}
		ageDocument(t, srv, art.Slug, 200)
	}

	raw, rpcErr := srv.toolWikiHealth(json.RawMessage(`{}`))
	if rpcErr != nil {
		t.Fatalf("wiki_health failed: %v", rpcErr)
	}
	out := raw.(ToolResponse).StructuredContent.(HealthOutput)

	if out.StalePlanCount != 1 {
		t.Errorf("stale_plan_count = %d, want 1 (only the abandoned plan); stale=%+v",
			out.StalePlanCount, out.StalePlans)
	}
	if len(out.StalePlans) == 1 && out.StalePlans[0].Slug != "genuinely-abandoned-plan" {
		t.Errorf("stale plan is %q, want genuinely-abandoned-plan", out.StalePlans[0].Slug)
	}
	if out.ParkedPlanCount != 1 {
		t.Errorf("parked_plan_count = %d, want 1 — parked plans are counted, not hidden", out.ParkedPlanCount)
	}
}

// TestWikiHealthReportsHygieneHonestly checks the report says *why* the cold scan was skipped
// rather than implying a clean bill of health, which is the difference between a quiet check and a
// misleading one.
func TestWikiHealthReportsHygieneHonestly(t *testing.T) {
	srv := newTestServer(t)
	if _, err := srv.Storage.SaveArticle("", "Some Memory", "# body", "d", "src", "", "seed",
		[]string{"memory-project"}, ContentTypeMemory); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	raw, rpcErr := srv.toolWikiHealth(json.RawMessage(`{}`))
	if rpcErr != nil {
		t.Fatalf("wiki_health failed: %v", rpcErr)
	}
	resp := raw.(ToolResponse)
	out := resp.StructuredContent.(HealthOutput)

	if out.ColdDays != defaultColdMemoryDays {
		t.Errorf("cold_days = %d, want the %d-day default", out.ColdDays, defaultColdMemoryDays)
	}
	if out.ColdMemoryScanRan {
		t.Error("the cold scan claims to have run against a wiki with no activity log")
	}
	if out.ColdMemorySkipped == "" {
		t.Error("the scan was skipped but gave no reason")
	}
	if !strings.Contains(resp.Content[0].Text, "Cold memories: not checked") {
		t.Errorf("prose does not disclose the skipped check:\n%s", resp.Content[0].Text)
	}
}

// TestColdDaysArgumentIsHonored confirms the knob reaches the output, mirroring how stale_days is
// verified — §6.5 records that a threshold nobody can move is a threshold nobody trusts.
func TestColdDaysArgumentIsHonored(t *testing.T) {
	srv := newTestServer(t)
	raw, rpcErr := srv.toolWikiHealth(json.RawMessage(`{"cold_days": 7}`))
	if rpcErr != nil {
		t.Fatalf("wiki_health failed: %v", rpcErr)
	}
	if out := raw.(ToolResponse).StructuredContent.(HealthOutput); out.ColdDays != 7 {
		t.Errorf("cold_days = %d, want 7", out.ColdDays)
	}
}

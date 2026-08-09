package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ageDocument backdates a document's modified time so a stale-plan check has something to find
// without the test sleeping. It rewrites the file's mtime as well, because the metadata cache is
// validated by mtime and would otherwise serve the pre-backdate copy.
func ageDocument(t *testing.T, srv *Server, slug string, days int) {
	t.Helper()
	art, err := srv.Storage.GetArticle(slug)
	if err != nil {
		t.Fatalf("GetArticle(%q) failed: %v", slug, err)
	}
	art.Timestamp = time.Now().AddDate(0, 0, -days)

	// Articles live flat in ArticleDir; the type directories exist only in OKF bundles.
	path := filepath.Join(srv.Storage.ArticleDir, slug+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s failed: %v", path, err)
	}
	stamp := art.Timestamp.UTC().Format(time.RFC3339)
	updated := replaceFrontMatterTimestamp(string(data), stamp)
	if updated == string(data) {
		t.Fatalf("could not backdate %s; front matter shape changed?\n%s", slug, firstLines(string(data), 12))
	}
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		t.Fatalf("writing %s failed: %v", path, err)
	}
	old := time.Now().AddDate(0, 0, -days)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("chtimes on %s failed: %v", path, err)
	}
}

func replaceFrontMatterTimestamp(doc, stamp string) string {
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "timestamp:") {
			lines[i] = "timestamp: " + stamp
			return strings.Join(lines, "\n")
		}
	}
	return doc
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// seedHealthFixture builds a wiki holding exactly one of each finding the tool reports, plus a
// near-miss for each so the test proves the checks discriminate rather than merely fire.
func seedHealthFixture(t *testing.T) *Server {
	t.Helper()
	srv := newMCPServer(t)

	must := func(toolJSON string) {
		t.Helper()
		if resp := toolCall(t, srv, toolJSON); resp.IsError {
			t.Fatalf("fixture setup failed: %s", resp.Content[0].Text)
		}
	}

	// A hub linking to one real page and one that was never written.
	must(`{"name":"create_wiki_article","arguments":{"title":"Hub","content":"# Hub\n\nSee [[Linked Page]] and [[Never Written]].","edit_summary":"Initial"}}`)
	// Linked from the hub: not an orphan.
	must(`{"name":"create_wiki_article","arguments":{"title":"Linked Page","content":"# Linked\n\nBody.","edit_summary":"Initial"}}`)
	// Nothing links here: an orphan.
	must(`{"name":"create_wiki_article","arguments":{"title":"Lonely Page","content":"# Lonely\n\nNobody links here.","edit_summary":"Initial"}}`)
	// Links only to itself, which must not rescue it from the orphan list.
	must(`{"name":"create_wiki_article","arguments":{"title":"Self Ref","content":"# Self\n\nSee [[Self Ref]].","edit_summary":"Initial"}}`)
	// Archived and unlinked: deliberately out of scope.
	must(`{"name":"create_wiki_article","arguments":{"title":"Retired Page","content":"# Retired\n\nOld.","tags":["archived"],"edit_summary":"Initial"}}`)
	// A memory with provenance, and one without.
	must(`{"name":"create_agent_memory","arguments":{"title":"Sourced Fact","content":"# Fact","memory_type":"nexwiki","description":"has provenance","source":"design review"}}`)
	must(`{"name":"create_agent_memory","arguments":{"title":"Floating Fact","content":"# Fact","memory_type":"nexwiki","description":"no provenance"}}`)
	// Plans: one stale, one recent, one finished-but-old, one old with no status tag.
	// create_agent_plan takes no tags argument — it derives only the project-context tag — so
	// status tags are applied the way an agent would, with update_article_tags.
	must(`{"name":"create_agent_plan","arguments":{"title":"Stalled Plan","content":"# Plan","project_context":"nexwiki","description":"in flight"}}`)
	must(`{"name":"create_agent_plan","arguments":{"title":"Fresh Plan","content":"# Plan","project_context":"nexwiki","description":"in flight"}}`)
	must(`{"name":"create_agent_plan","arguments":{"title":"Finished Plan","content":"# Plan","project_context":"nexwiki","description":"done"}}`)
	must(`{"name":"create_agent_plan","arguments":{"title":"Untagged Plan","content":"# Plan","project_context":"nexwiki","description":"old and never marked finished"}}`)
	must(`{"name":"create_agent_plan","arguments":{"title":"Superseded Plan","content":"# Plan","project_context":"nexwiki","description":"replaced by another plan"}}`)

	must(`{"name":"update_article_tags","arguments":{"slug":"stalled-plan","tags":["nexwiki","wip"]}}`)
	must(`{"name":"update_article_tags","arguments":{"slug":"fresh-plan","tags":["nexwiki","wip"]}}`)
	must(`{"name":"update_article_tags","arguments":{"slug":"finished-plan","tags":["nexwiki","wip","completed"]}}`)
	must(`{"name":"update_article_tags","arguments":{"slug":"superseded-plan","tags":["nexwiki","superseded"]}}`)

	// Backdate last, so the tag edits above do not refresh the timestamps being aged.
	for _, slug := range []string{"stalled-plan", "finished-plan", "untagged-plan", "superseded-plan"} {
		ageDocument(t, srv, slug, 90)
	}

	return srv
}

func healthReport(t *testing.T, srv *Server, args string) HealthOutput {
	t.Helper()
	var out HealthOutput
	decodeStructured(t, toolCall(t, srv, `{"name":"wiki_health","arguments":`+args+`}`), &out)
	return out
}

func findingSlugs(findings []HealthFinding) map[string]bool {
	set := map[string]bool{}
	for _, f := range findings {
		set[f.Slug] = true
	}
	return set
}

// TestWikiHealthFindsEachCategory is the tool's core contract: each check catches its own case and
// nothing else. The near-misses matter more than the hits — a check that fires on everything is
// noise an agent learns to skip.
func TestWikiHealthFindsEachCategory(t *testing.T) {
	srv := seedHealthFixture(t)
	out := healthReport(t, srv, `{}`)

	t.Run("orphans", func(t *testing.T) {
		got := findingSlugs(out.Orphans)
		for _, want := range []string{"lonely-page", "self-ref"} {
			if !got[want] {
				t.Errorf("%q should be reported as an orphan", want)
			}
		}
		// A self-link is not an inbound link; a page that only links to itself is still adrift.
		for _, notWant := range []string{"linked-page", "home", "retired-page"} {
			if got[notWant] {
				t.Errorf("%q must not be reported as an orphan", notWant)
			}
		}
		// Agent documents are reached through their list tools, search facets, and the context
		// overview. Nobody WikiLinks a memory, so flagging every one of them is pure noise —
		// measured at 27 of 70 findings on the real corpus before this was restricted.
		for _, doc := range out.Orphans {
			if doc.Type != ContentTypeWiki {
				t.Errorf("orphan detection should cover wiki articles only, got a %s: %s", doc.Type, doc.Slug)
			}
		}
	})

	t.Run("broken links", func(t *testing.T) {
		var found bool
		for _, bl := range out.BrokenLinks {
			if bl.FromSlug == "hub" && bl.TargetSlug == "never-written" {
				found = true
				if bl.Target != "Never Written" {
					t.Errorf("broken link should name the raw target as written, got %q", bl.Target)
				}
			}
			if bl.TargetSlug == "linked-page" {
				t.Error("a link to an existing page must not be reported as broken")
			}
		}
		if !found {
			t.Errorf("the seeded broken link is missing: %+v", out.BrokenLinks)
		}
	})

	t.Run("memories missing provenance", func(t *testing.T) {
		got := findingSlugs(out.UnsourcedMemory)
		if !got["floating-fact"] {
			t.Error("a memory with no source should be reported")
		}
		if got["sourced-fact"] {
			t.Error("a memory with a source must not be reported")
		}
	})

	t.Run("stale plans", func(t *testing.T) {
		got := findingSlugs(out.StalePlans)
		if !got["stalled-plan"] {
			t.Error("a 90-day-old 'wip' plan should be reported as stale")
		}
		// An in-flight tag is not required. Requiring one made the check incapable of firing on
		// the real corpus, where plans carry a project tag and nothing else.
		if !got["untagged-plan"] {
			t.Error("an old plan never marked finished is stale even with no status tag")
		}
		if got["fresh-plan"] {
			t.Error("a plan edited today is not stale")
		}
		// A terminal tag wins: nagging about work the user has marked done is worse than
		// staying quiet.
		if got["finished-plan"] {
			t.Error("a plan tagged 'completed' must never be stale, however old")
		}
		if got["superseded-plan"] {
			t.Error("a superseded plan is not waiting on anyone either")
		}
	})
}

// TestWikiHealthStaleDaysIsHonoured pins that the threshold is a real argument, not decoration.
func TestWikiHealthStaleDaysIsHonoured(t *testing.T) {
	srv := seedHealthFixture(t)

	if slugs := findingSlugs(healthReport(t, srv, `{"stale_days":365}`).StalePlans); slugs["stalled-plan"] {
		t.Error("a 90-day-old plan is not stale against a 365-day threshold")
	}
	if slugs := findingSlugs(healthReport(t, srv, `{"stale_days":1}`).StalePlans); !slugs["stalled-plan"] {
		t.Error("a 90-day-old plan is stale against a 1-day threshold")
	}
	// A nonsensical threshold falls back to the default rather than reporting every plan.
	if out := healthReport(t, srv, `{"stale_days":-5}`); out.StaleDays != defaultStaleDays {
		t.Errorf("negative stale_days should fall back to %d, got %d", defaultStaleDays, out.StaleDays)
	}
}

// TestWikiHealthCountsSurviveTruncation pins the split between counts and lists. A wiki with 400
// orphans has to be able to say so without returning 400 items and burying every other category.
func TestWikiHealthCountsSurviveTruncation(t *testing.T) {
	srv := seedHealthFixture(t)

	full := healthReport(t, srv, `{}`)
	if full.OrphanCount < 2 {
		t.Fatalf("fixture should produce at least 2 orphans, got %d", full.OrphanCount)
	}
	if full.Truncated {
		t.Error("the fixture is well under the default limit and must not report truncation")
	}

	capped := healthReport(t, srv, `{"limit":1}`)
	if capped.OrphanCount != full.OrphanCount {
		t.Errorf("count changed under a limit: %d then %d", full.OrphanCount, capped.OrphanCount)
	}
	if len(capped.Orphans) != 1 {
		t.Errorf("limit:1 should return 1 orphan, got %d", len(capped.Orphans))
	}
	if !capped.Truncated {
		t.Error("a capped category must set truncated")
	}
	if !strings.Contains(capped.Content(), "more; raise 'limit'") {
		t.Error("the prose should say the list was cut short")
	}
}

// Content re-renders the prose from a structured report, so the truncation test can assert on the
// text without a second tool call.
func (out HealthOutput) Content() string { return renderHealthReport(out) }

// TestWikiHealthProseMatchesStructure guards the rule every structured tool here follows: the two
// halves are rendered from one value and cannot disagree.
func TestWikiHealthProseMatchesStructure(t *testing.T) {
	srv := seedHealthFixture(t)
	resp := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)

	var out HealthOutput
	decodeStructured(t, resp, &out)
	text := resp.Content[0].Text

	for _, want := range []string{
		"Orphan pages: " + itoa(out.OrphanCount),
		"Broken WikiLinks: " + itoa(out.BrokenLinkCount),
		"Memories with no source: " + itoa(out.UnsourcedCount),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("prose is missing %q:\n%s", want, text)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestWikiHealthOnAHealthyWikiSaysSo pins the empty case. A maintenance tool that answers a clean
// wiki with a wall of zeros trains an agent to stop reading it.
func TestWikiHealthOnAHealthyWikiSaysSo(t *testing.T) {
	srv := newMCPServer(t)

	// The seeded home page ships with example WikiLinks to pages that do not exist yet, so make
	// them exist before asserting the wiki is clean.
	for _, title := range []string{"Guides", "Markdown Playground"} {
		if resp := toolCall(t, srv, `{"name":"create_wiki_article","arguments":{"title":"`+title+`","content":"# `+title+`\n\nBody.","edit_summary":"Initial"}}`); resp.IsError {
			t.Fatalf("setup failed: %s", resp.Content[0].Text)
		}
	}

	resp := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
	var out HealthOutput
	decodeStructured(t, resp, &out)

	if out.BrokenLinkCount != 0 {
		t.Errorf("expected no broken links, got %+v", out.BrokenLinks)
	}
	if out.OrphanCount != 0 {
		t.Errorf("pages linked from home are not orphans, got %+v", out.Orphans)
	}
	if !strings.Contains(resp.Content[0].Text, "the wiki is healthy") {
		t.Errorf("a clean wiki should say so:\n%s", resp.Content[0].Text)
	}
}

// TestScanLinkGraphIgnoresCodeFences pins the rule the whole link layer depends on: bracketed text
// inside code is code, not a link. Without it, every C++ [[nodiscard]] in the wiki becomes a
// broken link and the health report is unusable on a programming wiki.
func TestScanLinkGraphIgnoresCodeFences(t *testing.T) {
	srv := newMCPServer(t)

	content := "# Attributes\n\n" +
		"```cpp\n[[nodiscard]] int f();\n```\n\n" +
		"Inline `[[not a link]]` too, but [[Real Target]] is one.\n"
	args, err := json.Marshal(map[string]string{
		"title": "Attributes", "content": content, "edit_summary": "Initial",
	})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if resp := toolCall(t, srv, `{"name":"create_wiki_article","arguments":`+string(args)+`}`); resp.IsError {
		t.Fatalf("setup failed: %s", resp.Content[0].Text)
	}

	graph, err := srv.Storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	for _, ref := range graph.Outbound["attributes"] {
		if ref.Slug == "nodiscard" || ref.Slug == "not-a-link" {
			t.Errorf("bracketed code was treated as a WikiLink: %+v", ref)
		}
	}
	var sawReal bool
	for _, ref := range graph.Outbound["attributes"] {
		if ref.Slug == "real-target" {
			sawReal = true
		}
	}
	if !sawReal {
		t.Errorf("the genuine WikiLink was lost: %+v", graph.Outbound["attributes"])
	}
}

// TestScanLinkGraphIncludesHome pins a real gap the shared scan closes. get_wiki_statistics used to
// build its document set from ListArticles, which excludes home — so links written on the home
// page, the page a user is most likely to link from, were never scanned at all.
func TestScanLinkGraphIncludesHome(t *testing.T) {
	srv := newMCPServer(t)

	graph, err := srv.Storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	if _, ok := graph.Meta["home"]; !ok {
		t.Fatal("home must be part of the graph; it links to much of the wiki")
	}
	// The seeded home page links to [[Guides]] and [[Markdown Playground]], neither of which
	// exists on a fresh wiki.
	if len(graph.Broken) == 0 {
		t.Error("home's example WikiLinks should be reported as broken on a fresh wiki")
	}
	for _, bl := range graph.Broken {
		if bl.FromSlug != "home" {
			t.Errorf("unexpected broken link source on a fresh wiki: %+v", bl)
		}
	}
}

// TestScanLinkGraphIsDeterministic pins stable ordering. Directory walk order is
// filesystem-dependent, and an agent diffing two health reports should see only real changes.
func TestScanLinkGraphIsDeterministic(t *testing.T) {
	srv := seedHealthFixture(t)

	first, err := srv.Storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	second, err := srv.Storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	if len(first.Broken) != len(second.Broken) {
		t.Fatalf("broken-link count changed between runs: %d then %d", len(first.Broken), len(second.Broken))
	}
	for i := range first.Broken {
		if first.Broken[i] != second.Broken[i] {
			t.Errorf("broken link %d differs between runs: %+v vs %+v", i, first.Broken[i], second.Broken[i])
		}
	}
}

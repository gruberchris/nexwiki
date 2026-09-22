package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "timestamp:") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = indent + "timestamp: " + stamp
		} else if strings.HasPrefix(trimmed, "at:") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = indent + "at: " + stamp
		}
	}
	return strings.Join(lines, "\n")
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
	must(`{"name":"save_article","arguments":{"title":"Hub","content":"# Hub\n\nSee [[Linked Page]] and [[Never Written]].","edit_summary":"Initial"}}`)
	// Linked from the hub: not an orphan.
	must(`{"name":"save_article","arguments":{"title":"Linked Page","content":"# Linked\n\nBody.","edit_summary":"Initial"}}`)
	// Nothing links here: an orphan.
	must(`{"name":"save_article","arguments":{"title":"Lonely Page","content":"# Lonely\n\nNobody links here.","edit_summary":"Initial"}}`)
	// Links only to itself, which must not rescue it from the orphan list.
	must(`{"name":"save_article","arguments":{"title":"Self Ref","content":"# Self\n\nSee [[Self Ref]].","edit_summary":"Initial"}}`)
	// Archived and unlinked: deliberately out of scope.
	must(`{"name":"save_article","arguments":{"title":"Retired Page","content":"# Retired\n\nOld.","tags":["archived"],"edit_summary":"Initial"}}`)
	// A memory with provenance, and one without.
	must(`{"name":"save_article","arguments":{"type":"AI-Agent-Memory","memory_kind":"project","title":"Sourced Fact","content":"# Fact","memory_type":"nexwiki","description":"has provenance","source":"design review"}}`)
	// The unsourced one is seeded through storage rather than the tool, because
	// create_agent_memory now refuses a memory with no source. That is the point of the gate, and
	// it makes this fixture the shape it is actually testing: a memory that predates it. The
	// health check must keep reporting those — closing the intake does not rewrite history.
	if _, err := srv.Storage.SaveArticle("", "Floating Fact", "# Fact", "no provenance", "", "", "seed",
		[]string{MemoryScopeTagPrefix + "nexwiki"}, ContentTypeMemory); err != nil {
		t.Fatalf("seeding the unsourced memory failed: %v", err)
	}
	// Plans: one stale, one recent, one finished-but-old, one old still in its default draft.
	must(`{"name":"save_article","arguments":{"type":"AI-Agent-Plan","title":"Stalled Plan","content":"# Plan","project_context":"nexwiki","description":"in flight","status":"implementing"}}`)
	must(`{"name":"save_article","arguments":{"type":"AI-Agent-Plan","title":"Fresh Plan","content":"# Plan","project_context":"nexwiki","description":"in flight","status":"implementing"}}`)
	must(`{"name":"save_article","arguments":{"type":"AI-Agent-Plan","title":"Finished Plan","content":"# Plan","project_context":"nexwiki","description":"done","status":"completed"}}`)
	must(`{"name":"save_article","arguments":{"type":"AI-Agent-Plan","title":"Untagged Plan","content":"# Plan","project_context":"nexwiki","description":"old and never marked finished"}}`)
	must(`{"name":"save_article","arguments":{"type":"AI-Agent-Plan","title":"Superseded Plan","content":"# Plan","project_context":"nexwiki","description":"replaced by another plan","status":"superseded"}}`)

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
		"Broken internal links: " + itoa(out.BrokenLinkCount),
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

// newHealthyWikiServer returns a server whose wiki_health report has nothing to flag.
func newHealthyWikiServer(t *testing.T) *Server {
	t.Helper()
	srv := newMCPServer(t)

	// The seeded home page ships with example WikiLinks to pages that do not exist yet, so make
	// them exist before asserting the wiki is clean.
	for _, title := range []string{"Guides", "Markdown Playground"} {
		if resp := toolCall(t, srv, `{"name":"save_article","arguments":{"title":"`+title+`","content":"# `+title+`\n\nBody.","edit_summary":"Initial"}}`); resp.IsError {
			t.Fatalf("setup failed: %s", resp.Content[0].Text)
		}
	}
	return srv
}

// TestWikiHealthOnAHealthyWikiSaysSo pins the empty case. A maintenance tool that answers a clean
// wiki with a wall of zeros trains an agent to stop reading it.
func TestWikiHealthOnAHealthyWikiSaysSo(t *testing.T) {
	srv := newHealthyWikiServer(t)

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
		"Inline `[[not a link]]` too, but [[Real Target]] is one.\n\n" +
		// The same rule has to hold for the Markdown form, or the two format-template articles in
		// the real corpus — which document the convention with a fenced [Title](/articles/slug)
		// example — would each report a broken link to a page called "slug".
		"```markdown\n[Article Title](/articles/slug)\n```\n\n" +
		"And a real one: [Second Target](/articles/second-target).\n"
	args, err := json.Marshal(map[string]string{
		"title": "Attributes", "content": content, "edit_summary": "Initial",
	})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if resp := toolCall(t, srv, `{"name":"save_article","arguments":`+string(args)+`}`); resp.IsError {
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

// TestScanLinkGraphCountsMarkdownLinks is the §3.21 regression test. The link graph read only
// [[WikiLinks]], but the corpus prefers absolute Markdown links, so 84% of real internal links were invisible: broken-link detection reported 0 against
// 26, orphan detection called 44 of 84 documents orphans, and get_backlinks under-reported
// inbound references before a rename or delete. Revert ExtractLinkRefs' Markdown pass and all
// three assertions below fail.
func TestScanLinkGraphCountsMarkdownLinks(t *testing.T) {
	srv := newMCPServer(t)

	seed := func(title, content string) {
		if _, err := srv.Storage.SaveArticle("", title, content, "", "", "", "seed", nil, ""); err != nil {
			t.Fatalf("seeding %q failed: %v", title, err)
		}
	}
	seed("Target Page", "# Target")
	seed("Linker", "Only a Markdown link: [the target](/articles/target-page).")
	seed("Dangler", "Points nowhere: [gone](/articles/no-such-page).")

	graph, err := srv.Storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}

	// 1. The link is counted, so the target is not an orphan.
	if graph.InboundCount["target-page"] != 1 {
		t.Errorf("a Markdown link must count as inbound: InboundCount = %d, want 1", graph.InboundCount["target-page"])
	}

	// 2. A Markdown link with no destination is a broken link, reported in its own syntax.
	var dangling *BrokenLinkRef
	for i, bl := range graph.Broken {
		if bl.TargetSlug == "no-such-page" {
			dangling = &graph.Broken[i]
		}
	}
	if dangling == nil {
		t.Fatalf("a broken Markdown link must be reported, got %+v", graph.Broken)
	}
	if dangling.Form != LinkFormMarkdown {
		t.Errorf("Form = %q, want %q", dangling.Form, LinkFormMarkdown)
	}
	if dangling.Target != "/articles/no-such-page" {
		t.Errorf("Target = %q, want the destination as written", dangling.Target)
	}
	if got := dangling.Display(); got != "(/articles/no-such-page)" {
		t.Errorf("Display() = %q — a Markdown link rendered as [[…]] sends the author looking for text that is not in the file", got)
	}

	// 3. The backlink scan sees it too; read_article lists it, and agents read a page before a rename.
	backlinks, err := srv.Storage.GetBacklinks("target-page")
	if err != nil {
		t.Fatalf("GetBacklinks failed: %v", err)
	}
	if len(backlinks) != 1 || backlinks[0].Slug != "linker" {
		t.Errorf("expected linker as a backlink of target-page, got %+v", backlinks)
	}
}

// TestWikiHealthOrphansCountMarkdownLinks pins the consumer §3.21 hurt most. §6.5 already tuned
// orphan detection once after it fired on 84% of the corpus; counting only one link form left it
// firing on 52%, and an agent learns to skip a check that is usually wrong.
func TestWikiHealthOrphansCountMarkdownLinks(t *testing.T) {
	srv := newMCPServer(t)

	if _, err := srv.Storage.SaveArticle("", "Reachable", "# Reachable", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	home, err := srv.Storage.GetArticle("home")
	if err != nil {
		t.Fatalf("GetArticle home failed: %v", err)
	}
	if _, err := srv.Storage.SaveArticle("home", home.Title,
		home.Content+"\n\n| [Reachable](/articles/reachable) | linked from the dashboard |\n",
		"", "", "", "link it", home.Tags, ""); err != nil {
		t.Fatalf("home edit failed: %v", err)
	}

	resp := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
	if resp.IsError {
		t.Fatalf("wiki_health failed: %s", resp.Content[0].Text)
	}
	out, ok := resp.StructuredContent.(HealthOutput)
	if !ok {
		t.Fatalf("expected HealthOutput, got %T", resp.StructuredContent)
	}
	for _, o := range out.Orphans {
		if o.Slug == "reachable" {
			t.Errorf("a page the home dashboard links to in Markdown is not an orphan; orphans: %+v", out.Orphans)
		}
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

// TestExtractSlugMentions pins the extraction that the unreferenced-skill check is built on: a
// skill reference is code, not a link.
func TestExtractSlugMentions(t *testing.T) {
	body := "Load it with `read_article(slug: \"my-agent-skill\")` first.\n\n" +
		"Bare span: `other-skill-here`\n\n" +
		"```json\n{\"slug\": \"fenced-skill-ref\"}\n```\n\n" +
		"Prose naming plain-prose-slug outside code must not count.\n" +
		"A [[wiki-link-target]] is a link, not a mention.\n"

	got := map[string]bool{}
	for _, m := range ExtractSlugMentions(body) {
		got[m] = true
	}

	for _, want := range []string{"my-agent-skill", "other-skill-here", "fenced-skill-ref"} {
		if !got[want] {
			t.Errorf("expected %q to be extracted, got %v", want, got)
		}
	}
	// Prose is excluded: an unbacked mention is too weak a signal, and the link graph already
	// covers the case where the author actually linked the target.
	if got["plain-prose-slug"] {
		t.Error("slugs in plain prose should not count as mentions")
	}
	if got["wiki-link-target"] {
		t.Error("wikilink targets are links, not code mentions; InboundCount already covers them")
	}
}

// TestWikiHealthUnreferencedSkills covers the four cases that shaped this check, each of which the
// naive link-graph version gets wrong.
func TestWikiHealthUnreferencedSkills(t *testing.T) {
	srv := newTestServer(t)

	// Reached only by a read_article call in another document's prose — zero inbound links.
	// This is the shape of every real skill reference in the corpus.
	_, _ = srv.Storage.SaveArticle("", "Referenced By Call", "# s", "", "", "", "", nil, ContentTypeSkill)
	// Reached by an ordinary link.
	_, _ = srv.Storage.SaveArticle("", "Referenced By Link", "# s", "", "", "", "", nil, ContentTypeSkill)
	// Reached only from an archived document, which must not count.
	_, _ = srv.Storage.SaveArticle("", "Referenced Only By Archived", "# s", "", "", "", "", nil, ContentTypeSkill)
	// Reached by nothing at all.
	_, _ = srv.Storage.SaveArticle("", "Wholly Unreferenced", "# s", "", "", "", "", nil, ContentTypeSkill)
	// A skill named like the retired governance page gets no exemption: nothing in code names it.
	_, _ = srv.Storage.SaveArticle("", "NexWiki Agent Rules", "# g", "", "", "", "", nil, ContentTypeSkill)

	_, _ = srv.Storage.SaveArticle("", "Live Pointer",
		"Call `read_article(slug: \"referenced-by-call\")`, then see [it](/articles/referenced-by-link).",
		"", "", "", "", nil, ContentTypeWiki)
	_, _ = srv.Storage.SaveArticle("", "Retired Pointer",
		"Superseded. Once said `read_article(slug: \"referenced-only-by-archived\")`.",
		"", "", "", "", []string{"archived"}, ContentTypeWiki)

	flagged := findingSlugs(healthReport(t, srv, `{}`).UnreferencedSkills)

	for _, live := range []string{"referenced-by-call", "referenced-by-link"} {
		if flagged[live] {
			t.Errorf("%s is reachable and must not be flagged", live)
		}
	}
	for _, dead := range []string{"wholly-unreferenced", "referenced-only-by-archived", "nexwiki-agent-rules"} {
		if !flagged[dead] {
			t.Errorf("%s is unreachable and should be flagged, got %v", dead, flagged)
		}
	}
}

// TestWikiHealthUnreferencedSkillsIgnoresNonSkills guards the scope decision. Memories and plans
// are reached through their own list tools and are meant to be link-less; flagging them would fire
// on most of the corpus, which is why orphan detection already excludes them.
func TestWikiHealthUnreferencedSkillsIgnoresNonSkills(t *testing.T) {
	srv := newTestServer(t)
	_, _ = srv.Storage.SaveArticle("", "Lonely Memory", "# m", "", "src", "", "", nil, ContentTypeMemory)
	_, _ = srv.Storage.SaveArticle("", "Lonely Plan", "# p", "", "", "", "", nil, ContentTypePlan)
	_, _ = srv.Storage.SaveArticle("", "Lonely Article", "# a", "", "", "", "", nil, ContentTypeWiki)

	out := healthReport(t, srv, `{}`)
	if out.UnreferencedSkillCount != 0 {
		t.Errorf("only skills belong in this check, got %v", out.UnreferencedSkills)
	}
	// An archived skill is out of scope for every check, the same as every other type.
	_, _ = srv.Storage.SaveArticle("", "Retired Skill", "# s", "", "", "", "", []string{"archived"}, ContentTypeSkill)
	if c := healthReport(t, srv, `{}`).UnreferencedSkillCount; c != 0 {
		t.Errorf("archived skills must not be flagged, got %d", c)
	}
}

// TestWikiHealthFlagsStaleConcepts verifies that wiki_health flags an article with a past stale_after in stale_concepts.
func TestWikiHealthFlagsStaleConcepts(t *testing.T) {
	srv := newMCPServer(t)

	// Past stale_after -> should be flagged in stale_concepts
	past, _ := time.Parse("2006-01-02", "2020-01-01")
	if _, err := srv.Storage.SaveArticleWithOverrides("", "Expired Concept", "# Expired\n\nOld content.", "", "", "", "Initial", nil, ContentTypeWiki, ArticleOverrides{StaleAfter: &past}); err != nil {
		t.Fatalf("failed to create expired article: %v", err)
	}

	// Future stale_after -> should NOT be flagged
	future, _ := time.Parse("2006-01-02", "2035-01-01")
	if _, err := srv.Storage.SaveArticleWithOverrides("", "Fresh Concept", "# Fresh\n\nNew content.", "", "", "", "Initial", nil, ContentTypeWiki, ArticleOverrides{StaleAfter: &future}); err != nil {
		t.Fatalf("failed to create fresh article: %v", err)
	}

	out := healthReport(t, srv, `{}`)
	if out.StaleConceptCount != 1 {
		t.Errorf("expected 1 stale concept, got %d", out.StaleConceptCount)
	}
	got := findingSlugs(out.StaleConcepts)
	if !got["expired-concept"] {
		t.Errorf("expected 'expired-concept' in stale_concepts, got: %+v", out.StaleConcepts)
	}
	if got["fresh-concept"] {
		t.Errorf("'fresh-concept' must not be reported in stale_concepts")
	}
	if len(out.StaleConcepts) > 0 {
		expectedDetail := "Expired on 2020-01-01 (trust tier: unverified)"
		if out.StaleConcepts[0].Detail != expectedDetail {
			t.Errorf("expected detail %q, got %q", expectedDetail, out.StaleConcepts[0].Detail)
		}
	}
}

// writeBrokenArticle drops a file with malformed front matter straight into the article directory,
// the way a hand edit or a bad sync would, bypassing the save path that would never write one.
func writeBrokenArticle(t *testing.T, srv *Server, relPath string) {
	t.Helper()
	path := filepath.Join(srv.Storage.ArticleDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(path, []byte("---\ntitle: [unclosed\n---\nbody\n"), 0644); err != nil {
		t.Fatalf("writing %s failed: %v", path, err)
	}
}

// assertMatchesOutputSchema validates a response the way a client receives it against the schema
// the tool publishes. TestStructuredOutputMatchesSchema runs on a fixture with no unreadable files,
// so without this the entries of unreadable_files would never be checked.
func assertMatchesOutputSchema(t *testing.T, resp ToolResponse, schema map[string]interface{}) {
	t.Helper()
	encoded, err := json.Marshal(resp.StructuredContent)
	if err != nil {
		t.Fatalf("structuredContent does not serialize: %v", err)
	}
	var decoded interface{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("structuredContent does not round-trip: %v", err)
	}
	for _, problem := range validateAgainstSchema(decoded, schema, "$") {
		t.Error(problem)
	}
}

// TestWikiHealthReportsUnreadableFiles pins that a file the scan cannot parse is reported instead of
// silently missing, and that it leaves the report on the healthy documents around it unchanged.
func TestWikiHealthReportsUnreadableFiles(t *testing.T) {
	srv := newMCPServer(t)
	captureLog(t) // the scan warns about the broken file; keep that out of the test output

	if _, err := srv.Storage.SaveArticle("", "Healthy Page", "# Healthy", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("seeding failed: %v", err)
	}
	before := healthReport(t, srv, `{}`)
	if before.UnreadableFileCount != 0 || before.UnreadableFiles == nil {
		t.Fatalf("a wiki with no broken files should report 0 and an empty list, got %d and %#v",
			before.UnreadableFileCount, before.UnreadableFiles)
	}

	writeBrokenArticle(t, srv, "notes/broken.md")

	resp := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
	var out HealthOutput
	decodeStructured(t, resp, &out)

	if out.UnreadableFileCount != 1 || len(out.UnreadableFiles) != 1 {
		t.Fatalf("expected one unreadable file, got count %d and %+v", out.UnreadableFileCount, out.UnreadableFiles)
	}
	got := out.UnreadableFiles[0]
	if got.Path != "notes/broken.md" {
		t.Errorf("path = %q, want the slash-separated path relative to the article directory", got.Path)
	}
	if !strings.Contains(got.Error, "YAML") {
		t.Errorf("error should carry the parse failure, got %q", got.Error)
	}
	if out.Truncated {
		t.Error("one unreadable file is well under the default limit")
	}

	// The broken file is not a document, and the documents around it are reported as before.
	if out.TotalDocuments != before.TotalDocuments {
		t.Errorf("total_documents changed from %d to %d; an unreadable file is not a document", before.TotalDocuments, out.TotalDocuments)
	}
	if !findingSlugs(out.Orphans)["healthy-page"] {
		t.Errorf("the healthy page should still be reported as an orphan, got %+v", out.Orphans)
	}

	text := resp.Content[0].Text
	for _, want := range []string{
		"- Unreadable article files and folders (skipped by every check): 1\n",
		"== Unreadable article files and folders (1) ==",
		"- notes/broken.md — invalid format: front matter is not valid YAML",
		"Fix its front matter or file permissions, or delete the file.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("prose is missing %q:\n%s", want, text)
		}
	}

	assertMatchesOutputSchema(t, resp, wikiHealthTool.Output)
}

// TestWikiHealthUnreadableFileAloneNeedsAttention pins that an unreadable file on its own stops the
// report calling the wiki healthy. The other unreadable-file fixtures keep the seeded home page's
// broken links, which would mask the file being left out of the tally.
func TestWikiHealthUnreadableFileAloneNeedsAttention(t *testing.T) {
	srv := newHealthyWikiServer(t)
	captureLog(t)

	if text := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`).Content[0].Text; !strings.Contains(text, "the wiki is healthy") {
		t.Fatalf("the fixture must start with nothing to report, or this test proves nothing:\n%s", text)
	}

	writeBrokenArticle(t, srv, "broken.md")

	resp := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
	var out HealthOutput
	decodeStructured(t, resp, &out)
	if out.UnreadableFileCount != 1 {
		t.Fatalf("expected one unreadable file, got %d", out.UnreadableFileCount)
	}
	text := resp.Content[0].Text
	if strings.Contains(text, "the wiki is healthy") {
		t.Errorf("a wiki with an unreadable file is not healthy:\n%s", text)
	}
	if !strings.Contains(text, "- broken.md — ") {
		t.Errorf("the report should go on to list the file:\n%s", text)
	}
}

// TestWikiHealthProseDropsTheErrorsOwnPeriod pins that an error ending in a period, as Windows OS
// errors do ("Access is denied."), does not double up with the one the remedy sentence adds, and
// that only the prose is trimmed.
func TestWikiHealthProseDropsTheErrorsOwnPeriod(t *testing.T) {
	const osErr = `open articles\locked.md: Access is denied.`
	out := HealthOutput{
		UnreadableFileCount: 1,
		UnreadableFiles:     []UnreadableFile{{Path: "locked.md", Error: osErr}},
	}

	text := renderHealthReport(out)
	if want := `- locked.md — open articles\locked.md: Access is denied. Fix its front matter`; !strings.Contains(text, want) {
		t.Errorf("prose is missing %q:\n%s", want, text)
	}
	if strings.Contains(text, "denied..") {
		t.Errorf("prose doubles the period:\n%s", text)
	}
	if got := out.UnreadableFiles[0].Error; got != osErr {
		t.Errorf("the structured error must be left as the OS reported it, got %q", got)
	}
}

// TestWikiHealthCapsUnreadableFiles pins that the new category honours 'limit' like every other:
// the count stays complete, the list is cut, and the report says so.
func TestWikiHealthCapsUnreadableFiles(t *testing.T) {
	srv := newMCPServer(t)
	captureLog(t)
	for _, name := range []string{"c.md", "a.md", "b.md"} {
		writeBrokenArticle(t, srv, name)
	}

	full := healthReport(t, srv, `{}`)
	if full.UnreadableFileCount != 3 || len(full.UnreadableFiles) != 3 {
		t.Fatalf("expected all three unreadable files, got count %d and %+v", full.UnreadableFileCount, full.UnreadableFiles)
	}
	if full.Truncated {
		t.Error("three files are well under the default limit and must not report truncation")
	}

	capped := healthReport(t, srv, `{"limit":2}`)
	if capped.UnreadableFileCount != 3 {
		t.Errorf("count must stay complete under a limit, got %d", capped.UnreadableFileCount)
	}
	if len(capped.UnreadableFiles) != 2 || capped.UnreadableFiles[0].Path != "a.md" || capped.UnreadableFiles[1].Path != "b.md" {
		t.Errorf("limit:2 should keep the first two paths in order, got %+v", capped.UnreadableFiles)
	}
	if !capped.Truncated {
		t.Error("a capped unreadable list must set truncated")
	}
	if !strings.Contains(capped.Content(), "- b.md — ") || !strings.Contains(capped.Content(), "  ... and 1 more; raise 'limit' to see them.\n") {
		t.Errorf("the prose should list the kept files and say the list was cut short:\n%s", capped.Content())
	}
}

// TestWikiStatisticsCountsUnreadableFiles pins that get_wiki_statistics reports the number
// wiki_health does. Both count with ScanLinkGraph, and two tools disagreeing about the same wiki
// would leave an agent unsure which to believe.
func TestWikiStatisticsCountsUnreadableFiles(t *testing.T) {
	srv := newMCPServer(t)
	captureLog(t)

	stats := func() (StatisticsOutput, ToolResponse) {
		t.Helper()
		resp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{"include_stats":true}}`)
		var out OverviewOutput
		decodeStructured(t, resp, &out)
		if out.Statistics == nil {
			t.Fatalf("expected Statistics in overview")
		}
		return *out.Statistics, resp
	}

	clean, resp := stats()
	if clean.UnreadableFileCount != 0 || strings.Contains(resp.Content[0].Text, "Issues:") {
		t.Errorf("a clean wiki should report 0 unreadable files, got %d:\n%s", clean.UnreadableFileCount, resp.Content[0].Text)
	}

	writeBrokenArticle(t, srv, "one.md")
	writeBrokenArticle(t, srv, "nested/two.md")

	got, resp := stats()
	health := healthReport(t, srv, `{}`)
	if got.UnreadableFileCount != 2 || got.UnreadableFileCount != health.UnreadableFileCount {
		t.Errorf("get_wiki_overview reports %d unreadable files, wiki_health %d; want 2 from both",
			got.UnreadableFileCount, health.UnreadableFileCount)
	}
	if !strings.Contains(resp.Content[0].Text, "Issues: 2 unreadable files/dirs") {
		t.Errorf("prose is missing unreadable count:\n%s", resp.Content[0].Text)
	}

	assertMatchesOutputSchema(t, resp, getWikiOverviewTool.Output)
}

// TestUnreadableForClientHidesDataDir pins that an error handed to an MCP client names paths
// relative to the data directory, as every other tool's errors do, never by where the wiki lives
// on the server's disk, and that each entry's path, relative to the article directory, is kept.
func TestUnreadableForClientHidesDataDir(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "data")
	articles := filepath.Join(abs, "articles")
	sep := string(filepath.Separator)

	for _, tc := range []struct {
		name, dir, path, err, want string
	}{
		{
			name: "a top-level file",
			dir:  abs,
			path: "x.md",
			err:  "open " + filepath.Join(articles, "x.md") + ": permission denied",
			want: "open " + filepath.Join("articles", "x.md") + ": permission denied",
		},
		{
			name: "a nested file",
			dir:  abs,
			path: "notes/x.md",
			err:  "open " + filepath.Join(articles, "notes", "x.md") + ": permission denied",
			want: "open " + filepath.Join("articles", "notes", "x.md") + ": permission denied",
		},
		{
			name: "a directory that could not be listed is named as the OS named it",
			dir:  abs,
			path: "notes/",
			err:  "open " + filepath.Join(articles, "notes") + ": permission denied",
			want: "open " + filepath.Join("articles", "notes") + ": permission denied",
		},
		{
			name: "a path inside a reported directory",
			dir:  abs,
			path: "notes/",
			err:  "lstat " + filepath.Join(articles, "notes", "x.md") + ": input/output error",
			want: "lstat " + filepath.Join("articles", "notes", "x.md") + ": input/output error",
		},
		{
			name: "a sibling of a reported directory",
			dir:  abs,
			path: "notes/",
			err:  "open " + filepath.Join(articles, "notes-old", "x.md") + ": permission denied",
			want: "open " + filepath.Join("articles", "notes-old", "x.md") + ": permission denied",
		},
		{
			name: "a trailing separator on the configured directory",
			dir:  abs + sep,
			path: "x.md",
			err:  "open " + filepath.Join(articles, "x.md") + ": permission denied",
			want: "open " + filepath.Join("articles", "x.md") + ": permission denied",
		},
		{
			name: "another path under the article directory",
			dir:  abs,
			path: "x.md",
			err:  "readlink " + filepath.Join(articles, "other.md") + ": invalid argument",
			want: "readlink " + filepath.Join("articles", "other.md") + ": invalid argument",
		},
		{
			name: "the article directory",
			dir:  abs,
			path: "x.md",
			err:  "lstat " + articles + ": permission denied",
			want: "lstat articles: permission denied",
		},
		{
			name: "the article directory at the end of the text",
			dir:  abs,
			path: "x.md",
			err:  "cannot walk " + articles,
			want: "cannot walk articles",
		},
		{
			name: "the article directory in quotes",
			dir:  abs,
			path: "x.md",
			err:  `cannot walk "` + articles + `": permission denied`,
			want: `cannot walk "articles": permission denied`,
		},
		{
			name: "the article directory before whitespace",
			dir:  abs,
			path: "x.md",
			err:  "walking " + articles + " failed",
			want: "walking articles failed",
		},
		{
			name: "the data directory",
			dir:  abs,
			path: "x.md",
			err:  "lstat " + abs + ": permission denied",
			want: "lstat .: permission denied",
		},
		{
			name: "a sibling of the data directory is left alone",
			dir:  abs,
			path: "x.md",
			err:  "open " + abs + "-old: permission denied",
			want: "open " + abs + "-old: permission denied",
		},
		{
			name: "a file in a sibling of the data directory is left alone",
			dir:  abs,
			path: "x.md",
			err:  "open " + filepath.Join(abs+"-old", "articles", "x.md") + ": permission denied",
			want: "open " + filepath.Join(abs+"-old", "articles", "x.md") + ": permission denied",
		},
		{
			name: "a sibling at the end of the text is left alone",
			dir:  abs,
			path: "x.md",
			err:  "cannot walk " + abs + ".bak",
			want: "cannot walk " + abs + ".bak",
		},
		{
			name: "a parse error names no path and is left alone",
			dir:  abs,
			path: "x.md",
			err:  "invalid format: missing front matter header marker",
			want: "invalid format: missing front matter header marker",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := unreadableForClient(tc.dir, []UnreadableFile{{Path: tc.path, Error: tc.err}})
			if len(got) != 1 || got[0].Path != tc.path || got[0].Error != tc.want {
				t.Errorf("got %+v, want path %q and error %q", got, tc.path, tc.want)
			}
		})
	}

	// A data directory given relative to the working directory yields relative walk paths, and
	// both that form and its absolute form must come out relative to the data directory.
	t.Run("a relative data directory", func(t *testing.T) {
		t.Chdir(t.TempDir())
		const rel = "data"
		absRel, err := filepath.Abs(rel)
		if err != nil {
			t.Fatalf("Abs failed: %v", err)
		}
		files := []UnreadableFile{
			{Path: "x.md", Error: "open " + filepath.Join(rel, "articles", "x.md") + ": permission denied"},
			{Path: "x.md", Error: "open " + filepath.Join(absRel, "articles", "x.md") + ": permission denied"},
			{Path: "notes/", Error: "open " + filepath.Join(rel, "articles", "notes") + ": permission denied"},
			// A single relative name is matched only as the start of a path, so text that merely
			// contains the directory's name survives.
			{Path: "x.md", Error: "yaml: cannot unmarshal !!str `metadata" + sep + "y` into []string"},
			{Path: "x.md", Error: "yaml: unknown field data"},
		}
		got := unreadableForClient(rel, files)
		for i, want := range []string{
			"open " + filepath.Join("articles", "x.md") + ": permission denied",
			"open " + filepath.Join("articles", "x.md") + ": permission denied",
			"open " + filepath.Join("articles", "notes") + ": permission denied",
			files[3].Error,
			files[4].Error,
		} {
			if got[i].Path != files[i].Path || got[i].Error != want {
				t.Errorf("entry %d: got %+v, want path %q and error %q", i, got[i], files[i].Path, want)
			}
		}
	})

	if got := unreadableForClient(abs, []UnreadableFile{}); got == nil || len(got) != 0 {
		t.Errorf("no unreadable files must stay an empty list, not null: %#v", got)
	}
}

// TestWikiHealthUnreadableErrorHidesDataDir is the end-to-end case the sanitizing exists for: a
// permission error from the OS names the absolute path it failed to open.
func TestWikiHealthUnreadableErrorHidesDataDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode 0 does not stop reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads files whatever their mode")
	}
	srv := newMCPServer(t)
	captureLog(t)

	// Written directly and locked before any scan, so no cached copy can stand in for the read.
	path := filepath.Join(srv.Storage.ArticleDir, "locked.md")
	if err := os.WriteFile(path, []byte("---\ntitle: Locked\nslug: locked\n---\nbody\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0644) })

	resp := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
	var out HealthOutput
	decodeStructured(t, resp, &out)

	if len(out.UnreadableFiles) != 1 || out.UnreadableFiles[0].Path != "locked.md" {
		t.Fatalf("expected locked.md to be reported, got %+v", out.UnreadableFiles)
	}
	// Named as read_article names the same failure, relative to the data directory.
	if got, want := out.UnreadableFiles[0].Error, "open articles/locked.md: permission denied"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(encoded), srv.Storage.DataDir) {
		t.Errorf("the response reveals the server's data directory %q:\n%s", srv.Storage.DataDir, encoded)
	}
}

// TestWikiHealthReportsUnreadableDirectory pins how a folder the scan cannot list reaches a client:
// one entry whose path ends in /, an error naming it without the server's path, a remedy that fits
// a directory rather than a file, and a place in get_wiki_statistics' count.
func TestWikiHealthReportsUnreadableDirectory(t *testing.T) {
	srv := newMCPServer(t)
	captureLog(t)

	writeBrokenArticle(t, srv, "broken.md")
	locked := filepath.Join(srv.Storage.ArticleDir, "locked")
	if err := os.MkdirAll(locked, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(locked, "hidden.md"), []byte("---\ntitle: Hidden\nslug: hidden\n---\nbody\n"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	lockDir(t, locked)

	resp := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
	var out HealthOutput
	decodeStructured(t, resp, &out)

	if out.UnreadableFileCount != 2 || len(out.UnreadableFiles) != 2 || out.UnreadableFiles[1].Path != "locked/" {
		t.Fatalf("expected broken.md and locked/ to be reported, got count %d and %+v", out.UnreadableFileCount, out.UnreadableFiles)
	}
	if got, want := out.UnreadableFiles[1].Error, "open articles/locked: permission denied"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(encoded), srv.Storage.DataDir) {
		t.Errorf("the response reveals the server's data directory %q:\n%s", srv.Storage.DataDir, encoded)
	}

	text := resp.Content[0].Text
	for _, want := range []string{
		"- locked/ — open articles/locked: permission denied. Fix what keeps the directory from being listed (usually its permissions) so the articles in it are scanned.\n",
		"- broken.md — invalid format: front matter is not valid YAML",
		"Fix its front matter or file permissions, or delete the file.\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("prose is missing %q:\n%s", want, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "- locked/ ") && strings.Contains(line, "front matter") {
			t.Errorf("a directory has no front matter to fix: %q", line)
		}
	}
	assertMatchesOutputSchema(t, resp, wikiHealthTool.Output)

	statsResp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{"include_stats":true}}`)
	var ov OverviewOutput
	decodeStructured(t, statsResp, &ov)
	if ov.Statistics == nil {
		t.Fatalf("expected Statistics in overview")
	}
	if ov.Statistics.UnreadableFileCount != out.UnreadableFileCount {
		t.Errorf("get_wiki_overview reports %d unreadable entries, wiki_health %d; the directory must count in both",
			ov.Statistics.UnreadableFileCount, out.UnreadableFileCount)
	}
}

// TestScanErrorsHideDataDir pins the error text of a scan that fails outright: when the article
// directory cannot be read or searched, both tools that scan it fail, and neither error may reveal
// where it is. Paths are named relative to the data directory, as in every other tool's errors.
func TestScanErrorsHideDataDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory modes do not stop reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads directories whatever their mode")
	}

	for _, lock := range []struct {
		name  string
		apply func(*testing.T, string)
		// cause is the sanitized error both tools report after their own prefix.
		cause string
	}{
		{name: "unreadable", apply: lockDir, cause: "open articles: permission denied"},
		// The walk lists the directory and fails on the first article it stats, which in a fresh
		// wiki is the seeded home page. The error has to say the directory is what needs fixing.
		{name: "not searchable", apply: lockSearch, cause: "article directory is not searchable: lstat articles/home.md: permission denied"},
	} {
		t.Run(lock.name, func(t *testing.T) {
			srv := newMCPServer(t)
			captureLog(t)
			if entries, err := os.ReadDir(srv.Storage.ArticleDir); err != nil || len(entries) != 1 || entries[0].Name() != "home.md" {
				t.Fatalf("expected a fresh wiki holding only home.md, got %v (%v)", entries, err)
			}
			lock.apply(t, srv.Storage.ArticleDir)

			for _, tc := range []struct{ tool, want string }{
				{tool: "wiki_health", want: "Error scanning the wiki: " + lock.cause},
				// ListArticles runs before the link scan here, so its error is the one returned.
				{tool: "get_wiki_overview", want: "failed to list articles: " + lock.cause},
			} {
				resp := toolCall(t, srv, `{"name":"`+tc.tool+`","arguments":{}}`)
				if !resp.IsError || len(resp.Content) != 1 || resp.Content[0].Text != tc.want {
					t.Errorf("%s: got %+v, want an error reading %q", tc.tool, resp, tc.want)
				}
				encoded, err := json.Marshal(resp)
				if err != nil {
					t.Fatalf("marshal failed: %v", err)
				}
				if strings.Contains(string(encoded), srv.Storage.DataDir) {
					t.Errorf("%s reveals the server's data directory %q:\n%s", tc.tool, srv.Storage.DataDir, encoded)
				}
			}
		})
	}
}

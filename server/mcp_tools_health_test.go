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
	// Plans: one stale, one recent, one finished-but-old, one old still in its default draft.
	// Statuses are set the way an agent would, through edit_agent_plan's status field
	// (creation defaults every plan to 'draft').
	must(`{"name":"create_agent_plan","arguments":{"title":"Stalled Plan","content":"# Plan","project_context":"nexwiki","description":"in flight"}}`)
	must(`{"name":"create_agent_plan","arguments":{"title":"Fresh Plan","content":"# Plan","project_context":"nexwiki","description":"in flight"}}`)
	must(`{"name":"create_agent_plan","arguments":{"title":"Finished Plan","content":"# Plan","project_context":"nexwiki","description":"done"}}`)
	must(`{"name":"create_agent_plan","arguments":{"title":"Untagged Plan","content":"# Plan","project_context":"nexwiki","description":"old and never marked finished"}}`)
	must(`{"name":"create_agent_plan","arguments":{"title":"Superseded Plan","content":"# Plan","project_context":"nexwiki","description":"replaced by another plan"}}`)

	must(`{"name":"edit_agent_plan","arguments":{"slug":"stalled-plan","status":"implementing","loaded_version":1}}`)
	must(`{"name":"edit_agent_plan","arguments":{"slug":"fresh-plan","status":"implementing","loaded_version":1}}`)
	must(`{"name":"edit_agent_plan","arguments":{"slug":"finished-plan","status":"completed","loaded_version":1}}`)
	must(`{"name":"edit_agent_plan","arguments":{"slug":"superseded-plan","status":"superseded","loaded_version":1}}`)

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

// TestScanLinkGraphCountsMarkdownLinks is the §3.21 regression test. The link graph read only
// [[WikiLinks]], but the corpus — and nexwiki-agent-guidelines §5 — prefer absolute Markdown
// links, so 84% of real internal links were invisible: broken-link detection reported 0 against
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

	// 3. get_backlinks sees it too; the guidelines tell agents to run it before a rename.
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
	// The governance skill is referenced from Go, so no document need name it.
	_, _ = srv.Storage.SaveArticle("", agentGuidelinesTitle, "# g", "", "", "", "", nil, ContentTypeSkill)

	_, _ = srv.Storage.SaveArticle("", "Live Pointer",
		"Call `read_article(slug: \"referenced-by-call\")`, then see [it](/articles/referenced-by-link).",
		"", "", "", "", nil, ContentTypeWiki)
	_, _ = srv.Storage.SaveArticle("", "Retired Pointer",
		"Superseded. Once said `read_article(slug: \"referenced-only-by-archived\")`.",
		"", "", "", "", []string{"archived"}, ContentTypeWiki)

	flagged := findingSlugs(healthReport(t, srv, `{}`).UnreferencedSkills)

	for _, live := range []string{"referenced-by-call", "referenced-by-link", AgentGuidelinesSlug} {
		if flagged[live] {
			t.Errorf("%s is reachable and must not be flagged", live)
		}
	}
	for _, dead := range []string{"wholly-unreferenced", "referenced-only-by-archived"} {
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

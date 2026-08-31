package server

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestExtractWikiLinkTargets(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{"no links", "Plain text with no links.", nil},
		{"single link", "See [[Other Page]] here.", []string{"Other Page"}},
		{"piped link", "See [[target-slug|display text]].", []string{"target-slug"}},
		{"multiple links", "[[One]] and [[Two|2]] and [[Three]]", []string{"One", "Two", "Three"}},
		{"unterminated link", "Broken [[never closed", nil},
		{"whitespace trimmed", "[[  Padded Target  ]]", []string{"Padded Target"}},
		{"empty brackets", "[[]] then [[Real]]", []string{"", "Real"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractWikiLinkTargets(tc.content)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ExtractWikiLinkTargets(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

func TestExtractWikiLinkTargetsIgnoresCode(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{"inline code ignored", "Use `[[nodiscard]]` here, but link [[Real Page]].", []string{"Real Page"}},
		{"fenced block ignored", "```cpp\nint x [[maybe_unused]];\n```\nSee [[Other]].", []string{"Other"}},
		{"tilde fence ignored", "~~~lua\nlocal s = [[multi\nline]]\n~~~\n[[Kept]]", []string{"Kept"}},
		{"only code yields nothing", "```\n[[10, 2, 5]]\n```\nand `[[slug]]` example", nil},
		{"prose links still work", "Plain [[A]] and [[B|alias]].", []string{"A", "B"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractWikiLinkTargets(tc.content)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ExtractWikiLinkTargets(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

// TestExtractArticlePathTargets pins the other half of the link graph (§3.21). The corpus is
// overwhelmingly written in this form — the agent guidelines tell authors to prefer it — and it
// was invisible to the scanner, so wiki_health reported 0 broken links against 26 real ones and
// called 44 of 84 documents orphans.
func TestExtractArticlePathTargets(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{"no links", "Plain text with no links.", nil},
		{"single link", "See [Rust](/articles/rust) here.", []string{"/articles/rust"}},
		{"multiple links", "[A](/articles/a) and [B](/articles/b)", []string{"/articles/a", "/articles/b"}},
		{"empty link text", "[](/articles/bare)", []string{"/articles/bare"}},
		{"anchor trimmed", "[Go](/articles/go#history)", []string{"/articles/go"}},
		{"query trimmed", "[Go](/articles/go?v=2)", []string{"/articles/go"}},
		{"trailing slash trimmed", "[Go](/articles/go/)", []string{"/articles/go"}},
		{"link title preserved", `[Go](/articles/go "The Go article")`, []string{"/articles/go"}},
		{"in a table cell", "| [Go](/articles/go) | fast |", []string{"/articles/go"}},
		{"at start of content", "[Go](/articles/go) leads.", []string{"/articles/go"}},
		{"images are not links", "![diagram](/articles/go)", nil},
		{"api paths are not article links", "Call [the API](/api/articles/go).", nil},
		{"relative paths are not matched", "See [Go](articles/go).", nil},
		{"external links ignored", "[Go](https://go.dev)", nil},
		{"bare /articles/ yields nothing", "[Nothing](/articles/)", nil},
		{"fenced block ignored", "```markdown\n[Example](/articles/slug)\n```\nSee [Go](/articles/go).", []string{"/articles/go"}},
		{"inline code ignored", "Write `[Title](/articles/slug)` like [Go](/articles/go).", []string{"/articles/go"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractArticlePathTargets(tc.content)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ExtractArticlePathTargets(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

// TestExtractLinkRefsCoversBothForms checks that the two scanners meet correctly: every link is
// resolved to a slug, and each one records which syntax it was written in so a report can name it
// the way the author will find it in the file.
func TestExtractLinkRefsCoversBothForms(t *testing.T) {
	content := "See [[Rust Programming Language]] and [Go](/articles/go), plus [[go|the same page]]."

	got := ExtractLinkRefs(content)
	want := []LinkRef{
		{Target: "Rust Programming Language", Slug: "rust-programming-language", Form: LinkFormWiki},
		{Target: "go", Slug: "go", Form: LinkFormWiki},
		{Target: "/articles/go", Slug: "go", Form: LinkFormMarkdown},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractLinkRefs = %+v, want %+v", got, want)
	}

	// The Markdown target keeps its leading path, so Slugify must run on the segment rather than
	// the whole destination — Slugify("/articles/go") is "articlesgo", not "go".
	for _, ref := range got {
		if ref.Slug == "articlesgo" {
			t.Errorf("the /articles/ prefix leaked into the slug: %+v", ref)
		}
	}
}

func TestLinkRefDisplay(t *testing.T) {
	wiki := LinkRef{Target: "Rust", Form: LinkFormWiki}
	if got := wiki.Display(); got != "[[Rust]]" {
		t.Errorf("wiki Display() = %q, want %q", got, "[[Rust]]")
	}
	md := LinkRef{Target: "/articles/rust", Form: LinkFormMarkdown}
	if got := md.Display(); got != "(/articles/rust)" {
		t.Errorf("markdown Display() = %q, want %q", got, "(/articles/rust)")
	}
}

// TestRewriteArticlePathLinks pins rename healing for the Markdown form. Only the destination may
// change: the link text is the author's prose, and rewriting it would be a different and much less
// safe operation.
func TestRewriteArticlePathLinks(t *testing.T) {
	cases := []struct {
		name        string
		content     string
		want        string
		wantChanged bool
	}{
		{
			name:        "destination rewritten, text untouched",
			content:     "Read [Rust](/articles/rust) today.",
			want:        "Read [Rust](/articles/rust-programming-language) today.",
			wantChanged: true,
		},
		{
			name:        "anchor preserved",
			content:     "[Rust](/articles/rust#history)",
			want:        "[Rust](/articles/rust-programming-language#history)",
			wantChanged: true,
		},
		{
			name:        "other targets untouched",
			content:     "[Go](/articles/go) and [Rust](/articles/rust)",
			want:        "[Go](/articles/go) and [Rust](/articles/rust-programming-language)",
			wantChanged: true,
		},
		{
			name:        "images are not rewritten",
			content:     "![rust](/articles/rust)",
			want:        "![rust](/articles/rust)",
			wantChanged: false,
		},
		{
			name:        "no match reports no change",
			content:     "[Go](/articles/go)",
			want:        "[Go](/articles/go)",
			wantChanged: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := RewriteArticlePathLinks(tc.content, "rust", "rust-programming-language")
			if got != tc.want {
				t.Errorf("content = %q, want %q", got, tc.want)
			}
			if changed != tc.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tc.wantChanged)
			}
		})
	}

	// A rename that resolves to the same slug is not a rename.
	if _, changed := RewriteArticlePathLinks("[Go](/articles/go)", "go", "Go"); changed {
		t.Error("rewriting a slug to itself should report no change")
	}
}

// TestRenameHealsMarkdownLinks drives the whole rename path. Before §3.21 the healer only rewrote
// [[WikiLinks]], so an inbound Markdown link was left pointing at a slug that no longer existed —
// which is how /articles/rust came to be broken in 14 places in the real corpus.
func TestRenameHealsMarkdownLinks(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	if _, err := storage.SaveArticle("", "Rust", "# Rust", "", "", "", "", nil, ""); err != nil {
		t.Fatalf("seed target failed: %v", err)
	}
	// Markdown-only, deliberately. The healer starts from GetBacklinks, so a fixture that also
	// carries a [[WikiLink]] would be healed even with the Markdown scan reverted — the test would
	// agree with the bug (§3.20's lesson).
	if _, err := storage.SaveArticle("", "Systems", "See [the Rust page](/articles/rust) for details.", "", "", "", "", nil, ""); err != nil {
		t.Fatalf("seed Markdown linker failed: %v", err)
	}
	if _, err := storage.SaveArticle("", "Compilers", "Also [[Rust]].", "", "", "", "", nil, ""); err != nil {
		t.Fatalf("seed WikiLink linker failed: %v", err)
	}

	if _, err := storage.SaveArticle("rust", "Rust Programming Language", "# Rust", "", "", "", "rename", nil, ""); err != nil {
		t.Fatalf("rename failed: %v", err)
	}

	healed, err := storage.GetArticle("systems")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	wantMarkdown := "[the Rust page](/articles/rust-programming-language)"
	if !strings.Contains(healed.Content, wantMarkdown) {
		t.Errorf("Markdown link was not healed; content is:\n%s", healed.Content)
	}
	if !strings.Contains(healed.Content, "[the Rust page]") {
		t.Errorf("the link text must not be rewritten; content is:\n%s", healed.Content)
	}

	wikiHealed, err := storage.GetArticle("compilers")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if !strings.Contains(wikiHealed.Content, "[[Rust Programming Language]]") {
		t.Errorf("WikiLink healing regressed; content is:\n%s", wikiHealed.Content)
	}

	graph, err := storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	// The seeded home page carries its own example WikiLinks to pages that do not exist, so scope
	// the assertion to the document the rename touched.
	for _, bl := range graph.Broken {
		if bl.FromSlug == "systems" || bl.FromSlug == "compilers" {
			t.Errorf("a healed rename should leave no broken links behind, got %+v", bl)
		}
	}
}

func TestGetBacklinks(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	_, _ = storage.SaveArticle("", "Target Page", "# Target", "", "", "", "", nil, "")
	_, _ = storage.SaveArticle("", "Linker One", "Links to [[Target Page]] directly.", "", "", "", "", nil, "")
	_, _ = storage.SaveArticle("", "Linker Two", "Piped link: [[target-page|see target]].", "", "", "", "", nil, "")
	_, _ = storage.SaveArticle("", "Unrelated", "No links here.", "", "", "", "", nil, "")
	_, _ = storage.SaveArticle("", "Self Linker", "I link to [[Self Linker]] myself.", "", "", "", "", nil, "")

	backlinks, err := storage.GetBacklinks("target-page")
	if err != nil {
		t.Fatalf("GetBacklinks failed: %v", err)
	}
	if len(backlinks) != 2 {
		t.Fatalf("expected 2 backlinks, got %d: %+v", len(backlinks), backlinks)
	}
	slugs := map[string]bool{}
	for _, bl := range backlinks {
		slugs[bl.Slug] = true
		if bl.Content != "" {
			t.Errorf("expected metadata-only backlink, got content for %s", bl.Slug)
		}
	}
	if !slugs["linker-one"] || !slugs["linker-two"] {
		t.Errorf("expected linker-one and linker-two, got %v", slugs)
	}

	// Self-links are excluded
	selfLinks, err := storage.GetBacklinks("self-linker")
	if err != nil {
		t.Fatalf("GetBacklinks self failed: %v", err)
	}
	if len(selfLinks) != 0 {
		t.Errorf("expected no backlinks for self-linking article, got %d", len(selfLinks))
	}

	// The home article (excluded from listings) is scanned as a link source
	home, err := storage.GetArticle("home")
	if err != nil {
		t.Fatalf("GetArticle home failed: %v", err)
	}
	_, err = storage.SaveArticle("home", home.Title, home.Content+"\n\nPinned: [[Target Page]]", "", "", "", "pin link", home.Tags, "")
	if err != nil {
		t.Fatalf("home edit failed: %v", err)
	}
	withHome, err := storage.GetBacklinks("target-page")
	if err != nil {
		t.Fatalf("GetBacklinks after home edit failed: %v", err)
	}
	foundHome := false
	for _, bl := range withHome {
		if bl.Slug == "home" {
			foundHome = true
		}
	}
	if !foundHome {
		t.Errorf("expected home in backlinks after linking from it, got %+v", withHome)
	}
}

func TestMCPGetBacklinks(t *testing.T) {
	srv := newMCPServer(t)

	_, _ = srv.Storage.SaveArticle("", "Hub Page", "# Hub", "the hub summary", "", "", "", nil, "")
	_, _ = srv.Storage.SaveArticle("", "Spoke", "Points at [[Hub Page]].", "", "", "", "", nil, "")

	// Missing slug
	_, rpcErr := srv.executeToolCallInternal([]byte(`{"name":"get_backlinks","arguments":{}}`))
	if rpcErr == nil {
		t.Error("expected RPC error for missing slug")
	}

	// Unknown target
	notFound := toolCall(t, srv, `{"name":"get_backlinks","arguments":{"slug":"nope"}}`)
	if !notFound.IsError {
		t.Error("expected error for unknown article")
	}

	// Happy path
	resp := toolCall(t, srv, `{"name":"get_backlinks","arguments":{"slug":"hub-page"}}`)
	if resp.IsError {
		t.Fatalf("get_backlinks failed: %s", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, "Spoke (Slug: spoke") {
		t.Errorf("expected spoke in backlinks output: %s", resp.Content[0].Text)
	}

	// Empty result
	empty := toolCall(t, srv, `{"name":"get_backlinks","arguments":{"slug":"spoke"}}`)
	if empty.IsError || !strings.Contains(empty.Content[0].Text, "No articles link to 'spoke'") {
		t.Errorf("expected empty backlinks message, got: %s", empty.Content[0].Text)
	}

	// read_article appends the Linked from section on linked articles only
	read := toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"hub-page"}}`)
	if !strings.Contains(read.Content[0].Text, "Linked from: Spoke (spoke)") {
		t.Errorf("read_article missing Linked from section: %s", read.Content[0].Text)
	}
	readSpoke := toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"spoke"}}`)
	if strings.Contains(readSpoke.Content[0].Text, "Linked from:") {
		t.Errorf("read_article should not show Linked from on unlinked article: %s", readSpoke.Content[0].Text)
	}
}

func TestHandleGetBacklinks(t *testing.T) {
	srv := newTestServer(t)

	_, _ = srv.Storage.SaveArticle("", "Hub", "# Hub", "", "", "", "", nil, "")
	_, _ = srv.Storage.SaveArticle("", "Pointer", "See [[Hub]].", "", "", "", "", nil, "")

	// 404 for unknown article
	req := httptest.NewRequest("GET", "/api/articles/missing/backlinks", nil)
	req.SetPathValue("slug", "missing")
	w := httptest.NewRecorder()
	srv.HandleGetBacklinks(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404 for missing article, got %d", w.Code)
	}

	// 200 with one backlink
	req2 := httptest.NewRequest("GET", "/api/articles/hub/backlinks", nil)
	req2.SetPathValue("slug", "hub")
	w2 := httptest.NewRecorder()
	srv.HandleGetBacklinks(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(w2.Body.String(), `"slug":"pointer"`) {
		t.Errorf("expected pointer in backlinks JSON: %s", w2.Body.String())
	}

	// 200 with empty JSON array (not null) when no backlinks exist
	req3 := httptest.NewRequest("GET", "/api/articles/pointer/backlinks", nil)
	req3.SetPathValue("slug", "pointer")
	w3 := httptest.NewRecorder()
	srv.HandleGetBacklinks(w3, req3)
	if w3.Code != 200 || strings.TrimSpace(w3.Body.String()) != "[]" {
		t.Errorf("expected empty array, got %d: %s", w3.Code, w3.Body.String())
	}
}

// TestRewriteAssetPathLinks pins asset healing. Every syntactic form that can carry an asset URL
// has to be rewritten — unlike an article link, where the image form is deliberately excluded,
// because an asset URL always names a file that the rename moved.
func TestRewriteAssetPathLinks(t *testing.T) {
	cases := []struct {
		name        string
		content     string
		want        string
		wantChanged bool
	}{
		{
			name:        "markdown image",
			content:     "![chart](/api/assets/diagrams/chart.png)",
			want:        "![chart](/api/assets/architecture-diagrams/chart.png)",
			wantChanged: true,
		},
		{
			name:        "markdown link to an attachment",
			content:     "[the spec](/api/assets/diagrams/spec.pdf)",
			want:        "[the spec](/api/assets/architecture-diagrams/spec.pdf)",
			wantChanged: true,
		},
		{
			name:        "inline html",
			content:     `<img src="/api/assets/diagrams/chart.png" alt="chart">`,
			want:        `<img src="/api/assets/architecture-diagrams/chart.png" alt="chart">`,
			wantChanged: true,
		},
		{
			name:        "filename is never touched",
			content:     "![x](/api/assets/diagrams/Chart.Final_v2.PNG)",
			want:        "![x](/api/assets/architecture-diagrams/Chart.Final_v2.PNG)",
			wantChanged: true,
		},
		{
			name:        "another article's assets are untouched",
			content:     "![a](/api/assets/other/a.png) ![b](/api/assets/diagrams/b.png)",
			want:        "![a](/api/assets/other/a.png) ![b](/api/assets/architecture-diagrams/b.png)",
			wantChanged: true,
		},
		{
			name:        "article links are not asset links",
			content:     "[Diagrams](/articles/diagrams)",
			want:        "[Diagrams](/articles/diagrams)",
			wantChanged: false,
		},
		{
			name:        "no match reports no change",
			content:     "![a](/api/assets/other/a.png)",
			want:        "![a](/api/assets/other/a.png)",
			wantChanged: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := RewriteAssetPathLinks(tc.content, "diagrams", "architecture-diagrams")
			if got != tc.want {
				t.Errorf("content = %q, want %q", got, tc.want)
			}
			if changed != tc.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tc.wantChanged)
			}
		})
	}

	// A rename that resolves to the same slug is not a rename.
	if _, changed := RewriteAssetPathLinks("![a](/api/assets/go/a.png)", "go", "Go"); changed {
		t.Error("rewriting a slug to itself should report no change")
	}
}

// TestRenameHealsAssetLinks drives the whole rename path for embedded media. A slug rename moves
// data/assets/<slug>, and before this healing every embedded image in the renamed article pointed
// at a directory that no longer existed: the page rendered and every picture on it was broken.
//
// Both referrer shapes are covered, and they are found by different mechanisms. The renamed
// article embeds its own asset — the common case, and one healRenamedLinks cannot see, because it
// visits other documents. The second article embeds the image without linking to the page at all,
// so it has no backlink and is reachable only through the asset scan.
func TestRenameHealsAssetLinks(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	if _, err := storage.SaveArticle("", "Diagrams", "# Diagrams\n\n![chart](/api/assets/diagrams/chart.png)", "", "", "", "", nil, ""); err != nil {
		t.Fatalf("seed owner failed: %v", err)
	}
	if _, err := storage.SaveAsset("diagrams", "chart.png", []byte("PNG")); err != nil {
		t.Fatalf("SaveAsset failed: %v", err)
	}
	// Deliberately no link to the Diagrams page: an embedded image is not a link, so this document
	// has no backlink and the link graph cannot find it.
	if _, err := storage.SaveArticle("", "Overview", "Here it is: ![chart](/api/assets/diagrams/chart.png)", "", "", "", "", nil, ""); err != nil {
		t.Fatalf("seed embedder failed: %v", err)
	}

	if _, err := storage.SaveArticle("diagrams", "Architecture Diagrams", "# Diagrams\n\n![chart](/api/assets/diagrams/chart.png)", "", "", "", "rename", nil, ""); err != nil {
		t.Fatalf("rename failed: %v", err)
	}

	owner, err := storage.GetArticle("architecture-diagrams")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if !strings.Contains(owner.Content, "/api/assets/architecture-diagrams/chart.png") {
		t.Errorf("the renamed article's own asset URL was not healed; content is:\n%s", owner.Content)
	}
	if strings.Contains(owner.Content, "/api/assets/diagrams/chart.png") {
		t.Errorf("a stale asset URL survived the rename; content is:\n%s", owner.Content)
	}

	embedder, err := storage.GetArticle("overview")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if !strings.Contains(embedder.Content, "/api/assets/architecture-diagrams/chart.png") {
		t.Errorf("an embedder with no backlink was not healed; content is:\n%s", embedder.Content)
	}

	// The healed URL must resolve to the file the rename moved, which is the property the user
	// actually experiences.
	if _, err := storage.GetAssetPath("architecture-diagrams", "chart.png"); err != nil {
		t.Errorf("healed asset URL does not resolve: %v", err)
	}
}

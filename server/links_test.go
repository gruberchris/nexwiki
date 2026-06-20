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

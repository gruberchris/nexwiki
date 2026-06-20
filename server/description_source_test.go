package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDescriptionSourceRoundTrip(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	_, err = storage.SaveArticle("", "Round Trip", "# Body", "A one-line summary", "https://example.com/origin", "", "Initial", []string{"notes"}, "")
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	art, err := storage.GetArticle("round-trip")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if art.Description != "A one-line summary" {
		t.Errorf("expected description to round-trip, got '%s'", art.Description)
	}
	if art.Source != "https://example.com/origin" {
		t.Errorf("expected source to round-trip, got '%s'", art.Source)
	}

	// Values containing colons and quotes must survive the line-based parser
	_, err = storage.SaveArticle("", "Tricky Values", "# Body", `Summary: with "colon" and quotes`, "see: RFC 3339", "", "Initial", nil, "")
	if err != nil {
		t.Fatalf("SaveArticle tricky failed: %v", err)
	}
	tricky, err := storage.GetArticle("tricky-values")
	if err != nil {
		t.Fatalf("GetArticle tricky failed: %v", err)
	}
	if tricky.Description != `Summary: with "colon" and quotes` {
		t.Errorf("expected colon/quote description to survive, got '%s'", tricky.Description)
	}
	if tricky.Source != "see: RFC 3339" {
		t.Errorf("expected colon source to survive, got '%s'", tricky.Source)
	}
}

func TestDescriptionMultilinePreserved(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	// YAML front matter handles multi-line scalars natively, so newlines round-trip cleanly
	// instead of being flattened (the old line-based hack).
	_, err = storage.SaveArticle("", "Multi Line", "# Body", "line one\nline two\nline three", "src\nwith newline", "", "Initial", nil, "")
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	art, err := storage.GetArticle("multi-line")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if art.Description != "line one\nline two\nline three" {
		t.Errorf("expected multi-line description to round-trip, got %q", art.Description)
	}
	if art.Source != "src\nwith newline" {
		t.Errorf("expected multi-line source to round-trip, got %q", art.Source)
	}
	// The rest of the front matter must remain intact
	if len(art.Tags) != 0 || art.Title != "Multi Line" {
		t.Errorf("front matter corrupted by multi-line description: %+v", art)
	}
}

func TestParseArticleFileYAMLFrontMatter(t *testing.T) {
	// Native OKF YAML front matter: type + canonical/custom keys, tags as a real YAML list.
	raw := []byte("---\ntype: Wiki\ntitle: Yaml Page\nslug: yaml-page\ndescription: a summary\nresource: https://example.com/spec\ntags:\n    - alpha\n    - beta\ntimestamp: 2025-01-02T03:04:05Z\ncreated_at: 2025-01-02T03:04:05Z\nversion: 3\nedit_summary: yaml format\n---\n# Body\n\nSome text.")

	art, err := parseArticleFile(raw, true)
	if err != nil {
		t.Fatalf("YAML front matter failed to parse: %v", err)
	}
	if art.Type != ContentTypeWiki {
		t.Errorf("expected type Wiki, got '%s'", art.Type)
	}
	if art.Title != "Yaml Page" || art.Version != 3 || len(art.Tags) != 2 {
		t.Errorf("fields parsed incorrectly: %+v", art)
	}
	if art.Description != "a summary" || art.Resource != "https://example.com/spec" {
		t.Errorf("description/resource parsed incorrectly: '%s'/'%s'", art.Description, art.Resource)
	}
}

func TestContentPreviewOnMetadataParse(t *testing.T) {
	raw := []byte("---\ntitle: Preview Page\nslug: preview-page\ncreated_at: 2025-01-02T03:04:05Z\nupdated_at: 2025-01-02T03:04:05Z\nversion: 1\nedit_summary: x\n---\n# Preview Page\n\nThis [[Linked Page]] line should become the preview.")

	art, err := parseArticleFile(raw, false)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	// Heading-only first line is skipped in favor of... the heading text itself after '#' stripping;
	// "# Preview Page" strips to "Preview Page", which is non-empty, so it is the preview.
	if art.ContentPreview != "Preview Page" {
		t.Errorf("unexpected preview: %q", art.ContentPreview)
	}

	// WikiLink brackets are stripped when the first line is prose
	raw2 := []byte("---\ntitle: P2\nslug: p2\ncreated_at: 2025-01-02T03:04:05Z\nupdated_at: 2025-01-02T03:04:05Z\nversion: 1\nedit_summary: x\n---\nSee [[Other Page]] for details.")
	art2, err := parseArticleFile(raw2, false)
	if err != nil {
		t.Fatalf("parse 2 failed: %v", err)
	}
	if art2.ContentPreview != "See Other Page for details." {
		t.Errorf("unexpected preview 2: %q", art2.ContentPreview)
	}

	// Long lines are truncated to 120 runes with ellipsis
	long := strings.Repeat("a", 200)
	raw3 := []byte("---\ntitle: P3\nslug: p3\ncreated_at: 2025-01-02T03:04:05Z\nupdated_at: 2025-01-02T03:04:05Z\nversion: 1\nedit_summary: x\n---\n" + long)
	art3, err := parseArticleFile(raw3, false)
	if err != nil {
		t.Fatalf("parse 3 failed: %v", err)
	}
	if len([]rune(art3.ContentPreview)) != 123 || !strings.HasSuffix(art3.ContentPreview, "...") {
		t.Errorf("expected 120-rune truncated preview, got %d runes", len([]rune(art3.ContentPreview)))
	}

	// Full-content parses do not populate the preview
	art4, err := parseArticleFile(raw2, true)
	if err != nil {
		t.Fatalf("parse 4 failed: %v", err)
	}
	if art4.ContentPreview != "" {
		t.Errorf("expected empty preview on full parse, got %q", art4.ContentPreview)
	}
}

func TestDescriptionPreservedThroughTagUpdateAndRevert(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	_, err = storage.SaveArticle("", "Keeper", "# v1", "the summary", "the source", "", "Initial", []string{"one"}, "")
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	// Tag-only update preserves description and source
	_, err = storage.UpdateArticleTags("keeper", []string{"one", "two"}, 1, "")
	if err != nil {
		t.Fatalf("UpdateArticleTags failed: %v", err)
	}
	art, _ := storage.GetArticle("keeper")
	if art.Description != "the summary" || art.Source != "the source" {
		t.Errorf("tag update lost description/source: '%s'/'%s'", art.Description, art.Source)
	}

	// A content edit that clears the description, then a revert to v1, restores it
	_, err = storage.SaveArticle("keeper", "Keeper", "# v3", "", "", "", "cleared", art.Tags, "")
	if err != nil {
		t.Fatalf("clearing edit failed: %v", err)
	}
	cleared, _ := storage.GetArticle("keeper")
	if cleared.Description != "" {
		t.Fatalf("expected cleared description, got '%s'", cleared.Description)
	}

	reverted, err := storage.RevertArticle("keeper", 1)
	if err != nil {
		t.Fatalf("RevertArticle failed: %v", err)
	}
	if reverted.Description != "the summary" || reverted.Source != "the source" {
		t.Errorf("revert did not restore description/source: '%s'/'%s'", reverted.Description, reverted.Source)
	}
}

func TestMCPDescriptionSourceFlow(t *testing.T) {
	srv := newMCPServer(t)

	// Create with description and source
	resp := toolCall(t, srv, `{"name":"create_wiki_article","arguments":{"title":"Desc Article","content":"# Body","description":"short summary line","source":"https://example.com/ref"}}`)
	if resp.IsError {
		t.Fatalf("create failed: %s", resp.Content[0].Text)
	}

	// read_article surfaces both in the metadata header
	read := toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"desc-article"}}`)
	if !strings.Contains(read.Content[0].Text, "Description: short summary line") {
		t.Errorf("read_article missing description header: %s", read.Content[0].Text)
	}
	if !strings.Contains(read.Content[0].Text, "Source: https://example.com/ref") {
		t.Errorf("read_article missing source header: %s", read.Content[0].Text)
	}

	// list_articles shows the summary line
	list := toolCall(t, srv, `{"name":"list_articles","arguments":{}}`)
	if !strings.Contains(list.Content[0].Text, "Summary: short summary line") {
		t.Errorf("list_articles missing summary: %s", list.Content[0].Text)
	}

	// edit_wiki_article omitting description/source preserves them
	edit := toolCall(t, srv, `{"name":"edit_wiki_article","arguments":{"slug":"desc-article","title":"Desc Article","content":"# Body v2","loaded_version":1}}`)
	if edit.IsError {
		t.Fatalf("edit failed: %s", edit.Content[0].Text)
	}
	art, err := srv.Storage.GetArticle("desc-article")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if art.Description != "short summary line" || art.Source != "https://example.com/ref" {
		t.Errorf("edit without description/source did not preserve them: '%s'/'%s'", art.Description, art.Source)
	}

	// edit_wiki_article with a new description replaces it
	edit2 := toolCall(t, srv, `{"name":"edit_wiki_article","arguments":{"slug":"desc-article","title":"Desc Article","content":"# Body v3","description":"updated summary","loaded_version":2}}`)
	if edit2.IsError {
		t.Fatalf("edit2 failed: %s", edit2.Content[0].Text)
	}
	art2, _ := srv.Storage.GetArticle("desc-article")
	if art2.Description != "updated summary" {
		t.Errorf("expected updated description, got '%s'", art2.Description)
	}
	if art2.Source != "https://example.com/ref" {
		t.Errorf("expected source preserved on edit2, got '%s'", art2.Source)
	}

	// create_agent_memory carries description into list_agent_memories
	mem := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"Mem With Summary","content":"# fact","memory_type":"nexwiki","description":"memory gist","source":"session 2026-06-11"}}`)
	if mem.IsError {
		t.Fatalf("create_agent_memory failed: %s", mem.Content[0].Text)
	}
	memList := toolCall(t, srv, `{"name":"list_agent_memories","arguments":{}}`)
	if !strings.Contains(memList.Content[0].Text, "Summary: memory gist") {
		t.Errorf("list_agent_memories missing summary: %s", memList.Content[0].Text)
	}
}

func TestHandlersDescriptionSource(t *testing.T) {
	srv := newTestServer(t)

	// Create via REST with description and source
	req := httptest.NewRequest("POST", "/api/articles", strings.NewReader(`{"title": "Rest Desc", "content": "# Content", "description": "rest summary", "source": "https://example.com"}`))
	w := httptest.NewRecorder()
	srv.HandleCreateArticle(w, req)
	if w.Code != 201 {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	art, err := srv.Storage.GetArticle("rest-desc")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if art.Description != "rest summary" || art.Source != "https://example.com" {
		t.Errorf("REST create lost description/source: '%s'/'%s'", art.Description, art.Source)
	}

	// Update omitting the fields preserves them
	req2 := httptest.NewRequest("PUT", "/api/articles/rest-desc", strings.NewReader(`{"title": "Rest Desc", "content": "# v2", "loaded_version": 1}`))
	req2.SetPathValue("slug", "rest-desc")
	w2 := httptest.NewRecorder()
	srv.HandleUpdateArticle(w2, req2)
	art2, _ := srv.Storage.GetArticle("rest-desc")
	if art2.Description != "rest summary" || art2.Source != "https://example.com" {
		t.Errorf("REST update lost omitted description/source: '%s'/'%s'", art2.Description, art2.Source)
	}

	// Update with explicit empty strings clears them
	req3 := httptest.NewRequest("PUT", "/api/articles/rest-desc", strings.NewReader(`{"title": "Rest Desc", "content": "# v3", "description": "", "source": "", "loaded_version": 2}`))
	req3.SetPathValue("slug", "rest-desc")
	w3 := httptest.NewRecorder()
	srv.HandleUpdateArticle(w3, req3)
	art3, _ := srv.Storage.GetArticle("rest-desc")
	if art3.Description != "" || art3.Source != "" {
		t.Errorf("REST update with empty strings did not clear: '%s'/'%s'", art3.Description, art3.Source)
	}
}

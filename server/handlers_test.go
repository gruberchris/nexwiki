package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	return NewServer(storage, "Test Wiki", "default", false, NewEventBus(), "0.0.1", "8080")
}

func TestHandleGetStatusTags(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/status-tags", nil)
	w := httptest.NewRecorder()
	srv.HandleGetStatusTags(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if _, ok := resp["tags"]; !ok {
		t.Error("response missing 'tags' key")
	}
	if _, ok := resp["description"]; !ok {
		t.Error("response missing 'description' key")
	}
}

func TestHandleGetConfig(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	srv.HandleGetConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp ConfigResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.WikiName != "Test Wiki" {
		t.Errorf("expected wiki_name 'Test Wiki', got '%s'", resp.WikiName)
	}
	if resp.Version != "0.0.1" {
		t.Errorf("expected version '0.0.1', got '%s'", resp.Version)
	}
	if resp.DefaultTheme != "default" {
		t.Errorf("expected default_theme 'default', got '%s'", resp.DefaultTheme)
	}
}

func TestHandleListArticles(t *testing.T) {
	srv := newTestServer(t)

	// Empty storage: home is seeded but excluded from list
	req := httptest.NewRequest("GET", "/api/articles", nil)
	w := httptest.NewRecorder()
	srv.HandleListArticles(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var articles []Article
	if err := json.Unmarshal(w.Body.Bytes(), &articles); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(articles) != 0 {
		t.Errorf("expected empty list, got %d articles", len(articles))
	}

	// After creating articles
	_, _ = srv.Storage.SaveArticle("", "Alpha Article", "# Content", "", "", "", "", []string{"test"}, "")
	_, _ = srv.Storage.SaveArticle("", "Beta Article", "# Content", "", "", "", "", []string{"test"}, "")

	req2 := httptest.NewRequest("GET", "/api/articles", nil)
	w2 := httptest.NewRecorder()
	srv.HandleListArticles(w2, req2)
	var articles2 []Article
	if err := json.Unmarshal(w2.Body.Bytes(), &articles2); err != nil {
		t.Fatalf("failed to parse response2: %v", err)
	}
	if len(articles2) != 2 {
		t.Errorf("expected 2 articles, got %d", len(articles2))
	}
}

func TestHandleGetArticle(t *testing.T) {
	srv := newTestServer(t)

	// Missing slug
	req := httptest.NewRequest("GET", "/api/articles/", nil)
	w := httptest.NewRecorder()
	srv.HandleGetArticle(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty slug: expected 400, got %d", w.Code)
	}

	// Not found
	req2 := httptest.NewRequest("GET", "/api/articles/nonexistent", nil)
	req2.SetPathValue("slug", "nonexistent")
	w2 := httptest.NewRecorder()
	srv.HandleGetArticle(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("not found: expected 404, got %d", w2.Code)
	}

	// Valid
	_, _ = srv.Storage.SaveArticle("", "Test Page", "# Hello", "", "", "", "", nil, "")
	req3 := httptest.NewRequest("GET", "/api/articles/test-page", nil)
	req3.SetPathValue("slug", "test-page")
	w3 := httptest.NewRecorder()
	srv.HandleGetArticle(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("valid: expected 200, got %d", w3.Code)
	}
	var art Article
	if err := json.Unmarshal(w3.Body.Bytes(), &art); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if art.Title != "Test Page" {
		t.Errorf("expected title 'Test Page', got '%s'", art.Title)
	}
}

func TestHandleCreateArticle(t *testing.T) {
	srv := newTestServer(t)

	// Invalid JSON
	req0 := httptest.NewRequest("POST", "/api/articles", strings.NewReader("not json"))
	w0 := httptest.NewRecorder()
	srv.HandleCreateArticle(w0, req0)
	if w0.Code != http.StatusBadRequest {
		t.Errorf("invalid json: expected 400, got %d", w0.Code)
	}

	// Empty title
	req := httptest.NewRequest("POST", "/api/articles", strings.NewReader(`{"title": "", "content": "# Hello"}`))
	w := httptest.NewRecorder()
	srv.HandleCreateArticle(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty title: expected 400, got %d", w.Code)
	}

	// Valid creation
	req2 := httptest.NewRequest("POST", "/api/articles", strings.NewReader(`{"title": "My Article", "content": "# Content", "tags": ["test"]}`))
	w2 := httptest.NewRecorder()
	srv.HandleCreateArticle(w2, req2)
	if w2.Code != http.StatusCreated {
		t.Errorf("valid create: expected 201, got %d: %s", w2.Code, w2.Body.String())
	}

	// Duplicate slug
	req3 := httptest.NewRequest("POST", "/api/articles", strings.NewReader(`{"title": "My Article", "content": "# Dupe"}`))
	w3 := httptest.NewRecorder()
	srv.HandleCreateArticle(w3, req3)
	if w3.Code != http.StatusConflict {
		t.Errorf("duplicate: expected 409, got %d", w3.Code)
	}

	// Forged memory-scope tags are stripped on user creation; free tags pass through.
	req4 := httptest.NewRequest("POST", "/api/articles", strings.NewReader(`{"title": "Protected Article", "content": "# Content", "tags": ["memory-rules", "normal"]}`))
	w4 := httptest.NewRecorder()
	srv.HandleCreateArticle(w4, req4)
	if w4.Code != http.StatusCreated {
		t.Errorf("agent tag test: expected 201, got %d", w4.Code)
	}
	art, _ := srv.Storage.GetArticle("protected-article")
	for _, tag := range art.Tags {
		if strings.HasPrefix(strings.ToLower(tag), "memory-") {
			t.Errorf("forged memory-scope tag should be stripped, found: %s", tag)
		}
	}
	if !contains(art.Tags, "normal") {
		t.Errorf("expected free tag 'normal' to be preserved, got %v", art.Tags)
	}
}

func TestHandleUpdateArticle(t *testing.T) {
	srv := newTestServer(t)

	// Missing slug
	req := httptest.NewRequest("PUT", "/api/articles/", strings.NewReader(`{"title": "Updated", "content": "# Updated"}`))
	w := httptest.NewRecorder()
	srv.HandleUpdateArticle(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty slug: expected 400, got %d", w.Code)
	}

	// Invalid JSON
	req0 := httptest.NewRequest("PUT", "/api/articles/some-page", strings.NewReader("not json"))
	req0.SetPathValue("slug", "some-page")
	w0 := httptest.NewRecorder()
	srv.HandleUpdateArticle(w0, req0)
	if w0.Code != http.StatusBadRequest {
		t.Errorf("invalid json: expected 400, got %d", w0.Code)
	}

	// Empty title
	req00 := httptest.NewRequest("PUT", "/api/articles/some-page", strings.NewReader(`{"title": "", "content": "# Updated"}`))
	req00.SetPathValue("slug", "some-page")
	w00 := httptest.NewRecorder()
	srv.HandleUpdateArticle(w00, req00)
	if w00.Code != http.StatusBadRequest {
		t.Errorf("empty title: expected 400, got %d", w00.Code)
	}

	// Not found
	req2 := httptest.NewRequest("PUT", "/api/articles/nonexistent", strings.NewReader(`{"title": "Updated", "content": "# Updated"}`))
	req2.SetPathValue("slug", "nonexistent")
	w2 := httptest.NewRecorder()
	srv.HandleUpdateArticle(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("not found: expected 404, got %d", w2.Code)
	}

	// Valid update (same title, no slug change)
	_, _ = srv.Storage.SaveArticle("", "Update Me", "# v1", "", "", "", "", nil, "")
	req3 := httptest.NewRequest("PUT", "/api/articles/update-me", strings.NewReader(`{"title": "Update Me", "content": "# v2", "loaded_version": 1}`))
	req3.SetPathValue("slug", "update-me")
	w3 := httptest.NewRecorder()
	srv.HandleUpdateArticle(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("valid update: expected 200, got %d: %s", w3.Code, w3.Body.String())
	}

	// Version conflict: disk is now version 2, send loaded_version=1
	req4 := httptest.NewRequest("PUT", "/api/articles/update-me", strings.NewReader(`{"title": "Update Me", "content": "# conflict", "loaded_version": 1}`))
	req4.SetPathValue("slug", "update-me")
	w4 := httptest.NewRecorder()
	srv.HandleUpdateArticle(w4, req4)
	if w4.Code != http.StatusConflict {
		t.Errorf("version conflict: expected 409, got %d", w4.Code)
	}
}

func TestHandleUpdateArticleTags(t *testing.T) {
	srv := newTestServer(t)

	// Missing slug
	req := httptest.NewRequest("PATCH", "/api/articles//tags", strings.NewReader(`{"tags": ["test"]}`))
	w := httptest.NewRecorder()
	srv.HandleUpdateArticleTags(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty slug: expected 400, got %d", w.Code)
	}

	// Invalid JSON
	req0 := httptest.NewRequest("PATCH", "/api/articles/some-page/tags", strings.NewReader("not json"))
	req0.SetPathValue("slug", "some-page")
	w0 := httptest.NewRecorder()
	srv.HandleUpdateArticleTags(w0, req0)
	if w0.Code != http.StatusBadRequest {
		t.Errorf("invalid json: expected 400, got %d", w0.Code)
	}

	// Not found
	req2 := httptest.NewRequest("PATCH", "/api/articles/nope/tags", strings.NewReader(`{"tags": ["test"]}`))
	req2.SetPathValue("slug", "nope")
	w2 := httptest.NewRecorder()
	srv.HandleUpdateArticleTags(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("not found: expected 404, got %d", w2.Code)
	}

	// Valid
	_, _ = srv.Storage.SaveArticle("", "Taggable", "# content", "", "", "", "", []string{"old"}, "")
	req3 := httptest.NewRequest("PATCH", "/api/articles/taggable/tags", strings.NewReader(`{"tags": ["new", "updated"], "loaded_version": 1}`))
	req3.SetPathValue("slug", "taggable")
	w3 := httptest.NewRecorder()
	srv.HandleUpdateArticleTags(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("valid tags update: expected 200, got %d", w3.Code)
	}

	// Version conflict: disk is now version 2
	req4 := httptest.NewRequest("PATCH", "/api/articles/taggable/tags", strings.NewReader(`{"tags": ["conflict"], "loaded_version": 1}`))
	req4.SetPathValue("slug", "taggable")
	w4 := httptest.NewRecorder()
	srv.HandleUpdateArticleTags(w4, req4)
	if w4.Code != http.StatusConflict {
		t.Errorf("conflict: expected 409, got %d", w4.Code)
	}
}

func TestHandleDeleteArticle(t *testing.T) {
	srv := newTestServer(t)

	// Missing slug
	req := httptest.NewRequest("DELETE", "/api/articles/", nil)
	w := httptest.NewRecorder()
	srv.HandleDeleteArticle(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty slug: expected 400, got %d", w.Code)
	}

	// Not found
	req2 := httptest.NewRequest("DELETE", "/api/articles/nonexistent", nil)
	req2.SetPathValue("slug", "nonexistent")
	w2 := httptest.NewRecorder()
	srv.HandleDeleteArticle(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("not found: expected 404, got %d", w2.Code)
	}

	// Delete
	_, _ = srv.Storage.SaveArticle("", "Delete Me", "# bye", "", "", "", "", nil, "")
	req3 := httptest.NewRequest("DELETE", "/api/articles/delete-me", nil)
	req3.SetPathValue("slug", "delete-me")
	w3 := httptest.NewRecorder()
	srv.HandleDeleteArticle(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("valid delete: expected 200, got %d", w3.Code)
	}
	if _, err := srv.Storage.GetArticle("delete-me"); err == nil {
		t.Error("article should be deleted but still exists")
	}
}

func TestHandleSearchArticles(t *testing.T) {
	srv := newTestServer(t)

	// Empty query returns empty result
	req := httptest.NewRequest("GET", "/api/search?q=", nil)
	w := httptest.NewRecorder()
	srv.HandleSearchArticles(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("empty query: expected 200, got %d", w.Code)
	}

	// Valid query against fresh storage
	req2 := httptest.NewRequest("GET", "/api/search?q=test", nil)
	w2 := httptest.NewRecorder()
	srv.HandleSearchArticles(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("valid query: expected 200, got %d", w2.Code)
	}
}

func TestHandleGetArticleHistory(t *testing.T) {
	srv := newTestServer(t)

	// Missing slug
	req := httptest.NewRequest("GET", "/api/articles//history", nil)
	w := httptest.NewRecorder()
	srv.HandleGetArticleHistory(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty slug: expected 400, got %d", w.Code)
	}

	// Valid: new article has minimal history
	_, _ = srv.Storage.SaveArticle("", "History Page", "# v1", "", "", "", "", nil, "")
	req2 := httptest.NewRequest("GET", "/api/articles/history-page/history", nil)
	req2.SetPathValue("slug", "history-page")
	w2 := httptest.NewRecorder()
	srv.HandleGetArticleHistory(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("valid history: expected 200, got %d", w2.Code)
	}

	// After a second save, history contains versions
	_, _ = srv.Storage.SaveArticle("history-page", "History Page", "# v2", "", "", "", "", nil, "")
	req3 := httptest.NewRequest("GET", "/api/articles/history-page/history", nil)
	req3.SetPathValue("slug", "history-page")
	w3 := httptest.NewRecorder()
	srv.HandleGetArticleHistory(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("after saves: expected 200, got %d", w3.Code)
	}
	var history []interface{}
	if err := json.Unmarshal(w3.Body.Bytes(), &history); err != nil {
		t.Fatalf("failed to parse history: %v", err)
	}
	if len(history) == 0 {
		t.Error("expected history entries after two saves")
	}
}

func TestHandleGetArticleVersion(t *testing.T) {
	srv := newTestServer(t)

	// Missing slug
	req := httptest.NewRequest("GET", "/api/articles//versions/1", nil)
	req.SetPathValue("version", "1")
	w := httptest.NewRecorder()
	srv.HandleGetArticleVersion(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty slug: expected 400, got %d", w.Code)
	}

	// Invalid version string
	req2 := httptest.NewRequest("GET", "/api/articles/some-page/versions/abc", nil)
	req2.SetPathValue("slug", "some-page")
	req2.SetPathValue("version", "abc")
	w2 := httptest.NewRecorder()
	srv.HandleGetArticleVersion(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("invalid version: expected 400, got %d", w2.Code)
	}

	// Valid: retrieve version 1 after two saves
	_, _ = srv.Storage.SaveArticle("", "Versioned Page", "# v1 content", "", "", "", "", nil, "")
	_, _ = srv.Storage.SaveArticle("versioned-page", "Versioned Page", "# v2 content", "", "", "", "", nil, "")
	req3 := httptest.NewRequest("GET", "/api/articles/versioned-page/versions/1", nil)
	req3.SetPathValue("slug", "versioned-page")
	req3.SetPathValue("version", "1")
	w3 := httptest.NewRecorder()
	srv.HandleGetArticleVersion(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("valid version: expected 200, got %d", w3.Code)
	}
}

func TestHandleRevertArticle(t *testing.T) {
	srv := newTestServer(t)

	// Missing slug
	req := httptest.NewRequest("POST", "/api/articles//revert", strings.NewReader(`{"version": 1}`))
	w := httptest.NewRecorder()
	srv.HandleRevertArticle(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty slug: expected 400, got %d", w.Code)
	}

	// Invalid JSON
	req2 := httptest.NewRequest("POST", "/api/articles/some-page/revert", strings.NewReader("not json"))
	req2.SetPathValue("slug", "some-page")
	w2 := httptest.NewRecorder()
	srv.HandleRevertArticle(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON: expected 400, got %d", w2.Code)
	}

	// Version <= 0
	req3 := httptest.NewRequest("POST", "/api/articles/some-page/revert", strings.NewReader(`{"version": 0}`))
	req3.SetPathValue("slug", "some-page")
	w3 := httptest.NewRecorder()
	srv.HandleRevertArticle(w3, req3)
	if w3.Code != http.StatusBadRequest {
		t.Errorf("version 0: expected 400, got %d", w3.Code)
	}

	// Valid revert
	_, _ = srv.Storage.SaveArticle("", "Revertable", "# v1", "", "", "", "", nil, "")
	_, _ = srv.Storage.SaveArticle("revertable", "Revertable", "# v2", "", "", "", "", nil, "")
	req4 := httptest.NewRequest("POST", "/api/articles/revertable/revert", strings.NewReader(`{"version": 1}`))
	req4.SetPathValue("slug", "revertable")
	w4 := httptest.NewRecorder()
	srv.HandleRevertArticle(w4, req4)
	if w4.Code != http.StatusOK {
		t.Errorf("valid revert: expected 200, got %d: %s", w4.Code, w4.Body.String())
	}
}

func TestHandleDeleteTagGlobally(t *testing.T) {
	srv := newTestServer(t)

	// Missing tag
	req := httptest.NewRequest("DELETE", "/api/tags/", nil)
	w := httptest.NewRecorder()
	srv.HandleDeleteTagGlobally(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty tag: expected 400, got %d", w.Code)
	}

	// Protected tool-managed memory-scope tag
	req2 := httptest.NewRequest("DELETE", "/api/tags/memory-nexwiki", nil)
	req2.SetPathValue("tag", "memory-nexwiki")
	w2 := httptest.NewRecorder()
	srv.HandleDeleteTagGlobally(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Errorf("protected tag: expected 403, got %d", w2.Code)
	}

	// Valid tag deletion
	_, _ = srv.Storage.SaveArticle("", "Tagged", "# content", "", "", "", "", []string{"removable"}, "")
	req3 := httptest.NewRequest("DELETE", "/api/tags/removable", nil)
	req3.SetPathValue("tag", "removable")
	w3 := httptest.NewRecorder()
	srv.HandleDeleteTagGlobally(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("valid delete tag: expected 200, got %d", w3.Code)
	}
}

func TestHandleGetThemes(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/themes", nil)
	w := httptest.NewRecorder()
	srv.HandleGetThemes(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var themes []Theme
	if err := json.Unmarshal(w.Body.Bytes(), &themes); err != nil {
		t.Fatalf("failed to parse themes: %v", err)
	}
	if len(themes) < len(DefaultThemes) {
		t.Errorf("expected at least %d default themes, got %d", len(DefaultThemes), len(themes))
	}
}

func TestHandleSaveTheme(t *testing.T) {
	srv := newTestServer(t)

	// Invalid JSON
	req0 := httptest.NewRequest("POST", "/api/themes", strings.NewReader("not json"))
	w0 := httptest.NewRecorder()
	srv.HandleSaveTheme(w0, req0)
	if w0.Code != http.StatusBadRequest {
		t.Errorf("invalid json: expected 400, got %d", w0.Code)
	}

	// Empty name
	req := httptest.NewRequest("POST", "/api/themes", strings.NewReader(`{"name": ""}`))
	w := httptest.NewRecorder()
	srv.HandleSaveTheme(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty name: expected 400, got %d", w.Code)
	}

	// Default theme name conflict
	req2 := httptest.NewRequest("POST", "/api/themes", strings.NewReader(`{"name": "default", "default_mode": "light"}`))
	w2 := httptest.NewRecorder()
	srv.HandleSaveTheme(w2, req2)
	if w2.Code != http.StatusConflict {
		t.Errorf("default theme conflict: expected 409, got %d", w2.Code)
	}

	// Valid new custom theme
	customThemeJSON := `{"name": "my-custom-theme", "default_mode": "light", "light": {}, "dark": {}}`
	req3 := httptest.NewRequest("POST", "/api/themes", strings.NewReader(customThemeJSON))
	w3 := httptest.NewRecorder()
	srv.HandleSaveTheme(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("valid save: expected 200, got %d", w3.Code)
	}

	// Update existing custom theme (same name, different content)
	req4 := httptest.NewRequest("POST", "/api/themes", strings.NewReader(customThemeJSON))
	w4 := httptest.NewRecorder()
	srv.HandleSaveTheme(w4, req4)
	if w4.Code != http.StatusOK {
		t.Errorf("update existing: expected 200, got %d", w4.Code)
	}
}

func TestHandleDeleteTheme(t *testing.T) {
	srv := newTestServer(t)

	// Missing name
	req := httptest.NewRequest("DELETE", "/api/themes/", nil)
	w := httptest.NewRecorder()
	srv.HandleDeleteTheme(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty name: expected 400, got %d", w.Code)
	}

	// Default theme cannot be deleted
	req2 := httptest.NewRequest("DELETE", "/api/themes/default", nil)
	req2.SetPathValue("name", "default")
	w2 := httptest.NewRecorder()
	srv.HandleDeleteTheme(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("default theme: expected 400, got %d", w2.Code)
	}

	// Not found custom theme
	req3 := httptest.NewRequest("DELETE", "/api/themes/nonexistent", nil)
	req3.SetPathValue("name", "nonexistent")
	w3 := httptest.NewRecorder()
	srv.HandleDeleteTheme(w3, req3)
	if w3.Code != http.StatusNotFound {
		t.Errorf("not found: expected 404, got %d", w3.Code)
	}

	// Save then delete
	saveJSON := `{"name": "deletable-theme", "default_mode": "light", "light": {}, "dark": {}}`
	rSave := httptest.NewRequest("POST", "/api/themes", strings.NewReader(saveJSON))
	wSave := httptest.NewRecorder()
	srv.HandleSaveTheme(wSave, rSave)

	req5 := httptest.NewRequest("DELETE", "/api/themes/deletable-theme", nil)
	req5.SetPathValue("name", "deletable-theme")
	w5 := httptest.NewRecorder()
	srv.HandleDeleteTheme(w5, req5)
	if w5.Code != http.StatusOK {
		t.Errorf("valid delete: expected 200, got %d", w5.Code)
	}
}

func TestHandleGetWikiStats(t *testing.T) {
	srv := newTestServer(t)

	// Empty wiki
	req := httptest.NewRequest("GET", "/api/stats", nil)
	w := httptest.NewRecorder()
	srv.HandleGetWikiStats(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var stats WikiStats
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("failed to parse stats: %v", err)
	}
	if stats.TotalCount != 0 {
		t.Errorf("empty wiki: expected total_count 0, got %d", stats.TotalCount)
	}

	// Add articles with different tag categories
	_, _ = srv.Storage.SaveArticle("", "Wiki Article", "# content", "", "", "", "", nil, "")
	_, _ = srv.Storage.SaveArticle("", "Memory Article", "# content", "", "", "", "", []string{"aiagent-memory-rules"}, ContentTypeMemory)
	_, _ = srv.Storage.SaveArticle("", "Plan Article", "# content", "", "", "", "", []string{"aiagent-plan", "draft"}, ContentTypePlan)
	_, _ = srv.Storage.SaveArticle("", "Skill Article", "# content", "", "", "", "", []string{"aiagent-skill"}, ContentTypeSkill)

	req2 := httptest.NewRequest("GET", "/api/stats", nil)
	w2 := httptest.NewRecorder()
	srv.HandleGetWikiStats(w2, req2)
	var stats2 WikiStats
	if err := json.Unmarshal(w2.Body.Bytes(), &stats2); err != nil {
		t.Fatalf("failed to parse stats2: %v", err)
	}
	if stats2.TotalCount != 4 {
		t.Errorf("expected total_count 4, got %d", stats2.TotalCount)
	}
	if stats2.DirectoryCounts["wiki"] != 1 {
		t.Errorf("expected wiki count 1, got %d", stats2.DirectoryCounts["wiki"])
	}
	if stats2.DirectoryCounts["aimemories"] != 1 {
		t.Errorf("expected aimemories count 1, got %d", stats2.DirectoryCounts["aimemories"])
	}
	if stats2.DirectoryCounts["aiplans"] != 1 {
		t.Errorf("expected aiplans count 1, got %d", stats2.DirectoryCounts["aiplans"])
	}
	if stats2.DirectoryCounts["aiskills"] != 1 {
		t.Errorf("expected aiskills count 1, got %d", stats2.DirectoryCounts["aiskills"])
	}
}

func TestHandleListSkillsAndGetSkill(t *testing.T) {
	srv := newTestServer(t)

	// No skills: list returns empty
	req := httptest.NewRequest("GET", "/api/skills", nil)
	w := httptest.NewRecorder()
	srv.HandleListSkills(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("empty skills: expected 200, got %d", w.Code)
	}

	// Create skill article
	_, _ = srv.Storage.SaveArticle("", "My Skill", "# A skill\n\nThis is what it does.", "", "", "", "", []string{"aiagent-skill", "utility"}, ContentTypeSkill)

	// List skills now includes the skill
	req2 := httptest.NewRequest("GET", "/api/skills", nil)
	w2 := httptest.NewRecorder()
	srv.HandleListSkills(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("list skills: expected 200, got %d", w2.Code)
	}
	var skills []SkillResp
	if err := json.Unmarshal(w2.Body.Bytes(), &skills); err != nil {
		t.Fatalf("failed to parse skills: %v", err)
	}
	if len(skills) != 1 {
		t.Errorf("expected 1 skill, got %d", len(skills))
	}

	// Get the skill
	req3 := httptest.NewRequest("GET", "/api/skills/my-skill", nil)
	req3.SetPathValue("slug", "my-skill")
	w3 := httptest.NewRecorder()
	srv.HandleGetSkill(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("get skill: expected 200, got %d", w3.Code)
	}

	// Get non-skill as skill
	_, _ = srv.Storage.SaveArticle("", "Not A Skill", "# content", "", "", "", "", nil, "")
	req4 := httptest.NewRequest("GET", "/api/skills/not-a-skill", nil)
	req4.SetPathValue("slug", "not-a-skill")
	w4 := httptest.NewRecorder()
	srv.HandleGetSkill(w4, req4)
	if w4.Code != http.StatusNotFound {
		t.Errorf("not a skill: expected 404, got %d", w4.Code)
	}

	// Missing slug
	req5 := httptest.NewRequest("GET", "/api/skills/", nil)
	w5 := httptest.NewRecorder()
	srv.HandleGetSkill(w5, req5)
	if w5.Code != http.StatusBadRequest {
		t.Errorf("empty slug: expected 400, got %d", w5.Code)
	}
}

func TestHandleGetSkillRaw(t *testing.T) {
	srv := newTestServer(t)

	// Missing slug
	req := httptest.NewRequest("GET", "/api/skills//raw", nil)
	w := httptest.NewRecorder()
	srv.HandleGetSkillRaw(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty slug: expected 400, got %d", w.Code)
	}

	// Non-skill article
	_, _ = srv.Storage.SaveArticle("", "Plain Article", "# plain", "", "", "", "", nil, "")
	req2 := httptest.NewRequest("GET", "/api/skills/plain-article/raw", nil)
	req2.SetPathValue("slug", "plain-article")
	w2 := httptest.NewRecorder()
	srv.HandleGetSkillRaw(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("not a skill: expected 404, got %d", w2.Code)
	}

	// Valid skill raw
	_, _ = srv.Storage.SaveArticle("", "Raw Skill", "# raw skill content", "", "", "", "", []string{"aiagent-skill"}, ContentTypeSkill)
	req3 := httptest.NewRequest("GET", "/api/skills/raw-skill/raw", nil)
	req3.SetPathValue("slug", "raw-skill")
	w3 := httptest.NewRecorder()
	srv.HandleGetSkillRaw(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("valid raw: expected 200, got %d", w3.Code)
	}
}

func TestExtractDescription(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{"empty", "", ""},
		{"heading only", "# Heading", ""},
		{"paragraph after heading", "# Title\n\nFirst paragraph here.", "First paragraph here."},
		{"plain paragraph", "Just a paragraph.", "Just a paragraph."},
		{"long paragraph", "# Title\n\n" + strings.Repeat("a", 250), strings.Repeat("a", 200) + "..."},
		{"wikilinks cleaned", "# Title\n\nSee [[Some Page]] for more.", "See Some Page for more."},
		{"empty paragraph skipped", "\n\n# Title\n\nActual content.", "Actual content."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractDescription(tc.content)
			if got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestValidateAndCleanUserTags(t *testing.T) {
	tests := []struct {
		name         string
		incoming     []string
		existing     []string
		docType      string
		expectedLen  int
		expectTag    string
		notExpectTag string
	}{
		{"nil inputs", nil, nil, ContentTypeWiki, 0, "", ""},
		{"empty incoming", []string{}, nil, ContentTypeWiki, 0, "", ""},
		{"normal tags", []string{"tag1", "tag2"}, nil, ContentTypeWiki, 2, "tag1", ""},
		{"deduplication", []string{"tag1", "tag1"}, nil, ContentTypeWiki, 1, "tag1", ""},
		{"forged memory-scope stripped", []string{"memory-rules", "normal"}, nil, ContentTypeMemory, 1, "normal", "memory-rules"},
		{"existing memory-scope preserved on a memory", []string{"memory-rules", "normal"}, []string{"memory-rules"}, ContentTypeMemory, 2, "memory-rules", ""},
		{"legacy aiagent tag now a free tag", []string{"aiagent-skill"}, nil, ContentTypeWiki, 1, "aiagent-skill", ""},
		{"status tag allowed", []string{"completed"}, nil, ContentTypeWiki, 1, "completed", ""},
		{"whitespace trimmed", []string{"  tag1  ", "tag2"}, nil, ContentTypeWiki, 2, "tag1", ""},
		{"empty string skipped", []string{"", "tag1"}, nil, ContentTypeWiki, 1, "tag1", ""},

		// A memory-scope tag is only tool-managed on an AI-Agent-Memory. Anywhere else it is stray
		// data that must be removable, or it can never be cleaned off: DeleteTagGlobally refuses
		// memory-scope tags outright, so this helper was the only remaining path.
		{"stray scope tag removable from a skill", []string{"nexwiki"}, []string{"memory-rules", "nexwiki"}, ContentTypeSkill, 1, "nexwiki", "memory-rules"},
		{"stray scope tag removable from a wiki article", []string{"keep"}, []string{"memory-rules"}, ContentTypeWiki, 1, "keep", "memory-rules"},
		{"stray scope tag removable from a plan", []string{"wip"}, []string{"memory-nexwiki"}, ContentTypePlan, 1, "wip", "memory-nexwiki"},
		{"scope tag still unforgeable onto a skill", []string{"memory-rules", "ready"}, nil, ContentTypeSkill, 1, "ready", "memory-rules"},
		{"scope tag cannot be re-added to a skill that had one", []string{"memory-rules"}, []string{"memory-rules"}, ContentTypeSkill, 0, "", "memory-rules"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := validateAndCleanUserTags(tc.incoming, tc.existing, tc.docType)
			if len(result) != tc.expectedLen {
				t.Errorf("expected %d tags, got %d: %v", tc.expectedLen, len(result), result)
			}
			if tc.expectTag != "" {
				found := false
				for _, tag := range result {
					if tag == tc.expectTag {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected tag %q not found in %v", tc.expectTag, result)
				}
			}
			if tc.notExpectTag != "" {
				for _, tag := range result {
					if tag == tc.notExpectTag {
						t.Errorf("tag %q should not be present in %v", tc.notExpectTag, result)
					}
				}
			}
		})
	}
}

func TestHandleGetConfigSchedulingEnabled(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	// Create server with theme scheduling enabled
	srv := NewServer(storage, "Scheduled Wiki", "default", true, NewEventBus(), "1.0.0", "8080")

	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	srv.HandleGetConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp ConfigResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	if !resp.ThemeSchedulingEnabled {
		t.Error("expected theme_scheduling_enabled true")
	}
}

func TestHandleUploadAsset(t *testing.T) {
	srv := newTestServer(t)

	// Missing slug
	req := httptest.NewRequest("POST", "/api/articles//assets", nil)
	w := httptest.NewRecorder()
	srv.HandleUploadAsset(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty slug: expected 400, got %d", w.Code)
	}

	// Create article to upload asset for
	_, _ = srv.Storage.SaveArticle("", "Asset Article", "# content", "", "", "", "", nil, "")

	// Build a valid multipart form with an image
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "test.png")
	_, _ = fw.Write([]byte("fake-png-data"))
	_ = mw.Close()

	req2 := httptest.NewRequest("POST", "/api/articles/asset-article/assets", &body)
	req2.SetPathValue("slug", "asset-article")
	req2.Header.Set("Content-Type", mw.FormDataContentType())
	// Inject the file Content-Type header for the file part
	w2 := httptest.NewRecorder()
	srv.HandleUploadAsset(w2, req2)
	// Should fail with unsupported type (fake PNG data doesn't have the right MIME),
	// but the form parsing itself should succeed (400 for bad mime, not 500)
	if w2.Code == http.StatusInternalServerError {
		t.Errorf("multipart upload: expected 400 or 200, got 500: %s", w2.Body.String())
	}
}

func TestHandleGetAsset(t *testing.T) {
	srv := newTestServer(t)

	// Missing params
	req := httptest.NewRequest("GET", "/api/articles/slug/assets/", nil)
	req.SetPathValue("slug", "test-slug")
	w := httptest.NewRecorder()
	srv.HandleGetAsset(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing filename: expected 400, got %d", w.Code)
	}

	// Non-existent asset
	req2 := httptest.NewRequest("GET", "/api/articles/slug/assets/image.png", nil)
	req2.SetPathValue("slug", "test-slug")
	req2.SetPathValue("filename", "image.png")
	w2 := httptest.NewRecorder()
	srv.HandleGetAsset(w2, req2)
	if w2.Code != http.StatusForbidden && w2.Code != http.StatusNotFound {
		t.Errorf("non-existent: expected 403 or 404, got %d", w2.Code)
	}
}

func TestHandleActivityStream(t *testing.T) {
	srv := newTestServer(t)

	// Pre-cancel the context so the SSE loop exits immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest("GET", "/api/activity/stream", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	srv.HandleActivityStream(w, req)

	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %s", w.Header().Get("Content-Type"))
	}

	// Test with history events (publish before streaming)
	srv2 := newTestServer(t)
	srv2.EventBus.PublishActivity("api", "create", "", "slug1", "Article 1", "User")
	time.Sleep(5 * time.Millisecond)

	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	req2 := httptest.NewRequest("GET", "/api/activity/stream", nil).WithContext(ctx2)
	w2 := httptest.NewRecorder()
	srv2.HandleActivityStream(w2, req2)

	if !strings.Contains(w2.Body.String(), "event: history") {
		t.Error("expected history events in SSE output")
	}
}

func TestHandleExportOKFBundle(t *testing.T) {
	srv := newTestServer(t)
	_, _ = srv.Storage.SaveArticle("", "Export Article", "# content", "desc", "", "", "init", []string{"test"}, ContentTypeWiki)

	req := httptest.NewRequest("GET", "/api/okf/export", nil)
	w := httptest.NewRecorder()
	srv.HandleExportOKFBundle(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("expected Content-Type application/zip, got %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); cd == "" {
		t.Error("expected Content-Disposition header to be set")
	}
	if w.Body.Len() == 0 {
		t.Error("expected non-empty zip body")
	}
}

func TestHandleImportOKFBundle(t *testing.T) {
	// Happy path: export from a populated store, import into a fresh one.
	src := newTestServer(t)
	_, _ = src.Storage.SaveArticle("", "Import Article", "# hello", "desc", "", "", "init", []string{"tag"}, ContentTypeWiki)
	_, _ = src.Storage.SaveArticle("", "A Plan", "# plan content", "", "", "", "init", []string{"draft"}, ContentTypePlan)

	bundle, err := src.Storage.ExportOKFBundle()
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}

	dst := newTestServer(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "bundle.zip")
	_, _ = fw.Write(bundle)
	_ = mw.Close()

	req := httptest.NewRequest("POST", "/api/okf/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	dst.HandleImportOKFBundle(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("happy path: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var report OKFImportReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("failed to parse import report: %v", err)
	}
	if report.Imported < 2 {
		t.Errorf("expected >=2 articles imported, got %d (warnings: %v)", report.Imported, report.Warnings)
	}

	// Error path: empty body returns 400.
	req2 := httptest.NewRequest("POST", "/api/okf/import", strings.NewReader(""))
	req2.Header.Set("Content-Type", "multipart/form-data; boundary=nothing")
	w2 := httptest.NewRecorder()
	dst.HandleImportOKFBundle(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("empty body: expected 400, got %d", w2.Code)
	}
}

func TestEnableCORS(t *testing.T) {
	handler := EnableCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	// A loopback origin is echoed back verbatim (never "*"), and OPTIONS short-circuits with 200.
	req := httptest.NewRequest("OPTIONS", "/api/test", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("OPTIONS: expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("OPTIONS: expected echoed loopback origin, got %q", got)
	}
	if w.Header().Get("Vary") != "Origin" {
		t.Error("OPTIONS: missing Vary: Origin")
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("OPTIONS: missing nosniff security header")
	}

	// A request with no Origin is a non-browser client (curl, MCP SDK) and passes through.
	req2 := httptest.NewRequest("GET", "/api/test", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTeapot {
		t.Errorf("no-Origin GET: expected 418 from inner handler, got %d", w2.Code)
	}
	if got := w2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("no-Origin GET: should not echo an origin, got %q", got)
	}

	// A cross-site origin is rejected outright — reads included, since there is no auth.
	req3 := httptest.NewRequest("DELETE", "/api/articles/home", nil)
	req3.Header.Set("Origin", "https://evil.example")
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req3)
	if w3.Code != http.StatusForbidden {
		t.Errorf("cross-site DELETE: expected 403, got %d", w3.Code)
	}
	if got := w3.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("cross-site DELETE: must not echo the origin, got %q", got)
	}
}

func TestOriginAllowed(t *testing.T) {
	t.Setenv(AllowedOriginsEnv, "https://wiki.example.com")

	tests := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{"no origin (curl / MCP SDK)", "", "localhost:8080", true},
		{"own UI on loopback", "http://localhost:8080", "localhost:8080", true},
		{"vite dev server", "http://localhost:5173", "localhost:8080", true},
		{"127.0.0.1 loopback", "http://127.0.0.1:8080", "127.0.0.1:8080", true},
		{"IPv6 loopback", "http://[::1]:8080", "[::1]:8080", true},
		{"configured origin", "https://wiki.example.com", "wiki.example.com", true},
		{"LAN same-origin from a phone", "http://192.168.1.50:8080", "192.168.1.50:8080", true},
		{"malicious site", "https://evil.example", "localhost:8080", false},
		{"unconfigured proxy domain", "https://other.example.com", "other.example.com", false},
		{"DNS rebinding: Origin equals a DNS-name Host", "http://evil.example", "evil.example", false},
		{"opaque null origin", "null", "localhost:8080", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := originAllowed(tc.origin, tc.host); got != tc.want {
				t.Errorf("originAllowed(%q, %q) = %v, want %v", tc.origin, tc.host, got, tc.want)
			}
		})
	}

	t.Run("wildcard opt-out restores permissive behavior", func(t *testing.T) {
		t.Setenv(AllowedOriginsEnv, "*")
		origin, ok := originAllowed("https://evil.example", "localhost:8080")
		if !ok || origin != "*" {
			t.Errorf("wildcard: got (%q, %v), want (\"*\", true)", origin, ok)
		}
	})
}

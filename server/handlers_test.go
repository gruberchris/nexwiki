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
	_, _ = srv.Storage.SaveArticle("", "Alpha Article", "# Content", "", []string{"test"})
	_, _ = srv.Storage.SaveArticle("", "Beta Article", "# Content", "", []string{"test"})

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
	_, _ = srv.Storage.SaveArticle("", "Test Page", "# Hello", "", nil)
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

	// New aiagent-* tags (non-plan/skill) are stripped
	req4 := httptest.NewRequest("POST", "/api/articles", strings.NewReader(`{"title": "Protected Article", "content": "# Content", "tags": ["aiagent-memory-rules", "normal"]}`))
	w4 := httptest.NewRecorder()
	srv.HandleCreateArticle(w4, req4)
	if w4.Code != http.StatusCreated {
		t.Errorf("agent tag test: expected 201, got %d", w4.Code)
	}
	art, _ := srv.Storage.GetArticle("protected-article")
	for _, tag := range art.Tags {
		lower := strings.ToLower(tag)
		if strings.HasPrefix(lower, "aiagent-") && lower != "aiagent-skill" && lower != "aiagent-plan" {
			t.Errorf("new aiagent-* tag should be stripped, found: %s", tag)
		}
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
	_, _ = srv.Storage.SaveArticle("", "Update Me", "# v1", "", nil)
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
	_, _ = srv.Storage.SaveArticle("", "Taggable", "# content", "", []string{"old"})
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

	// Valid delete
	_, _ = srv.Storage.SaveArticle("", "Delete Me", "# bye", "", nil)
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
	_, _ = srv.Storage.SaveArticle("", "History Page", "# v1", "", nil)
	req2 := httptest.NewRequest("GET", "/api/articles/history-page/history", nil)
	req2.SetPathValue("slug", "history-page")
	w2 := httptest.NewRecorder()
	srv.HandleGetArticleHistory(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("valid history: expected 200, got %d", w2.Code)
	}

	// After a second save, history contains versions
	_, _ = srv.Storage.SaveArticle("history-page", "History Page", "# v2", "", nil)
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
	_, _ = srv.Storage.SaveArticle("", "Versioned Page", "# v1 content", "", nil)
	_, _ = srv.Storage.SaveArticle("versioned-page", "Versioned Page", "# v2 content", "", nil)
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
	_, _ = srv.Storage.SaveArticle("", "Revertable", "# v1", "", nil)
	_, _ = srv.Storage.SaveArticle("revertable", "Revertable", "# v2", "", nil)
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

	// Protected aiagent- tag
	req2 := httptest.NewRequest("DELETE", "/api/tags/aiagent-plan", nil)
	req2.SetPathValue("tag", "aiagent-plan")
	w2 := httptest.NewRecorder()
	srv.HandleDeleteTagGlobally(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Errorf("protected tag: expected 403, got %d", w2.Code)
	}

	// Valid tag deletion
	_, _ = srv.Storage.SaveArticle("", "Tagged", "# content", "", []string{"removable"})
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

	// Valid delete: save then delete
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
	_, _ = srv.Storage.SaveArticle("", "Wiki Article", "# content", "", nil)
	_, _ = srv.Storage.SaveArticle("", "Memory Article", "# content", "", []string{"aiagent-memory-rules"})
	_, _ = srv.Storage.SaveArticle("", "Plan Article", "# content", "", []string{"aiagent-plan"})
	_, _ = srv.Storage.SaveArticle("", "Skill Article", "# content", "", []string{"aiagent-skill"})

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
	_, _ = srv.Storage.SaveArticle("", "My Skill", "# A skill\n\nThis is what it does.", "", []string{"aiagent-skill", "utility"})

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
	_, _ = srv.Storage.SaveArticle("", "Not A Skill", "# content", "", nil)
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
	_, _ = srv.Storage.SaveArticle("", "Plain Article", "# plain", "", nil)
	req2 := httptest.NewRequest("GET", "/api/skills/plain-article/raw", nil)
	req2.SetPathValue("slug", "plain-article")
	w2 := httptest.NewRecorder()
	srv.HandleGetSkillRaw(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("not a skill: expected 404, got %d", w2.Code)
	}

	// Valid skill raw
	_, _ = srv.Storage.SaveArticle("", "Raw Skill", "# raw skill content", "", []string{"aiagent-skill"})
	req3 := httptest.NewRequest("GET", "/api/skills/raw-skill/raw", nil)
	req3.SetPathValue("slug", "raw-skill")
	w3 := httptest.NewRecorder()
	srv.HandleGetSkillRaw(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("valid raw: expected 200, got %d", w3.Code)
	}
}

func TestHandlePostActivityLog(t *testing.T) {
	srv := newTestServer(t)

	// Non-POST method
	req0 := httptest.NewRequest("GET", "/api/activity", nil)
	w0 := httptest.NewRecorder()
	srv.HandlePostActivityLog(w0, req0)
	if w0.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET method: expected 405, got %d", w0.Code)
	}

	// Invalid body
	req := httptest.NewRequest("POST", "/api/activity", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	srv.HandlePostActivityLog(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid body: expected 400, got %d", w.Code)
	}

	// Valid
	body := `{"source": "mcp", "action": "create", "slug": "test", "title": "Test", "agent": "Claude"}`
	req2 := httptest.NewRequest("POST", "/api/activity", strings.NewReader(body))
	w2 := httptest.NewRecorder()
	srv.HandlePostActivityLog(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("valid: expected 200, got %d", w2.Code)
	}

	// Valid with read action (no wiki update published)
	body2 := `{"source": "mcp", "action": "read", "slug": "test"}`
	req3 := httptest.NewRequest("POST", "/api/activity", strings.NewReader(body2))
	w3 := httptest.NewRecorder()
	srv.HandlePostActivityLog(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("read action: expected 200, got %d", w3.Code)
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
		expectedLen  int
		expectTag    string
		notExpectTag string
	}{
		{"nil inputs", nil, nil, 0, "", ""},
		{"empty incoming", []string{}, nil, 0, "", ""},
		{"normal tags", []string{"tag1", "tag2"}, nil, 2, "tag1", ""},
		{"deduplication", []string{"tag1", "tag1"}, nil, 1, "tag1", ""},
		{"new aiagent-memory stripped", []string{"aiagent-memory-rules", "normal"}, nil, 1, "normal", "aiagent-memory-rules"},
		{"existing aiagent-memory preserved", []string{"aiagent-memory-rules", "normal"}, []string{"aiagent-memory-rules"}, 2, "aiagent-memory-rules", ""},
		{"aiagent-skill always allowed", []string{"aiagent-skill"}, nil, 1, "aiagent-skill", ""},
		{"aiagent-plan always allowed", []string{"aiagent-plan"}, nil, 1, "aiagent-plan", ""},
		{"whitespace trimmed", []string{"  tag1  ", "tag2"}, nil, 2, "tag1", ""},
		{"empty string skipped", []string{"", "tag1"}, nil, 1, "tag1", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := validateAndCleanUserTags(tc.incoming, tc.existing)
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
	_, _ = srv.Storage.SaveArticle("", "Asset Article", "# content", "", nil)

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
	// Should fail with unsupported type (fake PNG data doesn't have the right MIME)
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

func TestEnableCORS(t *testing.T) {
	// OPTIONS request is short-circuited with 200 and CORS headers
	req := httptest.NewRequest("OPTIONS", "/api/test", nil)
	w := httptest.NewRecorder()
	handler := EnableCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("OPTIONS: expected 200, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing Access-Control-Allow-Origin: * header")
	}

	// Non-OPTIONS passes through to inner handler
	req2 := httptest.NewRequest("GET", "/api/test", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTeapot {
		t.Errorf("GET: expected 418 from inner handler, got %d", w2.Code)
	}
	if w2.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("non-OPTIONS: missing CORS header")
	}
}

package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureWithinRejectsSiblingPrefix guards the path-containment check. A string-prefix
// comparison accepts "<dir>-evil" because it literally starts with "<dir>"; filepath.Rel does not.
func TestEnsureWithinRejectsSiblingPrefix(t *testing.T) {
	base := filepath.Join(t.TempDir(), "assets")

	tests := []struct {
		name    string
		target  string
		allowed bool
	}{
		{"file directly inside", filepath.Join(base, "slug", "pic.png"), true},
		{"the directory itself", base, true},
		{"sibling sharing the prefix", base + "-evil/pic.png", false},
		{"parent escape", filepath.Join(base, "..", "secrets.txt"), false},
		{"deep parent escape", filepath.Join(base, "a", "..", "..", "etc", "passwd"), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ensureWithin(base, tc.target)
			if tc.allowed && err != nil {
				t.Errorf("ensureWithin(%q, %q) = %v, want nil", base, tc.target, err)
			}
			if !tc.allowed && err == nil {
				t.Errorf("ensureWithin(%q, %q) = nil, want a traversal rejection", base, tc.target)
			}
		})
	}
}

// TestImportOKFBundleRejectsOversizedEntry guards against zip-bomb style expansion: a small
// archive whose entry decompresses past the per-file ceiling must be skipped with a warning
// rather than read wholly into memory.
func TestImportOKFBundleRejectsOversizedEntry(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Highly compressible payload just past the per-entry cap: a few KB on disk, 8 MB expanded.
	w, err := zw.Create("wiki/huge.md")
	if err != nil {
		t.Fatalf("zip create failed: %v", err)
	}
	body := "---\ntitle: Huge\nslug: huge\ntype: Wiki\n---\n" + strings.Repeat("A", maxBundleEntryBytes+1024)
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatalf("zip write failed: %v", err)
	}

	// A normal document alongside it must still import — the cap skips, it does not abort.
	w2, err := zw.Create("wiki/small.md")
	if err != nil {
		t.Fatalf("zip create failed: %v", err)
	}
	if _, err := w2.Write([]byte("---\ntitle: Small Doc\nslug: small-doc\ntype: Wiki\n---\nhello")); err != nil {
		t.Fatalf("zip write failed: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close failed: %v", err)
	}

	t.Logf("compressed bundle: %d bytes, expands to >%d bytes", buf.Len(), maxBundleEntryBytes)

	report, err := storage.ImportOKFBundle(buf.Bytes())
	if err != nil {
		t.Fatalf("ImportOKFBundle returned a hard error: %v", err)
	}
	if report.Imported != 1 {
		t.Errorf("expected the well-sized document to import, got Imported=%d", report.Imported)
	}
	if report.Skipped == 0 {
		t.Error("expected the oversized document to be counted as skipped")
	}
	if !strings.Contains(strings.Join(report.Warnings, "\n"), "per-file limit") {
		t.Errorf("expected a size-limit warning, got %v", report.Warnings)
	}
	if _, err := storage.GetArticle("huge"); err == nil {
		t.Error("oversized document must not have been written to disk")
	}
}

// TestLimitRequestBodiesReturns413 covers the body ceiling end to end: an oversized payload must
// fail with 413 (not a misleading 400) and must not reach the handler.
func TestLimitRequestBodiesReturns413(t *testing.T) {
	reached := false
	handler := LimitRequestBodies(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeDecodeError(w, err)
			return
		}
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	oversized := `{"content":"` + strings.Repeat("x", int(maxJSONBodyBytes)+1024) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/articles", strings.NewReader(oversized))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: expected 413, got %d", w.Code)
	}
	if reached {
		t.Error("handler ran despite the body exceeding the limit")
	}

	// A normal payload passes straight through.
	req2 := httptest.NewRequest(http.MethodPost, "/api/articles", strings.NewReader(`{"content":"fine"}`))
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK || !reached {
		t.Errorf("normal body: expected 200 and handler execution, got %d reached=%v", w2.Code, reached)
	}
}

// TestRequestBodyLimitPerRoute pins the widened ceilings for the upload endpoints, which
// legitimately carry more than a JSON payload.
func TestRequestBodyLimitPerRoute(t *testing.T) {
	tests := []struct {
		path string
		want int64
	}{
		{"/api/articles", maxJSONBodyBytes},
		{"/api/articles/foo/assets", maxAssetUploadBytes},
		{"/api/okf/import", maxBundleUploadBytes},
	}
	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodPost, tc.path, nil)
		if got := requestBodyLimit(req); got != tc.want {
			t.Errorf("requestBodyLimit(%q) = %d, want %d", tc.path, got, tc.want)
		}
	}
}

// TestNegotiateProtocolVersion verifies the server echoes a supported client protocol revision
// instead of always answering with the hardcoded 2024-11-05.
func TestNegotiateProtocolVersion(t *testing.T) {
	tests := []struct {
		name   string
		params string
		want   string
	}{
		{"echoes a supported revision", `{"protocolVersion":"2025-03-26"}`, "2025-03-26"},
		{"echoes the legacy revision", `{"protocolVersion":"2024-11-05"}`, "2024-11-05"},
		{"falls back for an unknown revision", `{"protocolVersion":"1999-01-01"}`, defaultProtocolVersion},
		{"falls back when omitted", `{}`, defaultProtocolVersion},
		{"falls back with no params", ``, defaultProtocolVersion},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := negotiateProtocolVersion(json.RawMessage(tc.params)); got != tc.want {
				t.Errorf("negotiateProtocolVersion(%s) = %q, want %q", tc.params, got, tc.want)
			}
		})
	}
}

// TestApplyArticleEditPreservesTagsWhenNil pins the pointer semantics that keep a caller which
// does not manage tags (the MCP edit tool, when tags are omitted) from stripping them.
func TestApplyArticleEditPreservesTagsWhenNil(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	art, err := storage.SaveArticle("", "Tagged Page", "body", "", "", "", "seed", []string{"alpha", "beta"}, "")
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	// nil Tags => preserve
	updated, err := storage.ApplyArticleEdit(art.Slug, ArticleEdit{
		Title: "Tagged Page", Content: "new body", EditSummary: "edit", LoadedVersion: art.Version,
	})
	if err != nil {
		t.Fatalf("ApplyArticleEdit failed: %v", err)
	}
	if len(updated.Tags) != 2 {
		t.Errorf("nil Tags must preserve existing tags, got %v", updated.Tags)
	}

	// non-nil Tags => replace
	replacement := []string{"gamma"}
	updated2, err := storage.ApplyArticleEdit(updated.Slug, ArticleEdit{
		Title: "Tagged Page", Content: "newer", EditSummary: "edit", Tags: &replacement, LoadedVersion: updated.Version,
	})
	if err != nil {
		t.Fatalf("ApplyArticleEdit failed: %v", err)
	}
	if len(updated2.Tags) != 1 || updated2.Tags[0] != "gamma" {
		t.Errorf("non-nil Tags must replace, got %v", updated2.Tags)
	}

	// A stale loaded version is refused.
	_, err = storage.ApplyArticleEdit(updated2.Slug, ArticleEdit{
		Title: "Tagged Page", Content: "stale", EditSummary: "edit", LoadedVersion: 1,
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Errorf("expected ErrVersionConflict for a stale version, got %v", err)
	}
}

// TestMCPEndpointReturns413ForOversizedBody pins the gap the REST handlers never had: the MCP
// endpoint read its body directly and reported any failure as a flat 400 "Failed to read request
// body", so an oversized payload was described as malformed when it was well-formed and merely
// too big. An agent told its JSON is invalid rewrites the JSON; the actual fix is to send less.
//
// The endpoint is the one place on the server where that mattered most, since it is the surface
// an automated client hammers without a human reading the error.
func TestMCPEndpointReturns413ForOversizedBody(t *testing.T) {
	srv := newMCPServer(t)
	handler := LimitRequestBodies(http.HandlerFunc(srv.HandleStreamableHTTP))

	oversized := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_wiki_article",` +
		`"arguments":{"title":"Huge","content":"` + strings.Repeat("x", int(maxJSONBodyBytes)+1024) + `"}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(oversized))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized MCP body: expected 413, got %d (%s)", w.Code, strings.TrimSpace(w.Body.String()))
	}
	// The message has to name the ceiling, or the client has nothing to act on.
	if !strings.Contains(w.Body.String(), "MB limit") {
		t.Errorf("413 should name the limit, got %q", strings.TrimSpace(w.Body.String()))
	}

	// A well-formed request of normal size still works, and genuinely malformed JSON still gets
	// the JSON-RPC parse error rather than being swept into the size path.
	req2 := httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("normal MCP request: expected 200, got %d", w2.Code)
	}

	req3 := httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(`{not json`))
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req3)
	if w3.Code != http.StatusBadRequest || !strings.Contains(w3.Body.String(), "-32700") {
		t.Errorf("malformed JSON should still be a -32700 parse error, got %d (%s)",
			w3.Code, strings.TrimSpace(w3.Body.String()))
	}
}

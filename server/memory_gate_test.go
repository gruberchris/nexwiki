package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// The memory write gate. 0.14.0 refused a new memory without memory_kind, description and source;
// 0.21.0 folded create_agent_memory into save_article and the gate did not come along, so memories
// could be created bare again. These tests pin it on every path a memory comes into existence by:
// save_article create, save_article type change, REST POST, and REST PUT type change.

// fullMemoryArgs is a save_article create call that satisfies the gate.
func fullMemoryArgs(title string) map[string]interface{} {
	return map[string]interface{}{
		"title": title, "content": "# " + title, "type": "AI-Agent-Memory",
		"memory_kind": "feedback", "description": "one line", "source": "operator, in session",
	}
}

func TestSaveArticleMemoryCreateRequiresEachField(t *testing.T) {
	for _, field := range []string{"memory_kind", "description", "source"} {
		t.Run("without "+field, func(t *testing.T) {
			srv := newMCPServer(t)
			args := fullMemoryArgs("Gate " + field)
			delete(args, field)
			resp := saveCall(t, srv, args)
			if !resp.IsError {
				t.Fatalf("a memory without %s was created: %s", field, resp.Content[0].Text)
			}
			text := resp.Content[0].Text
			if !strings.Contains(text, "'"+field+"' (") {
				t.Errorf("the refusal does not name and explain %s: %s", field, text)
			}
			for _, other := range []string{"memory_kind", "description", "source"} {
				if other != field && strings.Contains(text, "'"+other+"' (") {
					t.Errorf("the refusal names %s, which was supplied: %s", other, text)
				}
			}
			if _, err := srv.Storage.GetArticle(Slugify("Gate " + field)); err == nil {
				t.Error("the refused memory was written anyway")
			}
		})
	}
}

func TestSaveArticleMemoryCreateNamesEveryMissingField(t *testing.T) {
	srv := newMCPServer(t)
	resp := saveCall(t, srv, map[string]interface{}{"title": "Bare Memory", "content": "# m", "type": "AI-Agent-Memory"})
	if !resp.IsError {
		t.Fatalf("a bare memory was created: %s", resp.Content[0].Text)
	}
	for _, field := range []string{"'memory_kind' (", "'description' (", "'source' ("} {
		if !strings.Contains(resp.Content[0].Text, field) {
			t.Errorf("the refusal does not name %s: %s", field, resp.Content[0].Text)
		}
	}
}

func TestSaveArticleMemoryCreateRefusesWhitespace(t *testing.T) {
	srv := newMCPServer(t)
	for _, field := range []string{"memory_kind", "description", "source"} {
		args := fullMemoryArgs("Blank " + field)
		args[field] = "   "
		resp := saveCall(t, srv, args)
		if !resp.IsError {
			t.Errorf("a whitespace-only %s passed the gate: %s", field, resp.Content[0].Text)
			continue
		}
		if !strings.Contains(resp.Content[0].Text, "'"+field+"' (") {
			t.Errorf("the refusal does not name %s: %s", field, resp.Content[0].Text)
		}
	}
}

func TestSaveArticleMemoryCreateRefusesUnknownKind(t *testing.T) {
	srv := newMCPServer(t)
	args := fullMemoryArgs("Odd Kind")
	args["memory_kind"] = "preference"
	resp := saveCall(t, srv, args)
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "not a memory kind") {
		t.Fatalf("an unknown kind must be refused with the vocabulary: %s", resp.Content[0].Text)
	}
}

func TestSaveArticleMemoryCreateWithEveryFieldSucceeds(t *testing.T) {
	srv := newMCPServer(t)
	resp := saveCall(t, srv, fullMemoryArgs("Complete Memory"))
	if resp.IsError {
		t.Fatalf("a complete memory was refused: %s", resp.Content[0].Text)
	}
	art, err := srv.Storage.GetArticle("complete-memory")
	if err != nil {
		t.Fatal(err)
	}
	if art.MemoryKind != "feedback" || art.Description != "one line" || art.Source != "operator, in session" {
		t.Errorf("kind %q description %q source %q", art.MemoryKind, art.Description, art.Source)
	}
}

func TestSaveArticleTypeChangeIntoMemoryIsGated(t *testing.T) {
	srv := newMCPServer(t)
	slug := seedTyped(t, srv, "Becomes A Memory", ContentTypeWiki)

	// The wiki article has neither description nor source, and the call supplies no kind.
	resp := saveCall(t, srv, map[string]interface{}{
		"slug": slug, "title": "Becomes A Memory", "content": "# m", "loaded_version": 1,
		"type": "AI-Agent-Memory", "description": "d", "source": "s",
	})
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "'memory_kind' (") {
		t.Fatalf("a type change into memory without a kind must be refused naming memory_kind: %s", resp.Content[0].Text)
	}
	if art, _ := srv.Storage.GetArticle(slug); art.Type != ContentTypeWiki {
		t.Fatalf("the refused change still relabelled the document to %s", art.Type)
	}

	// The existing description and source count when the call omits them.
	if _, err := srv.Storage.SaveArticle(slug, "Becomes A Memory", "# m", "kept summary", "kept source", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatal(err)
	}
	art, _ := srv.Storage.GetArticle(slug)
	resp = saveCall(t, srv, map[string]interface{}{
		"slug": slug, "title": "Becomes A Memory", "content": "# m", "loaded_version": art.Version,
		"type": "AI-Agent-Memory", "memory_kind": "project",
	})
	if resp.IsError {
		t.Fatalf("existing description and source must satisfy the gate on a type change: %s", resp.Content[0].Text)
	}
	art, _ = srv.Storage.GetArticle(slug)
	if art.Type != ContentTypeMemory || art.MemoryKind != "project" || art.Description != "kept summary" || art.Source != "kept source" {
		t.Errorf("type %q kind %q description %q source %q", art.Type, art.MemoryKind, art.Description, art.Source)
	}
}

// TestSaveArticleEditOfLegacyMemoryIsNotGated: a memory written before the gate — here, with no
// kind, description or source at all — stays editable. The gate is for a memory coming into
// existence, not for every later write.
func TestSaveArticleEditOfLegacyMemoryIsNotGated(t *testing.T) {
	srv := newMCPServer(t)
	legacy, err := srv.Storage.SaveArticle("", "Legacy Memory", "# old", "", "", "", "", nil, ContentTypeMemory)
	if err != nil {
		t.Fatal(err)
	}
	resp := saveCall(t, srv, map[string]interface{}{
		"slug": legacy.Slug, "title": "Legacy Memory", "content": "# corrected", "loaded_version": legacy.Version,
	})
	if resp.IsError {
		t.Fatalf("a plain edit of a legacy memory was refused: %s", resp.Content[0].Text)
	}
	// Passing the type it already has is not a type change either.
	art, _ := srv.Storage.GetArticle(legacy.Slug)
	resp = saveCall(t, srv, map[string]interface{}{
		"slug": legacy.Slug, "title": "Legacy Memory", "content": "# again", "loaded_version": art.Version,
		"type": "AI-Agent-Memory",
	})
	if resp.IsError {
		t.Fatalf("re-stating a legacy memory's own type was refused: %s", resp.Content[0].Text)
	}
}

func memoryGateREST(t *testing.T, srv *Server, method, slug string, body map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/articles"
	if slug != "" {
		path += "/" + slug
	}
	req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	w := httptest.NewRecorder()
	if slug != "" {
		req.SetPathValue("slug", slug)
		srv.HandleUpdateArticle(w, req)
	} else {
		srv.HandleCreateArticle(w, req)
	}
	return w
}

func TestRESTMemoryCreateIsGated(t *testing.T) {
	srv := newTestServer(t)
	w := memoryGateREST(t, srv, "POST", "", map[string]interface{}{
		"title": "Rest Memory", "content": "# m", "type": "AI-Agent-Memory", "description": "d",
	})
	if w.Code != 400 {
		t.Fatalf("POST of a memory without kind and source: got %d, want 400: %s", w.Code, w.Body.String())
	}
	for _, field := range []string{"'memory_kind' (", "'source' ("} {
		if !strings.Contains(w.Body.String(), field) {
			t.Errorf("the 400 does not name %s: %s", field, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "'description' (") {
		t.Errorf("the 400 names description, which was supplied: %s", w.Body.String())
	}

	w = memoryGateREST(t, srv, "POST", "", map[string]interface{}{
		"title": "Rest Memory", "content": "# m", "type": "AI-Agent-Memory",
		"memory_kind": "reference", "description": "d", "source": "s",
	})
	if w.Code != 201 {
		t.Fatalf("POST of a complete memory: got %d, want 201: %s", w.Code, w.Body.String())
	}

	// A wiki article is not a memory, so nothing is required of it.
	w = memoryGateREST(t, srv, "POST", "", map[string]interface{}{"title": "Rest Wiki", "content": "# w"})
	if w.Code != 201 {
		t.Fatalf("POST of a bare wiki article: got %d, want 201: %s", w.Code, w.Body.String())
	}
}

func TestRESTTypeChangeIntoMemoryIsGated(t *testing.T) {
	srv := newTestServer(t)
	art, err := srv.Storage.SaveArticle("", "Rest Relabel", "# r", "", "", "", "", nil, ContentTypeWiki)
	if err != nil {
		t.Fatal(err)
	}
	w := memoryGateREST(t, srv, "PUT", art.Slug, map[string]interface{}{
		"title": "Rest Relabel", "content": "# r", "loaded_version": art.Version, "type": "AI-Agent-Memory",
	})
	if w.Code != 400 {
		t.Fatalf("PUT into memory without metadata: got %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "'memory_kind' (") {
		t.Errorf("the 400 does not name memory_kind: %s", w.Body.String())
	}
	if got, _ := srv.Storage.GetArticle(art.Slug); got.Type != ContentTypeWiki {
		t.Fatalf("the refused PUT still relabelled the document to %s", got.Type)
	}

	w = memoryGateREST(t, srv, "PUT", art.Slug, map[string]interface{}{
		"title": "Rest Relabel", "content": "# r", "loaded_version": art.Version, "type": "AI-Agent-Memory",
		"memory_kind": "user", "description": "d", "source": "s",
	})
	if w.Code != 200 {
		t.Fatalf("PUT into memory with metadata: got %d, want 200: %s", w.Code, w.Body.String())
	}

	// A plain REST edit of a legacy memory is not gated.
	legacy, err := srv.Storage.SaveArticle("", "Rest Legacy Memory", "# old", "", "", "", "", nil, ContentTypeMemory)
	if err != nil {
		t.Fatal(err)
	}
	w = memoryGateREST(t, srv, "PUT", legacy.Slug, map[string]interface{}{
		"title": "Rest Legacy Memory", "content": "# new", "loaded_version": legacy.Version,
	})
	if w.Code != 200 {
		t.Fatalf("a plain REST edit of a legacy memory: got %d, want 200: %s", w.Code, w.Body.String())
	}
}

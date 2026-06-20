package server

import (
	"strings"
	"testing"
	"time"
)

// TestRenameHealsBacklinks verifies that renaming an article rewrites inbound WikiLinks.
func TestRenameHealsBacklinks(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	_, _ = storage.SaveArticle("", "Alpha", "# Alpha", "", "", "", "Initial", nil, ContentTypeWiki)
	_, _ = storage.SaveArticle("", "Beta", "See [[Alpha]] for details.", "", "", "", "Initial", nil, ContentTypeWiki)

	// Rename it Alpha -> Alpha Renamed
	if _, err := storage.SaveArticle("alpha", "Alpha Renamed", "# Alpha", "", "", "", "rename", nil, ContentTypeWiki); err != nil {
		t.Fatalf("rename failed: %v", err)
	}

	beta, err := storage.GetArticle("beta")
	if err != nil {
		t.Fatalf("GetArticle beta failed: %v", err)
	}
	if want := "See [[Alpha Renamed]] for details."; beta.Content != want {
		t.Errorf("expected healed link %q, got %q", want, beta.Content)
	}

	// Backlinks resolve against the new slug.
	backlinks, err := storage.GetBacklinks("alpha-renamed")
	if err != nil {
		t.Fatalf("GetBacklinks failed: %v", err)
	}
	if len(backlinks) != 1 || backlinks[0].Slug != "beta" {
		t.Errorf("expected beta to back-link the renamed article, got %+v", backlinks)
	}
}

// TestEditWikiArticleResourcePointerSemantics covers G2: omit=preserve, ""=clear, value=replace.
func TestEditWikiArticleResourcePointerSemantics(t *testing.T) {
	srv := newMCPServer(t)

	create := toolCall(t, srv, `{"name":"create_wiki_article","arguments":{"title":"Res Art","content":"# Body","resource":"https://example.com/spec"}}`)
	if create.IsError {
		t.Fatalf("create failed: %s", create.Content[0].Text)
	}
	art, _ := srv.Storage.GetArticle("res-art")
	if art.Resource != "https://example.com/spec" {
		t.Fatalf("expected resource set on create, got %q", art.Resource)
	}

	// Omit resource -> preserved
	_ = toolCall(t, srv, `{"name":"edit_wiki_article","arguments":{"slug":"res-art","title":"Res Art","content":"# v2","loaded_version":1}}`)
	art, _ = srv.Storage.GetArticle("res-art")
	if art.Resource != "https://example.com/spec" {
		t.Errorf("omitted resource should preserve, got %q", art.Resource)
	}

	// Explicit value -> replaced
	_ = toolCall(t, srv, `{"name":"edit_wiki_article","arguments":{"slug":"res-art","title":"Res Art","content":"# v3","resource":"https://example.com/other","loaded_version":2}}`)
	art, _ = srv.Storage.GetArticle("res-art")
	if art.Resource != "https://example.com/other" {
		t.Errorf("value resource should replace, got %q", art.Resource)
	}

	// Empty string -> cleared
	_ = toolCall(t, srv, `{"name":"edit_wiki_article","arguments":{"slug":"res-art","title":"Res Art","content":"# v4","resource":"","loaded_version":3}}`)
	art, _ = srv.Storage.GetArticle("res-art")
	if art.Resource != "" {
		t.Errorf("empty-string resource should clear, got %q", art.Resource)
	}
}

// TestReadActivityLogSpansArchives verifies durable history is readable across rotation archives.
func TestReadActivityLogSpansArchives(t *testing.T) {
	dataDir := t.TempDir()

	orig := activityLogRotateBytes
	activityLogRotateBytes = 200
	t.Cleanup(func() { activityLogRotateBytes = orig })

	oldTime := time.Now().Add(-48 * time.Hour)

	al, err := OpenActivityLog(dataDir)
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	// Write enough old events to exceed the threshold so they get archived on reopening.
	for i := 0; i < 6; i++ {
		_ = al.Append(seedEvent("old", oldTime, "api", "edit"))
	}
	_ = al.Close()

	al2, err := OpenActivityLog(dataDir) // rotates the old events into an archive
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	_ = al2.Append(seedEvent("new", time.Now(), "mcp", "create"))
	t.Cleanup(func() { _ = al2.Close() })

	// A wide window reads across the archive boundary and returns the old events too.
	events, err := ReadActivityLog(ActivityLogPath(dataDir), oldTime.Add(-time.Hour), 100, "", "")
	if err != nil {
		t.Fatalf("ReadActivityLog failed: %v", err)
	}
	if len(events) < 7 {
		t.Errorf("expected to read across archives (>=7 events), got %d", len(events))
	}
}

// TestOKFRoundTrip exports a bundle and re-imports it into a fresh store, preserving types/links.
func TestOKFRoundTrip(t *testing.T) {
	src, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })

	_, _ = src.SaveArticle("", "Concept A", "# A\n\nLinks to [[Concept B]].", "summary a", "", "https://example.com/a", "init", []string{"topic"}, ContentTypeWiki)
	_, _ = src.SaveArticle("", "Concept B", "# B", "", "", "", "init", nil, ContentTypeWiki)
	_, _ = src.SaveArticle("", "A Plan", "# plan", "", "", "", "init", []string{"proj"}, ContentTypePlan)
	_, _ = src.SaveArticle("", "A Memory", "# mem", "", "", "", "init", []string{"memory-proj"}, ContentTypeMemory)

	bundle, err := src.ExportOKFBundle()
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if len(bundle) == 0 {
		t.Fatal("expected non-empty bundle")
	}

	dst, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage dst failed: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })

	report, err := dst.ImportOKFBundle(bundle)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if report.Imported < 4 {
		t.Errorf("expected >=4 imported, got %d (warnings: %v)", report.Imported, report.Warnings)
	}

	// Types preserved
	plan, err := dst.GetArticle("a-plan")
	if err != nil || plan.Type != ContentTypePlan {
		t.Errorf("plan type not preserved: %+v err=%v", plan, err)
	}
	mem, err := dst.GetArticle("a-memory")
	if err != nil || mem.Type != ContentTypeMemory {
		t.Errorf("memory type not preserved: %+v err=%v", mem, err)
	}

	// Links re-translated back to WikiLinks
	a, err := dst.GetArticle("concept-a")
	if err != nil {
		t.Fatalf("GetArticle concept-a failed: %v", err)
	}
	if !strings.Contains(a.Content, "[[concept-b") {
		t.Errorf("expected re-translated WikiLink to concept-b, got: %s", a.Content)
	}
	if a.Resource != "https://example.com/a" {
		t.Errorf("expected resource preserved, got %q", a.Resource)
	}
}

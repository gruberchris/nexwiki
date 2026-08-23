package server

import (
	"archive/zip"
	"bytes"
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
	_, _ = src.SaveArticle("", "A Plan", "# plan", "", "", "", "init", []string{"proj", "draft"}, ContentTypePlan)
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

// buildForeignBundle assembles an OKF bundle by hand, the way a *different* tool would emit one.
// Nothing here comes from ExportOKFBundle: the directory names are not NexWiki's, none of the
// NexWiki custom front-matter keys (slug, version, created_at, edit_summary) are present, links are
// ordinary relative Markdown rather than the bundle-relative form we emit, and there are
// non-Markdown files mixed in.
func buildForeignBundle(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	add := func(name, body string) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s failed: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s failed: %v", name, err)
		}
	}

	// Foreign directory naming, no slug key, links written as a plain relative Markdown link.
	add("concepts/architecture-overview.md",
		"---\ntitle: Architecture Overview\ntype: Wiki\ndescription: How the system fits together\n---\n"+
			"# Architecture Overview\n\nSee [the data model](./data-model.md) for details.\n")

	// No `type` at all: the importer must default it to Wiki and flag it rather than reject.
	add("concepts/data-model.md",
		"---\ntitle: Data Model\ndescription: Entities and relations\n---\n# Data Model\n\nBody.\n")

	// A type this project has never heard of: also defaulted to Wiki and flagged.
	add("notes/field-notes.md",
		"---\ntitle: Field Notes\ntype: BlogPost\n---\n# Field Notes\n\nBody.\n")

	// CRLF line endings, which a Windows-authored bundle will have.
	add("concepts/windows-authored.md",
		"---\r\ntitle: Windows Authored\r\ntype: Wiki\r\n---\r\n# Windows Authored\r\n\r\nBody.\r\n")

	// Reserved names the importer must consume rather than import as documents.
	add("index.md", "---\ntitle: Index\nokf_version: \"0.1\"\n---\n# Index\n")
	add("concepts/index.md", "---\ntitle: Concepts\n---\n# Concepts\n")
	add("log.md", "---\ntitle: Log\n---\n# Log\n")

	// Non-Markdown files a real bundle carries; these must be ignored without complaint.
	add("README.txt", "This bundle came from another tool.\n")
	add("assets/diagram.svg", "<svg xmlns='http://www.w3.org/2000/svg'></svg>")

	// Not a concept document at all — no front matter. Permissive import means this is skipped
	// with a warning rather than aborting the whole bundle.
	add("concepts/not-a-document.md", "Just some loose prose with no front matter at all.\n")

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close failed: %v", err)
	}
	return buf.Bytes()
}

// TestImportForeignOKFBundle covers the gap TestOKFRoundTrip leaves: that test exports from
// NexWiki and imports it straight back, so it only ever proves we can read our own output. Import
// is the designed path for third-party content (§2.2), and the spec calls for permissive handling
// (OKF §9) — neither of which is exercised by a self-round-trip.
func TestImportForeignOKFBundle(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	report, err := storage.ImportOKFBundle(buildForeignBundle(t))
	if err != nil {
		t.Fatalf("importing a foreign bundle failed outright: %v", err)
	}

	t.Run("documents import despite foreign layout and missing custom keys", func(t *testing.T) {
		// Directory names are the other tool's, and no entry carries a `slug` key.
		for _, slug := range []string{"architecture-overview", "data-model", "field-notes", "windows-authored"} {
			art, err := storage.GetArticle(slug)
			if err != nil {
				t.Errorf("%q did not import: %v (warnings: %v)", slug, err, report.Warnings)
				continue
			}
			if art.Title == "" {
				t.Errorf("%q imported with no title", slug)
			}
		}
		if report.Imported != 4 {
			t.Errorf("expected 4 imported documents, got %d (warnings: %v)", report.Imported, report.Warnings)
		}
	})

	t.Run("unknown and missing types default to Wiki and are flagged", func(t *testing.T) {
		for _, slug := range []string{"data-model", "field-notes"} {
			art, err := storage.GetArticle(slug)
			if err != nil {
				t.Fatalf("%q missing: %v", slug, err)
			}
			if art.Type != ContentTypeWiki {
				t.Errorf("%q should default to Wiki, got %q", slug, art.Type)
			}
		}
		// Flagging is what makes the permissiveness auditable rather than silent.
		flagged := map[string]bool{}
		for _, s := range report.MissingType {
			flagged[s] = true
		}
		for _, slug := range []string{"data-model", "field-notes"} {
			if !flagged[slug] {
				t.Errorf("%q defaulted to Wiki but was not reported in MissingType: %v", slug, report.MissingType)
			}
		}
	})

	t.Run("plain relative Markdown links become WikiLinks", func(t *testing.T) {
		art, err := storage.GetArticle("architecture-overview")
		if err != nil {
			t.Fatalf("missing: %v", err)
		}
		if !strings.Contains(art.Content, "[[data-model") {
			t.Errorf("a foreign ./data-model.md link was not translated: %q", art.Content)
		}
	})

	t.Run("CRLF front matter parses", func(t *testing.T) {
		art, err := storage.GetArticle("windows-authored")
		if err != nil {
			t.Fatalf("a CRLF-authored document did not import: %v (warnings: %v)", err, report.Warnings)
		}
		if strings.TrimSpace(art.Title) != "Windows Authored" {
			t.Errorf("CRLF leaked into the parsed title: %q", art.Title)
		}
	})

	t.Run("reserved and non-Markdown entries are not imported as documents", func(t *testing.T) {
		for _, slug := range []string{"index", "concepts", "log", "readme", "diagram"} {
			if _, err := storage.GetArticle(slug); err == nil {
				t.Errorf("%q should not have been imported as a document", slug)
			}
		}
	})

	t.Run("a malformed entry is skipped, not fatal", func(t *testing.T) {
		// The whole point of permissive import: one bad document must not cost the other four.
		if _, err := storage.GetArticle("not-a-document"); err == nil {
			t.Error("an entry with no front matter should not have been imported")
		}
		if report.Imported == 0 {
			t.Error("a malformed entry aborted the entire bundle")
		}
	})

	t.Run("re-importing the same bundle updates rather than duplicating", func(t *testing.T) {
		before, err := storage.ListArticles()
		if err != nil {
			t.Fatalf("ListArticles failed: %v", err)
		}
		if _, err := storage.ImportOKFBundle(buildForeignBundle(t)); err != nil {
			t.Fatalf("re-import failed: %v", err)
		}
		after, err := storage.ListArticles()
		if err != nil {
			t.Fatalf("ListArticles failed: %v", err)
		}
		if len(after) != len(before) {
			t.Errorf("re-import duplicated documents: %d then %d", len(before), len(after))
		}
	})
}

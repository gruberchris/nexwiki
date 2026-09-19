package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The loops this file covers all have the same shape: they enumerate the wiki — via ListArticles
// or another listing — and then open each listed document, skipping one that will not open rather
// than failing the whole operation. The skip was silent; it now goes through
// getArticleForScan/skipUnreadable and is warned about once per file version. Each test pins the
// two halves of that: the loop completes (the readable documents are all processed) and the
// skipped one is named in a warning.

// corruptBehindCache breaks one listed article behind the article cache: the file's bytes are
// replaced with bytes the parser refuses while its size and modification time — what a cached
// parse is validated by — are kept, so listings go on reporting the document from the cache while
// a full read of the file fails. That is the state these loops see when a file breaks between
// their listing pass and their open pass, and the only way to produce it in a test: corrupting the
// file outright breaks the listing too, which then skips the document (with the walks' own
// warning) before the loop ever opens it.
func corruptBehindCache(t *testing.T, storage *Storage, slug string) {
	t.Helper()
	// Warm the cache while the file is still good, so the later listing serves its metadata.
	if _, err := storage.ListArticles(); err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	path := filepath.Join(storage.ArticleDir, slug+".md")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	// Same length as the file it replaces so the cache's size check passes, and no front-matter
	// marker so the full parse fails; pinned to the old mtime so the freshness check passes too.
	writeWithMtime(t, path, bytes.Repeat([]byte("x"), len(data)), info.ModTime())
}

// wantSkipWarning fails unless exactly one warning naming relPath was logged.
func wantSkipWarning(t *testing.T, buf *logCaptureBuffer, relPath string) {
	t.Helper()
	if lines := unreadableWarnings(t, buf, relPath); len(lines) != 1 {
		t.Errorf("expected exactly one skip warning for %s, got %d: %v", relPath, len(lines), lines)
	}
}

// bundleNames lists the files an exported OKF bundle holds.
func bundleNames(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("the exported bundle is not a zip: %v", err)
	}
	names := make(map[string]bool)
	for _, f := range zr.File {
		names[f.Name] = true
	}
	return names
}

func TestExportOKFBundleWarnsAndContinuesPastAnUnreadableDocument(t *testing.T) {
	storage := newLifecycleStorage(t)
	for _, title := range []string{"Good Doc", "Broken Doc"} {
		if _, err := storage.SaveArticle("", title, "# body", "", "", "", "seed", nil, ContentTypeWiki); err != nil {
			t.Fatalf("SaveArticle(%q): %v", title, err)
		}
	}
	corruptBehindCache(t, storage, "broken-doc")
	buf := captureLog(t)

	data, err := storage.ExportOKFBundle()
	if err != nil {
		t.Fatalf("the export must survive one unreadable document: %v", err)
	}
	names := bundleNames(t, data)
	if !names["wiki/good-doc.md"] {
		t.Errorf("the readable document is missing from the bundle: %v", names)
	}
	if names["wiki/broken-doc.md"] {
		t.Error("the unreadable document is in the bundle")
	}
	wantSkipWarning(t, buf, "broken-doc.md")

	// Once per file version: a second export over the same broken file adds no warning.
	if _, err := storage.ExportOKFBundle(); err != nil {
		t.Fatalf("second export: %v", err)
	}
	if lines := unreadableWarnings(t, buf, "broken-doc.md"); len(lines) != 1 {
		t.Errorf("the same broken version warned twice: %v", lines)
	}
}

func TestListAgentSkillsWarnsAndContinuesPastAnUnreadableSkill(t *testing.T) {
	srv := newMCPServer(t)
	for _, title := range []string{"Good Skill", "Broken Skill"} {
		if _, err := srv.Storage.SaveArticle("", title, "# body", "", "", "", "seed", nil, ContentTypeSkill); err != nil {
			t.Fatalf("SaveArticle(%q): %v", title, err)
		}
	}
	corruptBehindCache(t, srv.Storage, "broken-skill")
	buf := captureLog(t)

	resp := toolCall(t, srv, `{"name":"list_articles","arguments":{"type":"skills"}}`)
	if resp.IsError {
		t.Fatalf("the listing must survive one unreadable skill: %s", resp.Content[0].Text)
	}
	out, ok := resp.StructuredContent.(DocumentListOutput)
	if !ok {
		t.Fatalf("structured content is %T, want DocumentListOutput", resp.StructuredContent)
	}
	if out.Count != 1 || len(out.Documents) != 1 || out.Documents[0].Slug != "good-skill" {
		t.Errorf("the index must contain exactly the readable skill, got %+v", out)
	}
	if text := resp.Content[0].Text; !strings.Contains(text, "Good Skill") || strings.Contains(text, "Broken Skill") {
		t.Errorf("the text index lists the wrong skills: %q", text)
	}
	wantSkipWarning(t, buf, "broken-skill.md")
}

func TestListAgentPlansWarnsAndContinuesPastAnUnreadablePlan(t *testing.T) {
	srv := newMCPServer(t)
	for _, title := range []string{"Good Plan", "Broken Plan"} {
		if _, err := srv.Storage.SaveArticle("", title, "# body", "", "", "", "seed", nil, ContentTypePlan); err != nil {
			t.Fatalf("SaveArticle(%q): %v", title, err)
		}
	}
	corruptBehindCache(t, srv.Storage, "broken-plan")
	buf := captureLog(t)

	resp := toolCall(t, srv, `{"name":"list_articles","arguments":{"type":"plans"}}`)
	if resp.IsError {
		t.Fatalf("the listing must survive one unreadable plan: %s", resp.Content[0].Text)
	}
	out, ok := resp.StructuredContent.(DocumentListOutput)
	if !ok {
		t.Fatalf("structured content is %T, want DocumentListOutput", resp.StructuredContent)
	}
	if out.Count != 1 || len(out.Documents) != 1 || out.Documents[0].Slug != "good-plan" {
		t.Errorf("the index must contain exactly the readable plan, got %+v", out)
	}
	if text := resp.Content[0].Text; !strings.Contains(text, "Good Plan") || strings.Contains(text, "Broken Plan") {
		t.Errorf("the text index lists the wrong plans: %q", text)
	}
	wantSkipWarning(t, buf, "broken-plan.md")
}

func TestHandleListSkillsStillListsASkillItCannotRead(t *testing.T) {
	srv := newTestServer(t)
	for _, title := range []string{"Good Skill", "Broken Skill"} {
		if _, err := srv.Storage.SaveArticle("", title, "A summary paragraph.\n\n# body", "", "", "", "seed", nil, ContentTypeSkill); err != nil {
			t.Fatalf("SaveArticle(%q): %v", title, err)
		}
	}
	corruptBehindCache(t, srv.Storage, "broken-skill")
	buf := captureLog(t)

	w := httptest.NewRecorder()
	srv.HandleListSkills(w, httptest.NewRequest(http.MethodGet, "/api/skills", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("the registry must survive one unreadable skill: %d: %s", w.Code, w.Body.String())
	}
	var skills []SkillResp
	if err := json.Unmarshal(w.Body.Bytes(), &skills); err != nil {
		t.Fatalf("the registry response is not a skill list: %v", err)
	}
	bySlug := make(map[string]SkillResp, len(skills))
	for _, s := range skills {
		bySlug[s.Name] = s
	}
	// The broken skill stays in the registry — dropping it would hide it from the picker — with
	// the description it could not read reported rather than swallowed.
	good, ok := bySlug["good-skill"]
	if !ok || good.Description != "A summary paragraph." {
		t.Errorf("the readable skill is wrong in the registry: %+v (all: %+v)", good, skills)
	}
	broken, ok := bySlug["broken-skill"]
	if !ok || broken.Description != "" {
		t.Errorf("the unreadable skill must still be listed with no description: %+v (all: %+v)", broken, skills)
	}
	wantSkipWarning(t, buf, "broken-skill.md")
}

func TestPlanLifecycleWorkerWarnsAndContinuesPastAnUnreadablePlan(t *testing.T) {
	s := newLifecycleStorage(t)
	// Pre-field plans with no status, so the sweep's backfill of the readable one proves it got
	// past the broken one. The broken plan is newer, so the sweep reaches it first.
	writeArticleFile(t, s, "good-plan.md", "type: AI-Agent-Plan\ntitle: Good Plan\nslug: good-plan\nversion: 1\n", "body\n", time.Now().Add(-2*time.Hour))
	writeArticleFile(t, s, "broken-plan.md", "type: AI-Agent-Plan\ntitle: Broken Plan\nslug: broken-plan\nversion: 1\n", "body\n", time.Now().Add(-time.Hour))
	corruptBehindCache(t, s, "broken-plan")
	buf := captureLog(t)

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{}}
	out := captureWorkerLog(w)
	w.Sweep()

	if !strings.Contains(out.String(), "set plan 'good-plan' to 'draft'") {
		t.Errorf("the sweep must continue past the broken plan and backfill the readable one, got %q", out.String())
	}
	if art, err := s.GetArticle("good-plan"); err != nil || art.Status != DefaultPlanStatus {
		t.Errorf("the readable plan was not backfilled: %+v (%v)", art, err)
	}
	wantSkipWarning(t, buf, "broken-plan.md")
}

func TestStatusFieldMigrationSuppressesMarkerWhenItSkipsADocument(t *testing.T) {
	s := newLifecycleStorage(t)
	// The boot sweep over the empty corpus wrote the marker; this test is about the sweep itself.
	marker := filepath.Join(s.DataDir, statusFieldMigrationMarker)
	if err := os.Remove(marker); err != nil {
		t.Fatalf("remove the boot marker: %v", err)
	}
	writeArticleFile(t, s, "legacy-plan.md", "type: AI-Agent-Plan\ntitle: Legacy Plan\nslug: legacy-plan\nversion: 1\ntags:\n  - wip\n", "body\n", time.Now().Add(-2*time.Hour))
	writeArticleFile(t, s, "broken-plan.md", "type: AI-Agent-Plan\ntitle: Broken Plan\nslug: broken-plan\nversion: 1\ntags:\n  - done\n", "body\n", time.Now().Add(-time.Hour))
	corruptBehindCache(t, s, "broken-plan")
	buf := captureLog(t)

	if err := s.MigrateStatusToField(); err != nil {
		t.Fatalf("a skipped document must not fail the sweep: %v", err)
	}
	wantSkipWarning(t, buf, "broken-plan.md")
	// The marker says the sweep is done; a document still carrying its retired status tag says it
	// is not, so the marker must be left unwritten for the next boot to retry.
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the marker was written over a skipped document (stat error %v)", err)
	}
	if art, err := s.GetArticle("legacy-plan"); err != nil || art.Status != "implementing" || len(art.Tags) != 0 {
		t.Errorf("the readable plan was not migrated: %+v (%v)", art, err)
	}
}

func TestDeleteTagGloballyWarnsAndContinuesPastAnUnreadableDocument(t *testing.T) {
	s := newLifecycleStorage(t)
	for _, title := range []string{"Tagged Doc", "Broken Doc"} {
		if _, err := s.SaveArticle("", title, "# body", "", "", "", "seed", []string{"doomed"}, ContentTypeWiki); err != nil {
			t.Fatalf("SaveArticle(%q): %v", title, err)
		}
	}
	corruptBehindCache(t, s, "broken-doc")
	buf := captureLog(t)

	report, err := s.DeleteTagGlobally("doomed")
	if err != nil {
		t.Fatalf("the sweep must survive one unreadable document: %v", err)
	}
	if art, err := s.GetArticle("tagged-doc"); err != nil || len(art.Tags) != 0 {
		t.Errorf("the readable document kept its tag: %+v (%v)", art, err)
	}
	wantSkipWarning(t, buf, "broken-doc.md")
	// The skipped document is in the report as well as the log: a caller that cannot see stderr
	// still learns its tag was left in place.
	if got := report.Skipped; len(got) != 1 || got[0] != "broken-doc" {
		t.Errorf("the unreadable document is not reported as skipped: %v", got)
	}
	if len(report.Rewritten) != 1 || report.Rewritten[0].Slug != "tagged-doc" {
		t.Errorf("the rewritten document is not reported: %+v", report.Rewritten)
	}
	if len(report.Failed) != 0 {
		t.Errorf("nothing failed: %+v", report.Failed)
	}
}

// The sweep removes every case-insensitive variant of the tag, in the one rewrite: matching only
// the first left "Foo" and "FOO" behind a deletion of "foo".
func TestDeleteTagGloballyRemovesEveryCaseVariant(t *testing.T) {
	s := newLifecycleStorage(t)
	// Written directly, so the case duplicates survive however a save normalizes tags.
	writeArticleFile(t, s, "variant-doc.md", "type: Wiki\ntitle: Variant Doc\nslug: variant-doc\nversion: 1\ntags:\n  - Foo\n  - keep\n  - foo\n  - FOO\n", "body\n", time.Now().Add(-2*time.Hour))
	writeArticleFile(t, s, "single-doc.md", "type: Wiki\ntitle: Single Doc\nslug: single-doc\nversion: 1\ntags:\n  - foo\n", "body\n", time.Now().Add(-time.Hour))

	// Deleted in yet another casing than any the documents carry.
	report, err := s.DeleteTagGlobally("FOO")
	if err != nil {
		t.Fatalf("DeleteTagGlobally: %v", err)
	}
	if len(report.Rewritten) != 2 || len(report.Skipped) != 0 || len(report.Failed) != 0 {
		t.Fatalf("both documents must be reported as rewritten: %+v (skipped %v, failed %v)",
			report.Rewritten, report.Skipped, report.Failed)
	}
	for _, slug := range []string{"variant-doc", "single-doc"} {
		art, err := s.GetArticle(slug)
		if err != nil {
			t.Fatalf("GetArticle(%s): %v", slug, err)
		}
		if slices.ContainsFunc(art.Tags, func(tag string) bool { return strings.EqualFold(tag, "foo") }) {
			t.Errorf("%s kept a case variant of the deleted tag: %v", slug, art.Tags)
		}
	}
	if art, err := s.GetArticle("variant-doc"); err != nil || len(art.Tags) != 1 || art.Tags[0] != "keep" {
		t.Errorf("untouched tags must survive the sweep: %+v (%v)", art, err)
	}
}

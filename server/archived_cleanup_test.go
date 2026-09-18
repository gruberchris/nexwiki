package server

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const envAutoDeleteArchived = "NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS"

// saveArchived seeds a wiki article carrying the archived tag, which stamps archived_at.
func saveArchived(t *testing.T, s *Storage, title, content string) *Article {
	t.Helper()
	art, err := s.SaveArticle("", title, content, "", "", "", "seed", []string{"archived"}, ContentTypeWiki)
	if err != nil {
		t.Fatalf("seeding archived article %q failed: %v", title, err)
	}
	if art.ArchivedAt.IsZero() {
		t.Fatalf("seeding %q did not stamp archived_at", title)
	}
	return art
}

// backdateArchived rewrites a document on disk as archived days ago and last updated at updated,
// bypassing SaveArticle, which would restamp both. Cleanup works through documents in listing
// order, newest update first, so updated pins that order.
func backdateArchived(t *testing.T, articleDir, slug string, days int, updated time.Time) {
	t.Helper()
	path := filepath.Join(articleDir, slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", slug, err)
	}
	art, err := parseArticleFile(raw, true)
	if err != nil {
		t.Fatalf("parse %s: %v", slug, err)
	}
	art.ArchivedAt = time.Now().AddDate(0, 0, -days)
	art.Timestamp = updated
	if art.Generated != nil {
		art.Generated.At = updated
	}
	// A distinct mtime, so a cache entry from the original write cannot mask the rewrite.
	writeWithMtime(t, path, []byte(serializeFrontMatter(art)+art.Content), time.Now().Add(-time.Hour))
}

// exists reports whether a slug still has a document.
func exists(t *testing.T, s *Storage, slug string) bool {
	t.Helper()
	_, err := s.GetArticle(slug)
	return err == nil
}

// logLines returns the captured log lines containing every fragment.
func logLines(buf *logCaptureBuffer, fragments ...string) []string {
	var lines []string
	for _, line := range strings.Split(buf.String(), "\n") {
		matched := line != ""
		for _, f := range fragments {
			matched = matched && strings.Contains(line, f)
		}
		if matched {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestCleanupArchivedKeepsLinkedArticle(t *testing.T) {
	s := newLifecycleStorage(t)
	t.Setenv(envAutoDeleteArchived, "30")
	saveArchived(t, s, "Linked Archive", "# kept")
	for _, linker := range []struct{ title, content string }{
		{"Wiki Pointer", "See [[Linked Archive]]."},
		{"Path Pointer", "See [it](/articles/linked-archive)."},
	} {
		if _, err := s.SaveArticle("", linker.title, linker.content, "", "", "", "", nil, ContentTypeWiki); err != nil {
			t.Fatalf("seed linker: %v", err)
		}
	}
	backdateArchived(t, s.ArticleDir, "linked-archive", 31, time.Now())
	buf := captureLog(t)

	if err := s.CleanupArchivedArticles(); err != nil {
		t.Fatalf("CleanupArchivedArticles failed: %v", err)
	}

	if !exists(t, s, "linked-archive") {
		t.Fatal("an archived article other documents link to must not be auto-deleted")
	}
	warnings := logLines(buf, "Warning: not auto-deleting archived article 'linked-archive'", "still linked from 2 documents: ", "wiki-pointer", "path-pointer")
	if len(warnings) != 1 {
		t.Errorf("want one warning naming both linkers with their count, got %q", buf.String())
	}
	if strings.Contains(buf.String(), "Deleted archived article") {
		t.Errorf("nothing may be deleted, got %q", buf.String())
	}
}

func TestCleanupArchivedDeletesUnlinkedArticle(t *testing.T) {
	s := newLifecycleStorage(t)
	t.Setenv(envAutoDeleteArchived, "30")
	saveArchived(t, s, "Unlinked Archive", "# gone")
	saveArchived(t, s, "Recent Archive", "# not due")
	if _, err := s.SaveAsset("unlinked-archive", "diagram.png", []byte("png")); err != nil {
		t.Fatalf("SaveAsset failed: %v", err)
	}
	backdateArchived(t, s.ArticleDir, "unlinked-archive", 31, time.Now())
	historyDir := filepath.Join(s.HistoryDir, "unlinked-archive")
	if _, err := os.Stat(historyDir); err != nil {
		t.Fatalf("expected a history directory to check its removal: %v", err)
	}
	buf := captureLog(t)

	if err := s.CleanupArchivedArticles(); err != nil {
		t.Fatalf("CleanupArchivedArticles failed: %v", err)
	}

	if exists(t, s, "unlinked-archive") {
		t.Error("an unlinked article archived past the delay must be deleted")
	}
	for _, dir := range []string{historyDir, filepath.Join(s.AssetDir, "unlinked-archive")} {
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("deletion must remove %s as DeleteArticle does, got %v", dir, err)
		}
	}
	if !exists(t, s, "recent-archive") {
		t.Error("an article archived for less than the delay must be kept")
	}
	if len(logLines(buf, "Deleted archived article: unlinked-archive (archived at: ")) != 1 {
		t.Errorf("a deletion must leave its audit line, got %q", buf.String())
	}
	if strings.Contains(buf.String(), "recent-archive") {
		t.Errorf("an article not yet due must not be mentioned, got %q", buf.String())
	}
}

// TestCleanupArchivedKeepsArticlesWhenScanSkippedUnreadable is deletePlan's guard at startup: a file
// the scan cannot parse may link to any document, so no archived document is provably unlinked
// until it is fixed or removed.
func TestCleanupArchivedKeepsArticlesWhenScanSkippedUnreadable(t *testing.T) {
	s := newLifecycleStorage(t)
	t.Setenv(envAutoDeleteArchived, "30")
	buf := captureLog(t)
	saveArchived(t, s, "Shadowed One", "# one")
	saveArchived(t, s, "Shadowed Two", "# two")
	backdateArchived(t, s.ArticleDir, "shadowed-one", 31, time.Now())
	backdateArchived(t, s.ArticleDir, "shadowed-two", 31, time.Now().Add(-time.Minute))
	broken := filepath.Join(s.ArticleDir, "broken.md")
	writeWithMtime(t, broken, []byte("---\ntitle: [unclosed\n---\nSee [[Shadowed One]].\n"), time.Now().Add(-time.Hour))

	if err := s.CleanupArchivedArticles(); err != nil {
		t.Fatalf("CleanupArchivedArticles failed: %v", err)
	}

	for _, slug := range []string{"shadowed-one", "shadowed-two"} {
		if !exists(t, s, slug) {
			t.Errorf("%s must not be deleted while the backlink scan skipped a file that could link to it", slug)
		}
		want := "Warning: not auto-deleting archived article '" + slug + "'"
		if len(logLines(buf, want, "the backlink scan skipped 1 unreadable entry that may link to it")) != 1 {
			t.Errorf("%s must be warned about with the skipped count, got %q", slug, buf.String())
		}
	}

	// With the file gone the scan is complete, so the next startup deletes both.
	if err := os.Remove(broken); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if err := s.CleanupArchivedArticles(); err != nil {
		t.Fatalf("CleanupArchivedArticles failed: %v", err)
	}
	for _, slug := range []string{"shadowed-one", "shadowed-two"} {
		if exists(t, s, slug) {
			t.Errorf("%s must be deleted once the backlink scan is complete", slug)
		}
	}
}

// TestCleanupArchivedKeepsArticleWithMisplacedLinker pins that a misplaced document keeps only the
// documents it links to: it is no backlink, but deleting its target would still break its link.
func TestCleanupArchivedKeepsArticleWithMisplacedLinker(t *testing.T) {
	s := newLifecycleStorage(t)
	t.Setenv(envAutoDeleteArchived, "30")
	buf := captureLog(t)
	saveArchived(t, s, "Misplaced Target", "# kept")
	saveArchived(t, s, "Free Target", "# gone")
	backdateArchived(t, s.ArticleDir, "misplaced-target", 31, time.Now())
	backdateArchived(t, s.ArticleDir, "free-target", 31, time.Now().Add(-time.Minute))
	writeWithMtime(t, filepath.Join(s.ArticleDir, "sub", "pointer.md"),
		[]byte("---\ntitle: Pointer\nslug: pointer\n---\nSee [[Misplaced Target]].\n"), time.Now().Add(-time.Hour))
	writeWithMtime(t, filepath.Join(s.ArticleDir, "copy.md"),
		[]byte("---\ntitle: Original\nslug: original\n---\nNo links to archived documents.\n"), time.Now().Add(-time.Hour))

	if err := s.CleanupArchivedArticles(); err != nil {
		t.Fatalf("CleanupArchivedArticles failed: %v", err)
	}

	if !exists(t, s, "misplaced-target") {
		t.Error("an archived article a misplaced document links to must not be auto-deleted")
	}
	if len(logLines(buf, "Warning: not auto-deleting archived article 'misplaced-target'", "1 misplaced document links to it")) != 1 {
		t.Errorf("the kept article must be warned about with the misplaced linker count, got %q", buf.String())
	}
	if exists(t, s, "free-target") {
		t.Error("a misplaced document that does not link to an article must not keep it")
	}
	if strings.Contains(buf.String(), "not auto-deleting archived article 'free-target'") {
		t.Errorf("free-target must not be refused, got %q", buf.String())
	}
}

// TestCleanupArchivedLeavesPlansToLifecycleWorker pins that startup cleanup never deletes a plan,
// however long archived and however unlinked: the lifecycle worker owns that on its own timer.
func TestCleanupArchivedLeavesPlansToLifecycleWorker(t *testing.T) {
	s := newLifecycleStorage(t)
	t.Setenv(envAutoDeleteArchived, "30")
	buf := captureLog(t)
	savePlan(t, s, "Old Plan One", StatusArchived)
	savePlan(t, s, "Old Plan Two", StatusArchived)
	saveArchived(t, s, "Old Article", "# gone")
	for _, slug := range []string{"old-plan-one", "old-plan-two", "old-article"} {
		backdateArchived(t, s.ArticleDir, slug, 400, time.Now())
	}

	if err := s.CleanupArchivedArticles(); err != nil {
		t.Fatalf("CleanupArchivedArticles failed: %v", err)
	}

	for _, slug := range []string{"old-plan-one", "old-plan-two"} {
		if !exists(t, s, slug) {
			t.Errorf("startup cleanup must leave the archived plan %s to the plan lifecycle worker", slug)
		}
		if strings.Contains(buf.String(), slug) {
			t.Errorf("a skipped plan must not be logged by name, got %q", buf.String())
		}
	}
	if exists(t, s, "old-article") {
		t.Error("an unlinked archived article past the delay must still be deleted")
	}
	if lines := logLines(buf, "plan lifecycle worker"); len(lines) != 1 || !strings.Contains(lines[0], "leaving 2 archived plans") {
		t.Errorf("want one summary line counting the skipped plans, got %q", buf.String())
	}
}

// TestCleanupArchivedContinuesPastFailedDelete pins that one document that cannot be deleted neither
// stops the rest of the cleanup nor fails startup. The failure is an asset directory whose contents
// cannot be removed.
func TestCleanupArchivedContinuesPastFailedDelete(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(envAutoDeleteArchived, "")
	seed, err := NewStorage(dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	now := time.Now()
	for i, title := range []string{"First Due", "Stuck Due", "Last Due"} {
		art := saveArchived(t, seed, title, "# due")
		backdateArchived(t, seed.ArticleDir, art.Slug, 10, now.Add(-time.Duration(i)*time.Minute))
	}
	if _, err := seed.SaveAsset("stuck-due", "diagram.png", []byte("png")); err != nil {
		t.Fatalf("SaveAsset failed: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	stuckAssets := filepath.Join(dataDir, "assets", "stuck-due")
	if err := os.Chmod(stuckAssets, 0555); err != nil {
		t.Skipf("cannot chmod on this platform: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(stuckAssets, 0755) })
	probe := filepath.Join(stuckAssets, "probe")
	if err := os.WriteFile(probe, nil, 0644); err == nil {
		_ = os.Remove(probe)
		t.Skip("removing write permission does not stop changes to a directory here (running as root?)")
	}

	t.Setenv(envAutoDeleteArchived, "1")
	buf := captureLog(t)
	s, err := NewStorage(dataDir)
	if err != nil {
		t.Fatalf("a failed cleanup deletion must not fail startup: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	failed := strings.Index(buf.String(), "Warning: failed to delete archived article 'stuck-due', continuing with the rest")
	last := strings.Index(buf.String(), "Deleted archived article: last-due")
	if failed < 0 || last < failed {
		t.Errorf("want the failure logged and the next document deleted after it, got %q", buf.String())
	}
	for _, slug := range []string{"first-due", "last-due"} {
		if exists(t, s, slug) {
			t.Errorf("%s must be deleted despite the failure on stuck-due", slug)
		}
	}
	if _, err := os.Stat(filepath.Join(stuckAssets, "diagram.png")); err != nil {
		t.Errorf("the failed deletion should have left its asset behind: %v", err)
	}
}

// TestCleanupArchivedCountsItsOwnDeletions pins that the single scan is judged as a scan made at
// each deletion would be: a linker this cleanup already deleted no longer keeps its target, and one
// it has not reached yet still does.
func TestCleanupArchivedCountsItsOwnDeletions(t *testing.T) {
	s := newLifecycleStorage(t)
	t.Setenv(envAutoDeleteArchived, "30")
	buf := captureLog(t)
	now := time.Now()
	saveArchived(t, s, "Chain Target", "# target")
	saveArchived(t, s, "Chain Linker", "See [[Chain Target]].")
	saveArchived(t, s, "Late Target", "# target")
	saveArchived(t, s, "Late Linker", "See [[Late Target]].")
	// Listing order: chain-linker, chain-target, late-target, late-linker.
	backdateArchived(t, s.ArticleDir, "chain-linker", 31, now)
	backdateArchived(t, s.ArticleDir, "chain-target", 31, now.Add(-1*time.Minute))
	backdateArchived(t, s.ArticleDir, "late-target", 31, now.Add(-2*time.Minute))
	backdateArchived(t, s.ArticleDir, "late-linker", 31, now.Add(-3*time.Minute))

	if err := s.CleanupArchivedArticles(); err != nil {
		t.Fatalf("CleanupArchivedArticles failed: %v", err)
	}

	for _, slug := range []string{"chain-linker", "chain-target", "late-linker"} {
		if exists(t, s, slug) {
			t.Errorf("%s must be deleted", slug)
		}
	}
	if !exists(t, s, "late-target") {
		t.Error("late-target is linked from late-linker when it is reached, so it must be kept")
	}
	if len(logLines(buf, "not auto-deleting archived article 'late-target'", "still linked from 1 document: late-linker")) != 1 {
		t.Errorf("late-target must be refused naming its linker, got %q", buf.String())
	}
}

func TestCleanupArchivedDisabled(t *testing.T) {
	for _, value := range []string{"", "0", "-5"} {
		t.Run("value="+value, func(t *testing.T) {
			s := newLifecycleStorage(t)
			saveArchived(t, s, "Old Archive", "# kept")
			backdateArchived(t, s.ArticleDir, "old-archive", 400, time.Now())
			t.Setenv(envAutoDeleteArchived, value)
			buf := captureLog(t)

			if err := s.CleanupArchivedArticles(); err != nil {
				t.Fatalf("CleanupArchivedArticles failed: %v", err)
			}
			if !exists(t, s, "old-archive") {
				t.Error("disabled cleanup must delete nothing")
			}
			if buf.String() != "" {
				t.Errorf("disabled cleanup must log nothing, got %q", buf.String())
			}
		})
	}
}

// TestCleanupArchivedInvalidSettingDoesNotBlockStartup pins that a bad value deletes nothing and
// still lets the wiki start.
func TestCleanupArchivedInvalidSettingDoesNotBlockStartup(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(envAutoDeleteArchived, "")
	seed, err := NewStorage(dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	saveArchived(t, seed, "Old Archive", "# kept")
	backdateArchived(t, seed.ArticleDir, "old-archive", 400, time.Now())
	if err := seed.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	t.Setenv(envAutoDeleteArchived, "thirty")
	s, err := NewStorage(dataDir)
	if err != nil {
		t.Fatalf("an invalid auto-delete setting must not fail startup: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if !exists(t, s, "old-archive") {
		t.Error("an invalid auto-delete setting must delete nothing")
	}
	if err := s.CleanupArchivedArticles(); err == nil || !strings.Contains(err.Error(), envAutoDeleteArchived) {
		t.Errorf("CleanupArchivedArticles must report the invalid setting, got %v", err)
	}
}

func TestCleanupArchivedStopsOnClosedStorage(t *testing.T) {
	s := newLifecycleStorage(t)
	t.Setenv(envAutoDeleteArchived, "30")
	captureLog(t)
	saveArchived(t, s, "Closed Archive", "# kept")
	backdateArchived(t, s.ArticleDir, "closed-archive", 31, time.Now())
	if err := s.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err := s.CleanupArchivedArticles(); !errors.Is(err, ErrStorageClosed) {
		t.Errorf("cleanup on closed storage must stop with ErrStorageClosed, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.ArticleDir, "closed-archive.md")); err != nil {
		t.Errorf("closed storage must not delete anything: %v", err)
	}
}

// TestScanBacklinksToEachMatchesScanBacklinks pins the batched scan to scanBacklinks, slug by slug,
// across every case the guard depends on.
func TestScanBacklinksToEachMatchesScanBacklinks(t *testing.T) {
	s := newLifecycleStorage(t)
	captureLog(t) // the scans' own skip warnings
	old := time.Now().Add(-time.Hour)
	write := func(rel, content string) {
		writeWithMtime(t, filepath.Join(s.ArticleDir, filepath.FromSlash(rel)), []byte(content), old)
	}
	write("alpha.md", "---\ntitle: Alpha\nslug: alpha\ntimestamp: 2026-01-01T00:00:00Z\n---\nSee [[Beta]] and [[Alpha]].\n")
	write("beta.md", "---\ntitle: Beta\nslug: beta\ntimestamp: 2026-01-02T00:00:00Z\n---\nNothing.\n")
	write("gamma.md", "---\ntitle: Gamma\nslug: gamma\ntimestamp: 2026-01-03T00:00:00Z\n---\nNothing.\n")
	write("one.md", "---\ntitle: One\nslug: one\ntimestamp: 2026-01-04T00:00:00Z\n---\n[[Alpha]], [[Alpha|again]], [x](/articles/beta).\n")
	write("two.md", "---\ntitle: Two\nslug: two\ntimestamp: 2026-01-05T00:00:00Z\n---\n[[Alpha]] and `[[Gamma]]` in code.\n")
	write("sub/nested.md", "---\ntitle: Nested\nslug: nested\n---\n[[Alpha]] and [[Gamma]].\n")
	write("gamma-copy.md", "---\ntitle: Gamma\nslug: gamma\n---\nA copy linking to [[Gamma]].\n")
	write("broken.md", "---\ntitle: [unclosed\n---\n[[Beta]].\n")

	targets := []string{"alpha", "beta", "gamma", "missing", "!!!"}
	compare := func(t *testing.T) {
		t.Helper()
		batch, err := s.scanBacklinksToEach(targets)
		if err != nil {
			t.Fatalf("scanBacklinksToEach failed: %v", err)
		}
		if len(batch) != len(targets) {
			t.Errorf("want a scan for each of %d slugs, got %d", len(targets), len(batch))
		}
		for _, slug := range targets {
			single, err := s.scanBacklinks(slug)
			if err != nil {
				t.Fatalf("scanBacklinks(%q) failed: %v", slug, err)
			}
			if !reflect.DeepEqual(batch[slug], single) {
				t.Errorf("scan for %q differs:\nbatch  %+v\nsingle %+v", slug, batch[slug], single)
			}
		}
	}
	compare(t)

	// Spot-check the fixture exercises what it claims to, so a match is not two empty scans.
	alpha, _ := s.scanBacklinks("alpha")
	if len(alpha.backlinks) != 2 || len(alpha.misplaced) != 1 || len(alpha.unreadable) != 1 {
		t.Errorf("fixture: alpha scan = %+v, want 2 backlinks, 1 misplaced linker, 1 unreadable", alpha)
	}

	// A target whose own body cannot be read is skipped by its own scan but not by the others'. A new
	// version is listed first, so its metadata is cached and only the body read fails.
	t.Run("unreadable target body", func(t *testing.T) {
		writeWithMtime(t, filepath.Join(s.ArticleDir, "gamma.md"),
			[]byte("---\ntitle: Gamma\nslug: gamma\ntimestamp: 2026-01-03T00:00:00Z\n---\nNothing.\n"), old.Add(time.Minute))
		if _, err := s.ListArticles(); err != nil {
			t.Fatalf("ListArticles failed: %v", err)
		}
		makeUnreadable(t, filepath.Join(s.ArticleDir, "gamma.md"))
		gamma, err := s.scanBacklinks("gamma")
		if err != nil {
			t.Fatalf("scanBacklinks failed: %v", err)
		}
		beta, err := s.scanBacklinks("beta")
		if err != nil {
			t.Fatalf("scanBacklinks failed: %v", err)
		}
		if len(beta.unreadable) != len(gamma.unreadable)+1 {
			t.Fatalf("fixture: beta's scan should count gamma.md as unreadable and gamma's should not, got %+v and %+v", beta.unreadable, gamma.unreadable)
		}
		compare(t)
	})
}

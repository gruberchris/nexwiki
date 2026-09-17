package server

import (
	"bytes"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"
)

// lockedBuffer is a log sink safe to read while something else might still be logging.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog redirects the standard logger into a buffer for the rest of the test, with flags
// cleared so each line starts with the message itself.
func captureLog(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return buf
}

// unreadableWarnings returns the logged warning lines that name relPath.
func unreadableWarnings(t *testing.T, buf *lockedBuffer, relPath string) []string {
	t.Helper()
	var lines []string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, relPath) {
			if !strings.HasPrefix(line, "Warning:") {
				t.Errorf("log line naming %s does not start with Warning: %q", relPath, line)
			}
			lines = append(lines, line)
		}
	}
	return lines
}

// writeWithMtime writes a file and pins its modification time, so a rewrite is guaranteed to look
// like a new version even on filesystems with coarse timestamps.
func writeWithMtime(t *testing.T, path string, content []byte, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("Chtimes failed: %v", err)
	}
}

// newUnreadableFixture builds a storage with two good articles and one file with malformed front
// matter, returning the storage and the broken file's absolute path.
func newUnreadableFixture(t *testing.T) (*Storage, string) {
	t.Helper()
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	for _, title := range []string{"Good One", "Good Two"} {
		if _, err := storage.SaveArticle("", title, "body linking [[Good One]]", "", "", "", "seed", nil, ""); err != nil {
			t.Fatalf("SaveArticle failed: %v", err)
		}
	}

	broken := filepath.Join(storage.ArticleDir, "broken.md")
	writeWithMtime(t, broken, []byte("---\ntitle: [unclosed\n---\nbody\n"), time.Now().Add(-time.Hour))
	return storage, broken
}

func TestListArticlesSkipsUnreadableFile(t *testing.T) {
	storage, _ := newUnreadableFixture(t)
	captureLog(t)

	articles, err := storage.ListArticles()
	if err != nil {
		t.Fatalf("one broken file must not fail the listing: %v", err)
	}
	var slugs []string
	for _, a := range articles {
		slugs = append(slugs, a.Slug)
	}
	if len(articles) != 2 || !contains(slugs, "good-one") || !contains(slugs, "good-two") {
		t.Errorf("expected exactly the two good articles, got %v", slugs)
	}
}

// TestUnreadableFileWarnsOncePerVersion pins the dedup: every listing and scan rescans the wiki, so
// a warning per scan would flood stderr, but a new broken version is new information.
func TestUnreadableFileWarnsOncePerVersion(t *testing.T) {
	storage, broken := newUnreadableFixture(t)
	buf := captureLog(t)

	scanAll := func() {
		t.Helper()
		for i := 0; i < 3; i++ {
			if _, err := storage.ListArticles(); err != nil {
				t.Fatalf("ListArticles failed: %v", err)
			}
			if _, err := storage.ScanLinkGraph(); err != nil {
				t.Fatalf("ScanLinkGraph failed: %v", err)
			}
			if _, err := storage.GetBacklinks("good-one"); err != nil {
				t.Fatalf("GetBacklinks failed: %v", err)
			}
		}
	}

	scanAll()
	warnings := unreadableWarnings(t, buf, "broken.md")
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning across repeated scans, got %d: %q", len(warnings), warnings)
	}
	if strings.Contains(warnings[0], storage.ArticleDir) {
		t.Errorf("warning should name the path relative to the article directory: %q", warnings[0])
	}
	if !strings.Contains(warnings[0], "YAML") {
		t.Errorf("warning should carry the parse error: %q", warnings[0])
	}

	// A different broken version is new information and warns once more.
	writeWithMtime(t, broken, []byte("---\ntitle: [still unclosed, and longer\n---\nbody\n"), time.Now().Add(time.Hour))
	scanAll()
	if warnings := unreadableWarnings(t, buf, "broken.md"); len(warnings) != 2 {
		t.Fatalf("expected one more warning after the file changed, got %d total: %q", len(warnings), warnings)
	}
}

// TestScanLinkGraphReportsUnreadable pins that a skipped file is visible in the graph, and that
// fixing it both clears the report and forgets the failure, so breaking it again warns again.
func TestScanLinkGraphReportsUnreadable(t *testing.T) {
	storage, broken := newUnreadableFixture(t)
	buf := captureLog(t)

	brokenBytes, err := os.ReadFile(broken)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	brokenInfo, err := os.Stat(broken)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	nested := filepath.Join(storage.ArticleDir, "archive", "no-header.md")
	writeWithMtime(t, nested, []byte("no front matter at all\n"), time.Now().Add(-time.Hour))

	graph, err := storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	if len(graph.Unreadable) != 2 {
		t.Fatalf("expected two unreadable files, got %+v", graph.Unreadable)
	}
	// Sorted by path, relative and slash-separated.
	if graph.Unreadable[0].Path != "archive/no-header.md" || graph.Unreadable[1].Path != "broken.md" {
		t.Errorf("unexpected unreadable paths or order: %+v", graph.Unreadable)
	}
	for _, f := range graph.Unreadable {
		if f.Error == "" {
			t.Errorf("unreadable file %s has no error", f.Path)
		}
	}
	if _, ok := graph.Meta["good-one"]; !ok {
		t.Errorf("good articles must still be in the graph: %v", graph.Meta)
	}

	// Fix both files: one gets valid front matter, the other is deleted.
	fixed := "---\ntitle: Broken Fixed\nslug: broken\n---\nnow links to [[Good One]]\n"
	writeWithMtime(t, broken, []byte(fixed), time.Now().Add(time.Hour))
	if err := os.Remove(nested); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	graph, err = storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	if graph.Unreadable == nil || len(graph.Unreadable) != 0 {
		t.Errorf("expected an empty, non-nil unreadable list after the fix, got %#v", graph.Unreadable)
	}
	if _, ok := graph.Meta["broken"]; !ok {
		t.Errorf("fixed article should appear in the graph: %v", graph.Meta)
	}
	articles, err := storage.ListArticles()
	if err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	found := false
	for _, a := range articles {
		found = found || a.Slug == "broken"
	}
	if !found {
		t.Errorf("fixed article should appear in the listing")
	}

	// ListArticles pruned the deleted file and the fix forgot the other failure.
	storage.cache.mu.Lock()
	remaining := len(storage.cache.failures)
	storage.cache.mu.Unlock()
	if remaining != 0 {
		t.Errorf("expected no recorded failures after fix and delete, got %d", remaining)
	}

	// Breaking it again warns again, even when the broken bytes and mtime match the version that
	// already warned: the fix, not a changed fingerprint, is what reset it.
	writeWithMtime(t, broken, brokenBytes, brokenInfo.ModTime())
	if _, err := storage.ListArticles(); err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	if warnings := unreadableWarnings(t, buf, "broken.md"); len(warnings) != 2 {
		t.Errorf("expected a second warning after the fixed file broke again, got %d: %q", len(warnings), warnings)
	}
}

// TestUnreadableWarningIsOnceUnderConcurrency pins that concurrent scans, as the HTTP and MCP
// handlers run them, still agree on a single warning. Run with -race.
func TestUnreadableWarningIsOnceUnderConcurrency(t *testing.T) {
	storage, _ := newUnreadableFixture(t)
	buf := captureLog(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if _, err := storage.ListArticles(); err != nil {
					t.Errorf("ListArticles failed: %v", err)
					return
				}
				if _, err := storage.ScanLinkGraph(); err != nil {
					t.Errorf("ScanLinkGraph failed: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if warnings := unreadableWarnings(t, buf, "broken.md"); len(warnings) != 1 {
		t.Errorf("expected exactly one warning under concurrent scans, got %d: %q", len(warnings), warnings)
	}
}

// TestUnreadableDistinguishesVanishedFromDangling pins the one error skipUnreadable deliberately
// keeps quiet: a file deleted or renamed mid-walk is gone, not broken. A dangling symlink fails with
// the same not-exist error but is still sitting in the directory, so it must still be reported.
func TestUnreadableDistinguishesVanishedFromDangling(t *testing.T) {
	storage, broken := newUnreadableFixture(t)
	buf := captureLog(t)

	info, err := os.Stat(broken)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	gone := filepath.Join(storage.ArticleDir, "gone.md")
	if _, ok := storage.skipUnreadable(gone, info, &os.PathError{Op: "open", Path: gone, Err: os.ErrNotExist}); ok {
		t.Errorf("a file that vanished mid-walk must not be reported as unreadable")
	}
	if warnings := unreadableWarnings(t, buf, "gone.md"); len(warnings) != 0 {
		t.Errorf("a file that vanished mid-walk must not be logged, got %q", warnings)
	}

	dangling := filepath.Join(storage.ArticleDir, "dangling.md")
	if err := os.Symlink(filepath.Join(storage.ArticleDir, "no-such-target.md"), dangling); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	graph, err := storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	var paths []string
	for _, f := range graph.Unreadable {
		paths = append(paths, f.Path)
	}
	if !reflect.DeepEqual(paths, []string{"broken.md", "dangling.md"}) {
		t.Errorf("expected the broken file and the dangling symlink, got %v", paths)
	}
	if warnings := unreadableWarnings(t, buf, "dangling.md"); len(warnings) != 1 {
		t.Errorf("expected one warning for the dangling symlink, got %q", warnings)
	}
}

// makeUnreadable removes every permission from a file for the rest of the test. A read failure is
// what these tests need, so the test is skipped where that does not produce one: as root, or on a
// platform without Unix permissions.
func makeUnreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0); err != nil {
		t.Skipf("cannot chmod on this platform: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0644) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("removing permissions does not stop reads here (running as root?)")
	}
}

// inSearchIndex reports whether the search index holds a document with this slug.
func inSearchIndex(t *testing.T, storage *Storage, slug string) bool {
	t.Helper()
	res, err := storage.SearchIndex.Search(bleve.NewSearchRequest(bleve.NewDocIDQuery([]string{slug})))
	if err != nil {
		t.Fatalf("index lookup for %s failed: %v", slug, err)
	}
	return res.Total == 1
}

// TestFindAssetReferrersWarnsOnceForUnreadableFile pins that the rename-time asset scan reports a
// file it cannot read, once per version and sharing that record with the other walks, and still
// returns every referrer it can read. It matches raw text, so malformed front matter does not hide
// a referrer.
func TestFindAssetReferrersWarnsOnceForUnreadableFile(t *testing.T) {
	storage, _ := newUnreadableFixture(t)
	buf := captureLog(t)

	embed := "![diagram](/api/assets/good-one/diagram.png)"
	for _, title := range []string{"Embeds Diagram", "Locked Embed"} {
		if _, err := storage.SaveArticle("", title, embed, "", "", "", "seed", nil, ""); err != nil {
			t.Fatalf("SaveArticle failed: %v", err)
		}
	}
	writeWithMtime(t, filepath.Join(storage.ArticleDir, "malformed-embed.md"),
		[]byte("---\ntitle: [unclosed\n---\n"+embed+"\n"), time.Now().Add(-time.Hour))
	makeUnreadable(t, filepath.Join(storage.ArticleDir, "locked-embed.md"))

	want := []string{"embeds-diagram", "malformed-embed"}
	for i := 0; i < 3; i++ {
		got, err := storage.findAssetReferrers("good-one")
		if err != nil {
			t.Fatalf("findAssetReferrers failed: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("findAssetReferrers = %v, want %v", got, want)
		}
	}
	warnings := unreadableWarnings(t, buf, "locked-embed.md")
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning across repeated scans, got %d: %q", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "permission denied") {
		t.Errorf("warning should carry the read error: %q", warnings[0])
	}

	// A listing reaches the same file version and must not warn about it a second time.
	if _, err := storage.ListArticles(); err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	if warnings := unreadableWarnings(t, buf, "locked-embed.md"); len(warnings) != 1 {
		t.Errorf("a listing after the asset scan warned again, got %d: %q", len(warnings), warnings)
	}
}

// TestSyncSearchIndexWarnsOnceForUnreadableFile pins that boot indexing reports a document it
// listed but could not read, home included, once per version, and still indexes everything else.
// The listing serves both files from the metadata cache, which a permission change does not
// invalidate, so the full read in the sync is the first to fail.
func TestSyncSearchIndexWarnsOnceForUnreadableFile(t *testing.T) {
	storage, _ := newUnreadableFixture(t)
	buf := captureLog(t)

	// Cache every file while it is still readable.
	if _, err := storage.ListArticles(); err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	makeUnreadable(t, filepath.Join(storage.ArticleDir, "good-two.md"))
	makeUnreadable(t, filepath.Join(storage.ArticleDir, "home.md"))

	// Drop a healthy article from the index so the sync has to put it back.
	if err := storage.UnindexArticle("good-one"); err != nil {
		t.Fatalf("UnindexArticle failed: %v", err)
	}
	if inSearchIndex(t, storage, "good-one") {
		t.Fatalf("good-one should be out of the index before the sync")
	}

	for i := 0; i < 3; i++ {
		if err := storage.SyncSearchIndex(); err != nil {
			t.Fatalf("SyncSearchIndex failed: %v", err)
		}
	}

	for _, name := range []string{"good-two.md", "home.md"} {
		warnings := unreadableWarnings(t, buf, name)
		if len(warnings) != 1 {
			t.Errorf("expected exactly one warning for %s across repeated syncs, got %d: %q", name, len(warnings), warnings)
			continue
		}
		if !strings.Contains(warnings[0], "permission denied") {
			t.Errorf("warning should carry the read error: %q", warnings[0])
		}
	}

	if !inSearchIndex(t, storage, "good-one") {
		t.Errorf("the healthy article should be indexed again")
	}
	// A skipped document is still a valid slug, so its earlier entry is kept, not treated as an
	// orphan.
	for _, slug := range []string{"good-two", "home"} {
		if !inSearchIndex(t, storage, slug) {
			t.Errorf("the skipped document %s should keep its existing index entry", slug)
		}
	}
}

// lockDir removes every permission from a directory for the rest of the test. Like makeUnreadable,
// the test is skipped where that does not stop the directory being listed.
func lockDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0); err != nil {
		t.Skipf("cannot chmod on this platform: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("removing permissions does not stop listing a directory here (running as root?)")
	}
}

// unreadablePaths returns the paths a link graph reports as unreadable, failing the test for any
// entry that carries no error.
func unreadablePaths(t *testing.T, graph *LinkGraph) []string {
	t.Helper()
	var paths []string
	for _, f := range graph.Unreadable {
		if f.Error == "" {
			t.Errorf("unreadable entry %s has no error", f.Path)
		}
		paths = append(paths, f.Path)
	}
	return paths
}

// TestUnreadableSubdirectoryIsSkipped pins that a folder the walks cannot list costs only that
// folder: every scan still succeeds with everything outside it, the folder is reported to health
// checks, and it warns once for as long as it stays unreadable.
func TestUnreadableSubdirectoryIsSkipped(t *testing.T) {
	storage, _ := newUnreadableFixture(t)
	buf := captureLog(t)

	body := "![diagram](/api/assets/good-one/diagram.png) and [[Good One]]\n"
	writeWithMtime(t, filepath.Join(storage.ArticleDir, "open", "nested.md"),
		[]byte("---\ntitle: Nested\nslug: nested\n---\n"+body), time.Now().Add(-time.Hour))
	locked := filepath.Join(storage.ArticleDir, "locked")
	writeWithMtime(t, filepath.Join(locked, "hidden.md"),
		[]byte("---\ntitle: Hidden\nslug: hidden\n---\n"+body), time.Now().Add(-time.Hour))
	lockDir(t, locked)

	listed := func() []string {
		t.Helper()
		articles, err := storage.ListArticles()
		if err != nil {
			t.Fatalf("an unreadable subdirectory must not fail the listing: %v", err)
		}
		var slugs []string
		for _, a := range articles {
			slugs = append(slugs, a.Slug)
		}
		sort.Strings(slugs)
		return slugs
	}

	for i := 0; i < 3; i++ {
		if got, want := listed(), []string{"good-one", "good-two", "nested"}; !reflect.DeepEqual(got, want) {
			t.Errorf("ListArticles = %v, want %v", got, want)
		}

		graph, err := storage.ScanLinkGraph()
		if err != nil {
			t.Fatalf("an unreadable subdirectory must not fail the link graph: %v", err)
		}
		if _, ok := graph.Meta["nested"]; !ok {
			t.Errorf("articles outside the unreadable folder must still be in the graph: %v", graph.Meta)
		}
		if got, want := unreadablePaths(t, graph), []string{"broken.md", "locked/"}; !reflect.DeepEqual(got, want) {
			t.Errorf("Unreadable paths = %v, want %v", got, want)
		} else if !strings.Contains(graph.Unreadable[1].Error, "permission denied") {
			t.Errorf("the folder's entry should carry the listing error: %+v", graph.Unreadable[1])
		}

		backlinks, err := storage.GetBacklinks("good-one")
		if err != nil {
			t.Fatalf("an unreadable subdirectory must not fail a backlink scan: %v", err)
		}
		var from []string
		for _, a := range backlinks {
			from = append(from, a.Slug)
		}
		if !contains(from, "nested") || contains(from, "hidden") {
			t.Errorf("backlinks should come from outside the unreadable folder only, got %v", from)
		}

		referrers, err := storage.findAssetReferrers("good-one")
		if err != nil {
			t.Fatalf("an unreadable subdirectory must not fail the asset scan: %v", err)
		}
		if want := []string{"nested"}; !reflect.DeepEqual(referrers, want) {
			t.Errorf("findAssetReferrers = %v, want %v", referrers, want)
		}
	}

	warnings := unreadableWarnings(t, buf, "locked/")
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning across repeated scans, got %d: %q", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "directory locked/:") {
		t.Errorf("warning should name the folder relative to the article directory: %q", warnings[0])
	}

	// Once the folder can be listed again its articles come back and a listing forgets the
	// failure, so losing access again warns again.
	if err := os.Chmod(locked, 0755); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	if got := listed(); !contains(got, "hidden") {
		t.Errorf("the folder's articles should be listed once it is readable, got %v", got)
	}
	graph, err := storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	if got, want := unreadablePaths(t, graph), []string{"broken.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Unreadable paths after recovery = %v, want %v", got, want)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	listed()
	if warnings := unreadableWarnings(t, buf, "locked/"); len(warnings) != 2 {
		t.Errorf("expected a second warning after the folder became unreadable again, got %d: %q", len(warnings), warnings)
	}
}

// TestUnstattableArticleFileIsSkipped pins the other per-entry walk error: a file its directory
// lists but that cannot then be stat'd, which a directory granting read but not search permission
// produces. The file is skipped and warned about once. Its cached parse still matches the file,
// so the failure has to be forgotten some other way once the file is reachable again; this pins
// that it is, and that losing access again warns again.
func TestUnstattableArticleFileIsSkipped(t *testing.T) {
	storage, _ := newUnreadableFixture(t)
	buf := captureLog(t)

	dir := filepath.Join(storage.ArticleDir, "no-search")
	file := filepath.Join(dir, "listed.md")
	writeWithMtime(t, file, []byte("---\ntitle: Listed\nslug: listed\n---\nbody\n"), time.Now().Add(-time.Hour))

	listed := func() bool {
		t.Helper()
		articles, err := storage.ListArticles()
		if err != nil {
			t.Fatalf("a file that cannot be stat'd must not fail the listing: %v", err)
		}
		for _, a := range articles {
			if a.Slug == "listed" {
				return true
			}
		}
		return false
	}
	if !listed() { // also caches the file's parse
		t.Fatalf("the file should be listed while it is reachable")
	}

	if err := os.Chmod(dir, 0600); err != nil {
		t.Skipf("cannot chmod on this platform: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	if _, err := os.ReadDir(dir); err != nil {
		t.Skipf("a directory without search permission cannot be listed here: %v", err)
	}
	if _, err := os.Lstat(file); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Skipf("removing search permission does not stop a stat here (running as root?): %v", err)
	}

	for i := 0; i < 3; i++ {
		if listed() {
			t.Errorf("a file that cannot be stat'd must not be listed")
		}
		graph, err := storage.ScanLinkGraph()
		if err != nil {
			t.Fatalf("a file that cannot be stat'd must not fail the link graph: %v", err)
		}
		if got, want := unreadablePaths(t, graph), []string{"broken.md", "no-search/listed.md"}; !reflect.DeepEqual(got, want) {
			t.Errorf("Unreadable paths = %v, want %v", got, want)
		}
		if _, err := storage.GetBacklinks("good-one"); err != nil {
			t.Fatalf("a file that cannot be stat'd must not fail a backlink scan: %v", err)
		}
		if _, err := storage.findAssetReferrers("good-one"); err != nil {
			t.Fatalf("a file that cannot be stat'd must not fail the asset scan: %v", err)
		}
	}
	warnings := unreadableWarnings(t, buf, "no-search/listed.md")
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning across repeated scans, got %d: %q", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "permission denied") {
		t.Errorf("warning should carry the stat error: %q", warnings[0])
	}

	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	if !listed() {
		t.Errorf("the file should be listed again once it can be stat'd")
	}
	if err := os.Chmod(dir, 0600); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	listed()
	if warnings := unreadableWarnings(t, buf, "no-search/listed.md"); len(warnings) != 2 {
		t.Errorf("expected a second warning after the file became unreachable again, got %d: %q", len(warnings), warnings)
	}
}

// TestUnreadableArticleRootFailsScans pins the one walk error that must still fail a scan. A data
// directory that is missing or cannot be read is broken, and reporting it as an empty wiki would
// hide that.
func TestUnreadableArticleRootFailsScans(t *testing.T) {
	scans := []struct {
		name string
		run  func(*Storage) error
	}{
		{"ListArticles", func(s *Storage) error { _, err := s.ListArticles(); return err }},
		{"ScanLinkGraph", func(s *Storage) error { _, err := s.ScanLinkGraph(); return err }},
		{"GetBacklinks", func(s *Storage) error { _, err := s.GetBacklinks("good-one"); return err }},
		{"findAssetReferrers", func(s *Storage) error { _, err := s.findAssetReferrers("good-one"); return err }},
	}
	expectFailures := func(t *testing.T, storage *Storage) {
		t.Helper()
		for _, scan := range scans {
			if err := scan.run(storage); err == nil {
				t.Errorf("%s: expected an error", scan.name)
			}
		}
	}

	t.Run("missing", func(t *testing.T) {
		storage, _ := newUnreadableFixture(t)
		captureLog(t)
		if err := os.RemoveAll(storage.ArticleDir); err != nil {
			t.Fatalf("RemoveAll failed: %v", err)
		}
		expectFailures(t, storage)
	})

	t.Run("unreadable", func(t *testing.T) {
		storage, _ := newUnreadableFixture(t)
		captureLog(t)
		lockDir(t, storage.ArticleDir)
		expectFailures(t, storage)
	})
}

// TestSkipWalkErrorClassification pins how the walks classify an entry they cannot stat or list.
// An entry deleted or renamed between the directory read and its stat or listing is a race no
// test can trigger on demand, so the classification is checked directly: it is gone, not broken,
// and is skipped with no warning and no report. Any other error below the root is reported once,
// and the root itself fails the scan either way.
func TestSkipWalkErrorClassification(t *testing.T) {
	storage, broken := newUnreadableFixture(t)
	buf := captureLog(t)

	dirInfo, err := os.Stat(storage.ArticleDir)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	fileInfo, err := os.Stat(broken)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	dirEntry, fileEntry := fs.FileInfoToDirEntry(dirInfo), fs.FileInfoToDirEntry(fileInfo)
	pathErr := func(path string, err error) error {
		return &os.PathError{Op: "lstat", Path: path, Err: err}
	}

	var reported []UnreadableFile
	report := func(f UnreadableFile) { reported = append(reported, f) }

	goneDir := filepath.Join(storage.ArticleDir, "gone-dir")
	if ret := storage.skipWalkError(goneDir, dirEntry, pathErr(goneDir, fs.ErrNotExist), report); ret != fs.SkipDir {
		t.Errorf("a vanished directory should skip its subtree, got %v", ret)
	}
	goneFile := filepath.Join(storage.ArticleDir, "gone.md")
	if ret := storage.skipWalkError(goneFile, fileEntry, pathErr(goneFile, fs.ErrNotExist), report); ret != nil {
		t.Errorf("a vanished file should be skipped with nil, got %v", ret)
	}
	if len(reported) != 0 {
		t.Errorf("vanished entries must not be reported, got %+v", reported)
	}
	if warnings := unreadableWarnings(t, buf, "gone"); len(warnings) != 0 {
		t.Errorf("vanished entries must not be logged, got %q", warnings)
	}

	for i := 0; i < 2; i++ {
		if ret := storage.skipWalkError(goneDir, dirEntry, pathErr(goneDir, fs.ErrPermission), report); ret != fs.SkipDir {
			t.Errorf("an unreadable directory should skip its subtree, got %v", ret)
		}
	}
	if len(reported) != 2 || reported[0].Path != "gone-dir/" {
		t.Errorf("an unreadable directory should be reported on each scan with a trailing slash, got %+v", reported)
	}
	if warnings := unreadableWarnings(t, buf, "gone-dir/"); len(warnings) != 1 {
		t.Errorf("an unreadable directory should warn once, got %q", warnings)
	}

	for _, rootErr := range []error{pathErr(storage.ArticleDir, fs.ErrNotExist), pathErr(storage.ArticleDir, fs.ErrPermission)} {
		if ret := storage.skipWalkError(storage.ArticleDir, nil, rootErr, report); ret != rootErr {
			t.Errorf("the article root must fail the scan with its error, got %v for %v", ret, rootErr)
		}
	}
}

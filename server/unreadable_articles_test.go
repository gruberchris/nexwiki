package server

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"
)

// logCaptureBuffer is a log sink safe to read while something else might still be logging.
type logCaptureBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logCaptureBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logCaptureBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog redirects the standard logger into a buffer for the rest of the test, with flags
// cleared so each line starts with the message itself.
func captureLog(t *testing.T) *logCaptureBuffer {
	t.Helper()
	buf := &logCaptureBuffer{}
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
func unreadableWarnings(t *testing.T, buf *logCaptureBuffer, relPath string) []string {
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
// file it cannot read or parse, once per version and sharing that record with the other walks, and
// still returns every referrer it can read. A file with malformed front matter is one of those: it
// has no slug to heal it under, and healRenamedLinks could not parse it to rewrite it anyway.
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

	want := []string{"embeds-diagram"}
	for i := 0; i < 3; i++ {
		got, _, err := storage.findAssetReferrers("good-one")
		if err != nil {
			t.Fatalf("findAssetReferrers failed: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("findAssetReferrers = %v, want %v", got, want)
		}
	}
	if warnings := unreadableWarnings(t, buf, "malformed-embed.md"); len(warnings) != 1 || !strings.Contains(warnings[0], "YAML") {
		t.Errorf("expected exactly one warning carrying the parse error for the malformed file, got %q", warnings)
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

// lockSearch leaves a directory listable but not searchable for the rest of the test: read
// permission without execute, so its entries are listed and then cannot be stat'd. The test is
// skipped where that is not what happens. The directory must hold at least one entry to check.
func lockSearch(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0600); err != nil {
		t.Skipf("cannot chmod on this platform: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("a directory without search permission cannot be listed here: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("lockSearch needs an entry in %s to check that stats fail", dir)
	}
	if _, err := os.Lstat(filepath.Join(dir, entries[0].Name())); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Skipf("removing search permission does not stop a stat here (running as root?): %v", err)
	}
}

// stubWalkLstat routes skipWalkError's stats through fail for the rest of the test. fail gets the
// path and returns the error to deny the stat with, or nil to pass the call through.
func stubWalkLstat(t *testing.T, fail func(path string) error) {
	t.Helper()
	prev := walkLstat
	walkLstat = func(path string) (fs.FileInfo, error) {
		if err := fail(path); err != nil {
			return nil, &os.PathError{Op: "lstat", Path: path, Err: err}
		}
		return prev(path)
	}
	t.Cleanup(func() { walkLstat = prev })
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
// checks, and it warns once for as long as it stays unreadable. A document in a folder is misplaced
// once the folder can be listed, so recovery reports it as that rather than listing it.
func TestUnreadableSubdirectoryIsSkipped(t *testing.T) {
	storage, _ := newUnreadableFixture(t)
	buf := captureLog(t)

	body := "![diagram](/api/assets/good-one/diagram.png) and [[Good One]]\n"
	writeWithMtime(t, filepath.Join(storage.ArticleDir, "nested.md"),
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

		referrers, _, err := storage.findAssetReferrers("good-one")
		if err != nil {
			t.Fatalf("an unreadable subdirectory must not fail the asset scan: %v", err)
		}
		if want := []string{"nested"}; !reflect.DeepEqual(referrers, want) {
			t.Errorf("findAssetReferrers = %v, want %v", referrers, want)
		}
	}

	const folderWarning = "directory locked/:"
	warnings := unreadableWarnings(t, buf, folderWarning)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning across repeated scans, got %d: %q", len(warnings), warnings)
	}

	// Once the folder can be listed again its document is scanned, and found misplaced, and a
	// listing forgets the failure, so losing access again warns again.
	if err := os.Chmod(locked, 0755); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	if got := listed(); contains(got, "hidden") {
		t.Errorf("a document in a folder is misplaced and must not be listed, got %v", got)
	}
	graph, err := storage.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	if got, want := unreadablePaths(t, graph), []string{"broken.md"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Unreadable paths after recovery = %v, want %v", got, want)
	}
	if want := []MisplacedDocument{{Path: "locked/hidden.md", Slug: "hidden"}}; !reflect.DeepEqual(graph.Misplaced, want) {
		t.Errorf("Misplaced after recovery = %+v, want %+v", graph.Misplaced, want)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	listed()
	if warnings := unreadableWarnings(t, buf, folderWarning); len(warnings) != 2 {
		t.Errorf("expected a second warning after the folder became unreadable again, got %d: %q", len(warnings), warnings)
	}
}

// TestUnstattableArticleFileIsSkipped pins the other per-entry walk error: a file its directory
// lists but that cannot then be stat'd, which a directory granting read but not search permission
// produces. In a subdirectory the file is skipped and warned about once; an article root in that
// state fails the scan instead (see TestUnreadableArticleRootFailsScans). Its cached parse still
// matches the file, so the failure has to be forgotten some other way once the file is reachable
// again; this pins that it is, and that losing access again warns again. A document in a
// subdirectory is misplaced, so while it is reachable it is reported as that instead.
func TestUnstattableArticleFileIsSkipped(t *testing.T) {
	storage, _ := newUnreadableFixture(t)
	buf := captureLog(t)

	dir := filepath.Join(storage.ArticleDir, "no-search")
	file := filepath.Join(dir, "listed.md")
	writeWithMtime(t, file, []byte("---\ntitle: Listed\nslug: listed\n---\nbody\n"), time.Now().Add(-time.Hour))

	// scan lists the wiki, which prunes the cache as the server's listings do, and returns the link
	// graph.
	scan := func() *LinkGraph {
		t.Helper()
		articles, err := storage.ListArticles()
		if err != nil {
			t.Fatalf("a file that cannot be stat'd must not fail the listing: %v", err)
		}
		for _, a := range articles {
			if a.Slug == "listed" {
				t.Errorf("a document in a subdirectory must never be listed")
			}
		}
		graph, err := storage.ScanLinkGraph()
		if err != nil {
			t.Fatalf("a file that cannot be stat'd must not fail the link graph: %v", err)
		}
		return graph
	}
	reachable := []MisplacedDocument{{Path: "no-search/listed.md", Slug: "listed"}}
	expectReachable := func(when string) {
		t.Helper()
		graph := scan()
		if !reflect.DeepEqual(graph.Misplaced, reachable) || contains(unreadablePaths(t, graph), "no-search/listed.md") {
			t.Errorf("%s: the file should be reported misplaced and not unreadable, got misplaced %+v and unreadable %v",
				when, graph.Misplaced, unreadablePaths(t, graph))
		}
	}
	expectReachable("before locking") // also caches the file's parse

	lockSearch(t, dir)

	for i := 0; i < 3; i++ {
		graph := scan()
		if got, want := unreadablePaths(t, graph), []string{"broken.md", "no-search/listed.md"}; !reflect.DeepEqual(got, want) {
			t.Errorf("Unreadable paths = %v, want %v", got, want)
		}
		if len(graph.Misplaced) != 0 {
			t.Errorf("a file that cannot be stat'd cannot be found misplaced, got %+v", graph.Misplaced)
		}
		if _, err := storage.GetBacklinks("good-one"); err != nil {
			t.Fatalf("a file that cannot be stat'd must not fail a backlink scan: %v", err)
		}
		if _, _, err := storage.findAssetReferrers("good-one"); err != nil {
			t.Fatalf("a file that cannot be stat'd must not fail the asset scan: %v", err)
		}
	}
	const unreadableWarning = "unreadable article file no-search/listed.md"
	warnings := unreadableWarnings(t, buf, unreadableWarning)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning across repeated scans, got %d: %q", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "permission denied") {
		t.Errorf("warning should carry the stat error: %q", warnings[0])
	}

	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	expectReachable("once it can be stat'd again")
	if err := os.Chmod(dir, 0600); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	scan()
	if warnings := unreadableWarnings(t, buf, unreadableWarning); len(warnings) != 2 {
		t.Errorf("expected a second warning after the file became unreachable again, got %d: %q", len(warnings), warnings)
	}
	// Each spell of being reachable is news too, since the unreadable spell in between replaced it.
	if warnings := unreadableWarnings(t, buf, "misplaced article file no-search/listed.md"); len(warnings) != 2 {
		t.Errorf("expected one misplaced warning per reachable spell, got %d: %q", len(warnings), warnings)
	}
}

// TestUnstattableTopLevelDirectoryUnderSearchableRoot pins the case the root probe exists for: a
// folder directly under the article directory that this user can neither list nor stat, as an
// SELinux label, a macOS ACL, or a FUSE mount another user owns can leave one, while the article
// directory itself is searchable. That costs only the folder: every scan and the boot index sync
// still succeed, and the folder is reported like any other that cannot be listed. The folder's
// listing fails for real; its stat is simulated, since permissions alone cannot deny it here. The
// probe itself is real, and must not run at all on a scan that hits no denied stat. The document in
// the folder is misplaced, so while the folder is reachable it is reported as that.
func TestUnstattableTopLevelDirectoryUnderSearchableRoot(t *testing.T) {
	storage, _ := newUnreadableFixture(t)
	buf := captureLog(t)

	labelled := filepath.Join(storage.ArticleDir, "labelled")
	writeWithMtime(t, filepath.Join(labelled, "hidden.md"),
		[]byte("---\ntitle: Hidden\nslug: hidden\n---\n![diagram](/api/assets/good-one/diagram.png) [[Good One]]\n"), time.Now().Add(-time.Hour))
	probe := filepath.Join(storage.ArticleDir, articleDirSearchProbe)
	probes := 0
	stubWalkLstat(t, func(path string) error {
		switch path {
		case probe:
			probes++
		case labelled:
			return fs.ErrPermission
		}
		return nil
	})

	// scanAll runs every walk, checking each succeeds, and returns the link graph and the slugs
	// listed.
	scanAll := func() (graph *LinkGraph, listed []string) {
		t.Helper()
		articles, err := storage.ListArticles()
		if err != nil {
			t.Fatalf("ListArticles failed: %v", err)
		}
		for _, a := range articles {
			listed = append(listed, a.Slug)
		}
		sort.Strings(listed)
		graph, err = storage.ScanLinkGraph()
		if err != nil {
			t.Fatalf("ScanLinkGraph failed: %v", err)
		}
		if _, err := storage.GetBacklinks("good-one"); err != nil {
			t.Fatalf("GetBacklinks failed: %v", err)
		}
		if _, _, err := storage.findAssetReferrers("good-one"); err != nil {
			t.Fatalf("findAssetReferrers failed: %v", err)
		}
		return graph, listed
	}

	if graph, _ := scanAll(); len(graph.Misplaced) != 1 || graph.Misplaced[0].Path != "labelled/hidden.md" {
		t.Fatalf("the folder's document should be scanned, and found misplaced, while the folder is reachable, got %+v", graph.Misplaced)
	}
	if probes != 0 {
		t.Fatalf("scans that hit no denied stat probed the article directory %d times", probes)
	}

	lockDir(t, labelled)
	for i := 0; i < 3; i++ {
		graph, listed := scanAll()
		if want := []string{"good-one", "good-two"}; !reflect.DeepEqual(listed, want) {
			t.Errorf("ListArticles = %v, want %v", listed, want)
		}
		if unreadable, want := unreadablePaths(t, graph), []string{"broken.md", "labelled/"}; !reflect.DeepEqual(unreadable, want) {
			t.Errorf("Unreadable paths = %v, want %v", unreadable, want)
		}
	}
	if probes == 0 {
		t.Errorf("a denied top-level stat must probe the article directory before blaming it")
	}
	if err := storage.SyncSearchIndex(); err != nil {
		t.Fatalf("one denied folder must not fail the boot index sync: %v", err)
	}
	if !inSearchIndex(t, storage, "good-one") {
		t.Error("the boot index sync must keep articles outside the denied folder")
	}
	if warnings := unreadableWarnings(t, buf, "directory labelled/:"); len(warnings) != 1 {
		t.Errorf("expected exactly one warning across repeated scans, got %d: %q", len(warnings), warnings)
	}
}

// TestUnreadableArticleRootFailsScans pins the walk errors that must still fail a scan. A data
// directory that is missing, cannot be read, or can be listed but not searched is broken, and
// reporting it as an empty wiki would hide that.
func TestUnreadableArticleRootFailsScans(t *testing.T) {
	scans := []struct {
		name string
		run  func(*Storage) error
	}{
		{"ListArticles", func(s *Storage) error { _, err := s.ListArticles(); return err }},
		{"ScanLinkGraph", func(s *Storage) error { _, err := s.ScanLinkGraph(); return err }},
		{"GetBacklinks", func(s *Storage) error { _, err := s.GetBacklinks("good-one"); return err }},
		{"findAssetReferrers", func(s *Storage) error { _, _, err := s.findAssetReferrers("good-one"); return err }},
	}
	// expectFailures checks every scan fails, and when want is not empty that its error says so.
	expectFailures := func(t *testing.T, storage *Storage, want string) {
		t.Helper()
		for _, scan := range scans {
			if err := scan.run(storage); err == nil {
				t.Errorf("%s: expected an error", scan.name)
			} else if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q should contain %q", scan.name, err, want)
			}
		}
	}

	t.Run("missing", func(t *testing.T) {
		storage, _ := newUnreadableFixture(t)
		captureLog(t)
		if err := os.RemoveAll(storage.ArticleDir); err != nil {
			t.Fatalf("RemoveAll failed: %v", err)
		}
		expectFailures(t, storage, "")
	})

	t.Run("unreadable", func(t *testing.T) {
		storage, _ := newUnreadableFixture(t)
		captureLog(t)
		lockDir(t, storage.ArticleDir)
		expectFailures(t, storage, "")
	})

	// Every entry of such a root fails on its own, so skipping each one would list an empty wiki,
	// and boot reconciliation would then drop every search index entry as an orphan.
	t.Run("listable but not searchable", func(t *testing.T) {
		storage, _ := newUnreadableFixture(t)
		buf := captureLog(t)
		lockSearch(t, storage.ArticleDir)
		expectFailures(t, storage, "article directory is not searchable: lstat ")

		if err := storage.SyncSearchIndex(); err == nil {
			t.Error("SyncSearchIndex: expected an error")
		}
		if !inSearchIndex(t, storage, "good-one") {
			t.Error("a failed listing must leave the search index alone")
		}
		if strings.Contains(buf.String(), "skipping unreadable") {
			t.Errorf("the root's failure must not be reported as its entries':\n%s", buf)
		}
	})

	// With no article file at the top level, only a subdirectory's failed listing shows the root
	// cannot be searched, and it must not pass for that subdirectory being unreadable itself.
	t.Run("listable but not searchable, holding only subdirectories", func(t *testing.T) {
		storage, _ := newUnreadableFixture(t)
		buf := captureLog(t)
		writeWithMtime(t, filepath.Join(storage.ArticleDir, "notes", "nested.md"),
			[]byte("---\ntitle: Nested\nslug: nested\n---\n[[Good One]]\n"), time.Now().Add(-time.Hour))
		topLevel, err := filepath.Glob(filepath.Join(storage.ArticleDir, "*.md"))
		if err != nil {
			t.Fatalf("Glob failed: %v", err)
		}
		for _, path := range topLevel {
			if err := os.Remove(path); err != nil {
				t.Fatalf("Remove failed: %v", err)
			}
		}
		lockSearch(t, storage.ArticleDir)
		expectFailures(t, storage, "article directory is not searchable: lstat ")

		if err := storage.SyncSearchIndex(); err == nil {
			t.Error("SyncSearchIndex: expected an error")
		}
		if !inSearchIndex(t, storage, "good-one") {
			t.Error("a failed listing must leave the search index alone")
		}
		if strings.Contains(buf.String(), "skipping unreadable") {
			t.Errorf("the root's failure must not be reported as its subdirectory's:\n%s", buf)
		}
	})
}

// TestSkipWalkErrorClassification pins how the walks classify an entry they cannot stat or list.
// An entry deleted or renamed between the directory read and its stat or listing is a race no
// test can trigger on demand, so the classification is checked directly: it is gone, not broken,
// and is skipped with no warning and no report. Any other error below the root is reported once,
// except a permission error stat'ing an entry directly under a root that denies the probe's stat
// too, which like the root itself fails the scan. The errors are simulated too, so this runs where
// permissions are not enforced.
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
	// A top-level directory whose listing failed some other way, but which is gone by the time it
	// is stat'd to check whether the root is to blame, has vanished too.
	if ret := storage.skipWalkError(goneDir, dirEntry, pathErr(goneDir, fs.ErrPermission), report); ret != fs.SkipDir {
		t.Errorf("a directory that vanished after its listing failed should skip its subtree, got %v", ret)
	}
	if len(reported) != 0 {
		t.Errorf("vanished entries must not be reported, got %+v", reported)
	}
	if warnings := unreadableWarnings(t, buf, "gone"); len(warnings) != 0 {
		t.Errorf("vanished entries must not be logged, got %q", warnings)
	}

	// The directory exists, so it can be stat'd and its failed listing is its own, not the root's.
	lockedDir := filepath.Join(storage.ArticleDir, "locked-dir")
	if err := os.Mkdir(lockedDir, 0755); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}
	for i := 0; i < 2; i++ {
		if ret := storage.skipWalkError(lockedDir, dirEntry, pathErr(lockedDir, fs.ErrPermission), report); ret != fs.SkipDir {
			t.Errorf("an unreadable directory should skip its subtree, got %v", ret)
		}
	}
	if len(reported) != 2 || reported[0].Path != "locked-dir/" {
		t.Errorf("an unreadable directory should be reported on each scan with a trailing slash, got %+v", reported)
	}
	if warnings := unreadableWarnings(t, buf, "locked-dir/"); len(warnings) != 1 {
		t.Errorf("an unreadable directory should warn once, got %q", warnings)
	}

	// A file in a subdirectory that cannot be stat'd costs only that file.
	reported = nil
	nested := filepath.Join(storage.ArticleDir, "notes", "nested.md")
	for i := 0; i < 2; i++ {
		if ret := storage.skipWalkError(nested, fileEntry, pathErr(nested, fs.ErrPermission), report); ret != nil {
			t.Errorf("a file in a subdirectory that cannot be stat'd should be skipped with nil, got %v", ret)
		}
	}
	if len(reported) != 2 || reported[0].Path != "notes/nested.md" {
		t.Errorf("a file that cannot be stat'd should be reported on each scan, got %+v", reported)
	}
	if warnings := unreadableWarnings(t, buf, "notes/nested.md"); len(warnings) != 1 {
		t.Errorf("a file that cannot be stat'd should warn once, got %q", warnings)
	}

	// A stat denied directly under the root blames the root only if the root also denies a stat of
	// the probe, a name that is not there. The directory's stat is simulated, since permissions
	// alone cannot deny it under a searchable root, and so is the probe's answer where one is set.
	probe := filepath.Join(storage.ArticleDir, articleDirSearchProbe)
	var probeErr error
	stubWalkLstat(t, func(path string) error {
		switch path {
		case probe:
			return probeErr
		case filepath.Join(storage.ArticleDir, "denied-dir"), filepath.Join(storage.ArticleDir, "unsearchable-dir"):
			return fs.ErrPermission
		}
		return nil
	})
	topPaths := func() []string {
		var paths []string
		for _, f := range reported {
			paths = append(paths, f.Path)
		}
		return paths
	}

	// The real probe finds nothing, so the root is searchable and each denial is the entry's own,
	// such as an SELinux label or a FUSE mount another user owns: it costs only that entry.
	reported = nil
	deniedFile := filepath.Join(storage.ArticleDir, "denied.md")
	deniedDir := filepath.Join(storage.ArticleDir, "denied-dir")
	for i := 0; i < 2; i++ {
		if ret := storage.skipWalkError(deniedFile, fileEntry, pathErr(deniedFile, fs.ErrPermission), report); ret != nil {
			t.Errorf("a top-level file whose own stat is denied should be skipped with nil, got %v", ret)
		}
		if ret := storage.skipWalkError(deniedDir, dirEntry, pathErr(deniedDir, fs.ErrPermission), report); ret != fs.SkipDir {
			t.Errorf("a top-level directory whose own stat is denied should skip its subtree, got %v", ret)
		}
	}
	if got, want := topPaths(), []string{"denied.md", "denied-dir/", "denied.md", "denied-dir/"}; !reflect.DeepEqual(got, want) {
		t.Errorf("top-level entries whose own stat is denied should be reported on each scan, got %v, want %v", got, want)
	}
	for _, rel := range []string{"denied.md", "denied-dir/"} {
		if warnings := unreadableWarnings(t, buf, rel); len(warnings) != 1 {
			t.Errorf("%s should warn once, got %q", rel, warnings)
		}
	}

	// Neither a probe that finds the name after all nor one that fails some other way is evidence
	// against the root.
	if err := os.WriteFile(probe, nil, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	for _, tc := range []struct {
		name string
		err  error
	}{{"a probe that exists", nil}, {"a probe with an I/O error", syscall.EIO}} {
		reported, probeErr = nil, tc.err
		if ret := storage.skipWalkError(deniedFile, fileEntry, pathErr(deniedFile, fs.ErrPermission), report); ret != nil {
			t.Errorf("%s: a top-level file whose stat is denied should be skipped with nil, got %v", tc.name, ret)
		}
		if ret := storage.skipWalkError(deniedDir, dirEntry, pathErr(deniedDir, fs.ErrPermission), report); ret != fs.SkipDir {
			t.Errorf("%s: a top-level directory whose stat is denied should skip its subtree, got %v", tc.name, ret)
		}
		if got, want := topPaths(), []string{"denied.md", "denied-dir/"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got reports %v, want %v", tc.name, got, want)
		}
	}

	// A root that denies the probe too cannot be searched, and whichever entry showed it fails the
	// scan with the directory blamed and the entry's stat error wrapped.
	reported, probeErr = nil, fs.ErrPermission
	topFile := filepath.Join(storage.ArticleDir, "unsearchable.md")
	topErr := pathErr(topFile, fs.ErrPermission)
	ret := storage.skipWalkError(topFile, fileEntry, topErr, report)
	if !errors.Is(ret, topErr) || !strings.HasPrefix(fmt.Sprint(ret), "article directory is not searchable: ") {
		t.Errorf("a file directly under an unsearchable root must fail the scan, blaming the directory and wrapping the stat error, got %v", ret)
	}
	topDir := filepath.Join(storage.ArticleDir, "unsearchable-dir")
	ret = storage.skipWalkError(topDir, dirEntry, pathErr(topDir, fs.ErrPermission), report)
	if !errors.Is(ret, fs.ErrPermission) || !strings.HasPrefix(fmt.Sprint(ret), "article directory is not searchable: lstat "+topDir+":") {
		t.Errorf("a directory directly under an unsearchable root must fail the scan, blaming the directory and wrapping its stat error, got %v", ret)
	}
	if len(reported) != 0 {
		t.Errorf("a failure that fails the scan must not also be reported, got %+v", reported)
	}
	for _, rel := range []string{"unsearchable.md", "unsearchable-dir/"} {
		if warnings := unreadableWarnings(t, buf, rel); len(warnings) != 0 {
			t.Errorf("a failure that fails the scan must not also be logged, got %q", warnings)
		}
	}
	probeErr = nil

	// Only a permission error blames the root. Any other failure directly under it, such as an I/O
	// error, may be that one entry's, so it is skipped and reported like one.
	reported = nil
	eioFile := filepath.Join(storage.ArticleDir, "eio.md")
	eioDir := filepath.Join(storage.ArticleDir, "eio-dir")
	if err := os.Mkdir(eioDir, 0755); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}
	for i := 0; i < 2; i++ {
		if ret := storage.skipWalkError(eioFile, fileEntry, pathErr(eioFile, syscall.EIO), report); ret != nil {
			t.Errorf("a top-level file with an I/O error should be skipped with nil, got %v", ret)
		}
		if ret := storage.skipWalkError(eioDir, dirEntry, pathErr(eioDir, syscall.EIO), report); ret != fs.SkipDir {
			t.Errorf("a top-level directory with an I/O error should skip its subtree, got %v", ret)
		}
	}
	var eioPaths []string
	for _, f := range reported {
		eioPaths = append(eioPaths, f.Path)
	}
	if want := []string{"eio.md", "eio-dir/", "eio.md", "eio-dir/"}; !reflect.DeepEqual(eioPaths, want) {
		t.Errorf("top-level entries with I/O errors should be reported on each scan, got %v, want %v", eioPaths, want)
	}
	for _, rel := range []string{"eio.md", "eio-dir/"} {
		if warnings := unreadableWarnings(t, buf, rel); len(warnings) != 1 {
			t.Errorf("%s should warn once, got %q", rel, warnings)
		}
	}

	for _, rootErr := range []error{pathErr(storage.ArticleDir, fs.ErrNotExist), pathErr(storage.ArticleDir, fs.ErrPermission)} {
		if ret := storage.skipWalkError(storage.ArticleDir, nil, rootErr, report); ret != rootErr {
			t.Errorf("the article root must fail the scan with its error, got %v for %v", ret, rootErr)
		}
	}
}

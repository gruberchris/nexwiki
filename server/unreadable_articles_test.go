package server

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
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

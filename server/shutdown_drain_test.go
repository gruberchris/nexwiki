package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"
)

// These tests pin the shutdown ordering: every writer (the plan lifecycle worker, the stdio MCP loop,
// an in-flight save) finishes or is stopped before the search index closes, and a write arriving
// after Close has begun is refused before it touches the disk. Otherwise a write can land in a file
// the index never sees, or fail against a closed index. Every wait is bounded by testWaitLimit.

// TestCloseWaitsForWriteInProgress holds writeMu as a save in progress would, and checks that Close
// waits for it before closing the index, so the save still reaches the index.
func TestCloseWaitsForWriteInProgress(t *testing.T) {
	dataDir := t.TempDir()
	s, err := openStorage(t, dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { closeStorage(t, s) })
	// Let the background sweep finish first. Otherwise it is waiting on the writeMu taken below, and
	// Close would block on the sweep rather than on the write, passing whether or not it waits for
	// writes.
	waitSweep(t, s)

	s.writeMu.Lock()
	released := false
	release := func() {
		if !released {
			released = true
			s.writeMu.Unlock()
		}
	}
	defer release()

	var closeErr error
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		closeErr = s.Close()
	}()
	expectWaitingOnMutex(t, ".(*Storage).Close", closed, "Close returned while a write held writeMu")
	if _, err := s.SearchIndex.DocCount(); err != nil {
		t.Errorf("the search index was closed while a write held writeMu: %v", err)
	}

	// The save that was already under way when Close began finishes, index update included.
	if _, err := s.saveArticleLocked("", "In Flight", "# written during Close", "", "", "", "", nil, "", ArticleOverrides{}); err != nil {
		t.Errorf("the save in progress failed: %v", err)
	}
	release()

	if !waitClosed(t, closed, "Close") {
		t.FailNow()
	}
	if closeErr != nil {
		t.Fatalf("Close failed: %v", closeErr)
	}

	idx, err := bleve.Open(filepath.Join(dataDir, "search.bleve"))
	if err != nil {
		t.Fatalf("reopening the closed index failed: %v", err)
	}
	defer func() { _ = idx.Close() }()
	if doc, err := idx.Document("in-flight"); err != nil || doc == nil {
		t.Errorf("the save that finished during Close is missing from the index (doc %v, err %v)", doc, err)
	}
}

// TestWriteQueuedBehindCloseIsRefused covers a writer already waiting for writeMu when Close begins.
// It has not touched the disk yet, so it must be turned away rather than slip in ahead of Close.
func TestWriteQueuedBehindCloseIsRefused(t *testing.T) {
	s, err := openStorage(t, t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { closeStorage(t, s) })
	waitSweep(t, s) // so Close waits on writeMu, not on a sweep queued for it (see above)

	s.writeMu.Lock()
	released := false
	release := func() {
		if !released {
			released = true
			s.writeMu.Unlock()
		}
	}
	defer release()

	var saveErr error
	saved := make(chan struct{})
	go func() {
		defer close(saved)
		_, saveErr = s.SaveArticle("", "Queued", "# queued", "", "", "", "", nil, "")
	}()
	expectWaitingOnMutex(t, ".(*Storage).SaveArticleWithOverrides", saved, "the save returned without waiting for writeMu")

	// Close must be queued on writeMu behind the save before the lock is released, so the save is
	// the next to take it: that is the writer that would slip in ahead of a Close that set closed
	// only after its wait.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_ = s.Close()
	}()
	expectWaitingOnMutex(t, ".(*Storage).Close", closed, "Close returned while a write held writeMu")
	release()

	if !waitClosed(t, saved, "the queued save") || !waitClosed(t, closed, "Close") {
		t.FailNow()
	}
	if !errors.Is(saveErr, ErrStorageClosed) {
		t.Errorf("a save queued when Close began returned %v, want ErrStorageClosed", saveErr)
	}
	if _, err := os.Stat(filepath.Join(s.ArticleDir, "queued.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the refused save left an article file (stat err %v)", err)
	}
}

// TestWritesAfterCloseAreRefused calls every write entry point after Close and checks each returns
// ErrStorageClosed and changes nothing on disk.
func TestWritesAfterCloseAreRefused(t *testing.T) {
	s, err := openStorage(t, t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { closeStorage(t, s) })

	art, err := s.SaveArticle("", "Seeded", "# v1", "", "", "", "", []string{"shared-tag"}, "")
	if err != nil {
		t.Fatalf("seeding failed: %v", err)
	}
	if art, err = s.SaveArticle(art.Slug, art.Title, "# v2", "", "", "", "", art.Tags, ""); err != nil {
		t.Fatalf("seeding a second version failed: %v", err)
	}
	plan := savePlan(t, s, "Seeded Plan", "implementing")
	if _, err := s.SaveAsset(art.Slug, "image.png", []byte("png")); err != nil {
		t.Fatalf("seeding an asset failed: %v", err)
	}

	if !closeStorage(t, s) {
		t.FailNow()
	}
	before := snapshotTree(t, s.DataDir)

	ops := []struct {
		name string
		call func() error
	}{
		{"SaveArticle (new)", func() error {
			_, err := s.SaveArticle("", "After Close", "# new", "", "", "", "", nil, "")
			return err
		}},
		{"SaveArticle (edit)", func() error {
			_, err := s.SaveArticle(art.Slug, art.Title, "# v3", "", "", "", "", art.Tags, "")
			return err
		}},
		{"SaveArticleWithOverrides (in place)", func() error {
			_, err := s.SaveArticleWithOverrides(art.Slug, "Another Title", "# v3", "", "", "", "", art.Tags, "", ArticleOverrides{KeepSlug: true})
			return err
		}},
		{"SetStatus", func() error {
			_, err := s.SetStatus(plan.Slug, "completed", 0, "")
			return err
		}},
		{"SaveAsset", func() error {
			_, err := s.SaveAsset(art.Slug, "other.png", []byte("png"))
			return err
		}},
		{"ApplyArticleEdit", func() error {
			_, err := s.ApplyArticleEdit(art.Slug, ArticleEdit{Title: "Edited After Close", Content: "# v3", LoadedVersion: art.Version})
			return err
		}},
		{"RevertArticle", func() error {
			_, err := s.RevertArticle(art.Slug, 1)
			return err
		}},
		{"UpdateArticleTags", func() error {
			_, err := s.UpdateArticleTags(art.Slug, []string{"new-tag"}, art.Version, "")
			return err
		}},
		{"DeleteTagGlobally", func() error { return s.DeleteTagGlobally("shared-tag") }},
		{"DeleteArticle", func() error { return s.DeleteArticle(art.Slug) }}, // last: it would remove what the others edit
	}
	for _, op := range ops {
		if err := op.call(); !errors.Is(err, ErrStorageClosed) {
			t.Errorf("%s after Close returned %v, want ErrStorageClosed", op.name, err)
		}
	}

	after := snapshotTree(t, s.DataDir)
	for path, was := range before {
		if now, ok := after[path]; !ok {
			t.Errorf("a write after Close removed %s", path)
		} else if now != was {
			t.Errorf("a write after Close changed %s", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("a write after Close created %s", path)
		}
	}

	// Reads may keep working or fail cleanly after Close; the contract is only that they do not panic,
	// which would fail the test here.
	_, _ = s.GetArticle(art.Slug)
	_, _ = s.ListArticles()
	_, _ = s.GetArticleHistory(art.Slug)
	_, _ = s.SearchArticles("seeded")
}

// snapshotTree records the size and modification time of every file under the data directory
// except the search index, whose own files Close legitimately rewrites.
func snapshotTree(t *testing.T, dataDir string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "search.bleve" {
				return filepath.SkipDir
			}
			snap[path] = "dir"
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		snap[path] = fmt.Sprintf("%d bytes, modified %s", info.Size(), info.ModTime())
		return nil
	})
	if err != nil {
		t.Fatalf("snapshotting %s failed: %v", dataDir, err)
	}
	return snap
}

// TestCloseIsIdempotent closes the same storage repeatedly, in sequence and concurrently.
func TestCloseIsIdempotent(t *testing.T) {
	s, err := openStorage(t, t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	if !closeStorage(t, s) || !closeStorage(t, s) {
		t.FailNow()
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			closeStorage(t, s)
		}()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	waitClosed(t, done, "concurrent Close calls")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.CloseContext(ctx); err != nil {
		t.Errorf("CloseContext on closed storage failed: %v", err)
	}
}

// TestCloseContextClosesIndexAtDeadline covers a write that overruns the shutdown deadline: the index
// must still be closed before the supervisor kills the process, and later writes still refused.
func TestCloseContextClosesIndexAtDeadline(t *testing.T) {
	s, err := openStorage(t, t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { closeStorage(t, s) })
	waitSweep(t, s) // so the only thing CloseContext can be waiting on is the write

	s.writeMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var closeErr error
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		closeErr = s.CloseContext(ctx)
	}()
	ok := waitClosed(t, closed, "CloseContext past its deadline")
	s.writeMu.Unlock()
	if !ok {
		t.FailNow()
	}
	if closeErr != nil {
		t.Errorf("CloseContext failed: %v", closeErr)
	}
	if _, err := s.SearchIndex.DocCount(); err == nil {
		t.Error("CloseContext returned at its deadline without closing the search index")
	}
	if _, err := s.SaveArticle("", "Late", "# late", "", "", "", "", nil, ""); !errors.Is(err, ErrStorageClosed) {
		t.Errorf("a save after a CloseContext that gave up waiting returned %v, want ErrStorageClosed", err)
	}
}

// cancelOnLog is a worker log that cancels the worker's context on the first line containing match.
type cancelOnLog struct {
	lockedBuffer
	match  string
	cancel context.CancelFunc
}

func (c *cancelOnLog) Write(p []byte) (int, error) {
	if strings.Contains(string(p), c.match) {
		c.cancel()
	}
	return c.lockedBuffer.Write(p)
}

// runWorker runs w.Run(ctx) in a goroutine and returns a channel closed when Run returns.
func runWorker(w *PlanLifecycleWorker, ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	return done
}

// TestLifecycleWorkerRunReturnsAfterCancel pins that Run returns once canceled, which is what
// shutdown waits on before closing storage.
func TestLifecycleWorkerRunReturnsAfterCancel(t *testing.T) {
	s := newLifecycleStorage(t)
	out := &lockedBuffer{}
	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{IntervalDays: 1, ArchiveAfterDays: 90}, Log: out}

	ctx, cancel := context.WithCancel(context.Background())
	done := runWorker(w, ctx)
	cancel()
	if !waitClosed(t, done, "the plan lifecycle worker after cancellation") {
		t.FailNow()
	}
	if !strings.Contains(out.String(), "Plan lifecycle worker: stopped") {
		t.Errorf("the worker returned without reporting that it stopped; log:\n%s", out.String())
	}
}

// TestLifecycleWorkerStopsBetweenPlansOnCancel cancels the worker during its first sweep, just after
// it archives one plan. It must stop before the next plan rather than finish a sweep that could
// outlast the shutdown deadline, and the plan it finished must be complete.
func TestLifecycleWorkerStopsBetweenPlansOnCancel(t *testing.T) {
	s := newLifecycleStorage(t)
	slugs := []string{"due-one", "due-two", "due-three"}
	for _, slug := range slugs {
		savePlan(t, s, slug, "completed")
		backdateStatusChange(t, s, slug, 120)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &cancelOnLog{match: "archived plan", cancel: cancel}
	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{IntervalDays: 1, ArchiveAfterDays: 90}, Log: out}
	if !waitClosed(t, runWorker(w, ctx), "the plan lifecycle worker canceled mid-sweep") {
		t.FailNow()
	}

	archived := 0
	for _, slug := range slugs {
		art, err := s.GetArticle(slug)
		if err != nil {
			t.Fatalf("plan %q missing: %v", slug, err)
		}
		switch art.Status {
		case StatusArchived:
			archived++
			if art.ArchivedAt.IsZero() {
				t.Errorf("%s was archived without archived_at: the transition was cut short", slug)
			}
		case "completed":
		default:
			t.Errorf("%s: unexpected status %q", slug, art.Status)
		}
	}
	if archived != 1 {
		t.Errorf("the worker archived %d plans after being canceled at the first; log:\n%s", archived, out.String())
	}
}

// stdioHarness runs a StdioMCPServer over a pipe, so a test can feed it requests one at a time.
type stdioHarness struct {
	stdio  *StdioMCPServer
	in     *io.PipeWriter
	out    *lockedBuffer
	served chan struct{}
}

func startStdio(t *testing.T, srv *Server) *stdioHarness {
	t.Helper()
	// The tests hold writeMu to park a request in its save; a sweep still running would contend for
	// it too.
	waitSweep(t, srv.Storage)
	pr, pw := io.Pipe()
	h := &stdioHarness{in: pw, out: &lockedBuffer{}, served: make(chan struct{})}
	h.stdio = NewStdioMCPServer(srv, pr, h.out)
	go func() {
		defer close(h.served)
		h.stdio.Serve()
	}()
	// Unblocks a Serve still parked reading, and a send still waiting for Serve to read it.
	t.Cleanup(func() {
		_ = pw.Close()
		_ = pr.Close()
	})
	return h
}

// send writes one request line without waiting for Serve to read it; the returned channel closes
// once it has.
func (h *stdioHarness) send(line string) <-chan struct{} {
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		_, _ = io.WriteString(h.in, line+"\n")
	}()
	return sent
}

func createArticleRequest(id int, title string) string {
	return `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"tools/call","params":{"name":"create_wiki_article","arguments":{"title":"` +
		title + `","content":"# body"}}}`
}

// TestStdioStopWaitsForRequestInProgress covers the stdio loop at shutdown: Stop lets the request
// being handled finish, and no request read afterwards is handled.
func TestStdioStopWaitsForRequestInProgress(t *testing.T) {
	srv := newMCPServer(t)
	h := startStdio(t, srv)

	// Holding writeMu parks the first request inside its save.
	srv.Storage.writeMu.Lock()
	released := false
	release := func() {
		if !released {
			released = true
			srv.Storage.writeMu.Unlock()
		}
	}
	defer release()

	h.send(createArticleRequest(1, "Stdio In Flight"))
	expectWaitingOnMutex(t, ".(*StdioMCPServer).dispatch", h.served, "Serve returned before handling the request")

	var stopErr error
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		stopErr = h.stdio.Stop(context.Background())
	}()
	expectWaitingOnMutex(t, ".(*StdioMCPServer).Stop", stopped, "Stop returned while a request was still being handled")
	release()
	if !waitClosed(t, stopped, "Stop") {
		t.FailNow()
	}
	if stopErr != nil {
		t.Errorf("Stop failed: %v", stopErr)
	}
	if out := h.out.String(); !strings.Contains(out, `"id":1`) || strings.Contains(out, `"isError":true`) {
		t.Errorf("the request in progress did not finish successfully; output:\n%s", out)
	}
	if _, err := srv.Storage.GetArticle("stdio-in-flight"); err != nil {
		t.Errorf("the request in progress did not write its article: %v", err)
	}

	h.send(createArticleRequest(2, "Stdio After Stop"))
	if !waitClosed(t, h.served, "Serve after reading a request past Stop") {
		t.FailNow()
	}
	if out := h.out.String(); strings.Contains(out, `"id":2`) {
		t.Errorf("a request read after Stop was answered; output:\n%s", out)
	}
	if _, err := srv.Storage.GetArticle("stdio-after-stop"); err == nil {
		t.Error("a request read after Stop wrote its article")
	}
}

// TestStdioStopGivesUpAtDeadline covers a request that overruns the shutdown deadline: Stop returns
// the context's error instead of hanging shutdown, and still no later request is handled.
func TestStdioStopGivesUpAtDeadline(t *testing.T) {
	srv := newMCPServer(t)
	h := startStdio(t, srv)

	srv.Storage.writeMu.Lock()
	h.send(createArticleRequest(1, "Stdio Overrun"))
	expectWaitingOnMutex(t, ".(*StdioMCPServer).dispatch", h.served, "Serve returned before handling the request")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var stopErr error
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		stopErr = h.stdio.Stop(ctx)
	}()
	ok := waitClosed(t, stopped, "Stop past its deadline")
	srv.Storage.writeMu.Unlock()
	if !ok {
		t.FailNow()
	}
	if !errors.Is(stopErr, context.DeadlineExceeded) {
		t.Errorf("Stop past its deadline returned %v, want context.DeadlineExceeded", stopErr)
	}

	h.send(createArticleRequest(2, "Stdio After Overrun"))
	if !waitClosed(t, h.served, "Serve after reading a request past Stop") {
		t.FailNow()
	}
	if _, err := srv.Storage.GetArticle("stdio-after-overrun"); err == nil {
		t.Error("a request read after Stop wrote its article")
	}
}

// TestStdioServeReturnsAtEOF pins the -mcp-only lifecycle: requests are answered until the input
// ends, and then Serve returns so the process can close storage.
func TestStdioServeReturnsAtEOF(t *testing.T) {
	srv := newMCPServer(t)
	h := startStdio(t, srv)

	if !waitClosed(t, h.send(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`), "sending a request") {
		t.FailNow()
	}
	_ = h.in.Close()
	if !waitClosed(t, h.served, "Serve at EOF") {
		t.FailNow()
	}
	if out := h.out.String(); !strings.Contains(out, `"id":1`) {
		t.Errorf("the request before EOF was not answered; output:\n%s", out)
	}
}

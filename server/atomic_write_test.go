package server

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// dirNames lists a directory's entries, so a test can prove no temp file was left behind.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestWriteFileAtomicOverwritesAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.md")
	if err := os.WriteFile(path, []byte("old content that is longer than the new"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	// Group-writable: a mode that 0644 less any umask can never produce, so keeping it proves the
	// existing mode was carried over rather than perm reapplied.
	if err := os.Chmod(path, 0660); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	if err := writeFileAtomic(path, []byte("new"), 0644); err != nil {
		t.Fatalf("writeFileAtomic failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if after.Mode().Perm() != before.Mode().Perm() {
		t.Errorf("mode = %v, want the existing file's %v", after.Mode().Perm(), before.Mode().Perm())
	}
	if names := dirNames(t, dir); !slices.Equal(names, []string{"page.md"}) {
		t.Errorf("directory holds %v, want only page.md (a temp file was left behind)", names)
	}
}

// A slug can be long enough that "<slug>.md" sits just under the filesystem's 255-byte name limit,
// which os.WriteFile handled; a temp name built by decorating the destination's would not fit.
func TestWriteFileAtomicHandlesNameAtLengthLimit(t *testing.T) {
	dir := t.TempDir()
	name := strings.Repeat("a", 252) + ".md"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Skipf("filesystem rejects a %d-byte name: %v", len(name), err)
	}

	if err := writeFileAtomic(path, []byte("new"), 0644); err != nil {
		t.Fatalf("writeFileAtomic failed for a %d-byte name: %v", len(name), err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
	if names := dirNames(t, dir); !slices.Equal(names, []string{name}) {
		t.Errorf("directory holds %d entries, want only the written file", len(names))
	}
}

// A new file must get the mode os.WriteFile would have given it, umask included.
func TestWriteFileAtomicNewFileMatchesWriteFileMode(t *testing.T) {
	dir := t.TempDir()
	atomicPath := filepath.Join(dir, "atomic.md")
	plainPath := filepath.Join(dir, "plain.md")

	if err := writeFileAtomic(atomicPath, []byte("body"), 0644); err != nil {
		t.Fatalf("writeFileAtomic failed: %v", err)
	}
	if err := os.WriteFile(plainPath, []byte("body"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	atomicInfo, err := os.Stat(atomicPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	plainInfo, err := os.Stat(plainPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if atomicInfo.Mode().Perm() != plainInfo.Mode().Perm() {
		t.Errorf("new file mode = %v, os.WriteFile gives %v", atomicInfo.Mode().Perm(), plainInfo.Mode().Perm())
	}
	if names := dirNames(t, dir); !slices.Equal(names, []string{"atomic.md", "plain.md"}) {
		t.Errorf("directory holds %v, want only the two written files", names)
	}
}

// os.WriteFile writes through a symlinked article; replacing the link with a regular file would
// silently detach it from wherever the user keeps the real one.
func TestWriteFileAtomicWritesThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	linkDir := filepath.Join(dir, "links")
	for _, d := range []string{realDir, linkDir} {
		if err := os.Mkdir(d, 0755); err != nil {
			t.Fatalf("Mkdir failed: %v", err)
		}
	}
	target := filepath.Join(realDir, "page.md")
	link := filepath.Join(linkDir, "page.md")
	if err := os.WriteFile(target, []byte("old"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}

	if err := writeFileAtomic(link, []byte("new"), 0644); err != nil {
		t.Fatalf("writeFileAtomic failed: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat failed: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced by a regular file")
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Errorf("link target content = %q, want %q", got, "new")
	}
	if names := dirNames(t, realDir); !slices.Equal(names, []string{"page.md"}) {
		t.Errorf("target directory holds %v, want only page.md", names)
	}
	if names := dirNames(t, linkDir); !slices.Equal(names, []string{"page.md"}) {
		t.Errorf("link directory holds %v, want only page.md", names)
	}
}

func TestWriteFileAtomicRemovesTempFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	// A non-empty directory at the destination makes the final rename fail after the temp file
	// has been fully written — the latest point at which cleanup can be skipped by mistake.
	path := filepath.Join(dir, "page.md")
	if err := os.MkdirAll(filepath.Join(path, "child"), 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	if err := writeFileAtomic(path, []byte("body"), 0644); err == nil {
		t.Fatal("writeFileAtomic over a directory succeeded, want an error")
	}
	if names := dirNames(t, dir); !slices.Equal(names, []string{"page.md"}) {
		t.Errorf("directory holds %v after a failed write, want only page.md (temp file leaked)", names)
	}
}

// Replacing a file another handle has open is routine on Unix but fails transiently on Windows,
// where this exercises the rename retry.
func TestWriteFileAtomicReplacesOpenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.md")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(20 * time.Millisecond)
		_ = reader.Close()
	}()
	t.Cleanup(func() { <-released })

	if err := writeFileAtomic(path, []byte("new"), 0644); err != nil {
		t.Fatalf("writeFileAtomic failed while a reader held the file open: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
}

// stubChmod routes writeFileAtomic's chmod through a recorder for the rest of the test, returning
// the base name of every file it was asked to chmod. A nil fail passes the call through.
func stubChmod(t *testing.T, fail error) *[]string {
	t.Helper()
	var calls []string
	prev := chmodFile
	chmodFile = func(f *os.File, mode os.FileMode) error {
		calls = append(calls, filepath.Base(f.Name()))
		if fail != nil {
			return fail
		}
		return prev(f, mode)
	}
	t.Cleanup(func() { chmodFile = prev })
	return &calls
}

// Some CIFS/SMB and FUSE mounts reject chmod outright, so the usual overwrite, where the temp file
// already has the destination's mode, must not call it at all.
func TestWriteFileAtomicSkipsChmodWhenModeMatches(t *testing.T) {
	calls := stubChmod(t, nil)
	dir := t.TempDir()
	path := filepath.Join(dir, "page.md")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := writeFileAtomic(path, []byte("new"), 0644); err != nil {
		t.Fatalf("writeFileAtomic failed: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("chmod was called %d times for an overwrite whose mode already matched", len(*calls))
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}

	// Without this half the test would pass just as well if the seam were never wired up.
	if runtime.GOOS == "windows" {
		return // Windows modes record only the read-only bit, so 0660 over 0644 is no change there
	}
	if err := os.Chmod(path, 0660); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	if err := writeFileAtomic(path, []byte("newer"), 0644); err != nil {
		t.Fatalf("writeFileAtomic failed: %v", err)
	}
	if len(*calls) != 1 || !isAtomicTempName((*calls)[0]) {
		t.Errorf("chmod calls = %v, want exactly one, on the temp file, for a differing mode", *calls)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Mode().Perm() != 0660 {
		t.Errorf("mode after overwrite = %v, want the existing 0660", info.Mode().Perm())
	}
}

// A mount that rejects chmod must not turn a mode mismatch into a failed save: the content swap is
// the point of the write, the mode a nicety.
func TestWriteFileAtomicSavesDespiteChmodFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows modes record only the read-only bit, so no writable mode differs from 0644")
	}
	calls := stubChmod(t, errors.New("operation not supported"))
	logs := captureLog(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "page.md")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.Chmod(path, 0660); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}

	if err := writeFileAtomic(path, []byte("new"), 0644); err != nil {
		t.Fatalf("writeFileAtomic failed because chmod did: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("chmod calls = %v, want exactly one", *calls)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
	if names := dirNames(t, dir); !slices.Equal(names, []string{"page.md"}) {
		t.Errorf("directory holds %v, want only page.md (a temp file was left behind)", names)
	}
	if out := logs.String(); !strings.Contains(out, "Warning:") || !strings.Contains(out, "page.md") {
		t.Errorf("a mode that could not be kept should be logged, got %q", out)
	}
}

// The startup sweep deletes whatever isAtomicTempName accepts, so it must accept every name
// writeFileAtomic can generate and nothing else.
func TestAtomicTempNameMatchesOnlyGeneratedNames(t *testing.T) {
	for _, n := range []uint64{0, 1, 35, 36, math.MaxUint64, rand.Uint64()} {
		if name := atomicTempName(n); !isAtomicTempName(name) {
			t.Errorf("isAtomicTempName rejects the generated name %q", name)
		}
	}
	for _, name := range []string{
		"",
		"page.md",
		".nexwiki-.tmp",
		".nexwiki-ABC.tmp",
		".nexwiki-0abc.tmp",
		".nexwiki-00.tmp",
		".nexwiki-+1.tmp",
		".nexwiki--1.tmp",
		".nexwiki-a_b.tmp",
		".nexwiki-" + strings.Repeat("z", 14) + ".tmp", // overflows a uint64
		".nexwiki-abc",
		".nexwiki-abc.TMP",
		".nexwiki-abc.tmpx",
		".nexwiki-abc.tmp.md",
		"x.nexwiki-abc.tmp",
		"nexwiki-abc.tmp",
	} {
		if isAtomicTempName(name) {
			t.Errorf("isAtomicTempName accepts %q, which writeFileAtomic never generates", name)
		}
	}
}

// plantFile creates dataDir/rel (slash-separated) with placeholder content.
func plantFile(t *testing.T, dataDir, rel string) string {
	t.Helper()
	path := filepath.Join(dataDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(path, []byte("partial"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	return path
}

// A crash between writeFileAtomic's create and rename strands a temp file that nothing else would
// ever remove, in any tree writeFileAtomic writes into.
func TestNewStorageRemovesLeftoverTempFiles(t *testing.T) {
	dataDir := t.TempDir()
	logs := captureLog(t)
	first, err := openStorage(t, dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	waitSweep(t, first) // let the background sweep finish, or Close would cut it short
	if !closeStorage(t, first) {
		t.FailNow()
	}
	if strings.Contains(logs.String(), "leftover temp file") {
		t.Errorf("a clean data directory should log nothing about temp files, got %q", logs.String())
	}

	leftovers := []string{
		"articles/" + atomicTempName(1),
		"articles/nested/" + atomicTempName(math.MaxUint64),
		"history/home/" + atomicTempName(42),
		"assets/home/" + atomicTempName(7),
	}
	// Near misses of the pattern, in every tree. None of these is ours to delete.
	bystanders := []string{
		"articles/.nexwiki-ABC.tmp",
		"articles/.nexwiki-0a.tmp",
		"articles/nexwiki-abc.tmp",
		"articles/.nexwiki-abc.tmp.bak",
		"history/home/.nexwiki-abc.txt",
		"assets/home/notes.tmp",
		"assets/home/.nexwiki-.tmp",
		"articles/" + atomicTempName(9) + "/inside.txt", // a directory with a temp file's name
	}
	for _, rel := range append(slices.Clone(leftovers), bystanders...) {
		plantFile(t, dataDir, rel)
	}

	second, err := openStorage(t, dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { closeStorage(t, second) })
	waitSweep(t, second)

	for _, rel := range leftovers {
		if _, err := os.Lstat(filepath.Join(dataDir, filepath.FromSlash(rel))); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("leftover temp file %s survived startup (err %v)", rel, err)
		}
	}
	for _, rel := range bystanders {
		if _, err := os.Lstat(filepath.Join(dataDir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("startup removed %s, which is not a writeFileAtomic temp file: %v", rel, err)
		}
	}
	if _, err := second.GetArticle("home"); err != nil {
		t.Errorf("home article lost across the sweep: %v", err)
	}

	var sweepLines []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "temp file") {
			sweepLines = append(sweepLines, line)
		}
	}
	// One count from the startup sweep of the article directory, one from the background sweep.
	want := "Removed 2 leftover temp file(s) from interrupted writes"
	if !slices.Equal(sweepLines, []string{want, want}) {
		t.Errorf("want two lines logging the removal counts, got %q", sweepLines)
	}
}

// seedDefaultHome treats any entry in the article directory as an existing wiki, so the sweep has
// to run first or a crash during the very first save would leave a wiki with no home page forever.
func TestLeftoverTempFileDoesNotBlockHomeSeeding(t *testing.T) {
	dataDir := t.TempDir()
	plantFile(t, dataDir, "articles/"+atomicTempName(3))
	captureLog(t)

	storage, err := openStorage(t, dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { closeStorage(t, storage) })

	if _, err := storage.GetArticle("home"); err != nil {
		t.Errorf("home page was not seeded over a leftover temp file: %v", err)
	}
	if names := dirNames(t, storage.ArticleDir); !slices.Equal(names, []string{"home.md"}) {
		t.Errorf("article directory holds %v, want only home.md", names)
	}
}

func TestLeftoverTempFileRemovalFailureDoesNotFailStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not prevent deleting a file on Windows")
	}
	dataDir := t.TempDir()
	stuck := plantFile(t, dataDir, "history/stuck/"+atomicTempName(5))
	probe := plantFile(t, dataDir, "history/stuck/probe")
	stuckDir := filepath.Dir(stuck)
	if err := os.Chmod(stuckDir, 0555); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(stuckDir, 0755) })
	if err := os.Remove(probe); err == nil {
		t.Skip("this process can delete from a read-only directory (running as root?)")
	}
	logs := captureLog(t)

	storage, err := openStorage(t, dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed because a temp file could not be removed: %v", err)
	}
	t.Cleanup(func() { closeStorage(t, storage) })
	waitSweep(t, storage)

	if _, err := os.Lstat(stuck); err != nil {
		t.Fatalf("the undeletable temp file is gone, so this test proved nothing: %v", err)
	}
	if want := "history/stuck/" + filepath.Base(stuck); !strings.Contains(logs.String(), want) {
		t.Errorf("expected a warning naming %s, got %q", want, logs.String())
	}
}

// testWaitLimit bounds every wait in these tests on something the code under test must finish or
// release. Each normally takes milliseconds; a regression that never finishes fails the test at the
// limit instead of hanging it until the package timeout.
const testWaitLimit = 30 * time.Second

// waitClosed reports whether ch is closed within testWaitLimit, failing the test if it is not.
func waitClosed(t *testing.T, ch <-chan struct{}, what string) bool {
	t.Helper()
	select {
	case <-ch:
		return true
	case <-time.After(testWaitLimit):
		t.Errorf("%s did not finish within %s", what, testWaitLimit)
		return false
	}
}

// waitSweep waits for s's background temp file sweep to finish, and stops the test if s never
// started one (sweepDone would be nil, and receiving from it would block forever), if the sweep
// does not finish within testWaitLimit, or if it finished still holding writeMu, which would hang
// the next save instead of failing.
func waitSweep(t *testing.T, s *Storage) {
	t.Helper()
	if s.sweepDone == nil {
		t.Fatal("NewStorage started no background temp file sweep")
	}
	if !waitClosed(t, s.sweepDone, "the background temp file sweep") {
		t.FailNow()
	}
	if !s.writeMu.TryLock() {
		t.Fatal("the background temp file sweep finished without releasing writeMu")
	}
	s.writeMu.Unlock()
}

// openStorage is NewStorage bounded by testWaitLimit, failing the test if it has not returned by
// then: NewStorage sweeps the article directory and then seeds under writeMu, so a sweep that left
// the lock held would otherwise hang the test there.
func openStorage(t *testing.T, dataDir string) (*Storage, error) {
	t.Helper()
	var s *Storage
	var err error
	opened := make(chan struct{})
	go func() {
		defer close(opened)
		s, err = NewStorage(dataDir)
	}()
	if !waitClosed(t, opened, "NewStorage") {
		t.FailNow()
	}
	return s, err
}

// closeStorage closes s and reports whether Close returned nil within testWaitLimit, failing the
// test otherwise. Close waits for the background sweep, so a sweep that never ends would otherwise
// hang the test in Close.
func closeStorage(t *testing.T, s *Storage) bool {
	t.Helper()
	var err error
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		err = s.Close()
	}()
	if !waitClosed(t, closed, "Close") {
		return false
	}
	if err != nil {
		t.Errorf("Close failed: %v", err)
		return false
	}
	return true
}

// stubSweepDirHook installs hook as sweepDirHook for the rest of the test. Set it before the
// NewStorage whose sweep it should see, or after that storage's sweepDone, so no sweep reads it
// while it changes.
func stubSweepDirHook(t *testing.T, hook func(dir string)) {
	t.Helper()
	prev := sweepDirHook
	sweepDirHook = hook
	t.Cleanup(func() { sweepDirHook = prev })
}

// holdSweep holds the sweep that called a sweepDirHook for dir: it closes held, if not nil, and
// waits for release. Called from inside NewStorage instead, it fails the test and returns at once,
// because the sweeps these tests hold run beside a live storage, and holding NewStorage would hang
// the test rather than fail it. The wait is bounded too, so a release that never comes fails.
func holdSweep(t *testing.T, dir string, held chan<- struct{}, release <-chan struct{}) {
	t.Helper()
	stack := make([]byte, 64<<10)
	if strings.Contains(string(stack[:runtime.Stack(stack, false)]), ".NewStorage(") {
		t.Errorf("NewStorage swept %s itself instead of leaving it to the background sweep", dir)
		return
	}
	if held != nil {
		close(held)
	}
	select {
	case <-release:
	case <-time.After(testWaitLimit):
		t.Errorf("the sweep was held at %s for %s and never released", dir, testWaitLimit)
	}
}

// expectBlockedIn waits, reading goroutine dumps, until a goroutine with fn on its stack is blocked
// in this package's code. That is the observable sign of a goroutine waiting on a lock or channel
// rather than not having run yet, which a sleep could only make likely; it is a close proxy, not a
// proof, since any wait counts (a GC assist wait, say). It reports ifQuit if quit is closed first,
// and the goroutines in fn if none blocks within testWaitLimit; it returns either way, so the
// caller can release whatever it holds rather than leave the test to hang.
func expectBlockedIn(t *testing.T, fn string, quit <-chan struct{}, ifQuit string) {
	t.Helper()
	deadline := time.Now().Add(testWaitLimit)
	buf := make([]byte, 64<<10)
	for {
		select {
		case <-quit:
			t.Error(ifQuit)
			return
		default:
		}
		n := runtime.Stack(buf, true)
		if n == len(buf) {
			buf = make([]byte, 2*len(buf))
			continue
		}
		dump := string(buf[:n])
		var inFn []string
		for _, g := range strings.Split(dump, "\n\n") {
			if strings.Contains(g, fn) {
				if blockedInNexwiki(g) {
					return
				}
				inFn = append(inFn, g)
			}
		}
		if time.Now().After(deadline) {
			if inFn != nil {
				dump = strings.Join(inFn, "\n\n")
			}
			t.Errorf("no goroutine in %s blocked within %s; goroutines:\n%s", fn, testWaitLimit, dump)
			return
		}
		time.Sleep(time.Millisecond) // just the poll interval, not a guess at how long the wait takes
	}
}

// thisPackage is this package's import path ("nexwiki/server"), read from the runtime so that frame
// matching does not depend on the module's name.
var thisPackage = func() string {
	pc, _, _, _ := runtime.Caller(0)
	return funcPackage(runtime.FuncForPC(pc).Name())
}()

// funcPackage returns the import path of a function as the runtime names it, e.g. "nexwiki/server"
// for "nexwiki/server.(*Storage).Close.func1": the path ends at the first dot after the last slash.
func funcPackage(name string) string {
	slash := strings.LastIndex(name, "/") + 1
	if dot := strings.Index(name[slash:], "."); dot >= 0 {
		return name[:slash+dot]
	}
	return name
}

// goroutineState returns the state from a goroutine dump's header line, e.g. "chan receive" from
// `goroutine 7 [chan receive, 2 minutes, locked to thread] {k: v}:`: the text between the first
// " [" and the next "]", up to any comma. Durations, thread locking, and pprof labels (which follow
// the brackets and may themselves contain brackets) are dropped. It returns "" for a line it
// cannot parse.
func goroutineState(header string) string {
	_, rest, ok := strings.Cut(header, " [")
	if !ok {
		return ""
	}
	inside, _, ok := strings.Cut(rest, "]")
	if !ok {
		return ""
	}
	state, _, _ := strings.Cut(inside, ",")
	return state
}

// blockedInNexwiki reports whether the goroutine in dump g is blocked (in any wait: not running,
// runnable, preempted, or in a syscall, and with a header that parses) in this package's code: of
// its frames from this package or from a third-party one (a dotted import path), the first is this
// package's. That tells a wait written in Close from one inside something Close calls, such as the
// search index's own Close, which must not pass for waiting on the sweep. Standard library frames,
// above either, are skipped.
func blockedInNexwiki(g string) bool {
	header, frames, _ := strings.Cut(g, "\n")
	switch goroutineState(header) {
	case "", "running", "runnable", "syscall", "preempted":
		return false
	}
	for _, line := range strings.Split(frames, "\n") {
		call := strings.LastIndex(line, "(")
		if call < 0 || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "created by ") {
			continue
		}
		pkg := funcPackage(line[:call])
		if first, _, _ := strings.Cut(pkg, "/"); pkg == thisPackage || strings.Contains(first, ".") {
			return pkg == thisPackage
		}
	}
	return false
}

// A goroutine dump header can carry a wait duration, thread locking, and pprof labels, and a label
// value can hold brackets. Only the state may decide whether the goroutine counts as blocked.
func TestGoroutineStateForSweepTests(t *testing.T) {
	for header, want := range map[string]string{
		"goroutine 1 [running]:":                                             "running",
		"goroutine 9 [runnable] {k: v}:":                                     "runnable",
		`goroutine 9 [runnable] {k: v, sweep: "a]b[c"}:`:                     "runnable",
		"goroutine 7 [chan receive, 2 minutes]:":                             "chan receive",
		"goroutine 1 [running, locked to thread]:":                           "running",
		"goroutine 8 [sync.Mutex.Lock, 3 minutes, locked to thread] {k: v}:": "sync.Mutex.Lock",
		"goroutine 5 gp=0xc000102000 m=nil [GC assist wait]:":                "GC assist wait",
		"goroutine 6 [syscall]":                                              "syscall",
		"goroutine 6 [select":                                                "",
		"not a goroutine header":                                             "",
	} {
		if got := goroutineState(header); got != want {
			t.Errorf("goroutineState(%q) = %q, want %q", header, got, want)
		}
	}

	// So a labeled runnable goroutine in this package's code must not count as blocked.
	frames := "\n" + thisPackage + ".(*Storage).Close(0xc000010000)\n\t/src/server/storage.go:1 +0x1\n"
	for header, want := range map[string]bool{
		`goroutine 9 [runnable] {sweep: "a]b"}:`:     false,
		`goroutine 9 [chan receive] {sweep: "a]b"}:`: true,
		"goroutine 9 [sync.Mutex.Lock, 1 minutes]:":  true,
		"goroutine 9 [preempted]:":                   false,
		"goroutine 9 [chan receive":                  false, // unparseable
	} {
		if got := blockedInNexwiki(header + frames); got != want {
			t.Errorf("blockedInNexwiki with header %q = %v, want %v", header, got, want)
		}
	}
}

// Only the article directory has to be clean before startup goes on. The history and asset trees
// hold a directory per article, slow to walk on a NAS mount, so NewStorage must return without
// waiting on them and sweep them afterwards.
func TestStartupSweepsHistoryAndAssetsInBackground(t *testing.T) {
	dataDir := t.TempDir()
	article := plantFile(t, dataDir, "articles/"+atomicTempName(1))
	later := []string{
		plantFile(t, dataDir, "history/home/"+atomicTempName(2)),
		plantFile(t, dataDir, "assets/home/"+atomicTempName(3)),
	}
	captureLog(t)

	// Hold the background sweep at the history and asset roots until startup has been checked.
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseSweep := func() { releaseOnce.Do(func() { close(release) }) }
	stubSweepDirHook(t, func(dir string) {
		if dir == filepath.Join(dataDir, "history") || dir == filepath.Join(dataDir, "assets") {
			holdSweep(t, dir, nil, release)
		}
	})

	storage, err := openStorage(t, dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { closeStorage(t, storage) })
	t.Cleanup(releaseSweep) // runs first, so Close is not left waiting on a sweep still held

	if _, err := os.Lstat(article); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("leftover temp file in the article directory survived startup (err %v)", err)
	}
	if _, err := storage.GetArticle("home"); err != nil {
		t.Errorf("home page was not seeded: %v", err)
	}
	for _, path := range later {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("%s is gone while the background sweep was held, so startup swept it: %v", path, err)
		}
	}

	releaseSweep()
	waitSweep(t, storage)
	for _, path := range later {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("background sweep left %s behind (err %v)", path, err)
		}
	}
}

// The background sweep runs alongside saves, and a temp file mid-save looks exactly like a
// leftover. writeMu, which every writeFileAtomic caller holds, must keep the sweep out until the
// save has renamed its temp file into place; and the sweep must not keep the lock between
// directories, or it would stall saves for its whole walk.
func TestTempFileSweepWaitsForWriteMu(t *testing.T) {
	storage, err := openStorage(t, t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { closeStorage(t, storage) })
	waitSweep(t, storage) // so only the sweep started below sees the hook
	logs := captureLog(t)

	pageDir := filepath.Join(storage.HistoryDir, "page")
	inFlight := plantFile(t, storage.DataDir, "history/page/"+atomicTempName(11))
	stranded := plantFile(t, storage.DataDir, "assets/page/"+atomicTempName(12))

	reached := make(chan struct{})
	proceed := make(chan struct{})
	stubSweepDirHook(t, func(dir string) {
		if dir == pageDir {
			holdSweep(t, dir, reached, proceed)
		}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		storage.removeLeftoverTempFiles(nil, storage.HistoryDir, storage.AssetDir)
	}()

	select {
	case <-reached:
	case <-done:
		t.Fatal("the sweep never reached the directory holding the in-flight temp file")
	case <-time.After(testWaitLimit):
		t.Fatalf("the sweep did not reach the directory holding the in-flight temp file within %s", testWaitLimit)
	}
	// The sweep has finished other directories and not yet started this one.
	if !storage.writeMu.TryLock() {
		close(proceed)
		waitClosed(t, done, "the sweep")
		t.Fatal("the sweep holds writeMu between directories, so it would stall saves for its whole walk")
	}
	// A save now holds the lock with its temp file in pageDir. Let the sweep try that directory.
	close(proceed)
	expectBlockedIn(t, ".removeLeftoverTempFiles(", done,
		"the sweep finished without waiting for writeMu, which a save in progress held")
	if _, err := os.Lstat(inFlight); err != nil {
		t.Errorf("the sweep removed a temp file while writeMu was held: %v", err)
	}

	// The save renames its temp file into place, and only then releases the lock.
	snapshot := filepath.Join(pageDir, "2.md.gz")
	if err := os.Rename(inFlight, snapshot); err != nil {
		t.Errorf("Rename failed: %v", err)
	}
	storage.writeMu.Unlock()
	if !waitClosed(t, done, "the sweep") {
		t.FailNow()
	}

	if got, err := os.ReadFile(snapshot); err != nil || string(got) != "partial" {
		t.Errorf("saved snapshot = %q (err %v), want the save's content intact", got, err)
	}
	if names := dirNames(t, pageDir); !slices.Equal(names, []string{"2.md.gz"}) {
		t.Errorf("history directory holds %v, want only 2.md.gz", names)
	}
	if _, err := os.Lstat(stranded); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a genuine leftover survived the sweep (err %v)", err)
	}
	if out := logs.String(); out != "Removed 1 leftover temp file(s) from interrupted writes\n" {
		t.Errorf("want only the count of the one genuine leftover logged, got %q", out)
	}
}

// Close must stop the background sweep and wait for it: a sweep that ran on would, at shutdown,
// delete files after the storage was closed, and in tests walk a temp directory being removed.
func TestCloseStopsBackgroundTempFileSweep(t *testing.T) {
	dataDir := t.TempDir()
	leftover := plantFile(t, dataDir, "history/page/"+atomicTempName(4))
	captureLog(t)

	paused := make(chan struct{})
	release := make(chan struct{})
	stubSweepDirHook(t, func(dir string) {
		if dir == filepath.Join(dataDir, "history") {
			holdSweep(t, dir, paused, release)
		}
	})
	storage, err := openStorage(t, dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { closeStorage(t, storage) }) // a no-op once the test has closed it
	if storage.sweepDone == nil {
		t.Fatal("NewStorage started no background temp file sweep")
	}
	select {
	case <-paused:
	case <-storage.sweepDone:
		t.Fatal("the background sweep never reached the history tree")
	case <-time.After(testWaitLimit):
		t.Fatalf("the background sweep did not reach the history tree within %s", testWaitLimit)
	}

	var closeErr error
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		closeErr = storage.Close()
	}()
	expectBlockedIn(t, ".(*Storage).Close", closed, "Close returned while the background sweep was still running")
	close(release)
	if !waitClosed(t, closed, "Close") {
		t.FailNow()
	}
	if closeErr != nil {
		t.Fatalf("Close failed: %v", closeErr)
	}
	if _, err := os.Lstat(leftover); err != nil {
		t.Errorf("the sweep went on after Close asked it to stop: %v", err)
	}
}

// The sweep lists directories itself instead of using filepath.WalkDir, so it must keep WalkDir's
// rule below a root: a symlink is neither followed nor removed. Following one could delete a file
// outside the data directory or loop, and one named like a temp file is not something
// writeFileAtomic made.
func TestTempFileSweepIgnoresSymlinksBelowRoot(t *testing.T) {
	base := t.TempDir()
	storage, err := openStorage(t, filepath.Join(base, "data"))
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { closeStorage(t, storage) })
	waitSweep(t, storage) // the links below are only ever swept by the guarded call further down

	historyDir := storage.HistoryDir
	pageDir := filepath.Join(historyDir, "page")
	outsideTemp := plantFile(t, base, "outside/"+atomicTempName(21))
	target := plantFile(t, base, "outside/target.md.gz")
	leftover := plantFile(t, historyDir, "page/"+atomicTempName(22))
	realDirs := map[string]bool{} // taken before any link exists, so none is reached through one
	if err := filepath.WalkDir(historyDir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			realDirs[path] = true
		}
		return err
	}); err != nil {
		t.Fatalf("WalkDir failed: %v", err)
	}
	links := map[string]string{
		filepath.Join(historyDir, "linked"):        filepath.Dir(outsideTemp), // a directory outside the data tree
		filepath.Join(pageDir, "loop"):             historyDir,                // an ancestor
		filepath.Join(pageDir, atomicTempName(23)): target,                    // a regular file, under a temp file's name
	}
	for link, to := range links {
		if err := os.Symlink(to, link); err != nil {
			t.Skipf("symlinks unavailable here: %v", err)
		}
	}

	// Only the real directories may be visited, once each. Anything else was reached through a link,
	// so stop the sweep there: following the loop then fails the test instead of hanging it.
	stop := make(chan struct{})
	visits := map[string]int{}
	var strayed []string
	stubSweepDirHook(t, func(dir string) {
		if visits[dir]++; realDirs[dir] && visits[dir] == 1 {
			return
		}
		if strayed == nil {
			close(stop)
		}
		strayed = append(strayed, dir)
	})
	logs := captureLog(t)
	swept := make(chan struct{})
	go func() {
		defer close(swept)
		storage.removeLeftoverTempFiles(stop, historyDir)
	}()
	if !waitClosed(t, swept, "the sweep") {
		t.FailNow() // strayed is still the sweep's to write
	}

	if strayed != nil {
		t.Fatalf("the sweep followed a symlink into %v", strayed)
	}
	if _, err := os.Lstat(leftover); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the leftover beside the links survived (err %v)", err)
	}
	if _, err := os.Lstat(outsideTemp); err != nil {
		t.Errorf("a temp-named file outside the data tree is gone: %v", err)
	}
	for link := range links {
		if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("symlink %s was removed or replaced (err %v)", link, err)
		}
	}
	if _, err := os.Lstat(target); err != nil {
		t.Errorf("the temp-named link's target is gone: %v", err)
	}
	if out := logs.String(); out != "Removed 1 leftover temp file(s) from interrupted writes\n" {
		t.Errorf("want only the one genuine leftover counted and no warnings, got %q", out)
	}
}

// filepath.WalkDir does not descend into a root that is itself a symlink, and linking the data
// directory's trees out to other storage is a plausible NAS layout. The sweep must reach them.
func TestTempFileSweepFollowsSymlinkedRoots(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	elsewhere := filepath.Join(base, "elsewhere")
	if err := os.Mkdir(dataDir, 0755); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}
	for _, tree := range []string{"articles", "history", "assets"} {
		target := filepath.Join(elsewhere, tree)
		if err := os.MkdirAll(target, 0755); err != nil {
			t.Fatalf("MkdirAll failed: %v", err)
		}
		if err := os.Symlink(target, filepath.Join(dataDir, tree)); err != nil {
			t.Skipf("symlinks unavailable here: %v", err)
		}
	}
	article := plantFile(t, elsewhere, "articles/"+atomicTempName(1))
	later := []string{
		plantFile(t, elsewhere, "history/page/"+atomicTempName(2)),
		plantFile(t, elsewhere, "assets/page/"+atomicTempName(3)),
	}
	captureLog(t)

	storage, err := openStorage(t, dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { closeStorage(t, storage) })

	if _, err := os.Lstat(article); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("leftover under a symlinked article directory survived startup (err %v)", err)
	}
	if _, err := storage.GetArticle("home"); err != nil {
		t.Errorf("home page was not seeded over a symlinked article directory: %v", err)
	}
	waitSweep(t, storage)
	for _, path := range later {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("leftover %s under a symlinked root survived the sweep (err %v)", path, err)
		}
	}
	for _, tree := range []string{"articles", "history", "assets"} {
		if info, err := os.Lstat(filepath.Join(dataDir, tree)); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is no longer a symlink (err %v)", tree, err)
		}
	}
}

// Downloads are served without writeMu, so re-uploading an asset must swap in a new file rather
// than truncate and rewrite the one a download may be reading.
func TestSaveAssetReplacesFileAtomically(t *testing.T) {
	storage, err := openStorage(t, t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { closeStorage(t, storage) })
	waitSweep(t, storage) // SaveAsset takes writeMu, which a broken sweep could leave held

	oldData := bytes.Repeat([]byte("old "), 16<<10)
	newData := bytes.Repeat([]byte("new!"), 8<<10)
	if _, err := storage.SaveAsset("Asset Page", "chart.png", oldData); err != nil {
		t.Fatalf("SaveAsset failed: %v", err)
	}
	path, err := storage.GetAssetPath("asset-page", "chart.png")
	if err != nil {
		t.Fatalf("GetAssetPath failed: %v", err)
	}

	// A download already in flight. On Windows an open handle blocks the replacing rename itself.
	var download *os.File
	if runtime.GOOS != "windows" {
		if download, err = os.Open(path); err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		t.Cleanup(func() { _ = download.Close() })
	}

	if _, err := storage.SaveAsset("Asset Page", "chart.png", newData); err != nil {
		t.Fatalf("re-upload SaveAsset failed: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, newData) {
		t.Errorf("asset was not replaced with the re-uploaded content (err %v)", err)
	}
	if names := dirNames(t, filepath.Dir(path)); !slices.Equal(names, []string{"chart.png"}) {
		t.Errorf("asset directory holds %v, want only chart.png (a temp file was left behind)", names)
	}
	if download != nil {
		got, err := io.ReadAll(download)
		if err != nil || !bytes.Equal(got, oldData) {
			t.Errorf("an in-flight download read %d bytes of mixed or new content, want the whole old file (err %v)", len(got), err)
		}
	}
}

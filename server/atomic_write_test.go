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
	first, err := NewStorage(dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
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

	second, err := NewStorage(dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

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
	if len(sweepLines) != 1 || !strings.HasPrefix(sweepLines[0], "Removed 4 leftover temp file") {
		t.Errorf("want one line logging the removal count, got %q", sweepLines)
	}
}

// seedDefaultHome treats any entry in the article directory as an existing wiki, so the sweep has
// to run first or a crash during the very first save would leave a wiki with no home page forever.
func TestLeftoverTempFileDoesNotBlockHomeSeeding(t *testing.T) {
	dataDir := t.TempDir()
	plantFile(t, dataDir, "articles/"+atomicTempName(3))
	captureLog(t)

	storage, err := NewStorage(dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

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

	storage, err := NewStorage(dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed because a temp file could not be removed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	if _, err := os.Lstat(stuck); err != nil {
		t.Fatalf("the undeletable temp file is gone, so this test proved nothing: %v", err)
	}
	if want := "history/stuck/" + filepath.Base(stuck); !strings.Contains(logs.String(), want) {
		t.Errorf("expected a warning naming %s, got %q", want, logs.String())
	}
}

// Downloads are served without writeMu, so re-uploading an asset must swap in a new file rather
// than truncate and rewrite the one a download may be reading.
func TestSaveAssetReplacesFileAtomically(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

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

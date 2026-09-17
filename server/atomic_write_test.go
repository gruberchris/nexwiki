package server

import (
	"os"
	"path/filepath"
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

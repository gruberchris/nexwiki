package server

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newSeedFixture returns a server over fresh storage and the path the guidelines skill lives at.
func newSeedFixture(t *testing.T) (*Server, string) {
	t.Helper()
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	srv := NewServer(storage, "Test Wiki", "default", false, NewEventBus(), "0.0.1", "8080")
	return srv, filepath.Join(storage.ArticleDir, AgentGuidelinesSlug+".md")
}

func TestSeedAgentGuidelinesSeedsAMissingFile(t *testing.T) {
	srv, _ := newSeedFixture(t)
	logs := captureLog(t)

	srv.SeedAgentGuidelinesIfMissing()

	art, err := srv.Storage.GetArticle(AgentGuidelinesSlug)
	if err != nil {
		t.Fatalf("the skill was not seeded: %v", err)
	}
	if art.Type != ContentTypeSkill {
		t.Errorf("seeded type %q, want %q", art.Type, ContentTypeSkill)
	}
	if !strings.Contains(logs.String(), "Seeded default governance skill: "+AgentGuidelinesSlug) {
		t.Errorf("seeding was not logged:\n%s", logs.String())
	}
}

// The tests below pin that a guidelines file which exists but cannot be loaded is still the user's:
// seeding warns and writes nothing, rather than replacing it with the default.

func TestSeedAgentGuidelinesLeavesAMalformedFileAlone(t *testing.T) {
	srv, path := newSeedFixture(t)
	want := []byte("No front matter here, just the owner's notes.\n")
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}
	logs := captureLog(t)

	srv.SeedAgentGuidelinesIfMissing()

	assertSeedWarnedWithoutWriting(t, logs)
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
		t.Errorf("the malformed file changed: got %q (read error %v), want %q", got, err, want)
	}
}

func TestSeedAgentGuidelinesLeavesAnUnreadableFileAlone(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions cannot make a file unreadable on Windows or as root")
	}
	srv, path := newSeedFixture(t)
	want := []byte("---\ntitle: NexWiki Agent Guidelines\ntype: AI-Agent-Skill\n---\n\nThe owner's own rules.\n")
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	logs := captureLog(t)

	srv.SeedAgentGuidelinesIfMissing()

	assertSeedWarnedWithoutWriting(t, logs)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
		t.Errorf("the unreadable file changed: got %q (read error %v), want %q", got, err, want)
	}
}

// A symlink whose target is missing is left in place too, which is why seeding checks with Lstat.
func TestSeedAgentGuidelinesLeavesADanglingSymlinkAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	srv, path := newSeedFixture(t)
	target := filepath.Join(t.TempDir(), "moved-away.md")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	logs := captureLog(t)

	srv.SeedAgentGuidelinesIfMissing()

	assertSeedWarnedWithoutWriting(t, logs)
	if got, err := os.Readlink(path); err != nil || got != target {
		t.Errorf("the symlink changed: now points at %q (error %v), want %q", got, err, target)
	}
	if _, err := os.Lstat(target); err == nil {
		t.Errorf("seeding wrote through the symlink to %s", target)
	}
}

// assertSeedWarnedWithoutWriting checks the log for a warning naming the guidelines file and for the
// absence of the line a successful seed logs.
func assertSeedWarnedWithoutWriting(t *testing.T, logs *lockedBuffer) {
	t.Helper()
	rel := filepath.Join("articles", AgentGuidelinesSlug+".md")
	var warned bool
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.HasPrefix(line, "Warning:") && strings.Contains(line, rel) {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no warning naming %s was logged:\n%s", rel, logs.String())
	}
	if strings.Contains(logs.String(), "Seeded default governance skill") {
		t.Errorf("seeding reported writing the default over the existing file:\n%s", logs.String())
	}
}

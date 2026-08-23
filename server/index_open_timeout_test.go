package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIndexOpenTimeoutCoversOnlyTheLock pins the scope of the startup deadline.
//
// It used to wrap the whole of NewStorage from main.go — index open, seeding, the one-time status
// migration, and the boot index sync — even though only bleve.Open can block forever (it takes an
// exclusive bbolt lock and waits with no timeout of its own). On the NAS the status-field
// migration ran past the 15-second budget and the process was killed with
// "could not open the search index ... another process is holding it open", blaming a lock
// conflict that had not happened. It recovered only because the migration is idempotent and the
// stack had a restart policy.
func TestIndexOpenTimeoutCoversOnlyTheLock(t *testing.T) {
	dir := t.TempDir()

	first, err := NewStorage(dir)
	if err != nil {
		t.Fatalf("first open failed: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })

	// A second opener finds the lock held. This is the case the deadline exists for.
	start := time.Now()
	_, err = openSearchIndex(filepath.Join(dir, "search.bleve"), 300*time.Millisecond)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrSearchIndexLocked) {
		t.Fatalf("a contended index must report ErrSearchIndexLocked, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("the deadline did not bound the wait: %s", elapsed)
	}
}

// TestSlowBootWorkIsNotOnTheIndexDeadline is the regression the NAS hit: boot work that takes
// longer than the index deadline must still complete, because the deadline no longer wraps it.
func TestSlowBootWorkIsNotOnTheIndexDeadline(t *testing.T) {
	if IndexOpenTimeout < time.Second {
		t.Fatalf("IndexOpenTimeout is implausibly small: %s", IndexOpenTimeout)
	}

	dir := t.TempDir()
	articles := filepath.Join(dir, "articles")
	if err := os.MkdirAll(articles, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Enough pre-field documents that the one-time migration has real work to do at boot.
	now := time.Now()
	const seeded = 40
	for i := 0; i < seeded; i++ {
		slug := "legacy-plan-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		a := &Article{
			Type: ContentTypePlan, Title: slug, Slug: slug, Tags: []string{"proj", "wip"},
			CreatedAt: now, Timestamp: now, Version: 1, Content: "# " + slug,
		}
		if err := os.WriteFile(filepath.Join(articles, slug+".md"),
			[]byte(serializeFrontMatter(a)+a.Content), 0644); err != nil {
			t.Fatalf("seed %s: %v", slug, err)
		}
	}

	s, err := NewStorage(dir)
	if err != nil {
		t.Fatalf("boot with migration work failed: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// The migration must have finished — a run cut short would leave documents still tagged.
	metas, err := s.ListArticles()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	migrated := 0
	for _, m := range metas {
		if m.Type != ContentTypePlan {
			continue
		}
		for _, tag := range m.Tags {
			if strings.EqualFold(tag, "wip") {
				t.Fatalf("plan %q still carries a status tag — the migration did not complete", m.Slug)
			}
		}
		if m.Status != "implementing" {
			t.Errorf("plan %q: status %q, want implementing", m.Slug, m.Status)
		}
		migrated++
	}
	if migrated != seeded {
		t.Errorf("migrated %d plans, expected %d", migrated, seeded)
	}
	if _, err := os.Stat(filepath.Join(dir, statusFieldMigrationMarker)); err != nil {
		t.Errorf("the migration marker was not written, so it would re-run: %v", err)
	}
}

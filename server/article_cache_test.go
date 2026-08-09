package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// seedCorpus creates n articles, each linking to the previous one so backlink scans have work.
func seedCorpus(t testing.TB, storage *Storage, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		body := fmt.Sprintf("Article body number %d with enough prose to be worth parsing.\n", i)
		if i > 0 {
			body += fmt.Sprintf("\nSee also [[Bench Article %d]].\n", i-1)
		}
		if _, err := storage.SaveArticle("", fmt.Sprintf("Bench Article %d", i), body,
			"summary", "", "", "seed", []string{"bench"}, ""); err != nil {
			t.Fatalf("SaveArticle failed: %v", err)
		}
	}
}

// TestCachePicksUpExternalEdits is the correctness risk this cache takes on, and the reason it
// validates by mtime+size rather than invalidating only on NexWiki's own writes.
//
// NexWiki's storage pitch is that the Markdown files stay yours and stay editable by anything —
// vim, Obsidian, a sync client. A cache that only noticed NexWiki's own writes would serve stale
// content forever after an external edit, which is a worse failure than the cost it saves.
func TestCachePicksUpExternalEdits(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	art, err := storage.SaveArticle("", "External Edit Probe", "original body", "first summary",
		"", "", "seed", []string{"before"}, "")
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	// Warm the cache.
	if _, err := storage.ListArticles(); err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}

	// Edit the file behind NexWiki's back, exactly as an external editor would.
	path := filepath.Join(storage.ArticleDir, art.Slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	edited := strings.Replace(string(raw), "first summary", "externally edited summary", 1)
	edited = strings.Replace(edited, "before", "after", 1)
	// Ensure the modification time actually differs even on coarse-grained filesystems.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte(edited), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	articles, err := storage.ListArticles()
	if err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	if len(articles) != 1 {
		t.Fatalf("expected 1 article, got %d", len(articles))
	}
	if articles[0].Description != "externally edited summary" {
		t.Errorf("cache served stale metadata after an external edit: got %q", articles[0].Description)
	}
	if len(articles[0].Tags) != 1 || articles[0].Tags[0] != "after" {
		t.Errorf("cache served stale tags after an external edit: got %v", articles[0].Tags)
	}
}

// TestCacheInvalidatesLinkTargetsOnEdit covers the same staleness risk for the link graph, which
// backlink lookups depend on.
func TestCacheInvalidatesLinkTargetsOnEdit(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	if _, err := storage.SaveArticle("", "Link Target", "target body", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	source, err := storage.SaveArticle("", "Link Source", "no links yet", "", "", "", "seed", nil, "")
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	if links, err := storage.GetBacklinks("link-target"); err != nil || len(links) != 0 {
		t.Fatalf("expected no backlinks initially, got %d (err=%v)", len(links), err)
	}

	// Add the link through NexWiki and confirm the cached graph updates.
	if _, err := storage.SaveArticle(source.Slug, "Link Source", "now links to [[Link Target]]",
		"", "", "", "edit", nil, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	links, err := storage.GetBacklinks("link-target")
	if err != nil {
		t.Fatalf("GetBacklinks failed: %v", err)
	}
	if len(links) != 1 || links[0].Slug != "link-source" {
		t.Fatalf("expected link-source as a backlink, got %v", links)
	}

	// And again for an edit made outside NexWiki.
	path := filepath.Join(storage.ArticleDir, source.Slug+".md")
	raw, _ := os.ReadFile(path)
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte(strings.Replace(string(raw), "[[Link Target]]", "no link", 1)), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if links, err := storage.GetBacklinks("link-target"); err != nil || len(links) != 0 {
		t.Errorf("cache served a stale link graph after an external edit: %d backlinks (err=%v)", len(links), err)
	}
}

// TestCachePrunesDeletedArticles pins that a long-lived process does not accumulate metadata for
// files that no longer exist.
func TestCachePrunesDeletedArticles(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	seedCorpus(t, storage, 5)
	if _, err := storage.ListArticles(); err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}

	storage.cache.mu.Lock()
	before := len(storage.cache.entries)
	storage.cache.mu.Unlock()
	if before < 5 {
		t.Fatalf("expected at least 5 cached entries, got %d", before)
	}

	if err := storage.DeleteArticle("bench-article-0"); err != nil {
		t.Fatalf("DeleteArticle failed: %v", err)
	}
	if _, err := storage.ListArticles(); err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}

	storage.cache.mu.Lock()
	after := len(storage.cache.entries)
	storage.cache.mu.Unlock()
	if after != before-1 {
		t.Errorf("expected the deleted article to be pruned: %d entries before, %d after", before, after)
	}
}

// TestCachedMetadataIsCopied pins that a caller mutating returned tags cannot corrupt the shared
// cache entry — the classic bug when a cache hands out its own values.
func TestCachedMetadataIsCopied(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	if _, err := storage.SaveArticle("", "Mutation Probe", "body", "", "", "", "seed",
		[]string{"original"}, ""); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	first, err := storage.ListArticles()
	if err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	first[0].Tags[0] = "mutated"
	first[0].Title = "Mutated Title"

	second, err := storage.ListArticles()
	if err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	if second[0].Tags[0] != "original" {
		t.Errorf("caller mutation leaked into the cache: tag is %q", second[0].Tags[0])
	}
	if second[0].Title != "Mutation Probe" {
		t.Errorf("caller mutation leaked into the cache: title is %q", second[0].Title)
	}
}

// TestConcurrentListArticlesIsSafe exercises the cache under parallel readers and a writer.
// Run with -race.
func TestConcurrentListArticlesIsSafe(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	seedCorpus(t, storage, 10)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := storage.ListArticles(); err != nil {
					t.Errorf("ListArticles failed: %v", err)
					return
				}
				if _, err := storage.GetBacklinks("bench-article-0"); err != nil {
					t.Errorf("GetBacklinks failed: %v", err)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 10; j++ {
			_, _ = storage.SaveArticle("", fmt.Sprintf("Concurrent %d", j), "body", "", "", "", "w", nil, "")
		}
	}()
	wg.Wait()
}

// --- benchmarks -------------------------------------------------------------------------------
//
// Run: go test ./server/ -run XXX -bench 'BenchmarkListArticles|BenchmarkGetBacklinks' -benchtime 20x

func benchStorage(b *testing.B, n int) *Storage {
	b.Helper()
	storage, err := NewStorage(b.TempDir())
	if err != nil {
		b.Fatalf("NewStorage failed: %v", err)
	}
	b.Cleanup(func() { _ = storage.Close() })
	seedCorpus(b, storage, n)
	return storage
}

func BenchmarkListArticles(b *testing.B) {
	storage := benchStorage(b, 200)
	if _, err := storage.ListArticles(); err != nil {
		b.Fatalf("warmup failed: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := storage.ListArticles(); err != nil {
			b.Fatalf("ListArticles failed: %v", err)
		}
	}
}

func BenchmarkGetBacklinks(b *testing.B) {
	storage := benchStorage(b, 200)
	if _, err := storage.GetBacklinks("bench-article-100"); err != nil {
		b.Fatalf("warmup failed: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := storage.GetBacklinks("bench-article-100"); err != nil {
			b.Fatalf("GetBacklinks failed: %v", err)
		}
	}
}

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStorageVersioning(t *testing.T) {
	tempDir := t.TempDir()

	storage, err := NewStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to initialize storage: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	t.Cleanup(func() { _ = storage.Close() })

	// 1. Test Saving Initial version
	art, err := storage.SaveArticle("", "Test Page", "# Version 1 content", "", "", "", "Initial commit", []string{"tag1", "tag2"}, "")
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	if art.Version != 1 {
		t.Errorf("Expected version 1, got %d", art.Version)
	}
	if len(art.Tags) != 2 || art.Tags[0] != "tag1" || art.Tags[1] != "tag2" {
		t.Errorf("Expected tags ['tag1', 'tag2'], got %v", art.Tags)
	}
	if art.EditSummary != "Initial commit" {
		t.Errorf("Expected edit summary 'Initial commit', got '%s'", art.EditSummary)
	}

	// 2. Test saving second version
	art2, err := storage.SaveArticle("test-page", "Test Page", "# Version 2 content", "", "", "", "Typo fix", []string{"tag1", "tag2"}, "")
	if err != nil {
		t.Fatalf("SaveArticle update failed: %v", err)
	}

	if art2.Version != 2 {
		t.Errorf("Expected version 2, got %d", art2.Version)
	}
	if art2.EditSummary != "Typo fix" {
		t.Errorf("Expected edit summary 'Typo fix', got '%s'", art2.EditSummary)
	}

	// Verify compressed history directory exists and contains files
	histFolder := filepath.Join(storage.HistoryDir, "test-page")
	if _, err := os.Stat(filepath.Join(histFolder, "1.md.gz")); err != nil {
		t.Errorf("Expected compressed history file 1.md.gz: %v", err)
	}
	if _, err := os.Stat(filepath.Join(histFolder, "2.md.gz")); err != nil {
		t.Errorf("Expected compressed history file 2.md.gz: %v", err)
	}

	// 3. Test listing history
	history, err := storage.GetArticleHistory("test-page")
	if err != nil {
		t.Fatalf("GetArticleHistory failed: %v", err)
	}

	if len(history) != 2 {
		t.Errorf("Expected 2 history versions, got %d", len(history))
	}
	if history[0].Version != 2 || history[1].Version != 1 {
		t.Errorf("Expected sorted history descending (2, 1), got (%d, %d)", history[0].Version, history[1].Version)
	}

	// 4. Test reading a single version
	v1, err := storage.GetArticleVersion("test-page", 1)
	if err != nil {
		t.Fatalf("GetArticleVersion failed: %v", err)
	}
	if v1.Content != "# Version 1 content" {
		t.Errorf("Expected content '# Version 1 content', got '%s'", v1.Content)
	}

	// 5. Test slug renaming
	art3, err := storage.SaveArticle("test-page", "Renamed Page", "# Renamed content", "", "", "", "Renamed slug", []string{"tag1", "tag2", "renamed-tag"}, "")
	if err != nil {
		t.Fatalf("SaveArticle rename failed: %v", err)
	}

	newSlug := art3.Slug // "renamed-page"
	if newSlug != "renamed-page" {
		t.Errorf("Expected slug 'renamed-page', got '%s'", newSlug)
	}

	// Verify history folder was renamed
	if _, err := os.Stat(filepath.Join(storage.HistoryDir, "renamed-page")); err != nil {
		t.Errorf("Expected history folder renamed to renamed-page: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storage.HistoryDir, "test-page")); !os.IsNotExist(err) {
		t.Errorf("Expected old history folder test-page to be removed")
	}

	// 6. Test reverting
	art4, err := storage.RevertArticle("renamed-page", 1)
	if err != nil {
		t.Fatalf("RevertArticle failed: %v", err)
	}
	if art4.Content != "# Version 1 content" {
		t.Errorf("Expected reverted content to match v1, got '%s'", art4.Content)
	}
	if art4.Version != 4 {
		t.Errorf("Expected revert to increment version to 4, got %d", art4.Version)
	}

	// 7. Test global tag deletion
	art5, err := storage.SaveArticle("", "Tag Delete Test", "# Content", "", "", "", "Summary", []string{"tag1", "delete-me"}, "")
	if err != nil {
		t.Fatalf("SaveArticle for tag delete test failed: %v", err)
	}

	err = storage.DeleteTagGlobally("delete-me")
	if err != nil {
		t.Fatalf("DeleteTagGlobally failed: %v", err)
	}

	art5Updated, err := storage.GetArticle(art5.Slug)
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}

	for _, tName := range art5Updated.Tags {
		if tName == "delete-me" {
			t.Errorf("Expected 'delete-me' tag to be deleted globally")
		}
	}

	// Verify that protected tool-managed memory-scope tags cannot be deleted globally
	err = storage.DeleteTagGlobally("memory-nexwiki")
	if err == nil {
		t.Errorf("Expected error deleting protected memory-scope tag, got nil")
	}

	// Verify search filtering of agent documents (by type) by default
	_, err = storage.SaveArticle("", "AI Plan Page", "# Content", "", "", "", "Summary", nil, ContentTypePlan)
	if err != nil {
		t.Fatalf("SaveArticle for AI plan failed: %v", err)
	}

	results, err := storage.SearchArticles("AI")
	if err != nil {
		t.Fatalf("SearchArticles failed: %v", err)
	}

	for _, res := range results {
		if res.Slug == "ai-plan-page" {
			t.Errorf("Expected AI plan page to be filtered out from default search")
		}
	}

	// A query that names the plan class ('plan') opts agent plans back into the results
	resultsExplicit, err := storage.SearchArticles("plan")
	if err != nil {
		t.Fatalf("SearchArticles explicit failed: %v", err)
	}

	foundAIPlan := false
	for _, res := range resultsExplicit {
		if res.Slug == "ai-plan-page" {
			foundAIPlan = true
			break
		}
	}
	if !foundAIPlan {
		t.Errorf("Expected explicit search to find AI plan page")
	}

	// Clean up
	_ = storage.DeleteArticle("ai-plan-page")
	_ = storage.DeleteArticle("tag-delete-test")

	// 8. Test deleting an article clears the history folder
	err = storage.DeleteArticle("renamed-page")
	if err != nil {
		t.Fatalf("DeleteArticle failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storage.HistoryDir, "renamed-page")); !os.IsNotExist(err) {
		t.Errorf("Expected history directory for renamed-page to be deleted completely")
	}
}

func TestStorageHistoryInitialization(t *testing.T) {
	tempDir := t.TempDir()
	storage, err := NewStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to initialize storage: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	// 1. Manually write a file to disk to simulate a seeded/pre-existing file
	slug := "pre-existing"
	filePath := filepath.Join(storage.ArticleDir, slug+".md")
	fmt.Printf("TEST: Manually writing file to: %s\n", filePath)
	content := "---\ntitle: Pre-existing Page\nslug: pre-existing\ncreated_at: 2026-06-01T12:00:00Z\nupdated_at: 2026-06-01T12:00:00Z\nversion: 1\n---\n# Pre-existing Content\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write manual file: %v", err)
	}

	// 2. Perform an edit using SaveArticle (this is the first edit via storage)
	art, err := storage.SaveArticle(slug, "Pre-existing Page", "# Edited Content", "", "", "", "First Edit", []string{"tag"}, "")
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	if art.Version != 2 {
		t.Errorf("Expected version 2, got %d", art.Version)
	}

	// 3. Verify the history folder was created and contains BOTH version 1 (original) and version 2 (edit)
	v1, err := storage.GetArticleVersion(art.Slug, 1)
	if err != nil {
		t.Fatalf("Failed to retrieve version 1: %v", err)
	}
	if v1.Content != "# Pre-existing Content" {
		t.Errorf("Expected version 1 content '# Pre-existing Content', got '%s'", v1.Content)
	}

	v2, err := storage.GetArticleVersion(art.Slug, 2)
	if err != nil {
		t.Fatalf("Failed to retrieve version 2: %v", err)
	}
	if v2.Content != "# Edited Content" {
		t.Errorf("Expected version 2 content '# Edited Content', got '%s'", v2.Content)
	}
}

func TestStorageUpdateArticleTags(t *testing.T) {
	tempDir := t.TempDir()
	storage, err := NewStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to initialize storage: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	// 1. Create a page
	art, err := storage.SaveArticle("", "Original Page", "# Body Content", "", "", "", "Initial commit", []string{"initial"}, "")
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	// 2. Update tags via UpdateArticleTags
	updated, err := storage.UpdateArticleTags(art.Slug, []string{"new-tag", "another-tag"}, art.Version, "Update tags only")
	if err != nil {
		t.Fatalf("UpdateArticleTags failed: %v", err)
	}

	if updated.Version != 2 {
		t.Errorf("Expected version 2, got %d", updated.Version)
	}
	if len(updated.Tags) != 2 || updated.Tags[0] != "new-tag" || updated.Tags[1] != "another-tag" {
		t.Errorf("Expected updated tags, got %v", updated.Tags)
	}
	if updated.Content != "# Body Content" {
		t.Errorf("Expected content to remain unchanged, got '%s'", updated.Content)
	}

	// 3. Verify version conflict checking
	_, err = storage.UpdateArticleTags(art.Slug, []string{"fail"}, 1, "Should conflict")
	if err == nil {
		t.Errorf("Expected version conflict error, got nil")
	}
}

// TestSearchSnippetsAreHTMLEscaped guards the search-snippet XSS sink: the frontend renders
// SearchResult.Snippets with dangerouslySetInnerHTML, so every snippet the server emits must be
// entity-escaped. Bleve escapes the fragments it produces; the empty-fragment fallback path in
// SearchArticles must do the same for the raw article body it slices.
func TestSearchSnippetsAreHTMLEscaped(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("Failed to initialize storage: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	payload := `<img src=x onerror="alert(1)">`
	art, err := storage.SaveArticle("", "Xss Probe Page", payload+"\n\nzqxjrare body text",
		"", "", "", "seed", nil, "")
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	if err := storage.IndexArticle(art); err != nil {
		t.Fatalf("IndexArticle failed: %v", err)
	}

	// Query by exact title so the agent-type filter does not exclude the hit, and so Bleve is
	// likely to match on the title field (the path that yields no content fragments).
	results, err := storage.SearchArticles("Xss Probe Page")
	if err != nil {
		t.Fatalf("SearchArticles failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one search result")
	}

	for _, res := range results {
		for _, snippet := range res.Snippets {
			// <mark>/</mark> are the highlighter's own tags and are the only markup allowed
			// through; any other raw tag means article text reached the DOM unescaped.
			stripped := strings.NewReplacer("<mark>", "", "</mark>", "").Replace(snippet)
			if strings.Contains(stripped, "<") {
				t.Errorf("snippet contains unescaped markup and would execute in the browser: %q", snippet)
			}
		}
	}
}

// TestConcurrentSavesDoNotLoseRevisions guards the version-assignment race. SaveArticle picks the
// next revision number by scanning the history directory and then writes it; without writeMu, two
// writers can both observe the same highest version, compute the same successor, and have one
// silently overwrite the other's gzip snapshot. Run with -race.
func TestConcurrentSavesDoNotLoseRevisions(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("Failed to initialize storage: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	if _, err := storage.SaveArticle("", "Race Page", "v1", "", "", "", "seed", nil, ""); err != nil {
		t.Fatalf("seed SaveArticle failed: %v", err)
	}

	const writers = 12
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(n int) {
			defer wg.Done()
			_, _ = storage.SaveArticle("race-page", "Race Page", fmt.Sprintf("body %d", n),
				"", "", "", fmt.Sprintf("edit %d", n), nil, "")
		}(i)
	}
	wg.Wait()

	// Each of the seed + concurrent writes must have produced its own history snapshot, numbered
	// contiguously from 1. A gap or a short count means a revision was overwritten.
	history, err := storage.GetArticleHistory("race-page")
	if err != nil {
		t.Fatalf("GetArticleHistory failed: %v", err)
	}
	want := writers + 1
	if len(history) != want {
		t.Errorf("expected %d distinct revisions, got %d — a concurrent write clobbered a snapshot", want, len(history))
	}

	seen := make(map[int]bool, len(history))
	for _, h := range history {
		if seen[h.Version] {
			t.Errorf("duplicate revision number %d in history", h.Version)
		}
		seen[h.Version] = true
	}
	for v := 1; v <= want; v++ {
		if !seen[v] {
			t.Errorf("revision %d missing from history (versions are not contiguous)", v)
		}
	}
}

// TestVersionCounterComesFromFrontMatter pins where the version counter lives. It used to be
// derived by counting snapshots in data/history/<slug>/, which made a document's version a property
// of a local cache rather than of the document: prune the history directory, restore from a partial
// backup, or import a document without its snapshots, and a long-lived article silently restarted at
// version 1. Optimistic locking went with it — a reset counter compares equal to a stale
// loaded_version, so a conflicting write is accepted as a clean one.
func TestVersionCounterComesFromFrontMatter(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	for i := 0; i < 4; i++ {
		if _, err := storage.SaveArticle(slugOrEmpty(i, "counted"), "Counted", fmt.Sprintf("# v%d", i+1), "", "", "", "", nil, ""); err != nil {
			t.Fatalf("save %d failed: %v", i+1, err)
		}
	}
	art, err := storage.GetArticle("counted")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if art.Version != 4 {
		t.Fatalf("expected version 4 before history loss, got %d", art.Version)
	}

	// Lose the history directory, exactly as a pruned volume or a partial restore would.
	if err := os.RemoveAll(filepath.Join(storage.HistoryDir, "counted")); err != nil {
		t.Fatalf("failed to remove history: %v", err)
	}

	next, err := storage.SaveArticle("counted", "Counted", "# v5", "", "", "", "", nil, "")
	if err != nil {
		t.Fatalf("save after history loss failed: %v", err)
	}
	if next.Version != 5 {
		t.Errorf("expected the counter to continue at 5 after history loss, got %d", next.Version)
	}

	// The superseded state is re-archived so the timeline the numbers promise can be walked back to.
	if _, err := os.Stat(filepath.Join(storage.HistoryDir, "counted", "4.md.gz")); err != nil {
		t.Errorf("expected version 4 to be re-archived after history loss: %v", err)
	}

	// Optimistic locking still sees the conflict it exists to catch.
	if _, err := storage.ApplyArticleEdit("counted", ArticleEdit{
		Title:         "Counted",
		Content:       "# stale",
		LoadedVersion: 4,
	}); err == nil {
		t.Error("a write against the pre-loss version must still be rejected as a conflict")
	}
}

// slugOrEmpty returns "" for the first save (a create) and the slug thereafter (an update).
func slugOrEmpty(i int, slug string) string {
	if i == 0 {
		return ""
	}
	return slug
}

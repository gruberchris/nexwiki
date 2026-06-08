package server

import (
	"os"
	"testing"
)

func TestArchivedTagFunctionality(t *testing.T) {
	tempDir := t.TempDir()

	storage, err := NewStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to initialize storage: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	// 1. Test saving article with archived tag
	article, err := storage.SaveArticle("", "Archived Page", "# Archived Content", "Initial commit", []string{"archived"})
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	if article.Version != 1 {
		t.Errorf("Expected version 1, got %d", article.Version)
	}
	if len(article.Tags) != 1 || article.Tags[0] != "archived" {
		t.Errorf("Expected tags ['archived'], got %v", article.Tags)
	}
	if article.ArchivedAt.IsZero() {
		t.Error("Expected ArchivedAt to be set when article is archived")
	}

	// 2. Test that archived articles are excluded from the default search
	results, err := storage.SearchArticles("test")
	if err != nil {
		t.Fatalf("SearchArticles failed: %v", err)
	}

	for _, res := range results {
		if res.Slug == "archived-page" {
			t.Errorf("Expected archived page to be filtered out from default search")
		}
	}

	// 3. Test that explicit search for 'archived' shows archived articles
	resultsExplicit, err := storage.SearchArticles("archived")
	if err != nil {
		t.Fatalf("SearchArticles explicit failed: %v", err)
	}

	foundArchived := false
	for _, res := range resultsExplicit {
		if res.Slug == "archived-page" {
			foundArchived = true
			break
		}
	}
	if !foundArchived {
		t.Errorf("Expected explicit search to find archived page")
	}

	// 4. Test updating article tags to add the archived tag
	article2, err := storage.SaveArticle("", "Another Page", "# Another Content", "Initial commit", []string{"draft"})
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	article2Updated, err := storage.UpdateArticleTags(article2.Slug, []string{"draft", "archived"}, article2.Version, "Add archived tag")
	if err != nil {
		t.Fatalf("UpdateArticleTags failed: %v", err)
	}

	if len(article2Updated.Tags) != 2 || !contains(article2Updated.Tags, "archived") {
		t.Errorf("Expected 'archived' tag to be added, got %v", article2Updated.Tags)
	}
	if article2Updated.ArchivedAt.IsZero() {
		t.Error("Expected ArchivedAt to be set when article is archived via UpdateArticleTags")
	}

	// 5. Test environment variable configuration for auto-deletion
	// Save current environment and restore after test
	oldEnv := os.Getenv("NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS")
	defer func() {
		if oldEnv == "" {
			_ = os.Unsetenv("NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS")
		} else {
			_ = os.Setenv("NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS", oldEnv)
		}
	}()

	// Test with auto-delete disabled (default)
	_ = os.Setenv("NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS", "0")
	storedWithZero, err := NewStorage(tempDir + "2")
	if err != nil {
		t.Fatalf("Failed to initialize storage with zero delay: %v", err)
	}
	t.Cleanup(func() { _ = storedWithZero.Close() })

	// Test with auto-delete enabled after 1 day
	_ = os.Setenv("NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS", "1")
	storedWithOne, err := NewStorage(tempDir + "3")
	if err != nil {
		t.Fatalf("Failed to initialize storage with one day delay: %v", err)
	}
	t.Cleanup(func() { _ = storedWithOne.Close() })

	// Note: Actual auto-deletion testing would require mocking time or waiting,
	// which is beyond the scope of this unit test. The implementation will be
	// tested in integration tests.
}

// Helper function to check if a string slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

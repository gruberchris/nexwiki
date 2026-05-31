package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStorageVersioning(t *testing.T) {
	tempDir := t.TempDir()

	storage, err := NewStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to initialize storage: %v", err)
	}

	// 1. Test Saving Initial version
	art, err := storage.SaveArticle("", "Test Page", "# Version 1 content", "Initial commit")
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	if art.Version != 1 {
		t.Errorf("Expected version 1, got %d", art.Version)
	}
	if art.EditSummary != "Initial commit" {
		t.Errorf("Expected edit summary 'Initial commit', got '%s'", art.EditSummary)
	}

	// 2. Test saving second version
	art2, err := storage.SaveArticle("test-page", "Test Page", "# Version 2 content", "Typo fix")
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

	// 4. Test reading single version
	v1, err := storage.GetArticleVersion("test-page", 1)
	if err != nil {
		t.Fatalf("GetArticleVersion failed: %v", err)
	}
	if v1.Content != "# Version 1 content" {
		t.Errorf("Expected content '# Version 1 content', got '%s'", v1.Content)
	}

	// 5. Test slug renaming
	art3, err := storage.SaveArticle("test-page", "Renamed Page", "# Renamed content", "Renamed slug")
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

	// 7. Test deleting article clears history folder
	err = storage.DeleteArticle("renamed-page")
	if err != nil {
		t.Fatalf("DeleteArticle failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storage.HistoryDir, "renamed-page")); !os.IsNotExist(err) {
		t.Errorf("Expected history directory for renamed-page to be deleted completely")
	}
}

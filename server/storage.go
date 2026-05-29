package server

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/blevesearch/bleve/v2"
)

// Article represents a wiki article page, containing metadata in a front matter block and content body.
type Article struct {
	Title     string    `json:"title"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Content   string    `json:"content,omitempty"`
}

// Storage manages persistent article files and uploaded assets on disk.
type Storage struct {
	DataDir     string
	ArticleDir  string
	AssetDir    string
	SearchIndex bleve.Index
}

// NewStorage initializes and returns a Storage manager, ensuring required subdirectories exist.
func NewStorage(dataDir string) (*Storage, error) {
	articleDir := filepath.Join(dataDir, "articles")
	assetDir := filepath.Join(dataDir, "assets")
	indexPath := filepath.Join(dataDir, "search.bleve")

	if err := os.MkdirAll(articleDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create article directory: %w", err)
	}
	if err := os.MkdirAll(assetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create asset directory: %w", err)
	}

	// Open or create Bleve index
	var index bleve.Index
	var err error
	if _, err = os.Stat(indexPath); os.IsNotExist(err) {
		mapping := bleve.NewIndexMapping()
		index, err = bleve.New(indexPath, mapping)
		if err != nil {
			return nil, fmt.Errorf("failed to create search index: %w", err)
		}
	} else {
		index, err = bleve.Open(indexPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open search index: %w", err)
		}
	}

	s := &Storage{
		DataDir:     dataDir,
		ArticleDir:  articleDir,
		AssetDir:    assetDir,
		SearchIndex: index,
	}

	// Seed standard 'home' page if no articles exist
	if err := s.seedDefaultHome(); err != nil {
		index.Close()
		return nil, err
	}

	// Sync/populate search index
	if err := s.SyncSearchIndex(); err != nil {
		index.Close()
		return nil, fmt.Errorf("failed to sync search index: %w", err)
	}

	return s, nil
}

// Slugify standardizes title strings into valid URL-safe and file-safe slug formats.
func Slugify(title string) string {
	slug := strings.ToLower(title)
	// Replace non-alphanumeric characters with spaces
	reg := regexp.MustCompile(`[^a-z0-9\s-_]`)
	slug = reg.ReplaceAllString(slug, "")
	// Replace spaces and underscores with hyphens
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = strings.ReplaceAll(slug, "_", "-")
	// Replace multiple hyphens with a single hyphen
	regHyphen := regexp.MustCompile(`-+`)
	slug = regHyphen.ReplaceAllString(slug, "-")
	return strings.Trim(slug, "-")
}

// ListArticles reads all markdown files and returns metadata sorted by updated time (newest first).
func (s *Storage) ListArticles() ([]Article, error) {
	var articles []Article

	err := filepath.WalkDir(s.ArticleDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		art, err := parseArticleFile(data, false)
		if err != nil {
			// Skip malformed files silently or log in a production app
			return nil
		}
		articles = append(articles, *art)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list articles: %w", err)
	}

	// Sort articles: updated_at descending
	for i := 0; i < len(articles); i++ {
		for j := i + 1; j < len(articles); j++ {
			if articles[i].UpdatedAt.Before(articles[j].UpdatedAt) {
				articles[i], articles[j] = articles[j], articles[i]
			}
		}
	}

	return articles, nil
}

// GetArticle reads and parses a single article by slug.
func (s *Storage) GetArticle(slug string) (*Article, error) {
	cleanedSlug := Slugify(slug)
	if cleanedSlug == "" {
		return nil, fmt.Errorf("invalid slug")
	}

	filePath := filepath.Join(s.ArticleDir, cleanedSlug+".md")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("article not found: %s", slug)
		}
		return nil, err
	}

	return parseArticleFile(data, true)
}

// SaveArticle writes article markdown to disk, handling potential slug changes (renaming files and moving asset folders).
func (s *Storage) SaveArticle(oldSlug string, title string, content string) (*Article, error) {
	newSlug := Slugify(title)
	if newSlug == "" {
		return nil, fmt.Errorf("article title must contain valid characters to generate a slug")
	}

	var art *Article
	now := time.Now()

	// If updating an existing article
	if oldSlug != "" {
		oldSlug = Slugify(oldSlug)
		oldPath := filepath.Join(s.ArticleDir, oldSlug+".md")

		// Read existing to preserve CreatedAt timestamp
		existingData, err := os.ReadFile(oldPath)
		if err == nil {
			existingArt, parseErr := parseArticleFile(existingData, false)
			if parseErr == nil {
				art = &Article{
					Title:     title,
					Slug:      newSlug,
					CreatedAt: existingArt.CreatedAt,
					UpdatedAt: now,
					Content:   content,
				}
			}
		}

		// If the slug has changed, rename files and move assets
		if oldSlug != newSlug {
			newPath := filepath.Join(s.ArticleDir, newSlug+".md")
			// Check if target slug already exists
			if _, err := os.Stat(newPath); err == nil {
				return nil, fmt.Errorf("an article with slug '%s' already exists", newSlug)
			}

			// Delete old markdown file later, or rename it first
			if err := os.Rename(oldPath, newPath); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to rename article file: %w", err)
			}

			// Remove old slug from search index
			_ = s.UnindexArticle(oldSlug)

			// Rename corresponding asset directory if it exists
			oldAssetDir := filepath.Join(s.AssetDir, oldSlug)
			newAssetDir := filepath.Join(s.AssetDir, newSlug)
			if _, err := os.Stat(oldAssetDir); err == nil {
				if err := os.Rename(oldAssetDir, newAssetDir); err != nil {
					return nil, fmt.Errorf("failed to move assets: %w", err)
				}
			}
		}
	}

	// Create new article object if not populated above
	if art == nil {
		art = &Article{
			Title:     title,
			Slug:      newSlug,
			CreatedAt: now,
			UpdatedAt: now,
			Content:   content,
		}
	}

	// Serialize and write
	filePath := filepath.Join(s.ArticleDir, newSlug+".md")
	serialized := serializeFrontMatter(art) + art.Content
	if err := os.WriteFile(filePath, []byte(serialized), 0644); err != nil {
		return nil, fmt.Errorf("failed to write article file: %w", err)
	}

	// Add updated/new article to search index
	if err := s.IndexArticle(art); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to index article '%s' in search engine: %v\n", newSlug, err)
	}

	return art, nil
}

// DeleteArticle deletes the article's markdown file and recursively deletes all its assets on disk.
func (s *Storage) DeleteArticle(slug string) error {
	cleanedSlug := Slugify(slug)
	if cleanedSlug == "" {
		return fmt.Errorf("invalid slug")
	}

	// 1. Delete markdown file
	filePath := filepath.Join(s.ArticleDir, cleanedSlug+".md")
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete article file: %w", err)
	}

	// Remove from search index
	_ = s.UnindexArticle(cleanedSlug)

	// 2. Recursively delete asset folder
	assetPath := filepath.Join(s.AssetDir, cleanedSlug)
	if err := os.RemoveAll(assetPath); err != nil {
		return fmt.Errorf("failed to delete asset directory: %w", err)
	}

	return nil
}

// SaveAsset saves an uploaded file into data/assets/{slug}/{filename}.
func (s *Storage) SaveAsset(slug string, filename string, fileData []byte) (string, error) {
	cleanedSlug := Slugify(slug)
	if cleanedSlug == "" {
		return "", fmt.Errorf("invalid slug")
	}

	// Sanitize filename to prevent directory traversal
	safeFilename := filepath.Base(filename)
	safeFilename = strings.ReplaceAll(safeFilename, " ", "-")

	// Ensure the specific folder for this article exists
	articleAssetDir := filepath.Join(s.AssetDir, cleanedSlug)
	if err := os.MkdirAll(articleAssetDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create article asset folder: %w", err)
	}

	filePath := filepath.Join(articleAssetDir, safeFilename)
	if err := os.WriteFile(filePath, fileData, 0644); err != nil {
		return "", fmt.Errorf("failed to write asset file: %w", err)
	}

	// Return URL path for the client
	return fmt.Sprintf("/api/assets/%s/%s", cleanedSlug, safeFilename), nil
}

// GetAssetPath returns the absolute path to an asset, validating to prevent directory traversal.
func (s *Storage) GetAssetPath(slug, filename string) (string, error) {
	cleanedSlug := Slugify(slug)
	safeFilename := filepath.Base(filename)

	if cleanedSlug == "" || safeFilename == "" || safeFilename == "." || safeFilename == ".." {
		return "", fmt.Errorf("invalid path parameters")
	}

	filePath := filepath.Clean(filepath.Join(s.AssetDir, cleanedSlug, safeFilename))

	// Double security check to ensure it doesn't escape our asset directory
	expectedPrefix, err := filepath.Abs(s.AssetDir)
	if err != nil {
		return "", err
	}
	actualAbs, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}

	if !strings.HasPrefix(actualAbs, expectedPrefix) {
		return "", fmt.Errorf("unauthorized file access path traversal attempt")
	}

	return filePath, nil
}

// seedDefaultHome creates a default welcoming home wiki page if the articles folder is completely empty.
func (s *Storage) seedDefaultHome() error {
	files, err := os.ReadDir(s.ArticleDir)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		return nil
	}

	defaultHomeContent := `# Welcome to NexWiki 🚀

Welcome to your brand-new, self-hosted personal wiki application! 

This wiki is built using **Go** for the backend server and **React + TypeScript + Tailwind CSS** for the frontend interface. It is fully containerized with **Docker** and runs out of a single, optimized binary.

### 🌟 Features Ready To Use:
*   **Slug-Based Clean Routing:** Dynamic URLs mapping directly to your markdown files.
*   **Split-Pane Editor:** Enjoy editing raw markdown on the left with instant live visual rendering on the right.
*   **Drag-and-Drop Image Uploader:** Paste or drag images straight into the editor to upload them.
*   **WikiLinks:** Write double-bracket links like [[Guides]] or [[Markdown Playground]] to easily connect pages.
*   **Table of Contents (TOC):** A dynamic, scroll-observed floating outline generated from article headers.
*   **Dark Mode Toggle:** Easy reading day or night with a gorgeous dark aesthetic.
*   **Asset Lifecycle Management:** When you delete a wiki article, all uploaded images embedded in that article are instantly and securely removed from disk.

### 📝 Get Started
*   Click the **Edit** button in the top right to modify this page.
*   Click the **New Page** button in the sidebar or search index to create a new page.
*   Try inserting a link to a new page using the double-bracket syntax: ` + "`" + `[[My Draft Page]]` + "`" + `. Click it to create the article on the fly!
`
	_, err = s.SaveArticle("", "Home", defaultHomeContent)
	return err
}

// parseArticleFile parses front matter block and markdown body using standard Go libraries.
func parseArticleFile(fileContent []byte, loadContent bool) (*Article, error) {
	// Normalize Windows line endings
	str := strings.ReplaceAll(string(fileContent), "\r\n", "\n")

	if !strings.HasPrefix(str, "---\n") {
		return nil, fmt.Errorf("invalid format: missing front matter header marker")
	}

	parts := strings.SplitN(str, "---\n", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid format: malformed front matter delimiters")
	}

	metaSection := parts[1]
	bodySection := strings.TrimSpace(parts[2])

	art := &Article{}
	lines := strings.Split(metaSection, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		partsLine := strings.SplitN(line, ":", 2)
		if len(partsLine) != 2 {
			continue
		}
		key := strings.TrimSpace(partsLine[0])
		val := strings.TrimSpace(partsLine[1])

		// Strip quotes if they were added (e.g. title: "Home Page")
		val = strings.Trim(val, `"'`)

		switch key {
		case "title":
			art.Title = val
		case "slug":
			art.Slug = val
		case "created_at":
			t, err := time.Parse(time.RFC3339, val)
			if err == nil {
				art.CreatedAt = t
			}
		case "updated_at":
			t, err := time.Parse(time.RFC3339, val)
			if err == nil {
				art.UpdatedAt = t
			}
		}
	}

	// Basic check
	if art.Title == "" || art.Slug == "" {
		return nil, fmt.Errorf("invalid format: title and slug are required in front matter")
	}

	if loadContent {
		art.Content = bodySection
	}

	return art, nil
}

// serializeFrontMatter converts article metadata into the front matter block.
func serializeFrontMatter(art *Article) string {
	return fmt.Sprintf("---\ntitle: %s\nslug: %s\ncreated_at: %s\nupdated_at: %s\n---\n",
		art.Title,
		art.Slug,
		art.CreatedAt.Format(time.RFC3339),
		art.UpdatedAt.Format(time.RFC3339),
	)
}

// IndexArticle adds or updates a single article inside the Bleve search index.
func (s *Storage) IndexArticle(art *Article) error {
	return s.SearchIndex.Index(art.Slug, art)
}

// UnindexArticle deletes a single article from the Bleve search index.
func (s *Storage) UnindexArticle(slug string) error {
	return s.SearchIndex.Delete(slug)
}

// SyncSearchIndex populates the Bleve index with all existing markdown articles on startup if the index is brand new.
func (s *Storage) SyncSearchIndex() error {
	count, err := s.SearchIndex.DocCount()
	if err != nil {
		return fmt.Errorf("failed to count search index documents: %w", err)
	}

	// If the index is empty, read all articles from disk and index them
	if count == 0 {
		fmt.Fprintf(os.Stderr, "Search index is empty. Commencing full boot synchronization...\n")
		articles, err := s.ListArticles()
		if err != nil {
			return fmt.Errorf("failed to list articles for indexing: %w", err)
		}

		for _, item := range articles {
			art, err := s.GetArticle(item.Slug)
			if err == nil {
				if err := s.IndexArticle(art); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to index article '%s' on boot sync: %v\n", item.Slug, err)
				}
			}
		}
		newCount, _ := s.SearchIndex.DocCount()
		fmt.Fprintf(os.Stderr, "Boot synchronization complete. Successfully indexed %d articles.\n", newCount)
	}

	return nil
}

// SearchResult represents a single full-text query match.
type SearchResult struct {
	Title     string    `json:"title"`
	Slug      string    `json:"slug"`
	Score     float64   `json:"score"`
	UpdatedAt time.Time `json:"updated_at"`
	Snippets  []string  `json:"snippets"`
}

// SearchArticles searches for keywords inside article titles and contents, returning HTML highlighted snippets.
func (s *Storage) SearchArticles(queryStr string) ([]SearchResult, error) {
	if queryStr == "" {
		return []SearchResult{}, nil
	}

	// Create a query matching terms (supports boolean logic, wildcards, fuzzy matching natively!)
	q := bleve.NewQueryStringQuery(queryStr)
	searchRequest := bleve.NewSearchRequest(q)

	// Configure Bleve highlighter style to wrap matched words in HTML <mark> tags
	searchRequest.Highlight = bleve.NewHighlightWithStyle("html")
	searchRequest.Highlight.AddField("content")
	searchRequest.Highlight.AddField("title")

	// Limit to top 40 matches
	searchRequest.Size = 40

	searchResults, err := s.SearchIndex.Search(searchRequest)
	if err != nil {
		return nil, fmt.Errorf("bleve search failed: %w", err)
	}

	results := []SearchResult{}
	for _, hit := range searchResults.Hits {
		art, err := s.GetArticle(hit.ID)
		if err != nil {
			// Skip if the physical markdown file was deleted on disk but search index is slightly out of sync
			continue
		}

		var snippets []string
		if frags, ok := hit.Fragments["content"]; ok {
			snippets = frags
		} else if frags, ok := hit.Fragments["title"]; ok {
			snippets = frags
		}

		// Fallback snippet if Bleve returns empty fragments (extract first 150 characters)
		if len(snippets) == 0 {
			runes := []rune(art.Content)
			limit := 150
			if len(runes) < limit {
				limit = len(runes)
			}
			snippets = []string{string(runes[:limit]) + "..."}
		}

		results = append(results, SearchResult{
			Title:     art.Title,
			Slug:      art.Slug,
			Score:     hit.Score,
			UpdatedAt: art.UpdatedAt,
			Snippets:  snippets,
		})
	}

	return results, nil
}

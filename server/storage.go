package server

import (
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/blevesearch/bleve/v2"
)

// Article represents a wiki article page, containing metadata in a front matter block and content body.
type Article struct {
	Title       string    `json:"title"`
	Slug        string    `json:"slug"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Content     string    `json:"content,omitempty"`
	Version     int       `json:"version,omitempty"`      // Version number
	EditSummary string    `json:"edit_summary,omitempty"` // Summary of edits
	Tags        []string  `json:"tags,omitempty"`         // Tags list
}

// Storage manages persistent article files and uploaded assets on disk.
type Storage struct {
	DataDir     string
	ArticleDir  string
	AssetDir    string
	HistoryDir  string
	SearchIndex bleve.Index
	ThemeStore  *ThemeStore
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
	historyDir := filepath.Join(dataDir, "history")
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create history directory: %w", err)
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
		HistoryDir:  historyDir,
		SearchIndex: index,
		ThemeStore:  NewThemeStore(dataDir),
	}

	// Seed standard 'home' page if no articles exist
	if err := s.seedDefaultHome(); err != nil {
		_ = index.Close()
		return nil, err
	}

	// Sync/populate search index
	if err := s.SyncSearchIndex(); err != nil {
		_ = index.Close()
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

// ListArticles reads all Markdown files and returns metadata sorted by updated time (newest first).
// The "home" article is excluded from listings (reserved for the Hero dashboard).
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

		// Exclude "home" from listings (reserved for Hero dashboard)
		if art.Slug == "home" {
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

// SaveArticle writes article Markdown to disk, handling potential slug changes and compressing a copy in gzip version history.
func (s *Storage) SaveArticle(oldSlug string, title string, content string, editSummary string, tags []string) (*Article, error) {
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
					Tags:      tags,
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

			// Rename physical Markdown file
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

			// Rename corresponding history directory if it exists
			oldHistDir := filepath.Join(s.HistoryDir, oldSlug)
			newHistDir := filepath.Join(s.HistoryDir, newSlug)
			if _, err := os.Stat(oldHistDir); err == nil {
				if err := os.Rename(oldHistDir, newHistDir); err != nil {
					return nil, fmt.Errorf("failed to move history: %w", err)
				}
			}
		}
	}

	// Determine the next version number by scanning current history files in newSlug folder
	histFolder := filepath.Join(s.HistoryDir, newSlug)
	_ = os.MkdirAll(histFolder, 0755)

	nextVersion := 1
	files, err := os.ReadDir(histFolder)
	if err == nil {
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".md.gz") {
				name := strings.TrimSuffix(f.Name(), ".md.gz")
				if v, err := strconv.Atoi(name); err == nil {
					if v >= nextVersion {
						nextVersion = v + 1
					}
				}
			}
		}
	}

	// Clean up newlines from edit summary to keep YAML parsing clean
	editSummary = strings.ReplaceAll(editSummary, "\n", " ")
	editSummary = strings.ReplaceAll(editSummary, "\r", "")
	editSummary = strings.TrimSpace(editSummary)

	if editSummary == "" {
		if nextVersion == 1 {
			editSummary = "Initial version"
		} else {
			editSummary = "Updated article"
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
			Tags:      tags,
		}
	} else {
		art.Tags = tags
	}

	// Set version and edit summary
	art.Version = nextVersion
	art.EditSummary = editSummary

	// Serialize front matter and content
	serialized := serializeFrontMatter(art) + art.Content

	// Write uncompressed active version to data/articles/
	filePath := filepath.Join(s.ArticleDir, newSlug+".md")
	if err := os.WriteFile(filePath, []byte(serialized), 0644); err != nil {
		return nil, fmt.Errorf("failed to write active article file: %w", err)
	}

	// Write compressed history version to data/history/
	histFilePath := filepath.Join(histFolder, fmt.Sprintf("%d.md.gz", nextVersion))
	if err := writeGzippedFile(histFilePath, []byte(serialized)); err != nil {
		return nil, fmt.Errorf("failed to write compressed version history file: %w", err)
	}

	// Add updated/new article to search index
	if err := s.IndexArticle(art); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to index article '%s' in search engine: %v\n", newSlug, err)
	}

	return art, nil
}

// DeleteArticle deletes the article's Markdown file, all its assets, and all version history on disk.
func (s *Storage) DeleteArticle(slug string) error {
	cleanedSlug := Slugify(slug)
	if cleanedSlug == "" {
		return fmt.Errorf("invalid slug")
	}

	// 1. Delete the Markdown file
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

	// 3. Recursively delete history folder
	historyPath := filepath.Join(s.HistoryDir, cleanedSlug)
	if err := os.RemoveAll(historyPath); err != nil {
		return fmt.Errorf("failed to delete history directory: %w", err)
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
	_, err = s.SaveArticle("", "Home", defaultHomeContent, "Initial version", nil)
	return err
}

// parseArticleFile parses front matter block and Markdown body using standard Go libraries.
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

		// Strip quotes if they were added (e.g., title: "Home Page")
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
		case "version":
			if v, err := strconv.Atoi(val); err == nil {
				art.Version = v
			}
		case "edit_summary":
			art.EditSummary = val
		case "tags":
			var tags []string
			for _, t := range strings.Split(val, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					tags = append(tags, t)
				}
			}
			art.Tags = tags
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
	var tagsStr string
	if len(art.Tags) > 0 {
		tagsStr = fmt.Sprintf("\ntags: %s", strings.Join(art.Tags, ", "))
	}
	return fmt.Sprintf("---\ntitle: %s\nslug: %s\ncreated_at: %s\nupdated_at: %s\nversion: %d\nedit_summary: %s%s\n---\n",
		art.Title,
		art.Slug,
		art.CreatedAt.Format(time.RFC3339),
		art.UpdatedAt.Format(time.RFC3339),
		art.Version,
		art.EditSummary,
		tagsStr,
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

// SyncSearchIndex populates the Bleve index with all existing Markdown articles on startup if the index is brand new.
func (s *Storage) SyncSearchIndex() error {
	count, err := s.SearchIndex.DocCount()
	if err != nil {
		return fmt.Errorf("failed to count search index documents: %w", err)
	}

	// If the index is empty, read all articles from disk and index them
	if count == 0 {
		_, _ = fmt.Fprintf(os.Stderr, "Search index is empty. Commencing full boot synchronization...\n")
		articles, err := s.ListArticles()
		if err != nil {
			return fmt.Errorf("failed to list articles for indexing: %w", err)
		}

		for _, item := range articles {
			art, err := s.GetArticle(item.Slug)
			if err == nil {
				if err := s.IndexArticle(art); err != nil {
					_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to index article '%s' on boot sync: %v\n", item.Slug, err)
				}
			}
		}
		newCount, _ := s.SearchIndex.DocCount()
		_, _ = fmt.Fprintf(os.Stderr, "Boot synchronization complete. Successfully indexed %d articles.\n", newCount)
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
	Tags      []string  `json:"tags,omitempty"`
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

	// Check if the query explicitly mentions "aiagent-" (case-insensitive)
	allowAgentMemories := strings.Contains(strings.ToLower(queryStr), "aiagent-")

	var results []SearchResult
	for _, hit := range searchResults.Hits {
		art, err := s.GetArticle(hit.ID)
		if err != nil {
			// Skip if the physical Markdown file was deleted on disk but search index is slightly out of sync
			continue
		}

		// Exclude "home" from search results (reserved for Hero dashboard)
		if art.Slug == "home" {
			continue
		}

		// Filter out articles with agent tags (starting with "aiagent-") unless explicitly requested
		if !allowAgentMemories {
			hasAgentTag := false
			isSkill := false
			isPlan := false
			isMemory := false
			var agentTags []string

			for _, tag := range art.Tags {
				tagLower := strings.ToLower(tag)
				if strings.HasPrefix(tagLower, "aiagent-") {
					hasAgentTag = true
					agentTags = append(agentTags, tagLower)
					if tagLower == "aiagent-skill" {
						isSkill = true
					} else if tagLower == "aiagent-plan" {
						isPlan = true
					} else if strings.HasPrefix(tagLower, "aiagent-memory") {
						isMemory = true
					}
				}
			}

			if hasAgentTag {
				bypass := false
				queryLower := strings.ToLower(queryStr)

				// 1. Explicitly searching for them by slug/title name (exact match)
				if strings.EqualFold(art.Slug, queryStr) || strings.EqualFold(art.Title, queryStr) {
					bypass = true
				} else {
					// 2. Or searching by explicit aiagent- tag names
					for _, aTag := range agentTags {
						if strings.Contains(queryLower, aTag) {
							bypass = true
							break
						}
					}
					// 3. Or if the query includes "aiagent-skill" for skills, "aiagent-plan" for plans, or "aiagent-memory" for memories
					if !bypass {
						if isSkill && (strings.Contains(queryLower, "aiagent-skill") || strings.Contains(queryLower, "skill")) {
							bypass = true
						} else if isPlan && (strings.Contains(queryLower, "aiagent-plan") || strings.Contains(queryLower, "plan")) {
							bypass = true
						} else if isMemory && strings.Contains(queryLower, "aiagent-memory") {
							bypass = true
						}
					}
				}

				if !bypass {
					continue
				}
			}
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
			Tags:      art.Tags,
		})
	}

	return results, nil
}

// Helpers for reading/writing Gzip files

func writeGzippedFile(filePath string, data []byte) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	gw := gzip.NewWriter(file)
	defer func() { _ = gw.Close() }()

	_, err = gw.Write(data)
	return err
}

func readGzippedFile(filePath string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	gr, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gr.Close() }()

	return io.ReadAll(gr)
}

// GetArticleHistory returns metadata for all historical versions of an article (newest first).
func (s *Storage) GetArticleHistory(slug string) ([]Article, error) {
	cleanedSlug := Slugify(slug)
	if cleanedSlug == "" {
		return nil, fmt.Errorf("invalid slug")
	}

	histFolder := filepath.Join(s.HistoryDir, cleanedSlug)
	files, err := os.ReadDir(histFolder)
	if err != nil {
		if os.IsNotExist(err) {
			return []Article{}, nil
		}
		return nil, fmt.Errorf("failed to read history directory: %w", err)
	}

	var history []Article
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".md.gz") {
			continue
		}

		filePath := filepath.Join(histFolder, file.Name())
		data, err := readGzippedFile(filePath)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to read history version file %s: %v\n", file.Name(), err)
			continue
		}

		art, err := parseArticleFile(data, false)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to parse history version file %s: %v\n", file.Name(), err)
			continue
		}

		history = append(history, *art)
	}

	// Sort history by version descending
	sort.Slice(history, func(i, j int) bool {
		return history[i].Version > history[j].Version
	})

	return history, nil
}

// GetArticleVersion reads a single historical version of an article.
func (s *Storage) GetArticleVersion(slug string, version int) (*Article, error) {
	cleanedSlug := Slugify(slug)
	if cleanedSlug == "" {
		return nil, fmt.Errorf("invalid slug")
	}

	filePath := filepath.Join(s.HistoryDir, cleanedSlug, fmt.Sprintf("%d.md.gz", version))
	data, err := readGzippedFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("version %d not found for article %s: %w", version, slug, err)
	}

	return parseArticleFile(data, true)
}

// RevertArticle rolls the current active document back to the content of a historical version.
func (s *Storage) RevertArticle(slug string, version int) (*Article, error) {
	histArt, err := s.GetArticleVersion(slug, version)
	if err != nil {
		return nil, err
	}

	summary := fmt.Sprintf("Reverted to version %d", version)
	return s.SaveArticle(slug, histArt.Title, histArt.Content, summary, histArt.Tags)
}

// DeleteTagGlobally removes a tag from all articles in the wiki.
// Enforces validation: it returns an error if the tag is protected (starts with "aiagent-").
func (s *Storage) DeleteTagGlobally(tag string) error {
	tagLower := strings.ToLower(tag)
	if strings.HasPrefix(tagLower, "aiagent-") {
		return fmt.Errorf("cannot delete protected AI agent tag: %s", tag)
	}

	articles, err := s.ListArticles()
	if err != nil {
		return err
	}

	for _, artMeta := range articles {
		art, err := s.GetArticle(artMeta.Slug)
		if err != nil {
			continue
		}

		// Check if tag is present
		tagIndex := -1
		for i, t := range art.Tags {
			if strings.ToLower(t) == tagLower {
				tagIndex = i
				break
			}
		}

		if tagIndex != -1 {
			// Remove the tag
			newTags := append(art.Tags[:tagIndex], art.Tags[tagIndex+1:]...)
			// Save the updated article
			_, err = s.SaveArticle(art.Slug, art.Title, art.Content, fmt.Sprintf("Removed tag '%s' globally", tag), newTags)
			if err != nil {
				return fmt.Errorf("failed to update article %s during global tag deletion: %w", art.Slug, err)
			}
		}
	}

	return nil
}

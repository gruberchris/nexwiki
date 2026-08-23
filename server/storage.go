package server

import (
	"compress/gzip"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	"gopkg.in/yaml.v3"
)

// Article represents a wiki article page. On disk, it is a conformant OKF v0.1 concept
// document: a real YAML front-matter block plus a Markdown content body. The OKF canonical
// keys are type/title/description/resource/tags/timestamp; NexWiki's proprietary metadata
// (slug, created_at, version, edit_summary, source, archived_at) rides as OKF custom keys.
type Article struct {
	Type        string    `json:"type"` // OKF doc-class: Wiki, AI-Agent-Memory, AI-Agent-Plan, or AI-Agent-Skill
	Title       string    `json:"title"`
	Slug        string    `json:"slug"`
	CreatedAt   time.Time `json:"created_at"`
	Timestamp   time.Time `json:"timestamp"` // Canonical modified-time (OKF), replaces updated_at
	Content     string    `json:"content,omitempty"`
	Description string    `json:"description,omitempty"` // OKF one-line summary shown in indexes
	Resource    string    `json:"resource,omitempty"`    // OKF canonical URI of what the concept *is*
	Source      string    `json:"source,omitempty"`      // Provenance: where the knowledge *came from* (OKF citation)
	// Version is always reported, including 0. It used to carry `omitempty`, which dropped the
	// field entirely for articles written to disk before versioning existed — so read_article's
	// structured output had no `version` at all, and the documented loop of feeding it straight
	// back to edit_wiki_article as `loaded_version` dead-ended on exactly those articles.
	Version     int      `json:"version"`
	EditSummary string   `json:"edit_summary,omitempty"` // Summary of edits
	Tags        []string `json:"tags,omitempty"`         // Tags list (system and free user tags)
	// omitzero, not omitempty: encoding/json's omitempty does NOT drop a zero-valued struct, so
	// `omitempty` emitted "0001-01-01T00:00:00Z" on every unarchived document — a string every
	// JSON consumer reads as truthy. That hid every document from the dashboard and sidebar.
	ArchivedAt time.Time `json:"archived_at,omitzero"` // When the article was archived
	// Status is the document's lifecycle state — a single value, deliberately not a tag. Plans
	// and skills validate it against a closed vocabulary (see tags.go); wiki articles and
	// memories may use any value or none.
	Status string `json:"status,omitempty"`
	// StatusChangedAt records when a plan last changed lifecycle status. It exists because the
	// article Timestamp cannot drive the lifecycle timers — fixing a typo in a completed plan
	// would restart its archive clock. Only ever set on AI-Agent-Plan documents; the lifecycle
	// worker treats a missing value as "not yet eligible", never as "infinitely old".
	StatusChangedAt time.Time `json:"status_changed_at,omitzero"`

	// ContentPreview holds the first content line during metadata-only parses
	// (used as a description fallback in indexes); never serialized.
	ContentPreview string `json:"-"`

	// DeclaredType is the raw `type` exactly as it appeared in the front matter, before
	// normalization coerced it. Type above is always one of the four canonical values, so it
	// cannot tell an explicit "Wiki" from a missing or unrecognized type — which is precisely the
	// distinction the OKF import report has to make when it flags a coerced document. Never
	// serialized; empty for documents built in memory rather than parsed.
	DeclaredType string `json:"-"`
}

// Storage manages persistent article files and uploaded assets on disk.
type Storage struct {
	DataDir     string
	ArticleDir  string
	AssetDir    string
	HistoryDir  string
	SearchIndex bleve.Index
	ThemeStore  *ThemeStore
	closeOnce   sync.Once

	// cache memoizes parsed article metadata and link targets, validated by file mtime+size so
	// edits made outside NexWiki are still picked up. See article_cache.go.
	cache *articleCache

	// writeMu serializes every mutation of the article tree. Writers arrive concurrently from
	// the HTTP API, the in-process MCP goroutine, and the Streamable HTTP transport; without
	// this, two writers can both scan the history directory, compute the same next version
	// number, and have one silently overwrite the other's revision snapshot.
	//
	// Methods suffixed "Locked" assume the caller already holds it (Go mutexes are not
	// reentrant, and the write paths call into one another — e.g. SaveArticle → link healing
	// → SaveArticle). Read paths intentionally do not take it: a reader racing a writer sees
	// either the old or the new file, which is indistinguishable from reading a moment sooner.
	//
	// This guards a single process. A `-mcp-only` sidecar writing the same data directory is
	// still unsynchronized; that needs an on-disk lock file.
	writeMu sync.Mutex
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
		cache:       newArticleCache(),
	}

	// Seed standard 'home' page if no articles exist
	if err := s.seedDefaultHome(); err != nil {
		_ = index.Close()
		return nil, err
	}

	// One-time sweep lifting lifecycle status out of tags into the status field (no-op once its
	// marker exists).
	if err := s.MigrateStatusToField(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: status field migration failed: %v\n", err)
	}

	// Cleanup archived articles that have exceeded their retention period
	if err := s.CleanupArchivedArticles(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to cleanup archived articles: %v\n", err)
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

	seen := make(map[string]bool)
	err := filepath.WalkDir(s.ArticleDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		seen[path] = true

		// Served from cache when the file is unchanged; a stat beats an open + YAML parse.
		_, art, err := s.cachedMeta(path, info)
		if err != nil {
			// Skip malformed or unreadable files rather than failing the whole listing.
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

	// Drop cache entries for files that have since been deleted or renamed.
	s.cache.prune(seen)

	// Sort articles: updated_at descending
	sort.Slice(articles, func(i, j int) bool {
		return articles[i].Timestamp.After(articles[j].Timestamp)
	})

	return articles, nil
}

// metaBySlug returns cached metadata for one slug, reading the file only when it has changed.
// Callers that need the Markdown body must use GetArticle.
func (s *Storage) metaBySlug(slug string) (*Article, error) {
	cleanedSlug := Slugify(slug)
	if cleanedSlug == "" {
		return nil, fmt.Errorf("invalid slug")
	}

	filePath := filepath.Join(s.ArticleDir, cleanedSlug+".md")
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("article not found: %s", slug)
		}
		return nil, err
	}

	_, meta, err := s.cachedMeta(filePath, info)
	return meta, err
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
// Description, source, and resource are written as given (callers preserve existing values by passing them through).
// articleType sets the OKF document class; pass "" to preserve an existing article's type (or default a new one to Wiki).
func (s *Storage) SaveArticle(oldSlug string, title string, content string, description string, source string, resource string, editSummary string, tags []string, articleType string) (*Article, error) {
	return s.SaveArticleWithStatus(oldSlug, title, content, description, source, resource, editSummary, tags, articleType, nil)
}

// SaveArticleWithStatus is SaveArticle plus an explicit lifecycle status.
//
// Status is deliberately *not* a parameter of SaveArticle. A status change is a state transition,
// not a content edit, and the two travel separately: a nil status here means "leave it alone",
// so every ordinary save — a body edit, a rename, a tag change, a link heal — preserves the
// document's state the same way it preserves created_at. Only callers that genuinely mean to move
// a document through its lifecycle pass a value.
func (s *Storage) SaveArticleWithStatus(oldSlug string, title string, content string, description string, source string, resource string, editSummary string, tags []string, articleType string, status *string) (*Article, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.saveArticleLocked(oldSlug, title, content, description, source, resource, editSummary, tags, articleType, status)
}

// SetStatus moves a document to a new lifecycle status, leaving its content, title, and tags
// untouched. This is the one write path that changes state.
func (s *Storage) SetStatus(slug string, status string, loadedVersion int, editSummary string) (*Article, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	art, err := s.GetArticle(slug)
	if err != nil {
		return nil, err
	}
	if loadedVersion > 0 && art.Version > 0 && art.Version != loadedVersion {
		return nil, fmt.Errorf("%w: loaded version %d, current version %d", ErrVersionConflict, loadedVersion, art.Version)
	}
	if editSummary == "" {
		editSummary = fmt.Sprintf("Status changed to '%s'", NormalizeStatus(status))
	}
	return s.saveArticleLocked(slug, art.Title, art.Content, art.Description, art.Source, art.Resource, editSummary, art.Tags, art.Type, &status)
}

// saveArticleLocked is SaveArticle's body. The caller must hold writeMu.
func (s *Storage) saveArticleLocked(oldSlug string, title string, content string, description string, source string, resource string, editSummary string, tags []string, articleType string, statusOverride *string) (*Article, error) {
	newSlug := Slugify(title)
	if newSlug == "" {
		return nil, fmt.Errorf("article title must contain valid characters to generate a slug")
	}

	var art *Article
	now := time.Now()
	resolvedType := normalizeType(articleType) // empty/unknown → Wiki
	renamedFromSlug := ""                      // set when a slug rename occurs, to heal inbound WikiLinks
	prevStatus := ""                           // the status before this save, for change stamping

	// If updating an existing article
	if oldSlug != "" {
		oldSlug = Slugify(oldSlug)
		oldPath := filepath.Join(s.ArticleDir, oldSlug+".md")

		existingData, err := os.ReadFile(oldPath)
		if err == nil {
			existingArt, parseErr := parseArticleFile(existingData, false)
			if parseErr == nil {
				// Preserve the existing document class unless the caller explicitly supplied one.
				if articleType == "" {
					resolvedType = existingArt.Type
				}
				prevStatus = existingArt.Status
				art = &Article{
					Type:            resolvedType,
					Title:           title,
					Slug:            newSlug,
					CreatedAt:       existingArt.CreatedAt,
					ArchivedAt:      existingArt.ArchivedAt,
					Status:          existingArt.Status,
					StatusChangedAt: existingArt.StatusChangedAt,
					Timestamp:       now,
					Content:         content,
					Tags:            tags,
				}
			}
		}
	}

	// Resolve the status this save lands on: an explicit override wins, otherwise the document
	// keeps the state it already had, and a brand-new plan enters the lifecycle at draft.
	resolvedStatus := prevStatus
	if statusOverride != nil {
		resolvedStatus = NormalizeStatus(*statusOverride)
	}
	// A plan always ends up with a status: a new one enters at draft, and one written before the
	// field existed is defaulted here rather than rejected — otherwise a legacy plan would be
	// un-editable until a migration had swept it, which makes correctness depend on boot order.
	if resolvedType == ContentTypePlan && resolvedStatus == "" {
		resolvedStatus = DefaultPlanStatus
	}

	// The status contract holds at every write path — REST, MCP, revert, import — and this is the
	// one choke point they all pass through. Validated before the rename below so a rejected save
	// cannot leave a half-moved article behind. Only plans and skills have a contract; wiki
	// articles and memories may use any status and any tags.
	if err := ValidateStatus(resolvedType, resolvedStatus); err != nil {
		return nil, err
	}
	if err := ValidateStatusFreeTags(resolvedType, tags); err != nil {
		return nil, err
	}

	if oldSlug != "" {
		oldPath := filepath.Join(s.ArticleDir, oldSlug+".md")

		// If the slug has changed, rename files and move assets
		if oldSlug != newSlug {
			renamedFromSlug = oldSlug
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

	// If history is empty but the article already exists on disk, archive the current state as version 1 first
	if oldSlug != "" && nextVersion == 1 {
		activePath := filepath.Join(s.ArticleDir, newSlug+".md")
		if existingData, err := os.ReadFile(activePath); err == nil {
			histFilePath := filepath.Join(histFolder, "1.md.gz")
			if err := writeGzippedFile(histFilePath, existingData); err == nil {
				nextVersion = 2
			}
		}
	}

	// YAML front matter handles escaping/multi-line values natively, so no value flattening is needed.
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
			Type:      resolvedType,
			Title:     title,
			Slug:      newSlug,
			CreatedAt: now,
			Timestamp: now,
			Content:   content,
			Tags:      tags,
		}
	} else {
		art.Tags = tags
	}
	art.Description = description
	art.Source = source
	art.Resource = resource

	art.Status = resolvedStatus

	switch art.Type {
	case ContentTypePlan, ContentTypeSkill:
		if resolvedStatus != prevStatus {
			// Log-and-allow: an unusual transition is worth a trace, but a human correcting a
			// mis-set plan in the editor must always win over the state machine.
			if art.Type == ContentTypePlan && prevStatus != "" && !isLegalPlanTransition(prevStatus, resolvedStatus) {
				_, _ = fmt.Fprintf(os.Stderr, "Note: plan '%s' made an unusual status transition %s → %s (allowed)\n",
					art.Slug, prevStatus, resolvedStatus)
			}
			art.StatusChangedAt = now
		}
		// A plan predating the lifecycle (or written by an external tool) gets its clock started
		// now rather than being treated as infinitely old. Skills have no timers and no clock.
		if art.Type == ContentTypePlan && art.StatusChangedAt.IsZero() {
			art.StatusChangedAt = now
		}
		// archived_at exactly mirrors the archived status here: set on entry so the deletion
		// clock starts, cleared on revival so IsArchived cannot keep hiding a document that is
		// nominally back in flight (the one-way-door asymmetry the lifecycle design flagged).
		if resolvedStatus == StatusArchived {
			if art.ArchivedAt.IsZero() {
				art.ArchivedAt = now
			}
		} else if !art.ArchivedAt.IsZero() {
			art.ArchivedAt = time.Time{}
		}
	default:
		// Wiki articles and memories keep the long-standing tag semantics: archiving is manual,
		// the stamp is written once, and removing the tag deliberately does not clear it.
		if art.ArchivedAt.IsZero() {
			for _, tag := range art.Tags {
				if strings.EqualFold(tag, StatusArchived) {
					art.ArchivedAt = now
					break
				}
			}
		}
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

	// Best-effort link healing: when a slug changed, rewrite inbound WikiLinks so they keep
	// resolving. Failures here are logged and never block the rename that already succeeded.
	if renamedFromSlug != "" {
		s.healRenamedLinks(renamedFromSlug, newSlug, art.Title)
	}

	return art, nil
}

// healRenamedLinks (caller must hold writeMu) rewrites every article that links to oldSlug so it
// points at the renamed article. Both internal link forms are healed: a [[WikiLink]] is retargeted
// to the new title, and an absolute [text](/articles/<slug>) destination is retargeted to the new
// slug with its link text untouched. Best-effort: any per-article failure is logged and skipped.
func (s *Storage) healRenamedLinks(oldSlug, newSlug, newTitle string) {
	backlinks, err := s.GetBacklinks(oldSlug)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: link-heal scan failed after renaming '%s'→'%s': %v\n", oldSlug, newSlug, err)
		return
	}
	for _, bl := range backlinks {
		linker, err := s.GetArticle(bl.Slug)
		if err != nil {
			continue
		}
		rewritten, wikiChanged := RewriteWikiLinks(linker.Content, oldSlug, newTitle)
		rewritten, pathChanged := RewriteArticlePathLinks(rewritten, oldSlug, newSlug)
		if !wikiChanged && !pathChanged {
			continue
		}
		summary := fmt.Sprintf("Auto-healed internal link: '%s' renamed to '%s'", oldSlug, newSlug)
		if _, err := s.saveArticleLocked(linker.Slug, linker.Title, rewritten, linker.Description, linker.Source, linker.Resource, summary, linker.Tags, linker.Type, nil); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to heal links in '%s' after rename: %v\n", linker.Slug, err)
		}
	}
}

// DeleteArticle deletes the article's Markdown file, all its assets, and all version history on disk.
func (s *Storage) DeleteArticle(slug string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.deleteArticleLocked(slug)
}

// deleteArticleLocked is DeleteArticle's body. The caller must hold writeMu.
func (s *Storage) deleteArticleLocked(slug string) error {
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
	// Shares writeMu with SaveArticle, which renames asset directories on slug changes.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

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

	// Confirm the resolved path really is inside the asset directory. A string-prefix comparison
	// is not sufficient here: "/data/assets-evil/x" has "/data/assets" as a prefix but escapes the
	// directory. filepath.Rel answers containment properly — anything outside yields a path
	// starting with "..".
	if err := ensureWithin(s.AssetDir, filePath); err != nil {
		return "", err
	}

	return filePath, nil
}

// ensureWithin reports an error unless target resolves to a path inside dir. Both are made
// absolute first so a relative DataDir cannot produce a false negative.
func ensureWithin(dir, target string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}

	rel, err := filepath.Rel(absDir, absTarget)
	if err != nil {
		return fmt.Errorf("unauthorized file access path traversal attempt")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("unauthorized file access path traversal attempt")
	}

	return nil
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
	_, err = s.SaveArticle("", "Home", defaultHomeContent, "", "", "", "Initial version", nil, ContentTypeWiki)
	return err
}

// articleFrontMatter is the on-disk OKF YAML front-matter schema. The field order here
// determines the emitted key order: OKF canonical keys first, then NexWiki custom keys.
// Times are stored as RFC3339 strings for stable, controlled formatting.
type articleFrontMatter struct {
	Type        string   `yaml:"type"`
	Title       string   `yaml:"title"`
	Slug        string   `yaml:"slug"`
	Description string   `yaml:"description,omitempty"`
	Resource    string   `yaml:"resource,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
	Status      string   `yaml:"status,omitempty"`
	Timestamp   string   `yaml:"timestamp,omitempty"`
	CreatedAt   string   `yaml:"created_at,omitempty"`
	Version     int      `yaml:"version,omitempty"`
	EditSummary string   `yaml:"edit_summary,omitempty"`
	Source      string   `yaml:"source,omitempty"`
	ArchivedAt  string   `yaml:"archived_at,omitempty"`
	// StatusChangedAt is a NexWiki custom key carried only by AI-Agent-Plan documents; it feeds
	// the plan lifecycle timers (see server/plan_lifecycle.go).
	StatusChangedAt string `yaml:"status_changed_at,omitempty"`
}

// parseArticleFile parses the OKF YAML front-matter block and Markdown body.
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

	var fm articleFrontMatter
	if err := yaml.Unmarshal([]byte(metaSection), &fm); err != nil {
		return nil, fmt.Errorf("invalid format: front matter is not valid YAML: %w", err)
	}

	art := &Article{
		Type:         normalizeType(fm.Type),
		DeclaredType: strings.TrimSpace(fm.Type),
		Title:        fm.Title,
		Slug:         fm.Slug,
		Description:  fm.Description,
		Resource:     fm.Resource,
		Source:       fm.Source,
		Version:      fm.Version,
		EditSummary:  fm.EditSummary,
		Status:       NormalizeStatus(fm.Status),
	}
	// Clean and copy the tag list (drop blanks).
	for _, t := range fm.Tags {
		t = strings.TrimSpace(t)
		if t != "" {
			art.Tags = append(art.Tags, t)
		}
	}
	if t, err := time.Parse(time.RFC3339, fm.CreatedAt); err == nil {
		art.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, fm.Timestamp); err == nil {
		art.Timestamp = t
	}
	if fm.ArchivedAt != "" {
		if t, err := time.Parse(time.RFC3339, fm.ArchivedAt); err == nil {
			art.ArchivedAt = t
		}
	}
	if fm.StatusChangedAt != "" {
		if t, err := time.Parse(time.RFC3339, fm.StatusChangedAt); err == nil {
			art.StatusChangedAt = t
		}
	}

	// `slug` is a NexWiki *custom* front-matter key, not an OKF canonical one, so a conformant
	// bundle produced by any other tool will not carry it. Requiring it here meant
	// import_okf_bundle rejected every document in a third-party bundle — the interoperability
	// feature only worked against NexWiki's own exports.
	//
	// Deriving it is exact rather than a guess: saveArticleLocked writes every article as
	// Slugify(title).md and stores that same value in the front matter, so on disk the two can
	// never disagree. A document that omits it therefore gets precisely the slug it would have
	// been written with.
	if art.Slug == "" {
		art.Slug = Slugify(art.Title)
	}

	// Basic check. `title` stays required — it is an OKF canonical key, and without it there is
	// nothing to derive a slug from either.
	if art.Title == "" || art.Slug == "" {
		return nil, fmt.Errorf("invalid format: front matter must carry a title (and a slug, or a title that yields one)")
	}

	if loadContent {
		art.Content = bodySection
	} else {
		art.ContentPreview = extractContentPreview(bodySection)
	}

	return art, nil
}

// extractContentPreview returns the first meaningful content line of a Markdown body,
// stripped of heading markers and WikiLink brackets, truncated to 120 runes.
func extractContentPreview(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line == "" || line == "---" {
			continue
		}
		line = strings.ReplaceAll(line, "[[", "")
		line = strings.ReplaceAll(line, "]]", "")
		runes := []rune(line)
		if len(runes) > 120 {
			return string(runes[:120]) + "..."
		}
		return line
	}
	return ""
}

// serializeFrontMatter converts article metadata into a conformant OKF YAML front-matter block.
func serializeFrontMatter(art *Article) string {
	fm := articleFrontMatter{
		Type:        normalizeType(art.Type),
		Title:       art.Title,
		Slug:        art.Slug,
		Description: art.Description,
		Resource:    art.Resource,
		Tags:        art.Tags,
		Status:      art.Status,
		Version:     art.Version,
		EditSummary: art.EditSummary,
		Source:      art.Source,
	}
	if !art.Timestamp.IsZero() {
		fm.Timestamp = art.Timestamp.Format(time.RFC3339)
	}
	if !art.CreatedAt.IsZero() {
		fm.CreatedAt = art.CreatedAt.Format(time.RFC3339)
	}
	if !art.ArchivedAt.IsZero() {
		fm.ArchivedAt = art.ArchivedAt.Format(time.RFC3339)
	}
	if !art.StatusChangedAt.IsZero() {
		fm.StatusChangedAt = art.StatusChangedAt.Format(time.RFC3339)
	}

	out, err := yaml.Marshal(&fm)
	if err != nil {
		// yaml.Marshal of a plain struct does not realistically fail; fall back to a minimal block.
		return fmt.Sprintf("---\ntype: %s\ntitle: %s\nslug: %s\n---\n", fm.Type, fm.Title, fm.Slug)
	}
	return "---\n" + string(out) + "---\n"
}

// IndexArticle adds or updates a single article inside the Bleve search index.
func (s *Storage) IndexArticle(art *Article) error {
	return s.SearchIndex.Index(art.Slug, art)
}

// UnindexArticle deletes a single article from the Bleve search index.
func (s *Storage) UnindexArticle(slug string) error {
	return s.SearchIndex.Delete(slug)
}

// bootIndexBatchSize is how many documents are indexed per Bleve batch during boot
// synchronization.
//
// Batching is the whole cost of this function. Bleve's Index() is one transaction per call — a
// segment write and a store commit each time — so indexing document-by-document made startup
// linear in the corpus at roughly 24 ms per document: 26 s at 1,000 documents, four minutes at
// 10,000, with the server not answering for any of it. Batching amortizes the commit across a
// chunk instead.
//
// The chunk is bounded rather than "one batch for everything" because a batch is held in memory
// until it is executed, and a single batch over an entire large wiki would trade a startup delay
// for a startup allocation spike. 500 is comfortably past the point where per-commit overhead
// stops dominating.
const bootIndexBatchSize = 500

// SyncSearchIndex populates the Bleve index with all existing Markdown articles on startup and reconciles discrepancies.
func (s *Storage) SyncSearchIndex() error {
	_, _ = fmt.Fprintf(os.Stderr, "Commencing search index boot synchronization and reconciliation...\n")
	articles, err := s.ListArticles()
	if err != nil {
		return fmt.Errorf("failed to list articles for indexing: %w", err)
	}

	validSlugs := make(map[string]bool)
	// Always index the home page (since it is excluded from ListArticles)
	validSlugs["home"] = true

	batch := s.SearchIndex.NewBatch()
	// flush executes the pending batch and starts a fresh one. A batch failure is reported and
	// discarded rather than returned: boot indexing is best-effort reconciliation, and refusing to
	// start the server because one chunk of documents would not index is a worse outcome than
	// starting with an incomplete index that the next write repairs.
	flush := func() {
		if batch.Size() == 0 {
			return
		}
		if err := s.SearchIndex.Batch(batch); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to index a batch of %d articles: %v\n", batch.Size(), err)
		}
		batch = s.SearchIndex.NewBatch()
	}

	if homeArt, err := s.GetArticle("home"); err == nil {
		if err := batch.Index(homeArt.Slug, homeArt); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to index 'home' article: %v\n", err)
		}
	}

	for _, item := range articles {
		validSlugs[item.Slug] = true
		art, err := s.GetArticle(item.Slug)
		if err != nil {
			continue
		}
		if err := batch.Index(art.Slug, art); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to index article '%s': %v\n", item.Slug, err)
			continue
		}
		if batch.Size() >= bootIndexBatchSize {
			flush()
		}
	}
	flush()

	// Clean up any orphaned documents in the index that no longer exist on disk.
	//
	// DocCount bounds the request instead of a hardcoded ceiling: the previous Size of 1,000,000
	// asked Bleve to size a top-N collector for a million hits regardless of how many documents
	// existed, which is an allocation that scales with the constant rather than the corpus.
	docCount, err := s.SearchIndex.DocCount()
	if err != nil {
		docCount = 0
	}
	if docCount > 0 {
		q := bleve.NewMatchAllQuery()
		searchRequest := bleve.NewSearchRequest(q)
		searchRequest.Size = int(docCount)
		results, err := s.SearchIndex.Search(searchRequest)
		if err == nil {
			deletions := s.SearchIndex.NewBatch()
			for _, hit := range results.Hits {
				if !validSlugs[hit.ID] {
					_, _ = fmt.Fprintf(os.Stderr, "Removing orphaned article '%s' from search index...\n", hit.ID)
					deletions.Delete(hit.ID)
				}
			}
			if deletions.Size() > 0 {
				if err := s.SearchIndex.Batch(deletions); err != nil {
					_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to remove orphaned index entries: %v\n", err)
				}
			}
		}
	}

	newCount, _ := s.SearchIndex.DocCount()
	_, _ = fmt.Fprintf(os.Stderr, "Boot synchronization complete. Search index contains %d articles.\n", newCount)
	return nil
}

// SearchResult represents a single full-text query match.
type SearchResult struct {
	Title string `json:"title"`
	Slug  string `json:"slug"`
	// Type is the OKF document class of the hit. Faceted searches span types, so a caller has to
	// be able to tell a wiki article from an agent memory in the results.
	Type      string    `json:"type,omitempty"`
	Score     float64   `json:"score"`
	Timestamp time.Time `json:"timestamp"`
	// Snippets are HTML fragments: article text is entity-escaped and matched terms are
	// wrapped in <mark>. The frontend renders them as HTML, so every producer of a snippet
	// MUST escape the article text it embeds — see the fallback path in SearchArticles.
	Snippets []string `json:"snippets"`
	Tags     []string `json:"tags,omitempty"`
}

// SearchArticles searches for keywords inside article titles and contents, returning HTML highlighted snippets.
func (s *Storage) SearchArticles(queryStr string) ([]SearchResult, error) {
	return s.SearchArticlesWithOptions(queryStr, SearchOptions{legacyQueryHeuristics: true})
}

// SearchArticlesWithOptions runs a full-text search with explicit facets.
//
// The distinction from SearchArticles matters. The human sidebar wants agent documents hidden by
// default — they would drown out the wiki. An *agent* searching its own second brain wants the
// opposite: memories and plans are the whole point, and hiding them means the agent re-derives
// knowledge it already recorded. Facets make that an explicit choice by the caller rather than a
// property of the query text.
func (s *Storage) SearchArticlesWithOptions(queryStr string, opts SearchOptions) ([]SearchResult, error) {
	if queryStr == "" {
		return []SearchResult{}, nil
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	// An explicit "archived" entry in the tags facet implies IncludeArchived, mirroring the
	// browser's query-text heuristic. Without this, asking for archived documents the obvious way
	// — search_wiki(tags: ["archived"]) — returned zero results with no explanation: the archived
	// filter ran before the tag filter and discarded every hit the caller was asking for.
	for _, t := range opts.Tags {
		if strings.EqualFold(strings.TrimSpace(t), "archived") {
			opts.IncludeArchived = true
			break
		}
	}

	// Create a query matching terms (supports boolean logic, wildcards, fuzzy matching natively!)
	q := bleve.NewQueryStringQuery(queryStr)
	searchRequest := bleve.NewSearchRequest(q)

	// Configure Bleve highlighter style to wrap matched words in HTML <mark> tags
	searchRequest.Highlight = bleve.NewHighlightWithStyle("html")
	searchRequest.Highlight.AddField("content")
	searchRequest.Highlight.AddField("title")

	// Over-fetch: every filter below runs *after* Bleve has scored and truncated, so asking for
	// exactly `limit` hits silently returns fewer than requested whenever anything is dropped.
	//
	// This must not be conditional on opts.hasFilters(). Three filters apply to every search
	// regardless of the caller's facets: "home" is always excluded, archived documents are
	// excluded by default, and a hit whose file has been deleted is skipped. The home page in
	// particular mentions the wiki's own name constantly, so it scores highly on the most
	// ordinary queries — which made an unfaceted `limit: N` reliably return N-1.
	searchRequest.Size = maxSearchLimit

	searchResults, err := s.SearchIndex.Search(searchRequest)
	if err != nil {
		return nil, fmt.Errorf("bleve search failed: %w", err)
	}

	typeFilter := normalizeTypeFilter(opts.Types)
	tagFilter := lowercaseSet(opts.Tags)
	queryLower := strings.ToLower(queryStr)

	var results []SearchResult
	for _, hit := range searchResults.Hits {
		if len(results) >= limit {
			break
		}

		// Metadata is enough to decide whether a hit survives filtering; only the fallback
		// snippet below needs the body, and Bleve supplies fragments for most hits. Reading the
		// full file for every hit up front was pure waste.
		art, err := s.metaBySlug(hit.ID)
		if err != nil {
			// Skip if the physical Markdown file was deleted on disk but search index is slightly out of sync
			continue
		}

		// Exclude "home" from search results (reserved for Hero dashboard)
		if art.Slug == "home" {
			continue
		}

		if !opts.allowsArchived(art, queryLower) {
			continue
		}
		if !opts.allowsType(art, typeFilter, queryStr, queryLower) {
			continue
		}
		if !matchesAllTags(art.Tags, tagFilter) {
			continue
		}

		var snippets []string
		if frags, ok := hit.Fragments["content"]; ok {
			snippets = frags
		} else if frags, ok := hit.Fragments["title"]; ok {
			snippets = frags
		}

		// Fallback snippet if Bleve returns empty fragments (extract first 150 characters).
		// Bleve escapes the fragments it produces; this path must escape too, because the
		// frontend renders snippets as raw HTML. Without it, an article body starting with
		// <img src=x onerror=...> becomes stored XSS in the search dropdown.
		if len(snippets) == 0 {
			body := art.ContentPreview
			if full, err := s.GetArticle(art.Slug); err == nil {
				body = full.Content
			}
			runes := []rune(body)
			limit := 150
			if len(runes) < limit {
				limit = len(runes)
			}
			snippets = []string{html.EscapeString(string(runes[:limit])) + "..."}
		}

		results = append(results, SearchResult{
			Title:     art.Title,
			Slug:      art.Slug,
			Type:      art.Type,
			Score:     hit.Score,
			Timestamp: art.Timestamp,
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

// ErrVersionConflict reports that an article changed since the client loaded it. Callers map
// this to HTTP 409.
var ErrVersionConflict = errors.New("article has been updated in another session")

// ArticleEdit describes a user-initiated edit of an existing article. Nil pointer fields
// preserve the stored value; an explicit empty string clears it. LoadedVersion enables the
// optimistic-locking guard (0 disables it).
type ArticleEdit struct {
	Title       string
	Content     string
	Description *string
	Source      *string
	Resource    *string
	EditSummary string
	// Tags is a pointer so "omitted" and "explicitly empty" stay distinguishable: nil keeps the
	// article's current tags untouched, while a non-nil (even empty) slice replaces them after
	// memory-scope validation. Collapsing the two would make a caller that simply doesn't manage
	// tags silently strip every free tag off the document.
	Tags *[]string
	// Status is a pointer for the same reason: nil preserves the document's current lifecycle
	// state, so an editor that does not manage status cannot silently reset a completed plan.
	Status        *string
	LoadedVersion int
}

// ApplyArticleEdit loads the article, verifies LoadedVersion, merges the optional fields, and
// writes — all under a single lock. Doing the check and the write atomically is the point:
// performing them separately lets a concurrent writer land in between, so the guard passes and
// the other session's edit is silently overwritten anyway.
//
// The document type is immutable here; regular edits never relabel a reserved OKF class.
func (s *Storage) ApplyArticleEdit(slug string, edit ArticleEdit) (*Article, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	existing, err := s.GetArticle(slug)
	if err != nil {
		return nil, err
	}

	// A stored version of 0 means the file predates versioning — there is nothing to compare
	// against, so any loaded_version is accepted. Once an article has a version, the caller must
	// supply the matching one: a 0 against a versioned article is a stale read, not a waiver of
	// the check. (This condition used to also require edit.LoadedVersion > 0, which turned an
	// omitted version into a silent bypass of optimistic locking on every versioned article.)
	if existing.Version > 0 && existing.Version != edit.LoadedVersion {
		return nil, ErrVersionConflict
	}

	description := existing.Description
	if edit.Description != nil {
		description = *edit.Description
	}
	source := existing.Source
	if edit.Source != nil {
		source = *edit.Source
	}
	resource := existing.Resource
	if edit.Resource != nil {
		resource = *edit.Resource
	}

	// Preserve tool-managed memory-scope tags a user edit must not be able to drop or forge.
	cleanedTags := existing.Tags
	if edit.Tags != nil {
		cleanedTags = validateAndCleanUserTags(*edit.Tags, existing.Tags, existing.Type)
	}

	return s.saveArticleLocked(slug, edit.Title, edit.Content, description, source, resource,
		edit.EditSummary, cleanedTags, existing.Type, edit.Status)
}

// RevertArticle rolls the current active document back to the content of a historical version.
func (s *Storage) RevertArticle(slug string, version int) (*Article, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	histArt, err := s.GetArticleVersion(slug, version)
	if err != nil {
		return nil, err
	}

	summary := fmt.Sprintf("Reverted to version %d", version)
	// A revision written before status became a field carries it as a tag, so lift it out rather
	// than let a revert resurrect a tag set that validation now rejects. A revision that already
	// has the field keeps it.
	status, tags := ExtractLegacyStatus(histArt.Type, histArt.Tags)
	if histArt.Status != "" {
		status = histArt.Status
	}
	return s.saveArticleLocked(slug, histArt.Title, histArt.Content, histArt.Description, histArt.Source, histArt.Resource, summary, tags, histArt.Type, &status)
}

// UpdateArticleTags updates only the tag array for an article without modifying the title or content.
// The version check and the write happen under one lock, so the optimistic guard cannot be
// defeated by a concurrent writer slipping in between them.
func (s *Storage) UpdateArticleTags(slug string, tags []string, loadedVersion int, editSummary string) (*Article, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	art, err := s.GetArticle(slug)
	if err != nil {
		return nil, err
	}

	if loadedVersion > 0 && art.Version > 0 && art.Version != loadedVersion {
		// Wraps the sentinel so the MCP layer can errors.Is it and answer with the version to
		// retry on. The REST handler never reaches this: HandleUpdateArticleTags does its own
		// check and returns 409 before calling in.
		return nil, fmt.Errorf("%w: loaded version %d, current version %d", ErrVersionConflict, loadedVersion, art.Version)
	}

	if editSummary == "" {
		editSummary = "Updated article tags"
	}

	return s.saveArticleLocked(slug, art.Title, art.Content, art.Description, art.Source, art.Resource, editSummary, tags, art.Type, nil)
}

// Close releases resources held by the Storage, including the Bleve search index.
// It is safe to call multiple times; only the first invocation performs the close.
func (s *Storage) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.SearchIndex != nil {
			err = s.SearchIndex.Close()
		}
	})
	return err
}

// CleanupArchivedArticles removes articles that have been tagged as archived
// and whose archive time has elapsed based on the configured delay.
func (s *Storage) CleanupArchivedArticles() error {
	// Get the configured delay from environment variable
	delayStr := os.Getenv("NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS")
	if delayStr == "" {
		delayStr = "0" // Default to disabled
	}

	delay, err := strconv.Atoi(delayStr)
	if err != nil {
		return fmt.Errorf("invalid NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS value: %w", err)
	}

	// If delay is 0, auto-deletion is disabled
	if delay <= 0 {
		return nil
	}

	// List all articles
	articles, err := s.ListArticles()
	if err != nil {
		return fmt.Errorf("failed to list articles for cleanup: %w", err)
	}

	// Check each article
	for _, art := range articles {
		// Skip if not archived
		if art.ArchivedAt.IsZero() {
			continue
		}

		// Calculate if the delay has elapsed
		elapsed := time.Since(art.ArchivedAt)
		if elapsed >= time.Duration(delay)*24*time.Hour {
			// Delete the article
			err = s.DeleteArticle(art.Slug)
			if err != nil {
				return fmt.Errorf("failed to delete archived article %s: %w", art.Slug, err)
			}
			_, _ = fmt.Fprintf(os.Stderr, "Deleted archived article: %s (archived at: %s)\n", art.Slug, art.ArchivedAt.Format(time.RFC3339))
		}
	}

	return nil
}

// DeleteTagGlobally removes a tag from all articles in the wiki.
// Enforces validation: it returns an error if the tag is a tool-managed memory-scope tag.
func (s *Storage) DeleteTagGlobally(tag string) error {
	tagLower := strings.ToLower(tag)
	if strings.HasPrefix(tagLower, MemoryScopeTagPrefix) {
		return fmt.Errorf("cannot delete protected memory-scope tag: %s", tag)
	}

	// Held across the whole sweep so the operation is all-or-nothing with respect to other
	// writers, rather than interleaving per-article saves with concurrent edits.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

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
			_, err = s.saveArticleLocked(art.Slug, art.Title, art.Content, art.Description, art.Source, art.Resource, fmt.Sprintf("Removed tag '%s' globally", tag), newTags, art.Type, nil)
			if err != nil {
				return fmt.Errorf("failed to update article %s during global tag deletion: %w", art.Slug, err)
			}
		}
	}

	return nil
}

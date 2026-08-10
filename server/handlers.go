package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server coordinates the API handlers with the persistence storage layer.
type Server struct {
	Storage                *Storage
	WikiName               string
	DefaultTheme           string
	ThemeSchedulingEnabled bool
	EventBus               *EventBus
	Version                string
	Port                   string

	// AgentName is the attribution recorded in the activity log for MCP callers that do not
	// identify themselves. Optional; set from -agent-name / NEXWIKI_AGENT_NAME. It is a field
	// rather than a NewServer parameter because it is optional configuration with a working
	// default, and threading it through every construction site would say otherwise.
	//
	// Note this is NOT WikiName. Conflating the two is the defect this exists to fix — see
	// provenance.go.
	AgentName string

	// stdioClient holds the identity from a legacy `initialize` on the stdio connection, which is
	// the only transport where a handshake can be attributed for the life of a connection.
	stdioClient agentIdentity

	// shuttingDown is closed when the process begins a graceful shutdown. Every long-lived
	// response stream selects on it and returns.
	//
	// This is required for shutdown to work at all: http.Server.Shutdown waits for connections to
	// become *idle*, and an SSE stream never does. A single browser tab on the wiki holds one
	// open, so without this signal Shutdown blocks until its own timeout and the supervisor
	// SIGKILLs the process first — leaving the Bleve index open, which is the corruption this
	// whole mechanism exists to prevent.
	shuttingDown chan struct{}
	shutdownOnce sync.Once
}

// NewServer builds a new API controller.
func NewServer(storage *Storage, wikiName string, defaultTheme string, themeSchedulingEnabled bool, eventBus *EventBus, version string, port string) *Server {
	return &Server{
		shuttingDown:           make(chan struct{}),
		Storage:                storage,
		WikiName:               wikiName,
		DefaultTheme:           defaultTheme,
		ThemeSchedulingEnabled: themeSchedulingEnabled,
		EventBus:               eventBus,
		Version:                version,
		Port:                   port,
	}
}

// BeginShutdown signals every open response stream to end so http.Server.Shutdown can complete.
// Safe to call more than once.
func (srv *Server) BeginShutdown() {
	srv.shutdownOnce.Do(func() {
		if srv.shuttingDown != nil {
			close(srv.shuttingDown)
		}
	})
}

// shutdownSignal returns the channel closed at shutdown, tolerating a zero-value Server (some
// tests construct one directly) by returning nil, which blocks forever in a select.
func (srv *Server) shutdownSignal() <-chan struct{} {
	return srv.shuttingDown
}

// JSON Helper to write standard structured error responses.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// JSON Helper to write standard success responses.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// ConfigResp represents the public deployment settings exposed to the frontend.
type ConfigResp struct {
	WikiName               string `json:"wiki_name"`
	DefaultTheme           string `json:"default_theme"`
	ThemeSchedulingEnabled bool   `json:"theme_scheduling_enabled"`
	ScheduledTheme         string `json:"scheduled_theme,omitempty"`
	Version                string `json:"version"`
}

// HandleGetStatusTags returns the canonical list of recognized status tags.
func (srv *Server) HandleGetStatusTags(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tags":        StatusTags,
		"description": "Status tags indicate the current lifecycle state of a wiki article or collaborative AI plan. These tags are displayed with priority on the home dashboard.",
	})
}

// HandleGetConfig serves the custom title configuration settings to the client.
func (srv *Server) HandleGetConfig(w http.ResponseWriter, _ *http.Request) {
	var scheduledTheme string
	if srv.ThemeSchedulingEnabled {
		customThemes, err := srv.Storage.ThemeStore.LoadCustomThemes()
		if err == nil {
			allThemes := make([]Theme, 0, len(DefaultThemes)+len(customThemes))
			allThemes = append(allThemes, DefaultThemes...)
			allThemes = append(allThemes, customThemes...)
			scheduledTheme = ResolveScheduledTheme(allThemes, time.Now())
		}
	}

	writeJSON(w, http.StatusOK, ConfigResp{
		WikiName:               srv.WikiName,
		DefaultTheme:           srv.DefaultTheme,
		ThemeSchedulingEnabled: srv.ThemeSchedulingEnabled,
		ScheduledTheme:         scheduledTheme,
		Version:                srv.Version,
	})
}

// HandleListArticles lists all wiki pages' front-matter metadata.
func (srv *Server) HandleListArticles(w http.ResponseWriter, _ *http.Request) {
	articles, err := srv.Storage.ListArticles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, articles)
}

// HandleGetArticle gets metadata and Markdown body for a single slug.
func (srv *Server) HandleGetArticle(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "article slug is required")
		return
	}

	art, err := srv.Storage.GetArticle(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, art)
}

// CreateArticleReq represents the payload body for creating a new article.
// Description and Source are pointers, so clients that omit them preserve existing values on update,
// while sending an explicit empty string clears them.
type CreateArticleReq struct {
	Title         string   `json:"title"`
	Content       string   `json:"content"`
	Description   *string  `json:"description"`    // Optional one-line summary
	Source        *string  `json:"source"`         // Optional provenance reference
	Resource      *string  `json:"resource"`       // Optional OKF canonical URI of the concept
	EditSummary   string   `json:"edit_summary"`   // Summary for revision history
	LoadedVersion int      `json:"loaded_version"` // Version loaded by client for conflict validation
	Tags          []string `json:"tags"`           // Tags list
}

// validateAndCleanUserTags preserves tool-managed memory-scope tags (memory-<scope>) that already
// exist on a document and strips any the user tries to forge onto one. The document *class* is carried
// by the OKF `type` field, not by tags, so there is no class-tag stripping here anymore. Free user tags
// and recognized status tags pass through unchanged (deduplicated, case-insensitively).
func validateAndCleanUserTags(incomingTags []string, existingTags []string) []string {
	existingMemoryScope := make(map[string]bool)
	for _, t := range existingTags {
		tLower := strings.ToLower(strings.TrimSpace(t))
		if strings.HasPrefix(tLower, MemoryScopeTagPrefix) {
			existingMemoryScope[tLower] = true
		}
	}

	var result []string
	seen := make(map[string]bool)

	// Re-assert existing memory-scope tags first so a user edit can never drop them.
	for _, t := range existingTags {
		tTrimmed := strings.TrimSpace(t)
		tLower := strings.ToLower(tTrimmed)
		if strings.HasPrefix(tLower, MemoryScopeTagPrefix) && !seen[tLower] {
			seen[tLower] = true
			result = append(result, tTrimmed)
		}
	}

	for _, t := range incomingTags {
		tTrimmed := strings.TrimSpace(t)
		if tTrimmed == "" {
			continue
		}
		tLower := strings.ToLower(tTrimmed)
		// Users may not forge new memory-scope tags onto a document; only pre-existing ones survive.
		if strings.HasPrefix(tLower, MemoryScopeTagPrefix) && !existingMemoryScope[tLower] {
			continue
		}
		if !seen[tLower] {
			seen[tLower] = true
			result = append(result, tTrimmed)
		}
	}

	return result
}

// HandleCreateArticle parses details and creates a new article file.
func (srv *Server) HandleCreateArticle(w http.ResponseWriter, r *http.Request) {
	var req CreateArticleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, err)
		return
	}

	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	// Verify if slug already exists before writing
	slug := Slugify(req.Title)
	if _, err := srv.Storage.GetArticle(slug); err == nil {
		writeError(w, http.StatusConflict, "an article with this title or slug already exists")
		return
	}

	// Clean tags (existingTags is nil on creation)
	cleanedTags := validateAndCleanUserTags(req.Tags, nil)

	description := ""
	if req.Description != nil {
		description = *req.Description
	}
	source := ""
	if req.Source != nil {
		source = *req.Source
	}
	resource := ""
	if req.Resource != nil {
		resource = *req.Resource
	}

	// Regular article creation always produces a Wiki document; reserved types are tool-only.
	art, err := srv.Storage.SaveArticle("", req.Title, req.Content, description, source, resource, req.EditSummary, cleanedTags, ContentTypeWiki)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if srv.EventBus != nil {
		srv.EventBus.PublishActivity("api", "create", "", art.Slug, art.Title, "User")
		articles, err := srv.Storage.ListArticles()
		if err == nil {
			dir := getArticleDirectory(art.Type)
			dirCount := 0
			for _, a := range articles {
				if getArticleDirectory(a.Type) == dir {
					dirCount++
				}
			}
			srv.EventBus.PublishWikiUpdate(WikiUpdate{
				Type:           "article-added",
				Slug:           art.Slug,
				Title:          art.Title,
				Tags:           art.Tags,
				Directory:      dir,
				TotalCount:     len(articles),
				DirectoryCount: dirCount,
			})
		}
	}

	writeJSON(w, http.StatusCreated, art)
}

// HandleUpdateArticle updates an existing article, handling potential slug changes.
func (srv *Server) HandleUpdateArticle(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "article slug is required")
		return
	}

	var req CreateArticleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, err)
		return
	}

	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	// Existence check, optimistic-locking guard, field merge, and write happen atomically inside
	// ApplyArticleEdit. Omitted description/source/resource preserve existing values; explicit
	// empty strings clear them. The document type is preserved.
	art, err := srv.Storage.ApplyArticleEdit(slug, ArticleEdit{
		Title:       req.Title,
		Content:     req.Content,
		Description: req.Description,
		Source:      req.Source,
		Resource:    req.Resource,
		EditSummary: req.EditSummary,
		// The REST editor always submits the full tag set, so tags are always replaced here
		// (an omitted "tags" key clears them, which is the pre-existing behavior).
		Tags:          &req.Tags,
		LoadedVersion: req.LoadedVersion,
	})
	switch {
	case errors.Is(err, ErrVersionConflict):
		writeError(w, http.StatusConflict, "this article has been updated in another session. Please copy your edits, reload the page, and try again.")
		return
	case err != nil && strings.Contains(err.Error(), "article not found"):
		writeError(w, http.StatusNotFound, "article not found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if srv.EventBus != nil {
		srv.EventBus.PublishActivity("api", "edit", "", art.Slug, art.Title, "User")
		articles, err := srv.Storage.ListArticles()
		if err == nil {
			dir := getArticleDirectory(art.Type)
			dirCount := 0
			for _, a := range articles {
				if getArticleDirectory(a.Type) == dir {
					dirCount++
				}
			}
			srv.EventBus.PublishWikiUpdate(WikiUpdate{
				Type:           "article-edited",
				Slug:           art.Slug,
				Title:          art.Title,
				Tags:           art.Tags,
				Directory:      dir,
				TotalCount:     len(articles),
				DirectoryCount: dirCount,
			})
		}
	}

}

// HandleUpdateArticleTags updates only the tags of an existing article.
func (srv *Server) HandleUpdateArticleTags(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "article slug is required")
		return
	}

	var req struct {
		Tags          []string `json:"tags"`
		LoadedVersion int      `json:"loaded_version"`
		EditSummary   string   `json:"edit_summary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, err)
		return
	}

	// Verify that the article actually exists first
	existing, err := srv.Storage.GetArticle(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}

	// Optimistic locking verification
	if req.LoadedVersion > 0 && existing.Version > 0 && existing.Version != req.LoadedVersion {
		writeError(w, http.StatusConflict, "this article has been updated in another session. Please reload the page and try again.")
		return
	}

	// Clean tags and preserve existing tool-managed "memory-<scope>" tags
	cleanedTags := validateAndCleanUserTags(req.Tags, existing.Tags)

	summary := req.EditSummary
	if summary == "" {
		summary = "Updated article tags"
	}

	art, err := srv.Storage.SaveArticle(slug, existing.Title, existing.Content, existing.Description, existing.Source, existing.Resource, summary, cleanedTags, existing.Type)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if srv.EventBus != nil {
		srv.EventBus.PublishActivity("api", "edit", "update_tags", art.Slug, art.Title, "User")
		articles, err := srv.Storage.ListArticles()
		if err == nil {
			dir := getArticleDirectory(art.Type)
			dirCount := 0
			for _, a := range articles {
				if getArticleDirectory(a.Type) == dir {
					dirCount++
				}
			}
			srv.EventBus.PublishWikiUpdate(WikiUpdate{
				Type:           "article-edited",
				Slug:           art.Slug,
				Title:          art.Title,
				Tags:           art.Tags,
				Directory:      dir,
				TotalCount:     len(articles),
				DirectoryCount: dirCount,
			})
		}
	}

	writeJSON(w, http.StatusOK, art)
}

// HandleDeleteArticle deletes the article file and all its associated assets.
func (srv *Server) HandleDeleteArticle(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "article slug is required")
		return
	}

	// Verify existence and save tags/title
	existing, err := srv.Storage.GetArticle(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}

	if err := srv.Storage.DeleteArticle(slug); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if srv.EventBus != nil {
		srv.EventBus.PublishActivity("api", "delete", "", slug, existing.Title, "User")
		articles, err := srv.Storage.ListArticles()
		if err == nil {
			dir := getArticleDirectory(existing.Type)
			dirCount := 0
			for _, a := range articles {
				if getArticleDirectory(a.Type) == dir {
					dirCount++
				}
			}
			srv.EventBus.PublishWikiUpdate(WikiUpdate{
				Type:           "article-removed",
				Slug:           slug,
				Title:          existing.Title,
				Tags:           existing.Tags,
				Directory:      dir,
				TotalCount:     len(articles),
				DirectoryCount: dirCount,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "article and assets deleted successfully"})
}

// HandleUploadAsset uploads an image or asset specifically bound to the article slug.
func (srv *Server) HandleUploadAsset(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "article slug is required")
		return
	}

	// Buffer up to 10 MB of the form in memory; the total transfer is capped by the
	// LimitRequestBodies middleware, which is what stops an unbounded spill to disk.
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeDecodeError(w, err)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to retrieve file parameter 'file'")
		return
	}
	defer func() { _ = file.Close() }()

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read file contents")
		return
	}

	// Basic safety check for mime types. The client-supplied Content-Type is not trustworthy on
	// its own, so the filename extension must agree with it: http.ServeFile derives the response
	// Content-Type from the extension, and that (plus nosniff) is what actually governs how a
	// browser interprets the file later.
	mimeType := header.Header.Get("Content-Type")
	allowedExtsForMime := map[string][]string{
		"image/jpeg":    {".jpg", ".jpeg"},
		"image/png":     {".png"},
		"image/gif":     {".gif"},
		"image/webp":    {".webp"},
		"image/svg+xml": {".svg"},
	}

	allowedExts, mimeOK := allowedExtsForMime[mimeType]
	if !mimeOK {
		writeError(w, http.StatusBadRequest, "unsupported asset type: only standard images are allowed")
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !slices.Contains(allowedExts, ext) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("file extension %q does not match the declared type %q", ext, mimeType))
		return
	}

	url, err := srv.Storage.SaveAsset(slug, header.Filename, fileBytes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// HandleGetAsset serves the requested uploaded file from the disk.
func (srv *Server) HandleGetAsset(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	filename := r.PathValue("filename")

	if slug == "" || filename == "" {
		writeError(w, http.StatusBadRequest, "slug and filename are required parameters")
		return
	}

	filePath, err := srv.Storage.GetAssetPath(slug, filename)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	// SVG is an active document format: served inline from this origin, any <script> inside it
	// runs as same-origin JavaScript against an unauthenticated API. Force a download instead,
	// and stop content sniffing from re-interpreting any other asset as markup.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.EqualFold(filepath.Ext(filePath), ".svg") {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(filePath)+"\"")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	}

	// Serves file directly using net/http.ServeFile
	http.ServeFile(w, r, filePath)
}

// EnableCORS lives in security.go, alongside the Origin allow-list it enforces.

// HandleSearchArticles executes full-text search against the index and returns matching summaries.
func (srv *Server) HandleSearchArticles(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	results, err := srv.Storage.SearchArticles(query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// HandleGetBacklinks returns metadata for all articles that link to the target slug via WikiLinks.
func (srv *Server) HandleGetBacklinks(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "article slug is required")
		return
	}

	target, err := srv.Storage.GetArticle(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}

	backlinks, err := srv.Storage.GetBacklinks(target.Slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if backlinks == nil {
		backlinks = []Article{}
	}
	writeJSON(w, http.StatusOK, backlinks)
}

// HandleGetArticleHistory retrieves metadata for all historical versions of an article.
func (srv *Server) HandleGetArticleHistory(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "article slug is required")
		return
	}

	history, err := srv.Storage.GetArticleHistory(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, srv.attributeHistory(Slugify(slug), history))
}

// historyEntryResponse is a revision plus who made it. Article is embedded rather than copied
// field-by-field, so its JSON keys are promoted unchanged and the addition is purely additive for
// existing clients.
type historyEntryResponse struct {
	Article
	Agent string `json:"agent,omitempty"`
	Tool  string `json:"tool,omitempty"`
	Via   string `json:"via,omitempty"`
}

// attributeHistory joins revision metadata with the activity log so the History drawer can say who
// made each edit, using the same matching rules as the MCP tool — see attributeRevisions.
func (srv *Server) attributeHistory(slug string, history []Article) []historyEntryResponse {
	refs := make([]RevisionRef, len(history))
	for i, ver := range history {
		refs[i] = RevisionRef{Version: ver.Version, Timestamp: ver.Timestamp}
	}
	refs = attributeRevisions(ActivityLogPath(srv.Storage.DataDir), slug, refs)

	out := make([]historyEntryResponse, len(history))
	for i, ver := range history {
		out[i] = historyEntryResponse{Article: ver, Agent: refs[i].Agent, Tool: refs[i].Tool, Via: refs[i].Via}
	}
	return out
}

// HandleGetArticleVersion retrieves single historical version details including content body.
func (srv *Server) HandleGetArticleVersion(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "article slug is required")
		return
	}

	versionStr := r.PathValue("version")
	version, err := strconv.Atoi(versionStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid version parameter")
		return
	}

	art, err := srv.Storage.GetArticleVersion(slug, version)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, art)
}

// HandleRevertArticle rolls back the active article content to a historical version.
func (srv *Server) HandleRevertArticle(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "article slug is required")
		return
	}

	var req struct {
		Version int `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, err)
		return
	}

	if req.Version <= 0 {
		writeError(w, http.StatusBadRequest, "valid version number is required")
		return
	}

	art, err := srv.Storage.RevertArticle(slug, req.Version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if srv.EventBus != nil {
		srv.EventBus.PublishActivity("api", "revert", "", art.Slug, art.Title, "User")
		articles, err := srv.Storage.ListArticles()
		if err == nil {
			dir := getArticleDirectory(art.Type)
			dirCount := 0
			for _, a := range articles {
				if getArticleDirectory(a.Type) == dir {
					dirCount++
				}
			}
			srv.EventBus.PublishWikiUpdate(WikiUpdate{
				Type:           "article-edited",
				Slug:           art.Slug,
				Title:          art.Title,
				Tags:           art.Tags,
				Directory:      dir,
				TotalCount:     len(articles),
				DirectoryCount: dirCount,
			})
		}
	}

	writeJSON(w, http.StatusOK, art)
}

// HandleDeleteTagGlobally removes a tag from all articles.
func (srv *Server) HandleDeleteTagGlobally(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag")
	if tag == "" {
		writeError(w, http.StatusBadRequest, "tag parameter is required")
		return
	}

	// Double-check permission: tool-managed memory-scope tags are protected.
	if strings.HasPrefix(strings.ToLower(tag), MemoryScopeTagPrefix) {
		writeError(w, http.StatusForbidden, "cannot delete protected memory-scope tag")
		return
	}

	if err := srv.Storage.DeleteTagGlobally(tag); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "tag deleted globally successfully"})
}

// HandleGetThemes serves all default and custom themes to the client.
func (srv *Server) HandleGetThemes(w http.ResponseWriter, _ *http.Request) {
	customThemes, err := srv.Storage.ThemeStore.LoadCustomThemes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load custom themes")
		return
	}

	allThemes := make([]Theme, 0, len(DefaultThemes)+len(customThemes))
	allThemes = append(allThemes, DefaultThemes...)
	allThemes = append(allThemes, customThemes...)

	writeJSON(w, http.StatusOK, allThemes)
}

// HandleSaveTheme saves a custom dual-mode theme to storage.
func (srv *Server) HandleSaveTheme(w http.ResponseWriter, r *http.Request) {
	var newTheme Theme
	if err := json.NewDecoder(r.Body).Decode(&newTheme); err != nil {
		writeDecodeError(w, err)
		return
	}

	if newTheme.Name == "" {
		writeError(w, http.StatusBadRequest, "theme name is required")
		return
	}

	// Validate theme name does not conflict with default themes
	for _, t := range DefaultThemes {
		if strings.EqualFold(t.Name, newTheme.Name) {
			writeError(w, http.StatusConflict, "cannot overwrite default theme")
			return
		}
	}

	newTheme.Custom = true // enforce custom

	customThemes, err := srv.Storage.ThemeStore.LoadCustomThemes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load custom themes")
		return
	}

	// Check if updating an existing custom theme or adding a new one
	found := false
	for i, t := range customThemes {
		if strings.EqualFold(t.Name, newTheme.Name) {
			customThemes[i] = newTheme
			found = true
			break
		}
	}

	if !found {
		customThemes = append(customThemes, newTheme)
	}

	if err := srv.Storage.ThemeStore.SaveCustomThemes(customThemes); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save custom theme")
		return
	}

	writeJSON(w, http.StatusOK, newTheme)
}

// HandleDeleteTheme deletes a custom theme by name.
func (srv *Server) HandleDeleteTheme(w http.ResponseWriter, r *http.Request) {
	themeName := r.PathValue("name")
	if themeName == "" {
		writeError(w, http.StatusBadRequest, "theme name is required")
		return
	}

	// Validate not default theme
	for _, t := range DefaultThemes {
		if strings.EqualFold(t.Name, themeName) {
			writeError(w, http.StatusBadRequest, "cannot delete default theme")
			return
		}
	}

	customThemes, err := srv.Storage.ThemeStore.LoadCustomThemes()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load custom themes")
		return
	}

	var updatedThemes []Theme
	found := false
	for _, t := range customThemes {
		if strings.EqualFold(t.Name, themeName) {
			found = true
			continue
		}
		updatedThemes = append(updatedThemes, t)
	}

	if !found {
		writeError(w, http.StatusNotFound, "theme not found")
		return
	}

	if err := srv.Storage.ThemeStore.SaveCustomThemes(updatedThemes); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save updated custom themes")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "theme deleted successfully"})
}

// SkillResp represents the structured metadata format returned to AI agents for skills discovery.
type SkillResp struct {
	Name        string    `json:"name"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Tags        []string  `json:"tags"`
	Version     int       `json:"version"`
	RawURL      string    `json:"raw_url"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// extractDescription isolates the first non-heading, non-empty paragraph of a Markdown page,
// strips double bracket WikiLinks, and limits it to 200 characters to form a clean description snippet.
func extractDescription(content string) string {
	paragraphs := strings.Split(content, "\n\n")
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		// Clean double brackets
		p = strings.ReplaceAll(p, "[[", "")
		p = strings.ReplaceAll(p, "]]", "")

		runes := []rune(p)
		if len(runes) > 200 {
			return string(runes[:200]) + "..."
		}
		return p
	}
	return ""
}

// HandleListSkills queries all pages, isolates skill documents (OKF type AI-Agent-Skill),
// parses their descriptions, and exposes them as a JSON registry with fully qualified raw URLs.
func (srv *Server) HandleListSkills(w http.ResponseWriter, r *http.Request) {
	articles, err := srv.Storage.ListArticles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var skills []SkillResp
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	for _, art := range articles {
		if art.Type != ContentTypeSkill {
			continue
		}

		// Load full article to parse description from content body
		fullArt, err := srv.Storage.GetArticle(art.Slug)
		desc := ""
		if err == nil {
			desc = extractDescription(fullArt.Content)
		}

		rawURL := fmt.Sprintf("%s://%s/api/skills/%s/raw", scheme, r.Host, art.Slug)
		skills = append(skills, SkillResp{
			Name:        art.Slug,
			Title:       art.Title,
			Description: desc,
			Tags:        art.Tags,
			Version:     art.Version,
			RawURL:      rawURL,
			UpdatedAt:   art.Timestamp,
		})
	}

	writeJSON(w, http.StatusOK, skills)
}

// HandleGetSkill retrieves a single registered AI agent skill in JSON format.
func (srv *Server) HandleGetSkill(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "skill slug is required")
		return
	}

	art, err := srv.Storage.GetArticle(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	if art.Type != ContentTypeSkill {
		writeError(w, http.StatusNotFound, "requested article is not registered as an AI skill")
		return
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	rawURL := fmt.Sprintf("%s://%s/api/skills/%s/raw", scheme, r.Host, art.Slug)

	writeJSON(w, http.StatusOK, SkillResp{
		Name:        art.Slug,
		Title:       art.Title,
		Description: extractDescription(art.Content),
		Tags:        art.Tags,
		Version:     art.Version,
		RawURL:      rawURL,
		UpdatedAt:   art.Timestamp,
	})
}

// HandleGetSkillRaw serves the exact raw SKILL.md file with YAML frontmatter as plain text.
func (srv *Server) HandleGetSkillRaw(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "skill slug is required")
		return
	}

	cleanedSlug := Slugify(slug)
	if cleanedSlug == "" {
		writeError(w, http.StatusBadRequest, "invalid slug")
		return
	}

	art, err := srv.Storage.GetArticle(cleanedSlug)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	if art.Type != ContentTypeSkill {
		writeError(w, http.StatusNotFound, "requested article is not registered as an AI skill")
		return
	}

	filePath := filepath.Join(srv.Storage.ArticleDir, cleanedSlug+".md")
	data, err := os.ReadFile(filePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read raw skill file")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%s.md", cleanedSlug))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// WikiStats represents article metadata counts.
type WikiStats struct {
	TotalCount      int            `json:"total_count"`
	DirectoryCounts map[string]int `json:"directory_counts"`
}

// HandleGetWikiStats returns counts of wiki, memories, plans, and skills.
func (srv *Server) HandleGetWikiStats(w http.ResponseWriter, _ *http.Request) {
	articles, err := srv.Storage.ListArticles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	stats := WikiStats{
		TotalCount: len(articles),
		DirectoryCounts: map[string]int{
			"wiki":       0,
			"aimemories": 0,
			"aiplans":    0,
			"aiskills":   0,
		},
	}

	for _, art := range articles {
		dir := getArticleDirectory(art.Type)
		stats.DirectoryCounts[dir]++
	}

	writeJSON(w, http.StatusOK, stats)
}

// HandleActivityStream establishes the SSE connection for real-time syncing.
func (srv *Server) HandleActivityStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Subscribe to the event bus
	ch := srv.EventBus.Subscribe()
	defer srv.EventBus.Unsubscribe(ch)

	// Stream historical buffer first
	history := srv.EventBus.GetHistory()
	for _, ev := range history {
		data, err := json.Marshal(ev)
		if err == nil {
			_, _ = fmt.Fprintf(w, "event: history\ndata: %s\n\n", string(data))
		}
	}
	flusher.Flush()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	notify := r.Context().Done()

	for {
		select {
		case msg, open := <-ch:
			if !open {
				return
			}
			_, _ = fmt.Fprint(w, msg)
			flusher.Flush()

		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()

		case <-notify:
			return

		case <-srv.shutdownSignal():
			// Shutdown waits for connections to go idle, which this one never does; ending here
			// is what lets the process close the search index before it is killed.
			return
		}
	}
}

// HandleExportOKFBundle streams the knowledge base as a downloadable OKF v0.1 bundle (.zip).
func (srv *Server) HandleExportOKFBundle(w http.ResponseWriter, _ *http.Request) {
	data, err := srv.Storage.ExportOKFBundle()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	fileName := fmt.Sprintf("nexwiki-okf-%s.zip", time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// HandleImportOKFBundle imports an uploaded OKF bundle (.zip) and returns a conformance report.
func (srv *Server) HandleImportOKFBundle(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		writeDecodeError(w, err)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to retrieve file parameter 'file'")
		return
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read uploaded bundle")
		return
	}

	report, err := srv.Storage.ImportOKFBundle(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// HandleGetActivityLog serves a paginated page of the durable activity log (spanning archives).
// Query params: before (RFC3339 cursor — return events strictly older than this), limit (default 50,
// max 500), and optional action/source filters. Events are returned newest-first for the drawer.
func (srv *Server) HandleGetActivityLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var before time.Time
	if b := strings.TrimSpace(q.Get("before")); b != "" {
		ts, err := time.Parse(time.RFC3339, b)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid 'before' cursor; expected RFC3339 timestamp")
			return
		}
		before = ts
	}

	limit := 50
	if l := strings.TrimSpace(q.Get("limit")); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}

	events, err := ReadActivityLogBefore(ActivityLogPath(srv.Storage.DataDir), before, limit, q.Get("action"), q.Get("source"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = []LogEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"events":   events,
		"has_more": len(events) == limit,
	})
}

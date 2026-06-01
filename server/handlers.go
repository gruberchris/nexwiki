package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Server coordinates the API handlers with the persistence storage layer.
type Server struct {
	Storage      *Storage
	WikiName     string
	DefaultTheme string
}

// NewServer builds a new API controller.
func NewServer(storage *Storage, wikiName string, defaultTheme string) *Server {
	return &Server{
		Storage:      storage,
		WikiName:     wikiName,
		DefaultTheme: defaultTheme,
	}
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
	WikiName     string `json:"wiki_name"`
	DefaultTheme string `json:"default_theme"`
}

// HandleGetConfig serves the custom title configuration settings to the client.
func (srv *Server) HandleGetConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, ConfigResp{
		WikiName:     srv.WikiName,
		DefaultTheme: srv.DefaultTheme,
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
type CreateArticleReq struct {
	Title         string   `json:"title"`
	Content       string   `json:"content"`
	EditSummary   string   `json:"edit_summary"`   // Summary for revision history
	LoadedVersion int      `json:"loaded_version"` // Version loaded by client for conflict validation
	Tags          []string `json:"tags"`           // Tags list
}

// validateAndCleanUserTags handles stripping new "aiagent-" tags while allowing removal of existing ones.
func validateAndCleanUserTags(incomingTags []string, existingTags []string) []string {
	existingAgentTags := make(map[string]bool)
	for _, t := range existingTags {
		tLower := strings.ToLower(t)
		if strings.HasPrefix(tLower, "aiagent-") {
			existingAgentTags[tLower] = true
		}
	}

	var result []string
	seen := make(map[string]bool)

	for _, t := range incomingTags {
		tTrimmed := strings.TrimSpace(t)
		if tTrimmed == "" {
			continue
		}
		tLower := strings.ToLower(tTrimmed)
		if strings.HasPrefix(tLower, "aiagent-") && !existingAgentTags[tLower] && tLower != "aiagent-skill" {
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
		writeError(w, http.StatusBadRequest, "invalid request payload")
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

	art, err := srv.Storage.SaveArticle("", req.Title, req.Content, req.EditSummary, cleanedTags)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
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
		writeError(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	// Verify that the article actually exists first
	existing, err := srv.Storage.GetArticle(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}

	// Optimistic locking verification to prevent concurrent edit collision
	if req.LoadedVersion > 0 && existing.Version > 0 && existing.Version != req.LoadedVersion {
		writeError(w, http.StatusConflict, "this article has been updated in another session. Please copy your edits, reload the page, and try again.")
		return
	}

	// Clean tags and preserve existing "aiagent-" tags
	cleanedTags := validateAndCleanUserTags(req.Tags, existing.Tags)

	art, err := srv.Storage.SaveArticle(slug, req.Title, req.Content, req.EditSummary, cleanedTags)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
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

	// Verify existence
	if _, err := srv.Storage.GetArticle(slug); err != nil {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}

	if err := srv.Storage.DeleteArticle(slug); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
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

	// Parse multipart form (10 MB max)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart form data")
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

	// Basic safety check for mime types
	mimeType := header.Header.Get("Content-Type")
	allowedMime := map[string]bool{
		"image/jpeg":    true,
		"image/png":     true,
		"image/gif":     true,
		"image/webp":    true,
		"image/svg+xml": true,
	}

	if !allowedMime[mimeType] {
		writeError(w, http.StatusBadRequest, "unsupported asset type: only standard images are allowed")
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

	// Serves file directly using net/http.ServeFile
	http.ServeFile(w, r, filePath)
}

// EnableCORS handles standard CORS options matching standard browser pre-flight checks.
func EnableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

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
	writeJSON(w, http.StatusOK, history)
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
		writeError(w, http.StatusBadRequest, "invalid request payload")
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

	writeJSON(w, http.StatusOK, art)
}

// HandleDeleteTagGlobally removes a tag from all articles.
func (srv *Server) HandleDeleteTagGlobally(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag")
	if tag == "" {
		writeError(w, http.StatusBadRequest, "tag parameter is required")
		return
	}

	// Double-check permission
	if strings.HasPrefix(strings.ToLower(tag), "aiagent-") {
		writeError(w, http.StatusForbidden, "cannot delete protected AI agent tag")
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
		writeError(w, http.StatusBadRequest, "invalid request payload")
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

// HandleListSkills queries all pages, isolates skills possessing the 'aiagent-skill' tag,
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
		isSkill := false
		var cleanTags []string
		for _, tag := range art.Tags {
			tagLower := strings.ToLower(tag)
			if tagLower == "aiagent-skill" {
				isSkill = true
			} else if !strings.HasPrefix(tagLower, "aiagent-") {
				cleanTags = append(cleanTags, tag)
			}
		}

		if isSkill {
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
				Tags:        cleanTags,
				Version:     art.Version,
				RawURL:      rawURL,
				UpdatedAt:   art.UpdatedAt,
			})
		}
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

	isSkill := false
	var cleanTags []string
	for _, tag := range art.Tags {
		tagLower := strings.ToLower(tag)
		if tagLower == "aiagent-skill" {
			isSkill = true
		} else if !strings.HasPrefix(tagLower, "aiagent-") {
			cleanTags = append(cleanTags, tag)
		}
	}

	if !isSkill {
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
		Tags:        cleanTags,
		Version:     art.Version,
		RawURL:      rawURL,
		UpdatedAt:   art.UpdatedAt,
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

	isSkill := false
	for _, tag := range art.Tags {
		if strings.ToLower(tag) == "aiagent-skill" {
			isSkill = true
			break
		}
	}

	if !isSkill {
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

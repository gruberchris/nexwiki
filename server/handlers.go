package server

import (
	"encoding/json"
	"io"
	"net/http"
)

// Server coordinates the API handlers with the persistence storage layer.
type Server struct {
	Storage  *Storage
	WikiName string
}

// NewServer builds a new API controller.
func NewServer(storage *Storage, wikiName string) *Server {
	return &Server{
		Storage:  storage,
		WikiName: wikiName,
	}
}

// JSON Helper to write standard structured error responses.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// JSON Helper to write standard success responses.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// ConfigResp represents the public deployment settings exposed to the frontend.
type ConfigResp struct {
	WikiName string `json:"wiki_name"`
}

// HandleGetConfig serves the custom title configuration settings to the client.
func (srv *Server) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ConfigResp{WikiName: srv.WikiName})
}

// HandleListArticles lists all wiki pages' front-matter metadata.
func (srv *Server) HandleListArticles(w http.ResponseWriter, r *http.Request) {
	articles, err := srv.Storage.ListArticles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, articles)
}

// HandleGetArticle gets metadata and markdown body for a single slug.
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

// HandleCreateArticle payload body
type CreateArticleReq struct {
	Title   string `json:"title"`
	Content string `json:"content"`
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

	art, err := srv.Storage.SaveArticle("", req.Title, req.Content)
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
	if _, err := srv.Storage.GetArticle(slug); err != nil {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}

	art, err := srv.Storage.SaveArticle(slug, req.Title, req.Content)
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

	// Parse multi-part form (10 MB max)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart form data")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to retrieve file parameter 'file'")
		return
	}
	defer file.Close()

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

// HandleGetAsset serves the requested uploaded file from disk.
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

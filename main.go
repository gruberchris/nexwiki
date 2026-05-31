package main

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"nexwiki/server"
	"os"
	"path/filepath"
	"strings"
)

//go:embed frontend/dist/*
var embeddedFrontend embed.FS

func main() {
	// Force all log statements to print exclusively to Stderr!
	// This prevents logs from corrupting the Stdio MCP JSON-RPC communication on Stdout.
	log.SetOutput(os.Stderr)

	// Setup command-line configurations
	port := flag.String("port", "8080", "Port to run the web server on")
	dataDir := flag.String("data", "./data", "Directory to persist wiki markdown files and assets")
	wikiName := flag.String("name", "NexWiki", "The custom name/title of your wiki displayed in the UI")
	flag.Parse()

	// Environment variable WIKI_NAME takes precedence over command line flag
	name := *wikiName
	if envName := os.Getenv("WIKI_NAME"); envName != "" {
		name = envName
	}

	log.Printf("Starting NexWiki backend...")
	log.Printf("Data directory: %s", *dataDir)
	log.Printf("Wiki Name/Title: %s", name)

	// Ensure storage is initialized
	storage, err := server.NewStorage(*dataDir)
	if err != nil {
		log.Fatalf("Fatal: failed to initialize storage: %v", err)
	}

	// Initialize server instance with configured name
	srv := server.NewServer(storage, name)

	// Spin up the stdio MCP JSON-RPC server in a background goroutine!
	go srv.StartMCPServer()

	// Create New Mux Router (Go 1.22+ supports methods and wildcards out-of-the-box!)
	mux := http.NewServeMux()

	// Register API endpoints
	mux.HandleFunc("/api/mcp", srv.HandleStreamableHTTP)
	mux.HandleFunc("GET /api/config", srv.HandleGetConfig)
	mux.HandleFunc("GET /api/search", srv.HandleSearchArticles)
	mux.HandleFunc("GET /api/articles", srv.HandleListArticles)
	mux.HandleFunc("GET /api/articles/{slug}", srv.HandleGetArticle)
	mux.HandleFunc("POST /api/articles", srv.HandleCreateArticle)
	mux.HandleFunc("PUT /api/articles/{slug}", srv.HandleUpdateArticle)
	mux.HandleFunc("DELETE /api/articles/{slug}", srv.HandleDeleteArticle)
	mux.HandleFunc("POST /api/articles/{slug}/assets", srv.HandleUploadAsset)
	mux.HandleFunc("GET /api/assets/{slug}/{filename}", srv.HandleGetAsset)

	// Create FS for React Frontend.
	// We check if "frontend/dist" exists as a physical directory on disk for dev mode live-reloading.
	// If it doesn't exist, we fall back to the embedded binary filesystem.
	var frontendFS fs.FS
	if info, err := os.Stat("frontend/dist"); err == nil && info.IsDir() {
		log.Println("Serving frontend assets from live disk (development mode)")
		frontendFS = os.DirFS("frontend/dist")
	} else {
		log.Println("Serving frontend assets from embedded filesystem (production mode)")
		subFS, err := fs.Sub(embeddedFrontend, "frontend/dist")
		if err != nil {
			log.Fatalf("Fatal: failed to open embedded files: %v", err)
		}
		frontendFS = subFS
	}

	// Dynamic SPA routing handler for static frontend files
	frontendHandler := &SPAFrontendHandler{
		staticFS: frontendFS,
		storage:  storage,
	}

	// Mount frontend handler to catch all other requests
	mux.Handle("/", frontendHandler)

	// Wrap server in CORS middleware for effortless multi-port local development
	handler := server.EnableCORS(mux)

	addr := fmt.Sprintf(":%s", *port)
	log.Printf("NexWiki web server is running on http://localhost%s", addr)

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("Fatal: server exited: %v", err)
	}
}

// SPAFrontendHandler serves static files from React build directory,
// falling back to index.html for direct loads of client routes.
type SPAFrontendHandler struct {
	staticFS fs.FS
	storage  *server.Storage
}

func (h *SPAFrontendHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Clean the requested filepath to avoid traversal
	path := filepath.Clean(r.URL.Path)

	// Strip leading slash
	filePath := strings.TrimPrefix(path, "/")
	if filePath == "" || filePath == "." {
		filePath = "index.html"
	}

	// Try to open the requested file on the frontend FS
	file, err := h.staticFS.Open(filePath)
	if err == nil {
		file.Close()
		// If it exists, let the standard FileServer handle serving it with proper headers
		http.FileServer(http.FS(h.staticFS)).ServeHTTP(w, r)
		return
	}

	// If file does not exist, serve index.html as fallback for React SPA Routing.
	// This enables direct bookmarks or page-reloads to work flawlessly!
	indexFile, err := h.staticFS.Open("index.html")
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "Error: index.html not found in static files. Please run 'npm run build' inside frontend directory first.")
		return
	}
	defer indexFile.Close()

	// Set content type header before writing the status code or body
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// If it is an article path, check if the article actually exists.
	// If it does not exist, return a 404 status code while still serving index.html.
	if strings.HasPrefix(path, "/articles/") {
		slug := strings.TrimPrefix(path, "/articles/")
		if _, err := h.storage.GetArticle(slug); err != nil {
			w.WriteHeader(http.StatusNotFound)
		}
	}

	// Serve the index file
	io.Copy(w, indexFile)
}

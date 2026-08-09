package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"nexwiki/server"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

//go:embed frontend/dist/*
var embeddedFrontend embed.FS

// Version is the current semantic version of NexWiki.
// In CI/CD pipelines, this value can be overwritten at link/build time
// using: go build -ldflags "-X main.Version=0.1.0"
var Version = "0.1.0"

func main() {
	// Force all log statements to print exclusively to Stderr!
	// This prevents logs from corrupting the Stdio MCP JSON-RPC communication on Stdout.
	log.SetOutput(os.Stderr)

	// Set up command-line configurations
	port := flag.String("port", "8080", "Port to run the web server on")
	dataDir := flag.String("data", "./data", "Directory to persist wiki markdown files and assets")
	wikiName := flag.String("name", "NexWiki", "The custom name/title of your wiki displayed in the UI")
	theme := flag.String("theme", "default", "The default theme of your wiki")
	themeScheduling := flag.Bool("theme-scheduling", false, "Enable opt-in seasonal theme scheduling auto-swaps")
	mcpOnly := flag.Bool("mcp-only", false, "Run as a pure stdio MCP server (skip the web port bind entirely)")
	bindAddr := flag.String("bind", "", "Network interface to bind (e.g. 127.0.0.1 to accept only local connections). Empty binds all interfaces")
	flag.Parse()

	// NEXWIKI_MCP_ONLY env overrides the flag (e.g., set in a Claude Desktop spawn config).
	mcpOnlyMode := *mcpOnly
	if envMCP := os.Getenv("NEXWIKI_MCP_ONLY"); envMCP != "" {
		mcpOnlyMode = envMCP == "true" || envMCP == "1"
	}

	// Environment variable NEXWIKI_NAME takes precedence over command line flag
	name := *wikiName
	if envName := os.Getenv("NEXWIKI_NAME"); envName != "" {
		name = envName
	}

	// Environment variable NEXWIKI_THEME takes precedence over command line flag
	defaultTheme := *theme
	if envTheme := os.Getenv("NEXWIKI_THEME"); envTheme != "" {
		defaultTheme = envTheme
	}

	themeSchedulingEnabled := *themeScheduling
	if envSched := os.Getenv("NEXWIKI_THEME_SCHEDULING"); envSched != "" {
		themeSchedulingEnabled = envSched == "true"
	}

	log.Printf("Starting NexWiki backend...")
	log.Printf("Data directory: %s", *dataDir)
	log.Printf("Wiki Name/Title: %s", name)
	log.Printf("Default Theme: %s", defaultTheme)
	log.Printf("Theme Scheduling Enabled: %t", themeSchedulingEnabled)

	// Probe for a running web primary before opening storage, so a lock conflict can be explained
	// rather than merely reported. Cheap (750 ms against loopback) and needed for the message below.
	primaryDetected := mcpOnlyMode && probeForPrimary(*port)

	// Ensure storage is initialized.
	//
	// This is bounded by a timeout because the Bleve index holds an exclusive bbolt file lock on
	// its directory: if another NexWiki process already has this data directory open, bleve.Open
	// blocks *forever* — no error, no timeout of its own. A `-mcp-only` sidecar pointed at a
	// running instance's data directory therefore used to hang silently at startup and never reach
	// the MCP loop, which is precisely the documented Claude Desktop stdio configuration.
	storage, err := openStorageWithTimeout(*dataDir, storageOpenTimeout)
	if err != nil {
		if errors.Is(err, errStorageLocked) && primaryDetected {
			log.Fatalf("Fatal: a NexWiki web server is already running on port %s and holds an exclusive "+
				"lock on the search index in %s.\n"+
				"A -mcp-only process cannot share a data directory with a running instance.\n"+
				"Connect over the Streamable HTTP transport instead — point your MCP client at "+
				"http://localhost:%s/api/mcp — or stop the web server before using stdio.",
				*port, *dataDir, *port)
		}
		if errors.Is(err, errStorageLocked) {
			log.Fatalf("Fatal: could not open the search index in %s within %s — another process is "+
				"holding it open.\nStop any other NexWiki process using this data directory, or pass "+
				"a different -data path.", *dataDir, storageOpenTimeout)
		}
		log.Fatalf("Fatal: failed to initialize storage: %v", err)
	}

	// Initialize EventBus for real-time pub-sub sync
	eventBus := server.NewEventBus()

	// Initialize server instance with configured name, theme, event bus, and scheduling settings
	srv := server.NewServer(storage, name, defaultTheme, themeSchedulingEnabled, eventBus, Version, *port)

	// A sidecar that got this far is using a *different* data directory than the primary (it would
	// have failed the lock check otherwise), so it forwards activity events rather than writing the
	// primary's log itself.
	if primaryDetected {
		srv.IsSecondaryProcess = true
		log.Printf("-mcp-only: web server detected on port %s; forwarding activity events to it.", *port)
	} else if mcpOnlyMode {
		log.Printf("-mcp-only: no web server detected; running standalone and persisting activity directly.")
	}

	// Persist activity events durably to data/activity.jsonl.
	// When running as a mcp-only sidecar alongside a web server, events are forwarded via HTTP
	// instead of written here — this prevents two processes writing the same file concurrently.
	var openActivityLog *server.ActivityLog
	if activityLog, err := server.OpenActivityLog(*dataDir); err != nil {
		log.Printf("Warning: activity log persistence disabled: %v", err)
	} else {
		openActivityLog = activityLog
		eventBus.SetPersist(func(ev server.LogEvent) {
			if !srv.IsSecondaryProcess {
				if err := activityLog.Append(ev); err != nil {
					log.Printf("Warning: failed to persist activity event: %v", err)
				}
			}
		})
	}

	// closeResources releases the Bleve index and the activity log file handle. Skipping this on
	// exit is what leaves the search index inconsistent after a `docker stop`, so every exit path
	// below routes through it.
	closeResources := func() {
		if openActivityLog != nil {
			if err := openActivityLog.Close(); err != nil {
				log.Printf("Warning: failed to close activity log: %v", err)
			}
		}
		if err := storage.Close(); err != nil {
			log.Printf("Warning: failed to close storage: %v", err)
		}
	}

	// In -mcp-only mode, run the stdio MCP server in the foreground and never bind the web port.
	if mcpOnlyMode {
		log.Printf("Running in stdio MCP-only mode (no web server). All MCP tools operate against the in-process storage layer.")
		srv.StartMCPServer() // blocks until stdin EOF
		closeResources()
		return
	}

	// Ensure the governance skill the MCP tool-description hooks reference actually exists,
	// so agents can load nexwiki-agent-guidelines out of the box. Idempotent.
	srv.SeedAgentGuidelinesIfMissing()

	// Spin up the stdio MCP JSON-RPC server in a background goroutine!
	go srv.StartMCPServer()

	// Create New Mux Router (Go 1.22+ supports methods and wildcards out-of-the-box!)
	mux := http.NewServeMux()

	// Register API endpoints
	mux.HandleFunc("/api/mcp", srv.HandleStreamableHTTP)
	mux.HandleFunc("GET /api/config", srv.HandleGetConfig)
	mux.HandleFunc("GET /api/status-tags", srv.HandleGetStatusTags)
	mux.HandleFunc("GET /api/themes", srv.HandleGetThemes)
	mux.HandleFunc("POST /api/themes", srv.HandleSaveTheme)
	mux.HandleFunc("DELETE /api/themes/{name}", srv.HandleDeleteTheme)
	mux.HandleFunc("GET /api/search", srv.HandleSearchArticles)
	mux.HandleFunc("GET /api/articles", srv.HandleListArticles)
	mux.HandleFunc("GET /api/articles/{slug}", srv.HandleGetArticle)
	mux.HandleFunc("POST /api/articles", srv.HandleCreateArticle)
	mux.HandleFunc("PUT /api/articles/{slug}", srv.HandleUpdateArticle)
	mux.HandleFunc("PUT /api/articles/{slug}/tags", srv.HandleUpdateArticleTags)
	mux.HandleFunc("DELETE /api/articles/{slug}", srv.HandleDeleteArticle)
	mux.HandleFunc("POST /api/articles/{slug}/assets", srv.HandleUploadAsset)
	mux.HandleFunc("GET /api/assets/{slug}/{filename}", srv.HandleGetAsset)
	mux.HandleFunc("GET /api/articles/{slug}/backlinks", srv.HandleGetBacklinks)
	mux.HandleFunc("GET /api/articles/{slug}/history", srv.HandleGetArticleHistory)
	mux.HandleFunc("GET /api/articles/{slug}/history/{version}", srv.HandleGetArticleVersion)
	mux.HandleFunc("POST /api/articles/{slug}/revert", srv.HandleRevertArticle)
	mux.HandleFunc("DELETE /api/tags/{tag}", srv.HandleDeleteTagGlobally)
	mux.HandleFunc("GET /api/activity/stream", srv.HandleActivityStream)
	mux.HandleFunc("GET /api/activity/log", srv.HandleGetActivityLog)
	mux.HandleFunc("POST /api/activity/log", srv.HandlePostActivityLog)
	mux.HandleFunc("GET /api/wiki/stats", srv.HandleGetWikiStats)
	mux.HandleFunc("GET /api/okf/export", srv.HandleExportOKFBundle)
	mux.HandleFunc("POST /api/okf/import", srv.HandleImportOKFBundle)

	// Register AI Skills registry endpoints
	mux.HandleFunc("GET /api/skills", srv.HandleListSkills)
	mux.HandleFunc("GET /api/skills/{slug}", srv.HandleGetSkill)
	mux.HandleFunc("GET /api/skills/{slug}/raw", srv.HandleGetSkillRaw)

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

	// Wrap server in CORS middleware for effortless multi-port local development,
	// and cap request body sizes so a single request cannot exhaust memory or disk.
	handler := server.EnableCORS(server.LimitRequestBodies(mux))

	// The MCP spec recommends local servers bind loopback only. That cannot be the default here:
	// inside a container, binding 127.0.0.1 makes the wiki unreachable from the host, which would
	// break every existing Docker deployment. So all interfaces stays the default and -bind (or
	// NEXWIKI_BIND) is the opt-in for people running the binary directly on a shared network.
	bindHost := *bindAddr
	if envBind := os.Getenv("NEXWIKI_BIND"); envBind != "" {
		bindHost = envBind
	}
	addr := fmt.Sprintf("%s:%s", bindHost, *port)

	// Explicit timeouts: the zero-value http.Server has none, leaving the process open to
	// Slowloris-style connection exhaustion. WriteTimeout stays 0 because /api/mcp and
	// /api/activity/stream are long-lived streams that a write deadline would sever.
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Shut down cleanly on SIGINT/SIGTERM (i.e. Ctrl-C and `docker stop`) so in-flight requests
	// finish and, critically, the Bleve index and activity log are closed rather than killed.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("Received %s: shutting down gracefully...", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			log.Printf("Warning: graceful shutdown timed out: %v", err)
		}
	}()

	log.Printf("NexWiki web server is running on http://localhost%s", addr)

	// Bind-or-halt: a normal launch IS the web server. If the port is already in use or
	// misconfigured, it halts rather than silently falling back. To run a stdio MCP server
	// alongside an already-running web server, use -mcp-only instead.
	err = httpServer.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		closeResources()
		log.Fatalf("Fatal: could not bind web server to %s: %v\nIf you intended to run a stdio MCP server alongside an existing web server, relaunch with the -mcp-only flag (or NEXWIKI_MCP_ONLY=true).", addr, err)
	}

	<-shutdownDone
	closeResources()
	log.Printf("NexWiki shut down cleanly.")
}

// storageOpenTimeout bounds how long startup waits for the search-index lock before concluding
// another process holds it. Generous enough for a cold index rebuild on slow disks, short enough
// that a stdio MCP client reports a failure instead of appearing to hang.
const storageOpenTimeout = 15 * time.Second

// errStorageLocked indicates the search index could not be opened within storageOpenTimeout,
// which in practice always means another process holds its exclusive lock.
var errStorageLocked = errors.New("search index is locked by another process")

// openStorageWithTimeout runs server.NewStorage under a deadline. bleve.Open blocks indefinitely
// on a contended index lock rather than returning an error, so the timeout is the only way to
// distinguish "still indexing" from "another process owns this directory". The goroutine is left
// running on timeout; every caller treats that as fatal and exits.
func openStorageWithTimeout(dataDir string, timeout time.Duration) (*server.Storage, error) {
	type result struct {
		storage *server.Storage
		err     error
	}
	done := make(chan result, 1)

	go func() {
		s, err := server.NewStorage(dataDir)
		done <- result{storage: s, err: err}
	}()

	select {
	case res := <-done:
		return res.storage, res.err
	case <-time.After(timeout):
		return nil, errStorageLocked
	}
}

// probeForPrimary reports whether a NexWiki web server is already running on the given port,
// by issuing a short GET /api/config against the loopback interface.
func probeForPrimary(port string) bool {
	if port == "" {
		port = "8080"
	}
	client := &http.Client{Timeout: 750 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/api/config", port))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// SPAFrontendHandler serves static files from the React build directory,
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
		_ = file.Close()
		// If it exists, let the standard FileServer handle serving it with proper headers
		http.FileServer(http.FS(h.staticFS)).ServeHTTP(w, r)
		return
	}

	// If the file does not exist, serve index.html as a fallback for React SPA Routing.
	// This enables direct bookmarks or page-reloads to work flawlessly!
	indexFile, err := h.staticFS.Open("index.html")
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(w, "Error: index.html not found in static files. Please run 'npm run build' inside frontend directory first.")
		return
	}
	defer func() { _ = indexFile.Close() }()

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
	_, _ = io.Copy(w, indexFile)
}

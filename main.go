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
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
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

// defaultDataDir returns the OS-aware default wiki content directory for
// bare-binary launches (no explicit -data flag).
//
//   - Windows: %AppData%\nexwiki\nexwiki-data (os.UserConfigDir), falling
//     back to %USERPROFILE%\.config\nexwiki\nexwiki-data, then ./data.
//   - Linux/macOS: $XDG_CONFIG_HOME/nexwiki/nexwiki-data when XDG_CONFIG_HOME
//     is an absolute path, else $HOME/.config/nexwiki/nexwiki-data, then ./data.
//
// macOS deliberately uses ~/.config (not ~/Library/Application Support) so the
// default matches Linux. Docker is unaffected: the image ENTRYPOINT always
// passes an explicit -data=/app/data which overrides this default.
func defaultDataDir() string {
	if runtime.GOOS == "windows" {
		cfgDir, _ := os.UserConfigDir()
		var home string
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
		return resolveDefaultDataDir(runtime.GOOS, "", cfgDir, home)
	}
	var home string
	if h, err := os.UserHomeDir(); err == nil {
		home = h
	}
	return resolveDefaultDataDir(runtime.GOOS, os.Getenv("XDG_CONFIG_HOME"), "", home)
}

// resolveDefaultDataDir is the pure, testable core of defaultDataDir. Empty
// strings mean "unavailable" (unset env, lookup error). cfgDir is only
// consulted on Windows; xdg is only consulted on non-Windows.
func resolveDefaultDataDir(goos, xdg, cfgDir, home string) string {
	if goos == "windows" {
		if cfgDir != "" {
			return filepath.Join(cfgDir, "nexwiki", "nexwiki-data")
		}
		if home != "" {
			return filepath.Join(home, ".config", "nexwiki", "nexwiki-data")
		}
		return "./data"
	}
	if xdg != "" && filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "nexwiki", "nexwiki-data")
	}
	if home != "" {
		return filepath.Join(home, ".config", "nexwiki", "nexwiki-data")
	}
	return "./data"
}

func main() {
	// Force all log statements to print exclusively to Stderr!
	// This prevents logs from corrupting the Stdio MCP JSON-RPC communication on Stdout.
	log.SetOutput(os.Stderr)

	// Set up command-line configurations
	port := flag.String("port", "5808", "Port to run the web server on")
	dataDir := flag.String("data", defaultDataDir(), "Directory to persist wiki markdown files and assets")
	wikiName := flag.String("name", "NexWiki", "The custom name/title of your wiki displayed in the UI")
	theme := flag.String("theme", "default", "The default theme of your wiki")
	themeScheduling := flag.Bool("theme-scheduling", false, "Enable opt-in seasonal theme scheduling auto-swaps")
	mcpOnly := flag.Bool("mcp-only", false, "Run as a pure stdio MCP server (skip the web port bind entirely)")
	launchBrowser := flag.Bool("launch-in-browser", false, "Open the wiki URL in the system default web browser on startup")
	bindAddr := flag.String("bind", "", "Network interface to bind (default: 127.0.0.1 for native local security; all interfaces in containers). Set to 0.0.0.0 or NEXWIKI_BIND to bind all interfaces")
	agentName := flag.String("agent-name", "", "Fallback attribution recorded in the activity log for MCP clients that do not identify themselves. Clients that send MCP clientInfo are credited by their own name regardless of this")
	wikiskillRole := flag.String("wikiskill-role", "", "WikiSkill evolution least-privilege role for the MCP layer: inference, maintainer, or proposer. Empty (default) is unrestricted. Can also be set via NEXWIKI_WIKISKILL_ROLE, which takes precedence")
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

	// Environment variable NEXWIKI_LAUNCH_BROWSER takes precedence over the flag.
	launchInBrowser := *launchBrowser
	if envLB := os.Getenv("NEXWIKI_LAUNCH_BROWSER"); envLB != "" {
		launchInBrowser = envLB == "true" || envLB == "1"
	}

	// Fail fast on an invalid headless job-runner configuration (wikiskill story 04):
	// BYO CLI profiles are allowlisted command templates from server config only
	// (NEXWIKI_JOB_PROFILES_FILE / NEXWIKI_JOB_* env). An arbitrary template refuses
	// the whole process at startup rather than failing one job later.
	if err := server.ValidateJobRunnerConfig(); err != nil {
		log.Fatalf("Fatal: %v", err)
	}

	log.Printf("Starting NexWiki backend...")
	log.Printf("Data directory: %s", *dataDir)
	log.Printf("Wiki Name/Title: %s", name)
	log.Printf("Default Theme: %s", defaultTheme)
	log.Printf("Theme Scheduling Enabled: %t", themeSchedulingEnabled)

	// Probe for a running web primary before opening storage. Only one process can own a wiki —
	// the Bleve index holds an exclusive lock — so a sidecar pointed at a running instance must
	// not try to open it at all.
	primaryDetected := mcpOnlyMode && probeForPrimary(*port)

	// Proxy mode: forward stdio to the running primary instead of opening the data directory.
	//
	// This is what makes the documented Claude Desktop stdio configuration actually work. It used
	// to hang forever on the index lock; then it failed fast with an explanation, which was honest
	// but still left the setup unusable. Now the sidecar is a pipe: the primary owns the wiki and
	// answers every call, including subscription streams relayed back as stdio notifications.
	if primaryDetected {
		log.Printf("-mcp-only: web server detected on port %s; running as a proxy to it. "+
			"The primary owns the data directory; this process forwards MCP traffic to it.", *port)
		server.NewMCPProxy(*port, os.Stdout).Run(os.Stdin)
		return
	}

	// Ensure storage is initialized.
	//
	// The index-lock deadline lives inside NewStorage and covers only the Bleve open, which is the
	// one step that can block forever. It used to wrap this whole call, which put seeding, the
	// one-time migration, and the boot index sync on the same 15-second budget — a migration that
	// legitimately took longer was killed and reported as a lock conflict.
	storage, err := server.NewStorage(*dataDir)
	if err != nil {
		// A primary on the configured port is handled above by proxying, so reaching here with a
		// locked index means some other process owns the directory — a second instance on a
		// different port, or a stale one that never shut down.
		if errors.Is(err, server.ErrSearchIndexLocked) {
			log.Fatalf("Fatal: could not open the search index in %s within %s — another process is "+
				"holding it open.\nStop any other NexWiki process using this data directory, or pass "+
				"a different -data path.", *dataDir, server.IndexOpenTimeout)
		}
		log.Fatalf("Fatal: failed to initialize storage: %v", err)
	}

	// Initialize EventBus for real-time pub-sub sync
	eventBus := server.NewEventBus()

	// Initialize server instance with configured name, theme, event bus, and scheduling settings
	srv := server.NewServer(storage, name, defaultTheme, themeSchedulingEnabled, eventBus, Version, *port)
	// Attribution fallback for MCP callers that send no clientInfo. Deliberately NOT `name`:
	// NEXWIKI_NAME is the wiki's display title, and using it here is the defect this fixes.
	srv.AgentName = server.ResolveConfiguredAgentName(*agentName)
	// Process-wide WikiSkill evolution role (story 02 least-privilege). Empty is
	// unrestricted. An unrecognized non-empty value normalizes to unrestricted with a
	// warning rather than failing startup, so a typo cannot take a wiki offline.
	srv.WikiskillRole = server.ResolveWikiskillRole(*wikiskillRole)
	if strings.TrimSpace(*wikiskillRole) != "" || strings.TrimSpace(os.Getenv(server.WikiskillRoleEnv)) != "" {
		if srv.WikiskillRole == "" {
			log.Printf("Warning: unrecognized %s value; running with unrestricted MCP tool access", server.WikiskillRoleEnv)
		} else {
			log.Printf("WikiSkill evolution role: %s (MCP least-privilege enforced)", srv.WikiskillRole)
		}
	}

	// Any process reaching here owns its data directory outright: a detected primary would have
	// been proxied to above, so there is no secondary to forward activity events from.
	if mcpOnlyMode {
		log.Printf("-mcp-only: no web server detected; running standalone and persisting activity directly.")
	}

	// Persist activity events durably to data/activity.jsonl. Whichever process reaches here owns
	// the data directory outright, so it is the only writer: a sidecar alongside a primary proxies
	// instead, and the proxied call is logged by the primary that actually executes it.
	var openActivityLog *server.ActivityLog
	if activityLog, err := server.OpenActivityLog(*dataDir); err != nil {
		log.Printf("Warning: activity log persistence disabled: %v", err)
	} else {
		openActivityLog = activityLog
		eventBus.SetPersist(func(ev server.LogEvent) {
			if err := activityLog.Append(ev); err != nil {
				log.Printf("Warning: failed to persist activity event: %v", err)
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

	// Plan lifecycle worker: archives finished plans and deletes long-archived ones on a timer.
	// Primary-only by construction — this code path is only reached by the web primary (the
	// -mcp-only branch returned above), which is the one process that owns the data directory.
	// A sidecar must never run a second sweep over the same files.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	go (&server.PlanLifecycleWorker{
		Storage: storage,
		Bus:     eventBus,
		Cfg:     server.LoadPlanLifecycleConfig(),
	}).Run(workerCtx)

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
	mux.HandleFunc("POST /api/articles/{slug}/verify", srv.HandleVerifyArticle)
	mux.HandleFunc("DELETE /api/tags/{tag}", srv.HandleDeleteTagGlobally)
	mux.HandleFunc("GET /api/activity/stream", srv.HandleActivityStream)
	mux.HandleFunc("GET /api/activity/log", srv.HandleGetActivityLog)
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

	// Bind host resolution: native desktop execution defaults to binding loopback (127.0.0.1)
	// to avoid accidental exposure on shared LAN/Wi-Fi networks. In containerized environments,
	// it defaults to all interfaces ("") so container port mapping functions properly.
	// Users can explicitly configure binding via -bind flag or NEXWIKI_BIND environment variable.
	bindHost := resolveBindHost(*bindAddr, os.Getenv("NEXWIKI_BIND"), isRunningInContainer())
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

		// Tell open response streams to end first. http.Server.Shutdown waits for connections to
		// go *idle*, and an SSE stream never does — a single browser tab on the wiki would
		// otherwise hold shutdown open until the deadline.
		srv.BeginShutdown()
		stopWorker()

		// The deadline sits below a container runtime's default 10s stop grace (docker stop,
		// Kubernetes terminationGracePeriodSeconds) on purpose: if shutdown overruns it, the
		// supervisor SIGKILLs the process before closeResources() can close the search index,
		// which is the corruption this whole path exists to avoid.
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			log.Printf("Warning: graceful shutdown timed out: %v", err)
		}
	}()

	// The banner has to name a host someone can paste into a browser. addr is "host:port", so
	// concatenating it onto "http://localhost" only reads correctly when -bind is unset and the
	// host half is empty: with -bind 127.0.0.1 it printed "http://localhost127.0.0.1:8137".
	displayHost := bindHost
	if displayHost == "" || displayHost == "0.0.0.0" {
		displayHost = "localhost" // all interfaces: localhost is the address that works locally
	}
	log.Printf("NexWiki web server is running on http://%s:%s", displayHost, *port)

	// Open the wiki in the user's browser when requested. The opener runs in a
	// goroutine that waits until the server answers /api/config, so the tab
	// does not land on a connection-refused page on slow disks (first-run
	// index build). Never fatal: a headless box simply logs a warning.
	// -mcp-only never reaches here (it returns above), so there is no need to
	// guard against the headless stdio mode.
	if launchInBrowser {
		go waitForServerAndOpenBrowser(probeHost(bindHost), *port, buildAppURL(displayHost, *port))
	}

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

// shutdownTimeout bounds graceful shutdown. Deliberately under the 10s stop grace period a
// container runtime allows by default, so the index is always closed before any SIGKILL.
const shutdownTimeout = 5 * time.Second

// buildAppURL assembles the pasteable/openable wiki URL from the display
// host (bind interface, or "localhost" when bound to all interfaces) and port.
func buildAppURL(displayHost, port string) string {
	return fmt.Sprintf("http://%s:%s", displayHost, port)
}

// probeHost picks the address waitForServerAndOpenBrowser polls. The server
// may be bound to a specific interface, so probe that interface — except the
// wildcard binds, which are not dialable and fall back to loopback.
func probeHost(bindHost string) string {
	if bindHost == "" || bindHost == "0.0.0.0" || bindHost == "::" {
		return "127.0.0.1"
	}
	return bindHost
}

// waitForServerAndOpenBrowser polls /api/config until the server answers (or
// a deadline passes), then opens appURL in the system default browser.
// Failures are stderr warnings only — a headless machine must still serve.
func waitForServerAndOpenBrowser(host, port, appURL string) {
	waitForServer(host, port, 15*time.Second)
	if err := openBrowser(appURL); err != nil {
		log.Printf("Warning: -launch-in-browser could not open %s: %v", appURL, err)
	}
}

// waitForServer polls http://host:port/api/config until it answers 200 or
// timeout elapses. Pure polling, no side effects — safe to unit test.
func waitForServer(host, port string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 750 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("http://%s:%s/api/config", host, port))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// isRunningInContainer detects whether the process is executing inside a container
// (such as Docker, Podman, or Kubernetes).
func isRunningInContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	if os.Getenv("CONTAINER") != "" {
		return true
	}
	return false
}

// openBrowser opens url in the system default web browser using only stdlib
// process spawning (no new dependencies, keeps the CGO_ENABLED=0 static build).
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		if _, err := exec.LookPath("xdg-open"); err == nil {
			cmd = exec.Command("xdg-open", url)
		} else if _, err := exec.LookPath("sensible-browser"); err == nil {
			cmd = exec.Command("sensible-browser", url)
		} else if _, err := exec.LookPath("wslview"); err == nil {
			cmd = exec.Command("wslview", url)
		} else {
			return fmt.Errorf("no browser opener found (xdg-open, sensible-browser, wslview)")
		}
	}
	return cmd.Start()
}

// resolveBindHost determines the host interface to bind to. Native desktop execution
// defaults to loopback (127.0.0.1) for security, while containerized environments
// default to all interfaces ("") to allow container port mapping.
func resolveBindHost(flagBind string, envBind string, inContainer bool) string {
	if flagBind != "" {
		return flagBind
	}
	if envBind != "" {
		return envBind
	}
	if inContainer {
		return ""
	}
	return "127.0.0.1"
}

// probeForPrimary reports whether a NexWiki web server is already running on the given port,
// by issuing a short GET /api/config against the loopback interface.
func probeForPrimary(port string) bool {
	if port == "" {
		port = "5808"
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

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
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"nexwiki/server"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	bindAddr := flag.String("bind", "", "Network interface to bind, also set by NEXWIKI_BIND (default: 127.0.0.1 for native local security; all interfaces in containers). Use 0.0.0.0 or :: to bind all interfaces. With -mcp-only, the address to look for a running web server on when it is bound to a specific address")
	agentName := flag.String("agent-name", "", "Fallback name credited in the activity log for MCP calls whose client is not identified")
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

	log.Printf("Starting NexWiki backend...")
	log.Printf("Data directory: %s", *dataDir)
	log.Printf("Wiki Name/Title: %s", name)
	log.Printf("Default Theme: %s", defaultTheme)
	log.Printf("Theme Scheduling Enabled: %t", themeSchedulingEnabled)

	// Bind host resolution: native desktop execution defaults to binding loopback (127.0.0.1)
	// to avoid accidental exposure on shared LAN/Wi-Fi networks. In containerized environments,
	// it defaults to all interfaces ("") so container port mapping functions properly.
	// Users can explicitly configure binding via -bind flag or NEXWIKI_BIND environment variable.
	//
	// Resolved in -mcp-only mode too, which binds nothing but uses the host to find a web primary.
	bindHost := resolveBindHost(*bindAddr, os.Getenv("NEXWIKI_BIND"), isRunningInContainer())

	// Probe for a running web primary before opening storage. Only one process can own a wiki —
	// the Bleve index holds an exclusive lock — so a sidecar pointed at a running instance must
	// not try to open it at all.
	var (
		primaryHost     string
		primaryDetected bool
	)
	if mcpOnlyMode {
		var hint string
		primaryHost, primaryDetected, hint = findPrimary(primaryProbeHosts(bindHost), *port, primaryProbeTimeout)
		if hint != "" {
			log.Print(hint)
		}
	}

	// Proxy mode: forward stdio to the running primary instead of opening the data directory.
	//
	// This is what makes the documented Claude Desktop stdio configuration actually work. It used
	// to hang forever on the index lock; then it failed fast with an explanation, which was honest
	// but still left the setup unusable. Now the sidecar is a pipe: the primary owns the wiki and
	// answers every call, including subscription streams relayed back as stdio notifications.
	if primaryDetected {
		log.Printf("-mcp-only: web server detected at %s; running as a proxy to it. "+
			"The primary owns the data directory; this process forwards MCP traffic to it.", httpURL(primaryHost, *port, ""))
		// Proxied to the host that answered the probe, which is where the primary is listening.
		server.NewMCPProxy(httpURL(primaryHost, *port, "/api/mcp"), server.ResolveConfiguredAgentName(*agentName), os.Stdout).Run(os.Stdin)
		return
	}

	// From here this process opens the data directory itself, so SIGINT and SIGTERM are caught before
	// it does. NewStorage cannot be interrupted, and the default action would kill it partway through
	// its migration or first index build with the search index open. A signal that arrives before
	// NewStorage starts ends the launch without opening storage; one that arrives while it runs is
	// acknowledged at once and acted on as soon as it returns.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Any process reaching here owns its data directory outright: a detected primary would have
	// been proxied to above, so there is no secondary to forward activity events from.
	if mcpOnlyMode {
		log.Printf("-mcp-only: no web server detected; running standalone and persisting activity directly.")
	}

	// A normal launch is the web server, so what it needs that can fail without touching the data
	// directory comes first: the frontend assets, then the web port. A launch that cannot serve then
	// exits having created, seeded, and locked nothing. The usual cause is a stdio client config
	// missing -mcp-only while a web server already holds the port, and it used to create and seed a
	// whole data directory, often the default one, on its way to the bind error.
	var (
		frontendFS  fs.FS
		webListener net.Listener
		addr        string
	)
	if !mcpOnlyMode {
		var err error
		if frontendFS, err = loadFrontendFS(); err != nil {
			log.Fatalf("Fatal: failed to open embedded files: %v", err)
		}

		addr = listenAddr(bindHost, *port)

		// Bind-or-halt: a normal launch IS the web server. If the port is already in use or
		// misconfigured, it halts rather than silently falling back. To run a stdio MCP server
		// alongside an already-running web server, use -mcp-only instead.
		//
		// Listening is not serving: until httpServer.Serve runs, after storage is open and seeded,
		// connections only wait in the kernel's listen backlog and no request is read. A client that
		// connects meanwhile is answered once storage is ready; one that gives up first, like
		// findPrimary with its short timeout, concludes what a refused connection told it before:
		// no server yet.
		if webListener, err = net.Listen("tcp", addr); err != nil {
			log.Fatalf("Fatal: could not bind web server to %s: %v\nIf you intended to run a stdio MCP server alongside an existing web server, relaunch with the -mcp-only flag (or NEXWIKI_MCP_ONLY=true).", addr, err)
		}
	}

	beforeOpenStorage(sigCh) // a no-op outside tests

	// A signal that arrived while the frontend loaded or the port was bound ends the launch here, so
	// it leaves the data directory as it found it, uncreated if it did not exist.
	select {
	case sig := <-sigCh:
		log.Printf("Received %s before opening storage: exiting without opening it.", sig)
		if webListener != nil {
			_ = webListener.Close()
		}
		return
	default:
	}

	// NewStorage can run for a while: up to IndexOpenTimeout waiting for the index lock, and longer
	// for a migration or first index build. So a signal meanwhile is acknowledged as it arrives rather
	// than looking ignored, and the first is kept for handling once storage is open. A repeat cannot
	// kill the process, since signal.Notify has replaced the default action; it is only logged.
	var (
		openSignal   os.Signal
		openSignalAt time.Time
	)
	opened := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		for {
			select {
			case sig := <-sigCh:
				if openSignal != nil {
					log.Printf("Received %s while opening storage; shutdown is already pending and begins as soon as it finishes opening.", sig)
					continue
				}
				openSignal, openSignalAt = sig, time.Now()
				log.Printf("Received %s while opening storage; will shut down as soon as it finishes opening.", sig)
			case <-opened:
				return
			}
		}
	}()

	// Ensure storage is initialized.
	//
	// The index-lock deadline lives inside NewStorage and covers only the Bleve open, which is the
	// one step that can block forever. It used to wrap this whole call, which put seeding, the
	// one-time migration, and the boot index sync on the same 15-second budget — a migration that
	// legitimately took longer was killed and reported as a lock conflict.
	//
	// Logged first so a long lock wait or first index build reads as in progress, not as a hang.
	log.Printf("Opening storage...")
	storage, err := server.NewStorage(*dataDir)
	close(opened)
	// Once the watcher has returned, openSignal is safe to read, and a later signal waits in sigCh
	// for the mode's own shutdown path below.
	<-watchDone
	if err != nil {
		// Release the port before exiting: this process will never serve it.
		if webListener != nil {
			_ = webListener.Close()
		}
		// A primary on the configured port is handled above, proxied to in -mcp-only mode and
		// refused by the bind otherwise, so reaching here with a locked index means some other
		// process owns the directory — a second instance on a different port, or a stale one that
		// never shut down.
		if errors.Is(err, server.ErrSearchIndexLocked) {
			log.Fatalf("Fatal: could not open the search index in %s within %s — another process is "+
				"holding it open.\nStop any other NexWiki process using this data directory, or pass "+
				"a different -data path.", *dataDir, server.IndexOpenTimeout)
		}
		log.Fatalf("Fatal: failed to initialize storage: %v", err)
	}

	// A signal that arrived while storage was opening is acted on now, before anything else starts or
	// writes, so storage is all there is to close. Its deadline counts from the signal, as the stop
	// grace of whatever sent it does, not from the open finishing; see openSignalClose.
	if openSignal != nil {
		if webListener != nil {
			_ = webListener.Close()
		}
		log.Print(closeAfterOpenSignal(storage.CloseContext, openSignalAt, time.Now()))
		return
	}

	// Initialize EventBus for real-time pub-sub sync
	eventBus := server.NewEventBus()

	// Initialize server instance with configured name, theme, event bus, and scheduling settings
	srv := server.NewServer(storage, name, defaultTheme, themeSchedulingEnabled, eventBus, Version, *port)
	// Attribution fallback for MCP callers that send no clientInfo. Deliberately NOT `name`:
	// NEXWIKI_NAME is the wiki's display title, and using it here is the defect this fixes.
	srv.AgentName = server.ResolveConfiguredAgentName(*agentName)

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
	// below routes through it. ctx bounds both closes; see shutdownContexts.
	//
	// Storage closes first, for two reasons. A write that finishes while storage waits for it
	// publishes its activity event after releasing writeMu, so closing the log afterwards makes that
	// event more likely to be recorded, though not certain. And the log's Close waits on its lock with
	// no deadline, so it must not run ahead of the index close, where a stuck append could keep the
	// index open. It is bounded by ctx too: if storage used the whole deadline, the log is left open
	// and the process exits without closing it.
	closeResources := func(ctx context.Context) {
		if err := storage.CloseContext(ctx); err != nil {
			log.Printf("Warning: failed to close storage: %v", err)
		}
		if openActivityLog != nil {
			closed := make(chan error, 1)
			go func() { closed <- openActivityLog.Close() }()
			select {
			case err := <-closed:
				if err != nil {
					log.Printf("Warning: failed to close activity log: %v", err)
				}
			case <-ctx.Done():
				// Appends are unbuffered, so every event already appended is on disk regardless.
				log.Printf("Warning: exiting without waiting for the activity log to close: %v", ctx.Err())
			}
		}
	}

	stdioServer := server.NewStdioMCPServer(srv, os.Stdin, os.Stdout)
	// stopStdio stops the stdio loop dispatching, waiting for a request in progress, before storage
	// closes: a request it dispatched afterwards would write to closed storage.
	stopStdio := func(ctx context.Context) {
		if err := stdioServer.Stop(ctx); err != nil {
			log.Printf("Warning: a stdio MCP request was still running at the shutdown deadline: %v", err)
		}
	}

	// Ensure the governance skill the MCP tool-description hooks reference actually exists,
	// so agents can load nexwiki-agent-guidelines out of the box. Idempotent.
	//
	// Before either mode serves anything, because the server's instructions tell agents to load it
	// first. A standalone -mcp-only process owns its data directory just as the web server does, so
	// it seeds too; a proxy never gets here, and its primary has seeded already.
	srv.SeedAgentGuidelinesIfMissing()

	// In -mcp-only mode, run the stdio MCP server in the foreground and never bind the web port.
	if mcpOnlyMode {
		log.Printf("Running in stdio MCP-only mode (no web server). All MCP tools operate against the in-process storage layer.")
		served := make(chan struct{})
		go func() {
			defer close(served)
			stdioServer.Serve() // returns at stdin EOF
		}()

		// A signal can arrive mid-request, so it gets the web server's bounded drain rather than the
		// default of dying with the search index open. Stdin EOF is the normal end.
		select {
		case <-served:
		case sig := <-sigCh:
			log.Printf("Received %s: shutting down gracefully...", sig)
		}
		stopCtx, closeCtx, cancel := shutdownContexts()
		defer cancel()
		stopStdio(stopCtx) // returns at once after EOF: Serve has returned, so nothing is in progress
		closeResources(closeCtx)
		return
	}

	// Plan lifecycle worker: archives finished plans and deletes long-archived ones on a timer.
	// Primary-only by construction — this code path is only reached by the web primary (the
	// -mcp-only branch returned above), which is the one process that owns the data directory.
	// A sidecar must never run a second sweep over the same files.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		(&server.PlanLifecycleWorker{
			Storage: storage,
			Bus:     eventBus,
			Cfg:     server.LoadPlanLifecycleConfig(),
		}).Run(workerCtx)
	}()

	// Spin up the stdio MCP JSON-RPC server in a background goroutine!
	go stdioServer.Serve()

	// stopWriters stops the writers besides the HTTP server (the plan lifecycle worker and the stdio
	// loop) and waits within ctx for each to finish what it is doing. One still running at the
	// deadline is logged and left behind: storage turns its later writes away once it closes.
	stopWriters := func(ctx context.Context) {
		stopWorker()
		stdioStopped := make(chan struct{})
		go func() {
			defer close(stdioStopped)
			stopStdio(ctx)
		}()
		select {
		case <-workerDone:
		case <-ctx.Done():
			log.Printf("Warning: the plan lifecycle worker was still running at the shutdown deadline: %v", ctx.Err())
		}
		<-stdioStopped
	}

	// Create New Mux Router (Go 1.22+ supports methods and wildcards out-of-the-box!)
	mux := http.NewServeMux()

	// Register API endpoints
	mux.HandleFunc(server.MCPEndpointPath, srv.HandleStreamableHTTP)
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

	// Dynamic SPA routing handler for static frontend files, loaded before storage opened
	frontendHandler := &SPAFrontendHandler{
		staticFS: frontendFS,
		storage:  storage,
	}

	// Mount frontend handler to catch all other requests
	mux.Handle("/", frontendHandler)

	// Wrap server in CORS middleware for effortless multi-port local development,
	// and cap request body sizes so a single request cannot exhaust memory or disk.
	handler := server.EnableCORS(server.LimitRequestBodies(mux), server.WithBindHost(bindHost))

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

	// writersOverran and closeOverran record which shutdown deadline passed (see shutdownContexts).
	// Set before shutdown returns.
	writersOverran, closeOverran := false, false
	var shutdownOnce sync.Once
	// shutdown drains in-flight requests and every other writer, then closes the Bleve index and
	// activity log rather than leaving them to be killed. Once only: a signal can race the web server
	// failing, and the second caller waits for the first to finish.
	shutdown := func() {
		shutdownOnce.Do(func() {
			// Tell open response streams to end first. http.Server.Shutdown waits for connections to
			// go *idle*, and an SSE stream never does — a single browser tab on the wiki would
			// otherwise hold shutdown open until the deadline.
			srv.BeginShutdown()

			stopCtx, closeCtx, cancel := shutdownContexts()
			defer cancel()

			// Every writer stops before storage closes, so none can write to a closed index. The HTTP
			// server drains alongside the others rather than before them, so each gets the whole
			// deadline instead of what the one before it left.
			writersStopped := make(chan struct{})
			go func() {
				defer close(writersStopped)
				stopWriters(stopCtx)
			}()
			if err := httpServer.Shutdown(stopCtx); err != nil {
				log.Printf("Warning: graceful shutdown timed out: %v", err)
			}
			<-writersStopped
			// Checked here, not at the end: the writers' deadline also passes while storage closes.
			writersOverran = stopCtx.Err() != nil
			closeResources(closeCtx)
			closeOverran = closeCtx.Err() != nil
		})
	}

	// Shut down cleanly on SIGINT/SIGTERM (i.e. Ctrl-C and `docker stop`).
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		sig := <-sigCh
		log.Printf("Received %s: shutting down gracefully...", sig)
		shutdown()
	}()

	// The banner has to name a URL someone can paste into a browser, which formatting it by hand got
	// wrong: with -bind 127.0.0.1 it printed "http://localhost127.0.0.1:8137", and an IPv6 host needs
	// brackets. See displayHost and buildAppURL.
	appURL := buildAppURL(displayHost(bindHost), *port)
	log.Printf("NexWiki web server is running on %s", appURL)

	// Open the wiki in the user's browser when requested. Storage is ready by now, but requests are
	// only read once Serve below starts accepting, so the opener runs in a goroutine that waits until
	// the server answers /api/config: the tab never opens ahead of a serving wiki, or at all if Serve
	// fails. Never fatal: a headless box simply logs a warning. -mcp-only never reaches here (it
	// returns above), so there is no need to guard against the headless stdio mode.
	if launchInBrowser {
		go waitForServerAndOpenBrowser(probeHost(bindHost), *port, appURL)
	}

	// The port was bound before storage opened, so this is where accepting begins, and an error here
	// is the listener failing, not the bind. If a signal has already begun shutdown, Serve returns
	// ErrServerClosed at once and closes the listener.
	err = httpServer.Serve(webListener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		// Requests already accepted, the worker, and the stdio loop may all be mid-write.
		shutdown()
		log.Fatalf("Fatal: web server on %s stopped: %v", addr, err)
	}

	<-shutdownDone // the shutdown goroutine closes resources before it finishes
	log.Print(shutdownSummary(writersOverran, closeOverran, false))
}

// shutdownTimeout bounds graceful shutdown, from the signal to the search index closing.
// Deliberately under the 10s stop grace period a container runtime allows by default (docker stop,
// Kubernetes terminationGracePeriodSeconds): if shutdown overran it, the supervisor would SIGKILL
// the process before the index is closed, which is the corruption this whole path exists to avoid.
const shutdownTimeout = 5 * time.Second

// storageCloseReserve is the part of shutdownTimeout held back for closing storage, so writers that
// use all of their time to stop still leave storage time to wait for a write in progress.
const storageCloseReserve = 1 * time.Second

// shutdownContexts returns the deadlines one shutdown runs under, both counted from now: stopCtx for
// stopping the writers (the HTTP server, the plan lifecycle worker, the stdio loop), and closeCtx,
// storageCloseReserve later, for closing storage. cancel releases both.
func shutdownContexts() (stopCtx, closeCtx context.Context, cancel context.CancelFunc) {
	deadline := time.Now().Add(shutdownTimeout)
	closeCtx, cancelClose := context.WithDeadline(context.Background(), deadline)
	stopCtx, cancelStop := context.WithDeadline(closeCtx, deadline.Add(-storageCloseReserve))
	return stopCtx, closeCtx, func() {
		cancelStop()
		cancelClose()
	}
}

// closeAfterOpenSignal closes storage that finished opening at openedAt after a signal received at
// receivedAt, under the deadline openSignalClose gives, and returns the shutdown summary to log last.
func closeAfterOpenSignal(closeStorage func(context.Context) error, receivedAt, openedAt time.Time) string {
	deadline, deadlinePassed, logLine := openSignalClose(receivedAt, openedAt)
	log.Print(logLine)
	closeCtx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if err := closeStorage(closeCtx); err != nil {
		log.Printf("Warning: failed to close storage: %v", err)
	}
	return shutdownSummary(false, closeCtx.Err() != nil, deadlinePassed)
}

// openSignalClose returns the deadline for closing storage that finished opening at openedAt after a
// signal received at receivedAt, whether the shutdown deadline had already passed by then, and the
// line to log about it, which says when the minimum below replaced the usual deadline and why.
//
// It is shutdownTimeout from the signal, like any shutdown's deadline, because a container runtime
// counts its stop grace (10s by default) from sending the signal, however long the open kept the
// process from acting on it. But the close always gets at least storageCloseReserve, the least any
// shutdown leaves storage: an open that ran past the budget would otherwise get a deadline already
// passed, and close the index without waiting at all for the temp file sweep NewStorage just
// started. That is a limit, not a delay, since the close returns as soon as the sweep stops, which
// it does between directories. One second keeps even a close that needs all of it inside the stop
// grace for an open that finished within 9s of the signal. A longer minimum would only hold the index
// close back behind a sweep stuck on a slow directory, closer to the SIGKILL that ends the grace.
func openSignalClose(receivedAt, openedAt time.Time) (deadline time.Time, deadlinePassed bool, logLine string) {
	deadline = receivedAt.Add(shutdownTimeout)
	minimum := openedAt.Add(storageCloseReserve)
	if !deadline.Before(minimum) {
		return deadline, false, "Storage finished opening: shutting down gracefully..."
	}
	// Decided from the exact times. The figure shown is rounded up to the millisecond, which keeps it
	// on the same side of both boundaries, whole seconds, as the time it stands for: an open 3ms past
	// the deadline reads "5.003s", never "5s after the signal, past the 5s shutdown deadline".
	took := openedAt.Sub(receivedAt)
	if shown := took.Truncate(time.Millisecond); shown < took {
		took = shown + time.Millisecond
	}
	if openedAt.After(deadline) {
		return minimum, true, fmt.Sprintf("Storage finished opening %s after the signal, past the %s shutdown deadline: allowing up to %s to close it...",
			took, shutdownTimeout, storageCloseReserve)
	}
	return minimum, false, fmt.Sprintf("Storage finished opening %s after the signal, less than %s before the %s shutdown deadline: allowing up to %s to close it...",
		took, storageCloseReserve, shutdownTimeout, storageCloseReserve)
}

// beforeOpenStorage runs just before main checks sigCh and opens storage. It does nothing outside
// tests, which use it to hold startup until a signal is waiting in sigCh.
var beforeOpenStorage = func(chan os.Signal) {}

// shutdownSummary is the last line a web server shutdown logs, as is any shutdown that begins while
// storage is opening. writersOverran means the writers' deadline passed before they all stopped,
// which each overrunning writer logs a warning about. closeOverran means the close's deadline had
// passed once storage and the activity log were closed; that includes a slow search index close,
// which logs no warning of its own. That deadline is the overall shutdown deadline, except when
// deadlinePassedBeforeClose: storage finished opening after a signal only once the shutdown deadline
// had passed, so closing it got storageCloseReserve instead (see openSignalClose), and what ran out is
// that allowance.
func shutdownSummary(writersOverran, closeOverran, deadlinePassedBeforeClose bool) string {
	switch {
	case closeOverran && deadlinePassedBeforeClose:
		return fmt.Sprintf("NexWiki shut down after the %s allowed for closing storage ran out; its %s shutdown deadline had already passed while storage was opening. See any warnings above.",
			storageCloseReserve, shutdownTimeout)
	case closeOverran:
		return fmt.Sprintf("NexWiki shut down after its %s shutdown deadline passed while closing storage; see any warnings above.",
			shutdownTimeout)
	case writersOverran:
		return fmt.Sprintf("NexWiki shut down after the %s deadline for stopping writers passed; storage still closed within the %s shutdown deadline. See the warnings above.",
			shutdownTimeout-storageCloseReserve, shutdownTimeout)
	default:
		return "NexWiki shut down cleanly."
	}
}

// loadFrontendFS returns the React build the web server serves. "frontend/dist" on disk wins when it
// exists, for dev mode live-reloading; otherwise the build embedded in the binary is used.
func loadFrontendFS() (fs.FS, error) {
	if info, err := os.Stat("frontend/dist"); err == nil && info.IsDir() {
		log.Println("Serving frontend assets from live disk (development mode)")
		return os.DirFS("frontend/dist"), nil
	}
	log.Println("Serving frontend assets from embedded filesystem (production mode)")
	return fs.Sub(embeddedFrontend, "frontend/dist")
}

// listenAddr is the address the web server listens on. net.JoinHostPort brackets an IPv6 host,
// which formatting "host:port" by hand does not: -bind :: made ":::5808", which net.Listen rejects.
// An empty host still listens on all interfaces.
func listenAddr(bindHost, port string) string {
	return net.JoinHostPort(bindHost, port)
}

// isWildcardHost reports whether bindHost listens on all interfaces: empty, 0.0.0.0, or ::.
func isWildcardHost(bindHost string) bool {
	if bindHost == "" {
		return true
	}
	ip := net.ParseIP(bindHost)
	return ip != nil && ip.IsUnspecified()
}

// displayHost is the host the startup banner and -launch-in-browser name: the bind interface, or
// "localhost" when bound to all interfaces, since localhost is the address that works locally.
func displayHost(bindHost string) string {
	if isWildcardHost(bindHost) {
		return "localhost"
	}
	return bindHost
}

// buildAppURL assembles the pasteable/openable wiki URL from the display
// host (see displayHost) and port.
func buildAppURL(host, port string) string {
	return httpURL(host, port, "")
}

// httpURL returns the http URL for path on host and port. The host goes through net.JoinHostPort
// so an IPv6 literal is bracketed ("http://[::1]:5808"), and through url.URL so a zone in one is
// escaped as URLs require.
func httpURL(host, port, path string) string {
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: path}).String()
}

// probeHost picks the address waitForServerAndOpenBrowser polls. The server
// may be bound to a specific interface, so probe that interface — except the
// wildcard binds, which are not dialable and fall back to loopback.
func probeHost(bindHost string) string {
	if isWildcardHost(bindHost) {
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
//
// It connects directly, whatever HTTP_PROXY says: the server is this process, and a proxy would
// not reach it at a LAN bind address, leaving the poll to fail until timeout.
func waitForServer(host, port string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Transport: server.NewDirectTransport(), Timeout: 750 * time.Millisecond}
	defer client.CloseIdleConnections()
	for time.Now().Before(deadline) {
		resp, err := client.Get(httpURL(host, port, "/api/config"))
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
//
// The host is returned bare: an IPv6 literal bracketed as in a URL, "[::1]", worked while the listen
// address was formatted by hand, and net.JoinHostPort would bracket it a second time.
func resolveBindHost(flagBind string, envBind string, inContainer bool) string {
	host := flagBind
	if host == "" {
		host = envBind
	}
	if host == "" {
		if inContainer {
			return ""
		}
		return "127.0.0.1"
	}
	if len(host) > 2 && strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host[1 : len(host)-1]
	}
	return host
}

// primaryProbeTimeout bounds the whole search for a running web primary, however many hosts it
// tries: a stdio client waits on it before this process answers anything.
const primaryProbeTimeout = 750 * time.Millisecond

// primaryProbeHosts lists, in order, where an -mcp-only process looks for a web primary, given its
// own resolved bind host. A primary bound to a specific address, such as -bind 192.168.1.50 or
// -bind 127.0.0.2, listens only there, so a sidecar given that address looks only there. A wildcard
// names no address, as with the container default a `docker exec` sidecar inherits, so the sidecar
// looks on loopback, as it does for the native default of 127.0.0.1: IPv4, preferred, and ::1, so a
// primary started with -bind ::1 is found even by a sidecar given no -bind.
func primaryProbeHosts(bindHost string) []string {
	if isWildcardHost(bindHost) || bindHost == "127.0.0.1" {
		return []string{"127.0.0.1", "::1"}
	}
	return []string{bindHost}
}

// findPrimary reports where a NexWiki web server answers GET /api/config on port: the first of
// hosts, in order, that answers within timeout. The hosts are probed at once, not in turn, because
// the time a closed port takes to refuse varies by platform: Windows takes about two seconds on
// loopback, which would spend the whole budget on 127.0.0.1 before ::1 was tried. A host that
// answers is taken as soon as every host before it has failed, or once the budget runs out while one
// is still in flight, like a primary that has bound its port but is still opening storage. It
// connects directly, as the proxy then does, whatever HTTP_PROXY says.
//
// When no host is found but one did answer, with a status other than 200, hint explains in one line
// why it was not used (see probeHint). It is built from the answers already in hand, so it costs the
// search no time.
func findPrimary(hosts []string, port string, timeout time.Duration) (host string, found bool, hint string) {
	if port == "" {
		port = "5808"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	client := &http.Client{Transport: server.NewDirectTransport()}
	var probes sync.WaitGroup
	defer func() {
		// Cancelled first, so the probes still in flight end at once rather than at their own pace,
		// and waited for, so their connections are closed with the rest instead of lingering idle.
		cancel()
		probes.Wait()
		client.CloseIdleConnections()
	}()

	answered := make([]chan int, len(hosts))
	for i, h := range hosts {
		answered[i] = make(chan int, 1)
		probes.Add(1)
		go func() {
			defer probes.Done()
			answered[i] <- primaryStatus(ctx, client, h, port)
		}()
	}
	// statuses holds each host's answer as it is taken, 0 for none yet, for the hint.
	statuses := make([]int, len(hosts))
	for i := range hosts {
		select {
		case statuses[i] = <-answered[i]:
			if statuses[i] == http.StatusOK {
				return hosts[i], true, ""
			}
		case <-ctx.Done():
			// Out of time for the hosts still in flight: take the first later one that has answered.
			for j := i; j < len(hosts); j++ {
				select {
				case statuses[j] = <-answered[j]:
					if statuses[j] == http.StatusOK {
						return hosts[j], true, ""
					}
				default:
				}
			}
			return "", false, probeHint(hosts, port, statuses)
		}
	}
	return "", false, probeHint(hosts, port, statuses)
}

// primaryStatus returns the status GET /api/config on host and port answers with before ctx ends, or
// 0 when there is no answer: the connection is refused or fails, or the server stays silent.
func primaryStatus(ctx context.Context, client *http.Client, host, port string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpURL(host, port, "/api/config"), nil)
	if err != nil {
		return 0
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// probeHint explains why a server that answered the probe was not used, naming the first host, in
// probe order, that answered with a status other than 200, or returns "" when none did. Without it a
// sidecar that reaches a web server it cannot use runs standalone in silence, and its stdio client
// sees only the index-lock failure that follows, which says nothing about the probe.
//
// A 403 gets advice when the host is a name: the web server refuses a Host it does not trust, and an
// IP address is always trusted, so for a name that is the likely cause. It happens when this process
// is given a DNS name as -bind that the web server was not bound to.
func probeHint(hosts []string, port string, statuses []int) string {
	for i, status := range statuses {
		if status == 0 || status == http.StatusOK {
			continue
		}
		base := httpURL(hosts[i], port, "")
		hint := fmt.Sprintf("-mcp-only: %s answered GET /api/config with %s, not 200, so it is not used as a web server.",
			base, strings.TrimSpace(fmt.Sprintf("%d %s", status, http.StatusText(status))))
		if _, err := netip.ParseAddr(hosts[i]); status == http.StatusForbidden && err != nil {
			hint += fmt.Sprintf(" A NexWiki web server refuses host names it does not trust: give this process the same "+
				"-bind (or NEXWIKI_BIND) as the web server, or add %s to %s on the web server.", base, server.AllowedOriginsEnv)
		}
		return hint
	}
	return ""
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

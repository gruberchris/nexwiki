package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nexwiki/server"
)

// Test that demonstrates the archived tag functionality
func TestMainFunctionality(t *testing.T) {
	// This test is just to verify that the imports work correctly
	// The actual archived tag tests are in server/archived_tag_test.go
	t.Log("Server package imported successfully")
}

func TestResolveBindHost(t *testing.T) {
	tests := []struct {
		name        string
		flagBind    string
		envBind     string
		inContainer bool
		want        string
	}{
		{
			name:        "native default",
			flagBind:    "",
			envBind:     "",
			inContainer: false,
			want:        "127.0.0.1",
		},
		{
			name:        "container default",
			flagBind:    "",
			envBind:     "",
			inContainer: true,
			want:        "",
		},
		{
			name:        "flag overrides all",
			flagBind:    "0.0.0.0",
			envBind:     "",
			inContainer: false,
			want:        "0.0.0.0",
		},
		{
			name:        "flag overrides env",
			flagBind:    "192.168.1.10",
			envBind:     "127.0.0.1",
			inContainer: false,
			want:        "192.168.1.10",
		},
		{
			name:        "env overrides default",
			flagBind:    "",
			envBind:     "0.0.0.0",
			inContainer: false,
			want:        "0.0.0.0",
		},
		{
			name:        "env overrides container default",
			flagBind:    "",
			envBind:     "127.0.0.1",
			inContainer: true,
			want:        "127.0.0.1",
		},
		{
			name:        "bare IPv6 flag",
			flagBind:    "::1",
			envBind:     "",
			inContainer: false,
			want:        "::1",
		},
		{
			name:        "bracketed IPv6 flag is unbracketed",
			flagBind:    "[::1]",
			envBind:     "",
			inContainer: false,
			want:        "::1",
		},
		{
			name:        "bracketed IPv6 env is unbracketed",
			flagBind:    "",
			envBind:     "[::]",
			inContainer: true,
			want:        "::",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveBindHost(tt.flagBind, tt.envBind, tt.inContainer)
			if got != tt.want {
				t.Errorf("resolveBindHost(%q, %q, %v) = %q, want %q",
					tt.flagBind, tt.envBind, tt.inContainer, got, tt.want)
			}
		})
	}
}

func TestIsRunningInContainer(t *testing.T) {
	t.Run("kubernetes env detection", func(t *testing.T) {
		t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
		if !isRunningInContainer() {
			t.Errorf("expected isRunningInContainer to return true when KUBERNETES_SERVICE_HOST is set")
		}
	})

	t.Run("container env detection", func(t *testing.T) {
		t.Setenv("CONTAINER", "docker")
		if !isRunningInContainer() {
			t.Errorf("expected isRunningInContainer to return true when CONTAINER is set")
		}
	})
}

func TestResolveDefaultDataDir_Unix(t *testing.T) {
	cases := []struct {
		name string
		goos string
		xdg  string
		home string
		want string
	}{
		{
			name: "linux uses HOME .config",
			goos: "linux", xdg: "", home: "/home/alice",
			want: filepath.Join("/home/alice", ".config", "nexwiki", "nexwiki-data"),
		},
		{
			name: "darwin uses literal HOME .config",
			goos: "darwin", xdg: "", home: "/Users/alice",
			want: filepath.Join("/Users/alice", ".config", "nexwiki", "nexwiki-data"),
		},
		{
			name: "absolute XDG_CONFIG_HOME wins over HOME",
			goos: "linux", xdg: "/tmp/custom-config", home: "/home/alice",
			want: filepath.Join("/tmp/custom-config", "nexwiki", "nexwiki-data"),
		},
		{
			name: "XDG wins even without HOME",
			goos: "linux", xdg: "/tmp/custom-config", home: "",
			want: filepath.Join("/tmp/custom-config", "nexwiki", "nexwiki-data"),
		},
		{
			name: "relative XDG is ignored in favor of HOME",
			goos: "linux", xdg: "relative/config", home: "/home/alice",
			want: filepath.Join("/home/alice", ".config", "nexwiki", "nexwiki-data"),
		},
		{
			name: "no XDG no HOME falls back to ./data",
			goos: "linux", xdg: "", home: "",
			want: "./data",
		},
		{
			name: "relative XDG plus no HOME falls back to ./data",
			goos: "darwin", xdg: "relative/config", home: "",
			want: "./data",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveDefaultDataDir(tc.goos, tc.xdg, "", tc.home); got != tc.want {
				t.Errorf("resolveDefaultDataDir(%q, xdg=%q, home=%q) = %q, want %q",
					tc.goos, tc.xdg, tc.home, got, tc.want)
			}
		})
	}
}

func TestResolveDefaultDataDir_Windows(t *testing.T) {
	cfg := `C:\Users\alice\AppData\Roaming`
	wantNative := filepath.Join(cfg, "nexwiki", "nexwiki-data")
	if got := resolveDefaultDataDir("windows", "", cfg, `C:\Users\alice`); got != wantNative {
		t.Errorf("windows native AppData: got %q, want %q", got, wantNative)
	}

	// Without a config dir, mirror the unix layout under the home profile.
	wantMirror := filepath.Join(`C:\Users\alice`, ".config", "nexwiki", "nexwiki-data")
	if got := resolveDefaultDataDir("windows", "", "", `C:\Users\alice`); got != wantMirror {
		t.Errorf("windows home fallback: got %q, want %q", got, wantMirror)
	}

	// XDG must not affect the Windows branch.
	if got := resolveDefaultDataDir("windows", `/tmp/should-be-ignored`, cfg, `C:\Users\alice`); got != wantNative {
		t.Errorf("windows must ignore XDG: got %q, want %q", got, wantNative)
	}

	if got := resolveDefaultDataDir("windows", "", "", ""); got != "./data" {
		t.Errorf("windows no dirs fallback: got %q, want ./data", got)
	}
}

func TestDefaultDataDir_LiveEnvironment(t *testing.T) {
	got := defaultDataDir()
	if got == "" {
		t.Fatal("defaultDataDir returned empty string")
	}
	// On a normal dev/CI machine HOME (or XDG/AppData) is set, so the default
	// must be the OS-aware location; on a bare service account ./data is OK.
	if got != "./data" && !strings.HasSuffix(got, filepath.Join("nexwiki", "nexwiki-data")) {
		t.Errorf("defaultDataDir = %q, want ./data fallback or *nexwiki/nexwiki-data suffix", got)
	}
	// The live default must never be the Docker in-container path: that path is
	// supplied explicitly via ENTRYPOINT -data=/app/data, never as a default.
	if got == "/app/data" {
		t.Errorf("defaultDataDir must not default to the Docker path /app/data")
	}
}

// TestNewStorage_CreatesMissingDefaultTree proves the second half of the
// contract: when the resolved default path does not exist yet, starting
// NexWiki creates it (articles/assets/history) instead of failing.
func TestNewStorage_CreatesMissingDefaultTree(t *testing.T) {
	simulated := filepath.Join(t.TempDir(), ".config", "nexwiki", "nexwiki-data")

	st, err := server.NewStorage(simulated)
	if err != nil {
		t.Fatalf("NewStorage(%q) failed: %v", simulated, err)
	}
	defer func() { _ = st.Close() }()

	for _, sub := range []string{"articles", "assets", "history"} {
		if fi, err := os.Stat(filepath.Join(simulated, sub)); err != nil || !fi.IsDir() {
			t.Errorf("expected %s/ to exist after NewStorage, stat err=%v", sub, err)
		}
	}
}

func TestBuildAppURL(t *testing.T) {
	cases := []struct{ host, port, want string }{
		{"localhost", "5808", "http://localhost:5808"},
		{"127.0.0.1", "9090", "http://127.0.0.1:9090"},
		{"192.168.1.50", "5808", "http://192.168.1.50:5808"},
		{"::1", "5808", "http://[::1]:5808"},
	}
	for _, tc := range cases {
		if got := buildAppURL(tc.host, tc.port); got != tc.want {
			t.Errorf("buildAppURL(%q, %q) = %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}
}

func TestProbeHost(t *testing.T) {
	cases := []struct{ bind, want string }{
		{"", "127.0.0.1"},
		{"0.0.0.0", "127.0.0.1"},
		{"::", "127.0.0.1"},
		{"0:0:0:0:0:0:0:0", "127.0.0.1"},
		{"127.0.0.1", "127.0.0.1"},
		{"::1", "::1"},
		{"192.168.1.50", "192.168.1.50"},
	}
	for _, tc := range cases {
		if got := probeHost(tc.bind); got != tc.want {
			t.Errorf("probeHost(%q) = %q, want %q", tc.bind, got, tc.want)
		}
	}
}

// TestBindHostAddresses pins, for each kind of bind host, the address the web server listens on, the
// URL the startup banner and -launch-in-browser show, and the URL the browser opener polls. An IPv6
// literal must be bracketed in all three: formatting "host:port" by hand turned -bind :: into
// ":::5808", which net.Listen rejects. The wildcards still listen on all interfaces, are shown as
// localhost, and are polled on loopback.
func TestBindHostAddresses(t *testing.T) {
	for _, tc := range []struct {
		bind, listen, appURL, probeURL string
	}{
		{"", ":5808", "http://localhost:5808", "http://127.0.0.1:5808/api/config"},
		{"0.0.0.0", "0.0.0.0:5808", "http://localhost:5808", "http://127.0.0.1:5808/api/config"},
		{"127.0.0.1", "127.0.0.1:5808", "http://127.0.0.1:5808", "http://127.0.0.1:5808/api/config"},
		{"::", "[::]:5808", "http://localhost:5808", "http://127.0.0.1:5808/api/config"},
		{"::1", "[::1]:5808", "http://[::1]:5808", "http://[::1]:5808/api/config"},
		{"wiki.example.lan", "wiki.example.lan:5808", "http://wiki.example.lan:5808", "http://wiki.example.lan:5808/api/config"},
		// A zone is escaped in a URL, where a bare "%" would not parse.
		{"fe80::1%eth0", "[fe80::1%eth0]:5808", "http://[fe80::1%25eth0]:5808", "http://[fe80::1%25eth0]:5808/api/config"},
	} {
		name := tc.bind
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			listen := listenAddr(tc.bind, "5808")
			if listen != tc.listen {
				t.Errorf("listenAddr(%q) = %q, want %q", tc.bind, listen, tc.listen)
			}
			// What net.Listen does with the address first.
			if host, port, err := net.SplitHostPort(listen); err != nil || host != tc.bind || port != "5808" {
				t.Errorf("listen address %q splits into (%q, %q, %v), want (%q, \"5808\", nil)", listen, host, port, err, tc.bind)
			}

			appURL := buildAppURL(displayHost(tc.bind), "5808")
			if appURL != tc.appURL {
				t.Errorf("banner URL for bind %q = %q, want %q", tc.bind, appURL, tc.appURL)
			}
			if u, err := url.Parse(appURL); err != nil || u.Port() != "5808" || u.Hostname() != displayHost(tc.bind) {
				t.Errorf("banner URL %q does not parse back to host %q and port 5808 (err %v)", appURL, displayHost(tc.bind), err)
			}

			if got := httpURL(probeHost(tc.bind), "5808", "/api/config"); got != tc.probeURL {
				t.Errorf("probe URL for bind %q = %q, want %q", tc.bind, got, tc.probeURL)
			}
		})
	}
}

// serveOn serves handler on host and port, "0" for any free one, and returns the port, skipping the
// test when the address cannot be listened on (no IPv6, or no loopback alias such as 127.0.0.2).
func serveOn(t *testing.T, host, port string, handler http.Handler) string {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		t.Skipf("cannot listen on %s: %v", net.JoinHostPort(host, port), err)
	}
	srv := httptest.NewUnstartedServer(handler)
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	_, port, _ = net.SplitHostPort(ln.Addr().String())
	return port
}

// primaryStub is a stand-in web primary on host. It answers GET /api/config as NexWiki does, which
// is all detection asks for, and answers each POST /api/mcp with a result naming host, counting the
// calls in mcpCalls.
func primaryStub(host string, mcpCalls *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/config":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/mcp":
			mcpCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"answeredOn":"`+host+`"}}`)
		default:
			http.NotFound(w, r)
		}
	})
}

// startPrimaryStub serves a primaryStub on host at a free port, skipping the test as serveOn does.
func startPrimaryStub(t *testing.T, host string) (port string, mcpCalls *atomic.Int32) {
	t.Helper()
	mcpCalls = &atomic.Int32{}
	return serveOn(t, host, "0", primaryStub(host, mcpCalls)), mcpCalls
}

// TestPrimaryProbeHosts pins where an -mcp-only process looks for a web primary: only at a specific
// bind address, and on loopback, IPv4 first, for a wildcard bind or the native default. The
// container defaults, which a `docker exec` sidecar inherits, are wildcards and so look on loopback.
func TestPrimaryProbeHosts(t *testing.T) {
	loopback := []string{"127.0.0.1", "::1"}
	for _, tc := range []struct {
		name string
		bind string
		want []string
	}{
		{"native default", resolveBindHost("", "", false), loopback},
		{"container default", resolveBindHost("", "", true), loopback},
		{"container NEXWIKI_BIND=0.0.0.0", resolveBindHost("", "0.0.0.0", true), loopback},
		{"IPv6 wildcard", "::", loopback},
		{"IPv4 loopback alias", "127.0.0.2", []string{"127.0.0.2"}},
		{"IPv6 loopback", "::1", []string{"::1"}},
		{"LAN address", "192.168.1.50", []string{"192.168.1.50"}},
		{"hostname", "wiki.example.lan", []string{"wiki.example.lan"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := primaryProbeHosts(tc.bind); !slices.Equal(got, tc.want) {
				t.Errorf("primaryProbeHosts(%q) = %q, want %q", tc.bind, got, tc.want)
			}
		})
	}
}

// TestPrimaryProbeTimeoutFitsItsBudget pins the whole search for a web primary to the 750ms a single
// probe had: a stdio client waits on it before an -mcp-only process answers anything, and probing
// more hosts must not make it wait longer.
func TestPrimaryProbeTimeoutFitsItsBudget(t *testing.T) {
	if primaryProbeTimeout <= 0 || primaryProbeTimeout > 750*time.Millisecond {
		t.Errorf("primaryProbeTimeout %s must be positive and at most 750ms", primaryProbeTimeout)
	}
}

// TestFindPrimary pins that an -mcp-only process finds a primary bound to a specific address when
// given that address as its own -bind, where probing 127.0.0.1 alone used to miss it, and still
// finds one on IPv4 loopback from a wildcard or default bind.
func TestFindPrimary(t *testing.T) {
	find := func(bind, port string) (string, bool) {
		return findPrimary(primaryProbeHosts(bind), port, primaryProbeTimeout)
	}

	t.Run("wildcard and default binds find IPv4 loopback", func(t *testing.T) {
		port, _ := startPrimaryStub(t, "127.0.0.1")
		for _, bind := range []string{"", "0.0.0.0", "::", "127.0.0.1"} {
			if host, ok := find(bind, port); !ok || host != "127.0.0.1" {
				t.Errorf("bind %q: findPrimary = (%q, %t), want (\"127.0.0.1\", true)", bind, host, ok)
			}
		}
	})

	t.Run("-bind ::1 finds a primary only on IPv6 loopback", func(t *testing.T) {
		port, _ := startPrimaryStub(t, "::1")
		if host, ok := find("::1", port); !ok || host != "::1" {
			t.Errorf("findPrimary = (%q, %t), want (\"::1\", true)", host, ok)
		}
		// Loopback also falls back to ::1 once 127.0.0.1 refuses, so no -bind is needed for it.
		if host, ok := find("", port); !ok || host != "::1" {
			t.Errorf("with a wildcard bind, findPrimary = (%q, %t), want (\"::1\", true)", host, ok)
		}
	})

	t.Run("-bind 127.0.0.2 finds a primary only on that alias", func(t *testing.T) {
		port, _ := startPrimaryStub(t, "127.0.0.2")
		if host, ok := find("127.0.0.2", port); !ok || host != "127.0.0.2" {
			t.Errorf("findPrimary = (%q, %t), want (\"127.0.0.2\", true)", host, ok)
		}
		// What the sidecar found before it honored -bind: nothing.
		if host, ok := find("", port); ok {
			t.Errorf("with a wildcard bind, findPrimary found %q, but nothing listens on loopback", host)
		}
	})

	t.Run("nothing listening", func(t *testing.T) {
		closed, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		_, port, _ := net.SplitHostPort(closed.Addr().String())
		_ = closed.Close()
		if host, ok := find("", port); ok {
			t.Errorf("findPrimary found %q with nothing listening", host)
		}
	})

	// The IPv6 fallback must not depend on 127.0.0.1 refusing quickly, which Windows takes about two
	// seconds to do. A listener that accepts but never answers stands in for that slow refusal, as it
	// does for a primary still opening storage.
	t.Run("::1 is found while the 127.0.0.1 probe hangs", func(t *testing.T) {
		silent, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = silent.Close() }()
		_, port, _ := net.SplitHostPort(silent.Addr().String())
		serveOn(t, "::1", port, primaryStub("::1", &atomic.Int32{}))

		start := time.Now()
		if host, ok := find("", port); !ok || host != "::1" {
			t.Errorf("findPrimary = (%q, %t), want (\"::1\", true)", host, ok)
		}
		if elapsed := time.Since(start); elapsed > primaryProbeTimeout+500*time.Millisecond {
			t.Errorf("findPrimary took %s, want no more than its %s budget", elapsed, primaryProbeTimeout)
		}
	})

	t.Run("127.0.0.1 is preferred when both answer, even second", func(t *testing.T) {
		slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			primaryStub("127.0.0.1", &atomic.Int32{}).ServeHTTP(w, r)
		})
		port := serveOn(t, "127.0.0.1", "0", slow)
		serveOn(t, "::1", port, primaryStub("::1", &atomic.Int32{}))
		if host, ok := find("", port); !ok || host != "127.0.0.1" {
			t.Errorf("findPrimary = (%q, %t), want (\"127.0.0.1\", true)", host, ok)
		}
	})

	// A host that accepts but never answers, like a primary still opening storage, must not hold the
	// probe past its budget.
	t.Run("hosts that never answer share one budget", func(t *testing.T) {
		silent, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = silent.Close() }()
		_, port, _ := net.SplitHostPort(silent.Addr().String())
		if silent6, err := net.Listen("tcp", net.JoinHostPort("::1", port)); err == nil {
			defer func() { _ = silent6.Close() }()
		}

		start := time.Now()
		if host, ok := find("", port); ok {
			t.Errorf("findPrimary found %q, but nothing answers", host)
		}
		if elapsed := time.Since(start); elapsed > primaryProbeTimeout+500*time.Millisecond {
			t.Errorf("findPrimary took %s, want about its %s budget", elapsed, primaryProbeTimeout)
		}
	})
}

func TestWaitForServer_Healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/config" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	host, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if !waitForServer(host, port, 5*time.Second) {
		t.Errorf("waitForServer against healthy httptest server returned false")
	}
}

// TestWaitForServer_IPv6Loopback pins that the browser opener's poll reaches a server bound to an
// IPv6 literal, which needs the host bracketed in the URL.
func TestWaitForServer_IPv6Loopback(t *testing.T) {
	port, _ := startPrimaryStub(t, "::1")
	if !waitForServer("::1", port, 5*time.Second) {
		t.Errorf("waitForServer against a healthy server on [::1] returned false")
	}
}

func TestWaitForServer_Unreachable(t *testing.T) {
	// Port 1 is privileged/closed: the poll must exhaust the timeout and
	// report false rather than hang or succeed.
	if waitForServer("127.0.0.1", "1", 300*time.Millisecond) {
		t.Errorf("waitForServer against closed port returned true")
	}
}

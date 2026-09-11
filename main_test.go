package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
		{"127.0.0.1", "127.0.0.1"},
		{"192.168.1.50", "192.168.1.50"},
	}
	for _, tc := range cases {
		if got := probeHost(tc.bind); got != tc.want {
			t.Errorf("probeHost(%q) = %q, want %q", tc.bind, got, tc.want)
		}
	}
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

func TestWaitForServer_Unreachable(t *testing.T) {
	// Port 1 is privileged/closed: the poll must exhaust the timeout and
	// report false rather than hang or succeed.
	if waitForServer("127.0.0.1", "1", 300*time.Millisecond) {
		t.Errorf("waitForServer against closed port returned true")
	}
}

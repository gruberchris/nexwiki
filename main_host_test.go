package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nexwiki/server"
)

// startWebServer runs main() as a web server bound to bindHost on port and waits until it answers.
func startWebServer(t *testing.T, bindHost, port string) *mainRun {
	t.Helper()
	home := t.TempDir()
	run := startMain(t, home, nil, "-bind", bindHost, "-port", port, "-data", filepath.Join(home, "wiki"))

	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(time.Minute)
	for {
		resp, err := client.Get("http://" + net.JoinHostPort(bindHost, port) + "/api/config")
		if err == nil {
			_ = resp.Body.Close()
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("the web server did not answer within a minute: %v; stderr:\n%s", err, run.stderr.String())
		}
		select {
		case <-run.done:
			t.Fatalf("main exited before serving (%v); stderr:\n%s", run.err, run.stderr.String())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// requestWithHost sends a request with no Origin to addr, addressed to host, and returns the status
// and body.
func requestWithHost(t *testing.T, method, addr, path, host, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s with Host %q: %v", method, path, host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s %s: %v", method, path, err)
	}
	return resp.StatusCode, string(data)
}

// TestWebServerChecksTheHost pins the Host check on the real web server: a request addressed to a
// DNS name the server doesn't trust is refused on every route, while localhost, IP literals, and
// the -bind hostname are served, including the -launch-in-browser readiness poll.
func TestWebServerChecksTheHost(t *testing.T) {
	t.Run("bound to loopback", func(t *testing.T) {
		port := freePort(t)
		startWebServer(t, "127.0.0.1", port)
		addr := "127.0.0.1:" + port
		const listTools = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

		for _, host := range []string{"evil.example:" + port, "evil.example", "localhost.:" + port} {
			for _, rq := range []struct{ method, path, body string }{
				{http.MethodGet, "/api/okf/export", ""},
				{http.MethodGet, "/api/config", ""},
				{http.MethodGet, "/", ""},
				{http.MethodPost, "/api/mcp", listTools},
			} {
				status, body := requestWithHost(t, rq.method, addr, rq.path, host, rq.body)
				if status != http.StatusForbidden {
					t.Errorf("%s %s with Host %q: got %d, want 403: %.200s", rq.method, rq.path, host, status, body)
					continue
				}
				var decoded struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal([]byte(body), &decoded); err != nil ||
					!strings.Contains(decoded.Error, server.AllowedOriginsEnv) {
					t.Errorf("%s %s with Host %q: rejection does not name %s: %s",
						rq.method, rq.path, host, server.AllowedOriginsEnv, body)
				}
			}
		}

		for _, host := range []string{"localhost:" + port, "127.0.0.1:" + port, "LocalHost:" + port} {
			for _, rq := range []struct{ method, path, body string }{
				{http.MethodGet, "/api/okf/export", ""},
				{http.MethodGet, "/", ""},
				{http.MethodPost, "/api/mcp", listTools},
			} {
				if status, body := requestWithHost(t, rq.method, addr, rq.path, host, rq.body); status != http.StatusOK {
					t.Errorf("%s %s with Host %q: got %d, want 200: %.200s", rq.method, rq.path, host, status, body)
				}
			}
		}

		// The -mcp-only probe and proxy, and the readiness poll for a loopback bind, all dial 127.0.0.1.
		if !waitForServer("127.0.0.1", port, 10*time.Second) {
			t.Error("the readiness poll against 127.0.0.1 did not see the server answer 200")
		}
	})

	t.Run("bound to a hostname", func(t *testing.T) {
		// localhost. resolves to loopback, but no rule other than the bind hostname accepts it, since
		// names are compared exactly. So a 200 here comes from main passing its bind host.
		const bindHost = "localhost."
		ln, err := net.Listen("tcp", net.JoinHostPort(bindHost, "0"))
		if err != nil {
			t.Skipf("%s does not resolve to a local address here: %v", bindHost, err)
		}
		_, port, _ := net.SplitHostPort(ln.Addr().String())
		_ = ln.Close()
		run := startWebServer(t, bindHost, port)

		if !waitForServer(bindHost, port, 10*time.Second) {
			t.Errorf("the readiness poll against %s did not see the server answer 200; stderr:\n%s",
				bindHost, run.stderr.String())
		}
		addr := net.JoinHostPort(bindHost, port)
		if status, body := requestWithHost(t, http.MethodGet, addr, "/api/okf/export", "evil.example:"+port, ""); status != http.StatusForbidden {
			t.Errorf("GET /api/okf/export with Host evil.example: got %d, want 403: %.200s", status, body)
		}
	})
}

package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// loopbackHost is the Host a request built for EnableCORS tests is addressed to.
const loopbackHost = "localhost:5808"

// newLoopbackRequest is httptest.NewRequest addressed to localhost. httptest's default Host,
// example.com, is a DNS name EnableCORS rejects, so a test meant to reach the Origin check or a
// handler through the middleware must name an allowed host.
func newLoopbackRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.Host = loopbackHost
	return req
}

func TestHostAllowed(t *testing.T) {
	tests := []struct {
		name     string
		origins  string // NEXWIKI_ALLOWED_ORIGINS
		bindHost string // as WithBindHost stores it
		host     string
		want     bool
	}{
		// Loopback names.
		{name: "localhost", host: "localhost", want: true},
		{name: "localhost with port", host: "localhost:5808", want: true},
		{name: "localhost in upper case", host: "LOCALHOST:5808", want: true},
		{name: "subdomain of localhost", host: "app.localhost:5808", want: true},
		{name: "subdomain of localhost in mixed case", host: "App.LocalHost", want: true},
		{name: "localhost with a trailing dot is compared exactly", host: "localhost.", want: false},
		{name: "localhost with a trailing dot and port", host: "localhost.:5808", want: false},
		{name: "name merely ending in localhost", host: "notlocalhost:5808", want: false},
		{name: "localhost as a subdomain of another name", host: "localhost.evil.example", want: false},

		// IP literals, loopback or not.
		{name: "IPv4 loopback", host: "127.0.0.1:5808", want: true},
		{name: "IPv4 loopback elsewhere in 127/8", host: "127.1.2.3", want: true},
		{name: "IPv6 loopback bracketed with port", host: "[::1]:5808", want: true},
		{name: "IPv6 loopback bracketed without port", host: "[::1]", want: true},
		{name: "IPv6 loopback without brackets", host: "::1", want: true},
		{name: "LAN IPv4", host: "192.168.1.50:5808", want: true},
		{name: "all-interfaces IPv4", host: "0.0.0.0:5808", want: true},
		{name: "IPv6 in upper case", host: "[2001:DB8::1]:5808", want: true},
		{name: "IPv4-mapped IPv6", host: "[::ffff:192.0.2.1]:80", want: true},
		{name: "IPv6 with a zone", host: "[fe80::1%eth0]:5808", want: true},
		{name: "IPv6 with a percent-encoded zone", host: "[fe80::1%25eth0]:5808", want: true},
		{name: "IPv4 with a trailing dot is a name", host: "127.0.0.1.", want: false},

		// Malformed or missing.
		{name: "empty", host: "", want: false},
		{name: "port only", host: ":5808", want: false},
		{name: "brackets around a name", host: "[localhost]:5808", want: false},
		{name: "brackets around IPv4", host: "[192.168.1.50]:5808", want: false},
		{name: "unclosed bracket", host: "[::1", want: false},
		{name: "text between bracket and port", host: "[::1]5808", want: false},
		{name: "non-numeric port", host: "localhost:http", want: false},
		{name: "extra colons in a name", host: "localhost:5808:1", want: false},

		// DNS names.
		{name: "DNS name", host: "evil.example", want: false},
		{name: "DNS name with port", host: "evil.example:5808", want: false},
		{name: "allowed-origin hostname", origins: "https://wiki.example.com", host: "wiki.example.com", want: true},
		{name: "allowed-origin hostname with a different case and port", origins: "https://wiki.example.com",
			host: "WIKI.Example.COM:443", want: true},
		{name: "allowed-origin hostname from an entry with a port", origins: "https://wiki.example.com:8443",
			host: "wiki.example.com", want: true},
		{name: "second allowed-origin hostname", origins: "https://wiki.example.com, http://notes.internal:5808/",
			host: "notes.internal:5808", want: true},
		{name: "allowed-origin hostname with a trailing dot", origins: "https://wiki.example.com",
			host: "wiki.example.com.", want: false},
		{name: "subdomain of an allowed-origin hostname", origins: "https://wiki.example.com",
			host: "sub.wiki.example.com", want: false},
		{name: "other DNS name with origins configured", origins: "https://wiki.example.com",
			host: "evil.example", want: false},
		{name: "allow-list entry that is not an origin", origins: "wiki.example.com", host: "wiki.example.com", want: false},
		{name: "wildcard opt-in allows any name", origins: "*", host: "evil.example:5808", want: true},
		{name: "wildcard opt-in among origins", origins: "https://wiki.example.com, *", host: "evil.example", want: true},
		{name: "wildcard opt-in allows an empty host", origins: "*", host: "", want: true},
		{name: "bind hostname", bindHost: "nexwiki.lan", host: "nexwiki.lan:5808", want: true},
		{name: "bind hostname in upper case", bindHost: "nexwiki.lan", host: "NEXWIKI.LAN", want: true},
		{name: "other DNS name with a bind hostname", bindHost: "nexwiki.lan", host: "evil.example", want: false},
		{name: "empty host with a bind hostname", bindHost: "nexwiki.lan", host: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(AllowedOriginsEnv, tc.origins)
			if got := hostAllowed(tc.host, tc.bindHost); got != tc.want {
				t.Errorf("hostAllowed(%q, %q) with %s=%q = %v, want %v",
					tc.host, tc.bindHost, AllowedOriginsEnv, tc.origins, got, tc.want)
			}
		})
	}
}

func TestWithBindHostNormalizes(t *testing.T) {
	for in, want := range map[string]string{
		"NexWiki.LAN": "nexwiki.lan",
		" [::1] ":     "::1",
		"":            "",
	} {
		var cfg corsConfig
		WithBindHost(in)(&cfg)
		if cfg.bindHost != want {
			t.Errorf("WithBindHost(%q) stored %q, want %q", in, cfg.bindHost, want)
		}
	}
}

// hostCheckHandler mounts real handlers the way main.go does, behind EnableCORS and
// LimitRequestBodies, and records every request that reaches one of them, so a test can tell a
// request refused by the middleware from one a handler answered.
type hostCheckHandler struct {
	http.Handler
	srv *Server

	mu      sync.Mutex
	reached []string // "METHOD path host" of each request a handler saw
}

func newHostCheckHandler(t *testing.T, opts ...CORSOption) *hostCheckHandler {
	t.Helper()
	h := &hostCheckHandler{srv: newMCPServer(t)}
	record := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			h.mu.Lock()
			h.reached = append(h.reached, r.Method+" "+r.URL.Path+" "+r.Host)
			h.mu.Unlock()
			next(w, r)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mcp", record(h.srv.HandleStreamableHTTP))
	mux.HandleFunc("GET /api/config", record(h.srv.HandleGetConfig))
	mux.HandleFunc("GET /api/okf/export", record(h.srv.HandleExportOKFBundle))
	mux.HandleFunc("/", record(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html>")
	}))
	h.Handler = EnableCORS(LimitRequestBodies(mux), opts...)
	return h
}

func (h *hostCheckHandler) reachedHandlers() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.reached...)
}

// hostCheckRequest builds a request with no Origin, as a browser sends a same-origin GET. A GET to
// /api/mcp asks for the event stream with a cancelled context, so it ends at once if served.
func hostCheckRequest(method, path, host string) *http.Request {
	var req *http.Request
	if method == http.MethodPost {
		req = httptest.NewRequest(method, path, strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_wiki_article",`+
				`"arguments":{"title":"Host Check Write","content":"# Written"}}}`))
		req.Header.Set("Content-Type", "application/json")
	} else {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		req = httptest.NewRequest(method, path, nil).WithContext(ctx)
		if path == "/api/mcp" {
			req.Header.Set("Accept", "text/event-stream")
		}
	}
	req.Host = host
	return req
}

// assertHostRejected checks the 403 a disallowed Host gets: the JSON error shape, a message naming
// the setting that fixes it, the security headers, and no CORS grant.
func assertHostRejected(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("rejection is not the JSON error shape: %v: %s", err, w.Body.String())
	}
	for _, want := range []string{"host not allowed", AllowedOriginsEnv, "https://wiki.example.com"} {
		if !strings.Contains(body.Error, want) {
			t.Errorf("rejection message %q does not mention %q", body.Error, want)
		}
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	for name, want := range map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := w.Header().Get(name); got != want {
			t.Errorf("security header %s = %q, want %q", name, got, want)
		}
	}
	if w.Header().Get("Content-Security-Policy") == "" {
		t.Error("security header Content-Security-Policy is missing from the rejection")
	}
	for name := range w.Header() {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Errorf("rejection carries CORS header %s: %q", name, w.Header().Get(name))
		}
	}
}

// TestEnableCORSRejectsDisallowedHost pins that a request whose Host is a DNS name the server
// doesn't trust gets 403 before any handler runs, whether or not it carries an Origin, so a DNS
// name that resolves to the server can't reach the API, /api/mcp, or the web UI.
func TestEnableCORSRejectsDisallowedHost(t *testing.T) {
	t.Setenv(AllowedOriginsEnv, "") // a developer's own allow list must not decide the outcome
	h := newHostCheckHandler(t)

	requests := []struct{ method, path string }{
		{http.MethodGet, "/api/okf/export"},
		{http.MethodGet, "/api/config"},
		{http.MethodGet, "/api/mcp"},
		{http.MethodPost, "/api/mcp"},
		{http.MethodGet, "/"},
	}
	for _, host := range []string{"evil.example", "evil.example:5808", "wiki.example.com:443"} {
		for _, rq := range requests {
			t.Run(rq.method+" "+rq.path+" Host "+host, func(t *testing.T) {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, hostCheckRequest(rq.method, rq.path, host))
				assertHostRejected(t, w)
			})
		}
	}

	t.Run("checked before the Origin, preflights included", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/mcp", nil)
		req.Host = "evil.example:5808"
		req.Header.Set("Origin", "http://localhost:5173") // an Origin originAllowed accepts
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assertHostRejected(t, w)

		get := hostCheckRequest(http.MethodGet, "/api/okf/export", "evil.example:5808")
		get.Header.Set("Origin", "http://localhost:5808")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, get)
		assertHostRejected(t, w)
	})

	if reached := h.reachedHandlers(); len(reached) != 0 {
		t.Errorf("rejected requests reached handlers: %v", reached)
	}
	if _, err := h.srv.Storage.GetArticle("host-check-write"); err == nil {
		t.Error("a tools/call with a disallowed Host still created the article")
	}
}

// TestEnableCORSServesAllowedHosts is the counterpart: the same requests addressed to a loopback
// name, an IP literal, an allowed-origin hostname, or the bind hostname reach their handlers.
func TestEnableCORSServesAllowedHosts(t *testing.T) {
	cases := []struct {
		name     string
		origins  string
		bindHost string
		host     string
	}{
		{name: "localhost", host: "localhost:5808"},
		{name: "IPv4 loopback", host: "127.0.0.1:5808"},
		{name: "IPv6 loopback", host: "[::1]:5808"},
		{name: "LAN IP", host: "192.168.1.50:5808"},
		{name: "reverse proxy forwarding an allowed domain", origins: "https://wiki.example.com", host: "wiki.example.com"},
		{name: "allowed domain with port and case", origins: "https://wiki.example.com:8443", host: "Wiki.Example.com:8443"},
		{name: "bind hostname", bindHost: "NexWiki.lan", host: "nexwiki.lan:5808"},
		{name: "wildcard opt-in", origins: "*", host: "evil.example:5808"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(AllowedOriginsEnv, tc.origins)
			var opts []CORSOption
			if tc.bindHost != "" {
				opts = append(opts, WithBindHost(tc.bindHost))
			}
			h := newHostCheckHandler(t, opts...)

			for _, path := range []string{"/api/okf/export", "/api/config", "/api/mcp", "/"} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, hostCheckRequest(http.MethodGet, path, tc.host))
				if w.Code != http.StatusOK {
					t.Errorf("GET %s Host %s: expected 200, got %d: %s", path, tc.host, w.Code, w.Body.String())
				}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, hostCheckRequest(http.MethodPost, "/api/mcp", tc.host))
			if w.Code != http.StatusOK {
				t.Errorf("POST /api/mcp Host %s: expected 200, got %d: %s", tc.host, w.Code, w.Body.String())
			}
			if got := len(h.reachedHandlers()); got != 5 {
				t.Errorf("%d requests reached handlers, want 5: %v", got, h.reachedHandlers())
			}
			if _, err := h.srv.Storage.GetArticle("host-check-write"); err != nil {
				t.Errorf("tools/call with an allowed Host did not create the article: %v", err)
			}
		})
	}

	t.Run("bind hostname is only trusted when passed", func(t *testing.T) {
		t.Setenv(AllowedOriginsEnv, "")
		w := httptest.NewRecorder()
		newHostCheckHandler(t).ServeHTTP(w, hostCheckRequest(http.MethodGet, "/api/config", "nexwiki.lan:5808"))
		assertHostRejected(t, w)
	})
}

// TestHostRejectionQuotesHostSafely pins that the Host quoted in the rejection is stripped of
// control characters and bounded, whatever the client sent.
func TestHostRejectionQuotesHostSafely(t *testing.T) {
	t.Setenv(AllowedOriginsEnv, "")
	h := newHostCheckHandler(t)

	req := hostCheckRequest(http.MethodGet, "/api/config", "evil\x00\x1b[31m\r\n.example"+strings.Repeat("a", 5000))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assertHostRejected(t, w)

	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if strings.ContainsAny(body.Error, "\x00\x1b\r\n") {
		t.Errorf("rejection message carries control characters: %q", body.Error)
	}
	if !strings.Contains(body.Error, `"evil[31m.example`) {
		t.Errorf("rejection message does not quote the sanitized host: %q", body.Error)
	}
	if strings.Contains(body.Error, strings.Repeat("a", maxQuotedValueBytes)) {
		t.Errorf("rejection message quotes the host unbounded (%d bytes)", len(body.Error))
	}
}

// TestEnableCORSRejectsEmptyHostOverTheWire covers the Host values only a real connection can
// produce. Go's server answers an HTTP/1.1 request with no Host header with 400 itself, but it
// passes an HTTP/1.0 request without one, or an HTTP/1.1 request with an empty one, to the handler
// with r.Host == "".
func TestEnableCORSRejectsEmptyHostOverTheWire(t *testing.T) {
	requests := map[string]string{
		"HTTP/1.0 without Host":    "GET /api/config HTTP/1.0\r\n\r\n",
		"HTTP/1.1 with empty Host": "GET /api/config HTTP/1.1\r\nHost:\r\nConnection: close\r\n\r\n",
	}
	send := func(t *testing.T, addr, raw string) *http.Response {
		t.Helper()
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.WriteString(conn, raw); err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatalf("reading the response: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp
	}

	for name, raw := range requests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(AllowedOriginsEnv, "")
			h := newHostCheckHandler(t)
			ts := httptest.NewServer(h)
			defer ts.Close()
			if resp := send(t, ts.Listener.Addr().String(), raw); resp.StatusCode != http.StatusForbidden {
				t.Errorf("expected 403, got %d", resp.StatusCode)
			}
			if reached := h.reachedHandlers(); len(reached) != 0 {
				t.Errorf("request with no Host reached handlers: %v", reached)
			}

			// With the wildcard opt-in the same request is served, which shows Go's server delivered
			// it with an empty Host rather than refusing it itself.
			t.Setenv(AllowedOriginsEnv, "*")
			if resp := send(t, ts.Listener.Addr().String(), raw); resp.StatusCode != http.StatusOK {
				t.Errorf("wildcard opt-in: expected 200, got %d", resp.StatusCode)
			}
			if reached := h.reachedHandlers(); len(reached) != 1 || reached[0] != "GET /api/config " {
				t.Errorf("wildcard opt-in: handlers saw %q, want one request with an empty Host", reached)
			}
		})
	}
}

// TestOriginAllowedSameOriginFollowsHostRules pins rule 4 of originAllowed: an Origin equal to the
// request's Host, compared as host:port without the scheme, is accepted when the Host is allowed on
// its own: localhost or *.localhost, an IP literal, or the bind hostname. A Host allowed only as the
// hostname of a listed origin doesn't qualify; its origins must match the list exactly (rule 3),
// which applies whatever the Host.
func TestOriginAllowedSameOriginFollowsHostRules(t *testing.T) {
	tests := []struct {
		name     string
		origins  string // NEXWIKI_ALLOWED_ORIGINS
		bindHost string
		origin   string
		host     string
		want     bool
	}{
		{name: "subdomain of localhost", origin: "http://app.localhost:5808", host: "app.localhost:5808", want: true},
		{name: "subdomain of localhost in another case", origin: "http://App.LocalHost:5808", host: "app.localhost:5808", want: true},
		{name: "bind hostname", bindHost: "wiki.lan", origin: "http://wiki.lan:5808", host: "wiki.lan:5808", want: true},
		{name: "IP literal", origin: "http://192.168.1.50:5808", host: "192.168.1.50:5808", want: true},
		{name: "listed origin with its own hostname as Host", origins: "https://wiki.example.com",
			origin: "https://wiki.example.com", host: "wiki.example.com", want: true},

		{name: "bind hostname Origin with another allowed Host", bindHost: "wiki.lan",
			origin: "http://wiki.lan:5808", host: "localhost:5808", want: false},
		{name: "bind hostname Origin with another allowed Host, listed", origins: "http://wiki.lan:5808", bindHost: "wiki.lan",
			origin: "http://wiki.lan:5808", host: "localhost:5808", want: true},
		{name: "bind hostname on another port", bindHost: "wiki.lan", origin: "http://wiki.lan:9999", host: "wiki.lan:5808", want: false},
		{name: "bind hostname not passed", origin: "http://wiki.lan:5808", host: "wiki.lan:5808", want: false},
		{name: "listed origin with a different Host", origins: "https://wiki.example.com", bindHost: "wiki.lan",
			origin: "https://wiki.example.com", host: "wiki.lan:5808", want: true},
		{name: "unlisted scheme of a listed origin with a different Host", origins: "https://wiki.example.com",
			origin: "http://wiki.example.com", host: "localhost:5808", want: false},
		{name: "hostname of a listed origin under another scheme", origins: "https://wiki.example.com",
			origin: "http://wiki.example.com", host: "wiki.example.com", want: false},
		{name: "hostname of a listed origin on another port", origins: "https://wiki.example.com",
			origin: "http://wiki.example.com:8080", host: "wiki.example.com:8080", want: false},
		{name: "DNS name that is not allowed", origin: "http://evil.example:5808", host: "evil.example:5808", want: false},
		{name: "null origin with an allowed Host", bindHost: "wiki.lan", origin: "null", host: "wiki.lan:5808", want: false},
		{name: "trailing-dot Origin with a Host without it", origin: "http://app.localhost.:5808", host: "app.localhost:5808", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(AllowedOriginsEnv, tc.origins)
			if _, got := originAllowed(tc.origin, tc.host, tc.bindHost); got != tc.want {
				t.Errorf("originAllowed(%q, %q, %q) with %s=%q = %v, want %v",
					tc.origin, tc.host, tc.bindHost, AllowedOriginsEnv, tc.origins, got, tc.want)
			}
		})
	}
}

// TestEnableCORSAcceptsSameOriginOnAllowedHosts covers rule 4 on the production wiring: a browser
// write whose Origin matches an allowed Host is served and granted CORS, one from another origin is
// rejected by the Origin check, and one to a DNS name that isn't allowed is rejected by the Host
// check before its Origin is considered.
func TestEnableCORSAcceptsSameOriginOnAllowedHosts(t *testing.T) {
	t.Setenv(AllowedOriginsEnv, "")
	h := newHostCheckHandler(t, WithBindHost("wiki.lan"))

	write := func(origin, host string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	for _, host := range []string{"app.localhost:5808", "wiki.lan:5808"} {
		origin := "http://" + host
		w := write(origin, host)
		if w.Code != http.StatusOK {
			t.Errorf("Origin and Host %s: expected 200, got %d: %s", host, w.Code, w.Body.String())
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("Origin and Host %s: Access-Control-Allow-Origin = %q, want %q", host, got, origin)
		}
	}

	w := write("http://wiki.lan:5808", "localhost:5808")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "origin not allowed") {
		t.Errorf("bind hostname Origin with Host localhost: expected the Origin rejection, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("rejected origin was granted Access-Control-Allow-Origin %q", got)
	}

	assertHostRejected(t, write("http://evil.example:5808", "evil.example:5808"))

	if got := len(h.reachedHandlers()); got != 2 {
		t.Errorf("%d requests reached handlers, want the 2 same-origin writes: %v", got, h.reachedHandlers())
	}
}

// TestOriginRejectionQuotesOriginSafely pins that the Origin quoted in the rejection is limited to
// printable ASCII and bounded, whatever the client sent.
func TestOriginRejectionQuotesOriginSafely(t *testing.T) {
	t.Setenv(AllowedOriginsEnv, "")
	h := newHostCheckHandler(t)

	req := newLoopbackRequest(http.MethodGet, "/api/config", nil)
	req.Header.Set("Origin", "https://\u00e9vil\x00\x1b[31m\t.example"+strings.Repeat("a", 5000))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("rejection is not the JSON error shape: %v: %s", err, w.Body.String())
	}
	if strings.ContainsAny(body.Error, "\x00\x1b\t\u00e9") {
		t.Errorf("rejection message carries control or non-ASCII characters: %q", body.Error)
	}
	if !strings.Contains(body.Error, `origin not allowed: "https://vil[31m.example`) {
		t.Errorf("rejection message does not quote the sanitized origin: %q", body.Error)
	}
	if strings.Contains(body.Error, strings.Repeat("a", maxQuotedValueBytes)) {
		t.Errorf("rejection message quotes the origin unbounded (%d bytes)", len(body.Error))
	}
	if reached := h.reachedHandlers(); len(reached) != 0 {
		t.Errorf("rejected origin reached handlers: %v", reached)
	}
}

// TestEnableCORSListedOriginHostNeedsAnExactOrigin pins that a Host allowed only as the hostname of
// a listed origin passes the Host check but gets no same-origin acceptance: a browser request from
// that host is served only when its Origin matches the listed entry exactly, scheme included.
func TestEnableCORSListedOriginHostNeedsAnExactOrigin(t *testing.T) {
	t.Setenv(AllowedOriginsEnv, "https://wiki.example.com")
	h := newHostCheckHandler(t)

	write := func(origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Host = "wiki.example.com"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	w := write("http://wiki.example.com")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "origin not allowed") {
		t.Errorf("unlisted scheme: expected the Origin rejection, got %d: %s", w.Code, w.Body.String())
	}
	for name := range w.Header() {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Errorf("unlisted scheme: rejection carries CORS header %s: %q", name, w.Header().Get(name))
		}
	}
	if reached := h.reachedHandlers(); len(reached) != 0 {
		t.Errorf("unlisted scheme reached handlers: %v", reached)
	}

	w = write("https://wiki.example.com")
	if w.Code != http.StatusOK {
		t.Errorf("listed origin: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://wiki.example.com" {
		t.Errorf("listed origin: Access-Control-Allow-Origin = %q, want the listed origin", got)
	}
}

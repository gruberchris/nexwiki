package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// AllowedOriginsEnv is the environment variable that extends the browser Origin allow-list.
// It holds a comma-separated list of exact origins (scheme://host[:port]), e.g.
// "https://wiki.example.com,http://192.168.1.50:5808". The single value "*" restores the
// legacy permissive behavior and is unsafe on any machine that also browses the web.
const AllowedOriginsEnv = "NEXWIKI_ALLOWED_ORIGINS"

// configuredOrigins returns the exact origins listed in NEXWIKI_ALLOWED_ORIGINS, plus whether
// the wildcard opt-out was requested.
func configuredOrigins() (origins []string, wildcard bool) {
	raw := os.Getenv(AllowedOriginsEnv)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(part), "/"))
		if part == "" {
			continue
		}
		if part == "*" {
			wildcard = true
			continue
		}
		origins = append(origins, strings.ToLower(part))
	}
	return origins, wildcard
}

// isLoopbackHost reports whether a bare hostname (no port) is a loopback address.
func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// hostnameOnly strips any :port suffix from a Host or Origin authority.
func hostnameOnly(authority string) string {
	if h, _, err := net.SplitHostPort(authority); err == nil {
		return h
	}
	return strings.Trim(authority, "[]")
}

// parseHostHeader splits a Host header value into its lower-cased hostname, without port or IPv6
// brackets, and reports whether that hostname is an IP literal (a zone is allowed). ok is false for
// an empty or malformed value: brackets around anything but an IPv6 address, or a non-numeric port.
func parseHostHeader(authority string) (hostname string, isIP bool, ok bool) {
	name, port := authority, ""
	switch {
	case strings.HasPrefix(authority, "["):
		end := strings.IndexByte(authority, ']')
		if end < 0 {
			return "", false, false
		}
		name, port = authority[1:end], authority[end+1:]
		if port != "" {
			if port[0] != ':' {
				return "", false, false
			}
			port = port[1:]
		}
		if addr, err := netip.ParseAddr(name); err != nil || !addr.Is6() {
			return "", false, false
		}
	case strings.Count(authority, ":") > 1:
		// An IPv6 address sent without brackets, which leaves no room for a port. Checked below.
	case strings.Contains(authority, ":"):
		i := strings.LastIndexByte(authority, ':')
		name, port = authority[:i], authority[i+1:]
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return "", false, false
		}
	}
	if name == "" {
		return "", false, false
	}
	name = strings.ToLower(name)
	_, err := netip.ParseAddr(name)
	if err != nil && strings.Contains(name, ":") {
		return "", false, false // colons outside brackets that don't form an IPv6 address
	}
	return name, err == nil, true
}

// hostAllowed decides whether a request's Host header names this NexWiki instance.
//
// originAllowed only sees requests that carry an Origin, and browsers omit it on same-origin GET
// and HEAD requests. The Host header is always the name the client used, so requiring a trusted
// name there keeps a DNS name that merely resolves to this server from reaching it. Allowed:
//
//  1. Any Host when NEXWIKI_ALLOWED_ORIGINS includes the "*" opt-out.
//  2. An IP literal, loopback or not (IPv4, or IPv6 with optional brackets and zone). Rebinding
//     needs a DNS name, and this keeps LAN, Docker port publishing, and loopback access working.
//  3. localhost and *.localhost, which are reserved for loopback and can't be registered.
//  4. The server's own -bind / NEXWIKI_BIND hostname, which the operator chose (bindHost, already
//     lower-cased and without brackets).
//  5. The hostname of any origin listed in NEXWIKI_ALLOWED_ORIGINS, so a reverse proxy that
//     forwards the public Host keeps working.
//
// Names are compared case-insensitively and without the port, but otherwise exactly: a trailing
// dot ("localhost.") is not stripped, as origins are compared the same way. An empty or malformed
// Host is rejected.
func hostAllowed(host, bindHost string) bool {
	allowList, wildcard := configuredOrigins()
	if wildcard { // rule 1
		return true
	}
	name, isIP, ok := parseHostHeader(host)
	if !ok {
		return false
	}
	if intrinsicHost(name, isIP, bindHost) { // rules 2 to 4
		return true
	}
	for _, origin := range allowList { // rule 5
		if u, err := url.Parse(origin); err == nil && u.Hostname() == name {
			return true
		}
	}
	return false
}

// intrinsicHost reports whether a parsed Host name is trusted on its own, whatever
// NEXWIKI_ALLOWED_ORIGINS lists: an IP literal, localhost or *.localhost, or the bind hostname
// (rules 2 to 4 of hostAllowed).
func intrinsicHost(name string, isIP bool, bindHost string) bool {
	return isIP || name == "localhost" || strings.HasSuffix(name, ".localhost") ||
		(bindHost != "" && name == bindHost)
}

// hostIntrinsicallyAllowed is hostAllowed without the wildcard opt-in and without the hostnames
// of listed origins: it accepts a Host only for what it is.
func hostIntrinsicallyAllowed(host, bindHost string) bool {
	name, isIP, ok := parseHostHeader(host)
	return ok && intrinsicHost(name, isIP, bindHost)
}

// maxQuotedValueBytes bounds how much of a rejected Host or Origin header is quoted back in the
// error.
const maxQuotedValueBytes = 100

// quoteClientValue renders a client-supplied header value for an error message: printable ASCII
// only, truncated, and quoted, so the value can't inject control characters or bulk into the
// response.
func quoteClientValue(value string) string {
	var b strings.Builder
	for i := 0; i < len(value) && b.Len() < maxQuotedValueBytes; i++ {
		if c := value[i]; c > ' ' && c < 0x7f {
			b.WriteByte(c)
		}
	}
	return strconv.Quote(b.String())
}

// hostRejectedMessage explains a rejected Host to the operator, who is the one able to fix it.
func hostRejectedMessage(host string) string {
	return "host not allowed: " + quoteClientValue(host) + ". NexWiki is unauthenticated and only answers " +
		"requests addressed to localhost, an IP address, or its -bind hostname by default. To serve it " +
		"under a domain name, for example behind a reverse proxy, add the site's origin " +
		"(such as https://wiki.example.com) to " + AllowedOriginsEnv + "."
}

// originAllowed decides whether a browser Origin may talk to this NexWiki instance.
//
// NexWiki has no authentication: anything a browser is allowed to send here can read, edit,
// and delete the entire wiki (and drive every MCP tool). A wildcard Access-Control-Allow-Origin
// therefore lets *any* website the user visits exfiltrate or destroy the knowledge base over
// localhost — the DNS-rebinding class the MCP spec requires local servers to reject. The rules:
//
//  1. No Origin header — a non-browser client (curl, an MCP SDK, a native app). Allowed;
//     browsers always send Origin on cross-origin and on non-GET same-origin requests. The
//     same-origin GETs that carry none are covered by hostAllowed, which EnableCORS checks first.
//  2. Loopback origin — the wiki's own UI and the Vite dev server on :5173. Allowed.
//  3. Exactly listed in NEXWIKI_ALLOWED_ORIGINS. Allowed.
//  4. Same-origin as the request's Host (host:port compared without the scheme), when that Host
//     is allowed on its own (hostIntrinsicallyAllowed): localhost or *.localhost, an IP literal
//     (e.g. reaching the wiki from a phone at http://192.168.1.50:5808), or the bind hostname
//     (bindHost). Any other DNS name is excluded, so a name that merely resolves to this server
//     can't satisfy Origin == Host. That includes the hostname of a listed origin: the Host check
//     lets its requests through, but its browser origins must match rule 3 exactly, scheme and
//     port included.
//
// Anything else is rejected. Returns the origin to echo back, or "" when it must be blocked.
func originAllowed(origin, host, bindHost string) (string, bool) {
	allowList, wildcard := configuredOrigins()
	if wildcard {
		return "*", true
	}
	if origin == "" {
		return "", true // rule 1: non-browser client, nothing to echo
	}

	normalized := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(origin), "/"))
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Host == "" {
		return "", false // malformed or opaque ("null") origin
	}

	if isLoopbackHost(hostnameOnly(parsed.Host)) { // rule 2
		return origin, true
	}
	for _, allowed := range allowList { // rule 3
		if normalized == allowed {
			return origin, true
		}
	}
	if host != "" && strings.EqualFold(parsed.Host, host) && hostIntrinsicallyAllowed(host, bindHost) { // rule 4
		return origin, true
	}

	return "", false
}

// applySecurityHeaders sets baseline hardening headers on every response.
func applySecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self' data:; connect-src 'self'; media-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
}

// corsAllowedMethods are the methods an allowed browser origin may use across the REST API and
// /api/mcp.
const corsAllowedMethods = "GET, POST, PUT, DELETE, OPTIONS"

// corsAllowedHeaders is the explicit set of request headers an allowed browser origin may send. A
// header missing here fails the preflight before the request reaches any handler, so it must name
// everything NexWiki reads or requires. It is never "*" or an echo of
// Access-Control-Request-Headers: the list is the contract, and a blind echo would make it
// meaningless.
//
// Mcp-Session-Id and Last-Event-ID are deliberately absent. NexWiki issues no session and writes
// no SSE event ids, so a conforming client has nothing to send back, and neither header is read.
//
// No Access-Control-Expose-Headers is sent either: every response header an MCP client reads
// (Content-Type, to tell a JSON reply from an SSE stream) is CORS-safelisted. A protocol header
// added to responses later, such as Mcp-Session-Id, must be exposed or browser JavaScript can't
// see it.
var corsAllowedHeaders = strings.Join([]string{
	"Content-Type",  // JSON bodies from the web UI and MCP clients aren't a safelisted type
	"Authorization", // NexWiki has no auth of its own, but a reverse proxy in front of it may
	"Accept",        // read by the MCP GET stream; long or unusual values lose safelisted status
	// Mirrored from the body and rejected when missing (validateModernHeaders): the first two on
	// every modern-era request, Mcp-Name only on those that name a target (methodsWithNameHeader).
	// Legacy HTTP clients also send MCP-Protocol-Version after initialize.
	"MCP-Protocol-Version",
	"Mcp-Method",
	"Mcp-Name",
	// Client attribution over HTTP, which has no session to remember an initialize handshake.
	clientNameHeader,
}, ", ")

// applyCORSHeaders echoes the validated origin (never "*" unless explicitly opted in) and
// marks the response as origin-dependent so shared caches do not cross-serve it. With no origin
// to echo (a rejected origin, or a non-browser client) only Vary is set: the Allow-* headers
// grant nothing without Allow-Origin, so sending them would only advertise the header surface.
func applyCORSHeaders(w http.ResponseWriter, allowOrigin, methods, headers string) {
	w.Header().Set("Vary", "Origin")
	if allowOrigin == "" {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
	w.Header().Set("Access-Control-Allow-Methods", methods)
	w.Header().Set("Access-Control-Allow-Headers", headers)
}

// corsConfig is what EnableCORS knows about the server beyond the environment.
type corsConfig struct {
	bindHost string // lower-cased, without brackets
}

// CORSOption configures EnableCORS.
type CORSOption func(*corsConfig)

// WithBindHost passes the host the web server is bound to (-bind / NEXWIKI_BIND). When it is a DNS
// name, requests addressed to that name are accepted, so clients that reach the server by the name
// it was bound to, such as the -launch-in-browser readiness poll, keep working.
func WithBindHost(host string) CORSOption {
	return func(c *corsConfig) {
		c.bindHost = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	}
}

// EnableCORS checks the request's Host against hostAllowed, then validates the browser Origin
// against originAllowed, echoing back only origins that pass, and applies the baseline security
// headers. Either rejection is a 403 before any handler runs — including reads, since with no
// authentication a cross-site read is exfiltration.
func EnableCORS(next http.Handler, opts ...CORSOption) http.Handler {
	var cfg corsConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applySecurityHeaders(w)

		// Before the Origin, which a same-origin GET doesn't carry: a request to a DNS name the
		// server doesn't trust is refused whatever its Origin, preflights included.
		if !hostAllowed(r.Host, cfg.bindHost) {
			writeError(w, http.StatusForbidden, hostRejectedMessage(r.Host))
			return
		}

		origin := r.Header.Get("Origin")
		allowOrigin, ok := originAllowed(origin, r.Host, cfg.bindHost)
		if !ok {
			applyCORSHeaders(w, "", "", "")
			writeError(w, http.StatusForbidden,
				"origin not allowed: "+quoteClientValue(origin)+". NexWiki is unauthenticated and only accepts same-origin "+
					"and loopback browser requests by default. Set "+AllowedOriginsEnv+" to permit this origin.")
			return
		}

		applyCORSHeaders(w, allowOrigin, corsAllowedMethods, corsAllowedHeaders)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Request body ceilings. Without these, every JSON endpoint reads an unbounded body into memory
// (json.Decoder has no inherent limit) and multipart uploads spill unbounded to disk —
// ParseMultipartForm's argument caps only how much is buffered in RAM, not the total transfer.
// A single unauthenticated request could therefore exhaust memory or fill the data volume.
const (
	maxJSONBodyBytes     = int64(8 << 20)   // article/theme/tag JSON payloads
	maxAssetUploadBytes  = int64(25 << 20)  // a single uploaded image
	maxBundleUploadBytes = int64(100 << 20) // an OKF restore bundle (decompressed limits live in okf.go)
)

// requestBodyLimit returns the byte ceiling for a request path. Upload endpoints legitimately
// carry more than a JSON payload, so they are widened explicitly rather than raising the default.
func requestBodyLimit(r *http.Request) int64 {
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/okf/import"):
		return maxBundleUploadBytes
	case strings.HasSuffix(r.URL.Path, "/assets"):
		return maxAssetUploadBytes
	default:
		return maxJSONBodyBytes
	}
}

// LimitRequestBodies caps how much a client can send. Applied as middleware rather than per
// handler so a newly added endpoint inherits the protection instead of silently omitting it.
// Bodies over the ceiling fail at read time; writeDecodeError turns that into a 413.
func LimitRequestBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
			r.Body = http.MaxBytesReader(w, r.Body, requestBodyLimit(r))
		}
		next.ServeHTTP(w, r)
	})
}

// writeDecodeError maps a body-read failure to the right status: 413 when the client exceeded
// the size ceiling, 400 for genuinely malformed input. Distinguishing them matters — a client
// that gets "invalid request payload" for an oversized upload has no idea what to fix.
func writeDecodeError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("request body exceeds the %d MB limit for this endpoint", maxErr.Limit>>20))
		return
	}
	writeError(w, http.StatusBadRequest, "invalid request payload")
}

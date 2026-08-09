package server

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// AllowedOriginsEnv is the environment variable that extends the browser Origin allow-list.
// It holds a comma-separated list of exact origins (scheme://host[:port]), e.g.
// "https://wiki.example.com,http://192.168.1.50:8080". The single value "*" restores the
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

// isIPLiteral reports whether the authority's host is a bare IP address rather than a DNS name.
// DNS-rebinding attacks require a resolvable hostname, so an IP-literal Host cannot be rebound.
func isIPLiteral(authority string) bool {
	return net.ParseIP(hostnameOnly(authority)) != nil
}

// originAllowed decides whether a browser Origin may talk to this NexWiki instance.
//
// NexWiki has no authentication: anything a browser is allowed to send here can read, edit,
// and delete the entire wiki (and drive every MCP tool). A wildcard Access-Control-Allow-Origin
// therefore lets *any* website the user visits exfiltrate or destroy the knowledge base over
// localhost — the DNS-rebinding class the MCP spec requires local servers to reject. The rules:
//
//  1. No Origin header — a non-browser client (curl, an MCP SDK, a native app). Allowed;
//     browsers always send Origin on cross-origin and on non-GET same-origin requests.
//  2. Loopback origin — the wiki's own UI and the Vite dev server on :5173. Allowed.
//  3. Exactly listed in NEXWIKI_ALLOWED_ORIGINS. Allowed.
//  4. Same-origin as the request's Host, but only when that Host is loopback or a bare IP
//     (e.g. reaching the wiki from a phone at http://192.168.1.50:8080). A DNS name is
//     excluded here because rebinding would otherwise satisfy Origin == Host; reverse-proxy
//     deployments must name their domain in NEXWIKI_ALLOWED_ORIGINS.
//
// Anything else is rejected. Returns the origin to echo back, or "" when it must be blocked.
func originAllowed(origin, host string) (string, bool) {
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
	if host != "" && strings.EqualFold(parsed.Host, host) && // rule 4
		(isLoopbackHost(hostnameOnly(host)) || isIPLiteral(host)) {
		return origin, true
	}

	return "", false
}

// applySecurityHeaders sets baseline hardening headers on every response.
func applySecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
}

// applyCORSHeaders echoes the validated origin (never "*" unless explicitly opted in) and
// marks the response as origin-dependent so shared caches do not cross-serve it.
func applyCORSHeaders(w http.ResponseWriter, allowOrigin, methods, headers string) {
	w.Header().Set("Vary", "Origin")
	if allowOrigin != "" {
		w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
	}
	w.Header().Set("Access-Control-Allow-Methods", methods)
	w.Header().Set("Access-Control-Allow-Headers", headers)
}

// EnableCORS validates the browser Origin against originAllowed, echoing back only origins that
// pass, and applies the baseline security headers. Rejected origins get 403 before reaching any
// handler — including reads, since with no authentication a cross-site read is exfiltration.
func EnableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applySecurityHeaders(w)

		origin := r.Header.Get("Origin")
		allowOrigin, ok := originAllowed(origin, r.Host)
		if !ok {
			applyCORSHeaders(w, "", "GET, POST, PUT, DELETE, OPTIONS", "Content-Type, Authorization")
			writeError(w, http.StatusForbidden,
				"origin not allowed: "+origin+". NexWiki is unauthenticated and only accepts same-origin "+
					"and loopback browser requests by default. Set "+AllowedOriginsEnv+" to permit this origin.")
			return
		}

		applyCORSHeaders(w, allowOrigin, "GET, POST, PUT, DELETE, OPTIONS", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

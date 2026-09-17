package server

import (
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

// modernEnv builds a params envelope carrying modern `_meta` clientInfo, the way a 2026-07-28
// client sends it on every request.
func modernEnv(t *testing.T, clientJSON string) paramsEnvelope {
	t.Helper()
	params := []byte(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientInfo":` + clientJSON + `}}`)
	return parseParamsEnvelope(params)
}

// TestNexWikiNameNeverBecomesTheAgent is the regression guard for the defect this work exists to
// fix. NEXWIKI_NAME is the wiki's display title; it used to be copied into the activity log's
// `agent` column, so every agent write on a configured deployment was credited to the wiki itself.
func TestNexWikiNameNeverBecomesTheAgent(t *testing.T) {
	t.Setenv("NEXWIKI_NAME", "My Personal Brain")

	srv := newTestServer(t)
	srv.WikiName = "My Personal Brain"

	got := srv.resolveAgent(&JSONRPCRequest{}, paramsEnvelope{})
	if strings.Contains(got, "My Personal Brain") {
		t.Fatalf("agent resolved to %q — the wiki's own name leaked into attribution again", got)
	}
	if got != DefaultAgentName {
		t.Errorf("agent = %q, want %q for an anonymous caller with no configured name", got, DefaultAgentName)
	}
}

func TestResolveAgentPrefersModernClientInfo(t *testing.T) {
	srv := newTestServer(t)
	srv.AgentName = "Configured Fallback"
	srv.stdioClient.set("Stdio Client 1.0")
	env := modernEnv(t, `{"name":"Claude Desktop","version":"1.4.2"}`)

	if got := srv.resolveAgent(&JSONRPCRequest{FromStdio: true}, env); got != "Claude Desktop 1.4.2" {
		t.Errorf("stdio: agent = %q, want the per-request clientInfo to win", got)
	}
	httpReq := &JSONRPCRequest{Headers: forwardedName("Sidecar Client")}
	if got := srv.resolveAgent(httpReq, env); got != "Claude Desktop 1.4.2" {
		t.Errorf("http: agent = %q, want the per-request clientInfo to beat a forwarded name", got)
	}
}

// forwardedName builds the headers a sidecar sends for a client name, encoded as it encodes them.
func forwardedName(name string) http.Header {
	return rawClientNameHeader(encodeHeaderValue(name))
}

// rawClientNameHeader sets the header to exactly value, as a hostile or careless client might.
func rawClientNameHeader(value string) http.Header {
	h := http.Header{}
	if value != "" {
		h.Set(clientNameHeader, value)
	}
	return h
}

func TestResolveAgentFallbackOrder(t *testing.T) {
	stdioReq := &JSONRPCRequest{FromStdio: true}
	tests := []struct {
		name       string
		stdio      string
		configured string
		req        *JSONRPCRequest
		env        paramsEnvelope
		want       string
	}{
		{
			name:       "stdio handshake beats configured name",
			stdio:      "Claude Desktop 1.4.2",
			configured: "Configured Fallback",
			req:        stdioReq,
			want:       "Claude Desktop 1.4.2",
		},
		{
			name:       "forwarded sidecar name beats configured name",
			configured: "Configured Fallback",
			req:        &JSONRPCRequest{Headers: forwardedName("reviewer")},
			want:       "reviewer",
		},
		{
			name:       "a non-ASCII forwarded name is decoded",
			configured: "Configured Fallback",
			req:        &JSONRPCRequest{Headers: forwardedName("Réviseur 日本")},
			want:       "Réviseur 日本",
		},
		{
			// A web primary serves stdio too. Its stdio client's handshake says nothing about who
			// sent an HTTP request, so crediting it would attribute one client's writes to another.
			name:       "stdio handshake is not used for an HTTP request",
			stdio:      "Claude Desktop 1.4.2",
			configured: "Configured Fallback",
			req:        &JSONRPCRequest{Headers: http.Header{}},
			want:       "Configured Fallback",
		},
		{
			name:       "a forwarded name that sanitizes to nothing falls through",
			configured: "Configured Fallback",
			req:        &JSONRPCRequest{Headers: rawClientNameHeader("\x1b\u200e \t")},
			want:       "Configured Fallback",
		},
		{
			name:       "configured name is used when nobody identifies themselves",
			configured: "Automation Script",
			req:        stdioReq,
			want:       "Automation Script",
		},
		{
			name: "anonymous caller falls back to the default",
			req:  &JSONRPCRequest{},
			want: DefaultAgentName,
		},
		{
			name:       "a configured name of control and zero-width characters falls back to the default",
			configured: "\x01\x1b\u200b\u200e\ufeff",
			req:        &JSONRPCRequest{Headers: http.Header{}},
			want:       DefaultAgentName,
		},
		{
			name:       "a configured name that sanitizes to nothing falls back to the default on stdio too",
			configured: "\x7f\u2060\u200d",
			req:        stdioReq,
			want:       DefaultAgentName,
		},
		{
			// Past maxAgentNameScanBytes of junk, the real text is never reached.
			name:       "a configured name whose text lies beyond the scan bound falls back to the default",
			configured: strings.Repeat("\u200b", maxAgentNameScanBytes/3+1) + "Automation Script",
			req:        &JSONRPCRequest{},
			want:       DefaultAgentName,
		},
		{
			name:       "empty modern clientInfo falls through rather than blanking attribution",
			configured: "Automation Script",
			req:        &JSONRPCRequest{},
			env:        modernEnv(t, `{"name":""}`),
			want:       "Automation Script",
		},
		{
			name:       "empty modern clientInfo falls through to a forwarded name",
			configured: "Automation Script",
			req:        &JSONRPCRequest{Headers: forwardedName("reviewer")},
			env:        modernEnv(t, `{"name":""}`),
			want:       "reviewer",
		},
		{
			name:       "malformed modern clientInfo falls through rather than failing",
			configured: "Automation Script",
			req:        &JSONRPCRequest{},
			env:        modernEnv(t, `"not-an-object"`),
			want:       "Automation Script",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			srv.AgentName = tc.configured
			if tc.stdio != "" {
				srv.stdioClient.set(tc.stdio)
			}
			if got := srv.resolveAgent(tc.req, tc.env); got != tc.want {
				t.Errorf("agent = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestForwardedClientNameIsSanitized covers the header's hostile inputs. The value is self-reported
// and lands in a durable log the UI renders, so it gets the same treatment as clientInfo: no
// control or invisible format characters, no invalid UTF-8, and a length ceiling — including when
// the characters arrive hidden inside the Base64 sentinel, which a raw-header check would miss.
func TestForwardedClientNameIsSanitized(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"plain name", "reviewer", "reviewer"},
		{"surrounding space is trimmed", "  reviewer  ", "reviewer"},
		{"escape sequences are stripped", "evil\x1b[31mred", "evil[31mred"},
		{"bidi overrides are stripped", "abc\u202edcba", "abcdcba"},
		{"control characters inside Base64 are stripped", "=?base64?" + base64Encode("line1\nline2\x00\x7f") + "?=", "line1 line2"},
		{"invalid UTF-8 is dropped", "ok\xff\xfename", "okname"},
		{"absent", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := forwardedClientName(rawClientNameHeader(tc.raw)); got != tc.want {
				t.Errorf("forwardedClientName(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}

	long := forwardedClientName(forwardedName(strings.Repeat("日", 200)))
	if len(long) > maxAgentNameBytes || !utf8.ValidString(long) || long == "" {
		t.Errorf("a long name must be cut to at most %d bytes on a rune boundary, got %d bytes (valid UTF-8: %t)",
			maxAgentNameBytes, len(long), utf8.ValidString(long))
	}
}

// TestLegacyInitializeCapturesClientOnStdioOnly covers the transport asymmetry.
//
// Stdio is one process talking to one client, so a handshake can be remembered for the connection.
// HTTP is sessionless — §3.4 records that sessions were removed from the spec and that NexWiki
// correctly ignores Mcp-Session-Id — so caching a legacy `initialize` there would attribute one
// client's writes to another.
func TestLegacyInitializeCapturesClientOnStdioOnly(t *testing.T) {
	initParams := json.RawMessage(`{"protocolVersion":"2025-06-18","clientInfo":{"name":"Cursor","version":"0.9"}}`)

	t.Run("stdio remembers", func(t *testing.T) {
		srv := newTestServer(t)
		var out strings.Builder
		srv.handleRequest(&out, &JSONRPCRequest{
			JSONRPC: "2.0", Method: "initialize", ID: 1, Params: initParams, FromStdio: true,
		})
		if got := srv.resolveAgent(&JSONRPCRequest{FromStdio: true}, paramsEnvelope{}); got != "Cursor 0.9" {
			t.Errorf("agent = %q, want %q after a stdio handshake", got, "Cursor 0.9")
		}
		if got := srv.resolveAgent(&JSONRPCRequest{Headers: http.Header{}}, paramsEnvelope{}); got != DefaultAgentName {
			t.Errorf("agent = %q, want %q — a stdio handshake must not credit HTTP callers", got, DefaultAgentName)
		}
	})

	t.Run("http does not", func(t *testing.T) {
		srv := newTestServer(t)
		var out strings.Builder
		srv.handleRequest(&out, &JSONRPCRequest{
			JSONRPC: "2.0", Method: "initialize", ID: 1, Params: initParams, // FromStdio false
		})
		if got := srv.resolveAgent(&JSONRPCRequest{Headers: http.Header{}}, paramsEnvelope{}); got != DefaultAgentName {
			t.Errorf("agent = %q, want %q — an HTTP handshake must not be cached for later callers",
				got, DefaultAgentName)
		}
	})
}

func TestDescribeClient(t *testing.T) {
	tests := []struct {
		name string
		in   clientInfo
		want string
	}{
		{"name and version", clientInfo{Name: "Claude Desktop", Version: "1.4.2"}, "Claude Desktop 1.4.2"},
		{"name only", clientInfo{Name: "Cursor"}, "Cursor"},
		{"title when name is absent", clientInfo{Title: "Some Client"}, "Some Client"},
		{"nothing usable", clientInfo{Version: "1.0"}, ""},
		{"whitespace is not a name", clientInfo{Name: "   "}, ""},
		{"newlines are flattened", clientInfo{Name: "Evil\nClient"}, "Evil Client"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := describeClient(tc.in); got != tc.want {
				t.Errorf("describeClient(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestAgentNameIsBounded keeps a self-reported value from writing an unbounded string into a
// durable append-only log that the UI renders.
func TestAgentNameIsBounded(t *testing.T) {
	long := strings.Repeat("A", 500)
	got := describeClient(clientInfo{Name: long})
	if len(got) > maxAgentNameBytes {
		t.Errorf("agent name is %d bytes, want at most %d", len(got), maxAgentNameBytes)
	}

	// Multi-byte input must not be cut mid-character.
	multi := describeClient(clientInfo{Name: strings.Repeat("日", 200)})
	if !utf8Valid(multi) {
		t.Error("truncation split a multi-byte character")
	}
}

// referenceAgentName is truncateAgentName as it was before it was made linear: the behavior the
// rewrite must keep. Its rune-at-a-time trim is quadratic, so it is only ever fed short inputs.
func referenceAgentName(name string) string {
	name = strings.ToValidUTF8(name, "")
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if len(name) > maxAgentNameBytes {
		trimmed := []rune(name)
		for len(string(trimmed)) > maxAgentNameBytes {
			trimmed = trimmed[:len(trimmed)-1]
		}
		name = string(trimmed)
	}
	return name
}

// TestTruncateAgentNameMatchesReference pins that the linear sanitizer produces exactly what the
// original did: on hand-picked cases around the byte cap and every class of stripped character,
// and on generated inputs up to the scan bound mixing all of them.
func TestTruncateAgentNameMatchesReference(t *testing.T) {
	pad := func(n int) string { return strings.Repeat("a", n) }
	cases := []string{
		"", "   ", "Claude Desktop 1.4.2", "  padded  ", "Evil\nClient", "tab\there", "cr\rlf",
		"esc\x1b[31mred", "nul\x00byte", "del\x7f", "nel\u0085x", "bidi\u202eoverride", "zero\u200bwidth",
		"bom\ufeffx", "nbsp\u00a0inside", "\u00a0nbsp at ends\u00a0", "literal \ufffd kept", "bad\xffbyte",
		"truncated \xc3", "surrogate \xed\xa0\x80 half", "日本語 クライアント", "emoji 🙂 client",
		pad(119), pad(120), pad(121), pad(119) + "é", pad(118) + "日", pad(119) + "日", pad(117) + "🙂",
		pad(118) + "🙂x", pad(119) + " x", pad(120) + "   ", "  " + pad(125), pad(119) + "\u202e" + "bb",
		"\n\n" + pad(200), strings.Repeat("日", 200), strings.Repeat("🙂", 100),
		// Longer than the scan bound, but ordinary: the result depends only on the start.
		"Claude Desktop " + strings.Repeat("x", 10_000), strings.Repeat("日", 5000), strings.Repeat(" ", 100) + pad(5000),
	}
	for _, in := range cases {
		if got, want := truncateAgentName(in), referenceAgentName(in); got != want {
			t.Errorf("truncateAgentName(%q) = %q, want %q", in, got, want)
		}
	}

	pieces := []string{"a", "Z", "7", " ", "-", "\n", "\r", "\t", "\x00", "\x1b", "\x7f", "\u0085", "\u00a0",
		"\u200b", "\u202e", "\ufeff", "\u2028", "é", "日", "🙂", "\ufffd", "\xff", "\xc3", "\xe6\x97", "\xed\xa0\x80"}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 3000; i++ {
		var b strings.Builder
		size := rng.IntN(maxAgentNameScanBytes)
		for b.Len() < size {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		in := b.String()
		if len(in) > maxAgentNameScanBytes {
			in = in[:maxAgentNameScanBytes]
		}
		if got, want := truncateAgentName(in), referenceAgentName(in); got != want {
			t.Fatalf("generated input %q: got %q, want %q", in, got, want)
		}
	}
}

// TestAgentNameSanitizingTimeIsBounded is the regression guard for sanitizing in time proportional
// to the square of the input. Every entry point for a self-reported name is fed 1 MB and 8 MB: 8 MB
// is the most an MCP request body may carry, and 1 MB the most a request header may, so the header
// entry points stop there. Each must finish far inside the budget, which includes decoding the JSON
// or Base64 around the name; the quadratic version took minutes for a few hundred kilobytes. The
// work runs on its own goroutine so a regression fails at the deadline instead of hanging the suite.
func TestAgentNameSanitizingTimeIsBounded(t *testing.T) {
	// Generous: the slowest case, decoding the 8 MB initialize JSON under -race, can take about a
	// second on a loaded machine, while a quadratic regression takes minutes.
	const budget = 10 * time.Second
	within := func(t *testing.T, name string, run func() string) {
		t.Helper()
		done := make(chan string, 1)
		start := time.Now()
		go func() { done <- run() }()
		select {
		case got := <-done:
			if len(got) == 0 || len(got) > maxAgentNameBytes || !utf8.ValidString(got) {
				t.Errorf("%s: got a %d-byte name (valid UTF-8: %t)", name, len(got), utf8.ValidString(got))
			}
			t.Logf("%s: %v", name, time.Since(start))
		case <-time.After(budget):
			t.Fatalf("%s: sanitizing did not finish within %v", name, budget)
		}
	}

	srv := newTestServer(t)
	for _, size := range []int{1 << 20, 8 << 20} {
		// A name of multi-byte runes, so the rune boundary at the cap is exercised too. The JSON
		// documents carry an ASCII one of the same size instead: decoding megabytes of multi-byte
		// JSON under -race costs more than the sanitizing being measured, and makes the budget flaky.
		name := strings.Repeat("日", (size-64)/3)
		clientJSON := `{"name":"` + strings.Repeat("A", size-64) + `","version":"1.0"}`
		env := modernEnv(t, clientJSON)
		initParams := json.RawMessage(`{"clientInfo":` + clientJSON + `}`)
		label := func(entry string) string { return entry + "/" + strconv.Itoa(size>>20) + "MB" }

		within(t, label("sanitizer"), func() string { return truncateAgentName(name) })
		if size <= 1<<20 {
			within(t, label("header"), func() string { return forwardedClientName(rawClientNameHeader(name)) })
			within(t, label("Base64 header"), func() string { return forwardedClientName(forwardedName(name)) })
		}
		within(t, label("_meta clientInfo"), func() string { return srv.resolveAgent(&JSONRPCRequest{}, env) })
		within(t, label("initialize clientInfo"), func() string {
			var identity agentIdentity
			identity.rememberInitialize(initParams)
			return identity.get()
		})
		within(t, label("-agent-name"), func() string {
			return (&Server{AgentName: name}).resolveAgent(&JSONRPCRequest{}, paramsEnvelope{})
		})
		within(t, label("sidecar -agent-name"), func() string { return NewMCPProxy("1", name, io.Discard).agentName })
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

func TestResolveConfiguredAgentNameEnvWins(t *testing.T) {
	t.Setenv("NEXWIKI_AGENT_NAME", "From Env")
	if got := ResolveConfiguredAgentName("From Flag"); got != "From Env" {
		t.Errorf("got %q, want the environment variable to take precedence", got)
	}

	t.Setenv("NEXWIKI_AGENT_NAME", "")
	if got := ResolveConfiguredAgentName("From Flag"); got != "From Flag" {
		t.Errorf("got %q, want the flag when the env var is unset", got)
	}
}

// TestResolveConfiguredAgentNameIgnoresAnEnvValueThatSanitizesToNothing pins that an env value
// made only of characters the sanitizer strips doesn't displace a usable flag: every consumer
// sanitizes the result, so it would resolve to DefaultAgentName instead of the flag's name.
func TestResolveConfiguredAgentNameIgnoresAnEnvValueThatSanitizesToNothing(t *testing.T) {
	for _, env := range []string{
		"\u200b\u200e",         // zero-width space and left-to-right mark: format characters
		" \x1b\x07 ",           // control characters between spaces
		"\t\u2060\n",           // a word joiner between whitespace
		"\u200b \u202e \u00a0", // format characters separated by spaces
	} {
		t.Run(strconv.QuoteToASCII(env), func(t *testing.T) {
			t.Setenv("NEXWIKI_AGENT_NAME", env)
			got := ResolveConfiguredAgentName("From Flag")
			if got != "From Flag" {
				t.Fatalf("got %q, want the flag when the env var sanitizes to nothing", got)
			}
			srv := &Server{AgentName: got}
			if agent := srv.resolveAgent(&JSONRPCRequest{}, paramsEnvelope{}); agent != "From Flag" {
				t.Errorf("resolveAgent = %q, want the flag's name", agent)
			}
			if proxied := NewMCPProxy("1", got, io.Discard).agentName; proxied != "From Flag" {
				t.Errorf("sidecar agent name = %q, want the flag's name", proxied)
			}
		})
	}

	// A name with visible text still wins, surrounded by stripped characters or not.
	t.Setenv("NEXWIKI_AGENT_NAME", "\u200bFrom Env ")
	if got := truncateAgentName(ResolveConfiguredAgentName("From Flag")); got != "From Env" {
		t.Errorf("got %q, want the environment variable's visible name to take precedence", got)
	}
}

// --- the history join -------------------------------------------------------------------------

func TestAttributeRevisionsJoinsTheActivityLog(t *testing.T) {
	dir := t.TempDir()
	logPath := ActivityLogPath(dir)
	al, err := OpenActivityLog(dir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })

	v1 := time.Now().Add(-2 * time.Hour)
	v2 := time.Now().Add(-1 * time.Hour)

	for _, ev := range []LogEvent{
		{Timestamp: v1, Source: "mcp", Action: "create", Tool: "create_wiki_article", Slug: "notes", Agent: "Claude Desktop 1.4.2"},
		{Timestamp: v2, Source: "api", Action: "edit", Slug: "notes", Agent: "User"},
		// Same moment, different article: must not bleed across slugs.
		{Timestamp: v2, Source: "mcp", Action: "edit", Tool: "edit_wiki_article", Slug: "other", Agent: "Cursor 0.9"},
		// A read at the same moment must never be credited as the writer.
		{Timestamp: v2, Source: "mcp", Action: "read", Tool: "read_article", Slug: "notes", Agent: "Some Reader"},
	} {
		if err := al.Append(ev); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	got := attributeRevisions(logPath, "notes", []RevisionRef{
		{Version: 1, Timestamp: v1},
		{Version: 2, Timestamp: v2},
	})

	if got[0].Agent != "Claude Desktop 1.4.2" || got[0].Tool != "create_wiki_article" || got[0].Via != "mcp" {
		t.Errorf("v1 = %+v, want the Claude Desktop create", got[0])
	}
	if got[1].Agent != "User" || got[1].Via != "api" {
		t.Errorf("v2 = %+v, want the web-UI edit, not the read at the same moment", got[1])
	}
}

// TestAttributeRevisionsDegradesGracefully is the case that matters for any wiki older than its
// activity log — which is every wiki that existed before this feature. No attribution is the
// correct answer; a wrong one would be worse.
func TestAttributeRevisionsDegradesGracefully(t *testing.T) {
	dir := t.TempDir()
	logPath := ActivityLogPath(dir)

	revisions := []RevisionRef{{Version: 1, Timestamp: time.Now().Add(-72 * time.Hour)}}

	t.Run("no log at all", func(t *testing.T) {
		got := attributeRevisions(logPath, "notes", revisions)
		if got[0].Agent != "" {
			t.Errorf("agent = %q, want empty when there is no activity log", got[0].Agent)
		}
	})

	t.Run("log exists but predates the revision", func(t *testing.T) {
		al, err := OpenActivityLog(dir)
		if err != nil {
			t.Fatalf("OpenActivityLog failed: %v", err)
		}
		defer func() { _ = al.Close() }()
		if err := al.Append(LogEvent{
			Timestamp: time.Now(), Source: "mcp", Action: "edit", Slug: "notes", Agent: "Recent Agent",
		}); err != nil {
			t.Fatalf("Append failed: %v", err)
		}

		got := attributeRevisions(logPath, "notes", revisions)
		if got[0].Agent != "" {
			t.Errorf("agent = %q, want empty — an event three days later is not this revision",
				got[0].Agent)
		}
	})
}

// TestGetArticleHistoryReportsAttribution exercises the whole path end to end through the MCP tool,
// which is what §6.9 actually asked for: "Claude wrote this on <date>, citing X".
func TestGetArticleHistoryReportsAttribution(t *testing.T) {
	srv := newTestServer(t)

	al, err := OpenActivityLog(srv.Storage.DataDir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })

	art, err := srv.Storage.SaveArticle("", "Attributed Notes", "# One", "desc",
		"An external citation", "", "first", nil, ContentTypeWiki)
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	if _, err := srv.Storage.SaveArticle("", "Attributed Notes", "# Two", "desc",
		"An external citation", "", "second", nil, ContentTypeWiki); err != nil {
		t.Fatalf("second SaveArticle failed: %v", err)
	}

	history, err := srv.Storage.GetArticleHistory(art.Slug)
	if err != nil {
		t.Fatalf("GetArticleHistory failed: %v", err)
	}
	if len(history) == 0 {
		t.Fatal("no revisions were stored")
	}
	// Log an event lined up with the newest stored revision.
	if err := al.Append(LogEvent{
		Timestamp: history[0].Timestamp, Source: "mcp", Action: "edit",
		Tool: "edit_wiki_article", Slug: art.Slug, Agent: "Claude Desktop 1.4.2",
	}); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	raw, rpcErr := srv.toolGetArticleHistory(json.RawMessage(`{"slug":"` + art.Slug + `"}`))
	if rpcErr != nil {
		t.Fatalf("get_article_history failed: %v", rpcErr)
	}
	resp := raw.(ToolResponse)
	out := resp.StructuredContent.(HistoryOutput)

	if out.Source != "An external citation" {
		t.Errorf("Source = %q, want the article's provenance field", out.Source)
	}
	found := false
	for _, v := range out.Versions {
		if v.Agent == "Claude Desktop 1.4.2" && v.Tool == "edit_wiki_article" && v.Via == "mcp" {
			found = true
		}
	}
	if !found {
		t.Errorf("no revision carried the expected attribution; got %+v", out.Versions)
	}
	if !strings.Contains(resp.Content[0].Text, "By: Claude Desktop 1.4.2") {
		t.Errorf("prose does not name the agent:\n%s", resp.Content[0].Text)
	}
}

// TestRapidRevisionsAreNotAllCreditedToOneEvent is the regression guard for a defect found by
// behavioral testing rather than by reading the code.
//
// Article timestamps are stored at RFC3339 *second* resolution, and an agent editing a document
// produces several revisions well inside one second. Independently picking the "nearest" event per
// revision therefore handed the same event to several of them: in the live probe a create and the
// edit 129 ms after it were both credited to the create. Assignment must be one-to-one.
func TestRapidRevisionsAreNotAllCreditedToOneEvent(t *testing.T) {
	dir := t.TempDir()
	al, err := OpenActivityLog(dir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })

	// Three writes inside one second, then revision timestamps truncated to the second — exactly
	// what lands on disk.
	base := time.Date(2026, 8, 9, 20, 25, 11, 0, time.UTC)
	for i, ev := range []LogEvent{
		{Timestamp: base.Add(10 * time.Millisecond), Source: "mcp", Action: "create", Tool: "create_wiki_article", Slug: "rapid", Agent: "Claude Desktop 1.4.2"},
		{Timestamp: base.Add(139 * time.Millisecond), Source: "mcp", Action: "edit", Tool: "edit_wiki_article", Slug: "rapid", Agent: "Cursor 0.9"},
		{Timestamp: base.Add(326 * time.Millisecond), Source: "mcp", Action: "edit", Tool: "edit_wiki_article", Slug: "rapid", Agent: "Opencode 2.1"},
	} {
		if err := al.Append(ev); err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}

	// Newest-first, because that is what GetArticleHistory returns — it sorts by version
	// descending. The first version of this test built the slice ascending, which quietly made it
	// agree with the input order instead of testing anything: the bug it was meant to catch
	// (assignment falling back to input order when timestamps tie) shipped, and turned up in the
	// pre-tag smoke test with every revision's author reversed.
	got := attributeRevisions(ActivityLogPath(dir), "rapid", []RevisionRef{
		{Version: 3, Timestamp: base},
		{Version: 2, Timestamp: base},
		{Version: 1, Timestamp: base},
	})

	want := map[int]string{
		1: "Claude Desktop 1.4.2", // the create, earliest event
		2: "Cursor 0.9",
		3: "Opencode 2.1", // the latest edit, latest event
	}
	seen := map[string]int{}
	for _, v := range got {
		if v.Agent != want[v.Version] {
			t.Errorf("v%d attributed to %q, want %q", v.Version, v.Agent, want[v.Version])
		}
		seen[v.Agent]++
	}
	for agent, n := range seen {
		if n > 1 {
			t.Errorf("%q was credited with %d revisions; each log event may be used at most once", agent, n)
		}
	}
}

// TestFailedToolCallsAreNotLogged is the regression guard for §3.19.
//
// A tool that refuses its work returns ToolResponse{IsError: true} inside a perfectly well-formed
// JSON-RPC *result*, not a JSON-RPC error. The logging hook only checked for a JSON-RPC error, so
// every refusal was recorded as a completed write. Measured live: an edit rejected by optimistic
// locking left the article at version 1 and still appeared in the activity log as an edit.
func TestFailedToolCallsAreNotLogged(t *testing.T) {
	srv := newTestServer(t)
	logPath := ActivityLogPath(srv.Storage.DataDir)
	al, err := OpenActivityLog(srv.Storage.DataDir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })
	srv.EventBus.SetPersist(func(ev LogEvent) { _ = al.Append(ev) })

	if _, rpcErr := srv.executeToolCall(json.RawMessage(
		`{"name":"create_wiki_article","arguments":{"title":"Locking Probe","content":"# v1","description":"d"}}`),
		"Claude Desktop 1.4.2"); rpcErr != nil {
		t.Fatalf("create failed: %v", rpcErr)
	}

	// A deliberately stale loaded_version. The tool must refuse, and the refusal must not be logged.
	result, rpcErr := srv.executeToolCall(json.RawMessage(
		`{"name":"edit_wiki_article","arguments":{"slug":"locking-probe","title":"Locking Probe","content":"# vX","loaded_version":99}}`),
		"Cursor 0.9")
	if rpcErr != nil {
		t.Fatalf("unexpected JSON-RPC error: %v", rpcErr)
	}
	if !isToolError(result) {
		t.Fatal("a stale loaded_version was accepted; this test can no longer detect the defect")
	}

	events, err := ReadActivityLogFiltered(logPath, ActivityFilter{Slug: "locking-probe"})
	if err != nil {
		t.Fatalf("reading the log failed: %v", err)
	}
	for _, ev := range events {
		if ev.Agent == "Cursor 0.9" {
			t.Errorf("the activity log records a %q by %q that was rejected and never happened",
				ev.Action, ev.Agent)
		}
	}
}

// TestActivityFilterBySlug covers the filter added for the join, including that it does not disturb
// the existing action/source filtering.
func TestActivityFilterBySlug(t *testing.T) {
	dir := t.TempDir()
	al, err := OpenActivityLog(dir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })

	now := time.Now()
	for _, ev := range []LogEvent{
		{Timestamp: now, Source: "mcp", Action: "edit", Slug: "alpha", Agent: "A"},
		{Timestamp: now, Source: "api", Action: "edit", Slug: "beta", Agent: "B"},
		{Timestamp: now, Source: "mcp", Action: "read", Slug: "alpha", Agent: "C"},
	} {
		if err := al.Append(ev); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	events, err := ReadActivityLogFiltered(ActivityLogPath(dir), ActivityFilter{Slug: "alpha"})
	if err != nil {
		t.Fatalf("ReadActivityLogFiltered failed: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("slug filter returned %d events, want 2", len(events))
	}

	events, err = ReadActivityLogFiltered(ActivityLogPath(dir), ActivityFilter{Slug: "alpha", Action: "edit"})
	if err != nil {
		t.Fatalf("ReadActivityLogFiltered failed: %v", err)
	}
	if len(events) != 1 || events[0].Agent != "A" {
		t.Errorf("slug+action filter returned %+v, want just the alpha edit", events)
	}
}

package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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

	got := srv.resolveAgent(paramsEnvelope{})
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

	got := srv.resolveAgent(modernEnv(t, `{"name":"Claude Desktop","version":"1.4.2"}`))
	if got != "Claude Desktop 1.4.2" {
		t.Errorf("agent = %q, want the per-request clientInfo to win", got)
	}
}

func TestResolveAgentFallbackOrder(t *testing.T) {
	tests := []struct {
		name       string
		stdio      string
		configured string
		env        paramsEnvelope
		want       string
	}{
		{
			name:       "stdio handshake beats configured name",
			stdio:      "Claude Desktop 1.4.2",
			configured: "Configured Fallback",
			want:       "Claude Desktop 1.4.2",
		},
		{
			name:       "configured name is used when nobody identifies themselves",
			configured: "Automation Script",
			want:       "Automation Script",
		},
		{
			name: "anonymous caller falls back to the default",
			want: DefaultAgentName,
		},
		{
			name:       "empty modern clientInfo falls through rather than blanking attribution",
			configured: "Automation Script",
			env:        modernEnv(t, `{"name":""}`),
			want:       "Automation Script",
		},
		{
			name:       "malformed modern clientInfo falls through rather than failing",
			configured: "Automation Script",
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
			if got := srv.resolveAgent(tc.env); got != tc.want {
				t.Errorf("agent = %q, want %q", got, tc.want)
			}
		})
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
		if got := srv.resolveAgent(paramsEnvelope{}); got != "Cursor 0.9" {
			t.Errorf("agent = %q, want %q after a stdio handshake", got, "Cursor 0.9")
		}
	})

	t.Run("http does not", func(t *testing.T) {
		srv := newTestServer(t)
		var out strings.Builder
		srv.handleRequest(&out, &JSONRPCRequest{
			JSONRPC: "2.0", Method: "initialize", ID: 1, Params: initParams, // FromStdio false
		})
		if got := srv.resolveAgent(paramsEnvelope{}); got != DefaultAgentName {
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

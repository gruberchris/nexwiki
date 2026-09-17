package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// proxyAgainstPrimary wires a real NexWiki server behind a real HTTP listener and points a proxy
// at it, which is the whole point of the feature: verifying the pipe end to end rather than
// asserting on a mock.
func proxyAgainstPrimary(t *testing.T) (*Server, *MCPProxy, *syncBuffer) {
	t.Helper()
	primary := resourceServer(t)

	httpSrv := httptest.NewServer(http.HandlerFunc(primary.HandleStreamableHTTP))
	t.Cleanup(httpSrv.Close)

	out := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	proxy := &MCPProxy{endpoint: httpSrv.URL, client: &http.Client{}, out: out, shutdown: ctx, stop: cancel}
	return primary, proxy, out
}

// syncBuffer is a mutex-guarded sink, since relayed subscription notifications are written from
// their own goroutine while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) messages(t *testing.T) []map[string]interface{} {
	t.Helper()
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("proxy emitted a non-JSON line: %q", line)
		}
		out = append(out, msg)
	}
	return out
}

// TestProxyForwardsLegacyCalls covers the ordinary path: a stdio client's request reaches the
// primary and its answer comes back on stdout.
func TestProxyForwardsLegacyCalls(t *testing.T) {
	_, proxy, out := proxyAgainstPrimary(t)

	proxy.Run(strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{}}` + "\n"))

	msgs := out.messages(t)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 responses, got %d:\n%s", len(msgs), out.String())
	}

	tools := msgs[0]["result"].(map[string]interface{})["tools"].([]interface{})
	if len(tools) != 29 {
		t.Errorf("proxied tools/list returned %d tools, want 29", len(tools))
	}
	if msgs[0]["id"].(float64) != 1 || msgs[1]["id"].(float64) != 2 {
		t.Error("responses must preserve their request ids and order")
	}
}

// TestProxySynthesizesModernHeaders is the subtle part of the conversion.
//
// stdio carries no headers, but a modern request over HTTP MUST mirror MCP-Protocol-Version,
// Mcp-Method, and Mcp-Name into headers matching the body — the primary rejects a mismatch with
// -32020. The proxy has to derive them from the payload it is forwarding, and a tools/call is the
// case that needs all three.
func TestProxySynthesizesModernHeaders(t *testing.T) {
	_, proxy, out := proxyAgainstPrimary(t)

	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"` + ModernProtocolVersion + `",` +
		`"io.modelcontextprotocol/clientCapabilities":{}}`
	proxy.Run(strings.NewReader(
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_status_tags","arguments":{},` + meta + `}}` + "\n"))

	msgs := out.messages(t)
	if len(msgs) != 1 {
		t.Fatalf("expected one response, got %d:\n%s", len(msgs), out.String())
	}
	if errObj, isErr := msgs[0]["error"]; isErr {
		t.Fatalf("modern call through the proxy was rejected — headers were not mirrored: %v", errObj)
	}
	result := msgs[0]["result"].(map[string]interface{})
	if result["resultType"] != "complete" {
		t.Errorf("expected a modern result envelope, got %v", result)
	}
}

// TestProxyHeaderSynthesisPerMethod pins which headers each method gets, without needing a server.
func TestProxyHeaderSynthesisPerMethod(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proxy := &MCPProxy{endpoint: "http://127.0.0.1:1/api/mcp", shutdown: ctx, stop: cancel}
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"` + ModernProtocolVersion + `",` +
		`"io.modelcontextprotocol/clientCapabilities":{}}`

	tests := []struct {
		name     string
		payload  string
		wantName string
	}{
		{"tools/call mirrors the tool name", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_wiki",` + meta + `}}`, "search_wiki"},
		{"prompts/get mirrors the prompt name", `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"article_creation_workflow",` + meta + `}}`, "article_creation_workflow"},
		{"resources/read mirrors the uri", `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"nexwiki://article/go",` + meta + `}}`, "nexwiki://article/go"},
		{"tools/list needs no name header", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + meta + `}}`, ""},
		// completion/complete names its target inside `ref`, not in `params.name`, so the
		// specification does not mirror it into Mcp-Name. A proxy that guessed otherwise would
		// synthesize a header the primary then rejects as a mismatch.
		{"completion/complete needs no name header",
			`{"jsonrpc":"2.0","id":1,"method":"completion/complete","params":{` +
				`"ref":{"type":"ref/resource","uri":"nexwiki://article/{slug}"},` +
				`"argument":{"name":"slug","value":"go"},` + meta + `}}`, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := proxy.newRequest([]byte(tc.payload))
			if err != nil {
				t.Fatalf("newRequest failed: %v", err)
			}
			if got := req.Header.Get("MCP-Protocol-Version"); got != ModernProtocolVersion {
				t.Errorf("MCP-Protocol-Version = %q", got)
			}
			if got := req.Header.Get("Mcp-Name"); got != tc.wantName {
				t.Errorf("Mcp-Name = %q, want %q", got, tc.wantName)
			}
		})
	}

	// A legacy request carries no _meta, so no mirrored headers are required or sent.
	req, err := proxy.newRequest([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	if err != nil {
		t.Fatalf("newRequest failed: %v", err)
	}
	if req.Header.Get("MCP-Protocol-Version") != "" {
		t.Error("legacy requests must not gain modern headers")
	}
}

// TestHeaderValueEncodingRoundTrip pins that the encode side matches the decoder the primary uses.
func TestHeaderValueEncodingRoundTrip(t *testing.T) {
	for _, value := range []string{"search_wiki", "nexwiki://article/go", "héllo wörld", " padded ", "=?base64?literal?="} {
		encoded := encodeHeaderValue(value)
		if decoded := decodeHeaderValue(encoded); decoded != value {
			t.Errorf("round trip failed for %q: encoded %q decoded %q", value, encoded, decoded)
		}
	}
	// A plain ASCII value should travel unencoded, so headers stay readable to intermediaries.
	if encodeHeaderValue("search_wiki") != "search_wiki" {
		t.Error("plain values should not be Base64-encoded")
	}
}

// TestProxyRelaysSubscriptionStream covers the sidecar's half of subscriptions: notifications
// originate in the primary, which owns the data directory, and the proxy relays each SSE frame to
// stdout as its own JSON-RPC line. A standalone stdio server now serves subscriptions from its own
// EventBus instead (see TestConformanceStdioSubscriptionStreams); the relay is what a sidecar needs,
// because only one process may hold the search index lock.
func TestProxyRelaysSubscriptionStream(t *testing.T) {
	primary, proxy, out := proxyAgainstPrimary(t)

	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		proxy.Run(reader)
	}()

	_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":9,"method":"subscriptions/listen","params":{"notifications":{` +
		`"resourceSubscriptions":["nexwiki://article/watched"]}}}` + "\n"))

	// Wait for the acknowledgment before publishing, so the stream is provably registered.
	waitFor(t, out, "notifications/subscriptions/acknowledged")

	primary.EventBus.PublishWikiUpdate(WikiUpdate{Type: "article-edited", Slug: "watched", Title: "Watched"})
	waitFor(t, out, "notifications/resources/updated")

	_ = writer.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}

	// Every relayed line must be a standalone JSON-RPC message: the stdout writer is shared with
	// the response path, so an interleaved write would corrupt the stream.
	for _, msg := range out.messages(t) {
		if msg["jsonrpc"] != "2.0" {
			t.Errorf("relayed line is not a JSON-RPC message: %v", msg)
		}
	}
}

// waitFor blocks until the proxy's output contains a message with the given method.
func waitFor(t *testing.T, out *syncBuffer, method string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), `"`+method+`"`) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; got:\n%s", method, out.String())
}

// TestProxyReportsUnreachablePrimary pins that a dead primary produces a correlated JSON-RPC error
// rather than silence, so the client can attribute the failure to the call it made.
func TestProxyReportsUnreachablePrimary(t *testing.T) {
	out := &syncBuffer{}
	// Port 1 is reserved and never listening.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proxy := &MCPProxy{endpoint: "http://127.0.0.1:1/api/mcp", client: &http.Client{Timeout: time.Second}, out: out, shutdown: ctx, stop: cancel}

	proxy.Run(strings.NewReader(`{"jsonrpc":"2.0","id":77,"method":"tools/list","params":{}}` + "\n"))

	msgs := out.messages(t)
	if len(msgs) != 1 {
		t.Fatalf("expected one error response, got %d", len(msgs))
	}
	if msgs[0]["id"].(float64) != 77 {
		t.Errorf("error must carry the request id so the client can correlate it, got %v", msgs[0]["id"])
	}
	errObj := msgs[0]["error"].(map[string]interface{})
	if !strings.Contains(errObj["message"].(string), "unreachable") {
		t.Errorf("expected an unreachable-primary message, got %v", errObj["message"])
	}
}

// TestProxyDropsNotificationReplies pins JSON-RPC's rule that a notification gets no response.
func TestProxyDropsNotificationReplies(t *testing.T) {
	_, proxy, out := proxyAgainstPrimary(t)

	proxy.Run(strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))

	if trimmed := strings.TrimSpace(out.String()); trimmed != "" {
		t.Errorf("a notification must produce no reply, got: %s", trimmed)
	}
}

// TestProxyHandlesLargePayloads guards the scanner buffer: MCP payloads carry whole article
// bodies, which overrun bufio's default 64 KB line limit.
func TestProxyHandlesLargePayloads(t *testing.T) {
	_, proxy, out := proxyAgainstPrimary(t)

	big := strings.Repeat("x", 200_000)
	payload := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_wiki_article",` +
		`"arguments":{"title":"Big Page","content":"` + big + `"}}}`

	proxy.Run(strings.NewReader(payload + "\n"))

	msgs := out.messages(t)
	if len(msgs) != 1 {
		t.Fatalf("expected one response, got %d", len(msgs))
	}
	if _, isErr := msgs[0]["error"]; isErr {
		t.Fatalf("large payload was rejected: %v", msgs[0]["error"])
	}
}

// scannerLineLimitIsRaised documents the buffer choice by exercising it directly.
func TestScannerBufferAcceptsLongLines(t *testing.T) {
	long := strings.Repeat("y", 500_000)
	scanner := bufio.NewScanner(strings.NewReader(long + "\n"))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	if !scanner.Scan() {
		t.Fatalf("raised buffer should accept a 500 KB line: %v", scanner.Err())
	}
	if len(scanner.Text()) != len(long) {
		t.Errorf("line truncated to %d bytes", len(scanner.Text()))
	}
}

// persistPrimaryActivity wires a durable activity log into the primary, as main.go does, and returns
// its path so a test can read back what the primary recorded.
func persistPrimaryActivity(t *testing.T, primary *Server) string {
	t.Helper()
	al, err := OpenActivityLog(primary.Storage.DataDir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })
	primary.EventBus.SetPersist(func(ev LogEvent) {
		if err := al.Append(ev); err != nil {
			t.Errorf("failed to persist activity event: %v", err)
		}
	})
	return ActivityLogPath(primary.Storage.DataDir)
}

// TestProxyAttributesToolCallsToTheStdioClient is the regression guard for tool calls through a
// sidecar being credited to the primary's fallback. The primary never sees the stdio client's
// `initialize` — it receives each call over sessionless HTTP — so the proxy has to carry the name.
//
// Each case checks the name the primary's durable log records for a tool call, and between them
// they pin the whole precedence: per-request clientInfo, then the stdio client's handshake, then
// the sidecar's own -agent-name, then the primary's.
func TestProxyAttributesToolCallsToTheStdioClient(t *testing.T) {
	const listCall = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_articles","arguments":{}}}`
	modernCall := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_articles","arguments":{},` +
		`"_meta":{"io.modelcontextprotocol/protocolVersion":"` + ModernProtocolVersion + `",` +
		`"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"Modern Client"}}}}`

	tests := []struct {
		name         string
		sidecarAgent string
		input        []string
		want         string
	}{
		{
			name:         "legacy initialize clientInfo",
			sidecarAgent: "Sidecar Fallback",
			input: []string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"reviewer"}}}`,
				`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
				listCall,
			},
			want: "reviewer",
		},
		{
			name:         "non-ASCII clientInfo survives the header",
			sidecarAgent: "Sidecar Fallback",
			input: []string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"Réviseur","version":"2.0"}}}`,
				listCall,
			},
			want: "Réviseur 2.0",
		},
		{
			name:         "sidecar -agent-name when the client sends no clientInfo",
			sidecarAgent: "Sidecar Fallback",
			input: []string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
				listCall,
			},
			want: "Sidecar Fallback",
		},
		{
			name: "primary -agent-name when the sidecar knows no name",
			input: []string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
				listCall,
			},
			want: "Primary Fallback",
		},
		{
			name:         "modern per-request clientInfo beats the forwarded name",
			sidecarAgent: "Sidecar Fallback",
			input: []string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"reviewer"}}}`,
				modernCall,
			},
			want: "Modern Client",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			primary, proxy, out := proxyAgainstPrimary(t)
			primary.AgentName = "Primary Fallback"
			proxy.agentName = tc.sidecarAgent
			logPath := persistPrimaryActivity(t, primary)

			proxy.Run(strings.NewReader(strings.Join(tc.input, "\n") + "\n"))

			for _, msg := range out.messages(t) {
				if errObj, isErr := msg["error"]; isErr {
					t.Fatalf("proxied request failed: %v", errObj)
				}
			}
			events, err := ReadActivityLog(logPath, time.Time{}, 50, "", "mcp")
			if err != nil {
				t.Fatalf("ReadActivityLog failed: %v", err)
			}
			if len(events) != 1 || events[0].Tool != "list_articles" {
				t.Fatalf("expected exactly one logged list_articles call, got %+v", events)
			}
			if events[0].Agent != tc.want {
				t.Errorf("tool call through the sidecar attributed to %q, want %q", events[0].Agent, tc.want)
			}
		})
	}
}

// TestInvisibleConfiguredAgentNameIsTreatedAsUnset pins that an -agent-name made only of control
// and zero-width characters counts as no name on both sides of a sidecar: the proxy forwards no
// client-name header for it, and a primary configured the same way credits DefaultAgentName rather
// than logging a blank agent. Both logging paths are covered: a per-call entry, and the per-document
// entries of a bulk import, which log the resolved agent as given.
func TestInvisibleConfiguredAgentNameIsTreatedAsUnset(t *testing.T) {
	const invisible = "\x01\x1b\u200b\u200e\ufeff"
	const listCall = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_articles","arguments":{}}}`

	built := NewMCPProxy("1", invisible, io.Discard)
	defer built.stop()
	req, err := built.newRequest([]byte(listCall))
	if err != nil {
		t.Fatalf("newRequest failed: %v", err)
	}
	if got := req.Header.Get(clientNameHeader); got != "" {
		t.Errorf("the sidecar forwarded %q for a name that sanitizes to nothing", got)
	}

	primary, proxy, out := proxyAgainstPrimary(t)
	primary.AgentName = invisible
	proxy.agentName = built.agentName
	logPath := persistPrimaryActivity(t, primary)

	bundlePath := filepath.Join(primary.Storage.DataDir, "bundle.zip")
	bundle := okfBundle(t, map[string]string{
		"wiki/imported-page.md": "---\ntype: Wiki\ntitle: Imported Page\nslug: imported-page\n---\n# imported\n",
	})
	if err := os.WriteFile(bundlePath, bundle, 0644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	importArgs, _ := json.Marshal(map[string]string{"path": bundlePath})
	importCall := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"import_okf_bundle","arguments":` +
		string(importArgs) + `}}`

	proxy.Run(strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, listCall, importCall,
	}, "\n") + "\n"))

	for _, msg := range out.messages(t) {
		if errObj, isErr := msg["error"]; isErr {
			t.Fatalf("proxied request failed: %v", errObj)
		}
	}
	events, err := ReadActivityLog(logPath, time.Time{}, 50, "", "mcp")
	if err != nil {
		t.Fatalf("ReadActivityLog failed: %v", err)
	}
	tools := map[string]bool{}
	for _, ev := range events {
		tools[ev.Tool] = true
		if ev.Agent != DefaultAgentName {
			t.Errorf("%s event attributed to %q, want %q", ev.Tool, ev.Agent, DefaultAgentName)
		}
	}
	if len(events) != 2 || !tools["list_articles"] || !tools["import_okf_bundle"] {
		t.Fatalf("expected one list_articles and one import_okf_bundle event, got %+v", events)
	}
}

// TestProxyClientNameHeader pins what the proxy sends: nothing until it knows a name, the Base64
// sentinel for a name that cannot travel as a plain header, and the same header on a subscription
// stream, which builds its request on another goroutine.
func TestProxyClientNameHeader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proxy := &MCPProxy{endpoint: "http://127.0.0.1:1/api/mcp", shutdown: ctx, stop: cancel}

	req, err := proxy.newRequest([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	if err != nil {
		t.Fatalf("newRequest failed: %v", err)
	}
	if got := req.Header.Get(clientNameHeader); got != "" {
		t.Errorf("no name is known yet, but the proxy sent %q", got)
	}

	// An initialize without clientInfo must not erase a name the client already gave.
	for _, payload := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"日本 Client"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{}}`,
	} {
		if _, err := proxy.newRequest([]byte(payload)); err != nil {
			t.Fatalf("newRequest failed: %v", err)
		}
	}

	done := make(chan *http.Request, 1)
	go func() {
		req, _ := proxy.newRequest([]byte(`{"jsonrpc":"2.0","id":3,"method":"subscriptions/listen","params":{}}`))
		done <- req
	}()
	select {
	case req = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out building a subscription request")
	}
	raw := req.Header.Get(clientNameHeader)
	if !strings.HasPrefix(raw, "=?base64?") {
		t.Errorf("a non-ASCII name must use the Base64 sentinel, got %q", raw)
	}
	if got := forwardedClientName(req.Header); got != "日本 Client" {
		t.Errorf("primary decodes %q, want %q", got, "日本 Client")
	}
}

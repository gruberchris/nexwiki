package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

// TestProxyRelaysSubscriptionStream is the payoff: a stdio client gets live notifications, which a
// standalone stdio server cannot deliver at all.
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

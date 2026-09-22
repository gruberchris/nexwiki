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
	"unicode"
	"unicode/utf8"
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
	if len(tools) != 8 {
		t.Errorf("proxied tools/list returned %d tools, want 8", len(tools))
	}
	if msgs[0]["id"].(float64) != 1 || msgs[1]["id"].(float64) != 2 {
		t.Error("responses must preserve their request ids and order")
	}
}

// TestNewMCPProxyForwardsToTheGivenEndpoint pins that the proxy talks to exactly the endpoint its
// caller chose. main picks the host where it found the primary, which can be IPv6 loopback or another
// specific address rather than the 127.0.0.1 the endpoint was once hardcoded to (#174).
func TestNewMCPProxyForwardsToTheGivenEndpoint(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1"} {
		t.Run(host, func(t *testing.T) {
			ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
			if err != nil {
				t.Skipf("cannot listen on %s: %v", host, err)
			}
			primary := resourceServer(t)
			mux := http.NewServeMux()
			mux.HandleFunc("/api/mcp", primary.HandleStreamableHTTP)
			httpSrv := httptest.NewUnstartedServer(mux)
			_ = httpSrv.Listener.Close()
			httpSrv.Listener = ln
			httpSrv.Start()
			t.Cleanup(httpSrv.Close)

			out := &syncBuffer{}
			endpoint := "http://" + ln.Addr().String() + "/api/mcp" // Addr brackets an IPv6 host
			NewMCPProxy(endpoint, "", out).Run(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n"))

			msgs := out.messages(t)
			if len(msgs) != 1 {
				t.Fatalf("expected 1 response from the primary at %s, got %d:\n%s", endpoint, len(msgs), out.String())
			}
			result, _ := msgs[0]["result"].(map[string]interface{})
			if _, ok := result["tools"]; !ok {
				t.Errorf("the proxied tools/list did not reach the primary at %s: %s", endpoint, out.String())
			}
		})
	}
}

// TestNewDirectTransportIgnoresProxyVariables pins the transport the sidecar reaches its primary
// with, both for main's probe and the proxy's forwarding: no proxy, whatever HTTP_PROXY says, the
// default transport's connection settings otherwise, and no response timeout to cut subscription
// streams. net/http reads the variables once per process, so the main package's child-process test
// covers the behavior end to end; this pins the configuration.
func TestNewDirectTransportIgnoresProxyVariables(t *testing.T) {
	def, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("http.DefaultTransport has been replaced")
	}
	check := func(label string, rt http.RoundTripper) {
		t.Helper()
		tr, ok := rt.(*http.Transport)
		if !ok {
			t.Fatalf("%s: transport is %T, want *http.Transport", label, rt)
		}
		if tr.Proxy != nil {
			t.Errorf("%s: transport has a Proxy function, so HTTP_PROXY can route traffic to the primary", label)
		}
		if tr.DialContext == nil || tr.IdleConnTimeout != def.IdleConnTimeout || tr.TLSHandshakeTimeout != def.TLSHandshakeTimeout ||
			tr.MaxIdleConns != def.MaxIdleConns || tr.ForceAttemptHTTP2 != def.ForceAttemptHTTP2 {
			t.Errorf("%s: transport does not keep the default transport's connection settings", label)
		}
		if tr.ResponseHeaderTimeout != 0 {
			t.Errorf("%s: ResponseHeaderTimeout %s would cut subscription streams", label, tr.ResponseHeaderTimeout)
		}
	}
	check("NewDirectTransport", NewDirectTransport())

	proxy := NewMCPProxy("http://127.0.0.1:1/api/mcp", "", io.Discard)
	check("NewMCPProxy", proxy.client.Transport)
	if proxy.client.Timeout != 0 {
		t.Errorf("the proxy client has a %s timeout, which would cut subscription streams", proxy.client.Timeout)
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
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_wiki_overview","arguments":{},` + meta + `}}` + "\n"))

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
	payload := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"save_article",` +
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

	built := NewMCPProxy("http://127.0.0.1:1/api/mcp", invisible, io.Discard)
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

	saveCall := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"save_article","arguments":{"title":"Imported Page","content":"# imported"}}}`

	proxy.Run(strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, listCall, saveCall,
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
	if len(events) != 2 || !tools["list_articles"] || !tools["save_article"] {
		t.Fatalf("expected one list_articles and one save_article event, got %+v", events)
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

// proxyTo points a proxy at endpoint, returning it with its stdout and its log.
func proxyTo(t *testing.T, endpoint string) (proxy *MCPProxy, out, logs *syncBuffer) {
	t.Helper()
	out, logs = &syncBuffer{}, &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	proxy = &MCPProxy{endpoint: endpoint, client: &http.Client{Timeout: 10 * time.Second}, out: out, errOut: logs, shutdown: ctx, stop: cancel}
	return proxy, out, logs
}

// proxyToHandler serves handler as the primary and points a proxy at it.
func proxyToHandler(t *testing.T, handler http.Handler) (proxy *MCPProxy, out, logs *syncBuffer) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return proxyTo(t, srv.URL+"/api/mcp")
}

// respond answers every request with status, content type, and body.
func respond(status int, contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// answering is respond in the form a test table builds its primaries in, alongside refusingHost.
func answering(status int, contentType, body string) func(*testing.T) http.Handler {
	return func(*testing.T) http.Handler { return respond(status, contentType, body) }
}

// refusingHost is the web server's real Host check refusing the request, as it does a sidecar that
// reaches it by a DNS name it does not trust. The Host is rewritten because the test server is
// reached by IP, which is always allowed.
func refusingHost(t *testing.T) http.Handler {
	t.Helper()
	t.Setenv(AllowedOriginsEnv, "")
	cors := EnableCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the request passed the Host check it was meant to fail")
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "wiki.example.lan:5808"
		cors.ServeHTTP(w, r)
	})
}

// closedEndpoint returns the MCP endpoint of a server that has already shut down.
func closedEndpoint() string {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL + "/api/mcp"
}

// stdoutLines returns the lines the proxy wrote to stdout.
func stdoutLines(out *syncBuffer) []string {
	trimmed := strings.TrimSpace(out.String())
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// assertJSONRPCResponse fails unless line is a JSON-RPC 2.0 response a client can parse: the given
// id, and either a result or an error with an integer code and a string message. It returns the
// decoded response.
func assertJSONRPCResponse(t *testing.T, line string, wantID interface{}) map[string]interface{} {
	t.Helper()
	var msg map[string]interface{}
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("stdout line is not JSON: %v\n%s", err, line)
	}
	if msg["jsonrpc"] != "2.0" {
		t.Errorf("stdout line is not JSON-RPC 2.0: %s", line)
	}
	if id, ok := msg["id"]; !ok || id != wantID {
		t.Errorf("response id = %#v, want %#v: %s", msg["id"], wantID, line)
	}
	_, hasResult := msg["result"]
	errObj, hasError := msg["error"]
	if hasResult == hasError {
		t.Errorf("response must carry exactly one of result and error: %s", line)
	}
	if hasError {
		e, _ := errObj.(map[string]interface{})
		code, isNumber := e["code"].(float64)
		if _, isString := e["message"].(string); !isNumber || code != float64(int(code)) || !isString {
			t.Errorf("error object needs an integer code and a string message: %s", line)
		}
	}
	return msg
}

// TestProxyWritesOnlyJSONRPC pins that stdout carries nothing but JSON-RPC, whatever answers the
// proxy. A Host or Origin rejection, a 413, a reverse proxy's error page, an empty or foreign body,
// or no answer at all each used to reach the client as a line it cannot parse, or as nothing, leaving
// the call waiting. Each now gets one JSON-RPC error under the request's id naming the HTTP status and
// what the primary said, while a JSON-RPC reply, an error one included, is relayed as sent.
func TestProxyWritesOnlyJSONRPC(t *testing.T) {
	const request = `{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{}}`
	const result = `{"jsonrpc":"2.0","id":7,"result":{"tools":[]}}`
	const rpcError = `{"jsonrpc":"2.0","id":7,"error":{"code":-32601,"message":"Method not found: nope"}}`

	tests := []struct {
		name     string
		primary  func(t *testing.T) http.Handler
		endpoint string // used instead of primary when set
		// wantRelay is the line the primary's reply must be relayed as: byte for byte, but on one
		// line. When empty, the proxy must write its own error, with a message containing each of
		// wantMessage.
		wantRelay   string
		wantMessage []string
	}{
		{
			name:        "403 from the Host check",
			primary:     refusingHost,
			wantMessage: []string{"proxy: primary answered HTTP 403 Forbidden: ", `host not allowed: "wiki.example.lan:5808"`},
		},
		{
			name: "413 from the body limit",
			primary: func(*testing.T) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					writeDecodeError(w, &http.MaxBytesError{Limit: maxJSONBodyBytes})
				})
			},
			wantMessage: []string{"HTTP 413 Request Entity Too Large: request body exceeds the 8 MB limit"},
		},
		{
			name: "500 HTML error page",
			primary: answering(http.StatusInternalServerError, "text/html; charset=utf-8",
				"<html>\n<head><title>500 Internal Server Error</title></head>\n<body>upstream failed</body>\n</html>\n"),
			wantMessage: []string{"HTTP 500 Internal Server Error: response is not JSON (text/html)"},
		},
		{
			name:        "empty 502",
			primary:     answering(http.StatusBadGateway, "", ""),
			wantMessage: []string{"HTTP 502 Bad Gateway: empty response body"},
		},
		{
			name:        "plain-text 405",
			primary:     answering(http.StatusMethodNotAllowed, "text/plain; charset=utf-8", "Method not allowed\n"),
			wantMessage: []string{"HTTP 405 Method Not Allowed: Method not allowed"},
		},
		{
			name:        "invalid JSON",
			primary:     answering(http.StatusOK, "application/json", `{"jsonrpc":"2.0","id":7,"result":{`),
			wantMessage: []string{"HTTP 200 OK: response is not JSON (application/json)"},
		},
		{
			name:        "JSON that is not JSON-RPC",
			primary:     answering(http.StatusOK, "application/json", `{"tools":[]}`),
			wantMessage: []string{"HTTP 200 OK: response is not JSON-RPC"},
		},
		{
			name:        "a JSON-RPC reply to another id",
			primary:     answering(http.StatusOK, "application/json", `{"jsonrpc":"2.0","id":8,"result":{}}`),
			wantMessage: []string{"HTTP 200 OK: the JSON-RPC response does not answer this request"},
		},
		{
			name:        "202 with no body for a request",
			primary:     answering(http.StatusAccepted, "", ""),
			wantMessage: []string{"HTTP 202 Accepted: empty response body"},
		},
		{
			name:        "transport failure",
			endpoint:    closedEndpoint(),
			wantMessage: []string{"proxy: primary unreachable: ", "/api/mcp"},
		},
		{
			name:      "a JSON-RPC result",
			primary:   answering(http.StatusOK, "application/json", result+"\n"),
			wantRelay: result,
		},
		{
			name:      "a JSON-RPC error",
			primary:   answering(http.StatusOK, "application/json", rpcError),
			wantRelay: rpcError,
		},
		{
			// How the primary reports a modern-era protocol error: the HTTP status and the JSON-RPC
			// error both, of which stdio can carry only the body.
			name:      "a JSON-RPC error under a 404",
			primary:   answering(http.StatusNotFound, "application/json", rpcError),
			wantRelay: rpcError,
		},
		{
			name:      "a JSON-RPC result spread over lines",
			primary:   answering(http.StatusOK, "application/json", "{\n  \"jsonrpc\": \"2.0\",\n  \"id\": 7,\n  \"result\": {\"tools\": []}\n}\n"),
			wantRelay: result,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := tc.endpoint
			if endpoint == "" {
				srv := httptest.NewServer(tc.primary(t))
				defer srv.Close()
				endpoint = srv.URL + "/api/mcp"
			}
			proxy, out, logs := proxyTo(t, endpoint)
			proxy.Run(strings.NewReader(request + "\n"))

			lines := stdoutLines(out)
			if len(lines) != 1 {
				t.Fatalf("stdout carries %d lines, want exactly one response:\n%s", len(lines), out.String())
			}
			msg := assertJSONRPCResponse(t, lines[0], float64(7))

			if tc.wantRelay != "" {
				if lines[0] != tc.wantRelay {
					t.Errorf("the primary's reply was not relayed as sent:\ngot  %s\nwant %s", lines[0], tc.wantRelay)
				}
				if logged := logs.String(); logged != "" {
					t.Errorf("a relayed reply logged a failure: %s", logged)
				}
				return
			}

			errObj, _ := msg["error"].(map[string]interface{})
			if code, _ := errObj["code"].(float64); code != errCodeInternal {
				t.Errorf("error code = %v, want %d", errObj["code"], errCodeInternal)
			}
			message, _ := errObj["message"].(string)
			for _, want := range tc.wantMessage {
				if !strings.Contains(message, want) {
					t.Errorf("error message %q does not contain %q", message, want)
				}
			}
			if strings.Contains(message, "<") {
				t.Errorf("error message quotes markup from the response: %q", message)
			}
			if !strings.Contains(logs.String(), "proxy: tools/list (id 7) failed: ") {
				t.Errorf("the failure was not logged; log:\n%s", logs.String())
			}
		})
	}
}

// TestProxyLogsFailedNotifications pins that a notification the primary does not accept is logged,
// never answered on stdout: JSON-RPC forbids replying to one, and the proxy used to relay the
// primary's error body, or its own null-id error, as if it were a reply.
func TestProxyLogsFailedNotifications(t *testing.T) {
	const notification = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	tests := []struct {
		name     string
		primary  func(t *testing.T) http.Handler
		endpoint string
		payload  string
		wantLog  string // empty when nothing may be logged
	}{
		{"403 from the Host check", refusingHost, "", notification, "notifications/initialized failed: primary answered HTTP 403 Forbidden: host not allowed"},
		{"500 HTML error page", answering(http.StatusInternalServerError, "text/html", "<html><body>down</body></html>"), "", notification, "HTTP 500 Internal Server Error: response is not JSON (text/html)"},
		{"empty 502", answering(http.StatusBadGateway, "", ""), "", notification, "HTTP 502 Bad Gateway: empty response body"},
		{"transport failure", nil, closedEndpoint(), notification, "notifications/initialized failed: primary unreachable"},
		{"202 accepted", answering(http.StatusAccepted, "", ""), "", notification, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := tc.endpoint
			if endpoint == "" {
				srv := httptest.NewServer(tc.primary(t))
				defer srv.Close()
				endpoint = srv.URL + "/api/mcp"
			}
			proxy, out, logs := proxyTo(t, endpoint)
			proxy.Run(strings.NewReader(tc.payload + "\n"))

			if got := out.String(); got != "" {
				t.Errorf("a notification must produce nothing on stdout, got: %s", got)
			}
			switch logged := logs.String(); {
			case tc.wantLog == "" && logged != "":
				t.Errorf("an accepted notification logged: %s", logged)
			case tc.wantLog != "" && !strings.Contains(logged, tc.wantLog):
				t.Errorf("log does not contain %q:\n%s", tc.wantLog, logged)
			}
		})
	}
}

// TestProxyErrorMessageQuotesTheReasonSafely pins that the part of an error message quoted from the
// primary is capped and stripped of control characters, which the client may render or hand to a
// model, and that a large integer id is echoed exactly rather than rounded through a float.
func TestProxyErrorMessageQuotesTheReasonSafely(t *testing.T) {
	long := "line one\r\nline two\x1b[31m red\u202e reversed\t" + strings.Repeat("é", 2000)
	body, _ := json.Marshal(map[string]string{"error": long})
	proxy, out, _ := proxyToHandler(t, respond(http.StatusForbidden, "application/json", string(body)))

	proxy.Run(strings.NewReader(`{"jsonrpc":"2.0","id":12345678901234567890,"method":"tools/list"}` + "\n"))

	lines := stdoutLines(out)
	if len(lines) != 1 {
		t.Fatalf("stdout carries %d lines, want one:\n%s", len(lines), out.String())
	}
	if !strings.HasPrefix(lines[0], `{"jsonrpc":"2.0","error":`) || !strings.HasSuffix(lines[0], `,"id":12345678901234567890}`) {
		t.Errorf("the error does not echo the request id exactly: %s", lines[0])
	}
	var resp struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("stdout line is not JSON: %v", err)
	}
	const prefix = "proxy: primary answered HTTP 403 Forbidden: "
	reason, ok := strings.CutPrefix(resp.Error.Message, prefix)
	if !ok {
		t.Fatalf("message %q does not start with %q", resp.Error.Message, prefix)
	}
	if !strings.HasPrefix(reason, "line one line two[31m red reversed éé") || !strings.HasSuffix(reason, "...") {
		t.Errorf("reason was not sanitized and marked as cut: %q", reason)
	}
	if len(reason) > maxProxyReasonBytes+len("...") {
		t.Errorf("reason is %d bytes, want at most %d", len(reason), maxProxyReasonBytes+len("..."))
	}
	if !utf8.ValidString(reason) {
		t.Errorf("reason was cut mid-character: %q", reason)
	}
	for _, r := range reason {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			t.Errorf("reason keeps control or format character %U: %q", r, reason)
		}
	}
}

// TestProxyAnswersUnreadableMessages pins that a line the proxy cannot read an id from is answered
// under the null id JSON-RPC gives a parse error: the primary's own error when it gives one, and the
// proxy's when the primary gives nothing usable.
func TestProxyAnswersUnreadableMessages(t *testing.T) {
	_, primaryProxy, primaryOut := proxyAgainstPrimary(t)
	primaryProxy.Run(strings.NewReader("not json\n"))
	lines := stdoutLines(primaryOut)
	if len(lines) != 1 {
		t.Fatalf("stdout carries %d lines, want one:\n%s", len(lines), primaryOut.String())
	}
	msg := assertJSONRPCResponse(t, lines[0], nil)
	if code, _ := msg["error"].(map[string]interface{})["code"].(float64); code != -32700 {
		t.Errorf("the primary's parse error was not relayed: %s", lines[0])
	}

	proxy, out, _ := proxyToHandler(t, respond(http.StatusBadGateway, "", ""))
	proxy.Run(strings.NewReader("not json\n"))
	lines = stdoutLines(out)
	if len(lines) != 1 {
		t.Fatalf("stdout carries %d lines, want one:\n%s", len(lines), out.String())
	}
	assertJSONRPCResponse(t, lines[0], nil)
}

// TestProxyReaddressesAnErrorWithoutAnID pins that a request the primary rejects as a whole still
// reaches the client under its own id: the primary keeps an id it could read even on a rejection,
// and the reply relays with the primary's code and message intact. With a null id a client cannot
// match the reply to the call, which then never ends.
func TestProxyReaddressesAnErrorWithoutAnID(t *testing.T) {
	_, proxy, out := proxyAgainstPrimary(t)
	// A method that is not a string fails the primary's decoding as a whole, but the id still reads.
	proxy.Run(strings.NewReader(`{"jsonrpc":"2.0","id":"call-1","method":42}` + "\n"))

	lines := stdoutLines(out)
	if len(lines) != 1 {
		t.Fatalf("stdout carries %d lines, want one:\n%s", len(lines), out.String())
	}
	msg := assertJSONRPCResponse(t, lines[0], "call-1")
	errObj, _ := msg["error"].(map[string]interface{})
	if errObj["code"] != float64(errCodeInvalidRequest) || errObj["message"] != `Invalid Request: "method" must be a string` {
		t.Errorf("the primary's error was not kept: %s", lines[0])
	}
}

// TestProxyAnswersBatches pins what a JSON-RPC batch gets through the proxy. The primary does not
// accept batches and rejects one with a single null-id error, and a failure gets the proxy's error:
// either way each request in the batch gets it under its own id, in a batch response, and a
// notification in the batch gets nothing. A batch response from the primary is relayed as sent.
func TestProxyAnswersBatches(t *testing.T) {
	const batch = `[{"jsonrpc":"2.0","id":1,"method":"tools/list"},` +
		`{"jsonrpc":"2.0","method":"notifications/initialized"},` +
		`{"jsonrpc":"2.0","id":"two","method":"ping"}]`

	// assertBatchErrors checks for one batch response holding an error with code under ids 1 and "two".
	assertBatchErrors := func(t *testing.T, out *syncBuffer, code float64) {
		t.Helper()
		lines := stdoutLines(out)
		if len(lines) != 1 {
			t.Fatalf("stdout carries %d lines, want one batch response:\n%s", len(lines), out.String())
		}
		var replies []json.RawMessage
		if err := json.Unmarshal([]byte(lines[0]), &replies); err != nil || len(replies) != 2 {
			t.Fatalf("want a batch response of 2 replies, got: %s", lines[0])
		}
		for i, wantID := range []interface{}{float64(1), "two"} {
			msg := assertJSONRPCResponse(t, string(replies[i]), wantID)
			if got, _ := msg["error"].(map[string]interface{})["code"].(float64); got != code {
				t.Errorf("reply %d has error code %v, want %v: %s", i, got, code, replies[i])
			}
		}
	}

	t.Run("the primary rejects the batch", func(t *testing.T) {
		_, proxy, out := proxyAgainstPrimary(t)
		proxy.Run(strings.NewReader(batch + "\n"))
		assertBatchErrors(t, out, errCodeInvalidRequest)
	})

	t.Run("403 from the Host check", func(t *testing.T) {
		proxy, out, _ := proxyToHandler(t, refusingHost(t))
		proxy.Run(strings.NewReader(batch + "\n"))
		assertBatchErrors(t, out, errCodeInternal)
	})

	t.Run("a batch of notifications", func(t *testing.T) {
		proxy, out, logs := proxyToHandler(t, refusingHost(t))
		proxy.Run(strings.NewReader(`[{"jsonrpc":"2.0","method":"notifications/initialized"}]` + "\n"))
		if got := out.String(); got != "" {
			t.Errorf("a batch of notifications must produce nothing on stdout, got: %s", got)
		}
		if !strings.Contains(logs.String(), "batch of 1 message failed: primary answered HTTP 403 Forbidden") {
			t.Errorf("the failure was not logged:\n%s", logs.String())
		}
	})

	t.Run("a batch response", func(t *testing.T) {
		const replies = `[{"jsonrpc":"2.0","id":1,"result":{}},{"jsonrpc":"2.0","id":"two","result":{}}]`
		proxy, out, _ := proxyToHandler(t, respond(http.StatusOK, "application/json", replies))
		proxy.Run(strings.NewReader(batch + "\n"))
		if got := strings.TrimSpace(out.String()); got != replies {
			t.Errorf("the batch response was not relayed as sent:\ngot  %s\nwant %s", got, replies)
		}
	})
}

// runUntilAnswered runs the proxy on payload, holding stdin open until something reaches stdout. A
// subscription is forwarded on its own goroutine, and stdin ending cancels it, so input that ended
// at once could cancel the request before the primary answered.
func runUntilAnswered(t *testing.T, proxy *MCPProxy, out *syncBuffer, payload string) {
	t.Helper()
	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		proxy.Run(reader)
	}()
	_, _ = writer.Write([]byte(payload + "\n"))
	for deadline := time.Now().Add(3 * time.Second); out.String() == "" && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	_ = writer.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the proxy did not stop after stdin closed")
	}
}

// TestProxyChecksARejectedSubscription pins that a subscription refused with a plain response rather
// than a stream gets the same treatment as any other call: the primary's JSON-RPC error is relayed,
// and anything else is replaced by a JSON-RPC error under the subscription's id.
func TestProxyChecksARejectedSubscription(t *testing.T) {
	const listen = `{"jsonrpc":"2.0","id":9,"method":"subscriptions/listen","params":{"notifications":{}}}`

	t.Run("403 from the Host check", func(t *testing.T) {
		proxy, out, _ := proxyToHandler(t, refusingHost(t))
		runUntilAnswered(t, proxy, out, listen)
		lines := stdoutLines(out)
		if len(lines) != 1 {
			t.Fatalf("stdout carries %d lines, want one:\n%s", len(lines), out.String())
		}
		msg := assertJSONRPCResponse(t, lines[0], float64(9))
		if message, _ := msg["error"].(map[string]interface{})["message"].(string); !strings.Contains(message, "HTTP 403 Forbidden: host not allowed") {
			t.Errorf("unexpected error message: %s", lines[0])
		}
	})

	t.Run("the primary's JSON-RPC error", func(t *testing.T) {
		// An unsupported protocol version fails validation with a 400 carrying a JSON-RPC error, whose
		// data lists the versions the primary supports.
		_, proxy, out := proxyAgainstPrimary(t)
		runUntilAnswered(t, proxy, out, `{"jsonrpc":"2.0","id":9,"method":"subscriptions/listen","params":{"_meta":{`+
			`"io.modelcontextprotocol/protocolVersion":"1999-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}`)
		lines := stdoutLines(out)
		if len(lines) != 1 {
			t.Fatalf("stdout carries %d lines, want one:\n%s", len(lines), out.String())
		}
		msg := assertJSONRPCResponse(t, lines[0], float64(9))
		errObj, _ := msg["error"].(map[string]interface{})
		if errObj["code"] != float64(errCodeUnsupportedProtocolVersion) || errObj["data"] == nil {
			t.Errorf("the primary's error was not relayed: %s", lines[0])
		}
	})
}

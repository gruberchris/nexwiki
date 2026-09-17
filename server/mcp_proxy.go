package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Sidecar proxy mode.
//
// A `-mcp-only` process pointed at a running instance's data directory cannot open it: the Bleve
// index holds an exclusive lock, so only one process may own a wiki at a time. That is precisely
// the documented Claude Desktop stdio configuration, which once hung forever on the lock and later
// failed fast with an explanation — honest, but still not a working setup.
//
// Proxy mode makes it work: when main.go detects a running primary, the sidecar does not open
// storage at all. It forwards each stdio JSON-RPC message to the primary's /api/mcp endpoint and
// writes the reply back to stdout. One process owns the wiki; the sidecar is a pipe.
//
// A useful consequence: because the primary answers subscriptions/listen with an SSE stream, the
// proxy can relay those notifications to stdout as they arrive — so a stdio client gets live
// subscriptions, which a standalone stdio server cannot offer.
//
// The one thing a pipe loses is the stdio handshake. A standalone server remembers the client name
// from a legacy `initialize`, but the primary receives these calls over sessionless HTTP, so the
// proxy remembers the name itself and forwards it on every request (see clientNameHeader).

// proxyRequestTimeout bounds a single forwarded request. Long enough for a slow OKF bundle import,
// short enough that a wedged primary does not hang the client forever. Streaming responses
// (subscriptions) are exempt: they are meant to stay open.
const proxyRequestTimeout = 5 * time.Minute

// MaxStdioLineBytes caps a single JSON-RPC line on stdio, in both the proxy and the standalone
// server. MCP payloads carry whole article bodies, so bufio's 64 KB default is far too small — and
// overrunning it is unrecoverable, ending the read loop for the life of the process.
const MaxStdioLineBytes = 8 << 20

// MCPProxy forwards stdio JSON-RPC traffic to a NexWiki web primary over Streamable HTTP.
type MCPProxy struct {
	endpoint string
	client   *http.Client

	// writeMu serializes stdout. Relayed subscription notifications arrive asynchronously from
	// their own goroutines, so without this a notification could interleave mid-line with a
	// response and corrupt the JSON-RPC stream — the exact failure the log-to-stderr rule exists
	// to prevent elsewhere.
	writeMu sync.Mutex
	out     io.Writer

	// shutdown is cancelled when stdin ends, tearing down every open subscription stream.
	// Without it the proxy would outlive its client: a subscription stays open until the *primary*
	// closes it, so waiting on those goroutines after the client disconnected would hang forever.
	shutdown context.Context
	stop     context.CancelFunc

	// agentName is this sidecar's own -agent-name / NEXWIKI_AGENT_NAME, sanitized once when the
	// proxy is built. It is forwarded when the stdio client sends no clientInfo, so it credits calls
	// here exactly as it would if this process were running standalone, rather than being ignored
	// in favor of the primary's.
	agentName string
	// stdioClient holds the name from the stdio client's legacy `initialize`, the identity a
	// standalone server would remember for the connection. agentIdentity is locked, which matters
	// here: subscription streams build their requests on their own goroutines.
	stdioClient agentIdentity
}

// NewDirectTransport returns the HTTP transport a sidecar uses to reach its primary: the default
// transport's dial, keep-alive, and handshake settings, but never a proxy from HTTP_PROXY or
// HTTPS_PROXY. The primary runs on this machine or its network, and the default transport exempts
// only loopback from those variables, so a primary bound to a LAN address would otherwise be probed
// and proxied through, say, a corporate proxy that cannot reach it. It sets no response timeout,
// which would cut subscription streams.
func NewDirectTransport() *http.Transport {
	t := &http.Transport{}
	if def, ok := http.DefaultTransport.(*http.Transport); ok {
		t = def.Clone()
	}
	t.Proxy = nil
	return t
}

// NewMCPProxy builds a proxy targeting the primary's MCP endpoint, a full URL such as
// "http://127.0.0.1:5808/api/mcp". The caller supplies the URL because it chooses the host: the
// primary may be bound to a specific address rather than IPv4 loopback, and the caller is where
// that address was probed. agentName is this process's configured attribution fallback, and may be
// empty. One that sanitizes to nothing is kept empty too, so the proxy forwards no name for it and
// the primary's own fallback applies rather than a blank agent.
func NewMCPProxy(endpoint, agentName string, out io.Writer) *MCPProxy {
	ctx, cancel := context.WithCancel(context.Background())
	return &MCPProxy{
		endpoint: endpoint,
		// No overall client timeout: subscription streams are long-lived by design. Per-request
		// deadlines are applied to non-streaming calls instead.
		client:    &http.Client{Transport: NewDirectTransport()},
		out:       out,
		shutdown:  ctx,
		stop:      cancel,
		agentName: truncateAgentName(agentName),
	}
}

// Run reads JSON-RPC messages from in and forwards each to the primary until EOF.
func (p *MCPProxy) Run(in io.Reader) {
	if p.shutdown == nil {
		p.shutdown, p.stop = context.WithCancel(context.Background())
	}
	// stdin ending means the client is gone; tear down any stream still waiting on the primary.
	defer p.stop()

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), MaxStdioLineBytes)

	var streams sync.WaitGroup
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		// Copy: the scanner reuses its buffer, and streaming requests outlive this iteration.
		msg := append([]byte(nil), line...)

		if isSubscriptionRequest(msg) {
			// Subscriptions stay open, so they get their own goroutine; everything else is
			// forwarded in order.
			streams.Add(1)
			go func() {
				defer streams.Done()
				p.forwardStream(msg)
			}()
			continue
		}
		p.forward(msg)
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "proxy: stdin error: %v\n", err)
	}

	// Cancel before waiting: the streams are blocked reading from the primary, and only the
	// context unblocks them.
	p.stop()
	streams.Wait()
}

// writeLine emits one JSON-RPC message to stdout as a single line, holding the lock so concurrent
// streams cannot interleave.
func (p *MCPProxy) writeLine(payload []byte) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, _ = fmt.Fprintf(p.out, "%s\n", bytes.TrimSpace(payload))
}

// newRequest builds the HTTP request for a JSON-RPC payload, mirroring the metadata headers the
// modern era requires.
//
// This is the crux of the conversion: stdio has no headers, but a modern request over HTTP MUST
// carry MCP-Protocol-Version, Mcp-Method, and Mcp-Name matching the body, or the primary rejects
// it with -32020. The proxy derives them from the body it is already forwarding.
func (p *MCPProxy) newRequest(payload []byte) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, p.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	var parsed struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	parseErr := json.Unmarshal(payload, &parsed)
	env := parseParamsEnvelope(parsed.Params)
	if parseErr == nil && parsed.Method == "initialize" && !isModernRequest(nil, env) {
		// The capture a standalone stdio server makes from the same handshake.
		p.stdioClient.rememberInitialize(parsed.Params)
	}
	p.setClientNameHeader(req)

	if parseErr != nil {
		return req, nil // let the primary report the parse error
	}

	// nil headers: the message arrived on stdio, so the body is the only era signal there is. The
	// headers this function is about to synthesize are the ones the primary will check.
	if !isModernRequest(nil, env) {
		return req, nil // legacy era: no mirrored headers required
	}

	req.Header.Set("MCP-Protocol-Version", env.Meta.ProtocolVersion)
	req.Header.Set("Mcp-Method", parsed.Method)
	if extract, needsName := methodsWithNameHeader[parsed.Method]; needsName {
		if name := extract(env); name != "" {
			req.Header.Set("Mcp-Name", encodeHeaderValue(name))
		}
	}
	return req, nil
}

// setClientNameHeader forwards who the stdio client is, since the primary cannot learn it from a
// handshake it never saw: the name from the client's `initialize`, else this sidecar's own
// -agent-name. The primary still prefers a modern request's per-request clientInfo over it.
func (p *MCPProxy) setClientNameHeader(req *http.Request) {
	name := p.stdioClient.get()
	if name == "" {
		name = p.agentName
	}
	if name != "" {
		req.Header.Set(clientNameHeader, encodeHeaderValue(name))
	}
}

// forward sends one request and writes the single JSON response back.
func (p *MCPProxy) forward(payload []byte) {
	req, err := p.newRequest(payload)
	if err != nil {
		p.writeLine(proxyError(payload, "proxy: could not build request: "+err.Error()))
		return
	}

	ctx, cancel := context.WithTimeout(p.shutdown, proxyRequestTimeout)
	defer cancel()
	resp, err := p.client.Do(req.WithContext(ctx))
	if err != nil {
		p.writeLine(proxyError(payload, "proxy: primary unreachable: "+err.Error()))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// A notification produces 202 with no body, and JSON-RPC forbids replying to it.
	if resp.StatusCode == http.StatusAccepted {
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		p.writeLine(proxyError(payload, "proxy: could not read response: "+err.Error()))
		return
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return
	}
	p.writeLine(body)
}

// forwardStream relays a subscriptions/listen response, writing each SSE data frame to stdout as
// its own JSON-RPC line until the primary closes the stream.
func (p *MCPProxy) forwardStream(payload []byte) {
	req, err := p.newRequest(payload)
	if err != nil {
		p.writeLine(proxyError(payload, "proxy: could not build request: "+err.Error()))
		return
	}

	resp, err := p.client.Do(req.WithContext(p.shutdown))
	if err != nil {
		// A cancelled context is the client disconnecting, not a failure worth reporting to it.
		if p.shutdown.Err() == nil {
			p.writeLine(proxyError(payload, "proxy: primary unreachable: "+err.Error()))
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// The primary may answer a malformed subscription with plain JSON rather than a stream.
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		if body, readErr := io.ReadAll(resp.Body); readErr == nil && len(bytes.TrimSpace(body)) > 0 {
			p.writeLine(body)
		}
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), MaxStdioLineBytes)
	for scanner.Scan() {
		line := scanner.Text()
		// Colon-prefixed lines are SSE keep-alive comments carrying no event data.
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		p.writeLine([]byte(strings.TrimPrefix(line, "data: ")))
	}
}

// isSubscriptionRequest reports whether a payload opens a long-lived stream, which must not block
// the sequential forwarding loop.
func isSubscriptionRequest(payload []byte) bool {
	var parsed struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return false
	}
	return parsed.Method == "subscriptions/listen"
}

// proxyError renders a JSON-RPC error carrying the original request's id, so a client can
// correlate the failure with the call it made rather than seeing an unattributed error.
func proxyError(payload []byte, message string) []byte {
	var parsed struct {
		ID interface{} `json:"id"`
	}
	_ = json.Unmarshal(payload, &parsed)

	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      parsed.ID,
		Error:   &JSONRPCError{Code: errCodeInternal, Message: message},
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"proxy failure"}}`)
	}
	return out
}

// base64Encode is the encoding half of the sentinel format decodeHeaderValue reverses.
func base64Encode(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

// encodeHeaderValue applies the specification's Base64 sentinel when a value cannot travel as a
// plain ASCII header, mirroring decodeHeaderValue on the receiving side.
func encodeHeaderValue(value string) string {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7E {
			return "=?base64?" + base64Encode(value) + "?="
		}
	}
	if strings.HasPrefix(value, "=?base64?") && strings.HasSuffix(value, "?=") {
		return "=?base64?" + base64Encode(value) + "?="
	}
	if value != strings.TrimSpace(value) {
		return "=?base64?" + base64Encode(value) + "?="
	}
	return value
}

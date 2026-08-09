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
// the documented Claude Desktop stdio configuration, which previously hung forever and now fails
// fast with an explanation.
//
// Failing clearly is better than hanging, but it still leaves the documented setup not *working*.
// Proxy mode fixes that properly: instead of opening storage at all, the sidecar forwards each
// stdio JSON-RPC message to the running primary's /api/mcp endpoint and writes the reply back to
// stdout. One process owns the wiki; the sidecar is a pipe.
//
// A useful consequence: because the primary answers subscriptions/listen with an SSE stream, the
// proxy can relay those notifications to stdout as they arrive — so a stdio client gets live
// subscriptions, which a standalone stdio server cannot offer.

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
}

// NewMCPProxy builds a proxy targeting the primary's MCP endpoint on the given port.
func NewMCPProxy(port string, out io.Writer) *MCPProxy {
	ctx, cancel := context.WithCancel(context.Background())
	return &MCPProxy{
		endpoint: fmt.Sprintf("http://127.0.0.1:%s/api/mcp", port),
		// No overall client timeout: subscription streams are long-lived by design. Per-request
		// deadlines are applied to non-streaming calls instead.
		client:   &http.Client{},
		out:      out,
		shutdown: ctx,
		stop:     cancel,
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
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return req, nil // let the primary report the parse error
	}

	env := parseParamsEnvelope(parsed.Params)
	if !isModernRequest(env) {
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

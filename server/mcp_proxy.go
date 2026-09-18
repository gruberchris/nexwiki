package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
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
	// errOut receives diagnostics, os.Stderr when nil. stdout carries only JSON-RPC, so a failure
	// the client cannot be told about, such as a notification the primary refused, is logged here.
	errOut io.Writer

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
		p.logf("proxy: stdin error: %v", err)
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

// logf writes one diagnostic line to errOut.
func (p *MCPProxy) logf(format string, args ...interface{}) {
	w := p.errOut
	if w == nil {
		w = os.Stderr
	}
	_, _ = fmt.Fprintf(w, format+"\n", args...)
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

// forward sends one message and writes back the primary's reply, or a JSON-RPC error in its place
// when the primary gives none a client could parse.
func (p *MCPProxy) forward(payload []byte) {
	msg := inspectMessage(payload)
	req, err := p.newRequest(payload)
	if err != nil {
		p.fail(msg, "could not build request: "+sanitizeReason(err.Error(), maxProxyReasonBytes))
		return
	}

	ctx, cancel := context.WithTimeout(p.shutdown, proxyRequestTimeout)
	defer cancel()
	resp, err := p.client.Do(req.WithContext(ctx))
	if err != nil {
		p.fail(msg, "primary unreachable: "+sanitizeReason(err.Error(), maxProxyReasonBytes))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		p.fail(msg, "could not read the primary's response: "+sanitizeReason(err.Error(), maxProxyReasonBytes))
		return
	}
	p.relay(msg, resp, body)
}

// forwardStream relays a subscriptions/listen response, writing each SSE data frame to stdout as
// its own JSON-RPC line until the primary closes the stream.
func (p *MCPProxy) forwardStream(payload []byte) {
	msg := inspectMessage(payload)
	req, err := p.newRequest(payload)
	if err != nil {
		p.fail(msg, "could not build request: "+sanitizeReason(err.Error(), maxProxyReasonBytes))
		return
	}

	resp, err := p.client.Do(req.WithContext(p.shutdown))
	if err != nil {
		// A cancelled context is the client disconnecting, not a failure worth reporting to it.
		if p.shutdown.Err() == nil {
			p.fail(msg, "primary unreachable: "+sanitizeReason(err.Error(), maxProxyReasonBytes))
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// The primary answers a subscription it rejects with plain JSON rather than a stream, and so does
	// anything in front of it that refuses the request, so that answer is checked like any other.
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		body, readErr := io.ReadAll(resp.Body)
		switch {
		case readErr == nil:
			p.relay(msg, resp, body)
		case p.shutdown.Err() == nil:
			p.fail(msg, "could not read the primary's response: "+sanitizeReason(readErr.Error(), maxProxyReasonBytes))
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

// Relaying only JSON-RPC.
//
// stdout is the client's JSON-RPC stream, so a line on it that is not a JSON-RPC response breaks the
// client's parser or leaves its call waiting forever. The primary does not always answer in
// JSON-RPC: EnableCORS refuses a Host or Origin with 403 and a {"error": "..."} body, an oversized
// body gets 413, and a reverse proxy in front of it can answer with an HTML error page or nothing.
// So the proxy relays what the primary sent only when it is a JSON-RPC reply to the message, and
// otherwise writes a JSON-RPC error of its own under each id the message awaits a reply under.
//
// Batches: the primary does not accept them (MCP dropped JSON-RPC batching in 2025-06-18), and
// rejects one as a whole with a single error whose id is null. A batch still gets an answer through
// the proxy: that error, or the proxy's own, is repeated under the id of every request in it, as a
// batch response. A batch response the primary sends is relayed if every element in it is a
// JSON-RPC response, without checking that each request was answered.

// maxProxyReasonBytes caps what a synthesized error quotes from the primary or the transport. The
// message reaches the client and often a model's context, where more than a sentence or two of an
// error body is noise.
const maxProxyReasonBytes = 300

// maxLogLabelBytes caps the method and id quoted from a client's message in a log line.
const maxLogLabelBytes = 100

// nullID is the id JSON-RPC gives the error answering a message whose own id could not be read,
// and the id the primary answers an id it rejects — null, fractional, or composite — with.
var nullID = json.RawMessage("null")

// proxiedMessage is what the proxy reads from a stdio message before forwarding it: what it may
// write back, and under which ids.
type proxiedMessage struct {
	// ids holds the id of each request in the message awaiting a reply, in order, exactly as the
	// client wrote it, so an error echoes a large integer id without rounding it. A message that is
	// not a JSON object awaits an error under nullID, as does one whose id the primary rejects —
	// null, fractional, or composite — because the primary answers those with a null id. A
	// notification, which carries no id member, awaits nothing.
	ids []json.RawMessage
	// batch marks a JSON-RPC batch, whose reply is an array.
	batch bool
	// label names the message in log lines.
	label string
}

// inspectMessage reads which ids a stdio message awaits replies under.
func inspectMessage(payload []byte) proxiedMessage {
	var elems []json.RawMessage
	if err := json.Unmarshal(payload, &elems); err == nil && len(elems) > 0 {
		msg := proxiedMessage{batch: true, label: "batch of 1 message"}
		if len(elems) > 1 {
			msg.label = fmt.Sprintf("batch of %d messages", len(elems))
		}
		for _, elem := range elems {
			if id, awaits := awaitedID(elem); awaits {
				msg.ids = append(msg.ids, id)
			}
		}
		return msg
	}
	// Anything else, an empty batch included, is answered as a single message, as JSON-RPC requires.
	msg := proxiedMessage{label: messageLabel(payload)}
	if id, awaits := awaitedID(payload); awaits {
		msg.ids = []json.RawMessage{id}
	}
	return msg
}

// awaitedID returns the id a single message awaits a reply under, if it awaits one. The id is judged
// exactly as the primary judges it in UnmarshalJSON, so the two never disagree about which messages
// are notifications: a member with the wrong case is no id, a present id the primary rejects is
// answered by it with a null id, and only an absent id makes a notification.
func awaitedID(raw []byte) (json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nullID, true
	}
	id, hasID := fields["id"]
	if !hasID {
		return nil, false
	}
	if !isValidRequestID(id) {
		return nullID, true
	}
	return id, true
}

// messageLabel names a single message by its method and id. Both come from the client, so they are
// sanitized before reaching the log.
func messageLabel(payload []byte) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil || fields == nil {
		return "unreadable message"
	}
	label := "message"
	var method string
	if json.Unmarshal(fields["method"], &method) == nil && method != "" {
		label = sanitizeReason(method, maxLogLabelBytes)
	}
	if id, ok := fields["id"]; ok && !isJSONNull(id) {
		label += " (id " + sanitizeReason(string(id), maxLogLabelBytes) + ")"
	}
	return label
}

// relay writes what the primary answered msg with. A JSON-RPC reply is relayed whatever the HTTP
// status: a modern-era protocol error arrives as a 400 or 404 carrying a JSON-RPC error, and over
// stdio the body is the only place its code can go. Anything else is replaced by an error.
func (p *MCPProxy) relay(msg proxiedMessage, resp *http.Response, body []byte) {
	contentType := resp.Header.Get("Content-Type")
	if len(msg.ids) == 0 {
		// JSON-RPC forbids replying to a notification, so its failure can only be logged.
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			p.logf("proxy: %s failed: %s", msg.label, httpFailure(resp.StatusCode, bodyReason(contentType, body)))
		}
		return
	}
	reply, reason := replyFor(msg, contentType, body)
	if reason != "" {
		p.fail(msg, httpFailure(resp.StatusCode, reason))
		return
	}
	p.writeLine(reply)
}

// fail reports a message the primary did not answer in JSON-RPC: to the log, and to the client as a
// JSON-RPC error under each id the message awaits a reply under.
//
// The code is -32603, internal error, as the proxy has always used for an unreachable primary. The
// failure is on the server's side of the client's connection, not a fault in the call, so no
// request-error code fits, and the implementation-defined -32000 to -32099 range already carries
// meanings a client may act on: MCP SDKs use -32000 and -32001 for a closed connection and a
// timeout, -32002 is MCP's resource not found, and -32020 to -32022 are the modern era's protocol
// errors. The message names the HTTP status and what the primary said.
func (p *MCPProxy) fail(msg proxiedMessage, detail string) {
	p.logf("proxy: %s failed: %s", msg.label, detail)
	if len(msg.ids) == 0 {
		return
	}
	errObj, err := json.Marshal(JSONRPCError{Code: errCodeInternal, Message: "proxy: " + detail})
	if err != nil {
		errObj = json.RawMessage(`{"code":-32603,"message":"proxy failure"}`)
	}
	p.writeLine(errorReplies(msg, errObj))
}

// httpFailure describes an HTTP response that was not a JSON-RPC reply.
func httpFailure(status int, reason string) string {
	return fmt.Sprintf("primary answered HTTP %s: %s", strings.TrimSpace(fmt.Sprintf("%d %s", status, http.StatusText(status))), reason)
}

// replyFor returns the line to relay for msg from the primary's response body, or, when the body is
// not a JSON-RPC reply to msg, why not.
func replyFor(msg proxiedMessage, contentType string, body []byte) (reply []byte, reason string) {
	body = bytes.TrimSpace(body)
	single, isSingle := parseReply(body)
	isBatch := isReplyBatch(body)
	// An error with a null id means the primary could not read the message's id, though the proxy
	// did. The error answers every request in it, so it is re-addressed to each: a client matches a
	// reply to its call only by id, and would otherwise wait on the call forever.
	unaddressedError := isSingle && single.err != nil && isJSONNull(single.id)

	switch {
	case msg.batch && isBatch:
		return oneLine(body), ""
	case msg.batch && unaddressedError:
		return errorReplies(msg, single.err), ""
	case !msg.batch && isSingle && (sameID(single.id, msg.ids[0]) || isJSONNull(msg.ids[0])):
		return oneLine(body), ""
	case !msg.batch && unaddressedError:
		return errorReplies(msg, single.err), ""
	case isSingle || isBatch:
		return nil, "the JSON-RPC response does not answer this request"
	}
	return nil, bodyReason(contentType, body)
}

// rpcReply holds the parts of a JSON-RPC response the proxy checks, still encoded.
type rpcReply struct {
	id  json.RawMessage
	err json.RawMessage // nil for a result
}

// parseReply reports whether raw is a JSON-RPC 2.0 response a client can parse: "jsonrpc" is "2.0",
// "id" is present, and it carries either a result or an error with an integer code and a string
// message. A null "error" or "result" beside the other member is taken as absent.
func parseReply(raw []byte) (rpcReply, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return rpcReply{}, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return rpcReply{}, false
	}
	var version string
	if json.Unmarshal(fields["jsonrpc"], &version) != nil || version != "2.0" {
		return rpcReply{}, false
	}
	id, hasID := fields["id"]
	result, hasResult := fields["result"]
	errObj, hasError := fields["error"]
	if hasResult && hasError {
		switch {
		case isJSONNull(errObj):
			hasError = false
		case isJSONNull(result):
			hasResult = false
		default:
			return rpcReply{}, false
		}
	}
	if !hasID || hasResult == hasError {
		return rpcReply{}, false
	}
	if !hasError {
		return rpcReply{id: id}, true
	}
	var e struct {
		Code    *float64 `json:"code"`
		Message *string  `json:"message"`
	}
	if json.Unmarshal(errObj, &e) != nil || e.Code == nil || e.Message == nil || *e.Code != math.Trunc(*e.Code) {
		return rpcReply{}, false
	}
	return rpcReply{id: id, err: errObj}, true
}

// isReplyBatch reports whether raw is a non-empty array of JSON-RPC responses.
func isReplyBatch(raw []byte) bool {
	if len(raw) == 0 || raw[0] != '[' {
		return false
	}
	var elems []json.RawMessage
	if json.Unmarshal(raw, &elems) != nil || len(elems) == 0 {
		return false
	}
	for _, elem := range elems {
		if _, ok := parseReply(elem); !ok {
			return false
		}
	}
	return true
}

// sameID compares ids by value, as the primary decodes and re-encodes them: a client's 1.0 comes
// back as 1.
func sameID(a, b json.RawMessage) bool {
	var x, y interface{}
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), nullID)
}

// oneLine returns a JSON body as a single line. A raw line break in valid JSON can only be whitespace
// between tokens, so compacting changes nothing else, and a body without one is relayed as sent.
func oneLine(body []byte) []byte {
	if !bytes.ContainsAny(body, "\r\n") {
		return body
	}
	var buf bytes.Buffer
	if json.Compact(&buf, body) != nil {
		return body
	}
	return buf.Bytes()
}

// errorReplies renders errObj as the reply to every id msg awaits a reply under: one response, or a
// batch response for a batch.
func errorReplies(msg proxiedMessage, errObj json.RawMessage) []byte {
	replies := make([]JSONRPCResponse, 0, len(msg.ids))
	for _, id := range msg.ids {
		replies = append(replies, JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: errObj})
	}
	var out []byte
	var err error
	if msg.batch {
		out, err = json.Marshal(replies)
	} else {
		out, err = json.Marshal(replies[0])
	}
	if err != nil {
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"proxy failure"}}`)
	}
	return out
}

// bodyReason says briefly what a response body that is not a usable reply held. NexWiki's own HTTP
// errors, such as a Host or Origin rejection or a 413, are JSON objects whose "error" says what to
// fix, and a JSON-RPC error has a message, so those are quoted, as is plain text. Any other body is
// only characterized: an HTML error page from a reverse proxy is noise in a one-line message.
func bodyReason(contentType string, body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return "empty response body"
	}
	if !json.Valid(body) {
		mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
		if mediaType == "text/plain" {
			if text := sanitizeReason(string(body), maxProxyReasonBytes); text != "" {
				return text
			}
		}
		if mediaType == "" {
			return "response is not JSON"
		}
		return "response is not JSON (" + sanitizeReason(mediaType, maxLogLabelBytes) + ")"
	}
	if reply, ok := parseReply(body); ok {
		if reply.err == nil {
			return "an unexpected JSON-RPC result"
		}
		var e struct {
			Code    float64 `json:"code"`
			Message string  `json:"message"`
		}
		_ = json.Unmarshal(reply.err, &e)
		return fmt.Sprintf("JSON-RPC error %.0f: %s", e.Code, sanitizeReason(e.Message, maxProxyReasonBytes))
	}
	if isReplyBatch(body) {
		return "an unexpected JSON-RPC batch response"
	}
	var fields map[string]json.RawMessage
	var text string
	if json.Unmarshal(body, &fields) == nil && json.Unmarshal(fields["error"], &text) == nil {
		if text = sanitizeReason(text, maxProxyReasonBytes); text != "" {
			return text
		}
	}
	return "response is not JSON-RPC"
}

// sanitizeReason renders untrusted text for a one-line message: line breaks and other whitespace
// become single spaces, control and format characters and invalid UTF-8 are dropped, and the result
// is cut to maxBytes on a rune boundary, with "..." marking the cut. Only the first 4*maxBytes bytes
// are examined, so the cost is fixed however large a body is.
func sanitizeReason(text string, maxBytes int) string {
	truncated := false
	if len(text) > 4*maxBytes {
		// A rune cut in half here decodes as invalid and is dropped below.
		text, truncated = text[:4*maxBytes], true
	}

	var b strings.Builder
	pendingSpace := false
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
		case unicode.IsSpace(r):
			pendingSpace = b.Len() > 0
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
		default:
			if pendingSpace {
				b.WriteByte(' ')
				pendingSpace = false
			}
			b.WriteRune(r)
		}
	}

	out := b.String()
	if len(out) > maxBytes {
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(out[cut]) {
			cut--
		}
		out, truncated = strings.TrimSpace(out[:cut]), true
	}
	if truncated && out != "" {
		out += "..."
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

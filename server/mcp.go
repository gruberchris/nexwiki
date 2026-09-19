package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// JSONRPCRequest represents an incoming request in the JSON-RPC 2.0 format.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	// ID is nil for a notification. A decoded request holds the id's JSON verbatim as a
	// json.RawMessage, so it is echoed exactly: decoded into interface{}, every number became a
	// float64 and an id past 2^53 came back rounded. It stays interface{} so a request built in Go
	// can still use a plain value.
	ID interface{} `json:"id,omitempty"`

	// Headers carries the HTTP headers when the request arrived over Streamable HTTP, so the
	// modern era can verify the mirrored metadata against the body, and so a legacy request can be
	// attributed to the client name a sidecar forwards (see clientNameHeader). Nil on stdio.
	Headers http.Header `json:"-"`
	// IsModern records that the request opted into the per-request-metadata era, which decides
	// whether protocol errors surface as HTTP failures or ride inside a 200 response.
	IsModern bool `json:"-"`
	// FromStdio records that the request arrived on the stdio transport, which is the only one
	// where a legacy `initialize` handshake can be remembered: stdio is one process talking to one
	// client, whereas HTTP is sessionless and caching a handshake there would attribute one
	// client's writes to another. handleRequest serves both, so the distinction has to be carried.
	FromStdio bool `json:"-"`
}

// UnmarshalJSON decodes one JSON-RPC request. JSON that is well formed but is not a single valid
// request is rejected with a *requestError, which carries the -32600 reply; malformed JSON never
// gets here, as encoding/json reports it first, and that is what keeps -32700 for it alone.
//
// Member names match exactly. JSON-RPC's are case-sensitive, so "ID" is not an id but an unknown
// member, which is ignored. A struct decode matched them case-insensitively, which also put the
// primary at odds with the -mcp-only sidecar: it reads only an exact-case "id", so the two
// disagreed about whether {"ID":7,...} was a request or a notification.
func (r *JSONRPCRequest) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '[' {
		return invalidRequest(nil, "batch requests are not supported; send each request as its own message")
	}
	if len(data) == 0 || data[0] != '{' {
		return invalidRequest(nil, "expected a request object")
	}

	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}

	// An absent id is what makes a notification; `"id": null` is present, and rejected. The id is
	// judged first so that every later rejection can still be matched to its request.
	var id json.RawMessage
	if rawID, present := members["id"]; present {
		if !isValidRequestID(rawID) {
			return invalidRequest(nil, `"id" must be a string or an integer`)
		}
		id = rawID
	}
	if version, ok := requestStringMember(members["jsonrpc"]); !ok || version != "2.0" {
		return invalidRequest(id, `"jsonrpc" must be "2.0"`)
	}
	rawMethod, present := members["method"]
	if !present {
		return invalidRequest(id, `missing "method"`)
	}
	method, ok := requestStringMember(rawMethod)
	if !ok {
		return invalidRequest(id, `"method" must be a string`)
	}

	// Fields are set one by one rather than replacing *r, which would wipe the transport context.
	r.JSONRPC = "2.0"
	r.Method = method
	r.Params = members["params"]
	r.ID = nil
	if id != nil {
		// Only a present id is stored: a nil json.RawMessage in the interface is not == nil, and
		// would turn every notification into a request.
		r.ID = id
	}
	return nil
}

// isValidRequestID reports whether raw is an id MCP allows: a string or an integer. JSON-RPC 2.0
// also permits null and fractions, but MCP rules both out. The check is lexical, so an integer too
// wide for any Go type is still accepted, and echoed back exactly.
func isValidRequestID(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	switch c := raw[0]; {
	case c == '"':
		return true
	case c == '-' || (c >= '0' && c <= '9'):
		return !bytes.ContainsAny(raw, ".eE")
	default:
		return false
	}
}

// requestStringMember decodes a request member that must be a JSON string. It fails for a missing
// member and for null, which decoding into a string would otherwise pass off as "".
func requestStringMember(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	return s, json.Unmarshal(raw, &s) == nil
}

// requestError is a message rejected before dispatch, with the error that answers it. id is the
// request's id when it could be read and was valid, and nil otherwise, which JSON-RPC requires to
// be answered as null.
type requestError struct {
	code    int
	message string
	id      interface{}
}

func (e *requestError) Error() string { return e.message }

// invalidRequest builds the -32600 rejection. A missing id is stored as a true nil rather than a
// nil json.RawMessage, so that e.id == nil means what it says.
func invalidRequest(id json.RawMessage, detail string) *requestError {
	e := &requestError{code: errCodeInvalidRequest, message: "Invalid Request: " + detail}
	if id != nil {
		e.id = id
	}
	return e
}

// decodeRequest parses one message from a transport, or returns the error that answers it: -32700
// only when the bytes are not JSON at all, -32600 for JSON that is not a single valid request.
func decodeRequest(data []byte) (JSONRPCRequest, *requestError) {
	var req JSONRPCRequest
	err := json.Unmarshal(data, &req)
	if err == nil {
		return req, nil
	}
	var rejected *requestError
	if errors.As(err, &rejected) {
		return req, rejected
	}
	return req, &requestError{code: errCodeParseError, message: "Parse error: invalid JSON"}
}

// JSONRPCResponse represents an outgoing response in the JSON-RPC 2.0 format.
type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// JSONRPCError defines the standard JSON-RPC 2.0 error block.
type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// ToolContent maps to the standard MCP tool execution content block.
type ToolContent struct {
	Type string `json:"type"` // e.g. "text"
	Text string `json:"text"`
}

// ToolResponse represents the standard tool/call execution result.
type ToolResponse struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
	// StructuredContent is the machine-readable half of the result, conforming to the tool's
	// declared outputSchema. Set only by tools that declare one, and never on an error result —
	// a payload that fails its own schema is worse for a client than no payload at all.
	//
	// omitempty keeps every prose-only tool byte-identical on the wire, so clients that predate
	// structured output see no change at all.
	StructuredContent interface{} `json:"structuredContent,omitempty"`

	// bulk is set by a tool that writes many documents in one call, which no single slug can
	// describe in the activity log. executeToolCall logs one event per document from it instead of
	// the per-call entry. Unexported, so it never reaches the wire.
	bulk *bulkWrite
}

// bulkWrite lists the documents a bulk tool call wrote, each as its metadata without the body. An
// empty list means the call wrote nothing, so nothing is logged.
type bulkWrite struct {
	docs []Article
}

// StdioMCPServer is the stdio MCP JSON-RPC loop. Serve runs it; Stop ends it at a request boundary,
// which shutdown needs before closing storage, since a request dispatched after that would write to
// closed storage.
type StdioMCPServer struct {
	srv *Server
	in  io.Reader
	out io.Writer

	// dispatchMu is held while a request is handled, and stopped is checked under it, so Stop can
	// wait for the request in progress by taking the lock. stopped is set before Stop takes the lock,
	// for the reason Storage.closed is: a Stop that gives up waiting still blocks later requests.
	dispatchMu sync.Mutex
	stopped    atomic.Bool
}

// NewStdioMCPServer returns a stdio loop serving srv, reading requests from in and writing responses
// to out (os.Stdin and os.Stdout in production). It does not start reading until Serve is called.
func NewStdioMCPServer(srv *Server, in io.Reader, out io.Writer) *StdioMCPServer {
	return &StdioMCPServer{srv: srv, in: in, out: out}
}

// Stop ends dispatch: a request being handled finishes, and no later request is handled. It waits
// for that request until ctx ends, returning ctx's error if it gave up.
//
// It cannot make Serve return. A blocked read on stdin cannot be interrupted portably, so the loop
// stays parked in it until a line or EOF arrives, and then returns without handling the line. That
// is harmless: Stop is only called on the way out of the process.
func (s *StdioMCPServer) Stop(ctx context.Context) error {
	s.stopped.Store(true)
	idle := make(chan struct{})
	go func() {
		defer close(idle)
		s.dispatchMu.Lock()
		// Re-observe stopped while holding the lock, mirroring dispatch: holding it
		// proves no request is in progress, so it is released straight away.
		_ = s.stopped.Load()
		s.dispatchMu.Unlock()
	}()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// dispatch handles one request line, reporting false, without handling it, once Stop has been
// called.
func (s *StdioMCPServer) dispatch(line []byte) bool {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if s.stopped.Load() {
		return false
	}

	req, rejected := decodeRequest(line)
	if rejected != nil {
		sendError(s.out, rejected.code, rejected.message, rejected.id)
		return true
	}
	req.FromStdio = true
	s.srv.handleRequest(s.out, &req)
	return true
}

// Serve runs the loop until its input ends or reading fails. After Stop, it returns at the next line
// it reads, without handling that line.
func (s *StdioMCPServer) Serve() {
	scanner := bufio.NewScanner(s.in)
	// A tool call carrying a whole article body easily exceeds bufio's default 64 KB line cap, and
	// exceeding it is not recoverable: Scan returns false, the loop below ends, and the stdio
	// server stops answering for the rest of the process's life.
	//
	// That failure was silent in the worst possible way. Standalone (-mcp-only) the process exited
	// with status 0, so a supervising client saw a clean shutdown rather than a crash. Alongside
	// the web server it was worse still: the background loop died while HTTP kept serving 200s, so
	// the app looked healthy while its MCP channel was permanently dead — and the agent that sent
	// the article got no response at all, not even an error, and the article was never written.
	scanner.Buffer(make([]byte, 0, 64*1024), MaxStdioLineBytes)

	_, _ = fmt.Fprintf(os.Stderr, "Always-on stdio MCP server loop successfully started in background!\n")

	// Read lines of JSON-RPC requests from standard input
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if !s.dispatch(line) {
			return
		}
	}

	// The loop above cannot resume after a scanner failure, so tell the client rather than going
	// quiet: an agent waiting on a response it will never receive has no way to distinguish a dead
	// channel from a slow one.
	if err := scanner.Err(); err != nil && err != io.EOF {
		if errors.Is(err, bufio.ErrTooLong) {
			sendError(s.out, -32700, fmt.Sprintf(
				"Request exceeded the %d MB stdio line limit; the stdio channel has closed. Use the HTTP transport for payloads this large.",
				MaxStdioLineBytes>>20), nil)
		}
		_, _ = fmt.Fprintf(os.Stderr, "MCP server stdio error: %v\n", err)
	}
}

// handleRequest dispatches JSON-RPC requests to appropriate tool handlers.
// supportedProtocolVersions lists the MCP protocol revisions this server implements, newest
// first. NexWiki speaks the 2025 Streamable HTTP transport, so the newer revisions are the
// honest default — previously the server always answered "2024-11-05" regardless of what the
// client asked for, contradicting the transport it actually serves.
var supportedProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// defaultProtocolVersion is returned when a client omits protocolVersion during initialize.
const defaultProtocolVersion = "2025-06-18"

// negotiateProtocolVersion echoes the client's requested protocol revision when this server
// supports it, per the MCP spec's version-negotiation rule. When the request names a revision we
// do not implement, the newest supported revision is returned instead so the client can decide
// whether to proceed or disconnect.
func negotiateProtocolVersion(params json.RawMessage) string {
	if len(params) == 0 {
		return defaultProtocolVersion
	}

	var initParams struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(params, &initParams); err != nil || initParams.ProtocolVersion == "" {
		return defaultProtocolVersion
	}

	for _, supported := range supportedProtocolVersions {
		if initParams.ProtocolVersion == supported {
			return initParams.ProtocolVersion
		}
	}

	return defaultProtocolVersion
}

// handleRequest dispatches one JSON-RPC request and writes its envelope to w. It returns the
// HTTP status the response should carry; stdio callers ignore it.
//
// NexWiki is dual-era. A request carrying per-request `_meta` protocol fields is served under the
// 2026-07-28 revision (stateless, no handshake); anything else is served under the older
// initialize-based revisions. Both eras share the tool registry and prompt definitions — only the
// envelope, the required metadata, and the HTTP status mapping differ.
func (srv *Server) handleRequest(w io.Writer, req *JSONRPCRequest) int {
	// Notifications (requests without an ID) carry no response. Only one of them means anything to
	// this server: on stdio there is no per-request stream to close, so notifications/cancelled is
	// the sole way a client can end a subscription it opened.
	if req.ID == nil {
		if req.Method == "notifications/cancelled" {
			srv.cancelStdioSubscription(req.Params)
		}
		return http.StatusAccepted
	}

	var result interface{}
	var rpcErr *JSONRPCError

	env := parseParamsEnvelope(req.Params)

	// subscriptions/listen is answered before the era branch, mirroring HandleStreamableHTTP, which
	// also lifts it out of dispatch. Both eras get the same answer here because the stdio loop is
	// strictly request/response on one channel and cannot interleave notifications either way.
	//
	// Taking it out of the branch is what makes the modern era work at all. The method was
	// *introduced* by the 2026-07-28 revision, but handleModernMethod has no case for it, so a
	// modern client — the only kind that knows the method exists — was told "Method not found",
	// while a legacy client got the graceful acknowledgment. Exactly backwards.
	if req.Method == "subscriptions/listen" {
		// Modern metadata is still validated first, so a malformed request fails the same way it
		// would on any other method, and the same way it does over HTTP.
		if isModernRequest(req.Headers, env) {
			req.IsModern = true
			if rpcErr := validateModernMeta(env); rpcErr != nil {
				return srv.writeResponse(w, req, nil, rpcErr)
			}
		}
		srv.handleStdioSubscription(w, req, parseSubscriptionParams(req.Params))
		return http.StatusOK
	}

	if isModernRequest(req.Headers, env) {
		req.IsModern = true
		result, rpcErr = srv.dispatchModern(req, env)
		return srv.writeResponse(w, req, result, rpcErr)
	}

	switch req.Method {
	case "ping":
		// A utility of the initialize-based revisions, used by clients as a liveness probe. It was
		// removed in 2026-07-28, so it stays out of the modern dispatcher and answers only here.
		result = map[string]interface{}{}

	case "initialize":
		// Capture who is connecting, so their writes are attributable. Only on stdio — see
		// JSONRPCRequest.FromStdio.
		if req.FromStdio {
			srv.rememberStdioClient(req.Params)
		}
		result = map[string]interface{}{
			"protocolVersion": negotiateProtocolVersion(req.Params),
			"capabilities":    legacyServerCapabilities(),
			"serverInfo":      srv.implementation(),
			// Connect-time hint surfaced by MCP clients as a system-prompt-style nudge, so
			// the agent reaches for NexWiki as a second brain without explicit prompting.
			"instructions": agentInstructions(),
		}

	case "tools/list":
		result, rpcErr = listTools(env.Cursor)

	case "tools/call":
		result, rpcErr = srv.executeToolCall(req.Params, srv.resolveAgent(req, env))

	case "prompts/list":
		result, rpcErr = listPrompts(env.Cursor)

	case "prompts/get":
		result, rpcErr = srv.getPrompt(req.Params)

	case "resources/list":
		result, rpcErr = srv.listResources(env.Cursor)

	case "resources/templates/list":
		result, rpcErr = srv.listResourceTemplates(env.Cursor)

	case "resources/read":
		result, rpcErr = srv.readResource(req.Params)

	case "completion/complete":
		result, rpcErr = srv.complete(req.Params)

	default:
		rpcErr = &JSONRPCError{
			Code:    -32601,
			Message: fmt.Sprintf("Method not found: %s", req.Method),
		}
	}

	return srv.writeResponse(w, req, result, rpcErr)
}

// dispatchModern validates a per-request-metadata request and runs it, wrapping a successful
// payload in the modern result envelope (resultType, server identity, and caching hints).
func (srv *Server) dispatchModern(req *JSONRPCRequest, env paramsEnvelope) (interface{}, *JSONRPCError) {
	if rpcErr := validateModernMeta(env); rpcErr != nil {
		return nil, rpcErr
	}
	if rpcErr := validateModernHeaders(req.Headers, req.Method, env); rpcErr != nil {
		return nil, rpcErr
	}

	payload, rpcErr := srv.handleModernMethod(req, env)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return srv.completeResult(req.Method, payload), nil
}

// writeResponse marshals the JSON-RPC envelope and reports the HTTP status it should carry.
// Legacy-era responses are always 200 (errors ride in the body); modern-era protocol failures
// surface as real HTTP failures, which is how a dual-era client distinguishes the two.
func (srv *Server) writeResponse(w io.Writer, req *JSONRPCRequest, result interface{}, rpcErr *JSONRPCError) int {
	var resp JSONRPCResponse
	resp.JSONRPC = "2.0"
	resp.ID = req.ID

	status := http.StatusOK
	if rpcErr != nil {
		resp.Error = rpcErr
		if req.IsModern {
			status = modernErrorStatus(rpcErr)
		}
	} else {
		resp.Result = result
	}

	respBytes, err := json.Marshal(resp)
	if err == nil {
		// Stdio transport expects each JSON-RPC envelope strictly on a single line!
		_, _ = fmt.Fprintf(w, "%s\n", string(respBytes))
	}
	return status
}

// mcpToolAction maps a tool name to the activity-log action its successful call is recorded as.
//
// A revert is its own action, as it is for the web UI's revert: filtering the log on "revert" must
// find an agent's reverts too, rather than only the ones made in a browser.
func mcpToolAction(tool string) string {
	switch {
	case strings.HasPrefix(tool, "create_"):
		return "create"
	case strings.HasPrefix(tool, "revert_"):
		return "revert"
	case strings.HasPrefix(tool, "edit_"), strings.HasPrefix(tool, "append_"), strings.HasPrefix(tool, "update_"), strings.HasPrefix(tool, "save_"):
		return "edit"
	case strings.HasPrefix(tool, "delete_"):
		return "delete"
	default:
		return "read"
	}
}

// logMCPToolCall logs a successfully executed MCP tool call and publishes it, attributed to agent.
func (srv *Server) logMCPToolCall(params json.RawMessage, agent string) {
	if srv.EventBus == nil {
		return
	}

	type ToolCallParams struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}

	var args ToolCallParams
	if err := json.Unmarshal(params, &args); err != nil {
		return
	}

	// Unmarshal common arguments like slug and title
	var common struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	_ = json.Unmarshal(args.Arguments, &common)

	tool := args.Name
	action := mcpToolAction(tool)

	slug := common.Slug
	if slug == "" && common.Title != "" {
		slug = Slugify(common.Title)
	}

	// The article as the successful call left it. A mutation just saved a new revision, so its
	// version is the revision the event keys its dedup on — two quick edits of the same document
	// both stay attributed (#173), while one save announced twice still collapses. A read touched
	// nothing, so it stays unversioned; a delete left nothing to read back.
	var written *Article
	if slug != "" {
		if art, err := srv.Storage.GetArticle(slug); err == nil {
			written = art
		}
	}
	if written == nil && common.Title != "" {
		if art, err := srv.Storage.GetArticle(Slugify(common.Title)); err == nil {
			written = art
			slug = written.Slug
		}
	}

	// A document's first revision is version 1, and any later save supersedes an existing
	// document, so the version tells a create from a replacement for polymorphic save tools.
	if strings.HasPrefix(tool, "save_") && written != nil && written.Version == 1 {
		action = "create"
	}

	// The title for the event: the argument the caller named the document by, or the stored one
	// when it did not.
	title := common.Title
	if title == "" && written != nil {
		title = written.Title
	}

	if agent == "" {
		agent = DefaultAgentName
	}

	version := 0
	if action != "read" && written != nil {
		version = written.Version
	}
	srv.EventBus.PublishActivityVersion("mcp", action, tool, slug, title, agent, version)

	// If it's a mutation, broadcast a WikiUpdate to sync all clients!
	if action != "read" {
		articles, err := srv.Storage.ListArticles()
		if err == nil {
			var targetTags []string
			targetType := ContentTypeWiki
			if written != nil {
				targetTags = written.Tags
				targetType = written.Type
			}

			dir := getArticleDirectory(targetType)
			dirCount := 0
			for _, a := range articles {
				if getArticleDirectory(a.Type) == dir {
					dirCount++
				}
			}

			updateType := "article-edited"
			switch action {
			case "create":
				updateType = "article-added"
			case "delete":
				updateType = "article-removed"
			}

			srv.EventBus.PublishWikiUpdate(WikiUpdate{
				Type:           updateType,
				Slug:           slug,
				Title:          title,
				Tags:           targetTags,
				Directory:      dir,
				TotalCount:     len(articles),
				DirectoryCount: dirCount,
			})
		}
	}
}

// memoryScopeTags returns the tool-managed memory-scope tags (memory-<scope>) present on a tag list.
// These are preserved across edits; the memory document *class* is carried by the OKF `type` field.
func memoryScopeTags(tags []string) []string {
	var out []string
	for _, tag := range tags {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(tag)), MemoryScopeTagPrefix) {
			out = append(out, tag)
		}
	}
	return out
}

// executeToolCall parses parameters and executes requested MCP tools, with automatic logging hooks.
// agent is the attribution recorded against the call — see resolveAgent.
func (srv *Server) executeToolCall(params json.RawMessage, agent string) (interface{}, *JSONRPCError) {
	result, rpcErr := srv.executeToolCallInternal(params)
	if rpcErr == nil && !isToolError(result) {
		if resp, ok := result.(ToolResponse); ok && resp.bulk != nil {
			// Logged per document, as the web equivalent is. The per-call entry would have no slug,
			// and its action would be guessed from the tool name: an import was logged as a read.
			var call struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(params, &call)
			srv.publishBulkChanges("mcp", call.Name, agent, resp.bulk.docs)
		} else {
			srv.logMCPToolCall(params, agent)
		}
		result = srv.applyLookupDamper(params, agent, result)
	}
	return result, rpcErr
}

// applyLookupDamper is the damper's only wiring point. It sits here rather than in the handlers
// because this is the one place that has both the resolved agent and the tool call — putting it
// in each handler would mean threading an identity through twelve signatures to do one thing.
//
// Two behaviours, both keyed on the same agent:
//
//   - a watched lookup is fingerprinted, and a repeat prepends an escalating notice;
//   - any successful write clears that agent's ring, because a write is progress and the loop
//     being damped is read-only by nature.
//
// Text content only. structuredContent is a machine contract and must not grow advisory prose —
// a client parsing the structured half would have to handle a field that is sometimes an essay.
func (srv *Server) applyLookupDamper(params json.RawMessage, agent string, result interface{}) interface{} {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return result
	}

	query, watched := damperedLookupQuery(call.Name, call.Arguments)
	if !watched {
		if tool, ok := toolsByName[call.Name]; ok && !tool.Behavior.ReadOnly {
			srv.damper.clear(agent)
		}
		return result
	}

	occurrence, sinceFirst := srv.damper.observe(agent, call.Name, query)
	notice := damperNotice(occurrence, sinceFirst)
	if notice == "" {
		return result
	}

	resp, ok := result.(ToolResponse)
	if !ok || len(resp.Content) == 0 {
		return result
	}
	resp.Content[0].Text = notice + resp.Content[0].Text
	return resp
}

// isToolError reports whether a tool reported failure *inside* a successful JSON-RPC response.
//
// This distinction is the whole reason the guard exists. A tool that refuses its work — a version
// conflict, a missing article, an invalid tag — returns ToolResponse{IsError: true} in a perfectly
// well-formed result, not a JSON-RPC error. The logging hook only checked rpcErr, so every one of
// those refusals was recorded as a completed write: a rejected optimistic-locking edit appeared in
// the activity log as an edit that happened, attributed to whoever attempted it, against an
// article that never changed.
func isToolError(result interface{}) bool {
	resp, ok := result.(ToolResponse)
	return ok && resp.IsError
}

// executeToolCallInternal parses parameters and executes requested MCP tools.
func (srv *Server) executeToolCallInternal(params json.RawMessage) (interface{}, *JSONRPCError) {
	type ToolCallArgs struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}

	var args ToolCallArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &JSONRPCError{Code: -32602, Message: "Invalid tool call parameters"}
	}

	tool, ok := toolsByName[args.Name]
	if !ok {
		return ToolResponse{
			IsError: true,
			Content: []ToolContent{{
				Type: "text",
				Text: fmt.Sprintf("Tool not found: %s", args.Name),
			}},
		}, nil
	}

	return tool.Handler(srv, args.Arguments)
}

// sendError sends standard formatted JSON-RPC error responses on standard out.
func sendError(w io.Writer, code int, msg string, id interface{}) {
	var resp JSONRPCResponse
	resp.JSONRPC = "2.0"
	resp.ID = id
	resp.Error = &JSONRPCError{
		Code:    code,
		Message: msg,
	}
	respBytes, err := json.Marshal(resp)
	if err == nil {
		_, _ = fmt.Fprintf(w, "%s\n", string(respBytes))
	}
}

// MCPEndpointPath is where the Streamable HTTP transport is served. Exported because main.go
// registers the route.
const MCPEndpointPath = "/api/mcp"

// HandleStreamableHTTP implements the Streamable HTTP transport (2025 Spec)
// supporting GET (initiating SSE stream) and POST (synchronous JSON-RPC).
//
// It must only be served behind EnableCORS, as main.go mounts it. Every MCP tool — including
// delete_wiki_article and export_okf_bundle — is reachable here with no authentication, so an
// unvalidated origin is full read/write access to the knowledge base. The Origin check lives in
// EnableCORS alone so the two cannot drift apart: the middleware answers preflights and rejects
// origins before the handler runs, so a second copy here could never take effect.
func (srv *Server) HandleStreamableHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Verify accept header supports text/event-stream
		accept := r.Header.Get("Accept")
		if accept != "" && !strings.Contains(accept, "text/event-stream") {
			http.Error(w, "Accept header must support text/event-stream", http.StatusNotAcceptable)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// Tells reverse proxies (nginx especially) not to buffer the stream, which would
		// otherwise hold events until the buffer fills and defeat the point of SSE.
		w.Header().Set("X-Accel-Buffering", "no")

		// Priming comment to flush connection
		_, _ = fmt.Fprint(w, ": keepalive\n\n")
		flusher.Flush()

		// This is the standalone stream the initialize-based revisions define for server-initiated
		// messages, and it is where a legacy client listens for notifications/resources/list_changed.
		// It used to carry nothing but keep-alives, which made the listChanged capability those
		// clients are told about a promise with no delivery channel behind it. The 2026-07-28
		// revision replaced this stream with subscriptions/listen, so nothing modern is served here
		// — note the absence of a subscriptionId, which is a modern-era field.
		var updates chan WikiUpdate
		if srv.EventBus != nil {
			updates = srv.EventBus.SubscribeWikiUpdates()
			defer srv.EventBus.UnsubscribeWikiUpdates(updates)
		}

		// Keep stream open with periodic keepalives
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		notify := r.Context().Done()
		for {
			select {
			case <-notify:
				return
			case <-srv.shutdownSignal():
				return // let the process shut down instead of holding the connection open
			case update, ok := <-updates:
				if !ok {
					return
				}
				// An edit changes a document's contents; only a create or delete changes which
				// documents exist, and the list is what this notification is about.
				if update.Type != "article-added" && update.Type != "article-removed" {
					continue
				}
				payload, err := json.Marshal(map[string]interface{}{
					"jsonrpc": "2.0",
					"method":  "notifications/resources/list_changed",
				})
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
					return
				}
				flusher.Flush()
			case <-ticker.C:
				_, _ = fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
			}
		}
	case http.MethodPost:
		// Read body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			// LimitRequestBodies caps this endpoint at 8 MB, and overrunning that cap surfaces
			// here as a read failure. Reporting it as a flat 400 told a client its request was
			// malformed when the request was fine and merely too big — the same misdirection
			// §2.8 fixed for the REST handlers, which all route through writeDecodeError. The
			// MCP endpoint was the one that never got it, so it answers 413 naming the limit.
			writeDecodeError(w, err)
			return
		}
		defer func() { _ = r.Body.Close() }()

		req, rejected := decodeRequest(body)
		if rejected != nil {
			// 400 on either era, for -32600 as for -32700. The era is read from a valid request's
			// params, so a message rejected here has none; and 400 is both what the transport
			// requires for input the server cannot accept and what the modern era uses for its
			// own protocol failures.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			sendError(w, rejected.code, rejected.message, rejected.id)
			return
		}

		// Hand the transport context to the dispatcher: the modern era verifies the mirrored
		// HTTP headers against the body, and reports protocol failures as HTTP status codes.
		req.Headers = r.Header
		env := parseParamsEnvelope(req.Params)
		req.IsModern = isModernRequest(req.Headers, env)

		// subscriptions/listen is intercepted before dispatch because its response *is* a stream:
		// it stays open delivering notifications rather than producing one buffered body. Modern
		// metadata is still validated first so a bad request fails the same way it would elsewhere.
		if req.Method == "subscriptions/listen" && req.ID != nil {
			if req.IsModern {
				if rpcErr := validateModernMeta(env); rpcErr != nil {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(modernErrorStatus(rpcErr))
					var out bytes.Buffer
					srv.writeResponse(&out, &req, nil, rpcErr)
					_, _ = w.Write(out.Bytes())
					return
				}
				if rpcErr := validateModernHeaders(req.Headers, req.Method, env); rpcErr != nil {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(modernErrorStatus(rpcErr))
					var out bytes.Buffer
					srv.writeResponse(&out, &req, nil, rpcErr)
					_, _ = w.Write(out.Bytes())
					return
				}
			}
			srv.streamSubscription(w, r, &req, parseSubscriptionParams(req.Params).honored())
			return
		}

		// Render into a buffer first. Committing 200 before dispatch made every outcome a 200 —
		// the status could never reflect a failure, and a panic or write error mid-render would
		// emit a truncated body under a success code.
		var out bytes.Buffer
		status := srv.handleRequest(&out, &req)

		w.Header().Set("Content-Type", "application/json")

		// A notification (no id) produces no response body; the spec calls for 202 Accepted.
		if out.Len() == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		w.WriteHeader(status)
		_, _ = w.Write(out.Bytes())
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

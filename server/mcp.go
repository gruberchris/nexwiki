package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// JSONRPCRequest represents an incoming request in the JSON-RPC 2.0 format.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id,omitempty"`

	// Headers carries the HTTP headers when the request arrived over Streamable HTTP, so the
	// modern era can verify the mirrored metadata against the body. Nil on stdio.
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
}

// StartMCPServer runs the stdio MCP JSON-RPC protocol loop in a non-blocking background goroutine.
func (srv *Server) StartMCPServer() {
	scanner := bufio.NewScanner(os.Stdin)
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
	writer := os.Stdout

	_, _ = fmt.Fprintf(os.Stderr, "Always-on stdio MCP server loop successfully started in background!\n")

	// Read lines of JSON-RPC requests from standard input
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			sendError(writer, -32700, "Parse error: invalid JSON", nil)
			continue
		}
		req.FromStdio = true

		// Handle request methods
		srv.handleRequest(writer, &req)
	}

	// The loop above cannot resume after a scanner failure, so tell the client rather than going
	// quiet: an agent waiting on a response it will never receive has no way to distinguish a dead
	// channel from a slow one.
	if err := scanner.Err(); err != nil && err != io.EOF {
		if errors.Is(err, bufio.ErrTooLong) {
			sendError(writer, -32700, fmt.Sprintf(
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
	// Notifications (requests without an ID) can be ignored or logged to stderr
	if req.ID == nil {
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
		if isModernRequest(env) {
			if rpcErr := validateModernMeta(env); rpcErr != nil {
				return srv.writeResponse(w, req, nil, rpcErr)
			}
		}
		srv.handleStdioSubscription(w, req, parseSubscriptionParams(req.Params))
		return http.StatusOK
	}

	if isModernRequest(env) {
		result, rpcErr = srv.dispatchModern(req, env)
		return srv.writeResponse(w, req, result, rpcErr)
	}

	switch req.Method {
	case "initialize":
		// Capture who is connecting, so their writes are attributable. Only on stdio — see
		// JSONRPCRequest.FromStdio.
		if req.FromStdio {
			srv.rememberStdioClient(req.Params)
		}
		result = map[string]interface{}{
			"protocolVersion": negotiateProtocolVersion(req.Params),
			"capabilities":    serverCapabilities(),
			"serverInfo":      srv.implementation(),
			// Connect-time hint surfaced by MCP clients as a system-prompt-style nudge, so
			// the agent reaches for NexWiki as a second brain without explicit prompting.
			"instructions": agentInstructions(),
		}

	case "tools/list":
		result = map[string]interface{}{
			"tools": toolSchemas(),
		}

	case "tools/call":
		result, rpcErr = srv.executeToolCall(req.Params, srv.resolveAgent(env))

	case "prompts/list":
		result = map[string]interface{}{
			"prompts": promptDefinitions(),
		}

	case "prompts/get":
		result, rpcErr = srv.getPrompt(req.Params)

	case "resources/list":
		result, rpcErr = srv.listResources()

	case "resources/templates/list":
		result, rpcErr = srv.listResourceTemplates()

	case "resources/read":
		result, rpcErr = srv.readResource(req.Params)

	default:
		rpcErr = &JSONRPCError{
			Code:    -32601,
			Message: fmt.Sprintf("Method not found: %s", req.Method),
		}
	}

	return srv.writeResponse(w, req, result, rpcErr)
}

// dispatchModern validates a per-request-metadata request and runs it, wrapping a successful
// payload in the modern result envelope (resultType plus server identity).
func (srv *Server) dispatchModern(req *JSONRPCRequest, env paramsEnvelope) (interface{}, *JSONRPCError) {
	if rpcErr := validateModernMeta(env); rpcErr != nil {
		return nil, rpcErr
	}
	if rpcErr := validateModernHeaders(req.Headers, req.Method, env); rpcErr != nil {
		return nil, rpcErr
	}

	payload, rpcErr := srv.handleModernMethod(req.Method, env)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return srv.completeResult(payload), nil
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

	action := "read"
	tool := args.Name
	if strings.HasPrefix(tool, "create_") {
		action = "create"
	} else if strings.HasPrefix(tool, "edit_") || strings.HasPrefix(tool, "append_") || strings.HasPrefix(tool, "revert_") || strings.HasPrefix(tool, "update_") {
		action = "edit"
	} else if strings.HasPrefix(tool, "delete_") {
		action = "delete"
	}

	slug := common.Slug
	if slug == "" && common.Title != "" {
		slug = Slugify(common.Title)
	}

	// Determine category and title if it's a mutation
	title := common.Title
	if title == "" && slug != "" {
		if art, err := srv.Storage.GetArticle(slug); err == nil {
			title = art.Title
		}
	}

	if agent == "" {
		agent = DefaultAgentName
	}

	srv.EventBus.PublishActivity("mcp", action, tool, slug, title, agent)

	// When running as a mcp-only sidecar alongside a web server, forward the event to it.

	// If it's a mutation, broadcast a WikiUpdate to sync all clients!
	if action != "read" {
		articles, err := srv.Storage.ListArticles()
		if err == nil {
			var targetTags []string
			targetType := ContentTypeWiki
			if slug != "" {
				if art, err := srv.Storage.GetArticle(slug); err == nil {
					targetTags = art.Tags
					targetType = art.Type
				}
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
		srv.logMCPToolCall(params, agent)
	}
	return result, rpcErr
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

// HandleStreamableHTTP implements the Streamable HTTP transport (2025 Spec)
// supporting GET (initiating SSE stream) and POST (synchronous JSON-RPC).
func (srv *Server) HandleStreamableHTTP(w http.ResponseWriter, r *http.Request) {
	// Validate the browser Origin before doing anything else. Every MCP tool — including
	// delete_wiki_article and export_okf_bundle — is reachable here with no authentication,
	// so an unvalidated origin is full read/write access to the knowledge base.
	applySecurityHeaders(w)
	allowOrigin, originOK := originAllowed(r.Header.Get("Origin"), r.Host)
	if !originOK {
		applyCORSHeaders(w, "", "GET, POST, OPTIONS", "Content-Type, MCP-Protocol-Version, MCP-Session-Id")
		http.Error(w, "origin not allowed; set "+AllowedOriginsEnv+" to permit it", http.StatusForbidden)
		return
	}
	applyCORSHeaders(w, allowOrigin, "GET, POST, OPTIONS", "Content-Type, MCP-Protocol-Version, MCP-Session-Id")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

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

		var req JSONRPCRequest
		if err := json.Unmarshal(body, &req); err != nil {
			// Send JSON-RPC Parse Error Response
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			sendError(w, -32700, "Parse error: invalid JSON", nil)
			return
		}

		// Hand the transport context to the dispatcher: the modern era verifies the mirrored
		// HTTP headers against the body, and reports protocol failures as HTTP status codes.
		req.Headers = r.Header
		env := parseParamsEnvelope(req.Params)
		req.IsModern = isModernRequest(env)

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

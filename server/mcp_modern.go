package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Modern-era (2026-07-28) MCP support.
//
// Revision 2026-07-28 reshaped the protocol: there is no `initialize` handshake and no session.
// Every request instead carries its protocol version and client capabilities in `_meta`, and the
// server answers each request independently. NexWiki is *dual-era* — it serves this revision and
// the older initialize-based revisions on the same endpoint, choosing per request based on how
// the client opens. See mcp.go for the routing and docs/mcp_server.md for the user-facing view.

// ModernProtocolVersion is the newest revision NexWiki implements in the per-request-metadata era.
const ModernProtocolVersion = "2026-07-28"

// modernProtocolVersions lists the per-request-metadata revisions NexWiki accepts, newest first.
// Legacy (initialize-based) revisions are negotiated separately by negotiateProtocolVersion.
var modernProtocolVersions = []string{ModernProtocolVersion}

// Reserved `_meta` keys defined by the specification for per-request protocol fields.
const (
	metaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaClientInfo         = "io.modelcontextprotocol/clientInfo"
	metaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaServerInfo         = "io.modelcontextprotocol/serverInfo"
	metaSubscriptionID     = "io.modelcontextprotocol/subscriptionId"
)

// MCP-defined error codes in the specification's reserved -32020..-32099 sub-range, and the
// standard JSON-RPC codes.
const (
	errCodeHeaderMismatch             = -32020
	errCodeMissingClientCapability    = -32021
	errCodeUnsupportedProtocolVersion = -32022
	errCodeParseError                 = -32700
	errCodeInvalidRequest             = -32600
	errCodeMethodNotFound             = -32601
	errCodeInvalidParams              = -32602
	errCodeInternal                   = -32603
)

// requestMeta holds the per-request protocol fields a modern client sends in `params._meta`.
type requestMeta struct {
	ProtocolVersion    string          `json:"io.modelcontextprotocol/protocolVersion"`
	ClientInfo         json.RawMessage `json:"io.modelcontextprotocol/clientInfo"`
	ClientCapabilities json.RawMessage `json:"io.modelcontextprotocol/clientCapabilities"`
}

// paramsEnvelope is the subset of any request's params needed for era detection, header
// validation, and metadata extraction. Tool arguments are decoded separately by each handler.
type paramsEnvelope struct {
	Meta *requestMeta `json:"_meta"`
	Name string       `json:"name"`
	URI  string       `json:"uri"`
	// Cursor is the pagination position on the four list methods. Decoded here rather than in each
	// handler because it is protocol-level, exactly like Name and URI.
	Cursor string          `json:"cursor"`
	Raw    json.RawMessage `json:"-"`
}

// parseParamsEnvelope decodes the protocol-level fields of a request's params. Malformed params
// are reported as absent rather than fatal: the legacy path tolerates them, and the modern path
// rejects the request on the missing protocol version anyway.
func parseParamsEnvelope(params json.RawMessage) paramsEnvelope {
	env := paramsEnvelope{Raw: params}
	if len(params) == 0 {
		return env
	}
	_ = json.Unmarshal(params, &env)
	return env
}

// isModernRequest reports whether a request opted into the per-request-metadata era.
//
// Two signals, because either one alone leaves a hole. The body's
// `_meta["io.modelcontextprotocol/protocolVersion"]` is the canonical discriminator and the only
// one stdio has. Over HTTP the `MCP-Protocol-Version` header MUST name the same revision, so a
// request whose header claims a modern version is a modern request even when its body omits the
// metadata.
//
// That second signal is what closes the hole. Such a request used to fall through to the legacy
// switch and be served under legacy semantics — a modern client with a mis-built body got a
// plausible-looking legacy answer and no indication anything was wrong. The specification is
// explicit that a request missing a required `_meta` field is malformed and MUST be rejected with
// -32602, so recognising the era first is what lets us say so.
func isModernRequest(headers http.Header, env paramsEnvelope) bool {
	if env.Meta != nil && env.Meta.ProtocolVersion != "" {
		return true
	}
	return headers != nil && supportsModernVersion(headers.Get("MCP-Protocol-Version"))
}

// supportsModernVersion reports whether NexWiki implements the requested revision.
func supportsModernVersion(version string) bool {
	for _, v := range modernProtocolVersions {
		if v == version {
			return true
		}
	}
	return false
}

// validateModernMeta enforces the required per-request protocol fields. protocolVersion must name
// a revision we implement; clientCapabilities must be present so the server never relies on a
// capability the client did not declare.
func validateModernMeta(env paramsEnvelope) *JSONRPCError {
	// Reachable when the era was decided by the MCP-Protocol-Version header alone: the request is
	// modern, but its body never carried the field the header mirrors.
	if env.Meta == nil || env.Meta.ProtocolVersion == "" {
		return &JSONRPCError{
			Code:    errCodeInvalidParams,
			Message: "Missing required _meta field: " + metaProtocolVersion,
		}
	}
	if !supportsModernVersion(env.Meta.ProtocolVersion) {
		return &JSONRPCError{
			Code:    errCodeUnsupportedProtocolVersion,
			Message: "Unsupported protocol version",
			Data: map[string]interface{}{
				"supported": modernProtocolVersions,
				"requested": env.Meta.ProtocolVersion,
			},
		}
	}
	// clientCapabilities is a required field; an explicit empty object is valid, a missing one is not.
	if len(env.Meta.ClientCapabilities) == 0 {
		return &JSONRPCError{
			Code:    errCodeInvalidParams,
			Message: "Missing required _meta field: " + metaClientCapabilities,
		}
	}
	return nil
}

// decodeHeaderValue reverses the specification's Base64 sentinel encoding, which clients use when
// a value cannot be represented as a plain ASCII header (non-ASCII, control characters, or
// surrounding whitespace). Values without the sentinel are returned unchanged.
func decodeHeaderValue(value string) string {
	const prefix, suffix = "=?base64?", "?="
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, suffix) {
		return value
	}
	encoded := value[len(prefix) : len(value)-len(suffix)]
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return value // leave it alone; the mismatch check will reject it
	}
	return string(decoded)
}

// methodsWithNameHeader maps the methods that must mirror a name/URI into the Mcp-Name header to
// the params field that supplies it.
var methodsWithNameHeader = map[string]func(paramsEnvelope) string{
	"tools/call":     func(e paramsEnvelope) string { return e.Name },
	"prompts/get":    func(e paramsEnvelope) string { return e.Name },
	"resources/read": func(e paramsEnvelope) string { return e.URI },
}

// validateModernHeaders checks the HTTP headers a modern client mirrors from the request body.
// The body is the source of truth; the headers exist so intermediaries can route without parsing
// it. Allowing them to disagree is a real security problem — a proxy could route on one value
// while the server executes another — so a mismatch is rejected rather than reconciled.
//
// Requests arriving over stdio have no headers; pass a nil header set to skip the check.
func validateModernHeaders(headers http.Header, method string, env paramsEnvelope) *JSONRPCError {
	if headers == nil {
		return nil
	}

	mismatch := func(format string, args ...interface{}) *JSONRPCError {
		return &JSONRPCError{Code: errCodeHeaderMismatch, Message: fmt.Sprintf(format, args...)}
	}

	version := headers.Get("MCP-Protocol-Version")
	if version == "" {
		return mismatch("Missing required header: MCP-Protocol-Version")
	}
	if version != env.Meta.ProtocolVersion {
		return mismatch("Header mismatch: MCP-Protocol-Version header value %q does not match body value %q",
			version, env.Meta.ProtocolVersion)
	}

	headerMethod := headers.Get("Mcp-Method")
	if headerMethod == "" {
		return mismatch("Missing required header: Mcp-Method")
	}
	if headerMethod != method {
		return mismatch("Header mismatch: Mcp-Method header value %q does not match body value %q",
			headerMethod, method)
	}

	if extract, needsName := methodsWithNameHeader[method]; needsName {
		bodyName := extract(env)
		// A body with no name to mirror is a malformed request, not a header failure. Reporting it
		// as -32020 sent the client looking for a header problem it could not fix, because the
		// header it was told to add has no value to carry.
		if bodyName == "" {
			return &JSONRPCError{
				Code:    errCodeInvalidParams,
				Message: fmt.Sprintf("Missing or invalid name in params for %s", method),
			}
		}
		headerName := decodeHeaderValue(headers.Get("Mcp-Name"))
		if headerName == "" {
			return mismatch("Missing required header: Mcp-Name")
		}
		if headerName != bodyName {
			return mismatch("Header mismatch: Mcp-Name header value %q does not match body value %q",
				headerName, bodyName)
		}
	}

	return nil
}

// Cacheable-result TTLs. The tool, prompt, and resource-template lists are compiled into the
// binary and cannot change while the process runs, so they stay fresh for an hour. Article data is
// the user's own content and changes the moment a page is edited, so it gets a short TTL — and
// clients holding a subscriptions/listen stream are told immediately, which invalidates the entry
// well before it expires.
const (
	staticResultTTLMs  = 3600000 // 1 hour
	articleResultTTLMs = 30000   // 30 seconds
)

// The two cache scopes the specification defines, mirroring HTTP Cache-Control.
const (
	cacheScopePublic  = "public"
	cacheScopePrivate = "private"
)

// cachingHints returns the caching metadata a complete result MUST carry for the given method,
// and whether that method is cacheable at all.
//
// The 2026-07-28 revision requires ttlMs and cacheScope on every `resultType: "complete"` result
// for the six methods below (spec: Server Utilities -> Caching, SEP-2549). Omitting them is not a
// soft failure. A conformant client validates the result against a schema in which both fields
// are required, so a missing ttlMs rejects the entire response: the client reports the server
// connected and healthy, then lists zero tools.
//
// tools/call and prompts/get are deliberately absent. The spec does not list them as cacheable,
// and a tool call is by definition not a repeatable read.
func cachingHints(method string) (ttlMs int, scope string, cacheable bool) {
	switch method {
	case "server/discover", "tools/list", "prompts/list", "resources/templates/list":
		// Identical for every caller and free of user data, so a shared gateway may serve one
		// cached copy to anyone.
		return staticResultTTLMs, cacheScopePublic, true

	case "resources/list", "resources/read":
		// Article slugs, titles, and bodies are the user's knowledge base. Never reuse one
		// caller's cache entry for another authorization context.
		return articleResultTTLMs, cacheScopePrivate, true

	default:
		return 0, "", false
	}
}

// completeResult wraps a handler's payload in the modern result envelope. Every modern result
// MUST carry a resultType, servers SHOULD identify themselves in the result's `_meta`, and a
// cacheable method's result MUST carry the caching hints cachingHints supplies for it.
func (srv *Server) completeResult(method string, payload interface{}) map[string]interface{} {
	result := map[string]interface{}{}

	// Merge the handler's own fields in, so callers keep returning plain maps/structs.
	switch typed := payload.(type) {
	case map[string]interface{}:
		for k, v := range typed {
			result[k] = v
		}
	case nil:
		// nothing to merge
	default:
		// Non-map payloads (e.g. ToolResponse) round-trip through JSON so their fields land at
		// the top level of the result object, as the schema expects.
		if encoded, err := json.Marshal(typed); err == nil {
			var fields map[string]interface{}
			if json.Unmarshal(encoded, &fields) == nil {
				for k, v := range fields {
					result[k] = v
				}
			}
		}
	}

	result["resultType"] = "complete"
	if ttlMs, scope, cacheable := cachingHints(method); cacheable {
		result["ttlMs"] = ttlMs
		result["cacheScope"] = scope
	}
	result["_meta"] = map[string]interface{}{
		metaServerInfo: srv.implementation(),
	}
	return result
}

// implementation is NexWiki's self-reported identity, shared by discover and result metadata.
func (srv *Server) implementation() map[string]interface{} {
	return map[string]interface{}{
		"name":    "NexWiki MCP Server",
		"version": srv.Version,
	}
}

// Capabilities are declared per era, because the same word promises a different method in each.
//
// `resources.subscribe` is the reason this is split. In the 2026-07-28 revision it means the server
// honours `resourceSubscriptions` on a subscriptions/listen stream, which NexWiki does. In the
// initialize-based revisions it means the server implements the `resources/subscribe` RPC, which
// NexWiki does not and deliberately will not — that RPC was replaced by subscriptions/listen.
// Advertising one capability set to both eras therefore told legacy clients about a method that
// answers -32601. A capability is a promise, so each era is told only what is true for it.
//
// `tools` and `prompts` stay bare in both: the registry is compiled into the binary and cannot
// change while the process runs, so claiming listChanged would promise a notification that can
// never arrive.

// modernServerCapabilities is what server/discover reports.
func modernServerCapabilities() map[string]interface{} {
	return map[string]interface{}{
		"tools":       map[string]interface{}{},
		"prompts":     map[string]interface{}{},
		"completions": map[string]interface{}{},
		"resources":   modernResourceCapability(),
	}
}

// legacyServerCapabilities is what the initialize result reports.
func legacyServerCapabilities() map[string]interface{} {
	return map[string]interface{}{
		"tools":       map[string]interface{}{},
		"prompts":     map[string]interface{}{},
		"completions": map[string]interface{}{},
		"resources":   legacyResourceCapability(),
	}
}

// agentInstructions is the connect-time hint MCP clients surface as a system-prompt-style nudge,
// so an agent reaches for NexWiki as a second brain without being told to every session. Shared
// by the legacy initialize result and the modern discover result.
//
// It carries only the universal rules, and names no document: the operator's own conventions are
// ordinary user and feedback memories, which get_wiki_overview returns as pinned_memories, and the
// rules a single tool enforces live in that tool's description. Keep it short — every client
// injects it into every session.
func agentInstructions() string {
	return "NexWiki is the user's persistent second brain: keep plans, durable facts and prior knowledge " +
		"here, not only in chat. At session start call get_wiki_overview once — its pinned_memories are " +
		"the operator's standing preferences and corrections; follow them. Search with search_wiki (no " +
		"query lists the index) before writing. Save multi-step work as save_article(type: \"AI-Agent-Plan\", " +
		"project_context) and durable facts as save_article(type: \"AI-Agent-Memory\") with memory_kind, " +
		"description and source. Session discipline: run each orientation call once; a \"not found\" is " +
		"an answer, not a reason to search again; on a version conflict, retry once with the version " +
		"the error names."
}

// handleModernMethod dispatches a request that opted into the per-request-metadata era. It shares
// the tool registry and prompt definitions with the legacy path; only the envelope differs.
func (srv *Server) handleModernMethod(req *JSONRPCRequest, env paramsEnvelope) (interface{}, *JSONRPCError) {
	switch req.Method {
	case "server/discover":
		// MUST be implemented by modern servers: it lets a client learn supported versions,
		// capabilities, and identity in one request before sending anything else.
		return map[string]interface{}{
			"supportedVersions": modernProtocolVersions,
			"capabilities":      modernServerCapabilities(),
			"instructions":      agentInstructions(),
		}, nil

	case "tools/list":
		return listTools(env.Cursor)

	case "tools/call":
		// The modern era carries clientInfo in _meta on every request, so attribution needs no
		// handshake and no session — the identity is right here in the envelope.
		return srv.executeToolCall(env.Raw, srv.resolveAgent(req, env))

	case "prompts/list":
		return listPrompts(env.Cursor)

	case "prompts/get":
		return srv.getPrompt(env.Raw)

	case "resources/list":
		return srv.listResources(env.Cursor)

	case "resources/templates/list":
		return srv.listResourceTemplates(env.Cursor)

	case "resources/read":
		return srv.readResource(env.Raw)

	case "completion/complete":
		return srv.complete(env.Raw)

	default:
		return nil, &JSONRPCError{
			Code:    errCodeMethodNotFound,
			Message: fmt.Sprintf("Method not found: %s", req.Method),
		}
	}
}

// modernErrorStatus maps a modern-era JSON-RPC error to its required HTTP status. The spec is
// explicit that these surface as HTTP failures, not as 200 responses carrying an error body —
// that is how a dual-era client tells a modern server from a legacy one.
func modernErrorStatus(err *JSONRPCError) int {
	switch err.Code {
	case errCodeMethodNotFound:
		return http.StatusNotFound
	case errCodeHeaderMismatch, errCodeMissingClientCapability,
		errCodeUnsupportedProtocolVersion, errCodeInvalidParams:
		return http.StatusBadRequest
	default:
		return http.StatusOK
	}
}

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
)

// MCP-defined error codes in the specification's reserved -32020..-32099 sub-range.
const (
	errCodeHeaderMismatch             = -32020
	errCodeMissingClientCapability    = -32021
	errCodeUnsupportedProtocolVersion = -32022
	errCodeMethodNotFound             = -32601
	errCodeInvalidParams              = -32602
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
	Meta *requestMeta    `json:"_meta"`
	Name string          `json:"name"`
	URI  string          `json:"uri"`
	Raw  json.RawMessage `json:"-"`
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

// isModernRequest reports whether a request opted into the per-request-metadata era. Presence of
// `_meta["io.modelcontextprotocol/protocolVersion"]` is the discriminator: legacy clients never
// send it, and modern clients MUST send it on every request.
func isModernRequest(env paramsEnvelope) bool {
	return env.Meta != nil && env.Meta.ProtocolVersion != ""
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

// completeResult wraps a handler's payload in the modern result envelope. Every modern result
// MUST carry a resultType, and servers SHOULD identify themselves in the result's `_meta`.
func (srv *Server) completeResult(payload interface{}) map[string]interface{} {
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

// serverCapabilities lists what NexWiki implements. Resources and subscriptions are deliberately
// absent — advertising a capability the server does not serve is worse than omitting it.
func serverCapabilities() map[string]interface{} {
	return map[string]interface{}{
		"tools":   map[string]interface{}{},
		"prompts": map[string]interface{}{},
	}
}

// agentInstructions is the connect-time hint MCP clients surface as a system-prompt-style nudge,
// so an agent reaches for NexWiki as a second brain without being told to every session. Shared
// by the legacy initialize result and the modern discover result.
func agentInstructions() string {
	return "This NexWiki server is the user's persistent second brain. Use it to store plans and " +
		"memories and to look up prior knowledge — do not keep that only in chat. At session start, load the " +
		"operating rules with read_article(slug: \"" + AgentGuidelinesSlug + "\"), then get_context_overview " +
		"and get_recent_activity(since: \"48h\") to orient. Save multi-step work with create_agent_plan, " +
		"durable facts with create_agent_memory (setting description and source), and search before writing."
}

// handleModernMethod dispatches a request that opted into the per-request-metadata era. It shares
// the tool registry and prompt definitions with the legacy path; only the envelope differs.
func (srv *Server) handleModernMethod(method string, env paramsEnvelope) (interface{}, *JSONRPCError) {
	switch method {
	case "server/discover":
		// MUST be implemented by modern servers: it lets a client learn supported versions,
		// capabilities, and identity in one request before sending anything else.
		return map[string]interface{}{
			"supportedVersions": modernProtocolVersions,
			"capabilities":      serverCapabilities(),
			"instructions":      agentInstructions(),
		}, nil

	case "tools/list":
		return map[string]interface{}{"tools": toolSchemas()}, nil

	case "tools/call":
		return srv.executeToolCall(env.Raw)

	case "prompts/list":
		return map[string]interface{}{"prompts": promptDefinitions()}, nil

	case "prompts/get":
		return srv.getPrompt(env.Raw)

	default:
		return nil, &JSONRPCError{
			Code:    errCodeMethodNotFound,
			Message: fmt.Sprintf("Method not found: %s", method),
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

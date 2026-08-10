package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// modernParams builds a params object carrying the required per-request protocol metadata.
func modernParams(t *testing.T, version string, extra map[string]interface{}) json.RawMessage {
	t.Helper()
	params := map[string]interface{}{
		"_meta": map[string]interface{}{
			metaProtocolVersion:    version,
			metaClientCapabilities: map[string]interface{}{},
			metaClientInfo:         map[string]interface{}{"name": "TestClient", "version": "1.0.0"},
		},
	}
	for k, v := range extra {
		params[k] = v
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return raw
}

// callModern dispatches a request through the full handler and returns the decoded envelope
// alongside the HTTP status the transport would apply.
func callModern(t *testing.T, srv *Server, method string, params json.RawMessage, headers http.Header) (map[string]interface{}, int) {
	t.Helper()
	req := &JSONRPCRequest{
		JSONRPC:  "2.0",
		Method:   method,
		Params:   params,
		ID:       1,
		Headers:  headers,
		IsModern: isModernRequest(parseParamsEnvelope(params)),
	}
	var buf bytes.Buffer
	status := srv.handleRequest(&buf, req)

	var envelope map[string]interface{}
	if buf.Len() > 0 {
		if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
			t.Fatalf("response is not valid JSON: %v\n%s", err, buf.String())
		}
	}
	return envelope, status
}

func errorCode(t *testing.T, envelope map[string]interface{}) int {
	t.Helper()
	errObj, ok := envelope["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected an error envelope, got %v", envelope)
	}
	code, ok := errObj["code"].(float64)
	if !ok {
		t.Fatalf("error has no numeric code: %v", errObj)
	}
	return int(code)
}

// TestModernRequestsAreDetectedByMeta pins the era discriminator: presence of the per-request
// protocolVersion in `_meta`. Legacy clients never send it, so the two eras cannot be confused.
func TestModernRequestsAreDetectedByMeta(t *testing.T) {
	modern := parseParamsEnvelope(modernParams(t, ModernProtocolVersion, nil))
	if !isModernRequest(modern) {
		t.Error("a request carrying _meta protocolVersion must be treated as modern")
	}

	legacy := parseParamsEnvelope(json.RawMessage(`{"protocolVersion":"2025-06-18"}`))
	if isModernRequest(legacy) {
		t.Error("an initialize-style request must not be treated as modern")
	}

	if isModernRequest(parseParamsEnvelope(nil)) {
		t.Error("a request with no params must not be treated as modern")
	}
}

// TestLegacyInitializeStillWorks is the dual-era guarantee: existing clients keep working
// untouched. This is the regression that would break every currently-connected agent.
func TestLegacyInitializeStillWorks(t *testing.T) {
	srv := newMCPServer(t)

	envelope, status := callModern(t, srv, "initialize", json.RawMessage(`{"protocolVersion":"2025-06-18"}`), nil)
	if status != http.StatusOK {
		t.Errorf("legacy initialize should be HTTP 200, got %d", status)
	}
	result, ok := envelope["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a result, got %v", envelope)
	}
	if result["protocolVersion"] != "2025-06-18" {
		t.Errorf("legacy initialize should echo the requested version, got %v", result["protocolVersion"])
	}
	// The legacy envelope must NOT gain the modern resultType wrapper.
	if _, present := result["resultType"]; present {
		t.Error("legacy results must not carry resultType")
	}
}

// TestModernResultsCarryResultTypeAndServerInfo pins the modern result envelope: every result
// MUST include resultType, and servers SHOULD identify themselves in the result's _meta.
func TestModernResultsCarryResultTypeAndServerInfo(t *testing.T) {
	srv := newMCPServer(t)

	envelope, status := callModern(t, srv, "tools/list", modernParams(t, ModernProtocolVersion, nil), nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	result, ok := envelope["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a result, got %v", envelope)
	}
	if result["resultType"] != "complete" {
		t.Errorf(`expected resultType "complete", got %v`, result["resultType"])
	}
	meta, ok := result["_meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("modern results should carry _meta, got %v", result)
	}
	if _, ok := meta[metaServerInfo]; !ok {
		t.Errorf("modern results should identify the server via %s", metaServerInfo)
	}
	tools, ok := result["tools"].([]interface{})
	if !ok || len(tools) != 29 {
		t.Errorf("expected 29 tools in the modern envelope, got %v", result["tools"])
	}
}

// TestServerDiscover covers the method modern servers MUST implement.
func TestServerDiscover(t *testing.T) {
	srv := newMCPServer(t)

	envelope, status := callModern(t, srv, "server/discover", modernParams(t, ModernProtocolVersion, nil), nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	result := envelope["result"].(map[string]interface{})

	versions, ok := result["supportedVersions"].([]interface{})
	if !ok || len(versions) == 0 {
		t.Fatalf("discover must list supportedVersions, got %v", result["supportedVersions"])
	}
	if versions[0] != ModernProtocolVersion {
		t.Errorf("expected %s in supportedVersions, got %v", ModernProtocolVersion, versions[0])
	}
	caps, ok := result["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("discover must report capabilities, got %v", result["capabilities"])
	}
	for _, want := range []string{"tools", "prompts", "resources"} {
		if _, ok := caps[want]; !ok {
			t.Errorf("capabilities should advertise %q", want)
		}
	}
	// The resources sub-features are claimed only because they are genuinely served: articles
	// are created and deleted (listChanged) and edited (subscribe).
	resources, ok := caps["resources"].(map[string]interface{})
	if !ok {
		t.Fatalf("resources capability should be an object, got %T", caps["resources"])
	}
	if resources["listChanged"] != true || resources["subscribe"] != true {
		t.Errorf("resources capability should claim listChanged and subscribe, got %v", resources)
	}
	// A capability NexWiki does not serve must not be advertised.
	if _, ok := caps["completions"]; ok {
		t.Error("capabilities must not advertise unimplemented completions")
	}
	if _, ok := result["instructions"].(string); !ok {
		t.Error("discover should carry instructions for the agent")
	}
}

// TestUnsupportedProtocolVersion pins the -32022 shape: the error must list what the server does
// support so the client can retry with a mutually supported version instead of giving up.
func TestUnsupportedProtocolVersion(t *testing.T) {
	srv := newMCPServer(t)

	envelope, status := callModern(t, srv, "tools/list", modernParams(t, "1900-01-01", nil), nil)
	if status != http.StatusBadRequest {
		t.Errorf("unsupported version must be HTTP 400, got %d", status)
	}
	if code := errorCode(t, envelope); code != errCodeUnsupportedProtocolVersion {
		t.Errorf("expected code %d, got %d", errCodeUnsupportedProtocolVersion, code)
	}
	data := envelope["error"].(map[string]interface{})["data"].(map[string]interface{})
	if data["requested"] != "1900-01-01" {
		t.Errorf("error data should echo the requested version, got %v", data["requested"])
	}
	if supported, ok := data["supported"].([]interface{}); !ok || len(supported) == 0 {
		t.Errorf("error data must list supported versions, got %v", data["supported"])
	}
}

// TestMissingClientCapabilitiesRejected pins that a required _meta field cannot be omitted.
func TestMissingClientCapabilitiesRejected(t *testing.T) {
	srv := newMCPServer(t)

	params := json.RawMessage(`{"_meta":{"` + metaProtocolVersion + `":"` + ModernProtocolVersion + `"}}`)
	envelope, status := callModern(t, srv, "tools/list", params, nil)

	if status != http.StatusBadRequest {
		t.Errorf("missing clientCapabilities must be HTTP 400, got %d", status)
	}
	if code := errorCode(t, envelope); code != errCodeInvalidParams {
		t.Errorf("expected code %d, got %d", errCodeInvalidParams, code)
	}
}

// TestModernUnknownMethodIs404 pins the status that lets a dual-era client tell a modern server
// from a legacy HTTP+SSE server that simply does not host the endpoint.
func TestModernUnknownMethodIs404(t *testing.T) {
	srv := newMCPServer(t)

	envelope, status := callModern(t, srv, "does/notexist", modernParams(t, ModernProtocolVersion, nil), nil)
	if status != http.StatusNotFound {
		t.Errorf("unknown modern method must be HTTP 404, got %d", status)
	}
	if code := errorCode(t, envelope); code != errCodeMethodNotFound {
		t.Errorf("expected code %d, got %d", errCodeMethodNotFound, code)
	}
}

// TestHeaderBodyValidation covers the mirrored-header contract. Letting headers disagree with the
// body is a real security problem: an intermediary could route on one value while the server acts
// on another, so a mismatch is rejected rather than reconciled.
func TestHeaderBodyValidation(t *testing.T) {
	srv := newMCPServer(t)
	params := modernParams(t, ModernProtocolVersion, map[string]interface{}{
		"name":      "get_status_tags",
		"arguments": map[string]interface{}{},
	})

	good := http.Header{}
	good.Set("MCP-Protocol-Version", ModernProtocolVersion)
	good.Set("Mcp-Method", "tools/call")
	good.Set("Mcp-Name", "get_status_tags")

	if _, status := callModern(t, srv, "tools/call", params, good); status != http.StatusOK {
		t.Errorf("matching headers should succeed, got %d", status)
	}

	tests := []struct {
		name    string
		mutate  func(http.Header)
		wantErr int
	}{
		{"version mismatch", func(h http.Header) { h.Set("MCP-Protocol-Version", "2025-06-18") }, errCodeHeaderMismatch},
		{"version missing", func(h http.Header) { h.Del("MCP-Protocol-Version") }, errCodeHeaderMismatch},
		{"method mismatch", func(h http.Header) { h.Set("Mcp-Method", "tools/list") }, errCodeHeaderMismatch},
		{"method missing", func(h http.Header) { h.Del("Mcp-Method") }, errCodeHeaderMismatch},
		{"name mismatch", func(h http.Header) { h.Set("Mcp-Name", "delete_wiki_article") }, errCodeHeaderMismatch},
		{"name missing", func(h http.Header) { h.Del("Mcp-Name") }, errCodeHeaderMismatch},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := good.Clone()
			tc.mutate(h)
			envelope, status := callModern(t, srv, "tools/call", params, h)
			if status != http.StatusBadRequest {
				t.Errorf("expected HTTP 400, got %d", status)
			}
			if code := errorCode(t, envelope); code != tc.wantErr {
				t.Errorf("expected code %d, got %d", tc.wantErr, code)
			}
		})
	}
}

// TestMcpNameBase64Sentinel covers the encoding clients must use when a name cannot be carried as
// a plain ASCII header value. The server has to decode before comparing, or every non-ASCII tool
// name would look like a mismatch.
func TestMcpNameBase64Sentinel(t *testing.T) {
	name := "search_wiki"
	encoded := "=?base64?" + base64.StdEncoding.EncodeToString([]byte(name)) + "?="

	if got := decodeHeaderValue(encoded); got != name {
		t.Errorf("decodeHeaderValue(%q) = %q, want %q", encoded, got, name)
	}
	if got := decodeHeaderValue(name); got != name {
		t.Errorf("plain values must pass through unchanged, got %q", got)
	}
	if got := decodeHeaderValue("=?base64?not-valid-base64!?="); got != "=?base64?not-valid-base64!?=" {
		t.Errorf("undecodable sentinel should be left alone for the mismatch check, got %q", got)
	}

	srv := newMCPServer(t)
	params := modernParams(t, ModernProtocolVersion, map[string]interface{}{
		"name": name, "arguments": map[string]interface{}{"query": "test"},
	})
	h := http.Header{}
	h.Set("MCP-Protocol-Version", ModernProtocolVersion)
	h.Set("Mcp-Method", "tools/call")
	h.Set("Mcp-Name", encoded)

	if _, status := callModern(t, srv, "tools/call", params, h); status != http.StatusOK {
		t.Errorf("a Base64-encoded Mcp-Name matching the body should be accepted, got %d", status)
	}
}

// TestStdioSkipsHeaderValidation pins that the header contract is HTTP-only — stdio requests have
// no headers and must not be rejected for lacking them.
func TestStdioSkipsHeaderValidation(t *testing.T) {
	srv := newMCPServer(t)
	params := modernParams(t, ModernProtocolVersion, nil)

	if _, status := callModern(t, srv, "tools/list", params, nil); status != http.StatusOK {
		t.Errorf("stdio modern request should succeed without headers, got %d", status)
	}
}

// TestModernToolCallExecutes proves the modern envelope reaches the shared tool registry rather
// than a parallel implementation.
func TestModernToolCallExecutes(t *testing.T) {
	srv := newMCPServer(t)
	params := modernParams(t, ModernProtocolVersion, map[string]interface{}{
		"name": "get_status_tags", "arguments": map[string]interface{}{},
	})

	envelope, status := callModern(t, srv, "tools/call", params, nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	result := envelope["result"].(map[string]interface{})
	if result["resultType"] != "complete" {
		t.Errorf("tool results need resultType, got %v", result["resultType"])
	}
	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatalf("tool result content missing: %v", result)
	}
}

// TestModernPromptsShareDefinitions proves both eras serve the same prompts, so one cannot
// advertise a prompt the other lacks.
func TestModernPromptsShareDefinitions(t *testing.T) {
	srv := newMCPServer(t)

	envelope, _ := callModern(t, srv, "prompts/list", modernParams(t, ModernProtocolVersion, nil), nil)
	modernPrompts := envelope["result"].(map[string]interface{})["prompts"].([]interface{})

	legacyEnvelope, _ := callModern(t, srv, "prompts/list", nil, nil)
	legacyPrompts := legacyEnvelope["result"].(map[string]interface{})["prompts"].([]interface{})

	if len(modernPrompts) != len(legacyPrompts) {
		t.Errorf("prompt counts diverge between eras: modern=%d legacy=%d",
			len(modernPrompts), len(legacyPrompts))
	}
	if len(modernPrompts) != 2 {
		t.Errorf("expected 2 prompts, got %d", len(modernPrompts))
	}
}

// TestStreamableHTTPPropagatesModernStatus is the end-to-end check that a modern protocol failure
// surfaces as a real HTTP status through the transport, not a 200 with an error body.
func TestStreamableHTTPPropagatesModernStatus(t *testing.T) {
	srv := newMCPServer(t)

	body, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
		"params": map[string]interface{}{
			"_meta": map[string]interface{}{
				metaProtocolVersion:    "1900-01-01",
				metaClientCapabilities: map[string]interface{}{},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8080")
	req.Header.Set("MCP-Protocol-Version", "1900-01-01")
	req.Header.Set("Mcp-Method", "tools/list")
	w := httptest.NewRecorder()

	srv.HandleStreamableHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("unsupported version over HTTP must be 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Unsupported protocol version") {
		t.Errorf("expected an UnsupportedProtocolVersion body, got %s", w.Body.String())
	}
}

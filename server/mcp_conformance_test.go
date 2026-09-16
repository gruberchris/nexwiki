package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Conformance tests for MCP revision 2026-07-28.
//
// These differ in purpose from the rest of the MCP suite, which checks that NexWiki's features
// behave. These check that NexWiki's *wire protocol* matches the specification, one normative
// requirement per subtest, each named for the section it comes from. The distinction matters
// because of how the failure they exist to prevent presents itself: a client validates a result
// against a schema and rejects the whole response, so a single missing field does not degrade one
// feature — it makes the server look empty. Claude Code reported a healthy connection and zero of
// the 29 tools because `ttlMs` was absent from `tools/list`.
//
// So the assertions here are deliberately about shape rather than behaviour, and they are stated as
// the specification states them. When one fails, the subtest name is the rule that broke.

// conformanceServer builds one server for the whole suite. Storage setup dominates the runtime of
// every MCP test in this package, so the subtests share an instance; none of them depend on the
// wiki being empty, and the two that write seed their own documents.
func conformanceServer(t *testing.T) *Server {
	t.Helper()
	srv := newMCPServer(t)
	if _, err := srv.Storage.SaveArticle("", "Conformance Fixture", "# body", "", "", "", "seed", nil, ContentTypeWiki); err != nil {
		t.Fatalf("seeding the fixture article failed: %v", err)
	}
	return srv
}

const conformanceSlug = "conformance-fixture"

// modernCall dispatches a modern-era request and returns the decoded envelope and HTTP status.
func modernCall(t *testing.T, srv *Server, method string, extra map[string]interface{}) (map[string]interface{}, int) {
	t.Helper()
	return callModern(t, srv, method, modernParams(t, ModernProtocolVersion, extra), nil)
}

// modernResult unwraps a successful modern result, failing if the call errored.
func modernResult(t *testing.T, srv *Server, method string, extra map[string]interface{}) map[string]interface{} {
	t.Helper()
	envelope, _ := modernCall(t, srv, method, extra)
	if errObj, isErr := envelope["error"]; isErr {
		t.Fatalf("%s returned an error: %#v", method, errObj)
	}
	result, ok := envelope["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("%s carried no result object: %#v", method, envelope)
	}
	return result
}

// modernMethodCases is every method the modern dispatcher serves, with params good enough to
// succeed. Kept in one place so a new method cannot be added without the envelope rules applying
// to it.
func modernMethodCases() []struct {
	method    string
	extra     map[string]interface{}
	cacheable bool
} {
	return []struct {
		method    string
		extra     map[string]interface{}
		cacheable bool
	}{
		{"server/discover", nil, true},
		{"tools/list", nil, true},
		{"prompts/list", nil, true},
		{"resources/list", nil, true},
		{"resources/templates/list", nil, true},
		{"resources/read", map[string]interface{}{"uri": articleResourceURI(conformanceSlug)}, true},
		{"tools/call", map[string]interface{}{"name": "list_articles", "arguments": map[string]interface{}{}}, false},
		{"prompts/get", map[string]interface{}{
			"name":      "article_creation_workflow",
			"arguments": map[string]interface{}{"title": "Anything"},
		}, false},
		{"completion/complete", map[string]interface{}{
			"ref":      map[string]interface{}{"type": "ref/resource", "uri": resourceURIPrefix + "{slug}"},
			"argument": map[string]interface{}{"name": "slug", "value": ""},
		}, false},
	}
}

// TestConformanceResultEnvelope covers §Base Protocol → Responses and §Server Utilities → Caching.
func TestConformanceResultEnvelope(t *testing.T) {
	srv := conformanceServer(t)

	for _, tc := range modernMethodCases() {
		t.Run(tc.method, func(t *testing.T) {
			result := modernResult(t, srv, tc.method, tc.extra)

			// "The result MUST include a resultType field to indicate the type of the result."
			if result["resultType"] != "complete" {
				t.Errorf("resultType = %v, want \"complete\"", result["resultType"])
			}

			// "Servers SHOULD include io.modelcontextprotocol/serverInfo in every result's _meta."
			meta, ok := result["_meta"].(map[string]interface{})
			if !ok {
				t.Fatalf("result carried no _meta: %#v", result)
			}
			if _, present := meta[metaServerInfo]; !present {
				t.Errorf("_meta is missing %s, so the result identifies no server", metaServerInfo)
			}

			ttl, hasTTL := result["ttlMs"]
			scope, hasScope := result["cacheScope"]

			if !tc.cacheable {
				// The specification lists six cacheable operations; anything else carries no hints.
				if hasTTL || hasScope {
					t.Errorf("%s is not a cacheable operation but carries ttlMs=%v cacheScope=%v",
						tc.method, ttl, scope)
				}
				return
			}

			// "Servers MUST include caching hints on results with resultType: complete returned by
			// [the six cacheable operations]." This is the assertion that would have caught the
			// defect that made Claude Code list zero tools.
			if !hasTTL {
				t.Errorf("%s is cacheable but carries no ttlMs; a conformant client rejects the whole result", tc.method)
			}
			if !hasScope {
				t.Errorf("%s is cacheable but carries no cacheScope", tc.method)
			}
			// "Servers MUST provide a ttlMs value that is >= 0."
			if seconds, isNumber := ttl.(float64); !isNumber || seconds < 0 {
				t.Errorf("ttlMs = %v, want a number >= 0", ttl)
			}
			if scope != cacheScopePublic && scope != cacheScopePrivate {
				t.Errorf("cacheScope = %v, want %q or %q", scope, cacheScopePublic, cacheScopePrivate)
			}
		})
	}
}

// TestConformanceToolsListShape is a golden key set, and exists for one reason: the field that
// broke Claude Code was missing, and nothing asserted the *whole* shape of the payload. An
// assertion that only checks the fields it remembers to name cannot catch a field being dropped.
func TestConformanceToolsListShape(t *testing.T) {
	srv := conformanceServer(t)
	result := modernResult(t, srv, "tools/list", nil)

	want := map[string]bool{"tools": true, "resultType": true, "ttlMs": true, "cacheScope": true, "_meta": true}
	for key := range result {
		if !want[key] {
			t.Errorf("tools/list result carries unexpected key %q", key)
		}
		delete(want, key)
	}
	for key := range want {
		t.Errorf("tools/list result is missing required key %q", key)
	}

	tools, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatalf("tools is %T, want an array", result["tools"])
	}
	// Against the registry rather than a literal: TestRegistryCoversEveryTool already pins the
	// registry at 29, so what matters here is that tools/list exposes all of it and drops none.
	if len(tools) != len(mcpToolRegistry) {
		t.Errorf("tools/list returned %d tools, want all %d in the registry", len(tools), len(mcpToolRegistry))
	}
}

// TestConformanceToolListIsDeterministic covers §Server Features → Tools: "Servers SHOULD return
// tools in a deterministic order." A list that reshuffles defeats client caching and, because tool
// definitions go into the model's context, the prompt cache behind it.
func TestConformanceToolListIsDeterministic(t *testing.T) {
	srv := conformanceServer(t)

	first, err := json.Marshal(modernResult(t, srv, "tools/list", nil)["tools"])
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	second, err := json.Marshal(modernResult(t, srv, "tools/list", nil)["tools"])
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("two tools/list calls returned different bytes; the ordering is not deterministic")
	}
}

// TestConformanceDiscover covers §Server Features → Discovery. server/discover is the one RPC the
// specification says servers MUST implement.
func TestConformanceDiscover(t *testing.T) {
	srv := conformanceServer(t)
	result := modernResult(t, srv, "server/discover", nil)

	versions, ok := result["supportedVersions"].([]interface{})
	if !ok || len(versions) == 0 {
		t.Fatalf("supportedVersions is %#v, want a non-empty array", result["supportedVersions"])
	}
	if versions[0] != ModernProtocolVersion {
		t.Errorf("supportedVersions[0] = %v, want %q", versions[0], ModernProtocolVersion)
	}

	capabilities, ok := result["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("capabilities is %#v, want an object", result["capabilities"])
	}
	for _, feature := range []string{"tools", "prompts", "resources", "completions"} {
		if _, declared := capabilities[feature]; !declared {
			t.Errorf("capabilities omits %q, which this server serves", feature)
		}
	}
	if result["instructions"] == "" || result["instructions"] == nil {
		t.Error("discover carried no instructions; clients surface them as the connect-time nudge")
	}
}

// TestConformanceCapabilitiesAreTruthfulPerEra covers §Server Features → Resources → Capabilities.
//
// `subscribe` names a different method in each era: subscriptions/listen in 2026-07-28, the
// resources/subscribe RPC in the initialize-based revisions. NexWiki implements the first and not
// the second, so advertising one capability set to both eras pointed legacy clients at a method
// that answers -32601.
func TestConformanceCapabilitiesAreTruthfulPerEra(t *testing.T) {
	modernResources, ok := modernServerCapabilities()["resources"].(map[string]interface{})
	if !ok {
		t.Fatalf("modern resources capability is %#v", modernServerCapabilities()["resources"])
	}
	if modernResources["subscribe"] != true {
		t.Error("the modern era honors resourceSubscriptions on subscriptions/listen and must advertise subscribe")
	}
	if modernResources["listChanged"] != true {
		t.Error("the modern era delivers resources/list_changed and must advertise listChanged")
	}

	legacyResources, ok := legacyServerCapabilities()["resources"].(map[string]interface{})
	if !ok {
		t.Fatalf("legacy resources capability is %#v", legacyServerCapabilities()["resources"])
	}
	if _, advertised := legacyResources["subscribe"]; advertised {
		t.Error("the legacy era has no resources/subscribe handler, so it must not advertise subscribe")
	}
	if legacyResources["listChanged"] != true {
		t.Error("the legacy GET stream delivers resources/list_changed, so listChanged is honest and should be advertised")
	}
}

// TestConformanceVersionNegotiation covers §Versioning → Protocol Version Negotiation.
func TestConformanceVersionNegotiation(t *testing.T) {
	srv := conformanceServer(t)

	envelope, status := callModern(t, srv, "tools/list", modernParams(t, "1900-01-01", nil), nil)
	if got := errorCode(t, envelope); got != errCodeUnsupportedProtocolVersion {
		t.Errorf("error code = %d, want %d (UnsupportedProtocolVersion)", got, errCodeUnsupportedProtocolVersion)
	}
	if status != http.StatusBadRequest {
		t.Errorf("HTTP status = %d, want 400", status)
	}

	errObj, ok := envelope["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("envelope carried no error: %#v", envelope)
	}
	data, ok := errObj["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("UnsupportedProtocolVersionError carried no data: %#v", errObj)
	}
	// "...listing its supported versions", so the client can retry with one instead of giving up.
	if supported, ok := data["supported"].([]interface{}); !ok || len(supported) == 0 {
		t.Errorf("data.supported is %#v, want the versions this server implements", data["supported"])
	}
	if data["requested"] != "1900-01-01" {
		t.Errorf("data.requested = %v, want the version the client asked for", data["requested"])
	}
}

// TestConformanceRequiredMetaFields covers §Base Protocol → _meta: "A request missing any required
// field is malformed; the server MUST reject it with -32602. On HTTP, the response status MUST be
// 400 Bad Request."
func TestConformanceRequiredMetaFields(t *testing.T) {
	srv := conformanceServer(t)

	t.Run("missing clientCapabilities", func(t *testing.T) {
		params := json.RawMessage(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"` + ModernProtocolVersion + `"}}`)
		envelope, status := callModern(t, srv, "tools/list", params, nil)
		if got := errorCode(t, envelope); got != errCodeInvalidParams {
			t.Errorf("error code = %d, want %d", got, errCodeInvalidParams)
		}
		if status != http.StatusBadRequest {
			t.Errorf("HTTP status = %d, want 400", status)
		}
	})

	// The era is decided by the header here. Before, such a request fell through to the legacy
	// switch and was served under legacy semantics — a modern client with a mis-built body got a
	// plausible-looking answer and no signal that anything was wrong.
	t.Run("header says modern but body carries no _meta", func(t *testing.T) {
		headers := http.Header{"Mcp-Protocol-Version": []string{ModernProtocolVersion}, "Mcp-Method": []string{"tools/list"}}
		envelope, status := callModern(t, srv, "tools/list", nil, headers)
		if got := errorCode(t, envelope); got != errCodeInvalidParams {
			t.Errorf("error code = %d, want %d", got, errCodeInvalidParams)
		}
		if status != http.StatusBadRequest {
			t.Errorf("HTTP status = %d, want 400", status)
		}
		if errObj, ok := envelope["error"].(map[string]interface{}); ok {
			if message, _ := errObj["message"].(string); !strings.Contains(message, metaProtocolVersion) {
				t.Errorf("message %q does not name the missing field", message)
			}
		}
	})
}

// TestConformanceHeaderMirroring covers §Transports → Streamable HTTP → Server Validation. The body
// is the source of truth; the headers exist so intermediaries can route without parsing it, and
// letting them disagree is how a proxy ends up routing on one value while the server executes
// another.
func TestConformanceHeaderMirroring(t *testing.T) {
	srv := conformanceServer(t)
	callParams := map[string]interface{}{"name": "list_articles", "arguments": map[string]interface{}{}}

	for _, tc := range []struct {
		name    string
		headers http.Header
		want    int
	}{
		{"missing MCP-Protocol-Version", http.Header{"Mcp-Method": []string{"tools/call"}, "Mcp-Name": []string{"list_articles"}}, errCodeHeaderMismatch},
		{"protocol version disagrees with body", http.Header{
			"Mcp-Protocol-Version": []string{"2025-06-18"},
			"Mcp-Method":           []string{"tools/call"},
			"Mcp-Name":             []string{"list_articles"},
		}, errCodeHeaderMismatch},
		{"method disagrees with body", http.Header{
			"Mcp-Protocol-Version": []string{ModernProtocolVersion},
			"Mcp-Method":           []string{"tools/list"},
			"Mcp-Name":             []string{"list_articles"},
		}, errCodeHeaderMismatch},
		{"name disagrees with body", http.Header{
			"Mcp-Protocol-Version": []string{ModernProtocolVersion},
			"Mcp-Method":           []string{"tools/call"},
			"Mcp-Name":             []string{"some_other_tool"},
		}, errCodeHeaderMismatch},
		{"name header absent", http.Header{
			"Mcp-Protocol-Version": []string{ModernProtocolVersion},
			"Mcp-Method":           []string{"tools/call"},
		}, errCodeHeaderMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope, status := callModern(t, srv, "tools/call", modernParams(t, ModernProtocolVersion, callParams), tc.headers)
			if got := errorCode(t, envelope); got != tc.want {
				t.Errorf("error code = %d, want %d", got, tc.want)
			}
			if status != http.StatusBadRequest {
				t.Errorf("HTTP status = %d, want 400", status)
			}
		})
	}

	// A body with no name to mirror is a malformed request, not a header failure. Reporting -32020
	// sent the client looking for a header problem it had no value to fix.
	t.Run("empty name in body is invalid params, not a header mismatch", func(t *testing.T) {
		headers := http.Header{
			"Mcp-Protocol-Version": []string{ModernProtocolVersion},
			"Mcp-Method":           []string{"tools/call"},
		}
		params := modernParams(t, ModernProtocolVersion, map[string]interface{}{"arguments": map[string]interface{}{}})
		envelope, status := callModern(t, srv, "tools/call", params, headers)
		if got := errorCode(t, envelope); got != errCodeInvalidParams {
			t.Errorf("error code = %d, want %d (the body is what is malformed)", got, errCodeInvalidParams)
		}
		if status != http.StatusBadRequest {
			t.Errorf("HTTP status = %d, want 400", status)
		}
	})

	// §Transports → Value Encoding: a name that cannot travel as plain ASCII rides in the Base64
	// sentinel, and "servers MUST decode an encoded Mcp-Name value before comparing it".
	t.Run("base64 sentinel round-trips", func(t *testing.T) {
		uri := articleResourceURI(conformanceSlug)
		headers := http.Header{
			"Mcp-Protocol-Version": []string{ModernProtocolVersion},
			"Mcp-Method":           []string{"resources/read"},
			"Mcp-Name":             []string{"=?base64?" + base64Encode(uri) + "?="},
		}
		params := modernParams(t, ModernProtocolVersion, map[string]interface{}{"uri": uri})
		envelope, status := callModern(t, srv, "resources/read", params, headers)
		if _, isErr := envelope["error"]; isErr {
			t.Errorf("an encoded Mcp-Name must be decoded before comparison, got: %#v", envelope["error"])
		}
		if status != http.StatusOK {
			t.Errorf("HTTP status = %d, want 200", status)
		}
	})
}

// TestConformanceRemovedMethods covers §Changelog → Major changes: ping, logging/setLevel,
// resources/subscribe and resources/unsubscribe were removed in 2026-07-28, and the modern
// dispatcher must say so. ping still answers in the legacy era, where it is a defined utility.
func TestConformanceRemovedMethods(t *testing.T) {
	srv := conformanceServer(t)

	for _, method := range []string{"ping", "logging/setLevel", "resources/subscribe", "resources/unsubscribe", "not/a/method"} {
		t.Run("modern "+method, func(t *testing.T) {
			envelope, status := modernCall(t, srv, method, nil)
			if got := errorCode(t, envelope); got != errCodeMethodNotFound {
				t.Errorf("error code = %d, want %d (MethodNotFound)", got, errCodeMethodNotFound)
			}
			// "If the server does not implement the requested RPC method, it MUST respond with
			// 404 Not Found", which is what distinguishes it from a legacy server's 404.
			if status != http.StatusNotFound {
				t.Errorf("HTTP status = %d, want 404", status)
			}
		})
	}

	t.Run("legacy ping answers", func(t *testing.T) {
		envelope, status := callModern(t, srv, "ping", nil, nil)
		if _, isErr := envelope["error"]; isErr {
			t.Errorf("ping is a defined utility in the initialize-based revisions: %#v", envelope["error"])
		}
		if status != http.StatusOK {
			t.Errorf("HTTP status = %d, want 200", status)
		}
	})
}

// TestConformanceLegacyEnvelopeUnchanged is the dual-era guarantee from the other direction: none of
// the modern envelope fields may leak into a legacy result, where they are not defined.
func TestConformanceLegacyEnvelopeUnchanged(t *testing.T) {
	srv := conformanceServer(t)

	for _, method := range []string{"initialize", "tools/list", "prompts/list", "resources/list"} {
		t.Run(method, func(t *testing.T) {
			envelope, _ := callModern(t, srv, method, nil, nil)
			result, ok := envelope["result"].(map[string]interface{})
			if !ok {
				t.Fatalf("%s carried no result: %#v", method, envelope)
			}
			for _, field := range []string{"resultType", "ttlMs", "cacheScope"} {
				if value, present := result[field]; present {
					t.Errorf("legacy %s result carries modern-era field %s=%v", method, field, value)
				}
			}
		})
	}
}

// TestConformanceResourceErrors covers §Server Features → Resources → Error Handling: a missing
// resource MUST be -32602, and "Servers MUST NOT return an empty contents array for a non-existent
// resource" because that cannot be told apart from a resource that exists and is empty.
func TestConformanceResourceErrors(t *testing.T) {
	srv := conformanceServer(t)

	envelope, _ := modernCall(t, srv, "resources/read", map[string]interface{}{
		"uri": articleResourceURI("no-such-article"),
	})
	if got := errorCode(t, envelope); got != errCodeInvalidParams {
		t.Errorf("error code = %d, want %d (the -32002 of earlier revisions is retired)", got, errCodeInvalidParams)
	}
	if _, hasResult := envelope["result"]; hasResult {
		t.Error("a missing resource must be a JSON-RPC error, never a result with empty contents")
	}
	errObj, ok := envelope["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("envelope carried no error: %#v", envelope)
	}
	data, ok := errObj["data"].(map[string]interface{})
	if !ok || data["uri"] == nil {
		t.Errorf("the error should echo the uri in data so the client knows which one failed: %#v", errObj)
	}
}

// TestConformancePromptErrors covers §Server Features → Prompts → Error Handling: "Invalid prompt
// name: -32602" and "Missing required arguments: -32602".
//
// -32601 was not merely the wrong number. The modern era is required to surface MethodNotFound as
// HTTP 404, so a typo'd prompt name made the MCP endpoint itself look missing.
func TestConformancePromptErrors(t *testing.T) {
	srv := conformanceServer(t)

	for _, tc := range []struct {
		name  string
		extra map[string]interface{}
	}{
		{"unknown prompt name", map[string]interface{}{"name": "no_such_prompt"}},
		{"missing required argument", map[string]interface{}{
			"name": "project_planning_workflow", "arguments": map[string]interface{}{"title": "Some Plan"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope, status := modernCall(t, srv, "prompts/get", tc.extra)
			if got := errorCode(t, envelope); got != errCodeInvalidParams {
				t.Errorf("error code = %d, want %d", got, errCodeInvalidParams)
			}
			if status != http.StatusBadRequest {
				t.Errorf("HTTP status = %d, want 400 (404 would say the endpoint is missing)", status)
			}
		})
	}
}

// TestConformancePagination covers §Server Utilities → Pagination. The cursor is opaque and the
// server's own token, so anything it cannot read means the client invented or corrupted it:
// "Invalid cursors SHOULD result in an error with code -32602."
func TestConformancePagination(t *testing.T) {
	t.Run("cursor round-trips", func(t *testing.T) {
		for _, offset := range []int{0, 1, 100, 999999} {
			decoded, rpcErr := decodeCursor(encodeCursor(offset))
			if rpcErr != nil {
				t.Fatalf("decodeCursor(encodeCursor(%d)) failed: %v", offset, rpcErr)
			}
			if decoded != offset {
				t.Errorf("cursor for offset %d decoded to %d", offset, decoded)
			}
		}
	})

	t.Run("a full walk yields every item exactly once", func(t *testing.T) {
		items := make([]int, 0, 250)
		for i := range 250 {
			items = append(items, i)
		}

		seen := map[int]int{}
		cursor := ""
		for pages := 0; ; pages++ {
			if pages > 10 {
				t.Fatal("pagination did not terminate")
			}
			page, next, rpcErr := paginate(items, cursor, 100)
			if rpcErr != nil {
				t.Fatalf("paginate failed: %v", rpcErr)
			}
			for _, item := range page {
				seen[item]++
			}
			if next == "" {
				break
			}
			cursor = next
		}

		if len(seen) != len(items) {
			t.Errorf("walk saw %d distinct items, want %d", len(seen), len(items))
		}
		for item, count := range seen {
			if count != 1 {
				t.Errorf("item %d appeared %d times; a walk must yield each exactly once", item, count)
			}
		}
	})

	t.Run("an exhausted list offers no cursor", func(t *testing.T) {
		// "Clients MUST treat a missing nextCursor as the end of results" — and an *empty* cursor is
		// a valid cursor, so emitting one instead of omitting the field would loop the client.
		page, next, rpcErr := paginate([]int{1, 2, 3}, "", 100)
		if rpcErr != nil {
			t.Fatalf("paginate failed: %v", rpcErr)
		}
		if len(page) != 3 || next != "" {
			t.Errorf("page=%v next=%q, want the whole list and no cursor", page, next)
		}
		if result := listResult("things", page, next); result["nextCursor"] != nil {
			t.Error("nextCursor must be omitted at the end of a list, not emitted empty")
		}
	})

	t.Run("invalid cursors are rejected on every list method", func(t *testing.T) {
		srv := conformanceServer(t)
		for _, method := range []string{"tools/list", "prompts/list", "resources/list", "resources/templates/list"} {
			envelope, status := modernCall(t, srv, method, map[string]interface{}{"cursor": "not-a-real-cursor"})
			if got := errorCode(t, envelope); got != errCodeInvalidParams {
				t.Errorf("%s with a bad cursor gave error code %d, want %d", method, got, errCodeInvalidParams)
			}
			if status != http.StatusBadRequest {
				t.Errorf("%s with a bad cursor gave HTTP %d, want 400", method, status)
			}
		}
	})
}

// TestConformanceCompletion covers §Server Utilities → Completion.
func TestConformanceCompletion(t *testing.T) {
	srv := conformanceServer(t)

	t.Run("slug completion for the article template", func(t *testing.T) {
		result := modernResult(t, srv, "completion/complete", map[string]interface{}{
			"ref":      map[string]interface{}{"type": "ref/resource", "uri": resourceURIPrefix + "{slug}"},
			"argument": map[string]interface{}{"name": "slug", "value": "conform"},
		})
		completion, ok := result["completion"].(map[string]interface{})
		if !ok {
			t.Fatalf("result carried no completion object: %#v", result)
		}
		values, ok := completion["values"].([]interface{})
		if !ok {
			t.Fatalf("completion.values is %T, want an array", completion["values"])
		}
		// "Maximum 100 items per response."
		if len(values) > maxCompletionValues {
			t.Errorf("completion returned %d values, want at most %d", len(values), maxCompletionValues)
		}
		if len(values) == 0 || values[0] != conformanceSlug {
			t.Errorf("completing %q did not surface %q: %#v", "conform", conformanceSlug, values)
		}
		if completion["hasMore"] != false {
			t.Errorf("hasMore = %v, want false when every match fits in the response", completion["hasMore"])
		}
	})

	t.Run("an unknown prompt reference is invalid params", func(t *testing.T) {
		envelope, _ := modernCall(t, srv, "completion/complete", map[string]interface{}{
			"ref":      map[string]interface{}{"type": "ref/prompt", "name": "no_such_prompt"},
			"argument": map[string]interface{}{"name": "title", "value": ""},
		})
		if got := errorCode(t, envelope); got != errCodeInvalidParams {
			t.Errorf("error code = %d, want %d", got, errCodeInvalidParams)
		}
	})

	t.Run("an argument with nothing to suggest is not an error", func(t *testing.T) {
		result := modernResult(t, srv, "completion/complete", map[string]interface{}{
			"ref":      map[string]interface{}{"type": "ref/prompt", "name": "article_creation_workflow"},
			"argument": map[string]interface{}{"name": "description", "value": "any"},
		})
		completion, ok := result["completion"].(map[string]interface{})
		if !ok {
			t.Fatalf("result carried no completion object: %#v", result)
		}
		// An empty array, not null: a client typing into a free-text field must not see a failure.
		values, ok := completion["values"].([]interface{})
		if !ok {
			t.Fatalf("completion.values is %T, want an empty array", completion["values"])
		}
		if len(values) != 0 {
			t.Errorf("values = %#v, want empty", values)
		}
	})
}

// TestConformanceToolSchemas covers §Base Protocol → JSON Schema Usage and §Server Features → Tools.
func TestConformanceToolSchemas(t *testing.T) {
	headerNames := map[string]string{}

	for _, tool := range toolSchemas() {
		name, _ := tool["name"].(string)
		if name == "" {
			t.Fatalf("a tool entry has no name: %#v", tool)
		}

		t.Run(name, func(t *testing.T) {
			// "inputSchema MUST be a valid JSON Schema object (not null)."
			schema, ok := tool["inputSchema"].(map[string]interface{})
			if !ok {
				t.Fatalf("inputSchema is %T, want an object", tool["inputSchema"])
			}
			if schema["type"] != "object" {
				t.Errorf("inputSchema type = %v, want \"object\"", schema["type"])
			}

			// §Tool Names: the allowed set is letters, digits, underscore, hyphen and dot.
			if len(name) > 128 {
				t.Errorf("tool name is %d characters, want at most 128", len(name))
			}
			for _, r := range name {
				isAllowed := r == '_' || r == '-' || r == '.' ||
					(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
				if !isAllowed {
					t.Errorf("tool name contains %q, which is outside the allowed character set", r)
				}
			}

			// §Base Protocol → $ref Resolution: implementations MUST NOT dereference a $ref that
			// resolves to a network URI, so no schema we publish may contain one.
			if refs := networkRefs(schema); len(refs) > 0 {
				t.Errorf("inputSchema contains network $ref(s) %v; clients must not dereference those", refs)
			}
			if output, declared := tool["outputSchema"].(map[string]interface{}); declared {
				if refs := networkRefs(output); len(refs) > 0 {
					t.Errorf("outputSchema contains network $ref(s) %v", refs)
				}
			}

			// §Transports → Schema Extension: x-mcp-header values must be non-empty and
			// case-insensitively unique across the whole inputSchema. NexWiki declares none today;
			// this locks in that any future one is well-formed rather than silently rejected by
			// every conforming client, which would drop the whole tool from tools/list.
			for property, header := range mcpHeaderAnnotations(schema) {
				if header == "" {
					t.Errorf("property %q has an empty x-mcp-header", property)
				}
				lower := strings.ToLower(header)
				if previous, clash := headerNames[lower]; clash {
					t.Errorf("x-mcp-header %q on %q collides with %q", header, property, previous)
				}
				headerNames[lower] = name + "." + property
			}
		})
	}
}

// networkRefs collects every $ref in a schema that points at a network URI.
func networkRefs(schema map[string]interface{}) []string {
	var found []string
	var walk func(node interface{})
	walk = func(node interface{}) {
		switch typed := node.(type) {
		case map[string]interface{}:
			for key, value := range typed {
				if key == "$ref" {
					if ref, isString := value.(string); isString &&
						(strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://")) {
						found = append(found, ref)
					}
				}
				walk(value)
			}
		case []interface{}:
			for _, value := range typed {
				walk(value)
			}
		case []map[string]interface{}:
			for _, value := range typed {
				walk(value)
			}
		}
	}
	walk(schema)
	return found
}

// mcpHeaderAnnotations collects the x-mcp-header annotations declared directly on a schema's
// properties. Only statically reachable properties may carry one, so this deliberately does not
// descend through items, composition keywords, or $ref.
func mcpHeaderAnnotations(schema map[string]interface{}) map[string]string {
	annotations := map[string]string{}
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return annotations
	}
	for name, raw := range properties {
		property, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if header, declared := property["x-mcp-header"].(string); declared {
			annotations[name] = header
		}
	}
	return annotations
}

// postMCP issues a real HTTP request against the MCP endpoint, for the assertions that are about
// the transport rather than the JSON-RPC body.
//
// It goes through the **same middleware chain main.go builds**, not straight into the handler. That
// distinction is load-bearing: EnableCORS answers every preflight itself and returns before the mux
// runs, so a test that calls HandleStreamableHTTP directly never exercises the code path a browser
// actually reaches. Calling the handler directly is precisely how the preflight defect this suite
// is meant to catch stayed green while the wired server still refused modern browser clients.
func postMCP(t *testing.T, srv *Server, method string, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc(MCPEndpointPath, srv.HandleStreamableHTTP)
	handler := EnableCORS(LimitRequestBodies(mux))

	req := httptest.NewRequest(method, MCPEndpointPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// TestConformanceTransport covers §Transports → Streamable HTTP: the parts of the contract that are
// HTTP-level rather than JSON-RPC-level. None of these had coverage before.
func TestConformanceTransport(t *testing.T) {
	srv := conformanceServer(t)

	// "If the body is a JSON-RPC notification: if the server accepts it, the server MUST return
	// HTTP status code 202 Accepted with no body."
	t.Run("a notification is 202 with no body", func(t *testing.T) {
		w := postMCP(t, srv, http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil)
		if w.Code != http.StatusAccepted {
			t.Errorf("status = %d, want 202", w.Code)
		}
		if w.Body.Len() != 0 {
			t.Errorf("body = %q, want empty", w.Body.String())
		}
	})

	// "HTTP GET or DELETE to the MCP endpoint: respond with 405 Method Not Allowed." GET is kept
	// for the initialize-based revisions, which define a standalone stream there; DELETE terminated
	// a session, and sessions no longer exist in any era this server serves.
	t.Run("DELETE is not allowed", func(t *testing.T) {
		w := postMCP(t, srv, http.MethodDelete, "", nil)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", w.Code)
		}
	})

	// "Servers MUST validate the Origin header on all incoming connections to prevent DNS rebinding
	// attacks. If the Origin header is present and invalid, servers MUST respond with 403."
	t.Run("a foreign Origin is refused", func(t *testing.T) {
		w := postMCP(t, srv, http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
			map[string]string{"Origin": "https://attacker.example"})
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
		if echoed := w.Header().Get("Access-Control-Allow-Origin"); echoed != "" {
			t.Errorf("a refused origin must not be echoed, got %q", echoed)
		}
	})

	// A browser will not send a header the preflight did not allow, so omitting Mcp-Method and
	// Mcp-Name here rejected modern browser-hosted clients before a single message was exchanged.
	t.Run("preflight allows the headers a modern request must send", func(t *testing.T) {
		w := postMCP(t, srv, http.MethodOptions, "", map[string]string{"Origin": "http://127.0.0.1:5808"})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		allowed := strings.ToLower(w.Header().Get("Access-Control-Allow-Headers"))
		for _, required := range []string{"mcp-protocol-version", "mcp-method", "mcp-name", "content-type", "accept"} {
			if !strings.Contains(allowed, required) {
				t.Errorf("Access-Control-Allow-Headers %q omits %q", allowed, required)
			}
		}
	})

	// A modern request over the real transport, headers and all, must still come back complete.
	t.Run("a fully-formed modern POST succeeds", func(t *testing.T) {
		body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{%q:%q,%q:{}}}}`,
			metaProtocolVersion, ModernProtocolVersion, metaClientCapabilities)
		w := postMCP(t, srv, http.MethodPost, body, map[string]string{
			"MCP-Protocol-Version": ModernProtocolVersion,
			"Mcp-Method":           "tools/list",
			"Accept":               "application/json, text/event-stream",
		})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
		}

		var envelope map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("response is not JSON: %v", err)
		}
		result, ok := envelope["result"].(map[string]interface{})
		if !ok {
			t.Fatalf("no result in %s", w.Body.String())
		}
		for _, field := range []string{"resultType", "ttlMs", "cacheScope"} {
			if _, present := result[field]; !present {
				t.Errorf("tools/list over HTTP is missing %s", field)
			}
		}
	})
}

// TestConformanceStdioSubscription covers §Message Patterns → Subscriptions on the stdio transport,
// where every subscription shares one channel and correlation is by subscriptionId.
func TestConformanceStdioSubscription(t *testing.T) {
	srv := conformanceServer(t)

	var out bytes.Buffer
	req := JSONRPCRequest{JSONRPC: "2.0", ID: float64(42), Method: "subscriptions/listen",
		Params: json.RawMessage(`{"notifications":{"resourcesListChanged":true}}`)}
	srv.handleRequest(&out, &req)

	messages := decodeJSONLines(t, out.Bytes())
	if len(messages) == 0 {
		t.Fatal("subscriptions/listen produced no output at all")
	}

	// "The server MUST send notifications/subscriptions/acknowledged as the first message... and
	// MUST NOT send any notification on the subscription before it."
	ack := messages[0]
	if ack["method"] != "notifications/subscriptions/acknowledged" {
		t.Errorf("first message is %v, want the acknowledgment", ack["method"])
	}
	params, ok := ack["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("acknowledgment carried no params: %#v", ack)
	}
	meta, ok := params["_meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("acknowledgment carried no _meta: %#v", params)
	}
	// "...carrying the subscription's ID in _meta under io.modelcontextprotocol/subscriptionId."
	if meta[metaSubscriptionID] != float64(42) {
		t.Errorf("subscriptionId = %v, want the request id 42", meta[metaSubscriptionID])
	}

	// The long-lived request must be answered. The acknowledgment is a notification and carries no
	// id, so without this the client waits on a response that never comes.
	closure := messages[len(messages)-1]
	if closure["id"] != float64(42) {
		t.Fatalf("no response correlated to the subscriptions/listen request: %#v", messages)
	}
	result, ok := closure["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("closure carried no result: %#v", closure)
	}
	if result["resultType"] != "complete" {
		t.Errorf("closure resultType = %v, want \"complete\"", result["resultType"])
	}
	closureMeta, ok := result["_meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("closure result carried no _meta: %#v", result)
	}
	if closureMeta[metaSubscriptionID] != float64(42) {
		t.Errorf("closure subscriptionId = %v, want 42", closureMeta[metaSubscriptionID])
	}
	if _, present := closureMeta[metaServerInfo]; !present {
		t.Errorf("closure result is missing %s; every modern result identifies its server", metaServerInfo)
	}
}

// TestConformanceSubscriptionFilterIsHonored covers §Message Patterns → Subscriptions: "The server
// MUST NOT send notification types the client has not explicitly requested."
func TestConformanceSubscriptionFilterIsHonored(t *testing.T) {
	requested := subscriptionFilter{
		ToolsListChanged:      true,
		PromptsListChanged:    true,
		ResourcesListChanged:  false,
		ResourceSubscriptions: []string{articleResourceURI(conformanceSlug)},
	}
	honored := requested.honored()

	// The acknowledgment reports the subset the server agreed to, so a client can stop waiting for
	// what will never arrive. The tool and prompt sets are compiled in and cannot change.
	acknowledged := honored.acknowledgedFilter()
	for _, promised := range []string{"toolsListChanged", "promptsListChanged"} {
		if _, present := acknowledged[promised]; present {
			t.Errorf("acknowledged %s, but the registry is compiled in and can never change", promised)
		}
	}

	// A resource the client did not name must produce nothing, even though the event itself fires.
	update := WikiUpdate{Type: "article-edited", Slug: "some-other-article"}
	if notifications := wikiUpdateNotifications(update, honored, 1); len(notifications) != 0 {
		t.Errorf("an unsubscribed resource produced %d notification(s): %#v", len(notifications), notifications)
	}

	// The one it did name must produce exactly one, tagged with the subscription id.
	update = WikiUpdate{Type: "article-edited", Slug: conformanceSlug}
	notifications := wikiUpdateNotifications(update, honored, 1)
	if len(notifications) != 1 {
		t.Fatalf("a subscribed resource produced %d notification(s), want 1", len(notifications))
	}
	if notifications[0]["method"] != "notifications/resources/updated" {
		t.Errorf("method = %v, want notifications/resources/updated", notifications[0]["method"])
	}
}

// lockedBuffer is a bytes.Buffer safe to read while a subscription goroutine writes to it. The
// syncLineWriter in front of it serializes writers against each other, not against a reader.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) snapshot() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

// awaitStdioMessage polls the transcript until a message satisfying match appears, or fails.
// Polling rather than a channel because the production path writes bytes, not events — reading what
// a client would actually read is the point.
func awaitStdioMessage(t *testing.T, out *lockedBuffer, what string, match func(map[string]interface{}) bool) map[string]interface{} {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, message := range decodeJSONLines(t, out.snapshot()) {
			if match(message) {
				return message
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; transcript was:\n%s", what, out.snapshot())
	return nil
}

// TestConformanceStdioSubscriptionStreams is the end-to-end stdio subscription: a client subscribes
// to a resource, the wiki changes, and the notification arrives on the same channel the responses
// use — then notifications/cancelled ends it and the long-lived request is answered.
//
// stdio used to get a stub: acknowledged, then nothing, forever. The specification defines
// subscriptionId precisely because stdio multiplexes every subscription onto one channel, so the
// shape is supported by design; what it needed was a serialized writer and a goroutine per stream.
func TestConformanceStdioSubscriptionStreams(t *testing.T) {
	srv := conformanceServer(t)

	out := &lockedBuffer{}
	srv.stdioOut = newSyncLineWriter(out)

	uri := articleResourceURI(conformanceSlug)
	params := fmt.Sprintf(`{"notifications":{"resourceSubscriptions":[%q]}}`, uri)
	req := JSONRPCRequest{JSONRPC: "2.0", ID: float64(9), Method: "subscriptions/listen",
		Params: json.RawMessage(params), FromStdio: true}
	srv.handleRequest(srv.stdioOut, &req)

	// The acknowledgment MUST come first and no notification may precede it.
	awaitStdioMessage(t, out, "the acknowledgment", func(m map[string]interface{}) bool {
		return m["method"] == "notifications/subscriptions/acknowledged"
	})

	srv.EventBus.PublishWikiUpdate(WikiUpdate{Type: "article-edited", Slug: conformanceSlug})

	updated := awaitStdioMessage(t, out, "notifications/resources/updated", func(m map[string]interface{}) bool {
		return m["method"] == "notifications/resources/updated"
	})
	notificationParams, ok := updated["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("notification carried no params: %#v", updated)
	}
	if notificationParams["uri"] != uri {
		t.Errorf("notification uri = %v, want %q", notificationParams["uri"], uri)
	}
	// "On stdio, where all messages share a single channel, clients MUST use this field to
	// correlate notifications with their originating subscription."
	meta, ok := notificationParams["_meta"].(map[string]interface{})
	if !ok || meta[metaSubscriptionID] != float64(9) {
		t.Errorf("notification subscriptionId = %#v, want 9", notificationParams["_meta"])
	}

	// "The client cancels it — ...send notifications/cancelled referencing the subscriptions/listen
	// request ID (stdio)." There is no per-request stream to close here, so this is the only signal.
	cancel := JSONRPCRequest{JSONRPC: "2.0", Method: "notifications/cancelled",
		Params: json.RawMessage(`{"requestId":9}`), FromStdio: true}
	srv.handleRequest(srv.stdioOut, &cancel)

	closure := awaitStdioMessage(t, out, "the closure response", func(m map[string]interface{}) bool {
		return m["id"] == float64(9)
	})
	result, ok := closure["result"].(map[string]interface{})
	if !ok || result["resultType"] != "complete" {
		t.Errorf("closure result = %#v, want a complete result", closure["result"])
	}
}

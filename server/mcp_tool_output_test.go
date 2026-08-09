package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// seedStructuredFixture builds a wiki with one of every document type, plus a WikiLink and a
// broken WikiLink, so every structured-output tool has something real to answer with. Returning
// an empty payload would let a tool pass these tests while populating nothing.
func seedStructuredFixture(t *testing.T) *Server {
	t.Helper()
	srv := newMCPServer(t)

	mustCall := func(toolJSON string) ToolResponse {
		t.Helper()
		resp := toolCall(t, srv, toolJSON)
		if resp.IsError {
			t.Fatalf("fixture setup failed: %s", resp.Content[0].Text)
		}
		return resp
	}

	mustCall(`{"name":"create_wiki_article","arguments":{"title":"Bleve Notes","content":"# Bleve\n\nLinks to [[Search Design]] and [[Never Written]].","description":"How search is indexed","tags":["search"],"edit_summary":"Initial"}}`)
	mustCall(`{"name":"create_wiki_article","arguments":{"title":"Search Design","content":"# Search Design\n\nThe design.","description":"Design notes","edit_summary":"Initial"}}`)
	mustCall(`{"name":"edit_wiki_article","arguments":{"slug":"search-design","title":"Search Design","content":"# Search Design\n\nRevised.","loaded_version":1,"edit_summary":"Revised the design"}}`)
	mustCall(`{"name":"create_agent_memory","arguments":{"title":"Chose Bleve","content":"# Decision\n\nBleve over Elasticsearch.","memory_type":"nexwiki","description":"Search engine decision","source":"design review"}}`)
	mustCall(`{"name":"create_agent_plan","arguments":{"title":"Ship Structured Output","content":"# Plan\n\nSteps.","project_context":"nexwiki","description":"Structured output rollout"}}`)
	mustCall(`{"name":"create_agent_skill","arguments":{"title":"Wiki Style Guide","content":"# Style\n\nRules.","description":"How to write here"}}`)

	return srv
}

// structuredCalls pairs every tool that declares an outputSchema with a call that exercises it
// against the fixture. A tool missing from this table is caught by
// TestEveryDeclaredOutputSchemaIsCovered, so adding an Output without a test is not possible.
func structuredCalls() map[string]string {
	return map[string]string{
		"search_wiki":         `{"name":"search_wiki","arguments":{"query":"bleve"}}`,
		"read_article":        `{"name":"read_article","arguments":{"slug":"search-design"}}`,
		"list_articles":       `{"name":"list_articles","arguments":{}}`,
		"list_agent_memories": `{"name":"list_agent_memories","arguments":{}}`,
		"list_agent_plans":    `{"name":"list_agent_plans","arguments":{}}`,
		"list_agent_skills":   `{"name":"list_agent_skills","arguments":{}}`,
		"get_backlinks":       `{"name":"get_backlinks","arguments":{"slug":"search-design"}}`,
		"get_article_history": `{"name":"get_article_history","arguments":{"slug":"search-design"}}`,
		"get_wiki_statistics": `{"name":"get_wiki_statistics","arguments":{}}`,
		"get_status_tags":     `{"name":"get_status_tags","arguments":{}}`,
		"get_recent_activity": `{"name":"get_recent_activity","arguments":{}}`,
	}
}

// TestEveryDeclaredOutputSchemaIsCovered is the registry invariant for structured output: a tool
// that publishes an outputSchema is promising every client a machine-readable result, and the
// only way to keep that promise honest is to exercise it. Declaring Output without adding a call
// here fails this test with the tool's name.
func TestEveryDeclaredOutputSchemaIsCovered(t *testing.T) {
	covered := structuredCalls()
	for _, tool := range mcpToolRegistry {
		name, _ := tool.Schema["name"].(string)
		if tool.Output == nil {
			if _, present := covered[name]; present {
				t.Errorf("tool %q has a structured-output test but declares no outputSchema", name)
			}
			continue
		}
		if _, present := covered[name]; !present {
			t.Errorf("tool %q declares an outputSchema but has no case in structuredCalls()", name)
		}
	}
}

// TestOutputSchemasAreWellFormedObjects checks the published schemas before checking what the
// tools return. structuredContent is defined as an object, so an outputSchema of any other type
// could never be satisfied.
func TestOutputSchemasAreWellFormedObjects(t *testing.T) {
	for _, entry := range toolSchemas() {
		name, _ := entry["name"].(string)
		raw, present := entry["outputSchema"]
		if !present {
			continue
		}
		schema, ok := raw.(map[string]interface{})
		if !ok {
			t.Errorf("tool %q: outputSchema is %T, want an object", name, raw)
			continue
		}
		if schema["type"] != "object" {
			t.Errorf("tool %q: outputSchema type is %v, want \"object\" — structuredContent is an object", name, schema["type"])
		}
		if _, ok := schema["properties"].(map[string]interface{}); !ok {
			t.Errorf("tool %q: outputSchema has no properties object", name)
		}
	}
}

// TestStructuredOutputMatchesSchema is the pairing this whole feature rests on: what each tool
// actually returns has to satisfy the schema it published. An agent that trusts outputSchema
// enough to skip parsing the prose has no way to notice a mismatch — it reads a field that is
// never populated and concludes the knowledge is not there.
//
// This validates the parts of JSON Schema the schemas here actually use — required keys, and the
// declared type of each present property, recursively through objects and array items — rather
// than pulling in a validator dependency for a fixed, hand-written set of schemas.
func TestStructuredOutputMatchesSchema(t *testing.T) {
	srv := seedStructuredFixture(t)

	for name, call := range structuredCalls() {
		t.Run(name, func(t *testing.T) {
			tool, ok := toolsByName[name]
			if !ok {
				t.Fatalf("tool %q is not registered", name)
			}
			resp := toolCall(t, srv, call)
			if resp.IsError {
				t.Fatalf("tool returned an error: %s", resp.Content[0].Text)
			}
			if resp.StructuredContent == nil {
				t.Fatal("tool declares an outputSchema but returned no structuredContent")
			}
			// The prose is still required: the spec asks for it, and clients predating
			// structured output have nothing else to read.
			if len(resp.Content) == 0 || strings.TrimSpace(resp.Content[0].Text) == "" {
				t.Error("structured output must not replace the human-readable text content")
			}

			// Validate the serialized form, which is what a client actually receives.
			encoded, err := json.Marshal(resp.StructuredContent)
			if err != nil {
				t.Fatalf("structuredContent does not serialize: %v", err)
			}
			var decoded interface{}
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("structuredContent does not round-trip: %v", err)
			}
			for _, problem := range validateAgainstSchema(decoded, tool.Output, "$") {
				t.Error(problem)
			}
		})
	}
}

// TestStructuredOutputCarriesRealData guards against the failure this feature is most likely to
// have: a payload that validates because every field is absent. These assertions name the values
// an agent would actually reach for.
func TestStructuredOutputCarriesRealData(t *testing.T) {
	srv := seedStructuredFixture(t)

	t.Run("read_article carries the version needed to edit", func(t *testing.T) {
		var out ArticleOutput
		decodeStructured(t, toolCall(t, srv, `{"name":"read_article","arguments":{"slug":"search-design"}}`), &out)
		if out.Article.Slug != "search-design" {
			t.Errorf("slug = %q, want search-design", out.Article.Slug)
		}
		// The point of structuring read_article: loaded_version arrives as a number rather
		// than as an integer embedded in a sentence.
		if out.Article.Version != 2 {
			t.Errorf("version = %d, want 2", out.Article.Version)
		}
		if !strings.Contains(out.Article.Content, "Revised") {
			t.Errorf("content missing from structured output: %q", out.Article.Content)
		}
		if len(out.Backlinks) != 1 || out.Backlinks[0].Slug != "bleve-notes" {
			t.Errorf("backlinks = %+v, want one entry for bleve-notes", out.Backlinks)
		}
	})

	t.Run("search_wiki echoes the query and returns hits", func(t *testing.T) {
		var out SearchOutput
		decodeStructured(t, toolCall(t, srv, `{"name":"search_wiki","arguments":{"query":"bleve"}}`), &out)
		if out.Query != "bleve" {
			t.Errorf("query = %q, want bleve", out.Query)
		}
		if out.Count != len(out.Results) {
			t.Errorf("count %d disagrees with %d results", out.Count, len(out.Results))
		}
		if out.Count == 0 {
			t.Fatal("expected at least one hit for 'bleve'")
		}
		for _, hit := range out.Results {
			for _, snippet := range hit.Snippets {
				// Snippets are HTML on the browser path. An agent must never receive markup
				// it did not ask for; it will paste it back into an article.
				if strings.Contains(snippet, "<mark>") || strings.Contains(snippet, "&lt;") {
					t.Errorf("snippet still carries HTML: %q", snippet)
				}
			}
		}
	})

	t.Run("search_wiki echoes applied facets", func(t *testing.T) {
		var out SearchOutput
		decodeStructured(t, toolCall(t, srv, `{"name":"search_wiki","arguments":{"query":"bleve","type":["memories"]}}`), &out)
		// Echoing the facet is what lets an agent tell "no such knowledge" from "my filter
		// excluded it" without re-reading the prose.
		if len(out.Types) != 1 || out.Types[0] != "memories" {
			t.Errorf("type facet not echoed: %+v", out.Types)
		}
	})

	t.Run("list_articles count matches the documents", func(t *testing.T) {
		var out DocumentListOutput
		decodeStructured(t, toolCall(t, srv, `{"name":"list_articles","arguments":{}}`), &out)
		if out.Count != len(out.Documents) {
			t.Errorf("count %d disagrees with %d documents", out.Count, len(out.Documents))
		}
		if out.Count == 0 {
			t.Fatal("fixture wiki should not list zero documents")
		}
	})

	t.Run("typed listings return only their own type", func(t *testing.T) {
		for _, tc := range []struct{ call, wantType string }{
			{`{"name":"list_agent_memories","arguments":{}}`, ContentTypeMemory},
			{`{"name":"list_agent_plans","arguments":{}}`, ContentTypePlan},
			{`{"name":"list_agent_skills","arguments":{}}`, ContentTypeSkill},
		} {
			var out DocumentListOutput
			decodeStructured(t, toolCall(t, srv, tc.call), &out)
			if out.Count == 0 {
				t.Errorf("%s returned nothing; the fixture seeds one", tc.wantType)
			}
			for _, doc := range out.Documents {
				if doc.Type != tc.wantType {
					t.Errorf("listing leaked a %s document into %s results", doc.Type, tc.wantType)
				}
				// Listings are metadata; bodies belong to read_article. A structured index
				// that inlines every body is a copy of the whole wiki.
				if doc.Content != "" {
					t.Errorf("listing inlined the body of %q", doc.Slug)
				}
			}
		}
	})

	t.Run("get_article_history exposes revertible version numbers", func(t *testing.T) {
		var out HistoryOutput
		decodeStructured(t, toolCall(t, srv, `{"name":"get_article_history","arguments":{"slug":"search-design"}}`), &out)
		if out.Slug != "search-design" {
			t.Errorf("slug = %q, want search-design", out.Slug)
		}
		if out.Count == 0 {
			t.Fatal("expected at least one stored revision after an edit")
		}
		for _, rev := range out.Versions {
			if rev.Version <= 0 {
				t.Errorf("revision has no usable version number: %+v", rev)
			}
		}
	})

	t.Run("get_wiki_statistics names the page a broken link wants", func(t *testing.T) {
		var out StatisticsOutput
		decodeStructured(t, toolCall(t, srv, `{"name":"get_wiki_statistics","arguments":{}}`), &out)
		if out.BrokenLinkCount != len(out.BrokenLinks) {
			t.Errorf("broken_link_count %d disagrees with %d entries", out.BrokenLinkCount, len(out.BrokenLinks))
		}
		var found bool
		for _, bl := range out.BrokenLinks {
			if bl.TargetSlug == "never-written" && bl.FromSlug == "bleve-notes" {
				found = true
			}
		}
		if !found {
			t.Errorf("the seeded broken link is missing: %+v", out.BrokenLinks)
		}
	})

	t.Run("get_status_tags returns the canonical list", func(t *testing.T) {
		var out StatusTagsOutput
		decodeStructured(t, toolCall(t, srv, `{"name":"get_status_tags","arguments":{}}`), &out)
		if len(out.StatusTags) != len(StatusTags) {
			t.Errorf("returned %d status tags, want %d", len(out.StatusTags), len(StatusTags))
		}
	})

	t.Run("get_recent_activity returns the events it counted", func(t *testing.T) {
		var out ActivityOutput
		decodeStructured(t, toolCall(t, srv, `{"name":"get_recent_activity","arguments":{}}`), &out)
		if out.Count != len(out.Events) {
			t.Errorf("count %d disagrees with %d events", out.Count, len(out.Events))
		}
	})
}

// TestErrorResultsCarryNoStructuredContent pins the rule that an error result stays prose-only.
// A payload that fails its own published schema is worse for a client than no payload: it makes
// every consumer handle a shape the schema says cannot occur.
func TestErrorResultsCarryNoStructuredContent(t *testing.T) {
	srv := seedStructuredFixture(t)

	for _, call := range []string{
		`{"name":"read_article","arguments":{"slug":"no-such-article"}}`,
		`{"name":"get_backlinks","arguments":{"slug":"no-such-article"}}`,
		`{"name":"search_wiki","arguments":{"query":"anything","type":["nonsense"]}}`,
		`{"name":"get_recent_activity","arguments":{"since":"not-a-duration"}}`,
	} {
		resp := toolCall(t, srv, call)
		if !resp.IsError {
			t.Errorf("expected an error result from %s", call)
			continue
		}
		if resp.StructuredContent != nil {
			t.Errorf("error result carried structuredContent: %s", call)
		}
	}
}

// TestEmptyListingsSerializeAsArrays guards the null-versus-[] trap: a schema declaring
// "type": "array" does not match null, so a nil slice would make an empty wiki fail validation
// against the server's own published schema.
func TestEmptyListingsSerializeAsArrays(t *testing.T) {
	srv := newMCPServer(t) // untouched wiki: only the seeded home page exists

	for name, call := range map[string]string{
		"list_agent_memories": `{"name":"list_agent_memories","arguments":{}}`,
		"list_agent_plans":    `{"name":"list_agent_plans","arguments":{}}`,
		"list_agent_skills":   `{"name":"list_agent_skills","arguments":{}}`,
		"list_articles":       `{"name":"list_articles","arguments":{}}`,
		"get_wiki_statistics": `{"name":"get_wiki_statistics","arguments":{}}`,
		"get_recent_activity": `{"name":"get_recent_activity","arguments":{}}`,
	} {
		resp := toolCall(t, srv, call)
		encoded, err := json.Marshal(resp.StructuredContent)
		if err != nil {
			t.Fatalf("%s: structuredContent does not serialize: %v", name, err)
		}
		if strings.Contains(string(encoded), ":null") {
			t.Errorf("%s emitted a null where the schema declares an array: %s", name, encoded)
		}
	}
}

// TestProseOnlyToolsAreUnchangedOnTheWire pins the compatibility promise: a tool that declares no
// outputSchema serializes exactly as it did before structured output existed, so clients that
// predate the feature see no change at all.
func TestProseOnlyToolsAreUnchangedOnTheWire(t *testing.T) {
	srv := seedStructuredFixture(t)

	resp := toolCall(t, srv, `{"name":"get_context_overview","arguments":{}}`)
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(encoded), "structuredContent") {
		t.Errorf("prose-only tool emitted a structuredContent key: %s", encoded)
	}
}

// TestStructuredOutputSurvivesBothEras pins that structured output is a property of the tool, not
// of a transport. The modern envelope rebuilds the result object to add resultType, so a payload
// could be dropped there without any legacy test noticing.
func TestStructuredOutputSurvivesBothEras(t *testing.T) {
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
		t.Errorf("modern results still need resultType, got %v", result["resultType"])
	}
	structured, ok := result["structuredContent"].(map[string]interface{})
	if !ok {
		t.Fatalf("structuredContent did not survive the modern envelope: %v", result)
	}
	if tags, ok := structured["status_tags"].([]interface{}); !ok || len(tags) != len(StatusTags) {
		t.Errorf("modern structuredContent lost its payload: %v", structured)
	}

	// tools/list must advertise the schema in both eras, or a modern client cannot discover
	// what a legacy client can.
	for era, listParams := range map[string]json.RawMessage{
		"modern": modernParams(t, ModernProtocolVersion, nil),
		"legacy": nil,
	} {
		listEnvelope, _ := callModern(t, srv, "tools/list", listParams, nil)
		tools := listEnvelope["result"].(map[string]interface{})["tools"].([]interface{})
		var found bool
		for _, raw := range tools {
			entry := raw.(map[string]interface{})
			if entry["name"] != "get_status_tags" {
				continue
			}
			found = true
			if _, present := entry["outputSchema"]; !present {
				t.Errorf("%s era: get_status_tags is missing its outputSchema", era)
			}
		}
		if !found {
			t.Errorf("%s era: get_status_tags is absent from tools/list", era)
		}
	}
}

// decodeStructured re-decodes a structured payload through JSON, so the test reads exactly what a
// client would receive rather than the in-memory value the handler happened to build.
func decodeStructured(t *testing.T, resp ToolResponse, dst interface{}) {
	t.Helper()
	if resp.IsError {
		t.Fatalf("tool returned an error: %s", resp.Content[0].Text)
	}
	encoded, err := json.Marshal(resp.StructuredContent)
	if err != nil {
		t.Fatalf("structuredContent does not serialize: %v", err)
	}
	if err := json.Unmarshal(encoded, dst); err != nil {
		t.Fatalf("structuredContent does not decode into %T: %v", dst, err)
	}
}

// validateAgainstSchema reports every way `value` fails `schema`, describing each problem by its
// path. It covers the JSON Schema vocabulary these schemas use — type, properties, required, and
// items — and deliberately no more; a general validator would be a dependency bought to check a
// fixed set of hand-written schemas.
func validateAgainstSchema(value interface{}, schema map[string]interface{}, path string) []string {
	var problems []string

	declared, _ := schema["type"].(string)
	if declared != "" && !jsonValueHasType(value, declared) {
		return []string{fmt.Sprintf("%s: schema says %s, payload has %T", path, declared, value)}
	}

	switch declared {
	case "object":
		obj, ok := value.(map[string]interface{})
		if !ok {
			return problems
		}
		for _, req := range schemaRequiredNames(schema) {
			if _, present := obj[req]; !present {
				problems = append(problems, fmt.Sprintf("%s: required property %q is absent", path, req))
			}
		}
		props, _ := schema["properties"].(map[string]interface{})
		for key, raw := range obj {
			sub, ok := props[key].(map[string]interface{})
			if !ok {
				problems = append(problems, fmt.Sprintf("%s.%s: present in the payload but absent from the schema", path, key))
				continue
			}
			problems = append(problems, validateAgainstSchema(raw, sub, path+"."+key)...)
		}
	case "array":
		items, ok := schema["items"].(map[string]interface{})
		if !ok {
			return problems
		}
		list, ok := value.([]interface{})
		if !ok {
			return problems
		}
		for i, elem := range list {
			problems = append(problems, validateAgainstSchema(elem, items, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}

	return problems
}

// jsonValueHasType reports whether a decoded JSON value matches a JSON Schema type name. Numbers
// need care: encoding/json decodes every number into float64, so "integer" can only be checked as
// "a number with no fractional part".
func jsonValueHasType(value interface{}, declared string) bool {
	switch declared {
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && n == float64(int64(n))
	default:
		return true
	}
}

// schemaRequiredNames reads a schema's "required" list, which the builders in mcp_tool_output.go
// emit as []string while a JSON round-trip would produce []interface{}.
func schemaRequiredNames(schema map[string]interface{}) []string {
	switch req := schema["required"].(type) {
	case []string:
		return req
	case []interface{}:
		names := make([]string, 0, len(req))
		for _, r := range req {
			if s, ok := r.(string); ok {
				names = append(names, s)
			}
		}
		return names
	default:
		return nil
	}
}

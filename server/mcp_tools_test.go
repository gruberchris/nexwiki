package server

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// TestRegistryCoversEveryTool is the invariant the registry exists to enforce: a tool cannot be
// advertised by tools/list without being callable, or callable without being advertised. Before
// the registry, the schema lived in one 760-line literal and the handler in a separate 1,180-line
// switch, so adding a tool to one and forgetting the other compiled cleanly and shipped broken.
func TestRegistryCoversEveryTool(t *testing.T) {
	const expectedToolCount = 29

	if len(mcpToolRegistry) != expectedToolCount {
		t.Errorf("registry holds %d tools, expected %d — update the count in README.md, AGENTS.md, "+
			"docs/README.md, docs/mcp_server.md, and docs/second_brain_workflow_guide.md too",
			len(mcpToolRegistry), expectedToolCount)
	}

	seen := make(map[string]bool, len(mcpToolRegistry))
	for i, tool := range mcpToolRegistry {
		name, ok := tool.Schema["name"].(string)
		if !ok || name == "" {
			t.Fatalf("registry entry %d has no string name in its schema", i)
		}
		if seen[name] {
			t.Errorf("tool %q is registered more than once", name)
		}
		seen[name] = true

		if tool.Handler == nil {
			t.Errorf("tool %q has a schema but no handler", name)
		}
		if _, ok := tool.Schema["description"].(string); !ok {
			t.Errorf("tool %q has no description", name)
		}
		if _, ok := tool.Schema["inputSchema"].(map[string]interface{}); !ok {
			t.Errorf("tool %q has no inputSchema object", name)
		}
		if toolsByName[name] != &mcpToolRegistry[i] {
			t.Errorf("tool %q is not reachable through the dispatch index", name)
		}
	}

	if len(toolsByName) != len(mcpToolRegistry) {
		t.Errorf("dispatch index holds %d entries for %d registered tools",
			len(toolsByName), len(mcpToolRegistry))
	}
}

// TestToolSchemasMatchRegistryOrder pins that tools/list emits the registry verbatim and in order.
// Clients and docs present tools in this sequence, so reordering is a visible change.
func TestToolSchemasMatchRegistryOrder(t *testing.T) {
	schemas := toolSchemas()
	if len(schemas) != len(mcpToolRegistry) {
		t.Fatalf("toolSchemas() returned %d entries for %d tools", len(schemas), len(mcpToolRegistry))
	}
	for i := range schemas {
		got := schemas[i]["name"]
		want := mcpToolRegistry[i].Schema["name"]
		if got != want {
			t.Errorf("position %d: tools/list says %v, registry says %v", i, got, want)
		}
	}
}

// TestUnknownToolIsReportedNotPanicked pins the dispatch miss path preserved from the old switch's
// default case: an unknown name is a tool-level error result, not a JSON-RPC protocol error.
func TestUnknownToolIsReportedNotPanicked(t *testing.T) {
	srv := newMCPServer(t)

	params, err := json.Marshal(map[string]interface{}{"name": "no_such_tool", "arguments": map[string]any{}})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	result, rpcErr := srv.executeToolCallInternal(params)
	if rpcErr != nil {
		t.Fatalf("expected a tool-level error result, got a JSON-RPC error: %v", rpcErr)
	}
	resp, ok := result.(ToolResponse)
	if !ok {
		t.Fatalf("expected ToolResponse, got %T", result)
	}
	if !resp.IsError {
		t.Error("unknown tool should produce IsError=true")
	}
}

// TestEveryToolIsAnnotated guards the annotation surface. The specification's defaults are
// pessimistic — an unannotated tool is assumed destructive and open-world — so a tool added
// without a Behavior silently tells clients it might destroy data, which makes cautious clients
// prompt the user for it. That is exactly the friction annotations exist to remove.
func TestEveryToolIsAnnotated(t *testing.T) {
	for _, tool := range mcpToolRegistry {
		name := tool.Schema["name"].(string)
		if tool.Behavior.Title == "" {
			t.Errorf("tool %q has no Behavior.Title — add one to its toolDef", name)
		}
		// A read-only tool must not also claim to be destructive; the two contradict.
		if tool.Behavior.ReadOnly && tool.Behavior.Destructive {
			t.Errorf("tool %q is marked both ReadOnly and Destructive", name)
		}
	}
}

// TestAnnotationsProjectedIntoToolsList pins the wire shape: what tools/list actually emits.
func TestAnnotationsProjectedIntoToolsList(t *testing.T) {
	byName := make(map[string]map[string]interface{}, len(mcpToolRegistry))
	for _, entry := range toolSchemas() {
		byName[entry["name"].(string)] = entry
	}

	t.Run("read-only tool", func(t *testing.T) {
		entry := byName["get_context_overview"]
		ann := entry["annotations"].(map[string]interface{})

		if ann["readOnlyHint"] != true {
			t.Error("get_context_overview must be readOnlyHint:true — it is the first call of every session")
		}
		// destructiveHint/idempotentHint are defined as meaningful only for writes; emitting
		// them here would contradict readOnlyHint.
		if _, present := ann["destructiveHint"]; present {
			t.Error("read-only tools should not carry destructiveHint")
		}
		if entry["title"] != "Get Context Overview" {
			t.Errorf("title should also appear top-level for 2026-07-28 clients, got %v", entry["title"])
		}
	})

	t.Run("additive write", func(t *testing.T) {
		ann := byName["create_wiki_article"]["annotations"].(map[string]interface{})
		if ann["readOnlyHint"] != false {
			t.Error("create_wiki_article writes")
		}
		if ann["destructiveHint"] != false {
			t.Error("creating a new article is additive, not destructive")
		}
	})

	t.Run("destructive write", func(t *testing.T) {
		for _, name := range []string{"delete_wiki_article", "delete_agent_memory",
			"edit_wiki_article", "update_article_tags", "revert_article_version", "import_okf_bundle"} {
			ann := byName[name]["annotations"].(map[string]interface{})
			if ann["destructiveHint"] != true {
				t.Errorf("%s can overwrite or remove existing content and must be destructiveHint:true", name)
			}
		}
	})

	t.Run("deletes are idempotent", func(t *testing.T) {
		// Deleting an already-deleted article has no further effect.
		for _, name := range []string{"delete_wiki_article", "delete_agent_memory"} {
			ann := byName[name]["annotations"].(map[string]interface{})
			if ann["idempotentHint"] != true {
				t.Errorf("%s should be idempotentHint:true", name)
			}
		}
	})

	t.Run("the whole server is a closed world", func(t *testing.T) {
		// Every tool operates on the local wiki directory and never reaches an external system.
		// This is the opposite of the spec default (openWorldHint defaults to true).
		for name, entry := range byName {
			ann := entry["annotations"].(map[string]interface{})
			if ann["openWorldHint"] != false {
				t.Errorf("%s should be openWorldHint:false — NexWiki never leaves the local wiki", name)
			}
		}
	})
}

// TestToolSchemasDoNotMutateRegistry pins that projecting annotations leaves the registry's own
// Schema maps untouched — otherwise repeated tools/list calls would accumulate keys.
func TestToolSchemasDoNotMutateRegistry(t *testing.T) {
	for _, tool := range mcpToolRegistry {
		if _, leaked := tool.Schema["annotations"]; leaked {
			t.Errorf("tool %q had annotations merged into its source schema", tool.Schema["name"])
		}
	}
	first := len(toolSchemas()[0])
	_ = toolSchemas()
	if got := len(toolSchemas()[0]); got != first {
		t.Errorf("tools/list entry grew across calls: %d then %d keys", first, got)
	}
}

// TestReadOnlyToolsCannotWrite is a behavioral cross-check rather than a declaration check: it
// runs every tool marked read-only and asserts the wiki is byte-identical afterwards. An
// annotation that merely repeats what the author believed is worth little; this verifies it.
func TestReadOnlyToolsCannotWrite(t *testing.T) {
	srv := newMCPServer(t)

	seed, err := srv.Storage.SaveArticle("", "Annotation Probe", "body text", "desc", "", "",
		"seed", []string{"probe"}, "")
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}

	snapshot := func() map[string]string {
		t.Helper()
		arts, err := srv.Storage.ListArticles()
		if err != nil {
			t.Fatalf("ListArticles failed: %v", err)
		}
		state := make(map[string]string, len(arts))
		for _, a := range arts {
			full, err := srv.Storage.GetArticle(a.Slug)
			if err != nil {
				continue
			}
			state[a.Slug] = fmt.Sprintf("%d|%s|%v|%s", full.Version, full.Title, full.Tags, full.Content)
		}
		return state
	}

	// Plausible arguments per read-only tool; tools with no required args get an empty object.
	args := map[string]string{
		"search_wiki":         `{"query":"body"}`,
		"read_article":        fmt.Sprintf(`{"slug":%q}`, seed.Slug),
		"get_article_history": fmt.Sprintf(`{"slug":%q}`, seed.Slug),
		"get_backlinks":       fmt.Sprintf(`{"slug":%q}`, seed.Slug),
		"get_recent_activity": `{"since":"24h"}`,
	}

	before := snapshot()
	for _, tool := range mcpToolRegistry {
		if !tool.Behavior.ReadOnly {
			continue
		}
		name := tool.Schema["name"].(string)
		raw := args[name]
		if raw == "" {
			raw = `{}`
		}
		if _, rpcErr := tool.Handler(srv, json.RawMessage(raw)); rpcErr != nil {
			t.Errorf("%s returned a protocol error: %v", name, rpcErr)
		}
	}
	after := snapshot()

	if len(before) != len(after) {
		t.Fatalf("a read-only tool changed the article count: %d -> %d", len(before), len(after))
	}
	for slug, state := range before {
		if after[slug] != state {
			t.Errorf("a read-only tool modified %q:\n  before: %s\n  after:  %s", slug, state, after[slug])
		}
	}
}

// TestDecodeToolArgsNamesTheWrongField covers an error message that misdirected the caller.
//
// Handlers folded the JSON decode into their required-field check, so any decode failure was
// reported as whichever field the handler named first. Passing search_wiki a string `type` — the
// schema wants an array, and a string is the natural mistake to make — answered "Missing or
// invalid 'query' argument" for a request whose query was present and correct. An agent following
// that message rewrites the one argument that was already right, and gets the same error again.
func TestDecodeToolArgsNamesTheWrongField(t *testing.T) {
	var args struct {
		Query string   `json:"query"`
		Types []string `json:"type"`
	}

	rpcErr := decodeToolArgs(json.RawMessage(`{"query":"zebra","type":"memories"}`), &args)
	if rpcErr == nil {
		t.Fatal("expected a decode error for a string in an array field")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("code = %d, want -32602", rpcErr.Code)
	}
	if !strings.Contains(rpcErr.Message, "'type'") {
		t.Errorf("message %q does not name the offending field 'type'", rpcErr.Message)
	}
	if strings.Contains(rpcErr.Message, "query") {
		t.Errorf("message %q blames 'query', which was valid", rpcErr.Message)
	}
}

// TestDecodeToolArgsAcceptsValidArguments confirms the helper stays out of the way when the
// payload is well formed, including the optional fields a caller omits.
func TestDecodeToolArgsAcceptsValidArguments(t *testing.T) {
	var args struct {
		Query string   `json:"query"`
		Types []string `json:"type"`
		Limit int      `json:"limit"`
	}

	if rpcErr := decodeToolArgs(json.RawMessage(`{"query":"zebra","type":["memories"],"limit":5}`), &args); rpcErr != nil {
		t.Fatalf("unexpected error: %s", rpcErr.Message)
	}
	if args.Query != "zebra" || len(args.Types) != 1 || args.Limit != 5 {
		t.Errorf("decoded %+v, want query=zebra type=[memories] limit=5", args)
	}
}

// TestDecodeToolArgsFallsBackForFieldlessErrors keeps malformed JSON — which carries no field
// information — from producing an empty or misleading message.
func TestDecodeToolArgsFallsBackForFieldlessErrors(t *testing.T) {
	var args struct {
		Query string `json:"query"`
	}

	rpcErr := decodeToolArgs(json.RawMessage(`{"query":`), &args)
	if rpcErr == nil {
		t.Fatal("expected an error for truncated JSON")
	}
	if !strings.HasPrefix(rpcErr.Message, "Invalid arguments: ") || len(rpcErr.Message) <= len("Invalid arguments: ") {
		t.Errorf("message %q carries no explanation", rpcErr.Message)
	}
}

// TestSchemaTypeNameSpeaksJSONSchema keeps decode errors in the vocabulary the caller read the
// schema in. An agent told its argument "expects []string" has to map a Go type name back onto the
// `"type": "array"` it saw in tools/list.
func TestSchemaTypeNameSpeaksJSONSchema(t *testing.T) {
	cases := []struct {
		value interface{}
		want  string
	}{
		{[]string{}, "array"},
		{map[string]interface{}{}, "object"},
		{"", "string"},
		{false, "boolean"},
		{0, "integer"},
		{0.0, "number"},
	}
	for _, tc := range cases {
		if got := schemaTypeName(reflect.TypeOf(tc.value)); got != tc.want {
			t.Errorf("schemaTypeName(%T) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// TestAgentCreateToolsAcceptTags is the §9.2 regression. create_wiki_article and
// create_agent_skill always took a `tags` argument; create_agent_memory and create_agent_plan did
// not, so an agent that wanted a plan already in flight had to follow with a second call — one
// annotated destructiveHint:true, which makes a cautious client stop and ask the user to approve a
// call that only existed because the first tool lacked an argument its siblings had. Lifecycle
// state now travels in `status` rather than `tags`, and both are settable at creation.
func TestAgentCreateToolsAcceptTags(t *testing.T) {
	t.Run("plan is created in flight in one call", func(t *testing.T) {
		srv := newMCPServer(t)
		resp := toolCall(t, srv, `{"name":"create_agent_plan","arguments":{"title":"Tagged Plan","content":"# Plan","project_context":"nexwiki","status":"implementing","tags":["postgres"]}}`)
		if resp.IsError {
			t.Fatalf("create failed: %s", resp.Content[0].Text)
		}
		art, err := srv.Storage.GetArticle("tagged-plan")
		if err != nil {
			t.Fatalf("GetArticle failed: %v", err)
		}
		if art.Status != "implementing" {
			t.Errorf("status was dropped: %q", art.Status)
		}
		if !hasTagFold(art.Tags, "postgres") {
			t.Errorf("caller tag was dropped: %v", art.Tags)
		}
		// The derived project tag must survive alongside it, not be replaced by it.
		if !hasTagFold(art.Tags, "nexwiki") {
			t.Errorf("project-context tag was lost: %v", art.Tags)
		}
	})

	t.Run("memory keeps its tool-managed scope tag", func(t *testing.T) {
		srv := newMCPServer(t)
		resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"memory_kind":"project","title":"Tagged Memory","content":"# M","memory_type":"nexwiki","tags":["review"],"description":"fixture memory","source":"test fixture"}}`)
		if resp.IsError {
			t.Fatalf("create failed: %s", resp.Content[0].Text)
		}
		art, _ := srv.Storage.GetArticle("tagged-memory")
		if !hasTagFold(art.Tags, "review") {
			t.Errorf("caller tag was dropped: %v", art.Tags)
		}
		if !hasTagFold(art.Tags, MemoryScopeTagPrefix+"nexwiki") {
			t.Errorf("tool-managed scope tag was lost: %v", art.Tags)
		}
	})

	t.Run("a caller cannot forge a memory scope tag", func(t *testing.T) {
		srv := newMCPServer(t)
		// Forging memory-<scope> would let a plan masquerade as scoped memory in list_agent_memories.
		toolCall(t, srv, `{"name":"create_agent_plan","arguments":{"title":"Forging Plan","content":"# P","project_context":"proj","tags":["memory-secret","postgres"]}}`)
		art, _ := srv.Storage.GetArticle("forging-plan")
		if hasTagFold(art.Tags, MemoryScopeTagPrefix+"secret") {
			t.Errorf("a forged memory-scope tag was accepted: %v", art.Tags)
		}
		if !hasTagFold(art.Tags, "postgres") {
			t.Errorf("legitimate tags must survive alongside a rejected one: %v", art.Tags)
		}

		srv2 := newMCPServer(t)
		toolCall(t, srv2, `{"name":"create_agent_memory","arguments":{"memory_kind":"project","title":"Forging Memory","content":"# M","memory_type":"real","tags":["memory-fake"],"description":"fixture memory","source":"test fixture"}}`)
		art2, _ := srv2.Storage.GetArticle("forging-memory")
		if hasTagFold(art2.Tags, MemoryScopeTagPrefix+"fake") {
			t.Errorf("a forged memory-scope tag was accepted: %v", art2.Tags)
		}
		if !hasTagFold(art2.Tags, MemoryScopeTagPrefix+"real") {
			t.Errorf("the real scope tag was lost: %v", art2.Tags)
		}
	})

	t.Run("omitting tags is unchanged", func(t *testing.T) {
		srv := newMCPServer(t)
		toolCall(t, srv, `{"name":"create_agent_plan","arguments":{"title":"Bare Plan","content":"# P","project_context":"nexwiki"}}`)
		art, _ := srv.Storage.GetArticle("bare-plan")
		if len(art.Tags) != 1 || !hasTagFold(art.Tags, "nexwiki") {
			t.Errorf("expected only the project tag, got %v", art.Tags)
		}
		// Lifecycle state lives in the field, never in the tag list.
		if art.Status != "draft" {
			t.Errorf("a plan created without a status starts in draft, got %q", art.Status)
		}
	})

	t.Run("a project_context that slugifies to nothing does not panic", func(t *testing.T) {
		srv := newMCPServer(t)
		// contextTags ends up empty here; the dedupe set must not be nil when written to.
		resp := toolCall(t, srv, `{"name":"create_agent_plan","arguments":{"title":"Odd Context","content":"# P","project_context":"!!!","status":"implementing"}}`)
		if resp.IsError {
			t.Fatalf("create failed: %s", resp.Content[0].Text)
		}
		art, _ := srv.Storage.GetArticle("odd-context")
		if art.Status != "implementing" {
			t.Errorf("status was dropped: %q", art.Status)
		}
	})

	t.Run("every create tool advertises tags", func(t *testing.T) {
		for _, name := range []string{"create_wiki_article", "create_agent_skill", "create_agent_memory", "create_agent_plan"} {
			schema := toolsByName[name].Schema["inputSchema"].(map[string]interface{})
			props := schema["properties"].(map[string]interface{})
			if _, ok := props["tags"]; !ok {
				t.Errorf("%s does not advertise a tags argument", name)
			}
		}
	})
}

func hasTagFold(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

// TestRejectToolArtifactTitle covers the guard against a client that serializes a tool call badly
// and leaks the tool's own verb into the title argument. Observed in the wild: a local model called
// create_wiki_article with title "create" and a complete article body, storing it at /articles/create.
func TestRejectToolArtifactTitle(t *testing.T) {
	rejected := []string{"create", "Create", "CREATE", "  create  ", "edit", "delete", "append",
		"read", "search", "get", "list", "update", "revert", "import", "export"}
	for _, title := range rejected {
		t.Run("reject/"+title, func(t *testing.T) {
			if rejectToolArtifactTitle(title, "article") == nil {
				t.Errorf("expected %q to be rejected as a bare tool verb", title)
			}
		})
	}

	// Every one of these is either a real title in this wiki or a plausible one. The guard must
	// not touch them — a false positive here blocks legitimate authoring.
	allowed := []string{"", "Go", "C", "Zig", "C++", "Large Language Models", "Markov Chains",
		"read_write_lock", "Creating a Wiki Article", "Get Started with Go", "Editor",
		"Reading List", "Import/Export Formats", "Updates"}
	for _, title := range allowed {
		t.Run("allow/"+title, func(t *testing.T) {
			if resp := rejectToolArtifactTitle(title, "article"); resp != nil {
				t.Errorf("expected %q to be allowed, got rejection: %s", title, resp.Content[0].Text)
			}
		})
	}
}

// TestBareToolVerbsCoverRegistry keeps the static bareToolVerbs list honest. rejectToolArtifactTitle
// cannot consult mcpToolRegistry — doing so closes an initialization cycle — so the coverage check
// lives here, where reading the registry is free of that constraint.
func TestBareToolVerbsCoverRegistry(t *testing.T) {
	// wiki_health leads with a noun, not a verb, and "Wiki" is a plausible article title, so it is
	// deliberately absent from bareToolVerbs.
	exempt := map[string]bool{"wiki": true}

	for i := range mcpToolRegistry {
		name, ok := mcpToolRegistry[i].Schema["name"].(string)
		if !ok || name == "" {
			t.Fatalf("tool at index %d has no name", i)
		}
		head, _, found := strings.Cut(name, "_")
		if !found {
			continue
		}
		if !bareToolVerbs[head] && !exempt[head] {
			t.Errorf("tool %q leads with verb %q, which is missing from bareToolVerbs "+
				"(add it, or add it to this test's exempt set if it is not a verb)", name, head)
		}
	}
}

// TestCreateWikiArticleRejectsToolVerbTitle is the end-to-end assertion: the bad call is refused
// and, critically, no article is written. The original incident left a real 2,500-word article
// stranded at a meaningless slug precisely because the write went through.
func TestCreateWikiArticleRejectsToolVerbTitle(t *testing.T) {
	srv := newTestServer(t)

	args := json.RawMessage(`{"title":"create","content":"# A complete article body that should not be saved."}`)
	result, rpcErr := createWikiArticleTool.Handler(srv, args)
	if rpcErr != nil {
		t.Fatalf("expected a tool-level error response, got a JSON-RPC error: %v", rpcErr)
	}
	resp, ok := result.(ToolResponse)
	if !ok {
		t.Fatalf("expected ToolResponse, got %T", result)
	}
	if !resp.IsError {
		t.Fatal("expected IsError on a bare-verb title")
	}
	if !strings.Contains(resp.Content[0].Text, "bare MCP tool verb") {
		t.Errorf("error should explain the cause, got: %s", resp.Content[0].Text)
	}
	if _, err := srv.Storage.GetArticle("create"); err == nil {
		t.Error("article was written despite the rejection")
	}
}

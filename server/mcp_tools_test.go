package server

import (
	"encoding/json"
	"testing"
)

// TestRegistryCoversEveryTool is the invariant the registry exists to enforce: a tool cannot be
// advertised by tools/list without being callable, or callable without being advertised. Before
// the registry, the schema lived in one 760-line literal and the handler in a separate 1,180-line
// switch, so adding a tool to one and forgetting the other compiled cleanly and shipped broken.
func TestRegistryCoversEveryTool(t *testing.T) {
	const expectedToolCount = 27

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

package server

import "encoding/json"

// toolDef pairs an MCP tool's JSON schema with the handler that executes it. Keeping them in
// one value is the point of the registry: previously the schema lived in a 760-line literal and
// the handler in a 1,180-line switch, with nothing forcing the two to stay in sync.
type toolDef struct {
	// Schema is the entry emitted by tools/list (name, description, inputSchema).
	Schema map[string]interface{}
	// Handler receives the raw "arguments" object from tools/call.
	Handler func(*Server, json.RawMessage) (interface{}, *JSONRPCError)
}

// mcpToolRegistry is the single source of truth for the exposed MCP tools. The slice is ordered
// because tools/list emits it verbatim, and clients (and the docs) present tools in this order;
// grouping across mcp_tools_*.go files is therefore free to follow domain rather than sequence.
var mcpToolRegistry = []toolDef{
	searchWikiTool,
	readArticleTool,
	listArticlesTool,
	createWikiArticleTool,
	editWikiArticleTool,
	updateArticleTagsTool,
	deleteWikiArticleTool,
	getArticleHistoryTool,
	revertArticleVersionTool,
	getWikiStatisticsTool,
	createAgentMemoryTool,
	appendAgentMemoryTool,
	editAgentMemoryTool,
	deleteAgentMemoryTool,
	listAgentMemoriesTool,
	createAgentPlanTool,
	appendAgentPlanTool,
	editAgentPlanTool,
	listAgentPlansTool,
	createAgentSkillTool,
	listAgentSkillsTool,
	getStatusTagsTool,
	getRecentActivityTool,
	getBacklinksTool,
	getContextOverviewTool,
	exportOkfBundleTool,
	importOkfBundleTool,
}

// toolsByName indexes the registry for O(1) dispatch. Built once at init from the same slice
// tools/list projects, so a tool can never be listed without being callable or vice versa.
var toolsByName = func() map[string]*toolDef {
	m := make(map[string]*toolDef, len(mcpToolRegistry))
	for i := range mcpToolRegistry {
		name, _ := mcpToolRegistry[i].Schema["name"].(string)
		m[name] = &mcpToolRegistry[i]
	}
	return m
}()

// toolSchemas projects the registry into the tools/list payload.
func toolSchemas() []map[string]interface{} {
	schemas := make([]map[string]interface{}, 0, len(mcpToolRegistry))
	for _, t := range mcpToolRegistry {
		schemas = append(schemas, t.Schema)
	}
	return schemas
}

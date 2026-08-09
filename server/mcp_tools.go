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
	// Behavior tells clients what calling this tool does, so they can auto-approve safe reads
	// and confirm destructive writes. See toolBehavior.
	Behavior toolBehavior
}

// toolBehavior describes what a tool does to the wiki, projected into the MCP `annotations`
// object on each tools/list entry.
//
// This matters because the specification's defaults are pessimistic: an unannotated tool is
// assumed `destructiveHint: true` and `openWorldHint: true`. Shipping no annotations therefore
// tells every client that all 27 tools might destroy data and reach arbitrary external systems,
// so a cautious client prompts the user for each one — including get_context_overview, the very
// tool the agent skill says to call first in every session. That friction is what makes agents
// stop reaching for a tool at all.
//
// Annotations are hints, not guarantees; clients treat them as untrusted from untrusted servers.
// They are a description of intent, not an enforcement mechanism — the actual guards are the
// optimistic-locking checks and the reserved-type rules in the handlers.
type toolBehavior struct {
	// Title is a human-readable display name.
	Title string
	// ReadOnly marks a tool that does not modify the wiki at all.
	//
	// Note that every successful tool call — reads included — appends an entry to the durable
	// activity log. That is server-side audit bookkeeping, not a change to the content the tool
	// operates on, so it does not disqualify readOnlyHint. Reading the hint any other way would
	// make it unusable: no tool on any server that keeps an audit trail could ever claim it.
	ReadOnly bool
	// Destructive marks a write that can overwrite or remove existing content, as opposed to a
	// purely additive one. Meaningful only when ReadOnly is false.
	Destructive bool
	// Idempotent marks a write where repeating the same call has no further effect.
	// Meaningful only when ReadOnly is false.
	Idempotent bool
}

// annotations renders the behavior as the MCP annotations object.
//
// openWorldHint is false for every NexWiki tool without exception: the entire tool surface
// operates on the local wiki directory and never reaches out to an external system. That is a
// genuine property of this server worth advertising, and it is the opposite of the spec default.
func (b toolBehavior) annotations() map[string]interface{} {
	a := map[string]interface{}{
		"title":         b.Title,
		"readOnlyHint":  b.ReadOnly,
		"openWorldHint": false,
	}
	// destructiveHint and idempotentHint are defined as meaningful only for writes; emitting
	// them on a read-only tool would be noise contradicting readOnlyHint.
	if !b.ReadOnly {
		a["destructiveHint"] = b.Destructive
		a["idempotentHint"] = b.Idempotent
	}
	return a
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

// listedTools is the tools/list payload, built once: each tool's declared schema merged with
// its rendered annotations. Computed at init rather than per request so the payload is stable
// and the registry's own Schema maps are never mutated.
var listedTools = func() []map[string]interface{} {
	schemas := make([]map[string]interface{}, 0, len(mcpToolRegistry))
	for _, t := range mcpToolRegistry {
		entry := make(map[string]interface{}, len(t.Schema)+2)
		for k, v := range t.Schema {
			entry[k] = v
		}
		// `title` is a top-level Tool field in the 2026-07-28 revision and lives inside
		// annotations in earlier ones. Emitting both keeps every era's clients correct.
		if t.Behavior.Title != "" {
			entry["title"] = t.Behavior.Title
		}
		entry["annotations"] = t.Behavior.annotations()
		schemas = append(schemas, entry)
	}
	return schemas
}()

// toolSchemas projects the registry into the tools/list payload.
func toolSchemas() []map[string]interface{} {
	return listedTools
}

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// toolDef pairs an MCP tool's JSON schema with the handler that executes it. Keeping them in
// one value is the point of the registry: previously the schema lived in a 760-line literal and
// the handler in a 1,180-line switch, with nothing forcing the two to stay in sync.
type toolDef struct {
	// Schema is the entry emitted by tools/list (name, description, inputSchema).
	Schema map[string]interface{}
	// Output is the tool's outputSchema: the JSON Schema of the structuredContent it returns.
	// Nil for tools that answer only in prose. A tool that declares one MUST populate
	// ToolResponse.StructuredContent on success — see mcp_tool_output.go.
	Output map[string]interface{}
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
	editAgentSkillTool,
	listAgentSkillsTool,
	getStatusTagsTool,
	getRecentActivityTool,
	getBacklinksTool,
	getContextOverviewTool,
	exportOkfBundleTool,
	importOkfBundleTool,
	wikiHealthTool,
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
		if t.Output != nil {
			entry["outputSchema"] = t.Output
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

// decodeToolArgs unmarshals a tool's "arguments" object, reporting a malformed payload as its own
// distinct error.
//
// Handlers used to fold this into their required-field check —
// `if err := json.Unmarshal(...); err != nil || args.Slug == ""` — which blames whichever field
// the handler happens to name first no matter what actually went wrong. Passing search_wiki a
// string `type` (the schema wants an array) reported "Missing or invalid 'query' argument" for a
// request whose query was present and fine, sending the agent to fix the one argument that was
// already correct. A wrong type is exactly the mistake a model makes reading a schema, so the
// message it gets back needs to name the field it really got wrong.
func decodeToolArgs(args json.RawMessage, dst interface{}) *JSONRPCError {
	if err := json.Unmarshal(args, dst); err != nil {
		return &JSONRPCError{Code: -32602, Message: "Invalid arguments: " + describeDecodeError(err)}
	}
	return nil
}

// describeDecodeError renders a JSON decode failure in terms of the offending argument, falling
// back to the decoder's own text when the error carries no field information.
func describeDecodeError(err error) string {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return fmt.Sprintf("'%s' expects %s, got %s", typeErr.Field, schemaTypeName(typeErr.Type), typeErr.Value)
	}
	return err.Error()
}

// schemaTypeName names a Go type the way the tool's JSON Schema does. The caller is an agent that
// read `"type": "array"` from tools/list, so answering with Go's `[]string` describes the mistake
// in a vocabulary the agent has no reason to recognize.
func schemaTypeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Pointer:
		return schemaTypeName(t.Elem())
	default:
		return t.String()
	}
}

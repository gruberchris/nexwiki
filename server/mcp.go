package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// JSONRPCRequest represents an incoming request in the JSON-RPC 2.0 format.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id,omitempty"`
}

// JSONRPCResponse represents an outgoing response in the JSON-RPC 2.0 format.
type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// JSONRPCError defines the standard JSON-RPC 2.0 error block.
type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// ToolContent maps to the standard MCP tool execution content block.
type ToolContent struct {
	Type string `json:"type"` // e.g. "text"
	Text string `json:"text"`
}

// ToolResponse represents the standard tool/call execution result.
type ToolResponse struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// StartMCPServer runs the stdio MCP JSON-RPC protocol loop in a non-blocking background goroutine.
func (srv *Server) StartMCPServer() {
	scanner := bufio.NewScanner(os.Stdin)
	writer := os.Stdout

	_, _ = fmt.Fprintf(os.Stderr, "Always-on stdio MCP server loop successfully started in background!\n")

	// Read lines of JSON-RPC requests from standard input
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			sendError(writer, -32700, "Parse error: invalid JSON", nil)
			continue
		}

		// Handle request methods
		srv.handleRequest(writer, &req)
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		_, _ = fmt.Fprintf(os.Stderr, "MCP server stdio error: %v\n", err)
	}
}

// handleRequest dispatches JSON-RPC requests to appropriate tool handlers.
func (srv *Server) handleRequest(w io.Writer, req *JSONRPCRequest) {
	// Notifications (requests without an ID) can be ignored or logged to stderr
	if req.ID == nil {
		return
	}

	var result interface{}
	var rpcErr *JSONRPCError

	switch req.Method {
	case "initialize":
		result = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools":   map[string]interface{}{},
				"prompts": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "NexWiki MCP Server",
				"version": srv.Version,
			},
			// Connect-time hint surfaced by MCP clients as a system-prompt-style nudge, so
			// the agent reaches for NexWiki as a second brain without explicit prompting.
			"instructions": "This NexWiki server is the user's persistent second brain. Use it to store plans and " +
				"memories and to look up prior knowledge — do not keep that only in chat. At session start, load the " +
				"operating rules with read_article(slug: \"" + AgentGuidelinesSlug + "\"), then get_context_overview " +
				"and get_recent_activity(since: \"48h\") to orient. Save multi-step work with create_agent_plan, " +
				"durable facts with create_agent_memory (setting description and source), and search before writing.",
		}

	case "tools/list":
		result = map[string]interface{}{
			"tools": []map[string]interface{}{
				{
					"name":        "search_wiki",
					"description": "Perform full-text searches inside the NexWiki knowledge base using Bleve search query parsing. Returns scored article matches and highlighted content snippets.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"query": map[string]interface{}{
								"type":        "string",
								"description": "The search keywords or query string. Supports wildcards, quotes for exact matches, and boolean terms.",
							},
						},
						"required": []string{"query"},
					},
				},
				{
					"name":        "read_article",
					"description": "Retrieve the full raw Markdown content and front-matter configurations of a specific NexWiki article by its URL slug.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"slug": map[string]interface{}{
								"type":        "string",
								"description": "The clean URL-safe slug of the target article (e.g. 'home' or 'guides').",
							},
						},
						"required": []string{"slug"},
					},
				},
				{
					"name":        "list_articles",
					"description": "List all articles currently available inside your NexWiki knowledge base, showing their titles, URL slugs, and article types (e.g., Wiki Article, Agent Memory, Agent Plan, or Agent Skill).",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
				{
					"name":        "create_wiki_article",
					"description": "Create a brand new wiki article. (IMPORTANT: AI agents must ALWAYS load the global operational guidelines skill using 'read_article(slug: \"nexwiki-agent-guidelines\")' to understand formatting and style guide check requirements before executing this tool.)",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"title": map[string]interface{}{
								"type":        "string",
								"description": "The human-readable title of the new article (e.g. 'Advanced Go Syntax').",
							},
							"content": map[string]interface{}{
								"type":        "string",
								"description": "The raw Markdown content of the article body.",
							},
							"description": map[string]interface{}{
								"type":        "string",
								"description": "Optional one-line summary of the article, shown in list indexes and the context overview.",
							},
							"source": map[string]interface{}{
								"type":        "string",
								"description": "Optional provenance: the URL, document, or reference this knowledge came from. AI-created articles SHOULD cite their source.",
							},
							"resource": map[string]interface{}{
								"type":        "string",
								"description": "Optional OKF canonical URI identifying what the concept *is* (e.g. an official spec or homepage URL). Distinct from 'source' (where the knowledge came from).",
							},
							"tags": map[string]interface{}{
								"type": "array",
								"items": map[string]interface{}{
									"type": "string",
								},
								"description": "Optional status or user tags to apply to the article. Call get_status_tags to see the recognized status values (e.g. 'draft', 'wip'). The document type (Wiki vs the reserved AI-Agent-* classes) is set automatically by the creating tool, not via tags.",
							},
							"edit_summary": map[string]interface{}{
								"type":        "string",
								"description": "Optional description summarizing the purpose of the creation (e.g. 'Initial seed guide').",
							},
						},
						"required": []string{"title", "content"},
					},
				},
				{
					"name":        "edit_wiki_article",
					"description": "Modify the title, markdown content, tags, or edit summary of an existing article. Employs optimistic locking to prevent concurrent overwrite collisions.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"slug": map[string]interface{}{
								"type":        "string",
								"description": "The unique URL-safe identifier slug of the article to edit.",
							},
							"title": map[string]interface{}{
								"type":        "string",
								"description": "The updated title of the article (can remain identical to original).",
							},
							"content": map[string]interface{}{
								"type":        "string",
								"description": "The updated raw Markdown content of the article body.",
							},
							"description": map[string]interface{}{
								"type":        "string",
								"description": "Optional one-line summary of the article. Omit or pass empty to preserve the existing description.",
							},
							"source": map[string]interface{}{
								"type":        "string",
								"description": "Optional provenance reference. Omit or pass empty to preserve the existing source.",
							},
							"resource": map[string]interface{}{
								"type":        "string",
								"description": "Optional OKF canonical URI of the concept. Pointer semantics: omit to preserve the existing value, pass an empty string to clear it, or a value to replace it.",
							},
							"tags": map[string]interface{}{
								"type": "array",
								"items": map[string]interface{}{
									"type": "string",
								},
								"description": "Optional tags to set on the article (replaces existing user tags; tool-managed memory-scope tags are always preserved). Call get_status_tags to see the recognized status values (e.g. 'completed', 'review').",
							},
							"loaded_version": map[string]interface{}{
								"type":        "integer",
								"description": "The active version number of the article loaded by the client (helps detect multi-session edit collisions).",
							},
							"edit_summary": map[string]interface{}{
								"type":        "string",
								"description": "Optional summary outlining what changed (e.g., 'Corrected spelling error').",
							},
						},
						"required": []string{"slug", "title", "content", "loaded_version"},
					},
				},
				{
					"name":        "update_article_tags",
					"description": "Directly update the tags array of an existing article. This is fast, token-efficient, and prevents modifying any page content body. Employs optimistic locking.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"slug": map[string]interface{}{
								"type":        "string",
								"description": "The unique URL-safe identifier slug of the article to update tags for.",
							},
							"tags": map[string]interface{}{
								"type": "array",
								"items": map[string]interface{}{
									"type": "string",
								},
								"description": "The complete array of user/status tags to apply to the article (replaces existing user tags; tool-managed memory-scope tags are always preserved).",
							},
							"loaded_version": map[string]interface{}{
								"type":        "integer",
								"description": "Optional. The active version number of the article loaded by the client (helps detect multi-session edit collisions).",
							},
							"edit_summary": map[string]interface{}{
								"type":        "string",
								"description": "Optional. Summary explaining the tag updates.",
							},
						},
						"required": []string{"slug", "tags"},
					},
				},
				{
					"name":        "delete_wiki_article",
					"description": "Permanently delete an existing wiki article and its historical backups from disk. Refuses protected AI Agent Memories — use 'delete_agent_memory' for those.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"slug": map[string]interface{}{
								"type":        "string",
								"description": "The unique URL-safe slug of the article to delete.",
							},
						},
						"required": []string{"slug"},
					},
				},
				{
					"name":        "get_article_history",
					"description": "Retrieve the full revision history log of a wiki page, including version numbers, timestamps, and edit summaries.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"slug": map[string]interface{}{
								"type":        "string",
								"description": "The URL-safe slug of the target article.",
							},
						},
						"required": []string{"slug"},
					},
				},
				{
					"name":        "revert_article_version",
					"description": "Revert the active state of an article back to a historical version number.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"slug": map[string]interface{}{
								"type":        "string",
								"description": "The URL-safe slug of the target article to roll back.",
							},
							"version": map[string]interface{}{
								"type":        "integer",
								"description": "The historical version number to restore.",
							},
						},
						"required": []string{"slug", "version"},
					},
				},
				{
					"name":        "get_wiki_statistics",
					"description": "Retrieve high-level wiki statistics, including total articles, storage footprint, and a list of dead or broken double-bracket internal WikiLinks.",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
				{
					"name":        "create_agent_memory",
					"description": "Create a brand new protected AI Agent Memory document. The 'memory_type' controls the tag applied and how the memory is scoped: use the project name (e.g. 'nexwiki') for project-specific knowledge, a topic name (e.g. 'docker') for reusable cross-project knowledge, or omit it for general knowledge (no scope tag). Memories must be succinct and high-value — they are loaded into agent context windows, so keep them short, specific, and free of repetition. Search for an existing memory first; if one becomes stale later, use 'edit_agent_memory' to correct it or 'delete_agent_memory' to retire it rather than creating near-duplicates. The reserved AI-Agent-Memory type must NEVER be relabelled unless explicitly instructed. (IMPORTANT: AI agents must ALWAYS load the global operational guidelines skill using 'read_article(slug: \"nexwiki-agent-guidelines\")' before executing this tool.)",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"title": map[string]interface{}{
								"type":        "string",
								"description": "The human-readable title of the memory article (e.g. 'NexWiki MCP Tag Preservation Rules').",
							},
							"content": map[string]interface{}{
								"type":        "string",
								"description": "The raw Markdown content of the memory document. Keep it succinct — bullet points over paragraphs, one clear insight per memory.",
							},
							"memory_type": map[string]interface{}{
								"type":        "string",
								"description": "Scopes the memory and determines its tag. Use a project name (e.g. 'nexwiki') for project-specific knowledge, a topic name (e.g. 'docker') for cross-project knowledge, or omit for general knowledge. Becomes the tool-managed scope tag 'memory-<memory_type>', or no scope tag if omitted; the document type is always AI-Agent-Memory.",
							},
							"description": map[string]interface{}{
								"type":        "string",
								"description": "Optional one-line summary of the memory, shown in list indexes and the context overview.",
							},
							"source": map[string]interface{}{
								"type":        "string",
								"description": "Optional provenance: where this knowledge came from (URL, document, or session context).",
							},
							"edit_summary": map[string]interface{}{
								"type":        "string",
								"description": "Optional revision log description summarizing why this memory was created.",
							},
						},
						"required": []string{"title", "content"},
					},
				},
				{
					"name":        "append_agent_memory",
					"description": "Append logs, subtask completions, or troubleshooting observations to the end of an existing protected AI Agent Memory document (must be of OKF type AI-Agent-Memory). If existing memory content is stale or wrong, use 'edit_agent_memory' to correct it in place instead of appending contradictions.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"slug": map[string]interface{}{
								"type":        "string",
								"description": "The unique URL-safe slug of the memory article to append to.",
							},
							"content_to_append": map[string]interface{}{
								"type":        "string",
								"description": "The raw Markdown text to append to the end of the memory content.",
							},
							"edit_summary": map[string]interface{}{
								"type":        "string",
								"description": "Optional summary outlining what details were appended.",
							},
						},
						"required": []string{"slug", "content_to_append"},
					},
				},
				{
					"name":        "edit_agent_memory",
					"description": "Replace or correct an existing protected AI Agent Memory in place. Prefer this over creating a near-duplicate memory: update stale facts directly, then note what changed in edit_summary. The reserved AI-Agent-Memory type and its memory-<scope> tag are strictly preserved. Employs optimistic locking.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"slug": map[string]interface{}{
								"type":        "string",
								"description": "The unique URL-safe slug of the memory to edit.",
							},
							"title": map[string]interface{}{
								"type":        "string",
								"description": "Optional new memory title (preserves existing title if omitted).",
							},
							"content": map[string]interface{}{
								"type":        "string",
								"description": "Optional full replacement of the memory's Markdown content (preserves existing content if omitted). Use append_agent_memory to add without replacing.",
							},
							"description": map[string]interface{}{
								"type":        "string",
								"description": "Optional new one-line summary (preserves existing if omitted).",
							},
							"source": map[string]interface{}{
								"type":        "string",
								"description": "Optional new provenance reference (preserves existing if omitted).",
							},
							"tags": map[string]interface{}{
								"type": "array",
								"items": map[string]interface{}{
									"type": "string",
								},
								"description": "Optional tags to set on the memory (replaces existing tags; the tool-managed memory-<scope> tag is always preserved).",
							},
							"loaded_version": map[string]interface{}{
								"type":        "integer",
								"description": "The active version number of the memory loaded by the client (helps detect multi-session edit collisions).",
							},
							"edit_summary": map[string]interface{}{
								"type":        "string",
								"description": "Optional summary outlining what was corrected or changed.",
							},
						},
						"required": []string{"slug", "loaded_version"},
					},
				},
				{
					"name":        "delete_agent_memory",
					"description": "Permanently delete an obsolete or superseded protected AI Agent Memory. Use this when a memory is wrong or fully superseded; prefer edit_agent_memory to correct a memory rather than deleting and recreating it.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"slug": map[string]interface{}{
								"type":        "string",
								"description": "The unique URL-safe slug of the memory to delete.",
							},
						},
						"required": []string{"slug"},
					},
				},
				{
					"name":        "list_agent_memories",
					"description": "List all protected AI Agent Memory documents currently saved inside the knowledge base.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"memory_type": map[string]interface{}{
								"type":        "string",
								"description": "Optional filter by memory type (project name, topic name, or other free-form value used at creation). For example, 'nexwiki' returns only nexwiki project memories.",
							},
						},
					},
				},
				{
					"name":        "create_agent_plan",
					"description": "Create a brand new Collaborative AI Plan. Automatically sets the reserved AI-Agent-Plan type, which must NEVER be relabelled unless explicitly instructed. After a plan is fully implemented, use 'append_agent_plan' to add final notes, then use 'edit_agent_plan' to mark it as completed. (IMPORTANT: AI agents must ALWAYS load the global operational guidelines skill using 'read_article(slug: \"nexwiki-agent-guidelines\")' to understand how plans must be saved and structured before executing this tool.)",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"title": map[string]interface{}{
								"type":        "string",
								"description": "The human-readable title of the plan (e.g., 'Go 1.22 Migration Plan').",
							},
							"content": map[string]interface{}{
								"type":        "string",
								"description": "The raw Markdown content of the plan document.",
							},
							"project_context": map[string]interface{}{
								"type":        "string",
								"description": "The name of the project this plan is for. Generates a custom project tag.",
							},
							"description": map[string]interface{}{
								"type":        "string",
								"description": "Optional one-line summary of the plan, shown in list indexes and the context overview.",
							},
							"source": map[string]interface{}{
								"type":        "string",
								"description": "Optional provenance: where this plan originated (URL, ticket, or session context).",
							},
							"edit_summary": map[string]interface{}{
								"type":        "string",
								"description": "Optional summary detailing the creation.",
							},
						},
						"required": []string{"title", "content", "project_context"},
					},
				},
				{
					"name":        "append_agent_plan",
					"description": "Append task status, observations, or checklists to an existing Collaborative AI Plan (must be of OKF type AI-Agent-Plan). Use this to log implementation progress, and to add final notes when a plan is fully implemented before marking it completed.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"slug": map[string]interface{}{
								"type":        "string",
								"description": "The unique URL-safe slug of the plan to append to.",
							},
							"content_to_append": map[string]interface{}{
								"type":        "string",
								"description": "The raw Markdown text to append to the end of the plan.",
							},
							"edit_summary": map[string]interface{}{
								"type":        "string",
								"description": "Optional summary outlining the updates.",
							},
						},
						"required": []string{"slug", "content_to_append"},
					},
				},
				{
					"name":        "edit_agent_plan",
					"description": "Modify the title, content, tags, or edit summary of an existing Collaborative AI Plan. The reserved AI-Agent-Plan type is strictly preserved and must NEVER be relabelled. Use this to correct or rewrite plan content in-place, or to mark a plan as 'completed' by adding the 'completed' status tag.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"slug": map[string]interface{}{
								"type":        "string",
								"description": "The unique URL-safe slug of the plan to edit.",
							},
							"title": map[string]interface{}{
								"type":        "string",
								"description": "Optional new plan title (preserves existing title if omitted).",
							},
							"content": map[string]interface{}{
								"type":        "string",
								"description": "Optional replacement Markdown body. Omit to preserve existing content. Use append_agent_plan to add progress notes without replacing.",
							},
							"tags": map[string]interface{}{
								"type": "array",
								"items": map[string]interface{}{
									"type": "string",
								},
								"description": "Optional tags to set on the plan (replaces existing tags; the AI-Agent-Plan type is preserved). Use status tags to signal plan state — call get_status_tags to see recognized values (e.g. 'completed', 'wip', 'blocked').",
							},
							"loaded_version": map[string]interface{}{
								"type":        "integer",
								"description": "The active version number of the plan loaded by the client (helps detect multi-session edit collisions).",
							},
							"edit_summary": map[string]interface{}{
								"type":        "string",
								"description": "Optional summary outlining what details changed.",
							},
						},
						"required": []string{"slug", "loaded_version"},
					},
				},
				{
					"name":        "list_agent_plans",
					"description": "List all Collaborative AI Plans (OKF type AI-Agent-Plan) saved inside the knowledge base.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"project_context": map[string]interface{}{
								"type":        "string",
								"description": "Optional project context name to filter plans by.",
							},
							"tag": map[string]interface{}{
								"type":        "string",
								"description": "Optional tag to filter plans by. Use a status tag to find plans in a specific state (e.g. 'completed', 'wip'). Call get_status_tags to see all recognized status values.",
							},
						},
					},
				},
				{
					"name":        "create_agent_skill",
					"description": "Create a brand new Custom AI Skill. Automatically sets the reserved AI-Agent-Skill type, which must NEVER be relabelled unless explicitly instructed. Makes the skill part of the custom skills registry.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"title": map[string]interface{}{
								"type":        "string",
								"description": "The title of the skill (e.g. 'Docker Container Pruning').",
							},
							"content": map[string]interface{}{
								"type":        "string",
								"description": "The raw Markdown content of the skill instructions (SKILL.md format).",
							},
							"description": map[string]interface{}{
								"type":        "string",
								"description": "Optional one-line summary of what the skill does, shown in list indexes and the context overview.",
							},
							"source": map[string]interface{}{
								"type":        "string",
								"description": "Optional provenance: where this skill's procedure came from (URL, document, or session context).",
							},
							"tags": map[string]interface{}{
								"type": "array",
								"items": map[string]interface{}{
									"type": "string",
								},
								"description": "Optional tags to apply to the skill. Use status tags to signal the skill's state — call get_status_tags to see recognized values (e.g. 'draft', 'ready').",
							},
							"edit_summary": map[string]interface{}{
								"type":        "string",
								"description": "Optional summary describing the creation of the skill.",
							},
						},
						"required": []string{"title", "content"},
					},
				},
				{
					"name":        "list_agent_skills",
					"description": "List all Custom AI Skills (OKF type AI-Agent-Skill) currently saved in the knowledge base.",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
				{
					"name":        "get_status_tags",
					"description": "Returns the canonical list of recognized status tags used in NexWiki to indicate the lifecycle state of wiki articles and AI plans. Use these tags when creating or editing articles and plans to signal their current status. Status tags are displayed with highest priority on the home dashboard.",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
				{
					"name":        "get_recent_activity",
					"description": "Query the durable wiki activity log to see what changed and when — useful at session start to catch up on edits made by other agents, processes, or the human since you last looked. Events from a different MCP process may lag by milliseconds.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"since": map[string]interface{}{
								"type":        "string",
								"description": "Only return events newer than this. Accepts a Go duration (e.g. '30m', '24h', '168h' for a week) or an RFC3339 timestamp (e.g. '2026-06-10T00:00:00Z'). Omit for the newest events regardless of age.",
							},
							"limit": map[string]interface{}{
								"type":        "integer",
								"description": "Maximum number of events to return, newest kept (default 50, max 500).",
							},
							"action": map[string]interface{}{
								"type":        "string",
								"description": "Optional filter by action type.",
								"enum":        []string{"create", "edit", "delete", "read", "revert"},
							},
							"source": map[string]interface{}{
								"type":        "string",
								"description": "Optional filter by origin: 'mcp' for AI tool calls, 'api' for human web UI actions.",
								"enum":        []string{"mcp", "api"},
							},
						},
					},
				},
				{
					"name":        "get_backlinks",
					"description": "List all articles whose content links to the given article via double-bracket WikiLinks. Use this to traverse the knowledge graph in reverse: find the pages that reference a concept, decision, or note before editing or deleting it.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"slug": map[string]interface{}{
								"type":        "string",
								"description": "The URL-safe slug of the target article to find inbound links for.",
							},
						},
						"required": []string{"slug"},
					},
				},
				{
					"name":        "get_context_overview",
					"description": "Cheap progressive-disclosure index of the entire knowledge base: every wiki article, agent memory, plan, and skill on one compact line (title, slug, one-line summary, tags, updated date). Call this first to orient yourself in the wiki, then use read_article to load only the entries you actually need.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"type": map[string]interface{}{
								"type":        "string",
								"description": "Optional section filter: 'articles', 'memories', 'plans', or 'skills'. Omit for the full overview.",
								"enum":        []string{"articles", "memories", "plans", "skills"},
							},
						},
					},
				},
				{
					"name":        "export_okf_bundle",
					"description": "Export the entire knowledge base as a conformant Open Knowledge Format (OKF v0.1) bundle (a .zip). The bundle hierarchy is synthesized from each document's type, with reserved index.md / log.md files and bundle-relative links. Writes the archive into the data directory and returns its path.",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
				{
					"name":        "import_okf_bundle",
					"description": "Import an Open Knowledge Format (OKF v0.1) bundle (.zip) from a filesystem path into the knowledge base. Each concept document is created or updated (dedup by slug), bundle-relative links are translated back to WikiLinks, and a permissive conformance report is returned (documents missing a type default to Wiki and are flagged rather than rejected).",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"path": map[string]interface{}{
								"type":        "string",
								"description": "Filesystem path to the .zip OKF bundle to import.",
							},
						},
						"required": []string{"path"},
					},
				},
			},
		}

	case "tools/call":
		result, rpcErr = srv.executeToolCall(req.Params)

	case "prompts/list":
		result = map[string]interface{}{
			"prompts": []map[string]interface{}{
				{
					"name":        "article_creation_workflow",
					"description": "Guides the agent on how to correctly search for styling/formatting guidelines and custom memories before writing a new Wiki article, to avoid inconsistencies.",
					"arguments": []map[string]interface{}{
						{
							"name":        "title",
							"description": "The title of the article to be created.",
							"required":    true,
						},
						{
							"name":        "description",
							"description": "Brief summary of what the article should cover.",
							"required":    false,
						},
					},
				},
				{
					"name":        "project_planning_workflow",
					"description": "Guides the agent on how to collaboratively plan a new development task, outline subtasks, and ensure the plan is saved and updated in NexWiki.",
					"arguments": []map[string]interface{}{
						{
							"name":        "title",
							"description": "The title of the Collaborative Plan (e.g. Go 1.22 Migration Plan).",
							"required":    true,
						},
						{
							"name":        "project",
							"description": "The name of the project this plan belongs to (e.g. nexwiki).",
							"required":    true,
						},
					},
				},
			},
		}

	case "prompts/get":
		type GetPromptArgs struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		var promptArgs GetPromptArgs
		if err := json.Unmarshal(req.Params, &promptArgs); err != nil {
			rpcErr = &JSONRPCError{Code: -32602, Message: "Invalid prompt parameters"}
			break
		}

		switch promptArgs.Name {
		case "article_creation_workflow":
			title := promptArgs.Arguments["title"]
			desc := promptArgs.Arguments["description"]

			promptText := fmt.Sprintf(`You are an AI assistant tasked with creating a new article titled "%s" in the user's NexWiki knowledge base.

Before you begin writing the article, you MUST follow these steps to ensure format consistency and align with the user's rules:
1. Call 'list_agent_memories' or search for memory articles using 'search_wiki' specifically looking for "rules", "formatting", or "style guide" memories regarding this type of article (e.g., programming language guides, system architecture templates, etc.).
2. If any formatting guidelines or style memories are found, read their contents using 'read_article'.
3. Incorporate those styles, sections, structure, and constraints strictly into the new article's content.
4. Write the article content in clean, semantic Markdown.
5. Save the article using 'create_wiki_article'. Include a helpful edit summary detailing the style guidelines you incorporated.
6. Let the user know you successfully incorporated the specific style rules you found.`, title)

			if desc != "" {
				promptText += fmt.Sprintf("\n\nArticle Outline/Description: %s", desc)
			}

			result = map[string]interface{}{
				"description": "Guides the agent on how to correctly search for styling/formatting guidelines and custom memories before writing a new Wiki article.",
				"messages": []map[string]interface{}{
					{
						"role": "user",
						"content": map[string]interface{}{
							"type": "text",
							"text": promptText,
						},
					},
				},
			}

		case "project_planning_workflow":
			title := promptArgs.Arguments["title"]
			project := promptArgs.Arguments["project"]

			promptText := fmt.Sprintf(`You are an AI assistant tasked with creating a new Collaborative AI Plan for the project "%s" titled "%s".

Please follow these strict steps:
1. Collaboratively outline the plan with the user, dividing it into clear objectives, architectural details, technical requirements, and task checklists.
2. Format the plan using rich, clean Markdown.
3. Save the initial plan in NexWiki immediately using the 'create_agent_plan' tool. Make sure to specify the project_context as "%s".
4. Inform the user that the plan is saved in NexWiki, provide the article slug, and ask for their feedback or approval on the plan.
5. As tasks are completed or updated during implementation, use 'append_agent_plan' to log the progress and update the checklists.
6. When the plan is fully implemented, use 'append_agent_plan' to add final notes documenting anything worth noting (plan deviations, files created, tools used, unexpected challenges, or other observations).
7. After adding final notes, use 'edit_agent_plan' to mark the plan as completed by adding the 'completed' status tag.

IMPORTANT: The reserved AI-Agent-Plan type must NEVER be relabelled unless explicitly instructed by the user.`, project, title, project)

			result = map[string]interface{}{
				"description": "Guides the agent on how to collaboratively plan a new development task, outline subtasks, and ensure the plan is saved and updated in NexWiki.",
				"messages": []map[string]interface{}{
					{
						"role": "user",
						"content": map[string]interface{}{
							"type": "text",
							"text": promptText,
						},
					},
				},
			}

		default:
			rpcErr = &JSONRPCError{
				Code:    -32601,
				Message: fmt.Sprintf("Prompt not found: %s", promptArgs.Name),
			}
		}

	default:
		rpcErr = &JSONRPCError{
			Code:    -32601,
			Message: fmt.Sprintf("Method not found: %s", req.Method),
		}
	}

	// Send JSON-RPC response
	var resp JSONRPCResponse
	resp.JSONRPC = "2.0"
	resp.ID = req.ID

	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}

	respBytes, err := json.Marshal(resp)
	if err == nil {
		// Stdio transport expects each JSON-RPC envelope strictly on a single line!
		_, _ = fmt.Fprintf(w, "%s\n", string(respBytes))
	}
}

// logMCPToolCall logs a successfully executed MCP tool call and publishes it.
func (srv *Server) logMCPToolCall(params json.RawMessage) {
	if srv.EventBus == nil {
		return
	}

	type ToolCallParams struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}

	var args ToolCallParams
	if err := json.Unmarshal(params, &args); err != nil {
		return
	}

	// Unmarshal common arguments like slug and title
	var common struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	_ = json.Unmarshal(args.Arguments, &common)

	action := "read"
	tool := args.Name
	if strings.HasPrefix(tool, "create_") {
		action = "create"
	} else if strings.HasPrefix(tool, "edit_") || strings.HasPrefix(tool, "append_") || strings.HasPrefix(tool, "revert_") || strings.HasPrefix(tool, "update_") {
		action = "edit"
	} else if strings.HasPrefix(tool, "delete_") {
		action = "delete"
	}

	slug := common.Slug
	if slug == "" && common.Title != "" {
		slug = Slugify(common.Title)
	}

	// Determine category and title if it's a mutation
	title := common.Title
	if title == "" && slug != "" {
		if art, err := srv.Storage.GetArticle(slug); err == nil {
			title = art.Title
		}
	}

	agent := "AI Agent"
	if srvName := os.Getenv("NEXWIKI_NAME"); srvName != "" {
		agent = srvName
	}

	srv.EventBus.PublishActivity("mcp", action, tool, slug, title, agent)

	// When running as a mcp-only sidecar alongside a web server, forward the event to it.
	if srv.IsSecondaryProcess {
		go srv.forwardActivityToWebServer("mcp", action, tool, slug, title, agent)
	}

	// If it's a mutation, broadcast a WikiUpdate to sync all clients!
	if action != "read" {
		articles, err := srv.Storage.ListArticles()
		if err == nil {
			var targetTags []string
			targetType := ContentTypeWiki
			if slug != "" {
				if art, err := srv.Storage.GetArticle(slug); err == nil {
					targetTags = art.Tags
					targetType = art.Type
				}
			}

			dir := getArticleDirectory(targetType)
			dirCount := 0
			for _, a := range articles {
				if getArticleDirectory(a.Type) == dir {
					dirCount++
				}
			}

			updateType := "article-edited"
			if action == "create" {
				updateType = "article-added"
			} else if action == "delete" {
				updateType = "article-removed"
			}

			srv.EventBus.PublishWikiUpdate(WikiUpdate{
				Type:           updateType,
				Slug:           slug,
				Title:          title,
				Tags:           targetTags,
				Directory:      dir,
				TotalCount:     len(articles),
				DirectoryCount: dirCount,
			})
		}
	}
}

// forwardActivityToWebServer forwards the activity log to the main web server process via HTTP.
func (srv *Server) forwardActivityToWebServer(source, action, tool, slug, title, agent string) {
	// Construct payload
	payload := map[string]string{
		"source": source,
		"action": action,
		"tool":   tool,
		"slug":   slug,
		"title":  title,
		"agent":  agent,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	// We target the configured port. Default to 8080 if not set.
	port := srv.Port
	if port == "" {
		port = "8080"
	}

	url := fmt.Sprintf("http://127.0.0.1:%s/api/activity/log", port)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// Use a short timeout so we don't block
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// Silently ignore if the server is not running or listening
		return
	}
	_ = resp.Body.Close()
}

// memoryScopeTags returns the tool-managed memory-scope tags (memory-<scope>) present on a tag list.
// These are preserved across edits; the memory document *class* is carried by the OKF `type` field.
func memoryScopeTags(tags []string) []string {
	var out []string
	for _, tag := range tags {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(tag)), MemoryScopeTagPrefix) {
			out = append(out, tag)
		}
	}
	return out
}

// executeToolCall parses parameters and executes requested MCP tools, with automatic logging hooks.
func (srv *Server) executeToolCall(params json.RawMessage) (interface{}, *JSONRPCError) {
	result, rpcErr := srv.executeToolCallInternal(params)
	if rpcErr == nil {
		srv.logMCPToolCall(params)
	}
	return result, rpcErr
}

// executeToolCallInternal parses parameters and executes requested MCP tools.
func (srv *Server) executeToolCallInternal(params json.RawMessage) (interface{}, *JSONRPCError) {
	type ToolCallArgs struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}

	var args ToolCallArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &JSONRPCError{Code: -32602, Message: "Invalid tool call parameters"}
	}

	switch args.Name {
	case "search_wiki":
		type SearchArgs struct {
			Query string `json:"query"`
		}
		var searchArgs SearchArgs
		if err := json.Unmarshal(args.Arguments, &searchArgs); err != nil || searchArgs.Query == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'query' argument"}
		}

		results, err := srv.Storage.SearchArticles(searchArgs.Query)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: err.Error()}}}, nil
		}

		// Convert structured search results to friendly readable text for AI agents
		var text string
		if len(results) == 0 {
			text = fmt.Sprintf("No articles found matching query: '%s'\n", searchArgs.Query)
		} else {
			text = fmt.Sprintf("Found %d matching articles in NexWiki:\n\n", len(results))
			for i, res := range results {
				tagsStr := ""
				if len(res.Tags) > 0 {
					tagsStr = fmt.Sprintf(" | Tags: %s", strings.Join(res.Tags, ", "))
				}
				text += fmt.Sprintf("[%d] %s (Slug: %s, Score: %.3f%s)\n", i+1, res.Title, res.Slug, res.Score, tagsStr)
				for _, snippet := range res.Snippets {
					// Strip HTML <mark> tags to make it clean Markdown for the AI agent
					cleanSnippet := strings.ReplaceAll(snippet, "<mark>", "**")
					cleanSnippet = strings.ReplaceAll(cleanSnippet, "</mark>", "**")
					text += fmt.Sprintf("    Snippet: ... %s ...\n", cleanSnippet)
				}
				text += "\n"
			}
		}

		return ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}, nil

	case "read_article":
		type ReadArgs struct {
			Slug string `json:"slug"`
		}
		var readArgs ReadArgs
		if err := json.Unmarshal(args.Arguments, &readArgs); err != nil || readArgs.Slug == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'slug' argument"}
		}

		art, err := srv.Storage.GetArticle(readArgs.Slug)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error loading article '%s': %v", readArgs.Slug, err)}}}, nil
		}

		// Return tags in read metadata
		tagsStr := ""
		if len(art.Tags) > 0 {
			tagsStr = fmt.Sprintf("\nTags: %s", strings.Join(art.Tags, ", "))
		}
		descStr := ""
		if art.Description != "" {
			descStr = fmt.Sprintf("\nDescription: %s", art.Description)
		}
		resourceStr := ""
		if art.Resource != "" {
			resourceStr = fmt.Sprintf("\nResource: %s", art.Resource)
		}
		sourceStr := ""
		if art.Source != "" {
			sourceStr = fmt.Sprintf("\nSource: %s", art.Source)
		}

		// Return both front-matter configurations and full Markdown content to the agent
		text := fmt.Sprintf("Type: %s\nTitle: %s\nSlug: %s\nCreated: %s\nUpdated: %s%s%s%s%s\n\n%s",
			art.Type, art.Title, art.Slug, art.CreatedAt.Format(time.RFC3339), art.Timestamp.Format(time.RFC3339), descStr, resourceStr, sourceStr, tagsStr, art.Content)

		// Append inbound links for graph discoverability; never fail the read over a scan error
		if backlinks, blErr := srv.Storage.GetBacklinks(art.Slug); blErr == nil && len(backlinks) > 0 {
			const maxShownBacklinks = 15
			var refs []string
			for i, bl := range backlinks {
				if i >= maxShownBacklinks {
					refs = append(refs, fmt.Sprintf("and %d more", len(backlinks)-maxShownBacklinks))
					break
				}
				refs = append(refs, fmt.Sprintf("%s (%s)", bl.Title, bl.Slug))
			}
			text += fmt.Sprintf("\n\n---\nLinked from: %s", strings.Join(refs, ", "))
		}

		return ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}, nil

	case "list_articles":
		articles, err := srv.Storage.ListArticles()
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: err.Error()}}}, nil
		}

		var text string
		if len(articles) == 0 {
			text = "NexWiki contains no articles currently.\n"
		} else {
			text = fmt.Sprintf("NexWiki Directory Index contains %d articles:\n\n", len(articles))
			for i, art := range articles {
				articleType := "Wiki Article"
				switch art.Type {
				case ContentTypeMemory:
					articleType = "Agent Memory"
				case ContentTypePlan:
					articleType = "Agent Plan"
				case ContentTypeSkill:
					articleType = "Agent Skill"
				}

				tagsStr := ""
				if len(art.Tags) > 0 {
					tagsStr = fmt.Sprintf(" | Tags: %s", strings.Join(art.Tags, ", "))
				}
				text += fmt.Sprintf("[%d] %s (Slug: %s, Type: %s, Last Edited: %s%s)\n",
					i+1, art.Title, art.Slug, articleType, art.Timestamp.Format("2006-01-02 15:04:05"), tagsStr)
				if art.Description != "" {
					text += fmt.Sprintf("    Summary: %s\n", art.Description)
				}
			}
		}

		return ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}, nil

	case "create_wiki_article":
		type CreateArgs struct {
			Title       string   `json:"title"`
			Content     string   `json:"content"`
			Description string   `json:"description"`
			Source      string   `json:"source"`
			Resource    string   `json:"resource"`
			Tags        []string `json:"tags"`
			EditSummary string   `json:"edit_summary"`
		}
		var cArgs CreateArgs
		if err := json.Unmarshal(args.Arguments, &cArgs); err != nil || cArgs.Title == "" || cArgs.Content == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'title' or 'content' arguments"}
		}

		slug := Slugify(cArgs.Title)
		if _, err := srv.Storage.GetArticle(slug); err == nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: an article with title '%s' (slug: '%s') already exists", cArgs.Title, slug)}}}, nil
		}

		tags := validateAndCleanUserTags(cArgs.Tags, nil)
		// Regular article creation always produces a Wiki document; reserved types are tool-only.
		art, err := srv.Storage.SaveArticle("", cArgs.Title, cArgs.Content, cArgs.Description, cArgs.Source, cArgs.Resource, cArgs.EditSummary, tags, ContentTypeWiki)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error creating article: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Article '%s' created successfully.\nSlug: %s\nCreated At: %s\nVersion: %d\n",
			art.Title, art.Slug, art.CreatedAt.Format(time.RFC3339), art.Version)
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "edit_wiki_article":
		type EditArgs struct {
			Slug          string   `json:"slug"`
			Title         string   `json:"title"`
			Content       string   `json:"content"`
			Description   string   `json:"description"`
			Source        string   `json:"source"`
			Resource      *string  `json:"resource"`
			Tags          []string `json:"tags"`
			LoadedVersion int      `json:"loaded_version"`
			EditSummary   string   `json:"edit_summary"`
		}
		var eArgs EditArgs
		if err := json.Unmarshal(args.Arguments, &eArgs); err != nil || eArgs.Slug == "" || eArgs.Title == "" || eArgs.Content == "" || eArgs.LoadedVersion <= 0 {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. Requires 'slug', 'title', 'content', and positive 'loaded_version'"}
		}

		existing, err := srv.Storage.GetArticle(eArgs.Slug)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: article with slug '%s' not found", eArgs.Slug)}}}, nil
		}

		if existing.Version > 0 && existing.Version != eArgs.LoadedVersion {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: Version conflict! The article was updated by another session. Disk version is %d, but you loaded version %d. Re-fetch the article and try again.", existing.Version, eArgs.LoadedVersion)}}}, nil
		}

		tags := existing.Tags
		if eArgs.Tags != nil {
			tags = validateAndCleanUserTags(eArgs.Tags, existing.Tags)
		}

		// Empty/omitted description and source preserve the existing values
		description := existing.Description
		if eArgs.Description != "" {
			description = eArgs.Description
		}
		source := existing.Source
		if eArgs.Source != "" {
			source = eArgs.Source
		}
		// resource uses pointer semantics: omit=preserve, ""=clear, value=replace.
		resource := existing.Resource
		if eArgs.Resource != nil {
			resource = *eArgs.Resource
		}

		// Type is immutable on regular edits; preserve the existing document class.
		art, err := srv.Storage.SaveArticle(eArgs.Slug, eArgs.Title, eArgs.Content, description, source, resource, eArgs.EditSummary, tags, existing.Type)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error editing article: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Article '%s' (slug: %s) updated successfully.\nNew Version: %d\nLast Edited: %s\n",
			art.Title, art.Slug, art.Version, art.Timestamp.Format(time.RFC3339))
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "update_article_tags":
		type UpdateTagsArgs struct {
			Slug          string   `json:"slug"`
			Tags          []string `json:"tags"`
			LoadedVersion int      `json:"loaded_version"`
			EditSummary   string   `json:"edit_summary"`
		}
		var uArgs UpdateTagsArgs
		if err := json.Unmarshal(args.Arguments, &uArgs); err != nil || uArgs.Slug == "" || uArgs.Tags == nil {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. Requires 'slug' and 'tags' array."}
		}

		existing, err := srv.Storage.GetArticle(uArgs.Slug)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: article with slug '%s' not found", uArgs.Slug)}}}, nil
		}

		cleanedTags := validateAndCleanUserTags(uArgs.Tags, existing.Tags)

		art, err := srv.Storage.UpdateArticleTags(uArgs.Slug, cleanedTags, uArgs.LoadedVersion, uArgs.EditSummary)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error updating tags: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Article '%s' tags updated successfully.\nNew Version: %d\nTags: %s\n",
			art.Title, art.Version, strings.Join(art.Tags, ", "))
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "delete_wiki_article":
		type DelArgs struct {
			Slug string `json:"slug"`
		}
		var dArgs DelArgs
		if err := json.Unmarshal(args.Arguments, &dArgs); err != nil || dArgs.Slug == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'slug' argument"}
		}

		existing, err := srv.Storage.GetArticle(dArgs.Slug)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: article with slug '%s' not found", dArgs.Slug)}}}, nil
		}

		if existing.Type == ContentTypeMemory {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: this article is a protected AI Agent Memory. Use 'delete_agent_memory' to delete it intentionally, or 'edit_agent_memory' to correct it instead."}}}, nil
		}

		err = srv.Storage.DeleteArticle(dArgs.Slug)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error deleting article: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Article with slug '%s' has been permanently deleted from disk along with all history backups and media assets.\n", dArgs.Slug)
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "create_agent_memory":
		type CreateMemoryArgs struct {
			Title          string `json:"title"`
			Content        string `json:"content"`
			MemoryType     string `json:"memory_type"`
			ProjectContext string `json:"project_context"`
			Description    string `json:"description"`
			Source         string `json:"source"`
			EditSummary    string `json:"edit_summary"`
		}
		var mArgs CreateMemoryArgs
		if err := json.Unmarshal(args.Arguments, &mArgs); err != nil || mArgs.Title == "" || mArgs.Content == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'title' and 'content' are required."}
		}

		mType := strings.ToLower(strings.TrimSpace(mArgs.MemoryType))

		// The OKF type carries the memory document class; the scope facet rides as a
		// tool-managed memory-<scope> tag. A bare memory (no scope) carries no scope tag.
		var tags []string
		if mType != "" {
			tags = []string{MemoryScopeTagPrefix + Slugify(mType)}
		}

		title := mArgs.Title
		slug := Slugify(title)

		if _, err := srv.Storage.GetArticle(slug); err == nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: an article with slug '%s' already exists", slug)}}}, nil
		}

		summary := mArgs.EditSummary
		if summary == "" {
			if mType == "" {
				summary = "Created AI Agent Memory"
			} else {
				summary = fmt.Sprintf("Created AI Agent %s Memory", mType)
			}
		}

		art, err := srv.Storage.SaveArticle("", title, mArgs.Content, mArgs.Description, mArgs.Source, "", summary, tags, ContentTypeMemory)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error creating agent memory: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Protected AI Agent Memory '%s' created successfully.\nSlug: %s\nCreated At: %s\nVersion: %d\nTags: %s\n",
			art.Title, art.Slug, art.CreatedAt.Format(time.RFC3339), art.Version, strings.Join(art.Tags, ", "))
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "append_agent_memory":
		type AppendMemoryArgs struct {
			Slug            string `json:"slug"`
			ContentToAppend string `json:"content_to_append"`
			EditSummary     string `json:"edit_summary"`
		}
		var aArgs AppendMemoryArgs
		if err := json.Unmarshal(args.Arguments, &aArgs); err != nil || aArgs.Slug == "" || aArgs.ContentToAppend == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'slug' and 'content_to_append' are required."}
		}

		existing, err := srv.Storage.GetArticle(aArgs.Slug)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: article with slug '%s' not found", aArgs.Slug)}}}, nil
		}

		if existing.Type != ContentTypeMemory {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: target article is not a protected AI Agent Memory (type must be AI-Agent-Memory)."}}}, nil
		}

		newContent := existing.Content + "\n\n" + aArgs.ContentToAppend

		summary := aArgs.EditSummary
		if summary == "" {
			summary = "Appended AI Agent memory details"
		}

		art, err := srv.Storage.SaveArticle(existing.Slug, existing.Title, newContent, existing.Description, existing.Source, existing.Resource, summary, existing.Tags, existing.Type)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error appending agent memory: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Appended memory details to '%s' (version: %d, edited: %s).\n",
			art.Title, art.Version, art.Timestamp.Format(time.RFC3339))
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "edit_agent_memory":
		type EditMemoryArgs struct {
			Slug          string    `json:"slug"`
			Title         *string   `json:"title,omitempty"`
			Content       *string   `json:"content,omitempty"`
			Description   *string   `json:"description,omitempty"`
			Source        *string   `json:"source,omitempty"`
			Tags          *[]string `json:"tags,omitempty"`
			LoadedVersion int       `json:"loaded_version"`
			EditSummary   string    `json:"edit_summary"`
		}
		var eArgs EditMemoryArgs
		if err := json.Unmarshal(args.Arguments, &eArgs); err != nil || eArgs.Slug == "" || eArgs.LoadedVersion <= 0 {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'slug' and positive 'loaded_version' are required."}
		}

		existing, err := srv.Storage.GetArticle(eArgs.Slug)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: memory with slug '%s' not found", eArgs.Slug)}}}, nil
		}

		if existing.Type != ContentTypeMemory {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: target article is not a protected AI Agent Memory (type must be AI-Agent-Memory)."}}}, nil
		}

		if existing.Version > 0 && existing.Version != eArgs.LoadedVersion {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: Version conflict! The memory was updated by another session. Disk version is %d, but you loaded version %d. Re-read the memory and try again.", existing.Version, eArgs.LoadedVersion)}}}, nil
		}

		newTitle := existing.Title
		if eArgs.Title != nil {
			newTitle = strings.TrimSpace(*eArgs.Title)
			if newTitle == "" {
				return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: title cannot be empty"}}}, nil
			}
		}

		newContent := existing.Content
		if eArgs.Content != nil {
			if strings.TrimSpace(*eArgs.Content) == "" {
				return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: content cannot be empty. Use 'delete_agent_memory' to retire a memory entirely."}}}, nil
			}
			newContent = *eArgs.Content
		}

		newDescription := existing.Description
		if eArgs.Description != nil {
			newDescription = *eArgs.Description
		}
		newSource := existing.Source
		if eArgs.Source != nil {
			newSource = *eArgs.Source
		}

		newTags := existing.Tags
		if eArgs.Tags != nil {
			var parsedTags []string
			seen := make(map[string]bool)
			// Preserve the tool-managed memory-scope tag(s) first; type carries the class.
			for _, tag := range memoryScopeTags(existing.Tags) {
				tl := strings.ToLower(tag)
				if !seen[tl] {
					seen[tl] = true
					parsedTags = append(parsedTags, tag)
				}
			}
			for _, tag := range *eArgs.Tags {
				cleanTag := Slugify(tag)
				if cleanTag == "" || seen[strings.ToLower(cleanTag)] {
					continue
				}
				// Users may not forge new memory-scope tags; only the preserved ones above survive.
				if strings.HasPrefix(strings.ToLower(cleanTag), MemoryScopeTagPrefix) {
					continue
				}
				seen[strings.ToLower(cleanTag)] = true
				parsedTags = append(parsedTags, cleanTag)
			}
			newTags = parsedTags
		}

		summary := eArgs.EditSummary
		if summary == "" {
			summary = "Updated AI Agent Memory"
		}

		newResource := existing.Resource
		art, err := srv.Storage.SaveArticle(existing.Slug, newTitle, newContent, newDescription, newSource, newResource, summary, newTags, existing.Type)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error editing agent memory: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! AI Agent Memory '%s' updated successfully.\nSlug: %s\nNew Version: %d\nLast Edited: %s\nTags: %s\n",
			art.Title, art.Slug, art.Version, art.Timestamp.Format(time.RFC3339), strings.Join(art.Tags, ", "))
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "delete_agent_memory":
		type DelMemoryArgs struct {
			Slug string `json:"slug"`
		}
		var dArgs DelMemoryArgs
		if err := json.Unmarshal(args.Arguments, &dArgs); err != nil || dArgs.Slug == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'slug' argument"}
		}

		existing, err := srv.Storage.GetArticle(dArgs.Slug)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: memory with slug '%s' not found", dArgs.Slug)}}}, nil
		}

		if existing.Type != ContentTypeMemory {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: target article is not a protected AI Agent Memory. Use 'delete_wiki_article' for standard articles."}}}, nil
		}

		if err := srv.Storage.DeleteArticle(dArgs.Slug); err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error deleting agent memory: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! AI Agent Memory '%s' (slug: %s) has been permanently deleted from disk along with all history backups.\n", existing.Title, existing.Slug)
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "list_agent_memories":
		type ListMemoriesArgs struct {
			MemoryType string `json:"memory_type"`
		}
		var lArgs ListMemoriesArgs
		_ = json.Unmarshal(args.Arguments, &lArgs) // ignore err, it is optional

		articles, err := srv.Storage.ListArticles()
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: err.Error()}}}, nil
		}

		filterType := strings.ToLower(strings.TrimSpace(lArgs.MemoryType))

		var text string
		count := 0
		for _, artMeta := range articles {
			art, err := srv.Storage.GetArticle(artMeta.Slug)
			if err != nil {
				continue
			}

			if art.Type != ContentTypeMemory {
				continue
			}
			// Scope filtering is by the memory-<scope> tag facet.
			memoryTags := memoryScopeTags(art.Tags)
			matchFilter := filterType == ""
			if filterType != "" {
				wantTag := MemoryScopeTagPrefix + filterType
				for _, tag := range memoryTags {
					if strings.ToLower(tag) == wantTag {
						matchFilter = true
						break
					}
				}
			}

			if matchFilter {
				count++
				if count == 1 {
					text = "AI Agent Memories Index:\n\n"
				}
				text += fmt.Sprintf("[%d] %s (Slug: %s, Edited: %s)\n",
					count, art.Title, art.Slug, art.Timestamp.Format("2006-01-02 15:04:05"))
				if art.Description != "" {
					text += fmt.Sprintf("    Summary: %s\n", art.Description)
				}
				text += fmt.Sprintf("    Tags: %s\n\n", strings.Join(memoryTags, ", "))
			}
		}

		if count == 0 {
			if filterType != "" {
				text = fmt.Sprintf("No AI Agent memories found of type '%s'.\n", filterType)
			} else {
				text = "No AI Agent memories found inside the knowledge base.\n"
			}
		}

		return ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}, nil

	case "create_agent_plan":
		type CreatePlanArgs struct {
			Title          string `json:"title"`
			Content        string `json:"content"`
			ProjectContext string `json:"project_context"`
			Description    string `json:"description"`
			Source         string `json:"source"`
			EditSummary    string `json:"edit_summary"`
		}
		var pArgs CreatePlanArgs
		if err := json.Unmarshal(args.Arguments, &pArgs); err != nil || pArgs.Title == "" || pArgs.Content == "" || pArgs.ProjectContext == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'title', 'content', and 'project_context' are required."}
		}

		title := pArgs.Title
		slug := Slugify(title)

		// The OKF type carries the plan class; tags hold only the project context + status.
		var tags []string
		projCtx := strings.TrimSpace(pArgs.ProjectContext)
		if projCtx != "" {
			if contextTag := Slugify(projCtx); contextTag != "" {
				tags = append(tags, contextTag)
			}
		}

		if _, err := srv.Storage.GetArticle(slug); err == nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: a plan with slug '%s' already exists", slug)}}}, nil
		}

		summary := pArgs.EditSummary
		if summary == "" {
			summary = "Created Collaborative AI Plan"
		}

		art, err := srv.Storage.SaveArticle("", title, pArgs.Content, pArgs.Description, pArgs.Source, "", summary, tags, ContentTypePlan)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error creating agent plan: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Collaborative AI Plan '%s' created successfully.\nSlug: %s\nCreated At: %s\nVersion: %d\nTags: %s\n",
			art.Title, art.Slug, art.CreatedAt.Format(time.RFC3339), art.Version, strings.Join(art.Tags, ", "))
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "append_agent_plan":
		type AppendPlanArgs struct {
			Slug            string `json:"slug"`
			ContentToAppend string `json:"content_to_append"`
			EditSummary     string `json:"edit_summary"`
		}
		var aArgs AppendPlanArgs
		if err := json.Unmarshal(args.Arguments, &aArgs); err != nil || aArgs.Slug == "" || aArgs.ContentToAppend == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'slug' and 'content_to_append' are required."}
		}

		existing, err := srv.Storage.GetArticle(aArgs.Slug)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: plan with slug '%s' not found", aArgs.Slug)}}}, nil
		}

		if existing.Type != ContentTypePlan {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: target article is not a Collaborative AI Plan (type must be AI-Agent-Plan)."}}}, nil
		}

		newContent := existing.Content + "\n\n" + aArgs.ContentToAppend

		summary := aArgs.EditSummary
		if summary == "" {
			summary = "Appended Collaborative AI Plan details"
		}

		art, err := srv.Storage.SaveArticle(existing.Slug, existing.Title, newContent, existing.Description, existing.Source, existing.Resource, summary, existing.Tags, existing.Type)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error appending agent plan: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Appended plan details to '%s' (version: %d, edited: %s).\n",
			art.Title, art.Version, art.Timestamp.Format(time.RFC3339))
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "edit_agent_plan":
		type EditPlanArgs struct {
			Slug          string    `json:"slug"`
			Title         *string   `json:"title,omitempty"`
			Content       *string   `json:"content,omitempty"`
			Tags          *[]string `json:"tags,omitempty"`
			LoadedVersion int       `json:"loaded_version"`
			EditSummary   string    `json:"edit_summary"`
		}
		var eArgs EditPlanArgs
		if err := json.Unmarshal(args.Arguments, &eArgs); err != nil || eArgs.Slug == "" || eArgs.LoadedVersion <= 0 {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'slug' and positive 'loaded_version' are required."}
		}

		existing, err := srv.Storage.GetArticle(eArgs.Slug)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: plan with slug '%s' not found", eArgs.Slug)}}}, nil
		}

		if existing.Type != ContentTypePlan {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: target article is not a Collaborative AI Plan (type must be AI-Agent-Plan)."}}}, nil
		}

		if existing.Version > 0 && existing.Version != eArgs.LoadedVersion {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: Version conflict! The plan was updated by another session. Disk version is %d, but you loaded version %d. Re-fetch the plan and try again.", existing.Version, eArgs.LoadedVersion)}}}, nil
		}

		newTitle := existing.Title
		if eArgs.Title != nil {
			newTitle = strings.TrimSpace(*eArgs.Title)
			if newTitle == "" {
				return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: title cannot be empty"}}}, nil
			}
		}

		newContent := existing.Content
		if eArgs.Content != nil {
			if strings.TrimSpace(*eArgs.Content) == "" {
				return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: content cannot be empty. Use 'delete_wiki_article' to remove a plan entirely."}}}, nil
			}
			newContent = *eArgs.Content
		}

		newTags := existing.Tags
		if eArgs.Tags != nil {
			// The plan class lives in the OKF type; tags are freely settable (project + status).
			var parsedTags []string
			seen := make(map[string]bool)
			for _, tag := range *eArgs.Tags {
				cleanTag := Slugify(tag)
				if cleanTag != "" && !seen[cleanTag] {
					seen[cleanTag] = true
					parsedTags = append(parsedTags, cleanTag)
				}
			}
			newTags = parsedTags
		}

		summary := eArgs.EditSummary
		if summary == "" {
			summary = "Updated Collaborative AI Plan"
		}

		art, err := srv.Storage.SaveArticle(existing.Slug, newTitle, newContent, existing.Description, existing.Source, existing.Resource, summary, newTags, existing.Type)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error editing agent plan: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Collaborative AI Plan '%s' updated successfully.\nSlug: %s\nNew Version: %d\nLast Edited: %s\nTags: %s\n",
			art.Title, art.Slug, art.Version, art.Timestamp.Format(time.RFC3339), strings.Join(art.Tags, ", "))
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "list_agent_plans":
		type ListPlansArgs struct {
			ProjectContext string `json:"project_context"`
			Tag            string `json:"tag"`
		}
		var lArgs ListPlansArgs
		_ = json.Unmarshal(args.Arguments, &lArgs) // ignore err, it is optional

		articles, err := srv.Storage.ListArticles()
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: err.Error()}}}, nil
		}

		filterProj := Slugify(strings.TrimSpace(lArgs.ProjectContext))
		filterTag := strings.ToLower(strings.TrimSpace(lArgs.Tag))

		var text string
		count := 0
		for _, artMeta := range articles {
			art, err := srv.Storage.GetArticle(artMeta.Slug)
			if err != nil {
				continue
			}

			if art.Type != ContentTypePlan {
				continue
			}
			matchProjFilter := filterProj == ""
			matchTagFilter := filterTag == ""

			for _, tag := range art.Tags {
				tagLower := strings.ToLower(tag)
				if filterProj != "" && tagLower == filterProj {
					matchProjFilter = true
				}
				if filterTag != "" && tagLower == filterTag {
					matchTagFilter = true
				}
			}

			if matchProjFilter && matchTagFilter {
				count++
				if count == 1 {
					text = "Collaborative AI Plans Index:\n\n"
				}
				text += fmt.Sprintf("[%d] %s (Slug: %s, Edited: %s)\n",
					count, art.Title, art.Slug, art.Timestamp.Format("2006-01-02 15:04:05"))
				if art.Description != "" {
					text += fmt.Sprintf("    Summary: %s\n", art.Description)
				}
				text += fmt.Sprintf("    Tags: %s\n\n", strings.Join(art.Tags, ", "))
			}
		}

		if count == 0 {
			if filterProj != "" && filterTag != "" {
				text = fmt.Sprintf("No Collaborative AI Plans found for project '%s' with tag '%s'.\n", lArgs.ProjectContext, lArgs.Tag)
			} else if filterProj != "" {
				text = fmt.Sprintf("No Collaborative AI Plans found for project '%s'.\n", lArgs.ProjectContext)
			} else if filterTag != "" {
				text = fmt.Sprintf("No Collaborative AI Plans found with tag '%s'.\n", lArgs.Tag)
			} else {
				text = "No Collaborative AI Plans found inside the knowledge base.\n"
			}
		}

		return ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}, nil

	case "create_agent_skill":
		type CreateSkillArgs struct {
			Title       string   `json:"title"`
			Content     string   `json:"content"`
			Description string   `json:"description"`
			Source      string   `json:"source"`
			Tags        []string `json:"tags"`
			EditSummary string   `json:"edit_summary"`
		}
		var sArgs CreateSkillArgs
		if err := json.Unmarshal(args.Arguments, &sArgs); err != nil || sArgs.Title == "" || sArgs.Content == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'title' and 'content' are required."}
		}

		title := sArgs.Title
		slug := Slugify(title)

		// The OKF type carries the skill class; only free user/status tags ride here.
		tags := validateAndCleanUserTags(sArgs.Tags, nil)

		if _, err := srv.Storage.GetArticle(slug); err == nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: a skill with slug '%s' already exists", slug)}}}, nil
		}

		summary := sArgs.EditSummary
		if summary == "" {
			summary = "Created Custom AI Agent Skill"
		}

		art, err := srv.Storage.SaveArticle("", title, sArgs.Content, sArgs.Description, sArgs.Source, "", summary, tags, ContentTypeSkill)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error creating agent skill: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Custom AI Skill '%s' created successfully.\nSlug: %s\nCreated At: %s\nVersion: %d\nTags: %s\n",
			art.Title, art.Slug, art.CreatedAt.Format(time.RFC3339), art.Version, strings.Join(art.Tags, ", "))
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "list_agent_skills":
		articles, err := srv.Storage.ListArticles()
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: err.Error()}}}, nil
		}

		var text string
		count := 0
		for _, artMeta := range articles {
			art, err := srv.Storage.GetArticle(artMeta.Slug)
			if err != nil {
				continue
			}

			if art.Type == ContentTypeSkill {
				count++
				if count == 1 {
					text = "Custom AI Agent Skills Index:\n\n"
				}
				text += fmt.Sprintf("[%d] %s (Slug: %s, Edited: %s)\n",
					count, art.Title, art.Slug, art.Timestamp.Format("2006-01-02 15:04:05"))
				if art.Description != "" {
					text += fmt.Sprintf("    Summary: %s\n", art.Description)
				}
				text += fmt.Sprintf("    Tags: %s\n\n", strings.Join(art.Tags, ", "))
			}
		}

		if count == 0 {
			text = "No Custom AI Agent Skills found inside the knowledge base.\n"
		}

		return ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}, nil

	case "get_status_tags":
		text := "NexWiki Status Tags\n\nThe following tags indicate the lifecycle state of a wiki article or AI plan.\nApply them when creating or editing content to signal its current status.\nStatus tags are displayed with highest priority on the home dashboard.\n\nRecognized status tags:\n"
		for _, tag := range StatusTags {
			text += fmt.Sprintf("  • %s\n", tag)
		}
		text += "\nTips:\n"
		text += "  • Use 'list_agent_plans' with the 'tag' parameter to filter plans by status (e.g. tag: \"completed\").\n"
		text += "  • When a plan is fully implemented, use 'append_agent_plan' to add final notes, then use 'edit_agent_plan' to add the 'completed' status tag.\n"
		text += "  • The reserved AI-Agent-Plan type must NEVER be relabelled.\n"
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}, nil

	case "get_recent_activity":
		type ActivityArgs struct {
			Since  string `json:"since"`
			Limit  int    `json:"limit"`
			Action string `json:"action"`
			Source string `json:"source"`
		}
		var aArgs ActivityArgs
		_ = json.Unmarshal(args.Arguments, &aArgs) // all args optional

		var since time.Time
		if s := strings.TrimSpace(aArgs.Since); s != "" {
			if dur, err := time.ParseDuration(s); err == nil {
				since = time.Now().Add(-dur)
			} else if ts, err := time.Parse(time.RFC3339, s); err == nil {
				since = ts
			} else {
				return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: invalid 'since' value '%s'. Use a Go duration (e.g. '24h') or an RFC3339 timestamp.", s)}}}, nil
			}
		}

		limit := aArgs.Limit
		if limit <= 0 {
			limit = 50
		}
		if limit > 500 {
			limit = 500
		}

		events, err := ReadActivityLog(ActivityLogPath(srv.Storage.DataDir), since, limit, aArgs.Action, aArgs.Source)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error reading activity log: %v", err)}}}, nil
		}

		// Fall back to the in-memory ring buffer when no durable log exists yet
		if events == nil && srv.EventBus != nil {
			for _, ev := range srv.EventBus.GetHistory() {
				if !since.IsZero() && ev.Timestamp.Before(since) {
					continue
				}
				if aArgs.Action != "" && ev.Action != aArgs.Action {
					continue
				}
				if aArgs.Source != "" && ev.Source != aArgs.Source {
					continue
				}
				events = append(events, ev)
			}
			if len(events) > limit {
				events = events[len(events)-limit:]
			}
		}

		var text string
		if len(events) == 0 {
			text = "No activity events found for the given filters.\n"
		} else {
			text = fmt.Sprintf("Recent wiki activity (%d events, oldest first):\n\n", len(events))
			for _, ev := range events {
				toolStr := ev.Tool
				if toolStr == "" {
					toolStr = "web-ui"
				}
				line := fmt.Sprintf("%s [%s/%s] %s", ev.Timestamp.Format("2006-01-02 15:04:05"), ev.Source, ev.Action, toolStr)
				if ev.Title != "" || ev.Slug != "" {
					line += fmt.Sprintf(" → '%s' (%s)", ev.Title, ev.Slug)
				}
				if ev.Agent != "" {
					line += " by " + ev.Agent
				}
				text += line + "\n"
			}
		}

		return ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}, nil

	case "get_backlinks":
		type BacklinkArgs struct {
			Slug string `json:"slug"`
		}
		var bArgs BacklinkArgs
		if err := json.Unmarshal(args.Arguments, &bArgs); err != nil || bArgs.Slug == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'slug' argument"}
		}

		target, err := srv.Storage.GetArticle(bArgs.Slug)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: article with slug '%s' not found", bArgs.Slug)}}}, nil
		}

		backlinks, err := srv.Storage.GetBacklinks(target.Slug)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error scanning backlinks: %v", err)}}}, nil
		}

		var text string
		if len(backlinks) == 0 {
			text = fmt.Sprintf("No articles link to '%s'.\n", target.Slug)
		} else {
			text = fmt.Sprintf("Articles linking to '%s' (%d):\n\n", target.Slug, len(backlinks))
			for i, bl := range backlinks {
				text += fmt.Sprintf("[%d] %s (Slug: %s, Updated: %s)\n", i+1, bl.Title, bl.Slug, bl.Timestamp.Format("2006-01-02 15:04:05"))
				if bl.Description != "" {
					text += fmt.Sprintf("    Summary: %s\n", bl.Description)
				}
			}
		}

		return ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}, nil

	case "get_context_overview":
		type OverviewArgs struct {
			Type string `json:"type"`
		}
		var oArgs OverviewArgs
		_ = json.Unmarshal(args.Arguments, &oArgs) // optional args

		filter := strings.ToLower(strings.TrimSpace(oArgs.Type))
		sections := []struct {
			dir    string
			label  string
			filter string
		}{
			{"wiki", "Wiki Articles", "articles"},
			{"aimemories", "Agent Memories", "memories"},
			{"aiplans", "Agent Plans", "plans"},
			{"aiskills", "Agent Skills", "skills"},
		}

		validFilter := filter == ""
		for _, sec := range sections {
			if filter == sec.filter {
				validFilter = true
			}
		}
		if !validFilter {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: invalid 'type' filter '%s'. Valid values: articles, memories, plans, skills.", oArgs.Type)}}}, nil
		}

		articles, err := srv.Storage.ListArticles()
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: err.Error()}}}, nil
		}

		grouped := make(map[string][]Article)
		for _, art := range articles {
			dir := getArticleDirectory(art.Type)
			grouped[dir] = append(grouped[dir], art)
		}

		text := fmt.Sprintf("NexWiki Context Overview (%d articles total)\n", len(articles))
		text += "Each line: Title (slug) — summary [tags] (updated). Use read_article(slug) to load full content.\n\n"
		for _, sec := range sections {
			if filter != "" && filter != sec.filter {
				continue
			}
			entries := grouped[sec.dir]
			text += fmt.Sprintf("== %s (%d) ==\n", sec.label, len(entries))
			for _, art := range entries {
				summary := art.Description
				if summary == "" {
					summary = art.ContentPreview
				}
				line := fmt.Sprintf("- %s (%s)", art.Title, art.Slug)
				if summary != "" {
					line += " — " + summary
				}
				if len(art.Tags) > 0 {
					line += fmt.Sprintf(" [%s]", strings.Join(art.Tags, ", "))
				}
				line += fmt.Sprintf(" (updated %s)", art.Timestamp.Format("2006-01-02"))
				text += line + "\n"
			}
			text += "\n"
		}

		return ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}, nil

	case "get_article_history":
		type HistArgs struct {
			Slug string `json:"slug"`
		}
		var hArgs HistArgs
		if err := json.Unmarshal(args.Arguments, &hArgs); err != nil || hArgs.Slug == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'slug' argument"}
		}

		history, err := srv.Storage.GetArticleHistory(hArgs.Slug)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error loading history for '%s': %v", hArgs.Slug, err)}}}, nil
		}

		var respText string
		if len(history) == 0 {
			respText = fmt.Sprintf("No historical versions found for article '%s'\n", hArgs.Slug)
		} else {
			respText = fmt.Sprintf("Revision History for '%s' (%d versions):\n\n", hArgs.Slug, len(history))
			for _, ver := range history {
				respText += fmt.Sprintf("Version: %d | Edited: %s\n", ver.Version, ver.Timestamp.Format(time.RFC3339))
				if ver.EditSummary != "" {
					respText += fmt.Sprintf("  Summary: %s\n", ver.EditSummary)
				}
				respText += "\n"
			}
		}

		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "revert_article_version":
		type RevArgs struct {
			Slug    string `json:"slug"`
			Version int    `json:"version"`
		}
		var rArgs RevArgs
		if err := json.Unmarshal(args.Arguments, &rArgs); err != nil || rArgs.Slug == "" || rArgs.Version <= 0 {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. Requires 'slug' and positive 'version'"}
		}

		art, err := srv.Storage.RevertArticle(rArgs.Slug, rArgs.Version)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Revert failed: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Article '%s' reverted successfully to version %d.\nNew active version: %d\nLast Edited: %s\n",
			art.Title, rArgs.Version, art.Version, art.Timestamp.Format(time.RFC3339))
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "get_wiki_statistics":
		articles, err := srv.Storage.ListArticles()
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: err.Error()}}}, nil
		}

		var fullArticles []*Article
		var activeSlugs = make(map[string]bool)
		activeSlugs["home"] = true // Implicitly exists

		for _, artMeta := range articles {
			art, err := srv.Storage.GetArticle(artMeta.Slug)
			if err == nil {
				fullArticles = append(fullArticles, art)
				activeSlugs[art.Slug] = true
			}
		}

		type BrokenLink struct {
			FromSlug   string
			TargetLink string
		}
		var brokenLinks []BrokenLink
		totalLinks := 0

		for _, art := range fullArticles {
			for _, target := range ExtractWikiLinkTargets(art.Content) {
				totalLinks++
				if !activeSlugs[Slugify(target)] {
					brokenLinks = append(brokenLinks, BrokenLink{
						FromSlug:   art.Slug,
						TargetLink: target,
					})
				}
			}
		}

		var respText string
		respText = "NexWiki Knowledge Base Statistics:\n"
		respText += fmt.Sprintf("- Total Articles: %d\n", len(articles))
		respText += fmt.Sprintf("- Total WikiLinks Scanned: %d\n", totalLinks)
		respText += fmt.Sprintf("- Total Broken/Dead WikiLinks: %d\n\n", len(brokenLinks))

		if len(brokenLinks) == 0 {
			respText += "Excellent! All double-bracket WikiLinks are healthy and fully connected! 🎉\n"
		} else {
			respText += "Broken/Dead WikiLinks Detected (AI suggestion: create these pages to heal the wiki!):\n"
			for _, bl := range brokenLinks {
				respText += fmt.Sprintf("  - Link '[[%s]]' inside article '/articles/%s' (Target slug: '%s' is missing)\n",
					bl.TargetLink, bl.FromSlug, Slugify(bl.TargetLink))
			}
		}

		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "export_okf_bundle":
		data, err := srv.Storage.ExportOKFBundle()
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error exporting OKF bundle: %v", err)}}}, nil
		}
		fileName := fmt.Sprintf("okf-export-%s.zip", time.Now().UTC().Format("2006-01-02T15-04-05Z"))
		outPath := filepath.Join(srv.Storage.DataDir, fileName)
		if err := os.WriteFile(outPath, data, 0644); err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error writing OKF bundle to disk: %v", err)}}}, nil
		}
		respText := fmt.Sprintf("Success! Exported OKF v%s bundle (%d bytes) to:\n%s\n", OKFVersion, len(data), outPath)
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "import_okf_bundle":
		type ImportArgs struct {
			Path string `json:"path"`
		}
		var iArgs ImportArgs
		if err := json.Unmarshal(args.Arguments, &iArgs); err != nil || strings.TrimSpace(iArgs.Path) == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'path' argument"}
		}
		data, err := os.ReadFile(iArgs.Path)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error reading bundle at '%s': %v", iArgs.Path, err)}}}, nil
		}
		report, err := srv.Storage.ImportOKFBundle(data)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error importing OKF bundle: %v", err)}}}, nil
		}
		respText := fmt.Sprintf("OKF import complete: %d imported, %d skipped.\n", report.Imported, report.Skipped)
		if len(report.MissingType) > 0 {
			respText += fmt.Sprintf("Documents defaulted to Wiki (missing/unknown type): %s\n", strings.Join(report.MissingType, ", "))
		}
		for _, wmsg := range report.Warnings {
			respText += "Warning: " + wmsg + "\n"
		}
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	default:
		return nil, &JSONRPCError{
			Code:    -32601,
			Message: fmt.Sprintf("Tool not found: %s", args.Name),
		}
	}
}

// sendError sends standard formatted JSON-RPC error responses on standard out.
func sendError(w io.Writer, code int, msg string, id interface{}) {
	var resp JSONRPCResponse
	resp.JSONRPC = "2.0"
	resp.ID = id
	resp.Error = &JSONRPCError{
		Code:    code,
		Message: msg,
	}
	respBytes, err := json.Marshal(resp)
	if err == nil {
		_, _ = fmt.Fprintf(w, "%s\n", string(respBytes))
	}
}

// HandleStreamableHTTP implements the Streamable HTTP transport (2025 Spec)
// supporting GET (initiating SSE stream) and POST (synchronous JSON-RPC).
func (srv *Server) HandleStreamableHTTP(w http.ResponseWriter, r *http.Request) {
	// Enable CORS for remote clients
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, MCP-Protocol-Version, MCP-Session-Id")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method == http.MethodGet {
		// Verify accept header supports text/event-stream
		accept := r.Header.Get("Accept")
		if accept != "" && !strings.Contains(accept, "text/event-stream") {
			http.Error(w, "Accept header must support text/event-stream", http.StatusNotAcceptable)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Priming comment to flush connection
		_, _ = fmt.Fprint(w, ": keepalive\n\n")
		flusher.Flush()

		// Keep stream open with periodic keepalives
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		notify := r.Context().Done()
		for {
			select {
			case <-notify:
				return
			case <-ticker.C:
				_, _ = fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
			}
		}
	} else if r.Method == http.MethodPost {
		// Read body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		defer func() { _ = r.Body.Close() }()

		var req JSONRPCRequest
		if err := json.Unmarshal(body, &req); err != nil {
			// Send JSON-RPC Parse Error Response
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			sendError(w, -32700, "Parse error: invalid JSON", nil)
			return
		}

		// Set response headers
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		// Execute request synchronously and write response directly to the http response writer
		srv.handleRequest(w, &req)
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

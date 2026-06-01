package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "NexWiki MCP Server",
				"version": "1.0.0",
			},
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
					"description": "List all articles currently available inside your NexWiki knowledge base, showing their titles and URL slugs.",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
				{
					"name":        "create_wiki_article",
					"description": "Create a brand new wiki article with a given title, raw Markdown content body, and an optional revision edit summary. Automatically handles title slugification and checks for slug collision.",
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
					"description": "Modify the title, markdown content, or edit summary of an existing article. Employs optimistic locking to prevent concurrent overwrite collisions.",
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
					"name":        "delete_wiki_article",
					"description": "Permanently delete an existing wiki article and its historical backups from disk.",
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
					"description": "Create a brand new protected AI Agent Memory document (like a troubleshooting log, architecture decision, or custom rules). Automatically categorizes the page using special protected 'aiagent-memory-' tags. (Use create_agent_plan for plans and create_agent_skill for skills)",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"title": map[string]interface{}{
								"type":        "string",
								"description": "The human-readable title of the memory article (e.g. 'Build Server Outage Resolution').",
							},
							"content": map[string]interface{}{
								"type":        "string",
								"description": "The raw Markdown content of the memory document.",
							},
							"memory_type": map[string]interface{}{
								"type":        "string",
								"description": "The classification type of memory. Must be one of: troubleshooting, memory, decision, todo, rules.",
							},
							"project_context": map[string]interface{}{
								"type":        "string",
								"description": "Optional project identifier to apply a secondary contextual tag (e.g. 'project-x' generates the tag 'project-x').",
							},
							"edit_summary": map[string]interface{}{
								"type":        "string",
								"description": "Optional revision log description summarizing why this memory was created.",
							},
						},
						"required": []string{"title", "content", "memory_type"},
					},
				},
				{
					"name":        "append_agent_memory",
					"description": "Append logs, subtask completions, or troubleshooting observations to the end of an existing protected AI Agent Memory document (must have an 'aiagent-memory-' prefixed tag).",
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
					"name":        "list_agent_memories",
					"description": "List all protected AI Agent Memory documents (tagged with 'aiagent-memory-' prefixes) currently saved inside the knowledge base.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"memory_type": map[string]interface{}{
								"type":        "string",
								"description": "Optional memory type to filter the list (e.g., troubleshooting, memory, decision, todo, rules).",
							},
						},
					},
				},
				{
					"name":        "create_agent_plan",
					"description": "Create a brand new Collaborative AI Plan that can be collaboratively edited/viewed by both the user and the agent. Automatically tags the page with 'aiagent-plan'.",
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
					"description": "Append task status, observations, or checklists to an existing Collaborative AI Plan (must possess the 'aiagent-plan' tag).",
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
					"name":        "list_agent_plans",
					"description": "List all Collaborative AI Plans (tagged with 'aiagent-plan') saved inside the knowledge base.",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"project_context": map[string]interface{}{
								"type":        "string",
								"description": "Optional project context name to filter plans by.",
							},
						},
					},
				},
				{
					"name":        "create_agent_skill",
					"description": "Create a brand new Custom AI Skill. Automatically tags the page with 'aiagent-skill', making it part of the custom skills registry.",
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
							"tags": map[string]interface{}{
								"type": "array",
								"items": map[string]interface{}{
									"type": "string",
								},
								"description": "Optional user tags to apply to the skill.",
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
					"description": "List all Custom AI Skills (tagged with 'aiagent-skill') currently saved in the knowledge base.",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
			},
		}

	case "tools/call":
		result, rpcErr = srv.executeToolCall(req.Params)

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

// executeToolCall parses parameters and executes requested MCP tools.
func (srv *Server) executeToolCall(params json.RawMessage) (interface{}, *JSONRPCError) {
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

		// Return both front-matter configurations and full Markdown content to the agent
		text := fmt.Sprintf("Title: %s\nSlug: %s\nCreated: %s\nUpdated: %s%s\n\n%s",
			art.Title, art.Slug, art.CreatedAt.Format(time.RFC3339), art.UpdatedAt.Format(time.RFC3339), tagsStr, art.Content)

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
				tagsStr := ""
				if len(art.Tags) > 0 {
					tagsStr = fmt.Sprintf(" | Tags: %s", strings.Join(art.Tags, ", "))
				}
				text += fmt.Sprintf("[%d] %s (Slug: %s, Last Edited: %s%s)\n",
					i+1, art.Title, art.Slug, art.UpdatedAt.Format("2006-01-02 15:04:05"), tagsStr)
			}
		}

		return ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}, nil

	case "create_wiki_article":
		type CreateArgs struct {
			Title       string `json:"title"`
			Content     string `json:"content"`
			EditSummary string `json:"edit_summary"`
		}
		var cArgs CreateArgs
		if err := json.Unmarshal(args.Arguments, &cArgs); err != nil || cArgs.Title == "" || cArgs.Content == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'title' or 'content' arguments"}
		}

		slug := Slugify(cArgs.Title)
		if _, err := srv.Storage.GetArticle(slug); err == nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: an article with title '%s' (slug: '%s') already exists", cArgs.Title, slug)}}}, nil
		}

		art, err := srv.Storage.SaveArticle("", cArgs.Title, cArgs.Content, cArgs.EditSummary, nil)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error creating article: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Article '%s' created successfully.\nSlug: %s\nCreated At: %s\nVersion: %d\n",
			art.Title, art.Slug, art.CreatedAt.Format(time.RFC3339), art.Version)
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "edit_wiki_article":
		type EditArgs struct {
			Slug          string `json:"slug"`
			Title         string `json:"title"`
			Content       string `json:"content"`
			LoadedVersion int    `json:"loaded_version"`
			EditSummary   string `json:"edit_summary"`
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

		art, err := srv.Storage.SaveArticle(eArgs.Slug, eArgs.Title, eArgs.Content, eArgs.EditSummary, existing.Tags)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error editing article: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Article '%s' (slug: %s) updated successfully.\nNew Version: %d\nLast Edited: %s\n",
			art.Title, art.Slug, art.Version, art.UpdatedAt.Format(time.RFC3339))
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "delete_wiki_article":
		type DelArgs struct {
			Slug string `json:"slug"`
		}
		var dArgs DelArgs
		if err := json.Unmarshal(args.Arguments, &dArgs); err != nil || dArgs.Slug == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'slug' argument"}
		}

		if _, err := srv.Storage.GetArticle(dArgs.Slug); err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: article with slug '%s' not found", dArgs.Slug)}}}, nil
		}

		err := srv.Storage.DeleteArticle(dArgs.Slug)
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
			EditSummary    string `json:"edit_summary"`
		}
		var mArgs CreateMemoryArgs
		if err := json.Unmarshal(args.Arguments, &mArgs); err != nil || mArgs.Title == "" || mArgs.Content == "" || mArgs.MemoryType == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'title', 'content', and 'memory_type' are required."}
		}

		mType := strings.ToLower(strings.TrimSpace(mArgs.MemoryType))
		validTypes := map[string]bool{
			"troubleshooting": true,
			"memory":          true,
			"decision":        true,
			"todo":            true,
			"rules":           true,
		}
		if !validTypes[mType] {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: invalid memory_type '%s'. Valid types are: troubleshooting, memory, decision, todo, rules", mArgs.MemoryType)}}}, nil
		}

		title := mArgs.Title
		slug := Slugify(title)

		primaryTag := "aiagent-memory-" + mType
		tags := []string{primaryTag}

		projCtx := strings.TrimSpace(mArgs.ProjectContext)
		if projCtx != "" {
			contextTag := Slugify(projCtx)
			if contextTag != "" && contextTag != primaryTag {
				tags = append(tags, contextTag)
			}
		}

		if _, err := srv.Storage.GetArticle(slug); err == nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: an article with slug '%s' already exists", slug)}}}, nil
		}

		summary := mArgs.EditSummary
		if summary == "" {
			summary = fmt.Sprintf("Created AI Agent %s Memory", mType)
		}

		art, err := srv.Storage.SaveArticle("", title, mArgs.Content, summary, tags)
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

		hasAgentMemoryTag := false
		for _, tag := range existing.Tags {
			if strings.HasPrefix(strings.ToLower(tag), "aiagent-memory-") {
				hasAgentMemoryTag = true
				break
			}
		}
		if !hasAgentMemoryTag {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: target article is not a protected AI agent memory (must be tagged with an 'aiagent-memory-' prefix)."}}}, nil
		}

		newContent := existing.Content + "\n\n" + aArgs.ContentToAppend

		summary := aArgs.EditSummary
		if summary == "" {
			summary = "Appended AI Agent memory details"
		}

		art, err := srv.Storage.SaveArticle(existing.Slug, existing.Title, newContent, summary, existing.Tags)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error appending agent memory: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Appended memory details to '%s' (version: %d, edited: %s).\n",
			art.Title, art.Version, art.UpdatedAt.Format(time.RFC3339))
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

			isAgentMemory := false
			matchFilter := filterType == ""
			var memoryTags []string

			for _, tag := range art.Tags {
				tagLower := strings.ToLower(tag)
				if strings.HasPrefix(tagLower, "aiagent-memory-") {
					isAgentMemory = true
					memoryTags = append(memoryTags, tag)
					if filterType != "" && strings.HasPrefix(tagLower, "aiagent-memory-"+filterType) {
						matchFilter = true
					}
				}
			}

			if isAgentMemory && matchFilter {
				count++
				if count == 1 {
					text = "AI Agent Memories Index:\n\n"
				}
				text += fmt.Sprintf("[%d] %s (Slug: %s, Edited: %s)\n",
					count, art.Title, art.Slug, art.UpdatedAt.Format("2006-01-02 15:04:05"))
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
			EditSummary    string `json:"edit_summary"`
		}
		var pArgs CreatePlanArgs
		if err := json.Unmarshal(args.Arguments, &pArgs); err != nil || pArgs.Title == "" || pArgs.Content == "" || pArgs.ProjectContext == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'title', 'content', and 'project_context' are required."}
		}

		title := pArgs.Title
		slug := Slugify(title)

		tags := []string{"aiagent-plan"}
		projCtx := strings.TrimSpace(pArgs.ProjectContext)
		if projCtx != "" {
			contextTag := Slugify(projCtx)
			if contextTag != "" && contextTag != "aiagent-plan" {
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

		art, err := srv.Storage.SaveArticle("", title, pArgs.Content, summary, tags)
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

		hasPlanTag := false
		for _, tag := range existing.Tags {
			if strings.ToLower(tag) == "aiagent-plan" {
				hasPlanTag = true
				break
			}
		}
		if !hasPlanTag {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: target article is not a Collaborative AI Plan (must possess the 'aiagent-plan' tag)."}}}, nil
		}

		newContent := existing.Content + "\n\n" + aArgs.ContentToAppend

		summary := aArgs.EditSummary
		if summary == "" {
			summary = "Appended Collaborative AI Plan details"
		}

		art, err := srv.Storage.SaveArticle(existing.Slug, existing.Title, newContent, summary, existing.Tags)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error appending agent plan: %v", err)}}}, nil
		}

		respText := fmt.Sprintf("Success! Appended plan details to '%s' (version: %d, edited: %s).\n",
			art.Title, art.Version, art.UpdatedAt.Format(time.RFC3339))
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil

	case "list_agent_plans":
		type ListPlansArgs struct {
			ProjectContext string `json:"project_context"`
		}
		var lArgs ListPlansArgs
		_ = json.Unmarshal(args.Arguments, &lArgs) // ignore err, it is optional

		articles, err := srv.Storage.ListArticles()
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: err.Error()}}}, nil
		}

		filterProj := Slugify(strings.TrimSpace(lArgs.ProjectContext))

		var text string
		count := 0
		for _, artMeta := range articles {
			art, err := srv.Storage.GetArticle(artMeta.Slug)
			if err != nil {
				continue
			}

			isPlan := false
			matchFilter := filterProj == ""

			for _, tag := range art.Tags {
				tagLower := strings.ToLower(tag)
				if tagLower == "aiagent-plan" {
					isPlan = true
				}
				if filterProj != "" && tagLower == filterProj {
					matchFilter = true
				}
			}

			if isPlan && matchFilter {
				count++
				if count == 1 {
					text = "Collaborative AI Plans Index:\n\n"
				}
				text += fmt.Sprintf("[%d] %s (Slug: %s, Edited: %s)\n",
					count, art.Title, art.Slug, art.UpdatedAt.Format("2006-01-02 15:04:05"))
				text += fmt.Sprintf("    Tags: %s\n\n", strings.Join(art.Tags, ", "))
			}
		}

		if count == 0 {
			if filterProj != "" {
				text = fmt.Sprintf("No Collaborative AI Plans found for project '%s'.\n", lArgs.ProjectContext)
			} else {
				text = "No Collaborative AI Plans found inside the knowledge base.\n"
			}
		}

		return ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}, nil

	case "create_agent_skill":
		type CreateSkillArgs struct {
			Title       string   `json:"title"`
			Content     string   `json:"content"`
			Tags        []string `json:"tags"`
			EditSummary string   `json:"edit_summary"`
		}
		var sArgs CreateSkillArgs
		if err := json.Unmarshal(args.Arguments, &sArgs); err != nil || sArgs.Title == "" || sArgs.Content == "" {
			return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'title' and 'content' are required."}
		}

		title := sArgs.Title
		slug := Slugify(title)

		tags := []string{"aiagent-skill"}
		for _, t := range sArgs.Tags {
			tTrimmed := strings.TrimSpace(t)
			if tTrimmed != "" {
				tagLower := strings.ToLower(tTrimmed)
				if tagLower != "aiagent-skill" && !strings.HasPrefix(tagLower, "aiagent-") {
					tags = append(tags, tTrimmed)
				}
			}
		}

		if _, err := srv.Storage.GetArticle(slug); err == nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: a skill with slug '%s' already exists", slug)}}}, nil
		}

		summary := sArgs.EditSummary
		if summary == "" {
			summary = "Created Custom AI Agent Skill"
		}

		art, err := srv.Storage.SaveArticle("", title, sArgs.Content, summary, tags)
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

			isSkill := false
			for _, tag := range art.Tags {
				if strings.ToLower(tag) == "aiagent-skill" {
					isSkill = true
					break
				}
			}

			if isSkill {
				count++
				if count == 1 {
					text = "Custom AI Agent Skills Index:\n\n"
				}
				text += fmt.Sprintf("[%d] %s (Slug: %s, Edited: %s)\n",
					count, art.Title, art.Slug, art.UpdatedAt.Format("2006-01-02 15:04:05"))
				text += fmt.Sprintf("    Tags: %s\n\n", strings.Join(art.Tags, ", "))
			}
		}

		if count == 0 {
			text = "No Custom AI Agent Skills found inside the knowledge base.\n"
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
				respText += fmt.Sprintf("Version: %d | Edited: %s\n", ver.Version, ver.UpdatedAt.Format(time.RFC3339))
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
			art.Title, rArgs.Version, art.Version, art.UpdatedAt.Format(time.RFC3339))
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
			content := art.Content
			for {
				startIdx := strings.Index(content, "[[")
				if startIdx == -1 {
					break
				}
				endIdx := strings.Index(content[startIdx:], "]]")
				if endIdx == -1 {
					break
				}
				endIdx += startIdx

				linkContent := content[startIdx+2 : endIdx]
				content = content[endIdx+2:]

				totalLinks++

				target := linkContent
				pipeIdx := strings.Index(linkContent, "|")
				if pipeIdx != -1 {
					target = linkContent[:pipeIdx]
				}
				target = strings.TrimSpace(target)

				targetSlug := Slugify(target)
				if !activeSlugs[targetSlug] {
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

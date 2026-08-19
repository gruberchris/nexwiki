package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// This file holds the AI agent memory lifecycle tools.
// Each tool pairs its JSON schema with its handler in one place, so the two can never
// drift apart. Registration order lives in mcp_tools.go.

var createAgentMemoryTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "create_agent_memory",
		"description": "Create a brand new protected AI Agent Memory document. The 'memory_type' controls the tag applied and how the memory is scoped: use the project name (e.g. 'nexwiki') for project-specific knowledge, a topic name (e.g. 'docker') for reusable cross-project knowledge, or omit it for general knowledge (no scope tag). Memories must be succinct and high-value — they are loaded into agent context windows, so keep them short, specific, and free of repetition. Search for an existing memory first; if one becomes stale later, use 'edit_agent_memory' to correct it or 'delete_agent_memory' to retire it rather than creating near-duplicates. The reserved AI-Agent-Memory type must NEVER be relabelled unless explicitly instructed. (IMPORTANT: If you have not already loaded the global operational guidelines skill this session, load it once with 'read_article(slug: \"nexwiki-agent-guidelines\")'. If it is already in your context, do not re-read it — call this tool.)",
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
				"tags": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Optional status or user tags to apply. Call get_status_tags to see the recognized status values (e.g. 'draft', 'review'). The tool-managed 'memory-<memory_type>' scope tag is added automatically and cannot be set here.",
				},
				"edit_summary": map[string]interface{}{
					"type":        "string",
					"description": "Optional revision log description summarizing why this memory was created.",
				},
			},
			"required": []string{"title", "content"},
		},
	},
	Handler:  (*Server).toolCreateAgentMemory,
	Behavior: toolBehavior{Title: "Create Agent Memory", Destructive: false, Idempotent: false},
}

func (srv *Server) toolCreateAgentMemory(args json.RawMessage) (interface{}, *JSONRPCError) {
	type CreateMemoryArgs struct {
		Title          string   `json:"title"`
		Content        string   `json:"content"`
		MemoryType     string   `json:"memory_type"`
		ProjectContext string   `json:"project_context"`
		Description    string   `json:"description"`
		Source         string   `json:"source"`
		Tags           []string `json:"tags"`
		EditSummary    string   `json:"edit_summary"`
	}
	var mArgs CreateMemoryArgs
	if e := decodeToolArgs(args, &mArgs); e != nil {
		return nil, e
	}
	if mArgs.Title == "" || mArgs.Content == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'title' and 'content' are required."}
	}
	if resp := rejectToolArtifactTitle(mArgs.Title, "memory"); resp != nil {
		return *resp, nil
	}

	mType := strings.ToLower(strings.TrimSpace(mArgs.MemoryType))

	// The OKF type carries the memory document class; the scope facet rides as a
	// tool-managed memory-<scope> tag. A bare memory (no scope) carries no scope tag.
	var scopeTags []string
	if mType != "" {
		scopeTags = []string{MemoryScopeTagPrefix + Slugify(mType)}
	}

	// Caller tags are merged on top of the tool-managed scope tag, sanitized through the same
	// helper the REST path uses: the scope tag is re-asserted first so it cannot be displaced, and
	// a caller cannot forge a memory-<scope> tag of its own.
	tags := validateAndCleanUserTags(mArgs.Tags, scopeTags, ContentTypeMemory)

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
}

var appendAgentMemoryTool = toolDef{
	Schema: map[string]interface{}{
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
	Handler:  (*Server).toolAppendAgentMemory,
	Behavior: toolBehavior{Title: "Append to Agent Memory", Destructive: false, Idempotent: false},
}

func (srv *Server) toolAppendAgentMemory(args json.RawMessage) (interface{}, *JSONRPCError) {
	type AppendMemoryArgs struct {
		Slug            string `json:"slug"`
		ContentToAppend string `json:"content_to_append"`
		EditSummary     string `json:"edit_summary"`
	}
	var aArgs AppendMemoryArgs
	if e := decodeToolArgs(args, &aArgs); e != nil {
		return nil, e
	}
	if aArgs.Slug == "" || aArgs.ContentToAppend == "" {
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
}

var editAgentMemoryTool = toolDef{
	Schema: map[string]interface{}{
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
	Handler:  (*Server).toolEditAgentMemory,
	Behavior: toolBehavior{Title: "Edit Agent Memory", Destructive: true, Idempotent: false},
}

func (srv *Server) toolEditAgentMemory(args json.RawMessage) (interface{}, *JSONRPCError) {
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
	if e := decodeToolArgs(args, &eArgs); e != nil {
		return nil, e
	}
	if eArgs.Slug == "" || eArgs.LoadedVersion <= 0 {
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
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: versionConflictMessage("memory", eArgs.Slug, existing.Version, eArgs.LoadedVersion)}}}, nil
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
}

var deleteAgentMemoryTool = toolDef{
	Schema: map[string]interface{}{
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
	Handler:  (*Server).toolDeleteAgentMemory,
	Behavior: toolBehavior{Title: "Delete Agent Memory", Destructive: true, Idempotent: true},
}

func (srv *Server) toolDeleteAgentMemory(args json.RawMessage) (interface{}, *JSONRPCError) {
	type DelMemoryArgs struct {
		Slug string `json:"slug"`
	}
	var dArgs DelMemoryArgs
	if e := decodeToolArgs(args, &dArgs); e != nil {
		return nil, e
	}
	if dArgs.Slug == "" {
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
}

var listAgentMemoriesTool = toolDef{
	Schema: map[string]interface{}{
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
	Output:   documentListOutputSchema("Matching agent memories. Scope lives in the memory-<scope> tags."),
	Handler:  (*Server).toolListAgentMemories,
	Behavior: toolBehavior{Title: "List Agent Memories", ReadOnly: true},
}

func (srv *Server) toolListAgentMemories(args json.RawMessage) (interface{}, *JSONRPCError) {
	type ListMemoriesArgs struct {
		MemoryType string `json:"memory_type"`
	}
	var lArgs ListMemoriesArgs
	_ = json.Unmarshal(args, &lArgs) // ignore err, it is optional

	articles, err := srv.Storage.ListArticles()
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: err.Error()}}}, nil
	}

	filterType := strings.ToLower(strings.TrimSpace(lArgs.MemoryType))

	var text string
	count := 0
	matched := []Article{}
	// ListArticles already returns the type, tags, and description this loop needs, so it
	// reads the metadata directly rather than re-reading and re-parsing every file.
	for _, art := range articles {
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
			matched = append(matched, art)
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

	return ToolResponse{
		Content:           []ToolContent{{Type: "text", Text: text}},
		StructuredContent: DocumentListOutput{Count: count, Documents: matched},
	}, nil
}

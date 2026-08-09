package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// This file holds the collaborative AI plan tools.
// Each tool pairs its JSON schema with its handler in one place, so the two can never
// drift apart. Registration order lives in mcp_tools.go.

var createAgentPlanTool = toolDef{
	Schema: map[string]interface{}{
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
	Handler:  (*Server).toolCreateAgentPlan,
	Behavior: toolBehavior{Title: "Create Agent Plan", Destructive: false, Idempotent: false},
}

func (srv *Server) toolCreateAgentPlan(args json.RawMessage) (interface{}, *JSONRPCError) {
	type CreatePlanArgs struct {
		Title          string `json:"title"`
		Content        string `json:"content"`
		ProjectContext string `json:"project_context"`
		Description    string `json:"description"`
		Source         string `json:"source"`
		EditSummary    string `json:"edit_summary"`
	}
	var pArgs CreatePlanArgs
	if e := decodeToolArgs(args, &pArgs); e != nil {
		return nil, e
	}
	if pArgs.Title == "" || pArgs.Content == "" || pArgs.ProjectContext == "" {
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
}

var appendAgentPlanTool = toolDef{
	Schema: map[string]interface{}{
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
	Handler:  (*Server).toolAppendAgentPlan,
	Behavior: toolBehavior{Title: "Append to Agent Plan", Destructive: false, Idempotent: false},
}

func (srv *Server) toolAppendAgentPlan(args json.RawMessage) (interface{}, *JSONRPCError) {
	type AppendPlanArgs struct {
		Slug            string `json:"slug"`
		ContentToAppend string `json:"content_to_append"`
		EditSummary     string `json:"edit_summary"`
	}
	var aArgs AppendPlanArgs
	if e := decodeToolArgs(args, &aArgs); e != nil {
		return nil, e
	}
	if aArgs.Slug == "" || aArgs.ContentToAppend == "" {
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
}

var editAgentPlanTool = toolDef{
	Schema: map[string]interface{}{
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
	Handler:  (*Server).toolEditAgentPlan,
	Behavior: toolBehavior{Title: "Edit Agent Plan", Destructive: true, Idempotent: false},
}

func (srv *Server) toolEditAgentPlan(args json.RawMessage) (interface{}, *JSONRPCError) {
	type EditPlanArgs struct {
		Slug          string    `json:"slug"`
		Title         *string   `json:"title,omitempty"`
		Content       *string   `json:"content,omitempty"`
		Tags          *[]string `json:"tags,omitempty"`
		LoadedVersion int       `json:"loaded_version"`
		EditSummary   string    `json:"edit_summary"`
	}
	var eArgs EditPlanArgs
	if e := decodeToolArgs(args, &eArgs); e != nil {
		return nil, e
	}
	if eArgs.Slug == "" || eArgs.LoadedVersion <= 0 {
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
}

var listAgentPlansTool = toolDef{
	Schema: map[string]interface{}{
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
	Output:   documentListOutputSchema("Matching agent plans. Lifecycle state lives in the status tags."),
	Handler:  (*Server).toolListAgentPlans,
	Behavior: toolBehavior{Title: "List Agent Plans", ReadOnly: true},
}

func (srv *Server) toolListAgentPlans(args json.RawMessage) (interface{}, *JSONRPCError) {
	type ListPlansArgs struct {
		ProjectContext string `json:"project_context"`
		Tag            string `json:"tag"`
	}
	var lArgs ListPlansArgs
	_ = json.Unmarshal(args, &lArgs) // ignore err, it is optional

	articles, err := srv.Storage.ListArticles()
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: err.Error()}}}, nil
	}

	filterProj := Slugify(strings.TrimSpace(lArgs.ProjectContext))
	filterTag := strings.ToLower(strings.TrimSpace(lArgs.Tag))

	var text string
	count := 0
	matched := []Article{}
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
			// A listing is metadata; the body is what read_article is for. Dropping it keeps a
			// structured index of a large wiki from being a copy of the whole wiki.
			meta := *art
			meta.Content = ""
			matched = append(matched, meta)
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

	return ToolResponse{
		Content:           []ToolContent{{Type: "text", Text: text}},
		StructuredContent: DocumentListOutput{Count: count, Documents: matched},
	}, nil
}

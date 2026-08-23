package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// This file holds the custom AI agent skill registry tools.
// Each tool pairs its JSON schema with its handler in one place, so the two can never
// drift apart. Registration order lives in mcp_tools.go.

var createAgentSkillTool = toolDef{
	Schema: map[string]interface{}{
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
				"status": map[string]interface{}{
					"type":        "string",
					"description": "Optional lifecycle status: 'draft' while the procedure is still being written, 'ready' once it is safe for an agent to follow, or 'archived' when retired. Omit for none. Never invent a value — an unrecognized status is rejected.",
				},
				"tags": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "string",
					},
					"description": "Optional tags for topics and grouping. Lifecycle state does NOT go here — it belongs in 'status', and a status word passed as a tag is rejected.",
				},
				"edit_summary": map[string]interface{}{
					"type":        "string",
					"description": "Optional summary describing the creation of the skill.",
				},
			},
			"required": []string{"title", "content"},
		},
	},
	Handler:  (*Server).toolCreateAgentSkill,
	Behavior: toolBehavior{Title: "Create Agent Skill", Destructive: false, Idempotent: false},
}

func (srv *Server) toolCreateAgentSkill(args json.RawMessage) (interface{}, *JSONRPCError) {
	type CreateSkillArgs struct {
		Title       string   `json:"title"`
		Content     string   `json:"content"`
		Description string   `json:"description"`
		Source      string   `json:"source"`
		Status      string   `json:"status"`
		Tags        []string `json:"tags"`
		EditSummary string   `json:"edit_summary"`
	}
	var sArgs CreateSkillArgs
	if e := decodeToolArgs(args, &sArgs); e != nil {
		return nil, e
	}
	if sArgs.Title == "" || sArgs.Content == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'title' and 'content' are required."}
	}
	if resp := rejectToolArtifactTitle(sArgs.Title, "skill"); resp != nil {
		return *resp, nil
	}

	title := sArgs.Title
	slug := Slugify(title)

	// The OKF type carries the skill class; only free user tags ride here — lifecycle state
	// lives in the status field.
	tags := validateAndCleanUserTags(sArgs.Tags, nil, ContentTypeSkill)
	status := NormalizeStatus(sArgs.Status)

	if _, err := srv.Storage.GetArticle(slug); err == nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: a skill with slug '%s' already exists", slug)}}}, nil
	}

	summary := sArgs.EditSummary
	if summary == "" {
		summary = "Created Custom AI Agent Skill"
	}

	art, err := srv.Storage.SaveArticleWithStatus("", title, sArgs.Content, sArgs.Description, sArgs.Source, "", summary, tags, ContentTypeSkill, &status)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error creating agent skill: %v", err)}}}, nil
	}

	respText := fmt.Sprintf("Success! Custom AI Skill '%s' created successfully.\nSlug: %s\nCreated At: %s\nVersion: %d\nStatus: %s\nTags: %s\n",
		art.Title, art.Slug, art.CreatedAt.Format(time.RFC3339), art.Version, art.Status, strings.Join(art.Tags, ", "))
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}

var editAgentSkillTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "edit_agent_skill",
		"description": "Modify the title, content, description, source, status, tags, or edit summary of an existing Custom AI Skill. The reserved AI-Agent-Skill type is strictly preserved and must NEVER be relabelled. Use this to refine a skill's procedure in place, or to promote it from 'draft' to 'ready' with the 'status' field.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"slug": map[string]interface{}{
					"type":        "string",
					"description": "The unique URL-safe slug of the skill to edit.",
				},
				"title": map[string]interface{}{
					"type":        "string",
					"description": "Optional new skill title (preserves the existing title if omitted).",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "Optional replacement Markdown body in SKILL.md format. Omit to preserve existing content.",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Optional one-line summary shown in list indexes and get_context_overview. Pointer semantics: omit to preserve the existing value, pass an empty string to clear it.",
				},
				"source": map[string]interface{}{
					"type":        "string",
					"description": "Optional provenance reference. Pointer semantics: omit to preserve, empty string to clear.",
				},
				"status": map[string]interface{}{
					"type":        "string",
					"description": "Optional new lifecycle status: 'draft', 'ready', or 'archived'. Omit to leave the skill's current state untouched. Never invent a value — an unrecognized status is rejected.",
				},
				"tags": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "string",
					},
					"description": "Optional tags to set on the skill — topics and grouping only (replaces existing user tags; the AI-Agent-Skill type is preserved). Lifecycle state belongs in 'status'; a status word passed as a tag is rejected.",
				},
				"loaded_version": map[string]interface{}{
					"type":        "integer",
					"description": "The active version number of the skill loaded by the client (helps detect multi-session edit collisions).",
				},
				"edit_summary": map[string]interface{}{
					"type":        "string",
					"description": "Optional summary outlining what changed.",
				},
			},
			"required": []string{"slug", "loaded_version"},
		},
	},
	Handler:  (*Server).toolEditAgentSkill,
	Behavior: toolBehavior{Title: "Edit Agent Skill", Destructive: true, Idempotent: false},
}

// toolEditAgentSkill completes the skill lifecycle. create_agent_skill and list_agent_skills have
// always existed with no edit counterpart, so the only way to revise a skill through MCP was
// edit_wiki_article — which works, but offers none of the type guarding the memory and plan tools
// apply. That mattered most for nexwiki-agent-guidelines: the governance document every agent
// loads is itself a skill, so the one document intended to be revised had no first-class edit path.
func (srv *Server) toolEditAgentSkill(args json.RawMessage) (interface{}, *JSONRPCError) {
	type EditSkillArgs struct {
		Slug          string    `json:"slug"`
		Title         *string   `json:"title,omitempty"`
		Content       *string   `json:"content,omitempty"`
		Description   *string   `json:"description,omitempty"`
		Source        *string   `json:"source,omitempty"`
		Status        *string   `json:"status,omitempty"`
		Tags          *[]string `json:"tags,omitempty"`
		LoadedVersion int       `json:"loaded_version"`
		EditSummary   string    `json:"edit_summary"`
	}
	var eArgs EditSkillArgs
	if e := decodeToolArgs(args, &eArgs); e != nil {
		return nil, e
	}
	if eArgs.Slug == "" || eArgs.LoadedVersion <= 0 {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'slug' and positive 'loaded_version' are required."}
	}

	existing, err := srv.Storage.GetArticle(eArgs.Slug)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: skill with slug '%s' not found", eArgs.Slug)}}}, nil
	}

	if existing.Type != ContentTypeSkill {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: target article is not a Custom AI Skill (type must be AI-Agent-Skill)."}}}, nil
	}

	if existing.Version > 0 && existing.Version != eArgs.LoadedVersion {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: versionConflictMessage("skill", eArgs.Slug, existing.Version, eArgs.LoadedVersion)}}}, nil
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
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: content cannot be empty. Use 'delete_wiki_article' to remove a skill entirely."}}}, nil
		}
		newContent = *eArgs.Content
	}

	newDescription := existing.Description
	if eArgs.Description != nil {
		newDescription = strings.TrimSpace(*eArgs.Description)
	}

	newSource := existing.Source
	if eArgs.Source != nil {
		newSource = strings.TrimSpace(*eArgs.Source)
	}

	newTags := existing.Tags
	if eArgs.Tags != nil {
		newTags = validateAndCleanUserTags(*eArgs.Tags, existing.Tags, existing.Type)
	}

	summary := eArgs.EditSummary
	if summary == "" {
		summary = "Updated Custom AI Agent Skill"
	}

	art, err := srv.Storage.SaveArticleWithStatus(existing.Slug, newTitle, newContent, newDescription, newSource, existing.Resource, summary, newTags, existing.Type, eArgs.Status)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error editing agent skill: %v", err)}}}, nil
	}

	respText := fmt.Sprintf("Success! Custom AI Skill '%s' updated successfully.\nSlug: %s\nNew Version: %d\nLast Edited: %s\nStatus: %s\nTags: %s\n",
		art.Title, art.Slug, art.Version, art.Timestamp.Format(time.RFC3339), art.Status, strings.Join(art.Tags, ", "))
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}

var listAgentSkillsTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "list_agent_skills",
		"description": "List all Custom AI Skills (OKF type AI-Agent-Skill) currently saved in the knowledge base.",
		"inputSchema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	Output:   documentListOutputSchema("Every custom agent skill in the knowledge base."),
	Handler:  (*Server).toolListAgentSkills,
	Behavior: toolBehavior{Title: "List Agent Skills", ReadOnly: true},
}

func (srv *Server) toolListAgentSkills(args json.RawMessage) (interface{}, *JSONRPCError) {
	articles, err := srv.Storage.ListArticles()
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: err.Error()}}}, nil
	}

	var text string
	count := 0
	matched := []Article{}
	for _, artMeta := range articles {
		art, err := srv.Storage.GetArticle(artMeta.Slug)
		if err != nil {
			continue
		}

		if art.Type == ContentTypeSkill {
			count++
			// Metadata only; the body is what read_article is for.
			meta := *art
			meta.Content = ""
			matched = append(matched, meta)
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

	return ToolResponse{
		Content:           []ToolContent{{Type: "text", Text: text}},
		StructuredContent: DocumentListOutput{Count: count, Documents: matched},
	}, nil
}

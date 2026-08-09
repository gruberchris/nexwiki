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
	Handler:  (*Server).toolCreateAgentSkill,
	Behavior: toolBehavior{Title: "Create Agent Skill", Destructive: false, Idempotent: false},
}

func (srv *Server) toolCreateAgentSkill(args json.RawMessage) (interface{}, *JSONRPCError) {
	type CreateSkillArgs struct {
		Title       string   `json:"title"`
		Content     string   `json:"content"`
		Description string   `json:"description"`
		Source      string   `json:"source"`
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
}

package server

import (
	"encoding/json"
	"fmt"
)

// MCP Prompts — interactive workflow templates that walk an agent through a multistep task.
// Both protocol eras serve the same definitions and the same rendering logic from here, so a
// prompt can never be advertised by one era and missing from the other.

// promptDefinitions is the payload emitted by prompts/list.
func promptDefinitions() []map[string]interface{} {
	return []map[string]interface{}{
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
	}
}

// getPrompt renders a named prompt with its arguments interpolated, for prompts/get.
func (srv *Server) getPrompt(params json.RawMessage) (interface{}, *JSONRPCError) {
	type GetPromptArgs struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	var promptArgs GetPromptArgs
	if err := json.Unmarshal(params, &promptArgs); err != nil {
		return nil, &JSONRPCError{Code: -32602, Message: "Invalid prompt parameters"}
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

		return map[string]interface{}{
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
		}, nil

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

		return map[string]interface{}{
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
		}, nil

	default:
		return nil, &JSONRPCError{
			Code:    -32601,
			Message: fmt.Sprintf("Prompt not found: %s", promptArgs.Name),
		}
	}
}

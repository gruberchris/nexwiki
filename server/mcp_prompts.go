package server

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MCP Prompts — interactive workflow templates that walk an agent through a multistep task.
// Both protocol eras serve the same definitions and the same rendering logic from here, so a
// prompt can never be advertised by one era and missing from the other.

// promptDefinitions is the payload emitted by prompts/list.
func promptDefinitions() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "article_creation_workflow",
			"description": "Guides the agent on how to search for styling guidelines, custom memories, and declare OKF v0.2 sources with credibility signals and footnote links [^id]: ... before writing a new Wiki article.",
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

// listPrompts builds the prompts/list payload for a cursor. Like the tool list it is compiled in
// and fits one page; it shares the helper so cursor handling cannot differ between list methods.
func listPrompts(cursor string) (interface{}, *JSONRPCError) {
	page, nextCursor, rpcErr := paginate(promptDefinitions(), cursor, listPageSize)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return listResult("prompts", page, nextCursor), nil
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
		title := strings.TrimSpace(promptArgs.Arguments["title"])
		desc := promptArgs.Arguments["description"]
		if title == "" {
			return nil, missingPromptArgument("article_creation_workflow", "title")
		}

		promptText := fmt.Sprintf(`You are an AI assistant tasked with creating a new article titled "%s" in the user's NexWiki knowledge base.

Before you begin writing the article, you MUST follow these steps to ensure format consistency, provenance, and alignment with user rules:
1. If you have not called 'get_wiki_overview' this session, call it once: its pinned memories are the operator's standing preferences and corrections, and they apply here. Then make one 'search_wiki' call (or 'list_articles' with type "memories") for "rules", "formatting", or "style guide" documents covering this kind of article (e.g., programming language guides, system architecture templates). Finding nothing is a completed check — use a sensible structure of your own.
2. If any formatting rules or style memories are found, read their contents using 'read_article'.
3. Incorporate those styles, sections, structure, and constraints strictly into the new article's content.
4. Write the article content in clean, semantic Markdown. When citing external documentation, specifications, or reference materials, use OKF v0.2 footnote links matching source identifiers (e.g. '[^id]: https://...' or inline citations '[^id]').
5. Save the article using 'save_article' (type "Wiki"). Set 'description' to a one-line summary and 'source' to where the material came from; list every cited reference as a footnote in the body so each one stays attached to the claim it supports.
   Include a helpful edit summary detailing the style guidelines and sources you incorporated.
6. Let the user know you successfully incorporated the specific style rules and provenance sources you recorded.`, title)

		if desc != "" {
			promptText += fmt.Sprintf("\n\nArticle Outline/Description: %s", desc)
		}

		return map[string]interface{}{
			"description": "Guides the agent on how to correctly search for styling/formatting guidelines, custom memories, and OKF v0.2 sources before writing a new Wiki article.",
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
		title := strings.TrimSpace(promptArgs.Arguments["title"])
		project := strings.TrimSpace(promptArgs.Arguments["project"])
		if title == "" {
			return nil, missingPromptArgument("project_planning_workflow", "title")
		}
		if project == "" {
			return nil, missingPromptArgument("project_planning_workflow", "project")
		}

		promptText := fmt.Sprintf(`You are an AI assistant tasked with creating a new Collaborative AI Plan for the project "%s" titled "%s".

Please follow these strict steps:
1. Collaboratively outline the plan with the user, dividing it into clear objectives, architectural details, technical requirements, and task checklists.
2. Format the plan using rich, clean Markdown.
3. Save the initial plan in NexWiki immediately using the 'save_article' tool with type "AI-Agent-Plan". Make sure to specify the project_context as "%s".
4. Inform the user that the plan is saved in NexWiki, provide the article slug, and ask for their feedback or approval on the plan.
5. As tasks are completed or updated during implementation, use 'append_article' to log the progress and update the checklists.
6. When the plan is fully implemented, use 'append_article' to add final notes documenting anything worth noting (plan deviations, files created, tools used, unexpected challenges, or other observations).
7. After adding final notes, use 'save_article' with the plan's slug, its current loaded_version, status: "completed", and the plan's current body passed back unchanged as 'content' (content always replaces the whole body) to close the plan. Lifecycle state is the 'status' field, not a tag — a status word passed in 'tags' is rejected.

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
		// -32602, not -32601. A name that does not match a prompt is a bad *argument*; the method
		// itself exists and was found. The distinction is not academic over HTTP in the modern era,
		// where -32601 is required to surface as 404 — so a typo'd prompt name used to look to the
		// client like the MCP endpoint had disappeared.
		return nil, &JSONRPCError{
			Code:    -32602,
			Message: fmt.Sprintf("Prompt not found: %s", promptArgs.Name),
		}
	}
}

// missingPromptArgument reports an absent required argument. The specification asks for -32602
// here, and naming the argument is what lets a client fix the call rather than re-reading
// prompts/list to guess which one it left out.
func missingPromptArgument(prompt, argument string) *JSONRPCError {
	return &JSONRPCError{
		Code:    -32602,
		Message: fmt.Sprintf("Missing required argument %q for prompt %q", argument, prompt),
	}
}

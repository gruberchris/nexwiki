package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"
)

// This file holds the wiki article tools: search, read, list, create, edit, tag, delete, history, revert,
// backlinks, and the progressive-disclosure context overview.
// Each tool pairs its JSON schema with its handler in one place, so the two can never
// drift apart. Registration order lives in mcp_tools.go.

var searchWikiTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "search_wiki",
		"description": "Perform full-text searches across the entire NexWiki knowledge base using Bleve query parsing. Searches ALL document types by default — wiki articles, your own agent memories, plans, and skills — so prior knowledge you recorded is always retrievable. Returns scored matches with highlighted snippets. Use 'type' and 'tags' to narrow.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "The search keywords or query string. Supports wildcards, quotes for exact matches, and boolean terms.",
				},
				"type": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "string",
						"enum": SearchTypeNames(),
					},
					"description": "Optional document types to restrict the search to: 'articles', 'memories', 'plans', 'skills'. Omit to search every type.",
				},
				"tags": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "string",
					},
					"description": "Optional tags a result must ALL carry (case-insensitive), e.g. ['wip'] or ['memory-nexwiki'].",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Optional maximum number of results (default 40, maximum 200).",
				},
				"include_archived": map[string]interface{}{
					"type":        "boolean",
					"description": "Optional; set true to include archived documents, which are excluded by default.",
				},
			},
			"required": []string{"query"},
		},
	},
	Output:   searchOutputSchema(),
	Handler:  (*Server).toolSearchWiki,
	Behavior: toolBehavior{Title: "Search Wiki", ReadOnly: true},
}

func (srv *Server) toolSearchWiki(args json.RawMessage) (interface{}, *JSONRPCError) {
	type SearchArgs struct {
		Query           string   `json:"query"`
		Types           []string `json:"type"`
		Tags            []string `json:"tags"`
		Limit           int      `json:"limit"`
		IncludeArchived bool     `json:"include_archived"`
	}
	var searchArgs SearchArgs
	if e := decodeToolArgs(args, &searchArgs); e != nil {
		return nil, e
	}
	if searchArgs.Query == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'query' argument"}
	}

	// Report a bad type name rather than silently returning nothing — an agent that typos
	// "memorys" would otherwise conclude the knowledge simply is not there.
	if unknown := ValidateSearchTypes(searchArgs.Types); len(unknown) > 0 {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf(
			"Error: unknown document type(s): %s. Valid values: %s.",
			strings.Join(unknown, ", "), strings.Join(SearchTypeNames(), ", "))}}}, nil
	}

	// No legacyQueryHeuristics: an agent searching its own second brain sees every document
	// type unless it explicitly narrows. Memories and plans are the point, not noise.
	results, err := srv.Storage.SearchArticlesWithOptions(searchArgs.Query, SearchOptions{
		Types:           searchArgs.Types,
		Tags:            searchArgs.Tags,
		Limit:           searchArgs.Limit,
		IncludeArchived: searchArgs.IncludeArchived,
	})
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: err.Error()}}}, nil
	}

	// Describe the applied facets so the agent can tell "no such knowledge" from "my filter
	// excluded it" — the difference between giving up and retrying with a wider search.
	var facets []string
	if len(searchArgs.Types) > 0 {
		facets = append(facets, "type: "+strings.Join(searchArgs.Types, ", "))
	}
	if len(searchArgs.Tags) > 0 {
		facets = append(facets, "tags: "+strings.Join(searchArgs.Tags, ", "))
	}
	if searchArgs.IncludeArchived {
		facets = append(facets, "including archived")
	}
	facetStr := ""
	if len(facets) > 0 {
		facetStr = fmt.Sprintf(" [filtered by %s]", strings.Join(facets, "; "))
	}

	// Build the structured payload first, then render the prose from it, so the two halves of
	// the answer are the same data and cannot disagree.
	hits := make([]SearchHit, 0, len(results))
	for _, res := range results {
		snippets := make([]string, 0, len(res.Snippets))
		for _, snippet := range res.Snippets {
			snippets = append(snippets, plainSnippet(snippet))
		}
		hits = append(hits, SearchHit{
			Title:     res.Title,
			Slug:      res.Slug,
			Type:      res.Type,
			Score:     res.Score,
			Timestamp: res.Timestamp,
			Tags:      res.Tags,
			Snippets:  snippets,
		})
	}

	var text string
	if len(hits) == 0 {
		text = fmt.Sprintf("No documents found matching query: '%s'%s\n", searchArgs.Query, facetStr)
	} else {
		text = fmt.Sprintf("Found %d matching documents in NexWiki%s:\n\n", len(hits), facetStr)
		for i, hit := range hits {
			tagsStr := ""
			if len(hit.Tags) > 0 {
				tagsStr = fmt.Sprintf(" | Tags: %s", strings.Join(hit.Tags, ", "))
			}
			text += fmt.Sprintf("[%d] %s (Slug: %s, Type: %s, Score: %.3f%s)\n",
				i+1, hit.Title, hit.Slug, hit.Type, hit.Score, tagsStr)
			for _, snippet := range hit.Snippets {
				text += fmt.Sprintf("    Snippet: ... %s ...\n", snippet)
			}
			text += "\n"
		}
	}

	return ToolResponse{
		Content: []ToolContent{{Type: "text", Text: text}},
		StructuredContent: SearchOutput{
			Query:           searchArgs.Query,
			Count:           len(hits),
			Types:           searchArgs.Types,
			Tags:            searchArgs.Tags,
			IncludeArchived: searchArgs.IncludeArchived,
			Results:         hits,
		},
	}, nil
}

// plainSnippet converts a search snippet from the HTML the browser renders (entity-escaped text
// with <mark> highlights) into plain prose with Markdown bold. Handing an agent HTML it did not
// ask for invites it to paste markup back into an article.
func plainSnippet(snippet string) string {
	s := strings.ReplaceAll(snippet, "<mark>", "**")
	s = strings.ReplaceAll(s, "</mark>", "**")
	return html.UnescapeString(s)
}

var readArticleTool = toolDef{
	Schema: map[string]interface{}{
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
	Output:   articleOutputSchema(),
	Handler:  (*Server).toolReadArticle,
	Behavior: toolBehavior{Title: "Read Article", ReadOnly: true},
}

func (srv *Server) toolReadArticle(args json.RawMessage) (interface{}, *JSONRPCError) {
	type ReadArgs struct {
		Slug string `json:"slug"`
	}
	var readArgs ReadArgs
	if e := decodeToolArgs(args, &readArgs); e != nil {
		return nil, e
	}
	if readArgs.Slug == "" {
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
	links := []DocumentLink{}
	if backlinks, blErr := srv.Storage.GetBacklinks(art.Slug); blErr == nil && len(backlinks) > 0 {
		const maxShownBacklinks = 15
		var refs []string
		for i, bl := range backlinks {
			// The structured payload carries every backlink. Only the prose is truncated,
			// because that cap exists to keep a read from burying the article in a link list.
			links = append(links, DocumentLink{Title: bl.Title, Slug: bl.Slug})
			if i >= maxShownBacklinks {
				continue
			}
			refs = append(refs, fmt.Sprintf("%s (%s)", bl.Title, bl.Slug))
		}
		if len(backlinks) > maxShownBacklinks {
			refs = append(refs[:maxShownBacklinks], fmt.Sprintf("and %d more", len(backlinks)-maxShownBacklinks))
		}
		text += fmt.Sprintf("\n\n---\nLinked from: %s", strings.Join(refs, ", "))
	}

	return ToolResponse{
		Content:           []ToolContent{{Type: "text", Text: text}},
		StructuredContent: ArticleOutput{Article: *art, Backlinks: links},
	}, nil
}

var listArticlesTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "list_articles",
		"description": "List all articles currently available inside your NexWiki knowledge base, showing their titles, URL slugs, and article types (e.g., Wiki Article, Agent Memory, Agent Plan, or Agent Skill).",
		"inputSchema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	Output:   documentListOutputSchema("Every document in the knowledge base, most recently updated first."),
	Handler:  (*Server).toolListArticles,
	Behavior: toolBehavior{Title: "List Articles", ReadOnly: true},
}

func (srv *Server) toolListArticles(args json.RawMessage) (interface{}, *JSONRPCError) {
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

	return ToolResponse{
		Content:           []ToolContent{{Type: "text", Text: text}},
		StructuredContent: DocumentListOutput{Count: len(articles), Documents: nonNilDocuments(articles)},
	}, nil
}

// nonNilDocuments guarantees an empty listing serializes as [] rather than null. A schema
// declaring `"type": "array"` does not match null, so a client validating structuredContent
// against the published outputSchema would reject an empty wiki.
func nonNilDocuments(articles []Article) []Article {
	if articles == nil {
		return []Article{}
	}
	return articles
}

var createWikiArticleTool = toolDef{
	Schema: map[string]interface{}{
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
	Handler:  (*Server).toolCreateWikiArticle,
	Behavior: toolBehavior{Title: "Create Wiki Article", Destructive: false, Idempotent: false},
}

func (srv *Server) toolCreateWikiArticle(args json.RawMessage) (interface{}, *JSONRPCError) {
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
	if e := decodeToolArgs(args, &cArgs); e != nil {
		return nil, e
	}
	if cArgs.Title == "" || cArgs.Content == "" {
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
}

var editWikiArticleTool = toolDef{
	Schema: map[string]interface{}{
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
	Handler:  (*Server).toolEditWikiArticle,
	Behavior: toolBehavior{Title: "Edit Wiki Article", Destructive: true, Idempotent: false},
}

func (srv *Server) toolEditWikiArticle(args json.RawMessage) (interface{}, *JSONRPCError) {
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
	if e := decodeToolArgs(args, &eArgs); e != nil {
		return nil, e
	}
	if eArgs.Slug == "" || eArgs.Title == "" || eArgs.Content == "" || eArgs.LoadedVersion <= 0 {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. Requires 'slug', 'title', 'content', and positive 'loaded_version'"}
	}

	// Empty/omitted description and source preserve the existing values; resource already
	// uses pointer semantics (omit=preserve, ""=clear, value=replace). Nil tags preserve.
	edit := ArticleEdit{
		Title:         eArgs.Title,
		Content:       eArgs.Content,
		Resource:      eArgs.Resource,
		EditSummary:   eArgs.EditSummary,
		LoadedVersion: eArgs.LoadedVersion,
	}
	if eArgs.Description != "" {
		edit.Description = &eArgs.Description
	}
	if eArgs.Source != "" {
		edit.Source = &eArgs.Source
	}
	if eArgs.Tags != nil {
		edit.Tags = &eArgs.Tags
	}

	// ApplyArticleEdit performs the version check and the write under one lock. Reading the
	// article, comparing versions, and saving as three separate steps let a concurrent writer
	// land in the gap — the guard would pass and still clobber the other session's edit.
	art, err := srv.Storage.ApplyArticleEdit(eArgs.Slug, edit)
	switch {
	case errors.Is(err, ErrVersionConflict):
		current := "unknown"
		if existing, gErr := srv.Storage.GetArticle(eArgs.Slug); gErr == nil {
			current = fmt.Sprintf("%d", existing.Version)
		}
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: Version conflict! The article was updated by another session. Disk version is %s, but you loaded version %d. Re-fetch the article and try again.", current, eArgs.LoadedVersion)}}}, nil
	case err != nil && strings.Contains(err.Error(), "article not found"):
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: article with slug '%s' not found", eArgs.Slug)}}}, nil
	case err != nil:
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error editing article: %v", err)}}}, nil
	}

	respText := fmt.Sprintf("Success! Article '%s' (slug: %s) updated successfully.\nNew Version: %d\nLast Edited: %s\n",
		art.Title, art.Slug, art.Version, art.Timestamp.Format(time.RFC3339))
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}

var updateArticleTagsTool = toolDef{
	Schema: map[string]interface{}{
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
	Handler:  (*Server).toolUpdateArticleTags,
	Behavior: toolBehavior{Title: "Update Article Tags", Destructive: true, Idempotent: false},
}

func (srv *Server) toolUpdateArticleTags(args json.RawMessage) (interface{}, *JSONRPCError) {
	type UpdateTagsArgs struct {
		Slug          string   `json:"slug"`
		Tags          []string `json:"tags"`
		LoadedVersion int      `json:"loaded_version"`
		EditSummary   string   `json:"edit_summary"`
	}
	var uArgs UpdateTagsArgs
	if e := decodeToolArgs(args, &uArgs); e != nil {
		return nil, e
	}
	if uArgs.Slug == "" || uArgs.Tags == nil {
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
}

var deleteWikiArticleTool = toolDef{
	Schema: map[string]interface{}{
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
	Handler:  (*Server).toolDeleteWikiArticle,
	Behavior: toolBehavior{Title: "Delete Wiki Article", Destructive: true, Idempotent: true},
}

func (srv *Server) toolDeleteWikiArticle(args json.RawMessage) (interface{}, *JSONRPCError) {
	type DelArgs struct {
		Slug string `json:"slug"`
	}
	var dArgs DelArgs
	if e := decodeToolArgs(args, &dArgs); e != nil {
		return nil, e
	}
	if dArgs.Slug == "" {
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
}

var getArticleHistoryTool = toolDef{
	Schema: map[string]interface{}{
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
	Output:   historyOutputSchema(),
	Handler:  (*Server).toolGetArticleHistory,
	Behavior: toolBehavior{Title: "Get Article History", ReadOnly: true},
}

func (srv *Server) toolGetArticleHistory(args json.RawMessage) (interface{}, *JSONRPCError) {
	type HistArgs struct {
		Slug string `json:"slug"`
	}
	var hArgs HistArgs
	if e := decodeToolArgs(args, &hArgs); e != nil {
		return nil, e
	}
	if hArgs.Slug == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'slug' argument"}
	}

	history, err := srv.Storage.GetArticleHistory(hArgs.Slug)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error loading history for '%s': %v", hArgs.Slug, err)}}}, nil
	}

	// A revision listing exists to answer "which version do I revert to, and why", so the
	// structured form carries those fields rather than a full copy of every past version.
	versions := make([]RevisionRef, 0, len(history))
	for _, ver := range history {
		versions = append(versions, RevisionRef{
			Version:     ver.Version,
			Timestamp:   ver.Timestamp,
			EditSummary: ver.EditSummary,
		})
	}

	// Join who-wrote-what from the activity log. Unattributed revisions stay unattributed rather
	// than guessing — see attributeRevisions.
	slug := Slugify(hArgs.Slug)
	versions = attributeRevisions(ActivityLogPath(srv.Storage.DataDir), slug, versions)

	// The article's own provenance — where the knowledge came from, as distinct from who typed it.
	// A missing article is not an error here: history can outlive the document it belonged to.
	var source string
	if art, err := srv.Storage.GetArticle(slug); err == nil {
		source = art.Source
	}

	var respText string
	if len(versions) == 0 {
		respText = fmt.Sprintf("No historical versions found for article '%s'\n", hArgs.Slug)
	} else {
		respText = fmt.Sprintf("Revision History for '%s' (%d versions):\n\n", hArgs.Slug, len(versions))
		for _, ver := range versions {
			respText += fmt.Sprintf("Version: %d | Edited: %s\n", ver.Version, ver.Timestamp.Format(time.RFC3339))
			if ver.Agent != "" {
				byLine := fmt.Sprintf("  By: %s", ver.Agent)
				if ver.Tool != "" {
					byLine += fmt.Sprintf(" (via %s)", ver.Tool)
				}
				respText += byLine + "\n"
			}
			if ver.EditSummary != "" {
				respText += fmt.Sprintf("  Summary: %s\n", ver.EditSummary)
			}
			respText += "\n"
		}
		if source != "" {
			respText += fmt.Sprintf("Article source: %s\n", source)
		}
	}

	return ToolResponse{
		Content:           []ToolContent{{Type: "text", Text: respText}},
		StructuredContent: HistoryOutput{Slug: slug, Count: len(versions), Source: source, Versions: versions},
	}, nil
}

var revertArticleVersionTool = toolDef{
	Schema: map[string]interface{}{
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
	Handler:  (*Server).toolRevertArticleVersion,
	Behavior: toolBehavior{Title: "Revert Article Version", Destructive: true, Idempotent: false},
}

func (srv *Server) toolRevertArticleVersion(args json.RawMessage) (interface{}, *JSONRPCError) {
	type RevArgs struct {
		Slug    string `json:"slug"`
		Version int    `json:"version"`
	}
	var rArgs RevArgs
	if e := decodeToolArgs(args, &rArgs); e != nil {
		return nil, e
	}
	if rArgs.Slug == "" || rArgs.Version <= 0 {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. Requires 'slug' and positive 'version'"}
	}

	art, err := srv.Storage.RevertArticle(rArgs.Slug, rArgs.Version)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Revert failed: %v", err)}}}, nil
	}

	respText := fmt.Sprintf("Success! Article '%s' reverted successfully to version %d.\nNew active version: %d\nLast Edited: %s\n",
		art.Title, rArgs.Version, art.Version, art.Timestamp.Format(time.RFC3339))
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}

var getBacklinksTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "get_backlinks",
		"description": "List all articles whose content links to the given article, in either internal link form — double-bracket [[WikiLinks]] or absolute [text](/articles/<slug>) Markdown links. Use this to traverse the knowledge graph in reverse: find the pages that reference a concept, decision, or note before editing or deleting it.",
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
	Output:   backlinksOutputSchema(),
	Handler:  (*Server).toolGetBacklinks,
	Behavior: toolBehavior{Title: "Get Backlinks", ReadOnly: true},
}

func (srv *Server) toolGetBacklinks(args json.RawMessage) (interface{}, *JSONRPCError) {
	type BacklinkArgs struct {
		Slug string `json:"slug"`
	}
	var bArgs BacklinkArgs
	if e := decodeToolArgs(args, &bArgs); e != nil {
		return nil, e
	}
	if bArgs.Slug == "" {
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

	return ToolResponse{
		Content: []ToolContent{{Type: "text", Text: text}},
		StructuredContent: BacklinksOutput{
			Slug:      target.Slug,
			Count:     len(backlinks),
			Backlinks: nonNilDocuments(backlinks),
		},
	}, nil
}

var getContextOverviewTool = toolDef{
	Schema: map[string]interface{}{
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
	Handler:  (*Server).toolGetContextOverview,
	Behavior: toolBehavior{Title: "Get Context Overview", ReadOnly: true},
}

func (srv *Server) toolGetContextOverview(args json.RawMessage) (interface{}, *JSONRPCError) {
	type OverviewArgs struct {
		Type string `json:"type"`
	}
	var oArgs OverviewArgs
	_ = json.Unmarshal(args, &oArgs) // optional args

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
}

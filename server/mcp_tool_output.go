package server

import "time"

// This file holds the structured-output half of the tool contract: the Go types a read tool
// returns as `structuredContent`, and the JSON Schema each one publishes as `outputSchema`.
//
// The two live side by side deliberately. A schema that drifts from the struct it describes is
// worse than no schema at all — an agent that trusts `outputSchema` enough to skip parsing the
// prose has no way to discover the mismatch, so it reads a field that is never populated and
// concludes the knowledge is absent. Keeping the pair adjacent (and asserting the pairing in
// TestStructuredOutputMatchesSchema) is the same reasoning that put schema and handler together
// in toolDef.
//
// Why structured output at all: every tool used to answer only in prose, so an agent wanting the
// version number to pass as `loaded_version` had to scrape an integer out of a sentence. The
// text is still emitted — the spec asks for it, and it is what a human reading a transcript
// wants — but it is now derived from the same value the structured payload carries, so the two
// cannot disagree.

// SearchHit is one `search_wiki` match.
//
// Snippets differ from the SearchResult they come from: those carry HTML (entity-escaped text
// with <mark> highlights) because the browser renders them as HTML. Handing an agent HTML it did
// not ask for invites it to paste markup into an article, so the highlights become Markdown bold
// and the entities are unescaped — the same conversion the prose path already performed.
type SearchHit struct {
	Title     string    `json:"title"`
	Slug      string    `json:"slug"`
	Type      string    `json:"type"`
	Score     float64   `json:"score"`
	Timestamp time.Time `json:"timestamp"`
	Tags      []string  `json:"tags,omitempty"`
	Snippets  []string  `json:"snippets,omitempty"`
}

// SearchOutput is the `search_wiki` payload. Query and the applied facets are echoed back so an
// agent reading only the structured half can still tell "no such knowledge" from "my filter
// excluded it" — the same distinction the prose spells out.
type SearchOutput struct {
	Query           string      `json:"query"`
	Count           int         `json:"count"`
	Types           []string    `json:"type,omitempty"`
	Tags            []string    `json:"tags,omitempty"`
	IncludeArchived bool        `json:"include_archived"`
	Results         []SearchHit `json:"results"`
}

// DocumentLink is a bare title/slug reference, used where a full document summary would be noise.
type DocumentLink struct {
	Title string `json:"title"`
	Slug  string `json:"slug"`
}

// ArticleOutput is the `read_article` payload. The embedded Article carries `version`, which is
// what `edit_wiki_article` requires as `loaded_version` — the single most valuable field to hand
// over as a number rather than as prose.
type ArticleOutput struct {
	Article   Article        `json:"article"`
	Backlinks []DocumentLink `json:"backlinks"`
}

// DocumentListOutput is the shared payload of every list-shaped tool: list_articles,
// list_agent_memories, list_agent_plans, and list_agent_skills. One shape for all four means an
// agent learns to read a NexWiki listing once.
type DocumentListOutput struct {
	Count     int       `json:"count"`
	Documents []Article `json:"documents"`
}

// BacklinksOutput is the `get_backlinks` payload.
type BacklinksOutput struct {
	Slug      string    `json:"slug"`
	Count     int       `json:"count"`
	Backlinks []Article `json:"backlinks"`
}

// RevisionRef is one entry in an article's revision history. Deliberately not a full Article:
// history entries would otherwise repeat every field of every past version, and the useful
// content of a history listing is which version to revert to and why.
type RevisionRef struct {
	Version     int       `json:"version"`
	Timestamp   time.Time `json:"timestamp"`
	EditSummary string    `json:"edit_summary,omitempty"`

	// Agent is who made this revision, joined from the activity log. Omitted when the log has no
	// event for it — which is normal for revisions predating the log, or older than its retention.
	// Self-reported by the client: a provenance hint, not an authentication claim.
	Agent string `json:"agent,omitempty"`
	// Tool is the MCP tool used, empty for edits made through the web UI.
	Tool string `json:"tool,omitempty"`
	// Via is the transport that carried the edit: "mcp" or "api".
	Via string `json:"via,omitempty"`
}

// HistoryOutput is the `get_article_history` payload, newest version first.
type HistoryOutput struct {
	Slug  string `json:"slug"`
	Count int    `json:"count"`
	// Source is the article's provenance field — where the knowledge came from, as opposed to who
	// typed it. Together with the per-revision Agent this answers §6.9's question in full:
	// "Claude wrote this on 2026-06-20, citing X".
	Source   string        `json:"source,omitempty"`
	Versions []RevisionRef `json:"versions"`
}

// BrokenLinkRef is an internal link whose target does not exist. TargetSlug is the Slugify'd form
// the link actually resolves to, which is the name a fix has to create.
//
// Form says which syntax the link was written in ("wikilink" or "markdown"), because the two are
// repaired differently and an agent told to fix "[[rust]]" in a file that actually says
// "[Rust](/articles/rust)" will not find it.
type BrokenLinkRef struct {
	FromSlug   string   `json:"from_slug"`
	Target     string   `json:"target"`
	TargetSlug string   `json:"target_slug"`
	Form       LinkForm `json:"form"`
}

// Display renders the broken link in the syntax the author wrote it, delegating to LinkRef so the
// two reports that print broken links cannot describe the same link differently.
func (b BrokenLinkRef) Display() string {
	return LinkRef{Target: b.Target, Form: b.Form}.Display()
}

// StatisticsOutput is the `get_wiki_statistics` payload.
type StatisticsOutput struct {
	TotalArticles   int             `json:"total_articles"`
	TotalLinks      int             `json:"total_links"`
	BrokenLinkCount int             `json:"broken_link_count"`
	BrokenLinks     []BrokenLinkRef `json:"broken_links"`
}

// StatusTagsOutput is the `get_status_tags` payload. The two agent vocabularies are enforced;
// the general list is advisory, and status_tags remains their union for backward compatibility.
type StatusTagsOutput struct {
	StatusTags []string `json:"status_tags"`
	// PlanStatusTags is the closed plan lifecycle vocabulary: every AI-Agent-Plan carries
	// exactly one of these, and no other lifecycle word.
	PlanStatusTags []string `json:"plan_status_tags"`
	// SkillStatusTags is the closed skill lifecycle vocabulary: an AI-Agent-Skill carries at
	// most one of these, and no other lifecycle word.
	SkillStatusTags []string `json:"skill_status_tags"`
	// GeneralStatusTags are conventional lifecycle words for wiki articles and memories. They
	// are suggestions, not rules — those documents may carry any tags at all.
	GeneralStatusTags []string `json:"general_status_tags"`
}

// ActivityOutput is the `get_recent_activity` payload, oldest event first to match the prose.
type ActivityOutput struct {
	Count  int        `json:"count"`
	Events []LogEvent `json:"events"`
}

// --- JSON Schema construction -------------------------------------------------------------
//
// Each helper returns a freshly built map rather than a shared package-level value. The registry
// merges tool schemas into the tools/list payload, and a map shared between two tools would let a
// mutation in one surface in the other.

func schemaOf(typ, description string) map[string]interface{} {
	return map[string]interface{}{"type": typ, "description": description}
}

func schemaArrayOf(items map[string]interface{}, description string) map[string]interface{} {
	return map[string]interface{}{"type": "array", "items": items, "description": description}
}

func schemaStringArray(description string) map[string]interface{} {
	return schemaArrayOf(map[string]interface{}{"type": "string"}, description)
}

func schemaObject(properties map[string]interface{}, required ...string) map[string]interface{} {
	obj := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		obj["required"] = required
	}
	return obj
}

// articleSchema describes an Article as it appears in structured output. Field names are
// Article's own JSON tags, so a document read over MCP and the same document read from
// /api/articles have identical keys — an agent that has seen one already knows the other.
func articleSchema(withContent bool) map[string]interface{} {
	props := map[string]interface{}{
		"type":         schemaOf("string", "OKF document class: Wiki, AI-Agent-Memory, AI-Agent-Plan, or AI-Agent-Skill."),
		"title":        schemaOf("string", "Human-readable document title."),
		"slug":         schemaOf("string", "URL-safe identifier; the key every other tool takes."),
		"created_at":   schemaOf("string", "RFC3339 creation time."),
		"timestamp":    schemaOf("string", "RFC3339 last-modified time (OKF canonical modified time)."),
		"description":  schemaOf("string", "One-line summary shown in indexes."),
		"resource":     schemaOf("string", "OKF canonical URI of what the concept is."),
		"source":       schemaOf("string", "Provenance: where the knowledge came from."),
		"version":      schemaOf("integer", "Current revision number. Pass this as 'loaded_version' when editing."),
		"edit_summary": schemaOf("string", "Summary of the most recent edit."),
		"tags":         schemaStringArray("Tags carried by the document, including status and memory-scope tags."),
		"archived_at":       schemaOf("string", "RFC3339 archival time; absent unless the document is archived."),
		"status":            schemaOf("string", "Lifecycle status. Plans and skills use a closed vocabulary (see get_status_tags); other documents may use any value or none."),
		"status_changed_at": schemaOf("string", "RFC3339 time a plan last changed lifecycle status; drives the auto-archive/auto-delete timers. Only present on AI-Agent-Plan documents."),
	}
	if withContent {
		props["content"] = schemaOf("string", "Full raw Markdown body.")
	}
	return schemaObject(props, "type", "title", "slug", "timestamp")
}

func documentLinkSchema() map[string]interface{} {
	return schemaObject(map[string]interface{}{
		"title": schemaOf("string", "Title of the linking document."),
		"slug":  schemaOf("string", "Slug of the linking document."),
	}, "title", "slug")
}

func searchOutputSchema() map[string]interface{} {
	hit := schemaObject(map[string]interface{}{
		"title":     schemaOf("string", "Title of the matching document."),
		"slug":      schemaOf("string", "Slug of the matching document."),
		"type":      schemaOf("string", "OKF document class of the hit."),
		"score":     schemaOf("number", "Bleve relevance score; higher is a better match."),
		"timestamp": schemaOf("string", "RFC3339 last-modified time."),
		"tags":      schemaStringArray("Tags carried by the matching document."),
		"snippets":  schemaStringArray("Matching excerpts as plain text, with matched terms in Markdown bold."),
	}, "title", "slug", "type", "score", "timestamp")

	return schemaObject(map[string]interface{}{
		"query":            schemaOf("string", "The query that was run."),
		"count":            schemaOf("integer", "Number of results returned."),
		"type":             schemaStringArray("Document types the search was restricted to; absent when unrestricted."),
		"tags":             schemaStringArray("Tags every result was required to carry; absent when unfiltered."),
		"include_archived": schemaOf("boolean", "Whether archived documents were included."),
		"results":          schemaArrayOf(hit, "Matches, highest scoring first."),
	}, "query", "count", "results")
}

func articleOutputSchema() map[string]interface{} {
	return schemaObject(map[string]interface{}{
		"article":   articleSchema(true),
		"backlinks": schemaArrayOf(documentLinkSchema(), "Documents whose body links here via a WikiLink."),
	}, "article", "backlinks")
}

func documentListOutputSchema(documentsDescription string) map[string]interface{} {
	return schemaObject(map[string]interface{}{
		"count":     schemaOf("integer", "Number of documents returned."),
		"documents": schemaArrayOf(articleSchema(false), documentsDescription),
	}, "count", "documents")
}

func backlinksOutputSchema() map[string]interface{} {
	return schemaObject(map[string]interface{}{
		"slug":      schemaOf("string", "The slug whose inbound links were requested."),
		"count":     schemaOf("integer", "Number of inbound links found."),
		"backlinks": schemaArrayOf(articleSchema(false), "Documents linking to the target, most recently updated first."),
	}, "slug", "count", "backlinks")
}

func historyOutputSchema() map[string]interface{} {
	revision := schemaObject(map[string]interface{}{
		"version":      schemaOf("integer", "Revision number, usable with revert_article_version."),
		"timestamp":    schemaOf("string", "RFC3339 time the revision was written."),
		"edit_summary": schemaOf("string", "Summary recorded with the edit."),
		"agent":        schemaOf("string", "Who made this revision, from the activity log. Absent when the log has no matching event. Self-reported by the client: a provenance hint, not an authentication claim."),
		"tool":         schemaOf("string", "MCP tool used for the edit; absent for edits made in the web UI."),
		"via":          schemaOf("string", "Transport that carried the edit: 'mcp' or 'api'."),
	}, "version", "timestamp")

	return schemaObject(map[string]interface{}{
		"slug":     schemaOf("string", "The article whose history was requested."),
		"count":    schemaOf("integer", "Number of stored revisions."),
		"source":   schemaOf("string", "The article's provenance: where its knowledge came from, as opposed to who wrote it."),
		"versions": schemaArrayOf(revision, "Revisions, newest version first."),
	}, "slug", "count", "versions")
}

// brokenLinkSchema describes one BrokenLinkRef. get_wiki_statistics and wiki_health both publish
// the type, and they published two independently worded copies of this schema until the `form`
// field made keeping them in step matter — one builder, so they cannot drift.
func brokenLinkSchema() map[string]interface{} {
	return schemaObject(map[string]interface{}{
		"from_slug":   schemaOf("string", "Document containing the broken link."),
		"target":      schemaOf("string", "Raw link target as the author wrote it: the text between the double brackets for a WikiLink, or the '/articles/<slug>' destination for a Markdown link."),
		"target_slug": schemaOf("string", "Slug the target resolves to; this is the page to create."),
		"form":        schemaOf("string", "Which syntax the link was written in: 'wikilink' for [[Target]], 'markdown' for [text](/articles/<slug>). They are repaired differently."),
	}, "from_slug", "target", "target_slug", "form")
}

func statisticsOutputSchema() map[string]interface{} {
	return schemaObject(map[string]interface{}{
		"total_articles":    schemaOf("integer", "Number of documents in the knowledge base."),
		"total_links":       schemaOf("integer", "Number of internal links scanned, in either link form."),
		"broken_link_count": schemaOf("integer", "Number of internal links with no destination."),
		"broken_links":      schemaArrayOf(brokenLinkSchema(), "Every internal link whose target does not exist."),
	}, "total_articles", "total_links", "broken_link_count", "broken_links")
}

func statusTagsOutputSchema() map[string]interface{} {
	return schemaObject(map[string]interface{}{
		"status_tags":         schemaStringArray("Union of all three lists, kept for backward compatibility."),
		"plan_status_tags":    schemaStringArray("The closed plan lifecycle vocabulary. Every AI-Agent-Plan carries exactly ONE of these and no other lifecycle word."),
		"skill_status_tags":   schemaStringArray("The closed skill lifecycle vocabulary. An AI-Agent-Skill carries at most ONE of these and no other lifecycle word."),
		"general_status_tags": schemaStringArray("Conventional lifecycle words for wiki articles and memories. Advisory only — those documents may carry any tags."),
	}, "status_tags", "plan_status_tags", "skill_status_tags", "general_status_tags")
}

func activityOutputSchema() map[string]interface{} {
	event := schemaObject(map[string]interface{}{
		"id":        schemaOf("string", "Event identifier."),
		"timestamp": schemaOf("string", "RFC3339 time the event occurred."),
		"source":    schemaOf("string", "'mcp' for AI tool calls, 'api' for web UI actions."),
		"action":    schemaOf("string", "create, edit, delete, read, or revert."),
		"tool":      schemaOf("string", "MCP tool name; empty for REST API actions."),
		"slug":      schemaOf("string", "Slug of the affected document."),
		"title":     schemaOf("string", "Title of the affected document."),
		"agent":     schemaOf("string", "Who performed the action."),
	}, "timestamp", "source", "action")

	return schemaObject(map[string]interface{}{
		"count":  schemaOf("integer", "Number of events returned."),
		"events": schemaArrayOf(event, "Matching events, oldest first."),
	}, "count", "events")
}

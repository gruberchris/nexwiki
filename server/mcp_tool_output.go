package server

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

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
	MemoryKind      string      `json:"memory_kind,omitempty"`
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
// over as a number rather than as prose. It also carries `content`: the body ships exactly once,
// and this is the copy, because a client reading a tool's structuredContent must get a complete
// result. See (*Server).toolReadArticle for why the text block is not that copy.
type ArticleOutput struct {
	Article   Article        `json:"article"`
	Backlinks []DocumentLink `json:"backlinks"`
	// SkippedDocumentCount and SkippedDocuments are the skipped-entries indicator: what the
	// backlink computation could not see. Present only when something was skipped; the two fields
	// and their meaning are documented on BacklinksOutput, which carries the same pair.
	SkippedDocumentCount int               `json:"skipped_document_count,omitempty"`
	SkippedDocuments     []SkippedDocument `json:"skipped_documents,omitempty"`
}

// DocumentListOutput is the shared payload of every list-shaped tool: list_articles,
// list_agent_memories, list_agent_plans, and list_agent_skills. One shape for all four means an
// agent learns to read a NexWiki listing once.
type DocumentListOutput struct {
	Count      int       `json:"count"`
	Documents  []Article `json:"documents"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// SkippedDocument is one entry a backlink scan left out, as the two tools that answer backlink
// questions report it: a file the scan could not read or parse, or a directory it could not list
// (reason "unreadable"), or a document not stored as <slug>.md directly in the article directory
// (reason "misplaced"). An unreadable entry may link to the target and the scan cannot say; a
// misplaced one links to it but is no backlink. The indicator exists so "no articles link to
// this" can never read as certain while an entry went unseen (#162). wiki_health carries the
// full detail — the unreadable entry's error, the misplaced document's remedy.
type SkippedDocument struct {
	Path   string `json:"path"`           // relative to the article directory, slash-separated; an unreadable directory's ends in /
	Slug   string `json:"slug,omitempty"` // misplaced only: declared in front matter or derived from the title; an unreadable file has none to report
	Reason string `json:"reason"`         // "unreadable" or "misplaced"
}

// BacklinksOutput is the `get_backlinks` payload.
type BacklinksOutput struct {
	Slug      string    `json:"slug"`
	Count     int       `json:"count"`
	Backlinks []Article `json:"backlinks"`
	// SkippedDocumentCount is how many entries the scan skipped: unreadable files and
	// directories plus the misplaced documents that link to the target. Any of them may hold an
	// inbound link the list does not show, so a nonzero count means Count is a lower bound, not
	// the answer. SkippedDocuments lists the first maxSkippedDocuments of them, sorted by path;
	// the count is always the total. Both are absent when nothing was skipped — the only case in
	// which Count is the whole truth.
	SkippedDocumentCount int               `json:"skipped_document_count,omitempty"`
	SkippedDocuments     []SkippedDocument `json:"skipped_documents,omitempty"`
}

// maxSkippedDocuments caps skipped_documents: the indicator's job is to say the backlink list may
// be incomplete, not to repeat wiki_health's full report, and the count carries the total anyway.
const maxSkippedDocuments = 20

// skippedDocuments turns a backlink scan's skipped entries into the capped skipped_documents list
// both backlink-answering tools publish. Sorted by path, like every health report, so two runs of
// the same wiki diff clean.
func skippedDocuments(scan backlinkScan) []SkippedDocument {
	if len(scan.unreadable) == 0 && len(scan.misplaced) == 0 {
		return nil
	}
	skipped := make([]SkippedDocument, 0, len(scan.unreadable)+len(scan.misplaced))
	for _, f := range scan.unreadable {
		skipped = append(skipped, SkippedDocument{Path: f.Path, Reason: "unreadable"})
	}
	for _, d := range scan.misplaced {
		skipped = append(skipped, SkippedDocument{Path: d.Path, Slug: d.Slug, Reason: "misplaced"})
	}
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Path < skipped[j].Path })
	if len(skipped) > maxSkippedDocuments {
		skipped = skipped[:maxSkippedDocuments]
	}
	return skipped
}

// skippedDocumentCount is the total the indicator reports. One helper, because get_backlinks and
// read_article must never disagree about the number.
func skippedDocumentCount(scan backlinkScan) int {
	return len(scan.unreadable) + len(scan.misplaced)
}

// skippedDocumentsNote is the prose half of the indicator: the line get_backlinks and read_article
// append when their scan skipped entries, so a text-only client gets the same warning the
// structured payload carries, with the pointer to wiki_health's full detail. Empty when nothing
// was skipped.
func skippedDocumentsNote(scan backlinkScan) string {
	var parts []string
	if n := len(scan.unreadable); n > 0 {
		parts = append(parts, fmt.Sprintf("%d unreadable %s", n, plural(n, "entry", "entries")))
	}
	if n := len(scan.misplaced); n > 0 {
		parts = append(parts, fmt.Sprintf("%d misplaced %s", n, plural(n, "document", "documents")))
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("Note: the backlink scan skipped %s that may link to this page; wiki_health lists them in detail.", strings.Join(parts, " and "))
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
	// Tool is the MCP tool used, or the web or lifecycle operation when one applies ("okf_import",
	// "plan_lifecycle"); empty for ordinary web UI edits.
	Tool string `json:"tool,omitempty"`
	// Via is what carried the edit: "mcp", "api" (the web UI), or "lifecycle" (the plan worker).
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
	TotalArticles int `json:"total_articles"`
	// UnreadableFileCount is how many article files the scan could not read or parse, plus
	// directories it could not list, each counted once. None of the other numbers include them, so
	// without it a wiki with broken files just looks smaller. wiki_health lists them, found by its
	// own call to the same ScanLinkGraph.
	UnreadableFileCount int `json:"unreadable_file_count"`
	// MisplacedDocumentCount is how many documents the scan left out because they are not stored as
	// <slug>.md directly in the article directory (see Storage.isCanonical). Like the unreadable
	// files, none of the other numbers include them, and wiki_health lists them.
	MisplacedDocumentCount int             `json:"misplaced_document_count"`
	TotalLinks             int             `json:"total_links"`
	BrokenLinkCount        int             `json:"broken_link_count"`
	BrokenLinks            []BrokenLinkRef `json:"broken_links"`
}

// StatusTagsOutput is the `get_status_tags` payload. Only plans and skills have a status field;
// status_tags remains the union of their vocabularies for backward compatibility.
type StatusTagsOutput struct {
	StatusTags []string `json:"status_tags"`
	// PlanStatusTags is the closed plan lifecycle vocabulary: every AI-Agent-Plan has exactly
	// one of these, and no other lifecycle word.
	PlanStatusTags []string `json:"plan_status_tags"`
	// SkillStatusTags is the closed skill lifecycle vocabulary: an AI-Agent-Skill has at most
	// one of these, and no other lifecycle word.
	SkillStatusTags []string `json:"skill_status_tags"`
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
		"type":              schemaOf("string", "OKF document class: Wiki, AI-Agent-Memory, AI-Agent-Plan, or AI-Agent-Skill."),
		"title":             schemaOf("string", "Human-readable document title."),
		"slug":              schemaOf("string", "URL-safe identifier; the key every other tool takes."),
		"created_at":        schemaOf("string", "RFC3339 creation time."),
		"timestamp":         schemaOf("string", "RFC3339 last-modified time (OKF canonical modified time)."),
		"description":       schemaOf("string", "One-line summary shown in indexes."),
		"resource":          schemaOf("string", "OKF canonical URI of what the concept is."),
		"source":            schemaOf("string", "Provenance: where the knowledge came from."),
		"version":           schemaOf("integer", "Current revision number. Pass this as 'loaded_version' when editing."),
		"edit_summary":      schemaOf("string", "Summary of the most recent edit."),
		"tags":              schemaStringArray("Tags carried by the document, including status and memory-scope tags."),
		"archived_at":       schemaOf("string", "RFC3339 archival time; absent unless the document is archived."),
		"status":            schemaOf("string", "Lifecycle status. Plans and skills use a closed vocabulary (see get_status_tags); other documents may use any value or none."),
		"status_changed_at": schemaOf("string", "RFC3339 time a plan last changed lifecycle status; drives the auto-archive/auto-delete timers. Only present on AI-Agent-Plan documents."),
		"memory_kind":       schemaOf("string", "What sort of fact a memory holds: project, reference, user, or feedback. Only present on AI-Agent-Memory documents, and absent on memories written before the kind axis existed (wiki_health lists those as unkinded_memories). Independent of the memory-<scope> tag, which is reach rather than kind."),
		"generated": schemaObject(map[string]interface{}{
			"by": schemaOf("string", "Agent or entity that generated the document."),
			"at": schemaOf("string", "RFC3339 timestamp when the document was generated."),
		}),
		"verified": schemaArrayOf(schemaObject(map[string]interface{}{
			"by": schemaOf("string", "Agent, user, or process that verified the document."),
			"at": schemaOf("string", "RFC3339 timestamp when verification occurred."),
		}), "List of verifications performed on this document."),
		"trust_tier": schemaOf("string", "Trust tier: unverified, machine-confirmed, or human-reviewed."),
		"sources": schemaArrayOf(schemaObject(map[string]interface{}{
			"id":            schemaOf("string", "Identifier for the source."),
			"resource":      schemaOf("string", "Canonical URI or URL of the source."),
			"title":         schemaOf("string", "Title of the source material."),
			"author":        schemaOf("string", "Author or creator of the source."),
			"usage_count":   schemaOf("integer", "Number of times this source has been referenced."),
			"last_modified": schemaOf("string", "RFC3339 timestamp of source last modification."),
			"usage_window": schemaObject(map[string]interface{}{
				"from": schemaOf("string", "RFC3339 start of usage window."),
				"to":   schemaOf("string", "RFC3339 end of usage window."),
			}),
		}), "Provenance sources this document derives from."),
		"usage_window": schemaObject(map[string]interface{}{
			"from": schemaOf("string", "RFC3339 start of usage window."),
			"to":   schemaOf("string", "RFC3339 end of usage window."),
		}),
		"stale_after": schemaOf("string", "RFC3339 timestamp after which the document is considered stale."),
		"is_stale":    schemaOf("boolean", "Whether the document has exceeded its stale_after timestamp."),
		"runtime":     schemaOf("string", "Execution environment or runtime specification for attested computation."),
		"parameters": schemaArrayOf(schemaObject(map[string]interface{}{
			"name":     schemaOf("string", "Parameter name."),
			"type":     schemaOf("string", "Parameter data type."),
			"required": schemaOf("boolean", "Whether the parameter is required."),
		}), "Declared parameters for attested computation."),
		"computation": schemaOf("string", "Deterministic logic, script, or calculation specification."),
		"executor": schemaObject(map[string]interface{}{
			"resource": schemaOf("string", "Canonical URI of the executor."),
			"receipt":  schemaStringArray("Receipt contract or expected receipt format."),
		}),
		"attester": schemaObject(map[string]interface{}{
			"resource": schemaOf("string", "Canonical URI of the attester."),
		}),
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
		"memory_kind":      schemaOf("string", "Memory kind the search was narrowed to; absent when unfiltered."),
		"include_archived": schemaOf("boolean", "Whether archived documents were included."),
		"results":          schemaArrayOf(hit, "Matches, highest scoring first."),
	}, "query", "count", "results")
}

func articleOutputSchema() map[string]interface{} {
	return schemaObject(map[string]interface{}{
		// articleSchema(true): read_article is the one tool whose structured payload carries the
		// body, so it is the one place the `content` property is advertised. The listing schemas
		// stay at articleSchema(false) — a listing is metadata, and a structured index that
		// inlined every body would be unusable.
		"article":                articleSchema(true),
		"backlinks":              schemaArrayOf(documentLinkSchema(), "Documents whose body links here via a WikiLink."),
		"skipped_document_count": schemaOf("integer", "Entries the backlink scan skipped: unreadable files and directories it could not read or list, plus misplaced documents that link to this page. Any of them may hold an inbound link backlinks does not show. Absent when nothing was skipped; wiki_health lists every entry in full."),
		"skipped_documents":      schemaArrayOf(skippedDocumentSchema(), "The first skipped entries, up to 20, sorted by path; skipped_document_count is the total. Absent when nothing was skipped."),
	}, "article", "backlinks")
}

func documentListOutputSchema(documentsDescription string) map[string]interface{} {
	return schemaObject(map[string]interface{}{
		"count":       schemaOf("integer", "Number of documents returned."),
		"documents":   schemaArrayOf(articleSchema(false), documentsDescription),
		"next_cursor": schemaOf("string", "Optional opaque cursor for retrieving the next page of results; absent when on the last page."),
	}, "count", "documents")
}

// skippedDocumentSchema describes one SkippedDocument. get_backlinks and read_article both publish
// the skipped-entries indicator, and one builder keeps the two wordings from drifting — the same
// reasoning that gave brokenLinkSchema its single home.
func skippedDocumentSchema() map[string]interface{} {
	return schemaObject(map[string]interface{}{
		"path":   schemaOf("string", "Path relative to the article directory, slash-separated. An unreadable directory ends in /, standing for every article in it."),
		"slug":   schemaOf("string", "The document's slug, declared in its front matter or derived from its title. Present only on misplaced entries; an unreadable file has no slug to report."),
		"reason": schemaOf("string", "Why the scan skipped it: 'unreadable' (could not be read, parsed, or listed) or 'misplaced' (not stored as <slug>.md directly in the article directory)."),
	}, "path", "reason")
}

func backlinksOutputSchema() map[string]interface{} {
	return schemaObject(map[string]interface{}{
		"slug":                   schemaOf("string", "The slug whose inbound links were requested."),
		"count":                  schemaOf("integer", "Number of inbound links found."),
		"backlinks":              schemaArrayOf(articleSchema(false), "Documents linking to the target, most recently updated first."),
		"skipped_document_count": schemaOf("integer", "Entries the scan skipped: unreadable files and directories it could not read or list, plus misplaced documents that link to the target. Any of them may hold an inbound link backlinks does not show, so a nonzero count means count is a lower bound. Absent when nothing was skipped; wiki_health lists every entry in full."),
		"skipped_documents":      schemaArrayOf(skippedDocumentSchema(), "The first skipped entries, up to 20, sorted by path; skipped_document_count is the total. Absent when nothing was skipped."),
	}, "slug", "count", "backlinks")
}

func historyOutputSchema() map[string]interface{} {
	revision := schemaObject(map[string]interface{}{
		"version":      schemaOf("integer", "Revision number, usable with revert_article_version."),
		"timestamp":    schemaOf("string", "RFC3339 time the revision was written."),
		"edit_summary": schemaOf("string", "Summary recorded with the edit."),
		"agent":        schemaOf("string", "Who made this revision, from the activity log. Absent when the log has no matching event. Self-reported by the client: a provenance hint, not an authentication claim."),
		"tool":         schemaOf("string", "MCP tool used for the edit, or the web or lifecycle operation when one applies (e.g. 'okf_import', 'plan_lifecycle'); absent for ordinary web UI edits."),
		"via":          schemaOf("string", "What carried the edit: 'mcp', 'api' (the web UI), or 'lifecycle' (the plan lifecycle worker)."),
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
		"total_articles":           schemaOf("integer", "Number of documents in the knowledge base."),
		"unreadable_file_count":    schemaOf("integer", "Article files that could not be read or parsed, plus directories that could not be listed, each directory counted once however many articles it holds. None are counted anywhere else; wiki_health lists them."),
		"misplaced_document_count": schemaOf("integer", "Documents not stored as <slug>.md directly in the article directory: in a subfolder, under a filename that differs from their slug, or with a slug not in slug form. They cannot be opened, and none are counted anywhere else; wiki_health lists them."),
		"total_links":              schemaOf("integer", "Number of internal links scanned, in either link form."),
		"broken_link_count":        schemaOf("integer", "Number of internal links with no destination."),
		"broken_links":             schemaArrayOf(brokenLinkSchema(), "Every internal link whose target does not exist."),
	}, "total_articles", "unreadable_file_count", "misplaced_document_count", "total_links", "broken_link_count", "broken_links")
}

func statusTagsOutputSchema() map[string]interface{} {
	return schemaObject(map[string]interface{}{
		"status_tags":       schemaStringArray("Union of both vocabularies, kept for backward compatibility."),
		"plan_status_tags":  schemaStringArray("The closed plan lifecycle vocabulary. Every AI-Agent-Plan has exactly ONE of these and no other lifecycle word."),
		"skill_status_tags": schemaStringArray("The closed skill lifecycle vocabulary. An AI-Agent-Skill has at most ONE of these. Wiki articles and memories have no status field at all."),
	}, "status_tags", "plan_status_tags", "skill_status_tags")
}

func activityOutputSchema() map[string]interface{} {
	event := schemaObject(map[string]interface{}{
		"id":        schemaOf("string", "Event identifier."),
		"timestamp": schemaOf("string", "RFC3339 time the event occurred."),
		"source":    schemaOf("string", "'mcp' for AI tool calls, 'api' for web UI actions, 'lifecycle' for the plan lifecycle worker."),
		"action":    schemaOf("string", "One of: "+strings.Join(activityLogActions, ", ")+"."),
		"tool":      schemaOf("string", "MCP tool name, or the web or lifecycle operation when one applies (e.g. 'delete_tag', 'plan_lifecycle'); empty for ordinary web UI actions."),
		"slug":      schemaOf("string", "Slug of the affected document."),
		"title":     schemaOf("string", "Title of the affected document."),
		"agent":     schemaOf("string", "Who performed the action."),
	}, "timestamp", "source", "action")

	return schemaObject(map[string]interface{}{
		"count":  schemaOf("integer", "Number of events returned."),
		"events": schemaArrayOf(event, "Matching events, oldest first."),
	}, "count", "events")
}

// OverviewOutput is the `get_wiki_overview` payload.
type OverviewOutput struct {
	TotalArticles  int               `json:"total_articles"`
	Articles       []Article         `json:"articles"`
	RecentActivity []LogEvent        `json:"recent_activity,omitempty"`
	Statistics     *StatisticsOutput `json:"statistics,omitempty"`
	StatusTags     *StatusTagsOutput `json:"status_tags,omitempty"`
}

func overviewOutputSchema() map[string]interface{} {
	event := schemaObject(map[string]interface{}{
		"id":        schemaOf("string", "Event identifier."),
		"timestamp": schemaOf("string", "RFC3339 time the event occurred."),
		"source":    schemaOf("string", "'mcp' for AI tool calls, 'api' for web UI actions, 'lifecycle' for the plan lifecycle worker."),
		"action":    schemaOf("string", "One of: "+strings.Join(activityLogActions, ", ")+"."),
		"tool":      schemaOf("string", "MCP tool name, or the web or lifecycle operation when one applies (e.g. 'delete_tag', 'plan_lifecycle'); empty for ordinary web UI actions."),
		"slug":      schemaOf("string", "Slug of the affected document."),
		"title":     schemaOf("string", "Title of the affected document."),
		"agent":     schemaOf("string", "Who performed the action."),
	}, "timestamp", "source", "action")

	return schemaObject(map[string]interface{}{
		"total_articles":  schemaOf("integer", "Total number of documents in the knowledge base."),
		"articles":        schemaArrayOf(articleSchema(false), "Compact index of documents in the knowledge base."),
		"recent_activity": schemaArrayOf(event, "Recent activity events."),
		"statistics":      statisticsOutputSchema(),
		"status_tags":     statusTagsOutputSchema(),
	}, "total_articles", "articles")
}


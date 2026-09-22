package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"
)

// This file holds the wiki article tools: search, read, list, create, edit, tag, delete, history, revert,
// and the legacy context overview. get_wiki_overview lives in mcp_tools_overview.go.
// Each tool pairs its JSON schema with its handler in one place, so the two can never
// drift apart. Registration order lives in mcp_tools.go.

// StringOrArray unmarshals either a single string or an array of strings into a slice.
type StringOrArray []string

func (s *StringOrArray) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*s = nil
		return nil
	}
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		if strings.TrimSpace(str) != "" {
			*s = []string{strings.TrimSpace(str)}
		} else {
			*s = nil
		}
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*s = arr
		return nil
	}
	return errors.New("expected string or array of strings")
}

// searchWikiTool is both the search and the index. It used to be two tools, search_wiki and
// list_articles, whose filters had drifted apart — the listing could narrow by status but not by
// memory kind, the search the other way round — and whose two schemas every session paid for in
// context. One tool with an optional query keeps both behaviors: the query picks the mode, and
// every filter means the same thing in either.
var searchWikiTool = toolDef{
	Schema: map[string]interface{}{
		"name": "search_wiki",
		"description": "Search or list documents (articles, memories, plans, skills). With 'query': scored full-text search with snippets. " +
			"Without: the document index, most recently updated first, with descriptions and statuses. Filters and cursor paging work in both; archived documents are listed but not searched by default. " +
			"include_history also scans every stored revision for the query's literal text, to verify a redaction.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Bleve query: wildcards, quoted phrases, boolean terms. Omit to list the index.",
				},
				"type": map[string]interface{}{
					"type":        "string",
					"description": "'articles', 'memories', 'plans', 'skills', or an OKF type name. Unknown values are an error.",
				},
				"tag": map[string]interface{}{
					"type":        "string",
					"description": "Required tag (case-insensitive).",
				},
				"status": map[string]interface{}{
					"type":        "string",
					"description": "Lifecycle status, e.g. 'implementing'; 'archived' matches every archived document.",
				},
				"memory_kind": map[string]interface{}{
					"type":        "string",
					"enum":        MemoryKinds,
					"description": "Only memories of this kind; 'user' and 'feedback' describe the operator.",
				},
				"include_archived": map[string]interface{}{
					"type":        "boolean",
					"description": "Include archived documents (default: true without 'query', false with it).",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Page size: default 50 without 'query'; 40, maximum 200, with it.",
				},
				"cursor": map[string]interface{}{
					"type":        "string",
					"description": "next_cursor from the previous page; keep the other arguments the same.",
				},
				"include_history": map[string]interface{}{
					"type":        "boolean",
					"description": "Needs 'query'. Also scan every stored revision, front matter included, for the query as a case-insensitive literal (surrounding quotes stripped); returns history_matches, empty when no revision contains it. Filters do not narrow this scan.",
				},
			},
		},
	},
	Output:   searchOutputSchema(),
	Handler:  (*Server).toolSearchWiki,
	Behavior: toolBehavior{Title: "Search Wiki", ReadOnly: true},
}

// defaultIndexPageSize is the index mode's page size when the caller sets no limit — list_articles'
// default, kept so a caller moving over from it gets the same pages. There is deliberately no
// ceiling: an index entry is metadata only, so a large page is long but never a copy of the wiki.
const defaultIndexPageSize = 50

// searchWikiArgs is search_wiki's argument object. IncludeArchived is a pointer because its
// default depends on the mode, so "not given" has to be told apart from an explicit false.
type searchWikiArgs struct {
	Query           string        `json:"query"`
	Type            StringOrArray `json:"type"`
	Tag             StringOrArray `json:"tag"`
	Tags            StringOrArray `json:"tags"`
	Status          string        `json:"status"`
	MemoryKind      string        `json:"memory_kind"`
	IncludeArchived *bool         `json:"include_archived"`
	Limit           int           `json:"limit"`
	Cursor          string        `json:"cursor"`
	IncludeHistory  bool          `json:"include_history"`
}

// searchFilters is search_wiki's filters after validation, shared by both modes so a filter can
// never mean one thing in a search and another in the index.
type searchFilters struct {
	types           map[string]bool // canonical document types; empty means every type
	typeNames       []string        // the type names as the caller spelled them, for the echo
	tags            []string
	status          string // lowercased; "archived" matches IsArchived rather than the field
	memoryKind      string
	includeArchived bool
}

// matchesStatus applies the status filter the way list_articles always has: "archived" means
// archived by any of IsArchived's three marks, since wiki articles and memories archive by tag and
// have no status field to match; anything else is a case-insensitive match on the field.
func (f searchFilters) matchesStatus(art *Article) bool {
	switch f.status {
	case "":
		return true
	case StatusArchived:
		return IsArchived(art)
	default:
		return strings.EqualFold(art.Status, f.status)
	}
}

// facetSuffix describes the applied filters so the agent can tell "no such knowledge" from "my
// filter excluded it" — the difference between giving up and retrying with a wider search.
// includeArchived is named only where it departs from the mode's default.
func (f searchFilters) facetSuffix(indexMode bool) string {
	var facets []string
	if len(f.typeNames) > 0 {
		facets = append(facets, "type: "+strings.Join(f.typeNames, ", "))
	}
	if len(f.tags) > 0 {
		facets = append(facets, "tags: "+strings.Join(f.tags, ", "))
	}
	if f.status != "" {
		facets = append(facets, "status: "+f.status)
	}
	if f.memoryKind != "" {
		facets = append(facets, "memory kind: "+f.memoryKind)
	}
	if f.includeArchived && !indexMode {
		facets = append(facets, "including archived")
	}
	if !f.includeArchived && indexMode {
		facets = append(facets, "excluding archived")
	}
	if len(facets) == 0 {
		return ""
	}
	return fmt.Sprintf(" [filtered by %s]", strings.Join(facets, "; "))
}

func (srv *Server) toolSearchWiki(args json.RawMessage) (interface{}, *JSONRPCError) {
	var a searchWikiArgs
	if e := decodeToolArgs(args, &a); e != nil {
		return nil, e
	}
	indexMode := strings.TrimSpace(a.Query) == ""
	// The history scan looks for the query text, so without one there is nothing to scan for.
	// Answering with the index instead would hand a redaction audit an answer to a question it
	// did not ask, and an empty history_matches it would read as "the text is gone".
	if a.IncludeHistory && indexMode {
		return nil, &JSONRPCError{Code: -32602, Message: "Invalid arguments: 'include_history' requires a non-empty 'query' — the literal text to look for in every stored revision."}
	}

	// Report a bad type name rather than silently returning nothing — an agent that typos
	// "memorys" would otherwise conclude the knowledge simply is not there. ResolveDocumentType
	// rather than ResolveSearchType, so Attested Computation documents can be asked for too.
	f := searchFilters{types: map[string]bool{}}
	var unknown []string
	for _, name := range a.Type {
		if strings.TrimSpace(name) == "" {
			continue
		}
		canonical := ResolveDocumentType(name)
		if canonical == "" {
			unknown = append(unknown, strings.TrimSpace(name))
			continue
		}
		f.types[canonical] = true
		f.typeNames = append(f.typeNames, name)
	}
	if len(unknown) > 0 {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: " + unknownDocumentTypeError(strings.Join(unknown, ", ")).Error()}}}, nil
	}
	// Same reasoning as the type check above: an unrecognized kind must be reported, not answered
	// with an empty result an agent would read as "no such knowledge".
	f.memoryKind = NormalizeMemoryKind(a.MemoryKind)
	if f.memoryKind != "" && !IsMemoryKind(f.memoryKind) {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf(
			"Error: '%s' is not a memory kind. Valid values: %s.", f.memoryKind, strings.Join(MemoryKinds, ", "))}}}, nil
	}
	f.tags = append(append([]string(nil), a.Tag...), a.Tags...)
	f.status = strings.ToLower(strings.TrimSpace(a.Status))

	// Archived documents default in for the index and out for a search, the defaults the two
	// tools had before they merged: list_articles listed everything, search_wiki hid archived
	// hits. Keeping both means neither tool's callers see different results for the same call.
	f.includeArchived = indexMode
	if a.IncludeArchived != nil {
		f.includeArchived = *a.IncludeArchived
	}
	// Asking for archived documents by status or by tag implies including them; otherwise
	// the archived filter would discard every document the caller asked for, with no
	// explanation. The tag test is storage's own (trimmed, case-insensitive — see
	// SearchArticlesWithOptions), so the include_archived echoed back is the one it applies.
	if f.status == StatusArchived {
		f.includeArchived = true
	}
	for _, tag := range f.tags {
		if strings.EqualFold(strings.TrimSpace(tag), StatusArchived) {
			f.includeArchived = true
		}
	}

	if indexMode {
		return srv.searchWikiIndex(a, f)
	}
	return srv.searchWikiQuery(a, f)
}

// searchWikiIndex is search_wiki without a query: the filtered document index, most recently
// updated first, paged by cursor. It is what list_articles was, down to the page size and the
// cursor encoding, so a caller moving over from it pages identically.
func (srv *Server) searchWikiIndex(a searchWikiArgs, f searchFilters) (interface{}, *JSONRPCError) {
	articles, err := srv.Storage.ListArticles()
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: srv.clientError(err)}}}, nil
	}

	tagFilter := lowercaseSet(f.tags)
	var filtered []Article
	for _, art := range articles {
		if len(f.types) > 0 && !f.types[art.Type] {
			continue
		}
		if !f.includeArchived && IsArchived(&art) {
			continue
		}
		if !f.matchesStatus(&art) || !matchesAllTags(art.Tags, tagFilter) {
			continue
		}
		// Kind narrows to memories of that kind, and to nothing else: no other class carries the
		// field, so a match on "" would silently widen the facet to the whole corpus.
		if f.memoryKind != "" && art.MemoryKind != f.memoryKind {
			continue
		}
		fullArt, ok := srv.Storage.getArticleForScan(art.Slug)
		if !ok {
			continue
		}
		item := *fullArt
		item.Content = ""
		filtered = append(filtered, item)
	}

	pageSize := defaultIndexPageSize
	if a.Limit > 0 {
		pageSize = a.Limit
	}
	page, nextCursor, rpcErr := paginate(filtered, a.Cursor, pageSize)
	if rpcErr != nil {
		return nil, rpcErr
	}

	facetStr := f.facetSuffix(true)
	var text string
	if len(page) == 0 {
		text = fmt.Sprintf("No matching articles found%s.\n", facetStr)
	} else {
		text = fmt.Sprintf("NexWiki Directory Index (%d matching articles, showing %d)%s:\n\n", len(filtered), len(page), facetStr)
		for i, art := range page {
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
			statusStr := ""
			if art.Status != "" {
				statusStr = fmt.Sprintf(" | Status: %s", art.Status)
			}
			text += fmt.Sprintf("[%d] %s (Slug: %s, Type: %s%s, Last Edited: %s%s)\n",
				i+1, art.Title, art.Slug, articleType, statusStr, art.Timestamp.Format("2006-01-02 15:04:05"), tagsStr)
			if art.Description != "" {
				text += fmt.Sprintf("    Summary: %s\n", art.Description)
			}
		}
		if nextCursor != "" {
			text += fmt.Sprintf("\nNext page cursor: %s\n", nextCursor)
		}
	}

	out := f.output(len(filtered), nextCursor)
	out.Documents = nonNilDocuments(page)
	return ToolResponse{
		Content:           []ToolContent{{Type: "text", Text: text}},
		StructuredContent: out,
	}, nil
}

// searchWikiQuery is search_wiki with a query: scored full-text search, paged by cursor.
//
// Paging works by fetching every hit the index will score — maxSearchLimit, the ceiling a single
// search has always had — filtering, and slicing one page out with the same offset cursor the
// index mode uses. Asking storage for exactly one page instead cannot work: its filters run after
// Bleve has scored and truncated, so page boundaries would shift with whatever each filter
// dropped, and a page could repeat or skip a hit. Over one fixed, filtered list the pages are
// disjoint and together are the whole result, and the first page is exactly what a call without a
// cursor always returned.
func (srv *Server) searchWikiQuery(a searchWikiArgs, f searchFilters) (interface{}, *JSONRPCError) {
	// No legacyQueryHeuristics: an agent searching its own second brain sees every document
	// type unless it explicitly narrows. Memories and plans are the point, not noise. The type
	// filter is applied below rather than handed to storage, which knows only the four search
	// types and would silently widen an Attested Computation filter to every type.
	results, err := srv.Storage.SearchArticlesWithOptions(a.Query, SearchOptions{
		Tags:                 f.tags,
		Limit:                maxSearchLimit,
		IncludeArchived:      f.includeArchived,
		MemoryKind:           f.memoryKind,
		skipFallbackSnippets: true,
	})
	bleveNote := ""
	if err != nil {
		if !a.IncludeHistory {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: srv.clientError(err)}}}, nil
		}
		// The history scan takes the query literally, so a query the Bleve parser rejects is still
		// a valid audit term. Report the index half as failed and run the scan anyway.
		results = nil
		bleveNote = fmt.Sprintf("Index search failed for this query (%s); the version history scan below takes it literally.\n", srv.clientError(err))
	}

	matched := make([]SearchResult, 0, len(results))
	for _, res := range results {
		if len(f.types) > 0 && !f.types[res.Type] {
			continue
		}
		// A search result carries no status, so the filter reads the cached metadata — only
		// when one was asked for.
		if f.status != "" {
			meta, err := srv.Storage.metaBySlug(res.Slug)
			if err != nil || !f.matchesStatus(meta) {
				continue
			}
		}
		matched = append(matched, res)
	}

	pageSize := a.Limit
	if pageSize <= 0 {
		pageSize = defaultSearchLimit
	}
	pageSize = min(pageSize, maxSearchLimit)
	page, nextCursor, rpcErr := paginate(matched, a.Cursor, pageSize)
	if rpcErr != nil {
		return nil, rpcErr
	}

	// Build the structured payload first, then render the prose from it, so the two halves of
	// the answer are the same data and cannot disagree. Snippets are finished here, for the
	// page alone: storage skipped the fallback read for fragment-less hits (see
	// skipFallbackSnippets), and nothing before this point depends on a snippet.
	hits := make([]SearchHit, 0, len(page))
	for _, res := range page {
		raw := res.Snippets
		if len(raw) == 0 {
			preview := ""
			if meta, err := srv.Storage.metaBySlug(res.Slug); err == nil {
				preview = meta.ContentPreview
			}
			raw = srv.Storage.fallbackSnippet(res.Slug, preview)
		}
		snippets := make([]string, 0, len(raw))
		for _, snippet := range raw {
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

	facetStr := f.facetSuffix(false)
	text := bleveNote
	if len(matched) == 0 {
		text += fmt.Sprintf("No documents found matching query: '%s'%s\n", a.Query, facetStr)
	} else {
		showing := ""
		if len(hits) < len(matched) {
			showing = fmt.Sprintf(", showing %d", len(hits))
		}
		text += fmt.Sprintf("Found %d matching documents in NexWiki%s%s:\n\n", len(matched), facetStr, showing)
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
		if nextCursor != "" {
			text += fmt.Sprintf("Next page cursor: %s\n", nextCursor)
		}
	}

	out := f.output(len(matched), nextCursor)
	out.Query = a.Query
	out.Results = hits

	if a.IncludeHistory {
		term := historySearchTerm(a.Query)
		matches, err := srv.Storage.SearchHistory(term)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error scanning version history: " + srv.clientError(err)}}}, nil
		}
		out.IncludeHistory = true
		out.HistoryMatchCount = len(matches)
		if len(matches) > maxHistoryMatches {
			matches = matches[:maxHistoryMatches]
		}
		out.HistoryMatches = &matches
		text += renderHistoryMatches(term, matches, out.HistoryMatchCount, facetStr != "")
	}

	return ToolResponse{
		Content:           []ToolContent{{Type: "text", Text: text}},
		StructuredContent: out,
	}, nil
}

// output starts the structured payload both modes share: the total, the next cursor, and the
// applied filters echoed back — including include_archived as it was actually applied, since its
// default depends on the mode.
func (f searchFilters) output(count int, nextCursor string) SearchOutput {
	return SearchOutput{
		Count:           count,
		NextCursor:      nextCursor,
		Types:           f.typeNames,
		Tags:            f.tags,
		Status:          f.status,
		MemoryKind:      f.memoryKind,
		IncludeArchived: f.includeArchived,
	}
}

// maxHistoryMatches caps the history_matches list. An audit term is normally rare; a common word
// would otherwise put every revision of the wiki into one response.
const maxHistoryMatches = 500

// historySearchTerm is the literal the history scan looks for: the query with surrounding
// whitespace and one pair of surrounding double quotes removed, since an agent reaching for an
// exact phrase will quote it the way the Bleve query syntax asks.
func historySearchTerm(query string) string {
	term := strings.TrimSpace(query)
	if len(term) >= 2 && strings.HasPrefix(term, `"`) && strings.HasSuffix(term, `"`) {
		term = term[1 : len(term)-1]
	}
	return term
}

// renderHistoryMatches is the prose half of history_matches.
func renderHistoryMatches(term string, matches []HistoryMatch, total int, filtered bool) string {
	var b strings.Builder
	b.WriteString("\n== Version history scan ==\n")
	fmt.Fprintf(&b, "Scanned every stored revision (history and live files) for %q as a case-insensitive literal substring, front matter included.\n", term)
	if filtered {
		b.WriteString("The type, tag, memory-kind, and archived filters above do not narrow this scan; it covers every document.\n")
	} else {
		b.WriteString("Filters never narrow this scan; it covers every document.\n")
	}
	if total == 0 {
		b.WriteString("No revision of any document contains it.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "%d revision(s) contain it:\n", total)
	for _, m := range matches {
		state := "earlier revision"
		if m.Current {
			state = "current"
		}
		fmt.Fprintf(&b, "- %s (slug: %s) version %d — %s\n", m.Title, m.Slug, m.Version, state)
	}
	if total > len(matches) {
		fmt.Fprintf(&b, "... %d more not listed; narrow the term.\n", total-len(matches))
	}
	return b.String()
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
		"description": "Retrieve the full raw Markdown content, front-matter configurations, and inbound backlinks of a document by its URL slug. The backlinks are every document linking to this one, in either internal link form — [[WikiLinks]] or [text](/articles/<slug>) — so read a page to see what references it before editing or deleting it. Optionally specify 'version' to load a historical revision.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"slug": map[string]interface{}{
					"type":        "string",
					"description": "The clean URL-safe slug of the target document.",
				},
				"version": map[string]interface{}{
					"type":        "integer",
					"description": "Optional historical revision number to load. Omit to load the latest version.",
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
		Slug    string `json:"slug"`
		Version int    `json:"version"`
	}
	var readArgs ReadArgs
	if e := decodeToolArgs(args, &readArgs); e != nil {
		return nil, e
	}
	if readArgs.Slug == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'slug' argument"}
	}

	var art *Article
	var err error
	if readArgs.Version > 0 {
		art, err = srv.Storage.GetArticleVersion(readArgs.Slug, readArgs.Version)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error loading article '%s' version %d: %s", readArgs.Slug, readArgs.Version, srv.clientError(err))}}}, nil
		}
	} else {
		art, err = srv.Storage.GetArticle(readArgs.Slug)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error loading article '%s': %s", readArgs.Slug, srv.clientError(err))}}}, nil
		}
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
	sourcesStr := ""
	if len(art.Sources) > 0 {
		var srcEntries []string
		for _, s := range art.Sources {
			entry := s.Resource
			if s.ID != "" && s.Title != "" {
				entry = fmt.Sprintf("[%s] %s (%s)", s.ID, s.Title, s.Resource)
			} else if s.ID != "" {
				entry = fmt.Sprintf("[%s] %s", s.ID, s.Resource)
			} else if s.Title != "" {
				entry = fmt.Sprintf("%s (%s)", s.Title, s.Resource)
			}
			srcEntries = append(srcEntries, entry)
		}
		sourcesStr = fmt.Sprintf("\nSources: %s", strings.Join(srcEntries, ", "))
	}

	tier := art.TrustTier
	if tier == "" {
		tier = art.DeriveTrustTier()
	}
	trustTierStr := fmt.Sprintf("\nTrust Tier: %s", formatTrustTier(tier))

	compStr := ""
	if art.Type == ContentTypeComputation {
		if art.Runtime != "" {
			compStr += fmt.Sprintf("\nRuntime: %s", art.Runtime)
		}
		if len(art.Parameters) > 0 {
			var pList []string
			for _, p := range art.Parameters {
				reqStr := "optional"
				if p.Required {
					reqStr = "required"
				}
				pList = append(pList, fmt.Sprintf("%s (%s, %s)", p.Name, p.Type, reqStr))
			}
			compStr += fmt.Sprintf("\nParameters: %s", strings.Join(pList, ", "))
		}
	}

	staleWarning := ""
	if art.IsStale || IsStale(art) {
		staleWarning = fmt.Sprintf("\n⚠️ STALE CONCEPT: this document passed its freshness expiration on %s", art.StaleAfter.Format("2006-01-02"))
	}

	// The front-matter configuration and full Markdown body as prose.
	//
	// Version is part of this header because it is the one field an agent must carry from a read
	// into edit_wiki_article as loaded_version.
	text := fmt.Sprintf("Type: %s\nTitle: %s\nSlug: %s\nVersion: %d\nCreated: %s\nUpdated: %s%s%s%s%s%s%s%s%s\n\n%s",
		art.Type, art.Title, art.Slug, art.Version, art.CreatedAt.Format(time.RFC3339), art.Timestamp.Format(time.RFC3339),
		descStr, resourceStr, sourceStr, sourcesStr, tagsStr, trustTierStr, compStr, staleWarning, art.Content)

	// Append inbound links for graph discoverability; never fail the read over a scan error.
	//
	// The scan itself, not GetBacklinks: a read is where an agent checks what references a page
	// before editing or deleting it, so the entries the walk skipped — unreadable files, misplaced
	// documents — are reported in the structured output and the note below rather than letting a
	// missing Linked from list read as "nothing references this" (#162).
	links := []DocumentLink{}
	out := ArticleOutput{Article: *art, Backlinks: links}
	if scan, blErr := srv.Storage.scanBacklinks(art.Slug); blErr == nil {
		if backlinks := scan.backlinks; len(backlinks) > 0 {
			// Every backlink, in the prose as in the structured payload. The prose used to stop
			// at 15 with "and N more", but read_article is now the only tool that answers "what
			// links here", and a text-only client given a truncated list cannot ask for the rest.
			refs := make([]string, 0, len(backlinks))
			for _, bl := range backlinks {
				links = append(links, DocumentLink{Title: bl.Title, Slug: bl.Slug})
				refs = append(refs, fmt.Sprintf("%s (%s)", bl.Title, bl.Slug))
			}
			text += fmt.Sprintf("\n\n---\nLinked from: %s", strings.Join(refs, ", "))
		}
		if note := skippedDocumentsNote(scan); note != "" {
			// Inside the Linked from block when there is one, its own block otherwise: a note
			// pasted onto the body's last line would read as article content.
			if len(scan.backlinks) > 0 {
				text += "\n" + note + "\n"
			} else {
				text += "\n\n---\n" + note + "\n"
			}
		}
		out.Backlinks = links
		out.SkippedDocumentCount = skippedDocumentCount(scan)
		out.SkippedDocuments = skippedDocuments(scan)
	}

	// The body ships in both content[0].text (for text-reading clients such as Claude Desktop,
	// Cursor, and Antigravity) and structuredContent.article.content (for structured clients
	// such as Claude Code).
	//
	// Historically, 0.13.0 removed the body from structuredContent to prevent duplication, which
	// broke Claude Code because Claude Code ignores content[0].text when outputSchema is declared.
	// Then a follow-up fix moved the body exclusively to structuredContent, which broke all standard
	// text-reading MCP clients.
	//
	// Per the MCP specification, content is REQUIRED on CallToolResult while structuredContent is
	// OPTIONAL, and tools returning structured content SHOULD provide text content for backward
	// compatibility. Populating both guarantees interop across the entire MCP client ecosystem.
	return ToolResponse{
		Content:           []ToolContent{{Type: "text", Text: text}},
		StructuredContent: out,
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
		"description": "Create a brand new wiki article. Set 'title' to the subject's human-readable name — never a tool name, an action verb, or a placeholder, since the slug is derived from it.' to understand formatting and style-guide check requirements. If it is already in your context, do not re-read it — call this tool.)",
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
				"sources": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"id": map[string]interface{}{
								"type":        "string",
								"description": "Identifier matching inline footnote citations (e.g. 'fn1').",
							},
							"resource": map[string]interface{}{
								"type":        "string",
								"description": "Canonical URI, URL, or filepath of the source.",
							},
							"title": map[string]interface{}{
								"type":        "string",
								"description": "Optional human-readable title of the source.",
							},
							"author": map[string]interface{}{
								"type":        "string",
								"description": "Optional author or creator of the source.",
							},
							"usage_count": map[string]interface{}{
								"type":        "integer",
								"description": "Optional count of times this source has been referenced.",
							},
							"last_modified": map[string]interface{}{
								"type":        "string",
								"description": "Optional ISO 8601 timestamp of source last modification.",
							},
						},
						"required": []string{"resource"},
					},
					"description": "Optional array of source objects with credibility signals (OKF v0.2 provenance).",
				},
				"stale_after": map[string]interface{}{
					"type":        "string",
					"description": "Optional ISO 8601 timestamp (e.g. '2026-12-31') after which the article's freshness expires.",
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
		Title       string       `json:"title"`
		Content     string       `json:"content"`
		Description string       `json:"description"`
		Source      string       `json:"source"`
		Resource    string       `json:"resource"`
		Tags        []string     `json:"tags"`
		EditSummary string       `json:"edit_summary"`
		Sources     *[]OKFSource `json:"sources"`
		StaleAfter  string       `json:"stale_after"`
	}
	var cArgs CreateArgs
	if e := decodeToolArgs(args, &cArgs); e != nil {
		return nil, e
	}
	if cArgs.Title == "" || cArgs.Content == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid 'title' or 'content' arguments"}
	}
	if resp := rejectToolArtifactTitle(cArgs.Title, "article"); resp != nil {
		return *resp, nil
	}

	slug := Slugify(cArgs.Title)
	if _, err := srv.Storage.GetArticle(slug); err == nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: an article with title '%s' (slug: '%s') already exists", cArgs.Title, slug)}}}, nil
	}

	tags := validateAndCleanUserTags(cArgs.Tags, nil, ContentTypeWiki)
	// Regular article creation always produces a Wiki document; reserved types are tool-only.
	// Secret scanning, at the MCP write chokepoint. See server/secrets.go.
	if refusal := secretRefusal("wiki article", cArgs.Content, cArgs.Description, cArgs.Source); refusal != nil {
		return *refusal, nil
	}
	secretNote := secretWarning(warnedSecrets(cArgs.Content, cArgs.Description, cArgs.Source))

	now := time.Now()
	overrides := ArticleOverrides{
		Generated: &OKFGenerated{
			By: "nexwiki/mcp",
			At: now,
		},
	}
	if cArgs.Sources != nil {
		overrides.Sources = cArgs.Sources
	}
	if strings.TrimSpace(cArgs.StaleAfter) != "" {
		t, err := parseISO8601(cArgs.StaleAfter)
		if err != nil {
			return nil, &JSONRPCError{Code: -32602, Message: fmt.Sprintf("Invalid 'stale_after' timestamp: %v", err)}
		}
		overrides.StaleAfter = &t
	}

	art, err := srv.Storage.SaveArticleWithOverrides("", cArgs.Title, cArgs.Content, cArgs.Description, cArgs.Source, cArgs.Resource, cArgs.EditSummary, tags, ContentTypeWiki, overrides)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error creating article: %s", srv.clientError(err))}}}, nil
	}

	respText := fmt.Sprintf("Success! Article '%s' created successfully.\nSlug: %s\nCreated At: %s\nVersion: %d\n",
		art.Title, art.Slug, art.CreatedAt.Format(time.RFC3339), art.Version)
	respText = secretNote + respText
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
					"description": "The active version number of the article as returned by read_article (helps detect multi-session edit collisions). Pass 0 only when read_article reported 0, which means the article predates versioning; passing 0 for a versioned article is rejected as a stale read.",
				},
				"edit_summary": map[string]interface{}{
					"type":        "string",
					"description": "Optional summary outlining what changed (e.g., 'Corrected spelling error').",
				},
				"sources": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"id": map[string]interface{}{
								"type":        "string",
								"description": "Identifier matching inline footnote citations (e.g. 'fn1').",
							},
							"resource": map[string]interface{}{
								"type":        "string",
								"description": "Canonical URI, URL, or filepath of the source.",
							},
							"title": map[string]interface{}{
								"type":        "string",
								"description": "Optional human-readable title of the source.",
							},
							"author": map[string]interface{}{
								"type":        "string",
								"description": "Optional author or creator of the source.",
							},
							"usage_count": map[string]interface{}{
								"type":        "integer",
								"description": "Optional count of times this source has been referenced.",
							},
							"last_modified": map[string]interface{}{
								"type":        "string",
								"description": "Optional ISO 8601 timestamp of source last modification.",
							},
						},
						"required": []string{"resource"},
					},
					"description": "Optional array of source objects. Omit to preserve existing sources, pass empty array [] to clear.",
				},
				"stale_after": map[string]interface{}{
					"type":        "string",
					"description": "Optional ISO 8601 timestamp after which the article is considered stale. Omit to preserve, pass empty string \"\" to clear.",
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
		Slug          string       `json:"slug"`
		Title         string       `json:"title"`
		Content       string       `json:"content"`
		Description   string       `json:"description"`
		Source        string       `json:"source"`
		Resource      *string      `json:"resource"`
		Tags          []string     `json:"tags"`
		LoadedVersion int          `json:"loaded_version"`
		EditSummary   string       `json:"edit_summary"`
		Sources       *[]OKFSource `json:"sources"`
		StaleAfter    *string      `json:"stale_after"`
	}
	var eArgs EditArgs
	if e := decodeToolArgs(args, &eArgs); e != nil {
		return nil, e
	}
	// loaded_version may be 0: that is the version read_article reports for an article written to
	// disk before versioning existed, and rejecting it made those articles uneditable through the
	// documented read-then-edit loop. It is not a way to skip optimistic locking — a 0 against an
	// article that does have a version is treated as a stale read by ApplyArticleEdit.
	if eArgs.Slug == "" || eArgs.Title == "" || eArgs.Content == "" || eArgs.LoadedVersion < 0 {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. Requires 'slug', 'title', 'content', and a non-negative 'loaded_version'"}
	}

	// Empty/omitted description and source preserve the existing values; resource already
	// uses pointer semantics (omit=preserve, ""=clear, value=replace). Nil tags preserve.
	edit := ArticleEdit{
		Title:         eArgs.Title,
		Content:       eArgs.Content,
		Resource:      eArgs.Resource,
		EditSummary:   eArgs.EditSummary,
		LoadedVersion: eArgs.LoadedVersion,
		Generated: &OKFGenerated{
			By: "nexwiki/mcp",
			At: time.Now(),
		},
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
	if eArgs.Sources != nil {
		edit.Sources = eArgs.Sources
		if len(*eArgs.Sources) == 0 && edit.Source == nil {
			empty := ""
			edit.Source = &empty
		}
	}
	if eArgs.StaleAfter != nil {
		val := strings.TrimSpace(*eArgs.StaleAfter)
		if val == "" {
			edit.StaleAfter = &time.Time{}
		} else {
			t, err := parseISO8601(val)
			if err != nil {
				return nil, &JSONRPCError{Code: -32602, Message: fmt.Sprintf("Invalid 'stale_after' timestamp: %v", err)}
			}
			edit.StaleAfter = &t
		}
	}

	// ApplyArticleEdit performs the version check and the write under one lock. Reading the
	// article, comparing versions, and saving as three separate steps let a concurrent writer
	// land in the gap — the guard would pass and still clobber the other session's edit.
	// Secret scanning, at the MCP write chokepoint. See server/secrets.go.
	if refusal := secretRefusal("wiki article", edit.Content, derefOr(edit.Description), derefOr(edit.Source)); refusal != nil {
		return *refusal, nil
	}
	secretNote := secretWarning(warnedSecrets(edit.Content, derefOr(edit.Description), derefOr(edit.Source)))

	art, err := srv.Storage.ApplyArticleEdit(eArgs.Slug, edit)
	switch {
	case errors.Is(err, ErrVersionConflict):
		if existing, gErr := srv.Storage.GetArticle(eArgs.Slug); gErr == nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: versionConflictMessage("article", eArgs.Slug, existing.Version, eArgs.LoadedVersion)}}}, nil
		}
		// The article changed under us and then could not be read back, so there is no version to
		// name. This is the only conflict where "re-read" is the honest instruction rather than a
		// reflex — everywhere else the server already knows the answer.
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: version conflict on '%s'. It was updated by another session and its current version could not be read back. Re-read it with read_article and retry once with the version that reports.", eArgs.Slug)}}}, nil
	case err != nil && strings.Contains(err.Error(), "article not found"):
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: article with slug '%s' not found", eArgs.Slug)}}}, nil
	case err != nil:
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error editing article: %s", srv.clientError(err))}}}, nil
	}

	respText := fmt.Sprintf("Success! Article '%s' (slug: %s) updated successfully.\nNew Version: %d\nLast Edited: %s\n",
		art.Title, art.Slug, art.Version, art.Timestamp.Format(time.RFC3339))
	respText = secretNote + respText
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

	cleanedTags := validateAndCleanUserTags(uArgs.Tags, existing.Tags, existing.Type)

	art, err := srv.Storage.UpdateArticleTags(uArgs.Slug, cleanedTags, uArgs.LoadedVersion, uArgs.EditSummary)
	if errors.Is(err, ErrVersionConflict) {
		// Re-read rather than reusing the `existing` fetched above: the conflict was detected
		// against a fresher read inside UpdateArticleTags, and naming a version that is already
		// stale would send the caller straight back into the loop this message exists to end.
		disk := uArgs.LoadedVersion
		if fresh, gErr := srv.Storage.GetArticle(uArgs.Slug); gErr == nil {
			disk = fresh.Version
		}
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: versionConflictMessage("article", uArgs.Slug, disk, uArgs.LoadedVersion)}}}, nil
	}
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error updating tags: %s", srv.clientError(err))}}}, nil
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
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error deleting article: %s", srv.clientError(err))}}}, nil
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
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error loading history for '%s': %s", hArgs.Slug, srv.clientError(err))}}}, nil
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
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Revert failed: %s", srv.clientError(err))}}}, nil
	}

	respText := fmt.Sprintf("Success! Article '%s' reverted successfully to version %d.\nNew active version: %d\nLast Edited: %s\n",
		art.Title, rArgs.Version, art.Version, art.Timestamp.Format(time.RFC3339))
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
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
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: srv.clientError(err)}}}, nil
	}

	grouped := make(map[string][]Article)
	for _, art := range articles {
		dir := getArticleDirectory(art.Type)
		grouped[dir] = append(grouped[dir], art)
	}

	// Pin `user` and `feedback` memories ahead of the rest of the memory listing.
	//
	// These two kinds are what an agent needs *regardless of the task it is about to do*: who it
	// is working with, and the corrections that person has already given it. Every other memory is
	// only relevant once the task is known. This is the orientation call the server's own
	// instructions tell an agent to make first, so it is the right place to put them.
	//
	// This reorders; it does not duplicate. A separate pinned block above the index was the
	// obvious alternative and is worse: get_context_overview exists to be cheap, and listing a
	// memory twice spends context to say one thing. Because nothing is displaced or repeated, the
	// pinned set cannot crowd out the index however large it grows, and needs no cap.
	//
	// Stable within each group: ListArticles has already sorted by recency, and sort.SliceStable
	// keeps that order inside the pinned half and the remainder alike.
	sort.SliceStable(grouped["aimemories"], func(i, j int) bool {
		return isPinnedMemoryKind(grouped["aimemories"][i].MemoryKind) &&
			!isPinnedMemoryKind(grouped["aimemories"][j].MemoryKind)
	})

	text := fmt.Sprintf("NexWiki Context Overview (%d articles total)\n", len(articles))
	text += "Each line: Title (slug) — summary <memory kind> [tags] (updated). Use read_article(slug) to load full content.\n\n"
	for _, sec := range sections {
		if filter != "" && filter != sec.filter {
			continue
		}
		entries := grouped[sec.dir]
		text += fmt.Sprintf("== %s (%d) ==\n", sec.label, len(entries))
		if sec.dir == "aimemories" && countPinnedMemories(entries) > 0 {
			text += "   (user and feedback memories are listed first: they say who you are working " +
				"with and what corrections they have already given, which applies to any task.)\n"
		}
		for _, art := range entries {
			summary := art.Description
			if summary == "" {
				summary = art.ContentPreview
			}
			line := fmt.Sprintf("- %s (%s)", art.Title, art.Slug)
			if summary != "" {
				line += " — " + summary
			}
			// Kind is rendered before tags because it is the axis that decides whether this
			// memory is worth reading now, and an agent scanning the index reads left to right.
			if art.MemoryKind != "" {
				line += fmt.Sprintf(" <%s>", art.MemoryKind)
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

func formatTrustTier(tier string) string {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case TrustTierHumanReviewed:
		return "Human-Reviewed"
	case TrustTierMachineConfirmed:
		return "Machine-Confirmed"
	case TrustTierUnverified:
		return "Unverified"
	default:
		if tier == "" {
			return "Unverified"
		}
		return tier
	}
}

var saveArticleTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "save_article",
		"description": "Create or update any document (wiki article, agent memory, plan, or skill). If 'slug' is provided and matches an existing document, it updates it; otherwise it creates a new document. 'content' is always a full replacement of the body — to change only metadata or status, pass the current body back unchanged. A [[WikiLink]] must name a document that exists in this wiki — never your own local memory files, instruction files, tools, or scratch paths; cite external references as plain URLs. Creating a memory — or changing a document's type to AI-Agent-Memory — requires 'memory_kind', 'description', and 'source'; an ordinary edit of an existing memory does not. Supports optimistic locking via 'loaded_version'. On an update, passing 'type' changes the document's type (omit it to keep the current one); an unknown type is an error. Redaction: set 'purge_history: true' on an update to keep only the revision this call writes and permanently delete every earlier revision — body and metadata — from version history. That is not a deletion of anything a reader sees today: the document, its slug, type, status, and backlinks are unchanged.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"title": map[string]interface{}{
					"type":        "string",
					"description": "Human-readable title of the document. Never use a bare tool verb.",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "The complete Markdown body. Replaces the existing body in full; it is never merged or appended.",
				},
				"slug": map[string]interface{}{
					"type":        "string",
					"description": "Optional slug. If provided and matches an existing document, updates/revises it; if omitted or not existing, creates a new document.",
				},
				"type": map[string]interface{}{
					"type":        "string",
					"description": "Document type: 'Wiki' (default on create), 'AI-Agent-Memory', 'AI-Agent-Plan', 'AI-Agent-Skill', or 'Attested Computation'. On an update, omit to keep the current type, or pass one to change it: status, memory_kind, memory_type and project_context are then applied against the new type. Moving into AI-Agent-Plan without a status enters at 'draft' (or keeps a status already valid for a plan); moving into Wiki or AI-Agent-Memory drops the lifecycle status; a plan or skill status the new type does not accept must be replaced with an explicit 'status'; leaving AI-Agent-Memory drops the memory-<scope> tags and memory_kind. An unknown value is an error, never a silent Wiki.",
					"enum":        []string{"Wiki", "AI-Agent-Memory", "AI-Agent-Plan", "AI-Agent-Skill", "Attested Computation"},
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "One-line summary, shown in list indexes and overview. Required when creating a memory; otherwise optional. Omit on an update to keep the current one.",
				},
				"source": map[string]interface{}{
					"type":        "string",
					"description": "Provenance: URL, document, ticket, or session context. Required when creating a memory; otherwise optional. Omit on an update to keep the current one.",
				},
				"status": map[string]interface{}{
					"type":        "string",
					"description": "Optional lifecycle status for plans (draft, implementing, blocked, completed, superseded, parked, evergreen, archived) or skills (draft, ready, archived).",
				},
				"tags": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Optional tags for topics and context. Lifecycle status belongs in 'status', not tags.",
				},
				"memory_kind": map[string]interface{}{
					"type":        "string",
					"enum":        MemoryKinds,
					"description": "Kind of an AI-Agent-Memory, required when creating a memory: 'project' (goals and constraints not derivable from the repo), 'reference' (a pointer to an external resource), 'user' (who the operator is), or 'feedback' (a correction the operator gave). Omit on an update to keep the current one.",
				},
				"memory_type": map[string]interface{}{
					"type":        "string",
					"description": "Optional scope for AI-Agent-Memory (e.g. 'nexwiki' or 'docker'). Generates 'memory-<memory_type>' tag.",
				},
				"project_context": map[string]interface{}{
					"type":        "string",
					"description": "Optional project context for AI-Agent-Plan. Generates custom project tag.",
				},
				"loaded_version": map[string]interface{}{
					"type":        "integer",
					"description": "Optional active version number from read_article to enforce optimistic concurrency locking on edits.",
				},
				"edit_summary": map[string]interface{}{
					"type":        "string",
					"description": "Optional revision log description summarizing the edit.",
				},
				"purge_history": map[string]interface{}{
					"type":        "boolean",
					"description": "Optional, update only; requires 'loaded_version'. After this save succeeds, permanently delete every earlier revision of the document from version history, keeping only the revision this call writes, and retitle the document's earlier activity-log entries to its current title. Use it to redact text (including description, source, tags, or title) from history. It is not a deletion of anything a reader sees today: the document, its slug, type, status, backlinks, and version counter are unchanged. The response lists the revisions removed. Verify with search_wiki(include_history: true).",
				},
			},
			"required": []string{"title", "content"},
		},
	},
	Output:   nil,
	Handler:  (*Server).toolSaveArticle,
	Behavior: toolBehavior{Title: "Save Article", ReadOnly: false, Destructive: true, Idempotent: false},
}

func (srv *Server) toolSaveArticle(args json.RawMessage) (interface{}, *JSONRPCError) {
	type SaveArgs struct {
		Title          string   `json:"title"`
		Content        string   `json:"content"`
		Slug           string   `json:"slug"`
		Type           string   `json:"type"`
		Description    string   `json:"description"`
		Source         string   `json:"source"`
		Status         string   `json:"status"`
		Tags           []string `json:"tags"`
		MemoryKind     string   `json:"memory_kind"`
		MemoryType     string   `json:"memory_type"`
		ProjectContext string   `json:"project_context"`
		LoadedVersion  *int     `json:"loaded_version"`
		EditSummary    string   `json:"edit_summary"`
		PurgeHistory   bool     `json:"purge_history"`
	}
	var sArgs SaveArgs
	if e := decodeToolArgs(args, &sArgs); e != nil {
		return nil, e
	}
	if strings.TrimSpace(sArgs.Title) == "" || strings.TrimSpace(sArgs.Content) == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'title' and 'content' are required."}
	}
	if resp := rejectToolArtifactTitle(sArgs.Title, "document"); resp != nil {
		return *resp, nil
	}

	if refusal := secretRefusal("document", sArgs.Content, sArgs.Description, sArgs.Source); refusal != nil {
		return *refusal, nil
	}
	secretNote := secretWarning(warnedSecrets(sArgs.Content, sArgs.Description, sArgs.Source))

	var existing *Article
	if strings.TrimSpace(sArgs.Slug) != "" {
		existing, _ = srv.Storage.GetArticle(strings.TrimSpace(sArgs.Slug))
		if existing == nil && sArgs.LoadedVersion != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: document with slug '%s' not found", sArgs.Slug)}}}, nil
		}
	}

	if existing != nil {
		// A purge deletes revisions, so it is only ever applied to revisions the caller has seen:
		// without loaded_version a concurrent edit could land in between and be purged unseen.
		if sArgs.PurgeHistory && sArgs.LoadedVersion == nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf(
				"Error: purge_history requires 'loaded_version' — read the document first (read_article(slug: %q)) and pass its version. You cannot purge revisions you have not seen.", existing.Slug)}}}, nil
		}
		if sArgs.LoadedVersion != nil {
			if existing.Version > 0 && *sArgs.LoadedVersion != existing.Version {
				return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: versionConflictMessage(existing.Type, existing.Slug, existing.Version, *sArgs.LoadedVersion)}}}, nil
			}
		}

		// The type is resolved first, and strictly, because every other classification argument
		// below is validated and applied against the type the document is becoming.
		docType, err := resolveRequestedType(sArgs.Type, existing.Type)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: " + err.Error()}}}, nil
		}
		var statusArg *string
		if sArgs.Status != "" {
			statusArg = &sArgs.Status
		}
		statusOverride, err := resolveTypeChangeStatus(existing.Type, existing.Status, docType, statusArg)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: " + err.Error()}}}, nil
		}

		var kindOverride *string
		if sArgs.MemoryKind != "" {
			mk := NormalizeMemoryKind(sArgs.MemoryKind)
			if err := ValidateMemoryKind(docType, mk, false); err != nil {
				return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: " + err.Error()}}}, nil
			}
			kindOverride = &mk
		}

		desc := existing.Description
		if sArgs.Description != "" {
			desc = sArgs.Description
		}
		src := existing.Source
		if sArgs.Source != "" {
			src = sArgs.Source
		}

		// A type change into the memory class is a memory coming into existence, so it passes the
		// same gate as a create. An ordinary edit of an existing memory does not: see
		// checkNewMemoryMetadata.
		if docType == ContentTypeMemory && normalizeType(existing.Type) != ContentTypeMemory {
			kind := sArgs.MemoryKind
			if kind == "" {
				kind = existing.MemoryKind
			}
			if err := checkNewMemoryMetadata(kind, desc, src); err != nil {
				return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: " + err.Error()}}}, nil
			}
		}

		var tags []string
		if sArgs.Tags != nil {
			switch docType {
			case ContentTypeMemory:
				var scopeTags []string
				if sArgs.MemoryType != "" {
					scopeTags = []string{MemoryScopeTagPrefix + Slugify(sArgs.MemoryType)}
				} else if existing.Type == ContentTypeMemory {
					for _, t := range existing.Tags {
						if strings.HasPrefix(t, MemoryScopeTagPrefix) {
							scopeTags = append(scopeTags, t)
						}
					}
				}
				tags = validateAndCleanUserTags(sArgs.Tags, scopeTags, docType)
			case ContentTypePlan:
				var contextTags []string
				if sArgs.ProjectContext != "" {
					if ctx := Slugify(sArgs.ProjectContext); ctx != "" {
						contextTags = append(contextTags, ctx)
					}
				}
				tags = append([]string(nil), contextTags...)
				seen := make(map[string]bool, len(contextTags))
				for _, t := range contextTags {
					seen[strings.ToLower(t)] = true
				}
				for _, t := range validateAndCleanUserTags(sArgs.Tags, nil, ContentTypePlan) {
					if lower := strings.ToLower(t); !seen[lower] {
						seen[lower] = true
						tags = append(tags, t)
					}
				}
			default:
				tags = validateAndCleanUserTags(sArgs.Tags, nil, docType)
			}
		} else {
			tags = append([]string(nil), existing.Tags...)
			// A memory-<scope> tag is tool-managed on a memory and stray data on anything else, so
			// a document leaving the memory class leaves its scope tags behind.
			if existing.Type == ContentTypeMemory && docType != ContentTypeMemory {
				tags = stripMemoryScopeTags(tags)
			}
			if sArgs.MemoryType != "" && docType == ContentTypeMemory {
				newScope := MemoryScopeTagPrefix + Slugify(sArgs.MemoryType)
				var withoutScope []string
				for _, t := range tags {
					if !strings.HasPrefix(t, MemoryScopeTagPrefix) {
						withoutScope = append(withoutScope, t)
					}
				}
				tags = validateAndCleanUserTags(withoutScope, []string{newScope}, docType)
			}
			if sArgs.ProjectContext != "" && docType == ContentTypePlan {
				if ctx := Slugify(sArgs.ProjectContext); ctx != "" {
					if !hasTag(tags, ctx) {
						tags = append(tags, ctx)
					}
				}
			}
		}

		if err := ValidateStatusFreeTags(docType, tags); err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: " + err.Error()}}}, nil
		}

		summary := sArgs.EditSummary
		if summary == "" {
			summary = fmt.Sprintf("Updated %s", existing.Title)
			if sArgs.PurgeHistory {
				// The kept revision is the only one left, so its default summary must not repeat
				// the old title the purge may exist to remove.
				summary = fmt.Sprintf("Updated %s; earlier revisions purged", sArgs.Title)
			}
		}

		overrides := ArticleOverrides{
			KeepSlug:   (sArgs.Title == existing.Title) || (Slugify(sArgs.Title) == existing.Slug),
			Status:     statusOverride,
			MemoryKind: kindOverride,
			Generated:  &OKFGenerated{By: "nexwiki/mcp", At: time.Now()},
		}

		art, err := srv.Storage.SaveArticleWithOverrides(existing.Slug, sArgs.Title, sArgs.Content, desc, src, existing.Resource, summary, tags, docType, overrides)
		if err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error saving article: %s", srv.clientError(err))}}}, nil
		}

		respText := fmt.Sprintf("Success! Document '%s' (slug: %s) updated successfully.\n%sNew Version: %d\nLast Edited: %s\n",
			art.Title, art.Slug, typeChangeNote(existing.Type, art.Type), art.Version, art.Timestamp.Format(time.RFC3339))
		respText = secretNote + respText

		if sArgs.PurgeHistory {
			report, err := srv.purgeDocumentHistory(art)
			if err != nil {
				return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: respText + fmt.Sprintf(
					"\nError: the save succeeded, but purging the earlier revisions failed: %s. Retry with save_article(purge_history: true) and loaded_version %d.\n",
					srv.clientError(err), art.Version)}}}, nil
			}
			respText += report.text()
		}
		return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
	}

	if sArgs.PurgeHistory {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: purge_history applies only to an update of an existing document: pass the document's 'slug' and the 'loaded_version' you read. A new document has no earlier revisions to purge."}}}, nil
	}

	docType, err := resolveRequestedType(sArgs.Type, ContentTypeWiki)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: " + err.Error()}}}, nil
	}
	if docType == ContentTypeMemory {
		if err := checkNewMemoryMetadata(sArgs.MemoryKind, sArgs.Description, sArgs.Source); err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: " + err.Error()}}}, nil
		}
	}

	targetSlug := Slugify(sArgs.Title)
	if _, err := srv.Storage.GetArticle(targetSlug); err == nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: an article with slug '%s' already exists. Pass 'slug' to update an existing article.", targetSlug)}}}, nil
	}

	var statusOverride *string
	st := NormalizeStatus(sArgs.Status)
	if docType == ContentTypePlan && st == "" {
		st = DefaultPlanStatus
	}
	if st != "" {
		if err := ValidateStatus(docType, st); err != nil {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: " + err.Error()}}}, nil
		}
		statusOverride = &st
	}

	var kindOverride *string
	if docType == ContentTypeMemory {
		mk := NormalizeMemoryKind(sArgs.MemoryKind)
		if mk != "" {
			if err := ValidateMemoryKind(docType, mk, false); err != nil {
				return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: " + err.Error()}}}, nil
			}
			kindOverride = &mk
		}
	}

	var tags []string
	switch docType {
	case ContentTypeMemory:
		var scopeTags []string
		if sArgs.MemoryType != "" {
			scopeTags = []string{MemoryScopeTagPrefix + Slugify(sArgs.MemoryType)}
		}
		tags = validateAndCleanUserTags(sArgs.Tags, scopeTags, docType)
	case ContentTypePlan:
		var contextTags []string
		if sArgs.ProjectContext != "" {
			if ctx := Slugify(sArgs.ProjectContext); ctx != "" {
				contextTags = append(contextTags, ctx)
			}
		}
		tags = append([]string(nil), contextTags...)
		seen := make(map[string]bool, len(contextTags))
		for _, t := range contextTags {
			seen[strings.ToLower(t)] = true
		}
		for _, t := range validateAndCleanUserTags(sArgs.Tags, nil, ContentTypePlan) {
			if lower := strings.ToLower(t); !seen[lower] {
				seen[lower] = true
				tags = append(tags, t)
			}
		}
	default:
		tags = validateAndCleanUserTags(sArgs.Tags, nil, docType)
	}
	if err := ValidateStatusFreeTags(docType, tags); err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: "Error: " + err.Error()}}}, nil
	}

	summary := sArgs.EditSummary
	if summary == "" {
		summary = fmt.Sprintf("Created %s", sArgs.Title)
	}

	overrides := ArticleOverrides{
		Status:     statusOverride,
		MemoryKind: kindOverride,
		Generated:  &OKFGenerated{By: "nexwiki/mcp", At: time.Now()},
	}

	art, err := srv.Storage.SaveArticleWithOverrides("", sArgs.Title, sArgs.Content, sArgs.Description, sArgs.Source, "", summary, tags, docType, overrides)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error creating article: %s", srv.clientError(err))}}}, nil
	}

	respText := fmt.Sprintf("Success! Document '%s' created successfully.\nSlug: %s\nType: %s\nCreated At: %s\nVersion: %d\n",
		art.Title, art.Slug, art.Type, art.CreatedAt.Format(time.RFC3339), art.Version)
	respText = secretNote + respText
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}

var appendArticleTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "append_article",
		"description": "Append text or logs to the end of an existing document without modifying its metadata, status, or tags. Supports optimistic locking via 'loaded_version'.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"slug": map[string]interface{}{
					"type":        "string",
					"description": "The unique URL-safe slug of the document to append to.",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "The Markdown text to append to the end of the document.",
				},
				"loaded_version": map[string]interface{}{
					"type":        "integer",
					"description": "Optional active version number to detect concurrent edit collisions.",
				},
				"edit_summary": map[string]interface{}{
					"type":        "string",
					"description": "Optional summary outlining what details were appended.",
				},
			},
			"required": []string{"slug", "content"},
		},
	},
	Output:   nil,
	Handler:  (*Server).toolAppendArticle,
	Behavior: toolBehavior{Title: "Append Article", ReadOnly: false, Destructive: false, Idempotent: false},
}

func (srv *Server) toolAppendArticle(args json.RawMessage) (interface{}, *JSONRPCError) {
	type AppendArgs struct {
		Slug            string `json:"slug"`
		Content         string `json:"content"`
		ContentToAppend string `json:"content_to_append"`
		LoadedVersion   *int   `json:"loaded_version"`
		EditSummary     string `json:"edit_summary"`
	}
	var aArgs AppendArgs
	if e := decodeToolArgs(args, &aArgs); e != nil {
		return nil, e
	}
	content := aArgs.Content
	if content == "" {
		content = aArgs.ContentToAppend
	}
	if aArgs.Slug == "" || content == "" {
		return nil, &JSONRPCError{Code: -32602, Message: "Missing or invalid arguments. 'slug' and 'content' are required."}
	}

	existing, err := srv.Storage.GetArticle(aArgs.Slug)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error: article with slug '%s' not found", aArgs.Slug)}}}, nil
	}

	if aArgs.LoadedVersion != nil {
		if existing.Version > 0 && *aArgs.LoadedVersion != existing.Version {
			return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: versionConflictMessage(existing.Type, existing.Slug, existing.Version, *aArgs.LoadedVersion)}}}, nil
		}
	}

	newContent := existing.Content
	if newContent != "" {
		newContent += "\n\n" + content
	} else {
		newContent = content
	}

	summary := aArgs.EditSummary
	if summary == "" {
		summary = fmt.Sprintf("Appended content to %s", existing.Title)
	}

	if refusal := secretRefusal("document", content, "", ""); refusal != nil {
		return *refusal, nil
	}
	secretNote := secretWarning(warnedSecrets(content, "", ""))

	art, err := srv.Storage.SaveArticleWithOverrides(existing.Slug, existing.Title, newContent, existing.Description, existing.Source, existing.Resource, summary, existing.Tags, existing.Type, ArticleOverrides{KeepSlug: true})
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error appending to article: %s", srv.clientError(err))}}}, nil
	}

	respText := fmt.Sprintf("Success! Appended content to '%s' (version: %d, edited: %s).\n",
		art.Title, art.Version, art.Timestamp.Format(time.RFC3339))
	respText = secretNote + respText
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}

var deleteArticleTool = toolDef{
	Schema: map[string]interface{}{
		"name":        "delete_article",
		"description": "Permanently delete any document (wiki article, agent memory, plan, or skill) by slug. Irreversible: it destroys the document's version history and assets. It refuses, deleting nothing, while other documents link to the target (naming every one) or when the inbound-link check is incomplete, unless 'break_links: true' is passed; with it, the delete proceeds and lists the documents whose links it broke. A last resort — to change a document's type, pass 'type' to save_article; to remove text from history, use save_article with 'purge_history: true'.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"slug": map[string]interface{}{
					"type":        "string",
					"description": "The unique URL-safe slug of the document to delete.",
				},
				"break_links": map[string]interface{}{
					"type":        "boolean",
					"description": "Optional, default false. Delete even though other documents link to the target, or the inbound-link check could not see every document; their links to it become broken. Prefer fixing the links first.",
				},
			},
			"required": []string{"slug"},
		},
	},
	Output:   nil,
	Handler:  (*Server).toolDeleteArticle,
	Behavior: toolBehavior{Title: "Delete Article", ReadOnly: false, Destructive: true, Idempotent: true},
}

func (srv *Server) toolDeleteArticle(args json.RawMessage) (interface{}, *JSONRPCError) {
	type DelArgs struct {
		Slug       string `json:"slug"`
		BreakLinks bool   `json:"break_links"`
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

	// The same scan read_article runs, so the refusal names exactly the documents a read of the
	// target lists. A delete cannot be undone, so an incomplete scan — a failed walk, or entries it
	// skipped (#162) — is refused too: an empty backlink list is only "nothing links here" when
	// the walk saw everything. break_links: true is the agent saying it has looked and accepts
	// the breakage. Self-links never block: scanBacklinks does not count a document's own file.
	scan, scanErr := srv.Storage.scanBacklinks(existing.Slug)
	incomplete := ""
	if scanErr != nil {
		incomplete = fmt.Sprintf("The inbound-link check failed (%s), so it cannot say whether other documents link to '%s'.", srv.clientError(scanErr), existing.Slug)
	} else if n := skippedDocumentCount(scan); n > 0 {
		incomplete = fmt.Sprintf("The inbound-link check was incomplete: it skipped %d %s that may link to '%s':\n%s",
			n, plural(n, "entry", "entries"), existing.Slug, skippedDocumentsList(scan))
	}
	linkers := make([]string, 0, len(scan.backlinks))
	for _, bl := range scan.backlinks {
		linkers = append(linkers, fmt.Sprintf("- %s (%s)", bl.Title, bl.Slug))
	}

	if !dArgs.BreakLinks && (len(linkers) > 0 || incomplete != "") {
		var msg strings.Builder
		msg.WriteString("Refused: nothing was deleted.")
		if len(linkers) > 0 {
			fmt.Fprintf(&msg, " %d %s link to '%s', and deleting it would break those links:\n%s\n",
				len(linkers), plural(len(linkers), "document", "documents"), existing.Slug, strings.Join(linkers, "\n"))
		} else {
			msg.WriteString("\n")
		}
		if incomplete != "" {
			msg.WriteString(incomplete + "\n")
		}
		if len(linkers) > 0 {
			msg.WriteString("Fix or remove those links first")
		} else {
			msg.WriteString("Resolve what the check could not see first")
		}
		msg.WriteString(" (wiki_health reports unreadable and misplaced entries in detail), or call delete_article again with break_links: true to delete anyway.\n")
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: msg.String()}}}, nil
	}

	err = srv.Storage.DeleteArticle(existing.Slug)
	if err != nil {
		return ToolResponse{IsError: true, Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error deleting article: %s", srv.clientError(err))}}}, nil
	}

	respText := fmt.Sprintf("Success! Document with slug '%s' has been permanently deleted from disk along with all history backups and media assets.\n", existing.Slug)
	if len(linkers) > 0 {
		respText += fmt.Sprintf("These %d %s now hold broken links to '%s':\n%s\n",
			len(linkers), plural(len(linkers), "document", "documents"), existing.Slug, strings.Join(linkers, "\n"))
	}
	if incomplete != "" {
		respText += incomplete + "\n"
		if scanErr == nil {
			respText += "Any of those may now hold a broken link too.\n"
		}
	}
	return ToolResponse{Content: []ToolContent{{Type: "text", Text: respText}}}, nil
}

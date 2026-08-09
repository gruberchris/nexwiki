package server

import "strings"

// Search result-count bounds. The default keeps a tool response readable in an agent's context
// window; the ceiling stops a single call from dumping the whole wiki into it.
const (
	defaultSearchLimit = 40
	maxSearchLimit     = 200
)

// SearchOptions expresses what a caller wants back from a search, rather than inferring it from
// the words in the query.
//
// The zero value is the strictest, most agent-friendly form: no type filter, no tag filter,
// default limit, archived documents excluded, and *all* document types eligible. Callers that
// want the human sidebar's behavior opt into legacyQueryHeuristics.
type SearchOptions struct {
	// Types restricts results to these document types. Accepts OKF type names ("AI-Agent-Memory")
	// or the friendly aliases used elsewhere in the tool surface ("memories", "plans", "skills",
	// "articles"). Empty means every type is eligible.
	Types []string

	// Tags requires a result to carry *all* of these tags (case-insensitive). Empty means no
	// tag filtering. Conjunctive rather than disjunctive because narrowing is the useful
	// operation — "the plan tagged wip for project x" — and OR is already expressible in the
	// Bleve query string.
	Tags []string

	// Limit caps returned results. Zero uses defaultSearchLimit; anything above maxSearchLimit
	// is clamped to it.
	Limit int

	// IncludeArchived returns archived documents, which are otherwise filtered out.
	IncludeArchived bool

	// legacyQueryHeuristics restores the pre-facet behavior used by the human-facing sidebar and
	// REST endpoint: agent documents and archived pages are hidden unless the *query text*
	// happens to mention them. It is deliberately unexported — it exists to keep the browser UI
	// bit-identical, and is exactly the behavior the facets above replace for agent callers.
	legacyQueryHeuristics bool
}

// allowsArchived reports whether an archived document should survive filtering. A document counts
// as archived either by its archived_at timestamp or by carrying the "archived" tag.
func (o SearchOptions) allowsArchived(art *Article, queryLower string) bool {
	isArchived := !art.ArchivedAt.IsZero()
	if !isArchived {
		for _, tag := range art.Tags {
			if strings.EqualFold(tag, "archived") {
				isArchived = true
				break
			}
		}
	}
	if !isArchived {
		return true
	}
	if o.IncludeArchived {
		return true
	}
	// Legacy affordance: the browser UI has no archived toggle, so typing "archived" in the
	// search box is the only way a human can surface them.
	return o.legacyQueryHeuristics && strings.Contains(queryLower, "archived")
}

// allowsType reports whether a document's OKF type survives filtering.
func (o SearchOptions) allowsType(art *Article, typeFilter map[string]bool, queryStr, queryLower string) bool {
	if len(typeFilter) > 0 {
		return typeFilter[art.Type]
	}

	if !o.legacyQueryHeuristics {
		// Facet-based callers (agents) see every type unless they asked to narrow. An agent's
		// memories and plans are the point of the second brain, not noise to be hidden.
		return true
	}

	// --- legacy human-facing behavior, preserved verbatim -------------------------------
	if art.Type == ContentTypeWiki {
		return true
	}
	// A query naming the doc class opts every agent document back in.
	if strings.Contains(queryLower, "aiagent") || strings.Contains(queryLower, "ai-agent") {
		return true
	}
	// Explicitly searching for the doc by slug/title name (exact match).
	if strings.EqualFold(art.Slug, queryStr) || strings.EqualFold(art.Title, queryStr) {
		return true
	}
	switch art.Type {
	case ContentTypeSkill:
		return strings.Contains(queryLower, "skill")
	case ContentTypePlan:
		return strings.Contains(queryLower, "plan")
	case ContentTypeMemory:
		return strings.Contains(queryLower, "memory") || strings.Contains(queryLower, "memories")
	}
	return false
}

// searchTypeAliases maps the friendly names used across the tool surface (get_context_overview
// uses the same vocabulary) onto OKF document types, so an agent never has to spell out
// "AI-Agent-Memory" to filter for memories.
var searchTypeAliases = map[string]string{
	"article":  ContentTypeWiki,
	"articles": ContentTypeWiki,
	"wiki":     ContentTypeWiki,
	"memory":   ContentTypeMemory,
	"memories": ContentTypeMemory,
	"plan":     ContentTypePlan,
	"plans":    ContentTypePlan,
	"skill":    ContentTypeSkill,
	"skills":   ContentTypeSkill,
}

// normalizeTypeFilter resolves caller-supplied type names — aliases or canonical OKF types — into
// a set of canonical types. Unrecognized names are dropped; ValidateSearchTypes reports them to
// the caller instead of silently returning nothing.
func normalizeTypeFilter(types []string) map[string]bool {
	if len(types) == 0 {
		return nil
	}
	resolved := make(map[string]bool, len(types))
	for _, t := range types {
		if canonical := ResolveSearchType(t); canonical != "" {
			resolved[canonical] = true
		}
	}
	return resolved
}

// ResolveSearchType maps one type name or alias to its canonical OKF type, or "" if unrecognized.
func ResolveSearchType(name string) string {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	if trimmed == "" {
		return ""
	}
	if canonical, ok := searchTypeAliases[trimmed]; ok {
		return canonical
	}
	// Accept the canonical OKF type spelled out, case-insensitively.
	for _, canonical := range []string{ContentTypeWiki, ContentTypeMemory, ContentTypePlan, ContentTypeSkill} {
		if strings.EqualFold(trimmed, canonical) {
			return canonical
		}
	}
	return ""
}

// ValidateSearchTypes returns the names that do not resolve to a known document type, so a caller
// can be told it made a typo rather than being handed an empty result set.
func ValidateSearchTypes(types []string) []string {
	var unknown []string
	for _, t := range types {
		if strings.TrimSpace(t) == "" {
			continue
		}
		if ResolveSearchType(t) == "" {
			unknown = append(unknown, t)
		}
	}
	return unknown
}

// SearchTypeNames lists the accepted friendly type names, for error messages and tool schemas.
func SearchTypeNames() []string {
	return []string{"articles", "memories", "plans", "skills"}
}

// lowercaseSet builds a case-insensitive lookup set, skipping blanks.
func lowercaseSet(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]bool, len(values))
	for _, v := range values {
		if trimmed := strings.ToLower(strings.TrimSpace(v)); trimmed != "" {
			set[trimmed] = true
		}
	}
	return set
}

// matchesAllTags reports whether a document carries every required tag.
func matchesAllTags(articleTags []string, required map[string]bool) bool {
	if len(required) == 0 {
		return true
	}
	present := lowercaseSet(articleTags)
	for tag := range required {
		if !present[tag] {
			return false
		}
	}
	return true
}

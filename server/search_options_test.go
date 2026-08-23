package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// seedSearchCorpus creates one document of each type, all sharing a distinctive term, so a single
// query can prove which types a search surfaces.
func seedSearchCorpus(t *testing.T) *Storage {
	t.Helper()
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	docs := []struct {
		title    string
		docType  string
		tags     []string
		contents string
	}{
		{"Elasticsearch Notes", ContentTypeWiki, []string{"database"}, "zqterm comparing search engines"},
		{"Bleve Decision Record", ContentTypeMemory, []string{"memory-nexwiki"}, "zqterm we chose Bleve over Elasticsearch for the zero-dependency constraint"},
		{"Search Rework Plan", ContentTypePlan, []string{"retrieval"}, "zqterm rework the retrieval layer"},
		{"Search Tuning Skill", ContentTypeSkill, []string{"indexing"}, "zqterm how to tune the index"},
	}

	for _, d := range docs {
		art, err := storage.SaveArticle("", d.title, d.contents, "", "", "", "seed", d.tags, d.docType)
		if err != nil {
			t.Fatalf("SaveArticle(%s) failed: %v", d.title, err)
		}
		if err := storage.IndexArticle(art); err != nil {
			t.Fatalf("IndexArticle(%s) failed: %v", d.title, err)
		}
	}
	return storage
}

func resultTypes(results []SearchResult) map[string]bool {
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		seen[r.Type] = true
	}
	return seen
}

// TestAgentSearchFindsMemoriesWithoutMagicWords is the defect this change exists to fix.
//
// Previously a non-Wiki document was only returned if the *query text* happened to contain
// "memory"/"plan"/"skill". So a memory recording "we chose Bleve over Elasticsearch" was invisible
// to search_wiki("elasticsearch"), and the agent would re-derive a decision it had already stored
// — the exact failure a second brain exists to prevent.
func TestAgentSearchFindsMemoriesWithoutMagicWords(t *testing.T) {
	storage := seedSearchCorpus(t)

	results, err := storage.SearchArticlesWithOptions("elasticsearch", SearchOptions{})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	types := resultTypes(results)
	if !types[ContentTypeMemory] {
		t.Errorf("a query with no magic word must still surface agent memories; got types %v", types)
	}
	if !types[ContentTypeWiki] {
		t.Errorf("wiki articles must still be returned; got types %v", types)
	}
}

// TestFacetlessAgentSearchSpansEveryType pins the new default: no facets means all types.
func TestFacetlessAgentSearchSpansEveryType(t *testing.T) {
	storage := seedSearchCorpus(t)

	results, err := storage.SearchArticlesWithOptions("zqterm", SearchOptions{})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("expected all 4 seeded documents, got %d", len(results))
	}
	for _, want := range []string{ContentTypeWiki, ContentTypeMemory, ContentTypePlan, ContentTypeSkill} {
		if !resultTypes(results)[want] {
			t.Errorf("expected a %s result", want)
		}
	}
}

// TestTypeFacetNarrows covers the type filter, including alias resolution.
func TestTypeFacetNarrows(t *testing.T) {
	storage := seedSearchCorpus(t)

	tests := []struct {
		name  string
		types []string
		want  []string
	}{
		{"memories alias", []string{"memories"}, []string{ContentTypeMemory}},
		{"singular alias", []string{"memory"}, []string{ContentTypeMemory}},
		{"canonical OKF type", []string{"AI-Agent-Plan"}, []string{ContentTypePlan}},
		{"articles alias", []string{"articles"}, []string{ContentTypeWiki}},
		{"multiple types", []string{"plans", "skills"}, []string{ContentTypePlan, ContentTypeSkill}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			results, err := storage.SearchArticlesWithOptions("zqterm", SearchOptions{Types: tc.types})
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}
			if len(results) != len(tc.want) {
				t.Fatalf("expected %d results, got %d", len(tc.want), len(results))
			}
			got := resultTypes(results)
			for _, want := range tc.want {
				if !got[want] {
					t.Errorf("expected a %s result, got types %v", want, got)
				}
			}
		})
	}
}

// TestTagFacetRequiresAllTags pins the conjunctive tag filter.
func TestTagFacetRequiresAllTags(t *testing.T) {
	storage := seedSearchCorpus(t)

	results, err := storage.SearchArticlesWithOptions("zqterm", SearchOptions{Tags: []string{"memory-nexwiki"}})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 || results[0].Type != ContentTypeMemory {
		t.Fatalf("expected only the tagged memory, got %d results %v", len(results), resultTypes(results))
	}

	// Case-insensitive.
	upper, err := storage.SearchArticlesWithOptions("zqterm", SearchOptions{Tags: []string{"MEMORY-NEXWIKI"}})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(upper) != 1 {
		t.Errorf("tag matching should be case-insensitive, got %d results", len(upper))
	}

	// A tag no document carries yields nothing, rather than being ignored.
	none, err := storage.SearchArticlesWithOptions("zqterm", SearchOptions{Tags: []string{"memory-nexwiki", "wip"}})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("all tags must be required; got %d results", len(none))
	}
}

// TestSearchLimitIsAppliedAndClamped pins the limit facet and its ceiling.
func TestSearchLimitIsAppliedAndClamped(t *testing.T) {
	storage := seedSearchCorpus(t)

	results, err := storage.SearchArticlesWithOptions("zqterm", SearchOptions{Limit: 2})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected the limit to cap results at 2, got %d", len(results))
	}

	// A limit past the ceiling is clamped, not rejected.
	all, err := storage.SearchArticlesWithOptions("zqterm", SearchOptions{Limit: maxSearchLimit + 5000})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("expected all 4 documents under a clamped limit, got %d", len(all))
	}
}

// TestLimitIsFilledDespiteFiltering guards the over-fetch. Facets are applied after Bleve scores,
// so requesting exactly `limit` hits from the index would silently return short whenever a filter
// drops something.
func TestLimitIsFilledDespiteFiltering(t *testing.T) {
	storage := seedSearchCorpus(t)

	// Two documents match the type filter; asking for 2 must return 2 even though the index
	// returns four candidates and two get filtered out.
	results, err := storage.SearchArticlesWithOptions("zqterm", SearchOptions{
		Types: []string{"plans", "skills"},
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("filtering must not starve the limit; expected 2, got %d", len(results))
	}
}

// TestHumanSearchStillHidesAgentDocs pins that the browser sidebar is unchanged. The facets are
// for agents; a human searching the wiki should not suddenly see every agent memory.
func TestHumanSearchStillHidesAgentDocs(t *testing.T) {
	storage := seedSearchCorpus(t)

	results, err := storage.SearchArticles("zqterm")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	types := resultTypes(results)
	if types[ContentTypeMemory] || types[ContentTypePlan] || types[ContentTypeSkill] {
		t.Errorf("human search must still hide agent documents by default, got %v", types)
	}
	if !types[ContentTypeWiki] {
		t.Error("human search must still return wiki articles")
	}

	// The legacy magic-word affordance still works for humans, who have no facets in the UI.
	planResults, err := storage.SearchArticles("plan")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	found := false
	for _, r := range planResults {
		if r.Type == ContentTypePlan {
			found = true
		}
	}
	if !found {
		t.Error("legacy human behavior should still surface plans when the query names them")
	}
}

// TestArchivedRequiresOptIn pins the archived facet for agent callers.
func TestArchivedRequiresOptIn(t *testing.T) {
	storage := seedSearchCorpus(t)

	art, err := storage.SaveArticle("", "Retired Note", "zqterm obsolete content", "", "", "", "seed",
		[]string{"archived"}, ContentTypeWiki)
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	if err := storage.IndexArticle(art); err != nil {
		t.Fatalf("IndexArticle failed: %v", err)
	}

	hidden, err := storage.SearchArticlesWithOptions("zqterm", SearchOptions{})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	for _, r := range hidden {
		if r.Slug == art.Slug {
			t.Error("archived documents must be excluded unless requested")
		}
	}

	shown, err := storage.SearchArticlesWithOptions("zqterm", SearchOptions{IncludeArchived: true})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	found := false
	for _, r := range shown {
		if r.Slug == art.Slug {
			found = true
		}
	}
	if !found {
		t.Error("include_archived must surface archived documents")
	}
}

// TestResolveSearchTypeAndValidation covers alias resolution and the typo report.
func TestResolveSearchTypeAndValidation(t *testing.T) {
	cases := map[string]string{
		"memories":        ContentTypeMemory,
		"MEMORY":          ContentTypeMemory,
		"  plans  ":       ContentTypePlan,
		"skill":           ContentTypeSkill,
		"articles":        ContentTypeWiki,
		"wiki":            ContentTypeWiki,
		"AI-Agent-Memory": ContentTypeMemory,
		"ai-agent-plan":   ContentTypePlan,
		"nonsense":        "",
		"":                "",
		"memorys (typo)":  "",
	}
	for input, want := range cases {
		if got := ResolveSearchType(input); got != want {
			t.Errorf("ResolveSearchType(%q) = %q, want %q", input, got, want)
		}
	}

	unknown := ValidateSearchTypes([]string{"memories", "memorys", "", "bogus"})
	if len(unknown) != 2 {
		t.Fatalf("expected 2 unknown type names, got %v", unknown)
	}
}

// TestSearchWikiToolExposesFacets exercises the MCP tool end to end, including the typo report.
func TestSearchWikiToolExposesFacets(t *testing.T) {
	srv := &Server{Storage: seedSearchCorpus(t), Version: "test"}

	call := func(args map[string]interface{}) string {
		t.Helper()
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		result, rpcErr := srv.toolSearchWiki(raw)
		if rpcErr != nil {
			t.Fatalf("unexpected JSON-RPC error: %v", rpcErr)
		}
		return result.(ToolResponse).Content[0].Text
	}

	// Default: the memory is reachable without any magic word in the query.
	if text := call(map[string]interface{}{"query": "elasticsearch"}); !strings.Contains(text, "Bleve Decision Record") {
		t.Errorf("agent search should surface the memory by default, got:\n%s", text)
	}

	// Type facet narrows and the applied filter is reported back.
	text := call(map[string]interface{}{"query": "zqterm", "type": []string{"memories"}})
	if !strings.Contains(text, "filtered by type: memories") {
		t.Errorf("expected the applied facet to be described, got:\n%s", text)
	}
	if strings.Contains(text, "Search Rework Plan") {
		t.Errorf("type facet should have excluded the plan, got:\n%s", text)
	}

	// The document type is shown, so an agent can tell a memory from an article.
	if !strings.Contains(text, "Type: "+ContentTypeMemory) {
		t.Errorf("results should report the document type, got:\n%s", text)
	}

	// A typo is reported rather than silently returning nothing.
	if text := call(map[string]interface{}{"query": "zqterm", "type": []string{"memorys"}}); !strings.Contains(text, "unknown document type") {
		t.Errorf("expected an unknown-type error, got:\n%s", text)
	}
}

// TestSearchLimitIsFullyDeliveredWhenHomeMatches covers a limit that silently under-delivered.
//
// Bleve applies Size before NexWiki's own filters run, and three of those filters are
// unconditional: "home" is always excluded from results, archived documents are excluded by
// default, and a hit whose file has been deleted is skipped. The over-fetch that compensates for
// this was gated on the caller having supplied a type or tag facet, so an *unfaceted* search asked
// Bleve for exactly `limit` hits and then threw one away with nothing to backfill it.
//
// The home page is the reliable trigger rather than an edge case: it describes the wiki, so it
// scores on the most ordinary queries. In a real 83-article wiki this made every unfaceted
// `limit: N` return N-1, including the default limit of 40.
func TestSearchLimitIsFullyDeliveredWhenHomeMatches(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	// "home" must match the query for the filter to have anything to drop.
	seed := func(slug, title string) {
		art, err := storage.SaveArticle(slug, title, "zqterm shared body text", "", "", "", "seed", nil, ContentTypeWiki)
		if err != nil {
			t.Fatalf("SaveArticle(%s) failed: %v", title, err)
		}
		if err := storage.IndexArticle(art); err != nil {
			t.Fatalf("IndexArticle(%s) failed: %v", title, err)
		}
	}
	seed("home", "Home")
	for i := 0; i < 8; i++ {
		seed("", fmt.Sprintf("Article %d", i))
	}

	for _, limit := range []int{1, 2, 3, 4, 5, 8} {
		results, err := storage.SearchArticlesWithOptions("zqterm", SearchOptions{Limit: limit})
		if err != nil {
			t.Fatalf("search with limit %d failed: %v", limit, err)
		}
		if len(results) != limit {
			t.Errorf("limit %d returned %d results, want %d", limit, len(results), limit)
		}
		for _, r := range results {
			if r.Slug == "home" {
				t.Errorf("limit %d: home leaked into results", limit)
			}
		}
	}
}

// TestSearchLimitStillDeliveredWithFacets guards the path that already worked, so the unconditional
// over-fetch does not regress faceted searches.
func TestSearchLimitStillDeliveredWithFacets(t *testing.T) {
	storage := seedSearchCorpus(t)

	results, err := storage.SearchArticlesWithOptions("zqterm", SearchOptions{
		Types: []string{"memories", "plans"},
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("faceted search failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("faceted limit 2 returned %d results, want 2", len(results))
	}
}

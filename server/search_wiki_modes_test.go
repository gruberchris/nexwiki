package server

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// These tests pin search_wiki's two modes after list_articles was folded into it. Index mode (no
// query) must be exactly what list_articles was — ordering, page size, cursor, per-entry fields,
// archived documents shown by default — and query mode exactly what search_wiki was, plus the
// filters and paging each mode gained from the other.

// searchWiki calls search_wiki with an argument map and returns the structured payload and the
// prose. It fails the test on a tool error or a protocol error.
func searchWiki(t *testing.T, srv *Server, args map[string]interface{}) (SearchOutput, string) {
	t.Helper()
	resp, rpcErr := searchWikiRaw(t, srv, args)
	if rpcErr != nil {
		t.Fatalf("search_wiki(%v): protocol error %v", args, rpcErr)
	}
	if resp.IsError {
		t.Fatalf("search_wiki(%v): tool error %s", args, resp.Content[0].Text)
	}
	out, ok := resp.StructuredContent.(SearchOutput)
	if !ok {
		t.Fatalf("search_wiki(%v): structured content is %T, want SearchOutput", args, resp.StructuredContent)
	}
	return out, resp.Content[0].Text
}

// searchWikiRaw is searchWiki without the success assertions, for the error cases.
func searchWikiRaw(t *testing.T, srv *Server, args map[string]interface{}) (ToolResponse, *JSONRPCError) {
	t.Helper()
	raw, err := json.Marshal(map[string]interface{}{"name": "search_wiki", "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	res, rpcErr := srv.executeToolCallInternal(raw)
	if rpcErr != nil {
		return ToolResponse{}, rpcErr
	}
	return res.(ToolResponse), nil
}

func documentSlugs(docs []Article) []string {
	slugs := make([]string, 0, len(docs))
	for _, d := range docs {
		slugs = append(slugs, d.Slug)
	}
	return slugs
}

func hitSlugs(hits []SearchHit) []string {
	slugs := make([]string, 0, len(hits))
	for _, h := range hits {
		slugs = append(slugs, h.Slug)
	}
	return slugs
}

// seedModesFixture builds a wiki exercising every filter: two plans in different statuses, two
// memories of different kinds, a tagged article, and an archived one. Every document mentions
// "zqmode", so query mode sees the same set index mode does.
func seedModesFixture(t *testing.T) *Server {
	t.Helper()
	srv := newMCPServer(t)
	save := func(args map[string]interface{}) {
		t.Helper()
		if resp := saveCall(t, srv, args); resp.IsError {
			t.Fatalf("seeding %v: %s", args["title"], resp.Content[0].Text)
		}
	}
	save(map[string]interface{}{"title": "Tagged Article", "content": "# Tagged\n\nzqmode tagged body.", "tags": []string{"Blue"}, "description": "tagged summary"})
	save(map[string]interface{}{"title": "Archived Article", "content": "# Archived\n\nzqmode archived body.", "tags": []string{"archived"}})
	save(map[string]interface{}{"title": "Active Plan", "content": "# Plan\n\nzqmode active plan.", "type": "AI-Agent-Plan", "project_context": "modes", "status": "implementing"})
	save(map[string]interface{}{"title": "Draft Plan", "content": "# Plan\n\nzqmode draft plan.", "type": "AI-Agent-Plan", "project_context": "modes"})
	save(map[string]interface{}{"title": "Project Memory", "content": "# Fact\n\nzqmode project fact.", "type": "AI-Agent-Memory", "memory_kind": "project", "description": "a project fact", "source": "test"})
	save(map[string]interface{}{"title": "Feedback Memory", "content": "# Fact\n\nzqmode feedback fact.", "type": "AI-Agent-Memory", "memory_kind": "feedback", "description": "a correction", "source": "test"})
	return srv
}

// TestSearchWikiIndexIsListArticles carries list_articles' behavior over to index mode: every
// document but home, most recently updated first in ListArticles' own order, archived documents
// included by default, metadata without bodies, and each entry's summary and status in the prose.
func TestSearchWikiIndexIsListArticles(t *testing.T) {
	srv := seedModesFixture(t)

	out, text := searchWiki(t, srv, map[string]interface{}{})
	listed, err := srv.Storage.ListArticles()
	if err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	if got, want := documentSlugs(out.Documents), documentSlugs(listed); !reflect.DeepEqual(got, want) {
		t.Errorf("index order = %v, want ListArticles' order %v", got, want)
	}
	if out.Count != len(listed) || out.Count != 6 {
		t.Errorf("count = %d, want all 6 documents (%d listed)", out.Count, len(listed))
	}
	if out.Results != nil || out.Query != "" || out.NextCursor != "" {
		t.Errorf("index mode must carry documents only, with no query and no cursor on one page: %+v", out)
	}
	// list_articles showed archived documents unless asked otherwise, and the index still does.
	if !out.IncludeArchived || !strings.Contains(text, "Archived Article") {
		t.Errorf("index mode must include archived documents by default: include_archived=%v\n%s", out.IncludeArchived, text)
	}
	for _, d := range out.Documents {
		if d.Content != "" {
			t.Errorf("the index inlined the body of %q", d.Slug)
		}
	}
	for _, want := range []string{"NexWiki Directory Index (6 matching articles, showing 6)", "Summary: tagged summary", "Status: implementing", "Type: Agent Plan"} {
		if !strings.Contains(text, want) {
			t.Errorf("index prose is missing %q:\n%s", want, text)
		}
	}
}

// TestSearchWikiIndexFilters covers each filter in index mode, the ones list_articles had (type,
// status, tag) and the ones it gained from search (memory_kind, include_archived).
func TestSearchWikiIndexFilters(t *testing.T) {
	srv := seedModesFixture(t)

	for _, tc := range []struct {
		name string
		args map[string]interface{}
		want []string
	}{
		{"type", map[string]interface{}{"type": "plans"}, []string{"active-plan", "draft-plan"}},
		{"type array", map[string]interface{}{"type": []string{"plans", "memories"}}, []string{"active-plan", "draft-plan", "feedback-memory", "project-memory"}},
		{"status", map[string]interface{}{"status": "Implementing"}, []string{"active-plan"}},
		{"status archived", map[string]interface{}{"status": "archived"}, []string{"archived-article"}},
		{"tag is case-insensitive", map[string]interface{}{"tag": "blue"}, []string{"tagged-article"}},
		{"memory_kind", map[string]interface{}{"memory_kind": "feedback"}, []string{"feedback-memory"}},
		{"include_archived false", map[string]interface{}{"include_archived": false, "type": "articles"}, []string{"tagged-article"}},
		{"include_archived false yields to status archived", map[string]interface{}{"include_archived": false, "status": "archived"}, []string{"archived-article"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := searchWiki(t, srv, tc.args)
			got := documentSlugs(out.Documents)
			sort.Strings(got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("slugs = %v, want %v", got, tc.want)
			}
			if out.Count != len(tc.want) {
				t.Errorf("count = %d, want %d", out.Count, len(tc.want))
			}
		})
	}

	out, text := searchWiki(t, srv, map[string]interface{}{"include_archived": false})
	if out.IncludeArchived || strings.Contains(text, "Archived Article") {
		t.Errorf("include_archived: false must drop archived documents from the index:\n%s", text)
	}
	if !strings.Contains(text, "excluding archived") {
		t.Errorf("the prose must say archived documents were left out:\n%s", text)
	}
}

// TestSearchWikiIndexPaging pins list_articles' paging: 50 per page by default, a caller-set
// limit otherwise, pages that are disjoint and in order, a cursor in both halves of the answer,
// and none on the last page.
func TestSearchWikiIndexPaging(t *testing.T) {
	srv := newMCPServer(t)
	for i := 0; i < 55; i++ {
		if _, err := srv.Storage.SaveArticle("", fmt.Sprintf("Paged Doc %02d", i), "# body", "", "", "", "seed", nil, ""); err != nil {
			t.Fatalf("SaveArticle: %v", err)
		}
	}

	first, text := searchWiki(t, srv, map[string]interface{}{})
	if len(first.Documents) != 50 || first.Count != 55 || first.NextCursor == "" {
		t.Fatalf("default page = %d documents of %d, cursor %q; want 50 of 55 and a cursor", len(first.Documents), first.Count, first.NextCursor)
	}
	if !strings.Contains(text, "Next page cursor: "+first.NextCursor) {
		t.Errorf("the prose must carry the same cursor as the structured half:\n%s", text)
	}
	second, _ := searchWiki(t, srv, map[string]interface{}{"cursor": first.NextCursor})
	if len(second.Documents) != 5 || second.NextCursor != "" {
		t.Errorf("last page = %d documents, cursor %q; want 5 and no cursor", len(second.Documents), second.NextCursor)
	}

	all, _ := searchWiki(t, srv, map[string]interface{}{"limit": 1000})
	var paged []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("paging did not terminate")
		}
		args := map[string]interface{}{"limit": 20}
		if cursor != "" {
			args["cursor"] = cursor
		}
		out, _ := searchWiki(t, srv, args)
		if len(out.Documents) > 20 {
			t.Fatalf("page of %d exceeds the limit of 20", len(out.Documents))
		}
		paged = append(paged, documentSlugs(out.Documents)...)
		if out.NextCursor == "" {
			break
		}
		cursor = out.NextCursor
	}
	if want := documentSlugs(all.Documents); !reflect.DeepEqual(paged, want) {
		t.Errorf("pages concatenated = %v\nwant the unpaged index %v", paged, want)
	}
}

// TestSearchWikiQueryFilters: with a query, archived documents stay out by default and
// the answer is scored hits, not documents; include_archived brings them back, and the new status
// filter narrows hits the way it narrows the index.
func TestSearchWikiQueryFilters(t *testing.T) {
	srv := seedModesFixture(t)

	out, text := searchWiki(t, srv, map[string]interface{}{"query": "zqmode"})
	if out.Documents != nil || out.Query != "zqmode" || out.IncludeArchived {
		t.Errorf("query mode must carry results only, echo the query, and exclude archived by default: %+v", out)
	}
	if got := hitSlugs(out.Results); len(got) != 5 || strings.Contains(text, "Archived Article") {
		t.Errorf("default search = %v, want the 5 unarchived documents", got)
	}
	if out.Count != len(out.Results) {
		t.Errorf("count %d disagrees with %d results on a single page", out.Count, len(out.Results))
	}

	withArchived, _ := searchWiki(t, srv, map[string]interface{}{"query": "zqmode", "include_archived": true})
	if len(withArchived.Results) != 6 || !withArchived.IncludeArchived {
		t.Errorf("include_archived must bring the archived hit back: %v", hitSlugs(withArchived.Results))
	}

	for _, tc := range []struct {
		name string
		args map[string]interface{}
		want []string
	}{
		{"status", map[string]interface{}{"status": "implementing"}, []string{"active-plan"}},
		{"status draft", map[string]interface{}{"status": "draft"}, []string{"draft-plan"}},
		{"status archived implies archived", map[string]interface{}{"status": "archived"}, []string{"archived-article"}},
		{"status and type", map[string]interface{}{"status": "implementing", "type": "memories"}, []string{}},
		{"memory_kind", map[string]interface{}{"memory_kind": "project"}, []string{"project-memory"}},
		{"type", map[string]interface{}{"type": "plans"}, []string{"active-plan", "draft-plan"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]interface{}{"query": "zqmode"}
			for k, v := range tc.args {
				args[k] = v
			}
			out, text := searchWiki(t, srv, args)
			got := hitSlugs(out.Results)
			sort.Strings(got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("hits = %v, want %v\n%s", got, tc.want, text)
			}
			if s, ok := tc.args["status"].(string); ok && (out.Status != s || !strings.Contains(text, "status: "+s)) {
				t.Errorf("the status facet must be echoed in both halves: %q\n%s", out.Status, text)
			}
		})
	}
}

// TestSearchWikiQueryPaging pins cursor paging over scored results: pages are disjoint, their
// concatenation is the unpaged result in score order, the first page is what the same call
// without a cursor always returned, count is the total on every page, and the last page offers no
// cursor.
func TestSearchWikiQueryPaging(t *testing.T) {
	srv := newMCPServer(t)
	for i := 0; i < 7; i++ {
		body := "# Doc\n\n" + strings.Repeat("zqpage ", i+1) + "filler text."
		if _, err := srv.Storage.SaveArticle("", fmt.Sprintf("Scored Doc %d", i), body, "", "", "", "seed", nil, ""); err != nil {
			t.Fatalf("SaveArticle: %v", err)
		}
	}

	all, _ := searchWiki(t, srv, map[string]interface{}{"query": "zqpage", "limit": 200})
	if len(all.Results) != 7 || all.NextCursor != "" {
		t.Fatalf("unpaged search = %d hits, cursor %q; want 7 and no cursor", len(all.Results), all.NextCursor)
	}

	var paged []string
	seen := map[string]bool{}
	cursor := ""
	for pages := 1; ; pages++ {
		if pages > 5 {
			t.Fatal("paging did not terminate")
		}
		args := map[string]interface{}{"query": "zqpage", "limit": 3}
		if cursor != "" {
			args["cursor"] = cursor
		}
		out, text := searchWiki(t, srv, args)
		if out.Count != 7 {
			t.Errorf("page %d: count = %d, want the total 7", pages, out.Count)
		}
		for _, s := range hitSlugs(out.Results) {
			if seen[s] {
				t.Errorf("page %d repeats %s", pages, s)
			}
			seen[s] = true
		}
		paged = append(paged, hitSlugs(out.Results)...)
		if out.NextCursor == "" {
			if pages != 3 || len(out.Results) != 1 {
				t.Errorf("last page was page %d with %d hits; want page 3 with 1", pages, len(out.Results))
			}
			if strings.Contains(text, "Next page cursor") {
				t.Errorf("the last page must not offer a cursor in prose:\n%s", text)
			}
			break
		}
		if len(out.Results) != 3 {
			t.Errorf("page %d holds %d hits, want 3", pages, len(out.Results))
		}
		if !strings.Contains(text, "Next page cursor: "+out.NextCursor) || !strings.Contains(text, "showing 3") {
			t.Errorf("page %d prose must say it is a partial page and carry the cursor:\n%s", pages, text)
		}
		cursor = out.NextCursor
	}
	if want := hitSlugs(all.Results); !reflect.DeepEqual(paged, want) {
		t.Errorf("pages concatenated = %v\nwant the unpaged result %v", paged, want)
	}

	firstPage, _ := searchWiki(t, srv, map[string]interface{}{"query": "zqpage", "limit": 3})
	if want := hitSlugs(all.Results)[:3]; !reflect.DeepEqual(hitSlugs(firstPage.Results), want) {
		t.Errorf("first page = %v, want the top 3 %v", hitSlugs(firstPage.Results), want)
	}

	// The default and the ceiling are unchanged: 40 and 200.
	if _, text := searchWiki(t, srv, map[string]interface{}{"query": "zqpage", "limit": 5000}); strings.Contains(text, "Next page cursor") {
		t.Errorf("a limit above the ceiling is clamped, not an error, and 7 hits fit one page:\n%s", text)
	}
}

// TestSearchWikiArgumentErrors: an invalid cursor, include_history without a query, and an
// unknown type are each reported, in both modes where they apply, rather than answered.
func TestSearchWikiArgumentErrors(t *testing.T) {
	srv := seedModesFixture(t)

	for _, args := range []map[string]interface{}{
		{"cursor": "not-a-cursor"},
		{"query": "zqmode", "cursor": "not-a-cursor"},
		{"cursor": encodeCursor(9999)},
		{"query": "zqmode", "cursor": encodeCursor(9999)},
	} {
		_, rpcErr := searchWikiRaw(t, srv, args)
		if rpcErr == nil || rpcErr.Code != errCodeInvalidParams || !strings.Contains(strings.ToLower(rpcErr.Message), "cursor") {
			t.Errorf("%v: want an invalid-params error naming the cursor, got %v", args, rpcErr)
		}
	}

	for _, args := range []map[string]interface{}{
		{"include_history": true},
		{"query": "   ", "include_history": true},
	} {
		_, rpcErr := searchWikiRaw(t, srv, args)
		if rpcErr == nil || rpcErr.Code != -32602 || !strings.Contains(rpcErr.Message, "'include_history'") || !strings.Contains(rpcErr.Message, "'query'") {
			t.Errorf("%v: want an argument error naming include_history and query, got %v", args, rpcErr)
		}
	}

	for _, args := range []map[string]interface{}{
		{"type": "memorys"},
		{"query": "zqmode", "type": "memorys"},
	} {
		resp, rpcErr := searchWikiRaw(t, srv, args)
		if rpcErr != nil || !resp.IsError || !strings.Contains(resp.Content[0].Text, "memorys") || resp.StructuredContent != nil {
			t.Errorf("%v: want a tool error naming the unknown type, got %+v / %v", args, resp, rpcErr)
		}
	}
}

// TestListArticlesIsUnregistered: the merged tool replaced list_articles outright, so calling it
// is the ordinary unknown-tool error and tools/list omits it.
func TestListArticlesIsUnregistered(t *testing.T) {
	srv := newMCPServer(t)
	resp := toolCall(t, srv, `{"name":"list_articles","arguments":{}}`)
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "Tool not found: list_articles") {
		t.Errorf("list_articles should be an unknown tool, got %+v", resp)
	}
	for _, entry := range toolSchemas() {
		if entry["name"] == "list_articles" {
			t.Error("tools/list still advertises list_articles")
		}
	}
}

// TestDamperIgnoresIndexAndPaging: the index was never damped as list_articles, and a cursor is
// progress through one answer, so neither may draw a repeat notice however often it is called.
func TestDamperIgnoresIndexAndPaging(t *testing.T) {
	srv := newMCPServer(t)
	for i := 0; i < 3; i++ {
		if _, err := srv.Storage.SaveArticle("", fmt.Sprintf("Damper Doc %d", i), "# zqdamp body", "", "", "", "seed", nil, ""); err != nil {
			t.Fatalf("SaveArticle: %v", err)
		}
	}
	page, _ := searchWiki(t, srv, map[string]interface{}{"query": "zqdamp", "limit": 1})
	if page.NextCursor == "" {
		t.Fatal("setup: expected a second page")
	}
	for _, call := range []string{
		`{"name":"search_wiki","arguments":{}}`,
		`{"name":"search_wiki","arguments":{"type":"plans"}}`,
		fmt.Sprintf(`{"name":"search_wiki","arguments":{"query":"zqdamp","limit":1,"cursor":%q}}`, page.NextCursor),
	} {
		for i := 0; i < 3; i++ {
			if text := damperCall(t, srv, "agent-a", call); strings.Contains(text, "repeats a lookup") || strings.Contains(text, "COMPLETED check") {
				t.Fatalf("call %d of %s drew a damper notice:\n%s", i+1, call, text)
			}
		}
	}
}

// TestSearchWikiFallbackSnippetOnLaterPages: storage skips the fallback snippet's file read for
// search_wiki (which fetches every hit to page them), and the tool fills it in for the page it
// returns. Bleve's highlighter falls back to the head of any stored content or title, so the only
// fragment-less hit is one indexed with neither — an index entry lagging its file — and that hit
// must still carry the body excerpt read from disk, on page 2 as on page 1.
func TestSearchWikiFallbackSnippetOnLaterPages(t *testing.T) {
	srv := newMCPServer(t)
	for i := 0; i < 3; i++ {
		body := fmt.Sprintf("# Frag Body %d\n\nplain <b>text</b> number %d", i, i)
		art, err := srv.Storage.SaveArticle("", fmt.Sprintf("Frag Doc %d", i), body, "zqfrag summary", "", "", "seed", nil, "")
		if err != nil {
			t.Fatalf("SaveArticle: %v", err)
		}
		if err := srv.Storage.SearchIndex.Index(art.Slug, map[string]interface{}{"description": art.Description}); err != nil {
			t.Fatalf("IndexArticle: %v", err)
		}
	}

	// Setup check: these hits really have no fragments, and storage honours the skip.
	raw, err := srv.Storage.SearchArticlesWithOptions("zqfrag", SearchOptions{skipFallbackSnippets: true})
	if err != nil || len(raw) != 3 {
		t.Fatalf("setup: search = %d hits, err %v; want 3", len(raw), err)
	}
	for _, r := range raw {
		if len(r.Snippets) != 0 {
			t.Fatalf("setup: %s has snippets %v; the test needs fragment-less hits", r.Slug, r.Snippets)
		}
	}

	first, _ := searchWiki(t, srv, map[string]interface{}{"query": "zqfrag", "limit": 1})
	second, text := searchWiki(t, srv, map[string]interface{}{"query": "zqfrag", "limit": 1, "cursor": first.NextCursor})
	if len(second.Results) != 1 {
		t.Fatalf("page 2 holds %d hits, want 1", len(second.Results))
	}
	hit := second.Results[0]
	want := plainSnippet(srv.Storage.fallbackSnippet(hit.Slug, "")[0])
	if len(hit.Snippets) != 1 || hit.Snippets[0] != want || !strings.Contains(want, "Frag Body") || !strings.Contains(want, "<b>text</b>") {
		t.Errorf("page 2 snippet = %q, want the plain fallback excerpt %q", hit.Snippets, want)
	}
	if !strings.Contains(text, "Snippet: ... "+want+" ...") {
		t.Errorf("the prose must carry the same fallback snippet:\n%s", text)
	}
}

// TestSearchWikiAttestedComputationType: query mode accepts the Attested Computation type the
// index always did, and narrows to it rather than widening to every type.
func TestSearchWikiAttestedComputationType(t *testing.T) {
	srv := seedModesFixture(t)
	if _, err := srv.Storage.SaveArticle("", "Checksum Computation", "# Compute\n\nzqmode checksum.", "", "", "", "seed", nil, ContentTypeComputation); err != nil {
		t.Fatalf("SaveArticle: %v", err)
	}
	for _, args := range []map[string]interface{}{
		{"query": "zqmode", "type": "Attested Computation"},
		{"type": "Attested Computation"},
	} {
		out, text := searchWiki(t, srv, args)
		got := append(hitSlugs(out.Results), documentSlugs(out.Documents)...)
		if !reflect.DeepEqual(got, []string{"checksum-computation"}) {
			t.Errorf("%v: slugs = %v, want only the computation\n%s", args, got, text)
		}
	}
}

// TestSearchWikiArchivedTagForcesInclusion: a tag filter naming "archived" — trimmed and in any
// case, as storage reads it — includes archived documents in either mode, even over an explicit
// include_archived: false, and the echo says so.
func TestSearchWikiArchivedTagForcesInclusion(t *testing.T) {
	srv := seedModesFixture(t)
	for _, args := range []map[string]interface{}{
		{"tag": " Archived ", "include_archived": false},
		{"query": "zqmode", "tag": " Archived "},
	} {
		out, text := searchWiki(t, srv, args)
		got := append(hitSlugs(out.Results), documentSlugs(out.Documents)...)
		if !reflect.DeepEqual(got, []string{"archived-article"}) || !out.IncludeArchived {
			t.Errorf("%v: slugs = %v, include_archived = %v; want the archived article, included\n%s", args, got, out.IncludeArchived, text)
		}
	}
}

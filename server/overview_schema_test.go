package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWikiOverviewMatchesSchemaOnACleanWiki pins the overview against its published schema on a
// wiki with nothing broken. The schema once required a broken_links array the handler serialized
// as null, so a strict client rejected every call.
func TestWikiOverviewMatchesSchemaOnACleanWiki(t *testing.T) {
	srv := newMCPServer(t)
	if resp := toolCall(t, srv, `{"name":"save_article","arguments":{"title":"Clean Page","content":"# Clean\n\nNo links."}}`); resp.IsError {
		t.Fatal(resp.Content[0].Text)
	}
	// The seeded home page links to pages a fresh wiki does not have; without it nothing is broken.
	if err := srv.Storage.DeleteArticle("home"); err != nil {
		t.Fatal(err)
	}

	resp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{}}`)
	if resp.IsError {
		t.Fatal(resp.Content[0].Text)
	}
	assertMatchesOutputSchema(t, resp, toolsByName["get_wiki_overview"].Output)
}

// TestWikiOverviewCarriesNoLinkGraphStatistics pins that the overview stays out of wiki_health's
// territory. It used to scan the link graph on request (include_stats) and report broken links,
// unreadable files and misplaced documents — a full scan in the call every session makes, repeating
// what wiki_health reports with the lists and remedies attached. The argument is gone from the
// schema, and passing it anyway changes nothing.
func TestWikiOverviewCarriesNoLinkGraphStatistics(t *testing.T) {
	srv := newMCPServer(t) // the seeded home page links to pages that do not exist

	props := getWikiOverviewTool.Schema["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
	if _, ok := props["include_stats"]; ok {
		t.Error("include_stats is still declared in the input schema")
	}
	outProps := getWikiOverviewTool.Output["properties"].(map[string]interface{})
	if _, ok := outProps["statistics"]; ok {
		t.Error("statistics is still declared in the output schema")
	}

	for _, call := range []string{
		`{"name":"get_wiki_overview","arguments":{}}`,
		`{"name":"get_wiki_overview","arguments":{"include_stats":true}}`,
	} {
		resp := toolCall(t, srv, call)
		if resp.IsError {
			t.Fatal(resp.Content[0].Text)
		}
		encoded, err := json.Marshal(resp.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"statistics", "broken_link", "total_links", "unreadable_file_count", "misplaced_document_count"} {
			if strings.Contains(string(encoded), `"`+field) {
				t.Errorf("%s: structured output carries %q: %s", call, field, encoded)
			}
		}
		text := resp.Content[0].Text
		for _, section := range []string{"== Statistics ==", "Broken Links:", "Total Internal Links"} {
			if strings.Contains(text, section) {
				t.Errorf("%s: prose carries %q:\n%s", call, section, text)
			}
		}
	}
}

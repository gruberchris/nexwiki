package server

import (
	"encoding/json"
	"fmt"
	"testing"
)

// TestWikiOverviewMatchesSchemaOnACleanWiki is the get_wiki_overview schema violation: broken_links
// was nil — null on the wire — whenever include_stats was off or nothing was broken, and the schema
// requires an array, so a strict client rejected every call.
func TestWikiOverviewMatchesSchemaOnACleanWiki(t *testing.T) {
	srv := newMCPServer(t)
	if resp := toolCall(t, srv, `{"name":"save_article","arguments":{"title":"Clean Page","content":"# Clean\n\nNo links."}}`); resp.IsError {
		t.Fatal(resp.Content[0].Text)
	}
	// The seeded home page links to pages a fresh wiki does not have; without it nothing is broken.
	if err := srv.Storage.DeleteArticle("home"); err != nil {
		t.Fatal(err)
	}

	for _, stats := range []bool{false, true} {
		t.Run(fmt.Sprintf("include_stats=%v", stats), func(t *testing.T) {
			resp := toolCall(t, srv, fmt.Sprintf(`{"name":"get_wiki_overview","arguments":{"include_stats":%v}}`, stats))
			if resp.IsError {
				t.Fatal(resp.Content[0].Text)
			}
			encoded, err := json.Marshal(resp.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var decoded interface{}
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			for _, problem := range validateAgainstSchema(decoded, toolsByName["get_wiki_overview"].Output, "$") {
				t.Error(problem)
			}
			links := decoded.(map[string]interface{})["statistics"].(map[string]interface{})["broken_links"]
			if list, ok := links.([]interface{}); !ok || len(list) != 0 {
				t.Errorf("broken_links = %#v, want []", links)
			}
		})
	}
}

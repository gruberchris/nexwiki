package server

import (
	"strings"
	"testing"
)

func TestMCPGetContextOverview(t *testing.T) {
	srv := newMCPServer(t)

	// Seed one of each type: article with description, article without (preview fallback),
	// a memory, a plan, and a skill.
	_, err := srv.Storage.SaveArticle("", "Described Article", "# Body text", "explicit summary", "", "", "", []string{"notes"}, "")
	if err != nil {
		t.Fatalf("seed 1 failed: %v", err)
	}
	_, err = srv.Storage.SaveArticle("", "Bare Article", "First prose line becomes the preview.\n\nMore text.", "", "", "", "", nil, "")
	if err != nil {
		t.Fatalf("seed 2 failed: %v", err)
	}
	_, err = srv.Storage.SaveArticle("", "A Memory", "# remembered fact", "", "", "", "", []string{"aiagent-memory-nexwiki"}, ContentTypeMemory)
	if err != nil {
		t.Fatalf("seed 3 failed: %v", err)
	}
	_, err = srv.Storage.SaveArticle("", "A Plan", "# plan steps", "", "", "", "", []string{"aiagent-plan", "nexwiki"}, ContentTypePlan)
	if err != nil {
		t.Fatalf("seed 4 failed: %v", err)
	}
	_, err = srv.Storage.SaveArticle("", "A Skill", "# skill steps", "", "", "", "", []string{"aiagent-skill"}, ContentTypeSkill)
	if err != nil {
		t.Fatalf("seed 5 failed: %v", err)
	}

	resp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{}}`)
	if resp.IsError {
		t.Fatalf("get_wiki_overview failed: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text

	for _, want := range []string{
		"NexWiki Knowledge Base Overview (5 articles total)",
		"Documents: 2 wiki · 1 memories · 1 plans · 1 skills",
		"== Next Steps ==",
		"search_wiki without a query",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("overview missing %q in output:\n%s", want, text)
		}
	}
	// The overview is not an index: documents that are neither pinned memories nor active plans
	// are counted, not listed. search_wiki without a query is where they are listed.
	for _, absent := range []string{"(described-article)", "(bare-article)", "(a-memory)", "(a-skill)"} {
		if strings.Contains(text, absent) {
			t.Errorf("overview lists %s; it must count documents, not list them:\n%s", absent, text)
		}
	}

	// Filter by type using search_wiki's index
	memOnly := toolCall(t, srv, `{"name":"search_wiki","arguments":{"type":"memories"}}`)
	if memOnly.IsError {
		t.Fatalf("filtered list failed: %s", memOnly.Content[0].Text)
	}
	memOut := memOnly.StructuredContent.(SearchOutput)
	if memOut.Count != 1 || memOut.Documents[0].Slug != "a-memory" {
		t.Errorf("expected 1 memory, got %+v", memOut)
	}
}

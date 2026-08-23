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
	_, err = srv.Storage.SaveArticle("", "A Plan", "# plan steps", "", "", "", "", []string{"aiagent-plan", "nexwiki", "draft"}, ContentTypePlan)
	if err != nil {
		t.Fatalf("seed 4 failed: %v", err)
	}
	_, err = srv.Storage.SaveArticle("", "A Skill", "# skill steps", "", "", "", "", []string{"aiagent-skill"}, ContentTypeSkill)
	if err != nil {
		t.Fatalf("seed 5 failed: %v", err)
	}

	resp := toolCall(t, srv, `{"name":"get_context_overview","arguments":{}}`)
	if resp.IsError {
		t.Fatalf("get_context_overview failed: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text

	for _, want := range []string{
		"== Wiki Articles (2) ==",
		"== Agent Memories (1) ==",
		"== Agent Plans (1) ==",
		"== Agent Skills (1) ==",
		"- Described Article (described-article) — explicit summary [notes]",
		"- Bare Article (bare-article) — First prose line becomes the preview.",
		"- A Memory (a-memory)",
		"- A Plan (a-plan)",
		"- A Skill (a-skill)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("overview missing %q in output:\n%s", want, text)
		}
	}

	// Section filter restricts output
	memOnly := toolCall(t, srv, `{"name":"get_context_overview","arguments":{"type":"memories"}}`)
	if memOnly.IsError {
		t.Fatalf("filtered overview failed: %s", memOnly.Content[0].Text)
	}
	if !strings.Contains(memOnly.Content[0].Text, "== Agent Memories (1) ==") {
		t.Errorf("filtered overview missing memories section: %s", memOnly.Content[0].Text)
	}
	if strings.Contains(memOnly.Content[0].Text, "== Wiki Articles") {
		t.Errorf("filtered overview should not include articles section: %s", memOnly.Content[0].Text)
	}

	// Invalid filter is a tool error
	bad := toolCall(t, srv, `{"name":"get_context_overview","arguments":{"type":"bogus"}}`)
	if !bad.IsError {
		t.Error("expected error for invalid type filter")
	}
}

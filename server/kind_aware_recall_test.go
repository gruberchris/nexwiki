package server

import (
	"strings"
	"testing"
)

// get_wiki_overview is the first call the server's instructions tell an agent to make, and it
// is where the kind axis earns its keep rather than merely classifying. `user` and `feedback`
// memories apply to any task at all — who the agent is working with, and the corrections that
// person has already given — so they are the memories the overview lists. Every other memory is
// only relevant once the task is known, and is counted rather than listed.

// seedKindFixture builds a memory set where the pinned kinds are deliberately *not* the most
// recent, so a passing test proves the ordering is by kind and not an accident of recency.
func seedKindFixture(t *testing.T) *Server {
	t.Helper()
	srv := newMCPServer(t)

	seed := []struct{ title, kind string }{
		{"Operator Profile", "user"},
		{"How Chris Reviews", "feedback"},
		{"Deploy Constraint", "project"},
		{"Metrics Dashboard", "reference"},
		{"Another Constraint", "project"},
		{"One More Reference", "reference"},
	}
	for _, s := range seed {
		call := `{"name":"save_article","arguments":{"type":"AI-Agent-Memory","title":"` + s.title +
			`","content":"# fact","memory_kind":"` + s.kind +
			`","description":"fixture memory","source":"test fixture"}}`
		if resp := toolCall(t, srv, call); resp.IsError {
			t.Fatalf("seeding %q: %s", s.title, resp.Content[0].Text)
		}
	}
	return srv
}

func TestContextOverviewPinsUserAndFeedback(t *testing.T) {
	srv := seedKindFixture(t)

	resp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{}}`)
	if resp.IsError {
		t.Fatalf("overview failed: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text

	// The two pinned memories were created first, so they are the *oldest*. If they are the ones
	// listed, that is the pin, not recency.
	for _, slug := range []string{"(operator-profile)", "(how-chris-reviews)"} {
		if n := strings.Count(text, slug); n != 1 {
			t.Errorf("%s appears %d times, want exactly once:\n%s", slug, n, text)
		}
	}
	for _, slug := range []string{"deploy-constraint", "metrics-dashboard", "another-constraint", "one-more-reference"} {
		if strings.Contains(text, slug) {
			t.Errorf("%s is neither user nor feedback and must not be listed:\n%s", slug, text)
		}
	}
	if !strings.Contains(text, "== Pinned Memories (user and feedback: 2) ==") {
		t.Errorf("the pinned section should report both pinned memories:\n%s", text)
	}
	// Every memory is still counted.
	if !strings.Contains(text, "6 memories") {
		t.Errorf("the counts must still report every memory:\n%s", text)
	}

	var out OverviewOutput
	decodeStructured(t, resp, &out)
	if out.PinnedMemoryTotal != 2 || len(out.PinnedMemories) != 2 || out.Counts.Memories != 6 {
		t.Errorf("structured pinned memories = %+v (total %d), memories counted %d", out.PinnedMemories, out.PinnedMemoryTotal, out.Counts.Memories)
	}
}

func TestContextOverviewShowsKind(t *testing.T) {
	srv := seedKindFixture(t)

	resp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{}}`)
	text := resp.Content[0].Text

	// The kind has to be visible, or an agent cannot tell why a memory was pinned.
	for _, kind := range []string{"<user>", "<feedback>"} {
		if !strings.Contains(text, kind) {
			t.Errorf("overview should render %s:\n%s", kind, text)
		}
	}
}

// TestContextOverviewStaysQuietWithNothingPinned covers a corpus with no `user` or `feedback`
// memories at all: the section says so in one line instead of listing anything else.
func TestContextOverviewStaysQuietWithNothingPinned(t *testing.T) {
	srv := newMCPServer(t)

	if resp := toolCall(t, srv, `{"name":"save_article","arguments":{"type":"AI-Agent-Memory","title":"Only Project","content":"# fact","memory_kind":"project","description":"d","source":"s"}}`); resp.IsError {
		t.Fatalf("setup failed: %s", resp.Content[0].Text)
	}

	resp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{}}`)
	text := resp.Content[0].Text
	if !strings.Contains(text, "No user or feedback memories.") {
		t.Errorf("with nothing pinned the section should say so:\n%s", text)
	}
	if strings.Contains(text, "(only-project)") {
		t.Errorf("a project memory is counted, not listed:\n%s", text)
	}
	if !strings.Contains(text, "1 memories") {
		t.Errorf("the memory should still be counted:\n%s", text)
	}
}

// TestContextOverviewPinSurvivesAnUnkindedCorpus guards the upgrade path. Every memory written
// before the kind axis existed has no kind; they are counted, never pinned, and never rendered
// with an empty kind marker.
func TestContextOverviewPinSurvivesAnUnkindedCorpus(t *testing.T) {
	srv := newMCPServer(t)

	for _, title := range []string{"Legacy One", "Legacy Two"} {
		if _, err := srv.Storage.SaveArticle("", title, "# fact", "d", "s", "", "seed", nil, ContentTypeMemory); err != nil {
			t.Fatalf("seeding %q: %v", title, err)
		}
	}

	resp := toolCall(t, srv, `{"name":"get_wiki_overview","arguments":{}}`)
	if resp.IsError {
		t.Fatalf("overview failed: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text
	if !strings.Contains(text, "2 memories") {
		t.Errorf("unkinded memories must still be counted:\n%s", text)
	}
	if strings.Contains(text, "<>") {
		t.Errorf("an unkinded memory must render no kind marker at all, not an empty one:\n%s", text)
	}
}

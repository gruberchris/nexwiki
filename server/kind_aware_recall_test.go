package server

import (
	"strings"
	"testing"
)

// get_context_overview is the first call the server's instructions tell an agent to make, and it
// is where the kind axis earns its keep rather than merely classifying. `user` and `feedback`
// memories apply to any task at all — who the agent is working with, and the corrections that
// person has already given — so they lead the memory listing.

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
		call := `{"name":"create_agent_memory","arguments":{"title":"` + s.title +
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

	resp := toolCall(t, srv, `{"name":"get_context_overview","arguments":{"type":"memories"}}`)
	if resp.IsError {
		t.Fatalf("overview failed: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text

	// The two pinned memories were created first, so they are the *oldest* — and ListArticles
	// sorts newest first. If they lead the listing, that is the pin, not recency.
	posUser := strings.Index(text, "operator-profile")
	posFeedback := strings.Index(text, "how-chris-reviews")
	posProject := strings.Index(text, "deploy-constraint")
	posReference := strings.Index(text, "metrics-dashboard")

	for name, pos := range map[string]int{
		"operator-profile": posUser, "how-chris-reviews": posFeedback,
		"deploy-constraint": posProject, "metrics-dashboard": posReference,
	} {
		if pos < 0 {
			t.Fatalf("%s missing from the overview:\n%s", name, text)
		}
	}

	if posUser > posProject || posUser > posReference {
		t.Errorf("a 'user' memory must lead the listing, got position %d vs project %d / reference %d",
			posUser, posProject, posReference)
	}
	if posFeedback > posProject || posFeedback > posReference {
		t.Errorf("a 'feedback' memory must lead the listing, got position %d vs project %d / reference %d",
			posFeedback, posProject, posReference)
	}
}

// TestContextOverviewPinDoesNotDuplicate is the property that makes this an ordering rather than
// a pinned block above the index. The overview exists to be cheap; listing a memory twice spends
// context to say one thing, and it is why the pinned set needs no cap — nothing is displaced.
func TestContextOverviewPinDoesNotDuplicate(t *testing.T) {
	srv := seedKindFixture(t)

	resp := toolCall(t, srv, `{"name":"get_context_overview","arguments":{}}`)
	if resp.IsError {
		t.Fatalf("overview failed: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text

	if n := strings.Count(text, "(operator-profile)"); n != 1 {
		t.Errorf("pinned memory appears %d times, want exactly 1 — the pin reorders, it does not repeat", n)
	}
	if n := strings.Count(text, "(how-chris-reviews)"); n != 1 {
		t.Errorf("pinned memory appears %d times, want exactly 1", n)
	}

	// Every memory still has to be present. An ordering that quietly drops the tail would pass
	// the assertion above and be far worse than no pinning at all.
	for _, slug := range []string{"deploy-constraint", "metrics-dashboard", "another-constraint", "one-more-reference"} {
		if !strings.Contains(text, "("+slug+")") {
			t.Errorf("%s missing — pinning must not drop the rest of the index", slug)
		}
	}
	if !strings.Contains(text, "Agent Memories (6)") {
		t.Errorf("the section count must still report every memory:\n%s", text)
	}
}

func TestContextOverviewShowsKind(t *testing.T) {
	srv := seedKindFixture(t)

	resp := toolCall(t, srv, `{"name":"get_context_overview","arguments":{"type":"memories"}}`)
	text := resp.Content[0].Text

	// The kind has to be visible, or an agent cannot tell why the order is what it is, nor
	// which memories are worth reading for the task at hand.
	for _, kind := range []string{"<user>", "<feedback>", "<project>", "<reference>"} {
		if !strings.Contains(text, kind) {
			t.Errorf("overview should render %s:\n%s", kind, text)
		}
	}
	if !strings.Contains(text, "user and feedback memories are listed first") {
		t.Errorf("the overview should explain the ordering when a pinned memory is present:\n%s", text)
	}
}

// TestContextOverviewStaysQuietWithNothingPinned covers the wiki this feature shipped into: a
// corpus with no `user` or `feedback` memories at all. Explaining an ordering that is not visible
// would spend context describing nothing.
func TestContextOverviewStaysQuietWithNothingPinned(t *testing.T) {
	srv := newMCPServer(t)

	if resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"Only Project","content":"# fact","memory_kind":"project","description":"d","source":"s"}}`); resp.IsError {
		t.Fatalf("setup failed: %s", resp.Content[0].Text)
	}

	resp := toolCall(t, srv, `{"name":"get_context_overview","arguments":{"type":"memories"}}`)
	text := resp.Content[0].Text
	if strings.Contains(text, "listed first") {
		t.Errorf("no pinned memories exist, so the overview should not explain an ordering:\n%s", text)
	}
	if !strings.Contains(text, "(only-project)") {
		t.Errorf("the memory should still be listed:\n%s", text)
	}
}

// TestContextOverviewPinSurvivesAnUnkindedCorpus guards the upgrade path. Every memory written
// before the kind axis existed has no kind, and the overview must not reorder or drop them.
func TestContextOverviewPinSurvivesAnUnkindedCorpus(t *testing.T) {
	srv := newMCPServer(t)

	for _, title := range []string{"Legacy One", "Legacy Two"} {
		if _, err := srv.Storage.SaveArticle("", title, "# fact", "d", "s", "", "seed", nil, ContentTypeMemory); err != nil {
			t.Fatalf("seeding %q: %v", title, err)
		}
	}

	resp := toolCall(t, srv, `{"name":"get_context_overview","arguments":{"type":"memories"}}`)
	if resp.IsError {
		t.Fatalf("overview failed: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text
	for _, slug := range []string{"(legacy-one)", "(legacy-two)"} {
		if !strings.Contains(text, slug) {
			t.Errorf("%s missing from an all-unkinded overview:\n%s", slug, text)
		}
	}
	if strings.Contains(text, "<>") {
		t.Errorf("an unkinded memory must render no kind marker at all, not an empty one:\n%s", text)
	}
}

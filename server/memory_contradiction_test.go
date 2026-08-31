package server

import (
	"strings"
	"testing"
)

// Optimistic locking protects against *concurrent* edits. It does nothing about an agent that has
// loaded the current version and knowingly replaces a fact with an incompatible one — that is a
// clean, successful, silent overwrite. Git history retains the old assertion, but history is not
// where anyone looks, and a contradiction nobody surfaces is a contradiction nobody resolves.

func seedContestableMemory(t *testing.T, srv *Server) {
	t.Helper()
	resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"Embedding Provider Host","content":"# Fact\n\nThe embedding provider runs on mars.","memory_kind":"project","memory_type":"kimmydb","description":"where embeddings run","source":"deploy 2026-08-22"}}`)
	if resp.IsError {
		t.Fatalf("setup failed: %s", resp.Content[0].Text)
	}
}

func TestChangeIntentIsRequiredWhenContentIsReplaced(t *testing.T) {
	srv := newMCPServer(t)
	seedContestableMemory(t, srv)

	t.Run("replacing content without an intent is rejected", func(t *testing.T) {
		resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"embedding-provider-host","content":"# Different","loaded_version":1}}`)
		if !resp.IsError {
			t.Fatal("a content replacement must declare what it is doing to the claim")
		}
		for _, want := range ChangeIntents {
			if !strings.Contains(resp.Content[0].Text, want) {
				t.Errorf("the rejection must list %q: %s", want, resp.Content[0].Text)
			}
		}
	})

	t.Run("a metadata-only edit needs no intent", func(t *testing.T) {
		// An edit that touches a tag or a description makes no claim about the fact, so demanding
		// an intent for it would be friction with nothing behind it.
		resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"embedding-provider-host","description":"a better one-liner","loaded_version":1}}`)
		if resp.IsError {
			t.Fatalf("a metadata-only edit must not require an intent: %s", resp.Content[0].Text)
		}
	})

	t.Run("an invented intent is rejected", func(t *testing.T) {
		art, _ := srv.Storage.GetArticle("embedding-provider-host")
		resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"embedding-provider-host","content":"# Different","change_intent":"amend","loaded_version":`+itoa(art.Version)+`}}`)
		if !resp.IsError {
			t.Fatal("an out-of-vocabulary intent must be rejected")
		}
		if !strings.Contains(resp.Content[0].Text, "do not invent new ones") {
			t.Errorf("the rejection should say the vocabulary is closed: %s", resp.Content[0].Text)
		}
	})
}

// TestCorrectRequiresAnEditSummary keeps the intent from being a checkbox. An agent that can
// declare `correct` and leave no record of what the prior claim was, or why it was wrong, has
// performed the same silent overwrite with a label on it.
func TestCorrectRequiresAnEditSummary(t *testing.T) {
	srv := newMCPServer(t)
	seedContestableMemory(t, srv)

	resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"embedding-provider-host","content":"# Fact\n\nIt runs on the swarm.","change_intent":"correct","loaded_version":1}}`)
	if !resp.IsError {
		t.Fatal("'correct' without an edit_summary must be rejected")
	}
	if !strings.Contains(resp.Content[0].Text, "silent overwrite") {
		t.Errorf("the rejection should say why it matters: %s", resp.Content[0].Text)
	}

	// Whitespace is not a summary, for the same reason it is not a description.
	blank := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"embedding-provider-host","content":"# Fact\n\nIt runs on the swarm.","change_intent":"correct","edit_summary":"   ","loaded_version":1}}`)
	if !blank.IsError {
		t.Error("a whitespace-only edit_summary must be rejected too")
	}

	with := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"embedding-provider-host","content":"# Fact\n\nIt runs on the swarm.","change_intent":"correct","edit_summary":"mars was decommissioned; the provider moved to the swarm","loaded_version":1}}`)
	if with.IsError {
		t.Fatalf("'correct' with a summary must be accepted: %s", with.Content[0].Text)
	}
	art, _ := srv.Storage.GetArticle("embedding-provider-host")
	if !strings.Contains(art.Content, "the swarm") {
		t.Error("a correction must actually replace the content")
	}
}

func TestRefineReplacesContentNormally(t *testing.T) {
	srv := newMCPServer(t)
	seedContestableMemory(t, srv)

	resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"embedding-provider-host","content":"# Fact\n\nThe embedding provider runs on mars, not the swarm.","change_intent":"refine","loaded_version":1}}`)
	if resp.IsError {
		t.Fatalf("refine should behave exactly as an edit always did: %s", resp.Content[0].Text)
	}
	art, _ := srv.Storage.GetArticle("embedding-provider-host")
	if !strings.Contains(art.Content, "not the swarm") {
		t.Error("refine must replace the content")
	}
	if hasTag(art.Tags, ContestedTag) {
		t.Error("refine must not mark the memory contested")
	}
}

// TestContradictPreservesTheClaim is the heart of WS5. The agent said it could not adjudicate, so
// nothing here adjudicates on its behalf: both claims survive and a human decides.
func TestContradictPreservesTheClaim(t *testing.T) {
	srv := newMCPServer(t)
	seedContestableMemory(t, srv)

	resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"embedding-provider-host","content":"Measured 2026-08-30: the provider answered from a swarm node, not mars.","change_intent":"contradict","edit_summary":"observed on the swarm during the 0.14 roll","loaded_version":1}}`)
	if resp.IsError {
		t.Fatalf("contradict must be accepted: %s", resp.Content[0].Text)
	}

	art, err := srv.Storage.GetArticle("embedding-provider-host")
	if err != nil {
		t.Fatal(err)
	}

	// The original claim survives, verbatim.
	if !strings.Contains(art.Content, "The embedding provider runs on mars.") {
		t.Fatalf("contradict must NOT replace the existing claim:\n%s", art.Content)
	}
	// The conflicting claim is recorded beside it.
	if !strings.Contains(art.Content, "answered from a swarm node") {
		t.Errorf("the conflicting claim must be recorded:\n%s", art.Content)
	}
	if !strings.Contains(art.Content, "[!WARNING] Contested") {
		t.Errorf("the conflict must be marked as a Contested block:\n%s", art.Content)
	}
	// The edit summary is carried into the block, so a human can see what the agent believed.
	if !strings.Contains(art.Content, "observed on the swarm") {
		t.Errorf("the reported reason should appear in the block:\n%s", art.Content)
	}
	if !hasTag(art.Tags, ContestedTag) {
		t.Errorf("the memory must be tagged %q, got %v", ContestedTag, art.Tags)
	}

	// The response has to explain that the content was preserved, or an agent will assume its
	// edit landed and move on believing the memory now says something it does not.
	text := resp.Content[0].Text
	if !strings.Contains(text, "NOT replaced") {
		t.Errorf("the response must say the content was preserved:\n%s", text)
	}
	if !strings.Contains(text, "contested_memories") {
		t.Errorf("the response should say where the conflict surfaces:\n%s", text)
	}
}

// TestContestedTagSurvivesATagReplacement guards the ordering. A caller that replaces the tag list
// in the same call must not be able to drop the mark the call itself is applying.
func TestContestedTagSurvivesATagReplacement(t *testing.T) {
	srv := newMCPServer(t)
	seedContestableMemory(t, srv)

	resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"embedding-provider-host","content":"Conflicting observation.","change_intent":"contradict","tags":["observability"],"loaded_version":1}}`)
	if resp.IsError {
		t.Fatalf("edit failed: %s", resp.Content[0].Text)
	}
	art, _ := srv.Storage.GetArticle("embedding-provider-host")
	if !hasTag(art.Tags, ContestedTag) {
		t.Errorf("a tag replacement must not drop the contested mark, got %v", art.Tags)
	}
	if !hasTag(art.Tags, "observability") {
		t.Errorf("the caller's own tags must survive too, got %v", art.Tags)
	}
}

func TestWikiHealthReportsContestedMemories(t *testing.T) {
	srv := newMCPServer(t)
	seedContestableMemory(t, srv)

	before := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`).StructuredContent.(HealthOutput)
	if before.ContestedCount != 0 {
		t.Fatalf("nothing is contested yet, got %d", before.ContestedCount)
	}

	if resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"embedding-provider-host","content":"Conflicting observation.","change_intent":"contradict","loaded_version":1}}`); resp.IsError {
		t.Fatalf("edit failed: %s", resp.Content[0].Text)
	}

	out := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`).StructuredContent.(HealthOutput)
	if out.ContestedCount != 1 {
		t.Fatalf("contested_memory_count = %d, want 1", out.ContestedCount)
	}
	if len(out.ContestedMemories) != 1 || out.ContestedMemories[0].Slug != "embedding-provider-host" {
		t.Errorf("contested_memories = %+v", out.ContestedMemories)
	}
	if !strings.Contains(out.ContestedMemories[0].Detail, "change_intent") {
		t.Error("the finding should name how to resolve it")
	}

	t.Run("resolving it clears the finding", func(t *testing.T) {
		art, _ := srv.Storage.GetArticle("embedding-provider-host")
		resolve := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"embedding-provider-host","content":"# Fact\n\nThe provider runs on the swarm; the mars claim was stale.","change_intent":"correct","edit_summary":"adjudicated: the swarm observation was right","tags":[],"loaded_version":`+itoa(art.Version)+`}}`)
		if resolve.IsError {
			t.Fatalf("resolve failed: %s", resolve.Content[0].Text)
		}
		after := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`).StructuredContent.(HealthOutput)
		if after.ContestedCount != 0 {
			t.Errorf("contested_memory_count = %d after resolving, want 0", after.ContestedCount)
		}
	})
}

// TestContradictIsHonestAboutItsLimit records what this does not do, so nobody later mistakes the
// declared path for detection. `change_intent` is self-reported: an agent that means to overwrite
// can still say `refine`. Real detection needs semantic comparison, which is tabled.
//
// The value here is that the honest path is cheap and available, not that the dishonest one is
// impossible.
func TestContradictIsHonestAboutItsLimit(t *testing.T) {
	srv := newMCPServer(t)
	seedContestableMemory(t, srv)

	// A flatly contradictory replacement declared as `refine` is accepted. This is the documented
	// limit, asserted so a future reader sees it was known rather than missed.
	resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"embedding-provider-host","content":"# Fact\n\nThe embedding provider has never run on mars.","change_intent":"refine","loaded_version":1}}`)
	if resp.IsError {
		t.Fatalf("an undeclared contradiction is not detectable and must not be blocked: %s", resp.Content[0].Text)
	}
	art, _ := srv.Storage.GetArticle("embedding-provider-host")
	if hasTag(art.Tags, ContestedTag) {
		t.Error("nothing detects an undeclared contradiction; it must not be marked contested")
	}
}

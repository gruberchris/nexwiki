package server

import (
	"strings"
	"testing"
)

// The provenance gate moves a rule that already existed — "set a description and a source on
// everything you create" — from prose an agent may not have loaded into the write path, where it
// holds unconditionally.
//
// wiki_health has always reported memories with no source. That check is on the wrong side of the
// write: a fact whose origin was never recorded cannot have its origin recovered by a later
// report. These tests pin both halves — the intake closes, and the existing backlog is untouched.

func TestMemoryProvenanceIsRequiredAtCreation(t *testing.T) {
	srv := newMCPServer(t)

	cases := []struct {
		name string
		args string
		want []string
	}{
		{
			name: "neither field",
			args: `{"memory_kind":"project","title":"No Provenance","content":"# fact"}`,
			want: []string{"description", "source", "both fields"},
		},
		{
			name: "description only",
			args: `{"memory_kind":"project","title":"Half Provenance","content":"# fact","description":"a summary"}`,
			want: []string{"source", "this field"},
		},
		{
			name: "source only",
			args: `{"memory_kind":"project","title":"Other Half","content":"# fact","source":"a session"}`,
			want: []string{"description", "this field"},
		},
		{
			// " " satisfies a naive required-field check and fails the health check that motivated
			// the gate. The two have to agree on what counts as present, or the gate passes writes
			// the report then flags.
			name: "whitespace is not a value",
			args: `{"memory_kind":"project","title":"Blank Provenance","content":"# fact","description":"   ","source":"\t"}`,
			want: []string{"description", "source"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":`+tc.args+`}`)
			if !resp.IsError {
				t.Fatal("expected the create to be refused")
			}
			text := resp.Content[0].Text
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("rejection should mention %q: %s", want, text)
				}
			}
			// The message has to say *why* each field matters, or an agent learns to pass "x" to
			// get past it — which satisfies the gate and defeats its purpose.
			if strings.Contains(text, "'source'") && !strings.Contains(text, "re-verified") {
				t.Errorf("the source rejection should say why provenance matters: %s", text)
			}
			if strings.Contains(text, "'description'") && !strings.Contains(text, "get_context_overview") {
				t.Errorf("the description rejection should say where it is used: %s", text)
			}
		})
	}

	t.Run("a refused create writes nothing", func(t *testing.T) {
		if _, err := srv.Storage.GetArticle("no-provenance"); err == nil {
			t.Error("a refused create must not have written the document")
		}
	})

	t.Run("both fields present succeeds and stores them", func(t *testing.T) {
		resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"memory_kind":"project","title":"Full Provenance","content":"# fact","description":"the one-line summary","source":"design review 2026-08-30"}}`)
		if resp.IsError {
			t.Fatalf("a fully provenanced memory must be accepted: %s", resp.Content[0].Text)
		}
		art, err := srv.Storage.GetArticle("full-provenance")
		if err != nil {
			t.Fatal(err)
		}
		if art.Description != "the one-line summary" || art.Source != "design review 2026-08-30" {
			t.Errorf("fields were not stored: description=%q source=%q", art.Description, art.Source)
		}
	})
}

// TestProvenanceGateIsScopedToMemoryCreation records the three deliberate limits on the gate.
// Each of them is a place the same requirement could have been applied and was not, and the
// reasons differ — so a later change that extends the gate should have to argue past this test
// rather than discover the constraint by breaking something.
func TestProvenanceGateIsScopedToMemoryCreation(t *testing.T) {
	srv := newMCPServer(t)

	t.Run("edit keeps pointer semantics", func(t *testing.T) {
		// A caller fixing a memory's body must not be forced to restate provenance it is not
		// changing. Omitted means preserve, exactly as before.
		create := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"memory_kind":"project","title":"Editable Memory","content":"# original","description":"summary","source":"origin"}}`)
		if create.IsError {
			t.Fatalf("setup failed: %s", create.Content[0].Text)
		}
		resp := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"editable-memory","content":"# corrected","loaded_version":1}}`)
		if resp.IsError {
			t.Fatalf("an edit that omits provenance must be accepted: %s", resp.Content[0].Text)
		}
		art, _ := srv.Storage.GetArticle("editable-memory")
		if art.Description != "summary" || art.Source != "origin" {
			t.Errorf("an edit must preserve provenance it did not mention: description=%q source=%q", art.Description, art.Source)
		}
	})

	t.Run("plans and skills are untouched", func(t *testing.T) {
		// Same argument would apply, but there is no evidence of the same problem, and a gate
		// added without one is friction with no measured benefit. Deferred rather than declined.
		// project_context is a pre-existing requirement of create_agent_plan, unrelated to this
		// gate; it is supplied so the assertion is about provenance and nothing else.
		if resp := toolCall(t, srv, `{"name":"create_agent_plan","arguments":{"title":"Sourceless Plan","content":"# plan","project_context":"nexwiki"}}`); resp.IsError {
			t.Errorf("plan creation must not have acquired the gate: %s", resp.Content[0].Text)
		}
		if resp := toolCall(t, srv, `{"name":"create_agent_skill","arguments":{"title":"Sourceless Skill","content":"# skill"}}`); resp.IsError {
			t.Errorf("skill creation must not have acquired the gate: %s", resp.Content[0].Text)
		}
	})

	t.Run("append does not demand provenance either", func(t *testing.T) {
		resp := toolCall(t, srv, `{"name":"append_agent_memory","arguments":{"slug":"editable-memory","content_to_append":"more detail"}}`)
		if resp.IsError {
			t.Errorf("append must stay usable without restating provenance: %s", resp.Content[0].Text)
		}
	})
}

// TestUnsourcedMemoriesAreStillReported is the half of the design that is easy to lose. The gate
// closes the intake; it does not rewrite history, and it must not quietly make the existing
// backlog unreportable. A memory with no source stays valid, stays editable, and keeps showing up
// in wiki_health until somebody fixes it.
func TestUnsourcedMemoriesAreStillReported(t *testing.T) {
	srv := newMCPServer(t)

	// Seeded through storage, which is the only route left — and precisely the shape of every
	// memory written before the gate existed.
	if _, err := srv.Storage.SaveArticle("", "Legacy Unsourced", "# fact", "has a summary", "", "", "seed",
		[]string{MemoryScopeTagPrefix + "nexwiki"}, ContentTypeMemory); err != nil {
		t.Fatalf("seeding failed: %v", err)
	}

	resp := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
	if resp.IsError {
		t.Fatalf("wiki_health failed: %s", resp.Content[0].Text)
	}
	out, ok := resp.StructuredContent.(HealthOutput)
	if !ok {
		t.Fatalf("expected HealthOutput, got %T", resp.StructuredContent)
	}
	if out.UnsourcedCount != 1 {
		t.Fatalf("unsourced_memory_count = %d, want 1 — closing the intake must not stop the report", out.UnsourcedCount)
	}
	if len(out.UnsourcedMemory) != 1 || out.UnsourcedMemory[0].Slug != "legacy-unsourced" {
		t.Errorf("unsourced_memories = %+v, want the legacy memory", out.UnsourcedMemory)
	}

	t.Run("it stays editable, and the edit can fix it", func(t *testing.T) {
		art, _ := srv.Storage.GetArticle("legacy-unsourced")
		fix := toolCall(t, srv, `{"name":"edit_agent_memory","arguments":{"slug":"legacy-unsourced","source":"recovered from the 2026-08 session","loaded_version":`+itoa(art.Version)+`}}`)
		if fix.IsError {
			t.Fatalf("an unsourced memory must remain editable: %s", fix.Content[0].Text)
		}
		after := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
		if after.StructuredContent.(HealthOutput).UnsourcedCount != 0 {
			t.Error("setting a source should clear the finding")
		}
	})
}

// TestCreateGatesReportInAStableOrder pins how the two required-field gates on
// create_agent_memory interact. They were built in separate changes, so the order they fire in is
// currently an artefact of where each landed in the handler rather than a decision — written down
// here so a change to it has to be deliberate.
//
// The kind gate runs first, which is the better order: kind is a single closed-vocabulary choice
// an agent can make immediately, while provenance asks it to recall where the knowledge came
// from. Reporting the cheap requirement first gets a retry moving sooner.
//
// What matters more than the order is that a caller missing both is never left guessing — fixing
// the reported problem must reveal the next one rather than dead-ending.
func TestCreateGatesReportInAStableOrder(t *testing.T) {
	srv := newMCPServer(t)

	missingBoth := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"Missing Both","content":"# fact"}}`)
	if !missingBoth.IsError {
		t.Fatal("a memory missing both kind and provenance must be refused")
	}
	if !strings.Contains(missingBoth.Content[0].Text, "memory_kind") {
		t.Errorf("the kind gate should report first: %s", missingBoth.Content[0].Text)
	}

	// Fixing what was reported must surface the next requirement, not silently succeed.
	missingProvenance := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"memory_kind":"project","title":"Missing Both","content":"# fact"}}`)
	if !missingProvenance.IsError {
		t.Fatal("supplying a kind must not let a memory through without provenance")
	}
	if !strings.Contains(missingProvenance.Content[0].Text, "description") {
		t.Errorf("the provenance gate should report next: %s", missingProvenance.Content[0].Text)
	}

	// And satisfying both writes it, with both gates' fields stored.
	complete := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"memory_kind":"project","title":"Missing Both","content":"# fact","description":"d","source":"s"}}`)
	if complete.IsError {
		t.Fatalf("a call satisfying both gates must succeed: %s", complete.Content[0].Text)
	}
	art, err := srv.Storage.GetArticle("missing-both")
	if err != nil {
		t.Fatal(err)
	}
	if art.MemoryKind != "project" || art.Source != "s" {
		t.Errorf("both gates' fields should be stored: kind=%q source=%q", art.MemoryKind, art.Source)
	}
}

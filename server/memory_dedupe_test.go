package server

import (
	"strings"
	"testing"
)

// The duplicate check moves work the agent used to do onto the server.
//
// wiki_health has always found near-duplicate memories, but only after both exist. Running the
// same comparison at create time catches it while there is still one document — and, more
// importantly, it lets the agent stop performing retrieval chains before every write. That chain
// is livelock-shaped, and the server already owns the comparison.

func createMemory(t *testing.T, srv *Server, title, scope string) ToolResponse {
	t.Helper()
	args := `{"title":"` + title + `","content":"# fact","memory_kind":"project","description":"d","source":"s"`
	if scope != "" {
		args += `,"memory_type":"` + scope + `"`
	}
	args += `}`
	return toolCall(t, srv, `{"name":"create_agent_memory","arguments":`+args+`}`)
}

func TestDuplicateCheckWarnsButStillWrites(t *testing.T) {
	srv := newMCPServer(t)

	if resp := createMemory(t, srv, "Programming Language Article Format Template", "rules"); resp.IsError {
		t.Fatalf("setup failed: %s", resp.Content[0].Text)
	}

	resp := createMemory(t, srv, "SQL Dialect Article Format Template", "rules")
	if resp.IsError {
		t.Fatalf("the check must never block a write: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text

	// The write is the assertion that matters most. This is a title heuristic, and
	// parallel-by-design documents legitimately share titles — refusing would break real work.
	if _, err := srv.Storage.GetArticle("sql-dialect-article-format-template"); err != nil {
		t.Fatalf("the memory must still have been created: %v", err)
	}

	if !strings.Contains(text, "closely resembles") {
		t.Fatalf("expected a duplicate warning:\n%s", text)
	}
	// Named, so the agent can act without another lookup.
	if !strings.Contains(text, "programming-language-article-format-template") {
		t.Errorf("the warning must name the sibling's slug:\n%s", text)
	}
	if !strings.Contains(text, "% title overlap") {
		t.Errorf("the warning must report the measured overlap:\n%s", text)
	}
	for _, tool := range []string{"append_agent_memory", "edit_agent_memory"} {
		if !strings.Contains(text, tool) {
			t.Errorf("the warning must suggest %s:\n%s", tool, text)
		}
	}
}

// TestDuplicateCheckReportsTheNegative is the assertion that makes the single-lookup rule
// enforceable. An agent told "compared against N and found nothing" has a *completed check*; a
// silent negative is indistinguishable from a check that never ran, and an agent that cannot tell
// will search again — the exact loop this design exists to stop.
func TestDuplicateCheckReportsTheNegative(t *testing.T) {
	srv := newMCPServer(t)

	for _, title := range []string{"Deploy Constraint", "Metrics Dashboard"} {
		if resp := createMemory(t, srv, title, "nexwiki"); resp.IsError {
			t.Fatalf("setup failed: %s", resp.Content[0].Text)
		}
	}

	resp := createMemory(t, srv, "Wholly Unrelated Subject", "nexwiki")
	if resp.IsError {
		t.Fatalf("create failed: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text

	if strings.Contains(text, "closely resembles") {
		t.Errorf("an unrelated title must not warn:\n%s", text)
	}
	if !strings.Contains(text, "no near-duplicate") {
		t.Errorf("the negative outcome must still be reported:\n%s", text)
	}
	if !strings.Contains(text, "compared against 2 existing memories") {
		t.Errorf("the report must say how many were compared:\n%s", text)
	}
	if !strings.Contains(text, "do not search again") {
		t.Errorf("the report must state the check is complete:\n%s", text)
	}
}

// TestDuplicateCheckIsScoped mirrors the report's scoping. A "Deployment Notes" memory about
// docker and one about nexwiki are *supposed* to be separate documents; pairing them would report
// the scoping system working as intended.
func TestDuplicateCheckIsScoped(t *testing.T) {
	srv := newMCPServer(t)

	if resp := createMemory(t, srv, "Cluster Deployment Notes", "docker"); resp.IsError {
		t.Fatalf("setup failed: %s", resp.Content[0].Text)
	}

	resp := createMemory(t, srv, "Cluster Deployment Notes Two", "nexwiki")
	if resp.IsError {
		t.Fatalf("create failed: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text
	if strings.Contains(text, "closely resembles") {
		t.Errorf("memories in different scopes must not pair:\n%s", text)
	}
	if !strings.Contains(text, "scope nexwiki") {
		t.Errorf("the report must name the scope it compared within:\n%s", text)
	}

	// And the same title *in* the scope does pair, proving the scoping is what suppressed it.
	same := createMemory(t, srv, "Cluster Deployment Notes Three", "nexwiki")
	if !strings.Contains(same.Content[0].Text, "closely resembles") {
		t.Errorf("a same-scope near-duplicate must warn:\n%s", same.Content[0].Text)
	}
}

// TestDuplicateCheckSuppressesCrossLinkedPairs carries over the suppression that took the report
// from one false positive on the live corpus to zero: an author who has linked two documents
// together already knows both exist and has decided to keep them separate. Telling them to
// consider merging is telling them something they have already answered.
func TestDuplicateCheckSuppressesCrossLinkedPairs(t *testing.T) {
	srv := newMCPServer(t)

	if resp := createMemory(t, srv, "Programming Language Article Format Template", "rules"); resp.IsError {
		t.Fatalf("setup failed: %s", resp.Content[0].Text)
	}

	// The candidate links the sibling in its own body — the "I know it exists, they are parallel"
	// signal, available here without a link-graph scan because it is the candidate's own content.
	resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"SQL Dialect Article Format Template","content":"# fact\n\nSee also [[programming-language-article-format-template]].","memory_kind":"project","memory_type":"rules","description":"d","source":"s"}}`)
	if resp.IsError {
		t.Fatalf("create failed: %s", resp.Content[0].Text)
	}
	if strings.Contains(resp.Content[0].Text, "closely resembles") {
		t.Errorf("a cross-linked pair must be suppressed:\n%s", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, "no near-duplicate") {
		t.Errorf("suppression should still report a completed check:\n%s", resp.Content[0].Text)
	}
}

// TestDuplicateCheckAgreesWithWikiHealth is the reason the rule lives in one function. Two callers
// with two copies of a threshold is how a gate and a report come to disagree about what counts as
// a duplicate; this asserts they do not.
func TestDuplicateCheckAgreesWithWikiHealth(t *testing.T) {
	srv := newMCPServer(t)

	if resp := createMemory(t, srv, "Programming Language Article Format Template", "rules"); resp.IsError {
		t.Fatalf("setup failed: %s", resp.Content[0].Text)
	}
	created := createMemory(t, srv, "SQL Dialect Article Format Template", "rules")
	warnedAtCreate := strings.Contains(created.Content[0].Text, "closely resembles")

	health := toolCall(t, srv, `{"name":"wiki_health","arguments":{}}`)
	out, ok := health.StructuredContent.(HealthOutput)
	if !ok {
		t.Fatalf("expected HealthOutput, got %T", health.StructuredContent)
	}
	reportedByHealth := out.DuplicateCount > 0

	if warnedAtCreate != reportedByHealth {
		t.Errorf("the create-time check and wiki_health disagree: create warned=%v, health reported=%v",
			warnedAtCreate, reportedByHealth)
	}
}

// TestDuplicateCheckNeverFailsAWrite covers the failure mode of an advisory check: it must not be
// able to take down the write path it decorates.
func TestDuplicateCheckNeverFailsAWrite(t *testing.T) {
	srv := newMCPServer(t)

	// An unscoped memory compares against the unscoped group, which is empty here. The check must
	// handle that without erroring, and must still report the outcome.
	resp := createMemory(t, srv, "First Memory Of All", "")
	if resp.IsError {
		t.Fatalf("an empty corpus must not fail the check: %s", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, "compared against 0 existing memories") {
		t.Errorf("an empty scope should report zero comparisons:\n%s", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, "(unscoped)") {
		t.Errorf("an unscoped memory should say so rather than printing an empty scope:\n%s", resp.Content[0].Text)
	}
}

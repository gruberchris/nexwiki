package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// The damper exists because of a real event: on 2026-08-18 an agent alternated read_article and
// search_wiki for 31 minutes and ~170 MCP calls. The remediation for that was more prose in the
// guidelines, and prose is what had already failed. These tests pin the behaviour that replaces it.
//
// Everything here goes through executeToolCall rather than the toolCall helper, because the damper
// is keyed on the resolved agent and only that path carries one.

// damperCall runs a tool call as a named agent and returns the text content.
func damperCall(t *testing.T, srv *Server, agent, params string) string {
	t.Helper()
	result, rpcErr := srv.executeToolCall(json.RawMessage(params), agent)
	if rpcErr != nil {
		t.Fatalf("rpc error: %v", rpcErr)
	}
	resp, ok := result.(ToolResponse)
	if !ok {
		t.Fatalf("expected ToolResponse, got %T", result)
	}
	if len(resp.Content) == 0 {
		return ""
	}
	return resp.Content[0].Text
}

func searchCall(query string) string {
	b, _ := json.Marshal(query)
	return `{"name":"search_wiki","arguments":{"query":` + string(b) + `}}`
}

func TestDamperEscalatesOnRepeatedLookups(t *testing.T) {
	srv := newMCPServer(t)

	first := damperCall(t, srv, "agent-a", searchCall("docker build error"))
	if strings.Contains(first, "repeats a lookup") || strings.Contains(first, "§0") {
		t.Fatalf("a first lookup must be untouched:\n%s", first)
	}

	second := damperCall(t, srv, "agent-a", searchCall("docker build error"))
	if !strings.Contains(second, "repeats a lookup") {
		t.Errorf("a second identical lookup should note the repeat:\n%s", second)
	}
	if strings.Contains(second, "§0") {
		t.Errorf("the second notice should stay light, not cite §0 yet:\n%s", second)
	}

	third := damperCall(t, srv, "agent-a", searchCall("docker build error"))
	if !strings.Contains(third, "§0") {
		t.Errorf("the third repeat must cite §0 explicitly:\n%s", third)
	}
	if !strings.Contains(third, "COMPLETED check") {
		t.Errorf("the third notice must state that the check is complete:\n%s", third)
	}
	if !strings.Contains(third, "create_") {
		t.Errorf("the third notice must point at the action that ends the loop:\n%s", third)
	}
}

// TestDamperCatchesRewordedRepeats is the case that matters. An agent asking the identical
// question twice is easy to catch and is *not* the failure mode that happened — the livelock was
// rewordings, which is why the fingerprint sorts its tokens.
func TestDamperCatchesRewordedRepeats(t *testing.T) {
	srv := newMCPServer(t)

	damperCall(t, srv, "agent-a", searchCall("docker build error"))
	reworded := damperCall(t, srv, "agent-a", searchCall("error building docker"))
	if !strings.Contains(reworded, "repeats a lookup") {
		t.Errorf("a reworded repeat must be caught:\n%s", reworded)
	}

	// Stop words and punctuation must not defeat it either.
	third := damperCall(t, srv, "agent-a", searchCall("What is the error, for docker, when building?"))
	if !strings.Contains(third, "§0") {
		t.Errorf("stop words and punctuation must not hide a repeat:\n%s", third)
	}
}

func TestDamperLeavesDistinctLookupsAlone(t *testing.T) {
	srv := newMCPServer(t)

	for _, q := range []string{"docker build error", "bleve index mapping", "oauth token audience"} {
		if out := damperCall(t, srv, "agent-a", searchCall(q)); strings.Contains(out, "repeats a lookup") {
			t.Errorf("distinct query %q was flagged as a repeat:\n%s", q, out)
		}
	}

	// The same words asked of a different tool are a different question, and must not collide.
	damperCall(t, srv, "agent-a", `{"name":"list_agent_memories","arguments":{"memory_type":"nexwiki"}}`)
	out := damperCall(t, srv, "agent-a", searchCall("nexwiki"))
	if strings.Contains(out, "repeats a lookup") {
		t.Errorf("a listing and a search must not share a fingerprint:\n%s", out)
	}
}

// TestDamperIsPerAgent guards the keying. Two agents connected to the same server are doing
// different work, and one agent's searches must not make the other look stuck.
func TestDamperIsPerAgent(t *testing.T) {
	srv := newMCPServer(t)

	damperCall(t, srv, "agent-a", searchCall("docker build error"))
	damperCall(t, srv, "agent-a", searchCall("docker build error"))

	out := damperCall(t, srv, "agent-b", searchCall("docker build error"))
	if strings.Contains(out, "repeats a lookup") {
		t.Errorf("agent-b's first lookup was blamed for agent-a's repeats:\n%s", out)
	}
}

// TestDamperResetsOnAWrite pins the escape hatch. A write is progress, and the loop being damped
// is read-only by nature — an agent that has just created something is not stuck in the retrieval
// cycle this exists to interrupt.
func TestDamperResetsOnAWrite(t *testing.T) {
	srv := newMCPServer(t)

	damperCall(t, srv, "agent-a", searchCall("docker build error"))
	if out := damperCall(t, srv, "agent-a", searchCall("docker build error")); !strings.Contains(out, "repeats a lookup") {
		t.Fatalf("setup: expected the repeat to be noticed:\n%s", out)
	}

	created := damperCall(t, srv, "agent-a", `{"name":"create_agent_memory","arguments":{"title":"Docker Build Note","content":"# fact","memory_kind":"project","description":"d","source":"s"}}`)
	if strings.Contains(created, "Error") {
		t.Fatalf("setup: create failed:\n%s", created)
	}

	after := damperCall(t, srv, "agent-a", searchCall("docker build error"))
	if strings.Contains(after, "repeats a lookup") || strings.Contains(after, "§0") {
		t.Errorf("a write should have cleared the ring:\n%s", after)
	}
}

// TestDamperWindowExpires uses the injectable clock rather than sleeping. Returning to a topic
// later in a session is normal work, not a loop.
func TestDamperWindowExpires(t *testing.T) {
	d := newLookupDamper()
	base := time.Now()
	d.now = func() time.Time { return base }

	if n, _ := d.observe("agent-a", "search_wiki", "docker build error"); n != 1 {
		t.Fatalf("first observe = %d, want 1", n)
	}
	if n, _ := d.observe("agent-a", "search_wiki", "docker build error"); n != 2 {
		t.Fatalf("second observe = %d, want 2", n)
	}

	d.now = func() time.Time { return base.Add(damperWindow + time.Second) }
	if n, _ := d.observe("agent-a", "search_wiki", "docker build error"); n != 1 {
		t.Errorf("after the window the lookup should be new again, got occurrence %d", n)
	}
}

// TestDamperWindowIsMeasuredFromTheFirstAsk records a deliberate choice: the entry's timestamp is
// not refreshed on a repeat. Refreshing would let a steady drip of repeats keep an entry alive
// forever, so a loop that stops expires on its own.
func TestDamperWindowIsMeasuredFromTheFirstAsk(t *testing.T) {
	d := newLookupDamper()
	base := time.Now()
	d.now = func() time.Time { return base }
	d.observe("agent-a", "search_wiki", "docker build error")

	// A repeat most of the way through the window...
	d.now = func() time.Time { return base.Add(damperWindow - time.Second) }
	if n, _ := d.observe("agent-a", "search_wiki", "docker build error"); n != 2 {
		t.Fatalf("expected occurrence 2, got %d", n)
	}
	// ...must not extend it.
	d.now = func() time.Time { return base.Add(damperWindow + time.Second) }
	if n, _ := d.observe("agent-a", "search_wiki", "docker build error"); n != 1 {
		t.Errorf("a repeat must not refresh the window, got occurrence %d", n)
	}
}

func TestDamperIsBounded(t *testing.T) {
	d := newLookupDamper()

	t.Run("the ring caps per agent", func(t *testing.T) {
		for i := 0; i < damperRingSize*3; i++ {
			d.observe("agent-a", "search_wiki", fmt.Sprintf("distinct query number %d", i))
		}
		d.mu.Lock()
		size := len(d.rings["agent-a"])
		d.mu.Unlock()
		if size > damperRingSize {
			t.Errorf("ring holds %d entries, cap is %d", size, damperRingSize)
		}
	})

	t.Run("agents are evicted least-recently-used", func(t *testing.T) {
		fresh := newLookupDamper()
		for i := 0; i < damperMaxAgents*2; i++ {
			fresh.observe(fmt.Sprintf("agent-%d", i), "search_wiki", "same query")
		}
		fresh.mu.Lock()
		agents := len(fresh.rings)
		_, oldestSurvives := fresh.rings["agent-0"]
		_, newestSurvives := fresh.rings[fmt.Sprintf("agent-%d", damperMaxAgents*2-1)]
		fresh.mu.Unlock()

		if agents > damperMaxAgents {
			t.Errorf("tracking %d agents, cap is %d", agents, damperMaxAgents)
		}
		if oldestSurvives {
			t.Error("the least recently active agent should have been evicted")
		}
		if !newestSurvives {
			t.Error("the most recent agent must survive eviction")
		}
	})
}

// TestDamperIsConcurrencySafe is meaningful only under -race, which CI runs.
func TestDamperIsConcurrencySafe(t *testing.T) {
	d := newLookupDamper()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			agent := fmt.Sprintf("agent-%d", n%3)
			for j := 0; j < 50; j++ {
				d.observe(agent, "search_wiki", fmt.Sprintf("query %d", j%5))
				if j%10 == 0 {
					d.clear(agent)
				}
			}
		}(i)
	}
	wg.Wait()
}

// TestDamperNeverTouchesStructuredContent guards the machine contract. structuredContent is parsed
// by clients; growing advisory prose into it would make a consumer handle a field that is
// sometimes an essay.
func TestDamperNeverTouchesStructuredContent(t *testing.T) {
	srv := newMCPServer(t)

	damperCall(t, srv, "agent-a", searchCall("docker build error"))
	result, rpcErr := srv.executeToolCall(json.RawMessage(searchCall("docker build error")), "agent-a")
	if rpcErr != nil {
		t.Fatalf("rpc error: %v", rpcErr)
	}
	resp := result.(ToolResponse)
	if !strings.Contains(resp.Content[0].Text, "repeats a lookup") {
		t.Fatalf("setup: expected a notice on the text half")
	}

	out, ok := resp.StructuredContent.(SearchOutput)
	if !ok {
		t.Fatalf("expected SearchOutput, got %T", resp.StructuredContent)
	}
	if out.Query != "docker build error" {
		t.Errorf("the structured payload must be untouched, got query %q", out.Query)
	}
}

// TestDamperNeverBlocks is the property that keeps a false positive cheap. The damper annotates;
// it must never turn a working lookup into an error or drop its results.
func TestDamperNeverBlocks(t *testing.T) {
	srv := newMCPServer(t)

	if resp := toolCall(t, srv, `{"name":"create_wiki_article","arguments":{"title":"Bleve Notes","content":"# bleve indexing"}}`); resp.IsError {
		t.Fatalf("setup failed: %s", resp.Content[0].Text)
	}

	var last ToolResponse
	for i := 0; i < 5; i++ {
		result, rpcErr := srv.executeToolCall(json.RawMessage(searchCall("bleve")), "agent-a")
		if rpcErr != nil {
			t.Fatalf("call %d returned an rpc error: %v", i, rpcErr)
		}
		last = result.(ToolResponse)
		if last.IsError {
			t.Fatalf("call %d became an error — the damper must never block: %s", i, last.Content[0].Text)
		}
	}

	out := last.StructuredContent.(SearchOutput)
	if out.Count == 0 {
		t.Error("the fifth repeat must still return its results")
	}
}

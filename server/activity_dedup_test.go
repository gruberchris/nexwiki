package server

import (
	"encoding/json"
	"testing"
)

// The dedup window on PublishActivity used to hide a second legitimate change to the same
// document inside 2 seconds (#173): every save produces a new revision, but the second of two
// quick writes had its event swallowed as a "duplicate" of the first, and with it the attribution
// get_article_history joins a revision to. The fix qualifies the dedup key with the revision the
// write produced, so only a double-emitted event for one save collapses.

// callMCP runs one tools/call through the same wrapper a real request takes, so the call is
// logged and published exactly as in production.
func callMCP(t *testing.T, srv *Server, args string, agent string) {
	t.Helper()
	result, rpcErr := srv.executeToolCall(json.RawMessage(args), agent)
	if rpcErr != nil || isToolError(result) {
		t.Fatalf("tool call failed (%v): %s", rpcErr, args)
	}
}

// TestQuickSuccessiveMCPeditsAreBothAttributed is the scenario of the issue: two edits of the
// same article by the same agent, within the dedup window. Both revisions must carry the agent's
// attribution, in get_recent_activity and in get_article_history.
func TestQuickSuccessiveMCPEditsAreBothAttributed(t *testing.T) {
	srv := newTestServer(t)
	persistPrimaryActivity(t, srv)
	const agent = "Quick Agent"

	callMCP(t, srv, `{"name":"save_article","arguments":{"title":"Attribution","content":"# v1"}}`, agent)
	callMCP(t, srv, `{"name":"save_article","arguments":{"slug":"attribution","title":"Attribution","content":"# v2","loaded_version":1}}`, agent)
	callMCP(t, srv, `{"name":"save_article","arguments":{"slug":"attribution","title":"Attribution","content":"# v3","loaded_version":2}}`, agent)

	// Both edits are in the activity log, each with the revision it produced.
	found, _ := srv.toolGetRecentActivity(json.RawMessage(`{"action":"edit","source":"mcp"}`))
	out, ok := found.(ToolResponse).StructuredContent.(ActivityOutput)
	if !ok {
		t.Fatalf("get_recent_activity returned %T", found)
	}
	if out.Count != 2 {
		t.Fatalf("get_recent_activity found %d edit events, want 2 — the second edit was deduplicated", out.Count)
	}
	for _, ev := range out.Events {
		if ev.Agent != agent || ev.Version < 2 || ev.Version > 3 {
			t.Errorf("unexpected edit event %+v", ev)
		}
	}

	// And get_article_history attributes both revisions to the agent.
	raw, rpcErr := srv.toolGetArticleHistory(json.RawMessage(`{"slug":"attribution"}`))
	if rpcErr != nil {
		t.Fatalf("get_article_history failed: %v", rpcErr)
	}
	history := raw.(ToolResponse).StructuredContent.(HistoryOutput)
	byVersion := map[int]RevisionRef{}
	for _, v := range history.Versions {
		byVersion[v.Version] = v
	}
	for version := 1; version <= 3; version++ {
		v, ok := byVersion[version]
		if !ok {
			t.Fatalf("revision %d is missing from history: %+v", version, history.Versions)
		}
		if v.Agent != agent || v.Via != "mcp" {
			t.Errorf("revision %d is unattributed (%+v) — its event was hidden by the dedup window", version, v)
		}
	}
}

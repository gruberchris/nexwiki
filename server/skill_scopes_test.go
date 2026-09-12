package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Story 02 tests: per-phase tool scopes enforced at the MCP layer. Each role's
// allowed paths succeed; each denied path fails with an explicit scope error naming
// the role and the missing scope; every denial is audited to the activity log.

// roleServer seeds one wiki article, one skill, and one memory with no role set,
// then fixes the role. Seeding runs unrestricted so the tests measure the gate,
// not the fixtures.
func roleServer(t *testing.T, role string) *Server {
	t.Helper()
	srv := newMCPServer(t)
	if _, err := srv.Storage.SaveArticle("", "Pattern Note", "# pattern body", "", "", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatalf("seeding wiki failed: %v", err)
	}
	seedSkill(t, srv.Storage, "Evolving Skill", "# v1 body")
	resp := toolCall(t, srv, `{"name":"create_agent_memory","arguments":{"title":"Raw Observation","content":"# saw X","memory_kind":"reference","description":"a raw note","source":"test"}}`)
	if resp.IsError {
		t.Fatalf("seeding memory failed: %s", resp.Content[0].Text)
	}
	srv.WikiskillRole = role
	return srv
}

// roleCall runs a tool through the full MCP path (gate + logging) as agent.
func roleCall(t *testing.T, srv *Server, agent, toolJSON string) ToolResponse {
	t.Helper()
	res, rpcErr := srv.executeToolCall(json.RawMessage(toolJSON), agent)
	if rpcErr != nil {
		t.Fatalf("executeToolCall failed with RPC error: %v", rpcErr)
	}
	resp, ok := res.(ToolResponse)
	if !ok {
		t.Fatalf("expected ToolResponse, got %T", res)
	}
	return resp
}

// denyEvents returns the audited scope denials in the in-memory activity history.
func denyEvents(srv *Server) []LogEvent {
	var out []LogEvent
	for _, ev := range srv.EventBus.GetHistory() {
		if ev.Action == DenyAction {
			out = append(out, ev)
		}
	}
	return out
}

func deniedFor(t *testing.T, srv *Server, tool string) LogEvent {
	t.Helper()
	for _, ev := range denyEvents(srv) {
		if ev.Tool == tool {
			return ev
		}
	}
	t.Fatalf("no audited denial for tool %q (denials: %+v)", tool, denyEvents(srv))
	return LogEvent{}
}

func assertScopeDenial(t *testing.T, resp ToolResponse, role, scope string) {
	t.Helper()
	if !resp.IsError {
		t.Fatalf("expected a scope denial, got success: %v", resp.Content)
	}
	text := resp.Content[0].Text
	if !strings.Contains(text, "'"+role+"'") {
		t.Errorf("denial must name role '%s', got: %s", role, text)
	}
	if !strings.Contains(text, scope) {
		t.Errorf("denial must name missing scope '%s', got: %s", scope, text)
	}
}

func TestWikiskillRoleNormalization(t *testing.T) {
	cases := map[string]string{
		"": "…", "inference": WikiskillRoleInference, "INFERENCE": WikiskillRoleInference,
		" maintainer ": WikiskillRoleMaintainer, "Proposer": WikiskillRoleProposer,
		"operator": "", "admin": "",
	}
	for raw, want := range cases {
		if want == "…" {
			if got := NormalizeWikiskillRole(raw); got != "" {
				t.Errorf("NormalizeWikiskillRole(%q) = %q, want unrestricted", raw, got)
			}
			continue
		}
		if got := NormalizeWikiskillRole(raw); got != want {
			t.Errorf("NormalizeWikiskillRole(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestUnrestrictedByDefault(t *testing.T) {
	srv := roleServer(t, "")
	// No role: the pre-story-02 behavior holds, including the full candidate flow.
	resp := roleCall(t, srv, "op", `{"name":"propose_skill_candidate","arguments":{"parent_slug":"evolving-skill","proposed_body":"# v2 body"}}`)
	if resp.IsError {
		t.Fatalf("unrestricted propose failed: %s", resp.Content[0].Text)
	}
	cands, err := srv.Storage.ListSkillCandidates()
	if err != nil || len(cands) != 1 {
		t.Fatalf("expected one candidate, got %+v / %v", cands, err)
	}
	resp2 := roleCall(t, srv, "op", `{"name":"promote_skill_candidate","arguments":{"id":"`+cands[0].ID+`"}}`)
	if resp2.IsError {
		t.Fatalf("unrestricted promote failed: %s", resp2.Content[0].Text)
	}
	if len(denyEvents(srv)) != 0 {
		t.Errorf("unrestricted calls must not audit denials, got %+v", denyEvents(srv))
	}
}

func TestInferenceAllowedPaths(t *testing.T) {
	srv := roleServer(t, WikiskillRoleInference)
	if resp := roleCall(t, srv, "inf", `{"name":"list_agent_skills","arguments":{}}`); resp.IsError {
		t.Errorf("inference list_agent_skills denied: %s", resp.Content[0].Text)
	}
	if resp := roleCall(t, srv, "inf", `{"name":"read_article","arguments":{"slug":"evolving-skill"}}`); resp.IsError {
		t.Errorf("inference read of a skill denied: %s", resp.Content[0].Text)
	}
	if resp := roleCall(t, srv, "inf", `{"name":"append_agent_memory","arguments":{"slug":"raw-observation","content_to_append":"more"}}`); resp.IsError {
		t.Errorf("inference raw append denied: %s", resp.Content[0].Text)
	}
	if resp := roleCall(t, srv, "inf", `{"name":"get_status_tags","arguments":{}}`); resp.IsError {
		t.Errorf("inference get_status_tags denied: %s", resp.Content[0].Text)
	}
}

func TestInferenceDeniedWikiReads(t *testing.T) {
	srv := roleServer(t, WikiskillRoleInference)
	resp := roleCall(t, srv, "inf", `{"name":"read_article","arguments":{"slug":"pattern-note"}}`)
	assertScopeDenial(t, resp, WikiskillRoleInference, ScopeWikiRead)
	resp2 := roleCall(t, srv, "inf", `{"name":"search_wiki","arguments":{"query":"pattern"}}`)
	assertScopeDenial(t, resp2, WikiskillRoleInference, ScopeWikiRead)
	// Log reads are wiki-layer reads too.
	resp3 := roleCall(t, srv, "inf", `{"name":"get_recent_activity","arguments":{}}`)
	assertScopeDenial(t, resp3, WikiskillRoleInference, ScopeAuditRead)

	ev := deniedFor(t, srv, "read_article")
	if ev.Slug != "pattern-note" || ev.Agent != "inf" {
		t.Errorf("denial audit must carry slug and agent, got %+v", ev)
	}
	deniedFor(t, srv, "search_wiki")
	deniedFor(t, srv, "get_recent_activity")
}

func TestInferenceDeniedPromoteAndPublish(t *testing.T) {
	srv := roleServer(t, WikiskillRoleInference)
	resp := roleCall(t, srv, "inf", `{"name":"promote_skill_candidate","arguments":{"id":"whatever"}}`)
	assertScopeDenial(t, resp, WikiskillRoleInference, ScopeSkillsPromote)
	resp2 := roleCall(t, srv, "inf", `{"name":"create_agent_skill","arguments":{"title":"Sneaky","content":"# x"}}`)
	assertScopeDenial(t, resp2, WikiskillRoleInference, ScopeSkillsPublish)
	resp3 := roleCall(t, srv, "inf", `{"name":"propose_skill_candidate","arguments":{"parent_slug":"evolving-skill","proposed_body":"# x"}}`)
	assertScopeDenial(t, resp3, WikiskillRoleInference, ScopeCandidatePropose)
}

func TestMaintainerAllowedPaths(t *testing.T) {
	srv := roleServer(t, WikiskillRoleMaintainer)
	if resp := roleCall(t, srv, "mnt", `{"name":"create_wiki_article","arguments":{"title":"Curated Note","content":"# curated"}}`); resp.IsError {
		t.Errorf("maintainer wiki write denied: %s", resp.Content[0].Text)
	}
	live, err := srv.Storage.GetArticle("pattern-note")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	editJSON := `{"name":"edit_wiki_article","arguments":{"slug":"pattern-note","title":"` + live.Title + `","content":"# revised","loaded_version":` + fmt.Sprint(live.Version) + `}}`
	if resp := roleCall(t, srv, "mnt", editJSON); resp.IsError {
		t.Errorf("maintainer wiki edit denied: %s", resp.Content[0].Text)
	}
	if resp := roleCall(t, srv, "mnt", `{"name":"search_wiki","arguments":{"query":"pattern"}}`); resp.IsError {
		t.Errorf("maintainer wiki read denied: %s", resp.Content[0].Text)
	}
	if resp := roleCall(t, srv, "mnt", `{"name":"create_agent_memory","arguments":{"title":"M2","content":"# m","memory_kind":"reference","description":"d","source":"t"}}`); resp.IsError {
		t.Errorf("maintainer raw write denied: %s", resp.Content[0].Text)
	}
	if resp := roleCall(t, srv, "mnt", `{"name":"read_article","arguments":{"slug":"evolving-skill"}}`); resp.IsError {
		t.Errorf("maintainer skill read denied: %s", resp.Content[0].Text)
	}
	if resp := roleCall(t, srv, "mnt", `{"name":"get_recent_activity","arguments":{}}`); resp.IsError {
		t.Errorf("maintainer log read denied: %s", resp.Content[0].Text)
	}
}

func TestMaintainerDeniedPromotePublishAudit(t *testing.T) {
	srv := roleServer(t, WikiskillRoleMaintainer)
	resp := roleCall(t, srv, "mnt", `{"name":"promote_skill_candidate","arguments":{"id":"whatever"}}`)
	assertScopeDenial(t, resp, WikiskillRoleMaintainer, ScopeSkillsPromote)
	resp2 := roleCall(t, srv, "mnt", `{"name":"create_agent_skill","arguments":{"title":"Sneaky","content":"# x"}}`)
	assertScopeDenial(t, resp2, WikiskillRoleMaintainer, ScopeSkillsPublish)
	// A generic wiki edit targeted at a skill is a publish, not a wiki write.
	live, err := srv.Storage.GetArticle("evolving-skill")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	editJSON := `{"name":"edit_wiki_article","arguments":{"slug":"evolving-skill","title":"` + live.Title + `","content":"# hijack","loaded_version":` + fmt.Sprint(live.Version) + `}}`
	resp3 := roleCall(t, srv, "mnt", editJSON)
	assertScopeDenial(t, resp3, WikiskillRoleMaintainer, ScopeSkillsPublish)
	deniedFor(t, srv, "promote_skill_candidate")
	deniedFor(t, srv, "create_agent_skill")
}

func TestProposerAllowedPaths(t *testing.T) {
	srv := roleServer(t, WikiskillRoleProposer)
	if resp := roleCall(t, srv, "pro", `{"name":"search_wiki","arguments":{"query":"pattern"}}`); resp.IsError {
		t.Errorf("proposer wiki read denied: %s", resp.Content[0].Text)
	}
	if resp := roleCall(t, srv, "pro", `{"name":"read_article","arguments":{"slug":"pattern-note"}}`); resp.IsError {
		t.Errorf("proposer wiki read denied: %s", resp.Content[0].Text)
	}
	resp := roleCall(t, srv, "pro", `{"name":"propose_skill_candidate","arguments":{"parent_slug":"evolving-skill","proposed_body":"# v2 body","diff":"--- a\n+++ b","pattern_slugs":["pattern-note"],"proposer":"proposer-harness"}}`)
	if resp.IsError {
		t.Fatalf("proposer candidate propose failed: %s", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, "evolving-skill") {
		t.Errorf("propose response should name the parent, got: %s", resp.Content[0].Text)
	}
	cands, err := srv.Storage.ListSkillCandidates()
	if err != nil || len(cands) != 1 || cands[0].Proposer != "proposer-harness" {
		t.Errorf("candidate not recorded as proposed: %+v / %v", cands, err)
	}
}

func TestProposerDeniedPromoteAuditPublish(t *testing.T) {
	srv := roleServer(t, WikiskillRoleProposer)
	resp := roleCall(t, srv, "pro", `{"name":"promote_skill_candidate","arguments":{"id":"whatever"}}`)
	assertScopeDenial(t, resp, WikiskillRoleProposer, ScopeSkillsPromote)
	if !strings.Contains(resp.Content[0].Text, ScopeAuditWrite) {
		t.Errorf("promote denial must also name the audit scope, got: %s", resp.Content[0].Text)
	}
	resp2 := roleCall(t, srv, "pro", `{"name":"edit_agent_skill","arguments":{"slug":"evolving-skill","content":"# hijack","loaded_version":1}}`)
	assertScopeDenial(t, resp2, WikiskillRoleProposer, ScopeSkillsPublish)
	resp3 := roleCall(t, srv, "pro", `{"name":"append_agent_memory","arguments":{"slug":"raw-observation","content_to_append":"x"}}`)
	assertScopeDenial(t, resp3, WikiskillRoleProposer, ScopeMemoryAppend)
	// A wiki write is outside the proposer remit too.
	live, err := srv.Storage.GetArticle("pattern-note")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	editJSON := `{"name":"edit_wiki_article","arguments":{"slug":"pattern-note","title":"` + live.Title + `","content":"# edited","loaded_version":` + fmt.Sprint(live.Version) + `}}`
	resp4 := roleCall(t, srv, "pro", editJSON)
	assertScopeDenial(t, resp4, WikiskillRoleProposer, ScopeWikiWrite)
}

func TestDenialsAuditedToActivityLog(t *testing.T) {
	srv := roleServer(t, WikiskillRoleProposer)
	if len(denyEvents(srv)) != 0 {
		t.Fatalf("seeding must not audit denials, got %+v", denyEvents(srv))
	}
	resp := roleCall(t, srv, "phase-harness", `{"name":"promote_skill_candidate","arguments":{"id":"nope"}}`)
	if !resp.IsError {
		t.Fatal("expected denial")
	}
	denials := denyEvents(srv)
	if len(denials) != 1 {
		t.Fatalf("expected exactly one deny event, history: %+v", srv.EventBus.GetHistory())
	}
	ev := denials[len(denials)-1]
	if ev.Source != "mcp" || ev.Tool != "promote_skill_candidate" || ev.Agent != "phase-harness" || ev.Slug != "nope" {
		t.Errorf("deny event must carry source/tool/agent/slug, got %+v", ev)
	}
	// And the denial is visible through the log read path itself.
	logResp := roleCall(t, srv, "phase-harness", `{"name":"get_recent_activity","arguments":{"action":"deny","limit":10}}`)
	if logResp.IsError {
		t.Fatalf("reading the activity log failed: %s", logResp.Content[0].Text)
	}
	if !strings.Contains(logResp.Content[0].Text, "promote_skill_candidate") {
		t.Errorf("activity log should show the denial, got: %s", logResp.Content[0].Text)
	}
}

func TestScopeGateLeavesUnknownToolsAlone(t *testing.T) {
	srv := roleServer(t, WikiskillRoleInference)
	resp := roleCall(t, srv, "inf", `{"name":"no_such_tool","arguments":{}}`)
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "Tool not found") {
		t.Errorf("unknown tools must fall through to dispatch, got: %+v", resp)
	}
	if len(denyEvents(srv)) != 0 {
		t.Errorf("unknown tools must not audit denials, got %+v", denyEvents(srv))
	}
}

func TestProposeHandlerValidationNotScopeError(t *testing.T) {
	srv := roleServer(t, WikiskillRoleProposer)
	// The gate allows this (proposer holds candidates.propose); the handler refuses
	// the empty proposal — and must not be mistaken for a scope denial.
	resp := roleCall(t, srv, "pro", `{"name":"propose_skill_candidate","arguments":{"parent_slug":"evolving-skill"}}`)
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "Error proposing skill candidate") {
		t.Errorf("expected handler validation error, got: %+v", resp)
	}
	if len(denyEvents(srv)) != 0 {
		t.Errorf("handler refusals are not scope denials, got %+v", denyEvents(srv))
	}
}

package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// The texts NexWiki ships to instruct agents: the connect-time instructions and the MCP prompts.
// They are the only places where a stale sentence does not merely misinform but makes an agent's
// next tool call *fail*.
//
// This exists because exactly that shipped in 0.12.0. Lifecycle status moved from a tag to the
// `status` field, and `ValidateStatusFreeTags` began rejecting a status word in a plan's tags —
// while the then-seeded instructions page still said "add the `completed` tag with edit_agent_plan"
// and the create-plan prompt still said to "mark the plan as completed by adding the 'completed'
// status tag". An agent following either instruction to the letter got a rejected write.
// Documentation drifting out of date is cosmetic; instructions that are actively rejected by the
// code shipping alongside them are not.
func agentFacingTexts(t *testing.T) map[string]string {
	t.Helper()
	srv := newMCPServer(t)

	texts := map[string]string{"connect-time instructions": agentInstructions()}

	// Names come from promptDefinitions. A typo here would silently skip a prompt, so the count
	// is asserted below rather than trusted.
	for _, name := range []string{"article_creation_workflow", "project_planning_workflow"} {
		args, err := json.Marshal(map[string]interface{}{
			"name": name,
			"arguments": map[string]string{
				"title": "Some Title", "project": "someproject", "topic": "Some Topic",
			},
		})
		if err != nil {
			t.Fatalf("marshal prompt args: %v", err)
		}
		result, rpcErr := srv.getPrompt(args)
		if rpcErr != nil {
			t.Fatalf("prompt %q is not retrievable (%v) — fix the name, or this guard silently "+
				"checks nothing, which is how the stale prompt shipped", name, rpcErr)
		}
		texts["prompt "+name] = renderPromptText(t, result)
	}

	// Every prompt NexWiki advertises must be covered; adding one without adding it here would
	// leave its text unguarded.
	if got, want := len(texts)-1, len(promptDefinitions()); got != want { // -1: the instructions
		t.Fatalf("guarding %d prompts but %d are advertised — add the new one to this list", got, want)
	}
	return texts
}

// renderPromptText flattens a prompts/get result into the text an agent would actually read.
func renderPromptText(t *testing.T, result interface{}) string {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal prompt result: %v", err)
	}
	return string(encoded)
}

// TestAgentInstructionsDoNotTeachStatusTags fails if a shipped instruction tells an agent to put
// lifecycle state in a tag. Status is a field; a status word in `tags` on a plan or a skill is
// rejected on save.
func TestAgentInstructionsDoNotTeachStatusTags(t *testing.T) {
	// Phrases that can only mean "put the status in the tag list". `get_status_tags` is the tool
	// name and must keep working, so the patterns deliberately require a space before "tag".
	forbidden := []string{
		"status tag",
		"completed` tag",
		"'completed' tag",
		"completed\\\" tag",
		"lifecycle tags",
	}

	for source, text := range agentFacingTexts(t) {
		lower := strings.ToLower(text)
		for _, phrase := range forbidden {
			if strings.Contains(lower, strings.ToLower(phrase)) {
				t.Errorf("%s tells an agent to use %q, but a status word in `tags` is rejected on save — "+
					"status belongs in the `status` field", source, phrase)
			}
		}
	}
}

// TestAgentInstructionsNameTheStatusField is the positive half: save_article, the one write tool,
// must actually teach the field and its vocabulary, otherwise an agent has no way to learn where
// state lives.
func TestAgentInstructionsNameTheStatusField(t *testing.T) {
	props := saveArticleTool.Schema["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
	status, ok := props["status"].(map[string]interface{})
	if !ok {
		t.Fatal("save_article declares no status argument")
	}
	desc := status["description"].(string)
	for _, want := range append(append([]string{}, PlanStatusTags...), SkillStatusTags...) {
		if !strings.Contains(desc, want) {
			t.Errorf("save_article's status description never names %q — an agent cannot learn the lifecycle from it", want)
		}
	}
}

// TestAgentTextsUseOnlyLiveStatusValues catches the other drift direction: a status value named in
// a shipped text that the validator no longer accepts.
func TestAgentTextsUseOnlyLiveStatusValues(t *testing.T) {
	for source, text := range agentFacingTexts(t) {
		for _, retired := range retiredStatusTagLabels {
			if retired == "draft" || retired == "ready" {
				continue // still live: draft is a plan and skill status, ready is a skill status
			}
			if strings.Contains(strings.ToLower(text), "`"+retired+"`") {
				t.Errorf("%s cites retired status %q as if it were usable; ValidateStatus rejects it", source, retired)
			}
		}
	}
}

// TestAgentInstructionsAreCompactAndGeneric pins the connect-time instructions to what they are
// for. Every client injects them into every session, so they stay short; and they carry only the
// universal rules, naming no document — an operator's own conventions are feedback and user
// memories, which get_wiki_overview surfaces as pinned_memories. The instructions once sent every
// agent to read a seeded rules page by slug, which any caller could rewrite and every instance had
// to maintain by hand.
func TestAgentInstructionsAreCompactAndGeneric(t *testing.T) {
	text := agentInstructions()
	if n := len([]rune(text)); n < 600 || n > 1200 {
		t.Errorf("connect-time instructions are %d characters; keep them between 600 and 1200", n)
	}
	for _, want := range []string{
		"get_wiki_overview", "pinned_memories", "search_wiki", "list_articles", "save_article",
		`"AI-Agent-Plan"`, "project_context", `"AI-Agent-Memory"`, "memory_kind", "description", "source",
		"once", "not found", "version conflict",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("connect-time instructions do not mention %q:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"read_article(slug", "guideline", "slug:"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("connect-time instructions name a specific document (%q); they must stay generic:\n%s", forbidden, text)
		}
	}
}

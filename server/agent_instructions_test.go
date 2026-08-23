package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// The texts NexWiki ships to instruct agents: the guidelines seeded into a fresh wiki, and the
// MCP prompts. They are the only places where a stale sentence does not merely misinform but
// makes an agent's next tool call *fail*.
//
// This exists because exactly that shipped in 0.12.0. Lifecycle status moved from a tag to the
// `status` field, and `ValidateStatusFreeTags` began rejecting a status word in a plan's tags —
// while the seeded guidelines still said "add the `completed` tag with edit_agent_plan" and the
// create-plan prompt still said to "mark the plan as completed by adding the 'completed' status
// tag". An agent following either instruction to the letter got a rejected write. Documentation
// drifting out of date is cosmetic; instructions that are actively rejected by the code shipping
// alongside them are not.
func agentFacingTexts(t *testing.T) map[string]string {
	t.Helper()
	srv := newMCPServer(t)

	texts := map[string]string{"seeded agent guidelines": defaultAgentGuidelines}

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
	if got, want := len(texts)-1, len(promptDefinitions()); got != want {
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

// TestAgentInstructionsNameTheStatusField is the positive half: the guidelines a fresh wiki seeds
// must actually teach the field, otherwise an agent has no way to learn where state lives.
func TestAgentInstructionsNameTheStatusField(t *testing.T) {
	guidelines := strings.ToLower(defaultAgentGuidelines)
	for _, want := range []string{"status", "get_status_tags"} {
		if !strings.Contains(guidelines, want) {
			t.Errorf("the seeded agent guidelines never mention %q — an agent cannot learn the lifecycle from them", want)
		}
	}
}

// TestSeededGuidelinesUseOnlyLiveStatusValues catches the other drift direction: a status value
// named in the seeded text that the validator no longer accepts. Every plan status the guidelines
// mention by name has to survive ValidateStatus, or the instructions cite a value that fails.
func TestSeededGuidelinesUseOnlyLiveStatusValues(t *testing.T) {
	// The retired vocabulary. If one of these appears as a plan status in the seeded guidelines,
	// an agent copying it gets a rejected write naming the replacement.
	for _, retired := range retiredStatusTagLabels {
		if retired == "draft" || retired == "ready" {
			continue // still live: draft is a plan and skill status, ready is a skill status
		}
		if strings.Contains(strings.ToLower(defaultAgentGuidelines), "`"+retired+"`") {
			t.Errorf("the seeded guidelines cite retired status %q as if it were usable; "+
				"ValidateStatus rejects it", retired)
		}
	}
}

package server

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// unregisteredToolNames are tool names NexWiki once exposed and still defines as dead handlers,
// but no longer registers. An agent told to call one gets "tool not found" — or, worse, reaches
// for save_article without the arguments the instruction assumed. Connect-time instructions, a
// since-retired seeded instructions page, and the bundled skill used to name eighteen of them.
var unregisteredToolNames = []string{
	"append_agent_memory", "append_agent_plan", "create_agent_memory", "create_agent_plan",
	"create_agent_skill", "create_wiki_article", "delete_agent_memory", "delete_wiki_article",
	"edit_agent_memory", "edit_agent_plan", "edit_agent_skill", "edit_wiki_article",
	"export_okf_bundle", "get_article_history", "get_context_overview", "get_recent_activity",
	"get_status_tags", "get_wiki_statistics", "import_okf_bundle", "list_agent_memories",
	"list_agent_plans", "list_agent_skills", "revert_article_version", "update_article_tags",
}

// toolShapedName matches an identifier that reads like a tool call: an imperative verb, then an
// underscore. Argument names that happen to share the shape (edit_summary) are allowed below by
// collecting every property name the registered schemas declare.
var toolShapedName = regexp.MustCompile(`\b(?:create|edit|append|delete|list|get|update|revert|import|export|search|read|save)_[a-z_]+\b`)

// registeredVocabulary is every name an agent-facing text may legitimately use: the registered
// tools' names and every key anywhere in their input and output schemas.
func registeredVocabulary(t *testing.T) map[string]bool {
	t.Helper()
	// delete_tag is not a tool: it is the activity log's name for the web UI's global tag
	// deletion, cited as an example value of an event's `tool` field.
	allowed := map[string]bool{"delete_tag": true}
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch x := v.(type) {
		case map[string]interface{}:
			for k, sub := range x {
				allowed[k] = true
				walk(sub)
			}
		case []interface{}:
			for _, sub := range x {
				walk(sub)
			}
		}
	}
	for _, tool := range mcpToolRegistry {
		allowed[tool.Schema["name"].(string)] = true
		for _, part := range []interface{}{tool.Schema, tool.Output} {
			raw, err := json.Marshal(part)
			if err != nil {
				t.Fatal(err)
			}
			var decoded interface{}
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			walk(decoded)
		}
	}
	return allowed
}

// shippedAgentTexts gathers everything NexWiki itself tells an agent: connect-time instructions,
// the prompts, every registered tool's schema, and the bundled skill.
func shippedAgentTexts(t *testing.T) map[string]string {
	t.Helper()
	texts := agentFacingTexts(t)
	for _, tool := range mcpToolRegistry {
		raw, err := json.Marshal(map[string]interface{}{"schema": tool.Schema, "output": tool.Output})
		if err != nil {
			t.Fatal(err)
		}
		texts["tool schema "+tool.Schema["name"].(string)] = string(raw)
	}

	skillRoot := filepath.Join("..", "agent-skill")
	err := filepath.WalkDir(skillRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		texts["skill file "+path] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("reading the bundled skill: %v", err)
	}
	return texts
}

// TestAgentTextsNameOnlyRegisteredTools is F5 of the save_article type report: the instructions
// every agent receives at connect time told it to call four tools the server does not register,
// and the since-retired seeded instructions page named eighteen. Tests built their own tool map, so CI never noticed.
func TestAgentTextsNameOnlyRegisteredTools(t *testing.T) {
	allowed := registeredVocabulary(t)
	for source, text := range shippedAgentTexts(t) {
		for _, dead := range unregisteredToolNames {
			if strings.Contains(text, dead) {
				t.Errorf("%s names %q, which is not a registered MCP tool", source, dead)
			}
		}
		var unknown []string
		for _, name := range toolShapedName.FindAllString(text, -1) {
			if !allowed[name] {
				unknown = append(unknown, name)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			t.Errorf("%s names tool-shaped identifiers that are neither a registered tool nor one of their arguments: %v",
				source, unknown)
		}
	}
}

// TestUnregisteredToolNamesAreReallyUnregistered keeps the list above honest: re-registering one
// of them is fine, but then it must come off the list.
func TestUnregisteredToolNamesAreReallyUnregistered(t *testing.T) {
	for _, name := range unregisteredToolNames {
		for _, tool := range mcpToolRegistry {
			if tool.Schema["name"] == name {
				t.Errorf("%q is registered; remove it from unregisteredToolNames", name)
			}
		}
	}
}

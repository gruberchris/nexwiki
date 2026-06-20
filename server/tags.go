package server

import "strings"

// StatusTags is the canonical list of recognized status/lifecycle tags for
// wiki articles and AI plans. They appear prioritized on the home dashboard
// and are exposed to AI agents via the get_status_tags MCP tool.
var StatusTags = []string{
	"completed", "done", "wip", "draft", "in-progress", "archived",
	"active", "todo", "pending", "review", "blocked", "ready", "inbox",
}

// Content type discriminators. Every NexWiki document carries exactly one of these
// in its OKF `type` front-matter key. `Wiki` is the only value users/regular tooling
// may set; the three reserved AI-Agent-* values are assigned solely by the agent tools
// (create_agent_memory/_plan/_skill) and may never be reassigned to a non-reserved type.
const (
	ContentTypeWiki   = "Wiki"
	ContentTypeMemory = "AI-Agent-Memory"
	ContentTypePlan   = "AI-Agent-Plan"
	ContentTypeSkill  = "AI-Agent-Skill"
)

// normalizeType canonicalizes a free-form type string to one of the four content type constants,
// defaulting to ContentTypeWiki when empty or unrecognized.
func normalizeType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "ai-agent-memory":
		return ContentTypeMemory
	case "ai-agent-plan":
		return ContentTypePlan
	case "ai-agent-skill":
		return ContentTypeSkill
	case "wiki", "":
		return ContentTypeWiki
	default:
		return ContentTypeWiki
	}
}

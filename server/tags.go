package server

// StatusTags is the canonical list of recognized status/lifecycle tags for
// wiki articles and AI plans. They appear prioritized on the home dashboard
// and are exposed to AI agents via the get_status_tags MCP tool.
var StatusTags = []string{
	"completed", "done", "wip", "draft", "in-progress", "archived",
	"active", "todo", "pending", "review", "blocked", "ready",
}

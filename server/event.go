package server

import (
	"time"
)

// MemoryScopeTagPrefix marks the scope facet of an AI Agent Memory (e.g. "memory-nexwiki").
// The document class itself is carried by the OKF `type` field, not by tags.
const MemoryScopeTagPrefix = "memory-"

// LogEvent represents an entry in the live activity log (MCP tool or REST API call).
type LogEvent struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"` // "mcp" or "api"
	Action    string    `json:"action"` // "create", "edit", "delete", "read"
	Tool      string    `json:"tool"`   // "search_wiki", "read_article", etc. (empty for REST API)
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Agent     string    `json:"agent"` // e.g. "Claude Desktop", "User"
}

// WikiUpdate represents a real-time update payload broadcasted to clients to synchronize counts and listings.
type WikiUpdate struct {
	Type           string   `json:"type"` // "article-added", "article-edited", "article-removed"
	Slug           string   `json:"slug"`
	Title          string   `json:"title"`
	Tags           []string `json:"tags"`
	Directory      string   `json:"directory"` // "wiki", "aimemories", "aiplans", "aiskills"
	TotalCount     int      `json:"total_count"`
	DirectoryCount int      `json:"directory_count"`
}

// getArticleDirectory maps a document's OKF `type` to a UI category bucket name.
func getArticleDirectory(articleType string) string {
	switch normalizeType(articleType) {
	case ContentTypeMemory:
		return "aimemories"
	case ContentTypePlan:
		return "aiplans"
	case ContentTypeSkill:
		return "aiskills"
	default:
		return "wiki"
	}
}

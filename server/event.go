package server

import (
	"strings"
	"time"
)

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

// getArticleDirectory maps a list of tags to a UI category bucket name.
func getArticleDirectory(tags []string) string {
	for _, tag := range tags {
		lowerTag := strings.ToLower(tag)
		if strings.HasPrefix(lowerTag, "aiagent-memory") {
			return "aimemories"
		}
		if lowerTag == "aiagent-plan" {
			return "aiplans"
		}
		if lowerTag == "aiagent-skill" {
			return "aiskills"
		}
	}
	return "wiki"
}

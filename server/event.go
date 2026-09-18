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
	Source    string    `json:"source"` // "mcp", "api", or "lifecycle"
	Action    string    `json:"action"` // one of activityLogActions: "create", "edit", "read", ...
	Tool      string    `json:"tool"`   // "search_wiki", "read_article", etc. (usually empty for REST API)
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Agent     string    `json:"agent"` // e.g. "Claude Desktop", "User"
	// Version is the document revision the event acted on, for writes that know it: the version
	// the save produced, or for a delete the version that was removed. It qualifies the dedup key
	// so two legitimate changes to one document inside the window both survive, while the same
	// save announced twice still collapses (#173). Zero — omitted from the wire and the durable
	// log — means the event is not tied to a revision: reads, lifecycle actions.
	Version int `json:"version,omitempty"`
}

// UpdateTypeMissed is the WikiUpdate a subscriber receives in place of a collapsed backlog after
// its buffer overflowed. It names no document; it says per-document updates were missed and the
// only honest state is the durable one, and the subscriber is expected to re-sync.
const UpdateTypeMissed = "updates-missed"

// WikiUpdate represents a real-time update payload broadcasted to clients to synchronize counts and listings.
type WikiUpdate struct {
	Type           string   `json:"type"` // "article-added", "article-edited", "article-removed", or UpdateTypeMissed
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
	case ContentTypeComputation:
		return "computations"
	default:
		return "wiki"
	}
}

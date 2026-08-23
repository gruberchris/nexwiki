package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestZeroTimestampsAreOmittedFromJSON pins the struct tags that produce the API payload.
//
// This exists because `omitempty` does not do what it looks like it does for a time.Time:
// encoding/json omits empty *basic* values, never a zero-valued struct. So every unarchived
// document serialized `"archived_at": "0001-01-01T00:00:00Z"` — a string that is truthy in
// JavaScript. The browser read it as "this document is archived" and hid every article, memory,
// plan, and skill from the dashboard and the sidebar, while the section counts kept reporting the
// real totals because they come from the unfiltered list. Shipped in 0.12.0.
//
// `omitzero` (Go 1.24+) is the tag that actually drops a zero time.
func TestZeroTimestampsAreOmittedFromJSON(t *testing.T) {
	live := Article{
		Type: ContentTypeWiki, Title: "Live Article", Slug: "live-article",
		CreatedAt: time.Now(), Timestamp: time.Now(),
	}
	encoded, err := json.Marshal(live)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	payload := string(encoded)

	for _, key := range []string{"archived_at", "status_changed_at"} {
		if strings.Contains(payload, key) {
			t.Errorf("an unarchived document serialized %q; a zero time.Time must be omitted, or "+
				"every client reads the zero string as a real value: %s", key, payload)
		}
	}
	if strings.Contains(payload, "0001-01-01") {
		t.Errorf("payload carries a Go zero timestamp: %s", payload)
	}
}

// TestRealTimestampsSurviveSerialization is the other half: omitting the zero value must not
// omit a genuine one, or archival stops being visible to any client.
func TestRealTimestampsSurviveSerialization(t *testing.T) {
	when := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	archived := Article{
		Type: ContentTypePlan, Title: "Archived Plan", Slug: "archived-plan",
		Status: StatusArchived, ArchivedAt: when, StatusChangedAt: when,
	}
	encoded, err := json.Marshal(archived)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	payload := string(encoded)
	for _, want := range []string{`"archived_at":"2026-08-23T12:00:00Z"`, `"status_changed_at":"2026-08-23T12:00:00Z"`} {
		if !strings.Contains(payload, want) {
			t.Errorf("expected %s in %s", want, payload)
		}
	}
}

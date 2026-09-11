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

	for _, key := range []string{"archived_at", "status_changed_at", "stale_after"} {
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

// TestOKFv02FieldsJSONSerialization verifies OKF v0.2 fields serialize accurately to JSON.
func TestOKFv02FieldsJSONSerialization(t *testing.T) {
	staleInstant := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	genTime := time.Date(2026, 6, 20, 22, 53, 5, 0, time.UTC)
	verTime := time.Date(2026, 6, 25, 9, 0, 0, 0, time.UTC)

	art := Article{
		Type:        ContentTypeComputation,
		Title:       "Revenue",
		Slug:        "revenue",
		StaleAfter:  staleInstant,
		IsStale:     true,
		TrustTier:   TrustTierHumanReviewed,
		Runtime:     "bigquery",
		Computation: "references/lib.sql",
		Generated: &OKFGenerated{
			By: "reference_agent/gemini-2.5-pro",
			At: genTime,
		},
		Verified: []OKFVerification{
			{By: "human:ahormati", At: verTime},
		},
		Sources: []OKFSource{
			{
				ID:       "rev-policy",
				Resource: "https://wiki.acme/finance/policy",
				Title:    "Revenue Policy",
			},
		},
		Executor: &OKFExecutor{
			Resource: "references/skills/run.md",
			Receipt:  []string{"job_id", "result"},
		},
		Attester: &OKFAttester{
			Resource: "references/attesters/rev.py",
		},
	}

	encoded, err := json.Marshal(art)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	payload := string(encoded)

	for _, want := range []string{
		`"stale_after":"2026-09-23T00:00:00Z"`,
		`"is_stale":true`,
		`"trust_tier":"human-reviewed"`,
		`"runtime":"bigquery"`,
		`"computation":"references/lib.sql"`,
		`"by":"reference_agent/gemini-2.5-pro"`,
		`"at":"2026-06-20T22:53:05Z"`,
		`"by":"human:ahormati"`,
		`"at":"2026-06-25T09:00:00Z"`,
		`"resource":"https://wiki.acme/finance/policy"`,
		`"resource":"references/skills/run.md"`,
		`"receipt":["job_id","result"]`,
		`"resource":"references/attesters/rev.py"`,
	} {
		if !strings.Contains(payload, want) {
			t.Errorf("expected %s in %s", want, payload)
		}
	}
}

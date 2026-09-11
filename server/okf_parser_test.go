package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestOKFDualEraGeneratedTimestamp verifies Rule 1:
// - timestamp is superseded by generated: { by, at }
// - On parse, if generated is absent, fall back to legacy timestamp
// - On save/serialize, write generated
// - Keep art.Timestamp synchronized with generated.at
func TestOKFDualEraGeneratedTimestamp(t *testing.T) {
	t.Run("parse generated when timestamp is absent", func(t *testing.T) {
		raw := []byte(`---
type: Wiki
title: Modern Page
slug: modern-page
generated: { by: reference_agent/gemini-2.5-pro, at: 2026-06-20T22:53:05Z }
---
# Body
`)
		art, err := parseArticleFile(raw, true)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		if art.Generated == nil {
			t.Fatal("expected art.Generated to be populated")
		}
		if art.Generated.By != "reference_agent/gemini-2.5-pro" {
			t.Errorf("expected generated.by %q, got %q", "reference_agent/gemini-2.5-pro", art.Generated.By)
		}
		wantTime := time.Date(2026, 6, 20, 22, 53, 5, 0, time.UTC)
		if !art.Generated.At.Equal(wantTime) {
			t.Errorf("expected generated.at %v, got %v", wantTime, art.Generated.At)
		}
		// Synchronized with art.Timestamp
		if !art.Timestamp.Equal(wantTime) {
			t.Errorf("expected art.Timestamp synchronized with generated.at (%v), got %v", wantTime, art.Timestamp)
		}
	})

	t.Run("parse legacy timestamp fallback when generated is absent", func(t *testing.T) {
		raw := []byte(`---
type: Wiki
title: Legacy Page
slug: legacy-page
timestamp: 2025-01-02T03:04:05Z
---
# Legacy Body
`)
		art, err := parseArticleFile(raw, true)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		wantTime := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
		if !art.Timestamp.Equal(wantTime) {
			t.Errorf("expected art.Timestamp %v, got %v", wantTime, art.Timestamp)
		}
		if art.Generated == nil {
			t.Fatal("expected art.Generated to be initialized from legacy timestamp")
		}
		if !art.Generated.At.Equal(wantTime) {
			t.Errorf("expected art.Generated.At %v, got %v", wantTime, art.Generated.At)
		}
	})

	t.Run("generated takes precedence when both are present", func(t *testing.T) {
		raw := []byte(`---
type: Wiki
title: Dual Page
slug: dual-page
generated: { by: human:chris, at: 2026-07-01T12:00:00Z }
timestamp: 2025-01-01T00:00:00Z
---
# Dual Body
`)
		art, err := parseArticleFile(raw, true)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		wantTime := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
		if !art.Timestamp.Equal(wantTime) {
			t.Errorf("expected generated.at to take precedence: want %v, got %v", wantTime, art.Timestamp)
		}
		if !art.Generated.At.Equal(wantTime) {
			t.Errorf("expected art.Generated.At %v, got %v", wantTime, art.Generated.At)
		}
	})

	t.Run("serialize emits generated and keeps timestamp in sync", func(t *testing.T) {
		now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
		art := &Article{
			Type:      ContentTypeWiki,
			Title:     "Serialized Page",
			Slug:      "serialized-page",
			Timestamp: now,
			Generated: &OKFGenerated{
				By: "reference_agent/gemini-2.5-pro",
				At: now,
			},
		}
		serialized := serializeFrontMatter(art)
		if !strings.Contains(serialized, "generated:") {
			t.Errorf("expected serialized frontmatter to contain 'generated:', got:\n%s", serialized)
		}
		if !strings.Contains(serialized, "by: reference_agent/gemini-2.5-pro") {
			t.Errorf("expected serialized frontmatter to contain 'by: reference_agent/gemini-2.5-pro', got:\n%s", serialized)
		}
		if !strings.Contains(serialized, "2026-09-10T20:00:00Z") {
			t.Errorf("expected serialized frontmatter to contain timestamp string, got:\n%s", serialized)
		}

		// Re-parse to verify round-trip
		reparsed, err := parseArticleFile([]byte(serialized+"# Content\n"), true)
		if err != nil {
			t.Fatalf("re-parse failed: %v", err)
		}
		if reparsed.Generated == nil || !reparsed.Generated.At.Equal(now) {
			t.Errorf("round-trip failed for generated.at: got %+v", reparsed.Generated)
		}
		if !reparsed.Timestamp.Equal(now) {
			t.Errorf("round-trip failed for art.Timestamp: got %v", reparsed.Timestamp)
		}
	})
}

// TestOKFDualEraSourceSources verifies Rule 2:
// - source is superseded by sources array in frontmatter
// - On parse, if sources is absent and legacy source is present, populate art.Sources with [{ Resource: legacySource }]
// - Support legacy # Citations in Markdown body
func TestOKFDualEraSourceSources(t *testing.T) {
	t.Run("parse sources array with credibility signals", func(t *testing.T) {
		raw := []byte(`---
type: Wiki
title: Sources Page
slug: sources-page
sources:
  - id: ga4-schema
    resource: https://developers.google.com/analytics/bigquery/export-schema
    title: GA4 BigQuery Export schema
    author: team:ga4-docs
    usage_count: 5000
    last_modified: 2026-05-30T00:00:00Z
usage_window: { from: 2026-06-01T00:00:00Z, to: 2026-06-30T00:00:00Z }
---
# Body
`)
		art, err := parseArticleFile(raw, true)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		if len(art.Sources) != 1 {
			t.Fatalf("expected 1 source, got %d", len(art.Sources))
		}
		src := art.Sources[0]
		if src.ID != "ga4-schema" {
			t.Errorf("expected source id %q, got %q", "ga4-schema", src.ID)
		}
		if src.Resource != "https://developers.google.com/analytics/bigquery/export-schema" {
			t.Errorf("expected source resource %q, got %q", "https://developers.google.com/analytics/bigquery/export-schema", src.Resource)
		}
		if src.Title != "GA4 BigQuery Export schema" {
			t.Errorf("expected source title %q, got %q", "GA4 BigQuery Export schema", src.Title)
		}
		if src.Author != "team:ga4-docs" {
			t.Errorf("expected source author %q, got %q", "team:ga4-docs", src.Author)
		}
		if src.UsageCount == nil || *src.UsageCount != 5000 {
			t.Errorf("expected usage_count 5000, got %v", src.UsageCount)
		}
		if src.LastModified == nil || !src.LastModified.Equal(time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("expected last_modified 2026-05-30, got %v", src.LastModified)
		}
		if art.UsageWindow == nil || !art.UsageWindow.From.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("expected usage_window.from 2026-06-01, got %+v", art.UsageWindow)
		}
		// art.Source legacy compatibility field is synchronized
		if art.Source != src.Resource {
			t.Errorf("expected art.Source %q, got %q", src.Resource, art.Source)
		}
	})

	t.Run("parse legacy flat source string into sources array", func(t *testing.T) {
		raw := []byte(`---
type: Wiki
title: Legacy Source Page
slug: legacy-source-page
source: https://example.com/legacy-source
---
# Body
`)
		art, err := parseArticleFile(raw, true)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		if len(art.Sources) != 1 {
			t.Fatalf("expected art.Sources populated with 1 entry, got %d", len(art.Sources))
		}
		if art.Sources[0].Resource != "https://example.com/legacy-source" {
			t.Errorf("expected source resource %q, got %q", "https://example.com/legacy-source", art.Sources[0].Resource)
		}
		if art.Source != "https://example.com/legacy-source" {
			t.Errorf("expected art.Source %q, got %q", "https://example.com/legacy-source", art.Source)
		}
	})

	t.Run("parse legacy body # Citations when frontmatter sources absent", func(t *testing.T) {
		raw := []byte(`---
type: Wiki
title: Body Citations Page
slug: body-citations-page
---
# Definition
Here is some definition.

# Citations
- https://wiki.acme/finance/fpa-handbook
- https://wiki.acme/finance/revenue-recognition
`)
		art, err := parseArticleFile(raw, true)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		if len(art.Sources) != 2 {
			t.Fatalf("expected 2 sources extracted from citations body, got %d", len(art.Sources))
		}
		if art.Sources[0].Resource != "https://wiki.acme/finance/fpa-handbook" {
			t.Errorf("expected source 0 resource %q, got %q", "https://wiki.acme/finance/fpa-handbook", art.Sources[0].Resource)
		}
		if art.Sources[1].Resource != "https://wiki.acme/finance/revenue-recognition" {
			t.Errorf("expected source 1 resource %q, got %q", "https://wiki.acme/finance/revenue-recognition", art.Sources[1].Resource)
		}
		if art.Source != "https://wiki.acme/finance/fpa-handbook" {
			t.Errorf("expected art.Source set to first citation, got %q", art.Source)
		}
	})

	t.Run("serialize emits sources array", func(t *testing.T) {
		count := 100
		art := &Article{
			Type:  ContentTypeWiki,
			Title: "Sources Serialize",
			Slug:  "sources-serialize",
			Sources: []OKFSource{
				{
					ID:         "source-1",
					Resource:   "https://example.com/doc",
					Title:      "Example Doc",
					UsageCount: &count,
				},
			},
		}
		serialized := serializeFrontMatter(art)
		if !strings.Contains(serialized, "sources:") {
			t.Errorf("expected 'sources:' in frontmatter, got:\n%s", serialized)
		}
		if !strings.Contains(serialized, "resource: https://example.com/doc") {
			t.Errorf("expected source resource in frontmatter, got:\n%s", serialized)
		}
		if !strings.Contains(serialized, "usage_count: 100") {
			t.Errorf("expected usage_count in frontmatter, got:\n%s", serialized)
		}
	})
}

// TestOKFVerifiedBareMappingAndList verifies Rule 3:
// - Spec §5.2 and §11 requires consumers to treat a bare mapping { by, at } as a one-element list.
// - Support unmarshaling both [{ by, at }] and { by, at }.
func TestOKFVerifiedBareMappingAndList(t *testing.T) {
	t.Run("bare mapping unmarshaled as one-element list", func(t *testing.T) {
		raw := []byte(`---
type: Wiki
title: Bare Verified Page
slug: bare-verified-page
verified: { by: human:ahormati, at: 2026-06-25T09:00:00Z }
---
# Body
`)
		art, err := parseArticleFile(raw, true)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		if len(art.Verified) != 1 {
			t.Fatalf("expected 1 verification from bare mapping, got %d", len(art.Verified))
		}
		if art.Verified[0].By != "human:ahormati" {
			t.Errorf("expected verified.by %q, got %q", "human:ahormati", art.Verified[0].By)
		}
		wantTime := time.Date(2026, 6, 25, 9, 0, 0, 0, time.UTC)
		if !art.Verified[0].At.Equal(wantTime) {
			t.Errorf("expected verified.at %v, got %v", wantTime, art.Verified[0].At)
		}
	})

	t.Run("sequence of verifiers unmarshaled cleanly", func(t *testing.T) {
		raw := []byte(`---
type: Wiki
title: List Verified Page
slug: list-verified-page
verified:
  - { by: process:finance-nightly, at: 2026-06-26T02:00:00Z }
  - { by: human:reviewer, at: 2026-06-27T10:00:00Z }
---
# Body
`)
		art, err := parseArticleFile(raw, true)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		if len(art.Verified) != 2 {
			t.Fatalf("expected 2 verifications, got %d", len(art.Verified))
		}
		if art.Verified[0].By != "process:finance-nightly" {
			t.Errorf("expected first verifier %q, got %q", "process:finance-nightly", art.Verified[0].By)
		}
		if art.Verified[1].By != "human:reviewer" {
			t.Errorf("expected second verifier %q, got %q", "human:reviewer", art.Verified[1].By)
		}
	})

	t.Run("JSON unmarshaling handles bare mapping and array", func(t *testing.T) {
		bareJSON := []byte(`{"by":"human:alice","at":"2026-06-25T09:00:00Z"}`)
		var list1 OKFVerificationList
		if err := json.Unmarshal(bareJSON, &list1); err != nil {
			t.Fatalf("unmarshal bare json failed: %v", err)
		}
		if len(list1) != 1 || list1[0].By != "human:alice" {
			t.Errorf("unexpected bare json unmarshal result: %+v", list1)
		}

		arrayJSON := []byte(`[{"by":"human:bob","at":"2026-06-25T09:00:00Z"}]`)
		var list2 OKFVerificationList
		if err := json.Unmarshal(arrayJSON, &list2); err != nil {
			t.Fatalf("unmarshal array json failed: %v", err)
		}
		if len(list2) != 1 || list2[0].By != "human:bob" {
			t.Errorf("unexpected array json unmarshal result: %+v", list2)
		}
	})
}

// TestOKFTrustTierDerivation verifies Rule 4 (§5.3):
// - No verified entries -> unverified
// - Only non-human verifiers -> machine-confirmed
// - At least one verifier with human: prefix -> human-reviewed
func TestOKFTrustTierDerivation(t *testing.T) {
	cases := []struct {
		name     string
		verified []OKFVerification
		wantTier string
	}{
		{
			name:     "nil verifications",
			verified: nil,
			wantTier: TrustTierUnverified,
		},
		{
			name:     "empty verifications",
			verified: []OKFVerification{},
			wantTier: TrustTierUnverified,
		},
		{
			name: "single automated process",
			verified: []OKFVerification{
				{By: "process:finance-nightly", At: time.Now()},
			},
			wantTier: TrustTierMachineConfirmed,
		},
		{
			name: "agent verifier",
			verified: []OKFVerification{
				{By: "reference_agent/gemini-2.5-pro", At: time.Now()},
			},
			wantTier: TrustTierMachineConfirmed,
		},
		{
			name: "human verifier",
			verified: []OKFVerification{
				{By: "human:ahormati", At: time.Now()},
			},
			wantTier: TrustTierHumanReviewed,
		},
		{
			name: "mixed verifiers with human",
			verified: []OKFVerification{
				{By: "process:finance-nightly", At: time.Now()},
				{By: "human:ahormati", At: time.Now()},
				{By: "reference_agent/gemini-2.5-pro", At: time.Now()},
			},
			wantTier: TrustTierHumanReviewed,
		},
		{
			name: "case-insensitive human prefix",
			verified: []OKFVerification{
				{By: "Human:LeadReviewer", At: time.Now()},
			},
			wantTier: TrustTierHumanReviewed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveTrustTier(tc.verified)
			if got != tc.wantTier {
				t.Errorf("DeriveTrustTier() = %q, want %q", got, tc.wantTier)
			}
			art := Article{Verified: tc.verified}
			if art.DeriveTrustTier() != tc.wantTier {
				t.Errorf("art.DeriveTrustTier() = %q, want %q", art.DeriveTrustTier(), tc.wantTier)
			}
		})
	}
}

// TestOKFFreshnessStaleAfter verifies Rule 5 (§5.5):
// - stale_after is an absolute ISO 8601 timestamp.
// - art.IsStale is true when now >= stale_after.
func TestOKFFreshnessStaleAfter(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	future := time.Now().Add(24 * time.Hour)

	t.Run("past stale_after marks article as stale on parse", func(t *testing.T) {
		raw := []byte(`---
type: Wiki
title: Stale Page
slug: stale-page
stale_after: ` + past.UTC().Format(time.RFC3339) + `
---
# Stale Content
`)
		art, err := parseArticleFile(raw, true)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		if !art.IsStale {
			t.Error("expected art.IsStale to be true for past stale_after")
		}
		if !IsStale(art) {
			t.Error("expected IsStale(art) to be true for past stale_after")
		}
	})

	t.Run("future stale_after marks article as fresh on parse", func(t *testing.T) {
		raw := []byte(`---
type: Wiki
title: Fresh Page
slug: fresh-page
stale_after: ` + future.UTC().Format(time.RFC3339) + `
---
# Fresh Content
`)
		art, err := parseArticleFile(raw, true)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		if art.IsStale {
			t.Error("expected art.IsStale to be false for future stale_after")
		}
		if IsStale(art) {
			t.Error("expected IsStale(art) to be false for future stale_after")
		}
	})

	t.Run("boundary checks for IsStaleAt", func(t *testing.T) {
		instant := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
		art := &Article{StaleAfter: instant}

		// Instant before stale_after -> fresh
		if IsStaleAt(art, instant.Add(-time.Millisecond)) {
			t.Error("expected fresh before stale_after")
		}
		// Exact instant -> stale (now >= stale_after)
		if !IsStaleAt(art, instant) {
			t.Error("expected stale at exact stale_after instant")
		}
		// Instant after stale_after -> stale
		if !IsStaleAt(art, instant.Add(time.Millisecond)) {
			t.Error("expected stale after stale_after")
		}
		// Zero instant -> never stale
		noStale := &Article{}
		if IsStaleAt(noStale, instant.Add(100*time.Hour)) {
			t.Error("expected zero StaleAfter to never be stale")
		}
	})
}

// TestOKFStandardStatuses verifies Rule 6 (§5.4):
// - Standardized draft, stable, deprecated.
// - Generic concepts and computations can carry these without failing validation.
func TestOKFStandardStatuses(t *testing.T) {
	for _, status := range OKFStandardStatuses {
		if !IsOKFStandardStatus(status) {
			t.Errorf("expected %q to be recognized as OKF standard status", status)
		}
	}

	// Generic Wiki concept can carry standard OKF statuses
	for _, s := range []string{"draft", "stable", "deprecated", ""} {
		if err := ValidateStatus(ContentTypeWiki, s); err != nil {
			t.Errorf("ValidateStatus(ContentTypeWiki, %q) failed unexpectedly: %v", s, err)
		}
	}

	// Attested Computation can carry standard OKF statuses
	for _, s := range []string{"draft", "stable", "deprecated", ""} {
		if err := ValidateStatus(ContentTypeComputation, s); err != nil {
			t.Errorf("ValidateStatus(ContentTypeComputation, %q) failed unexpectedly: %v", s, err)
		}
	}

	// Attested Computation rejects unrecognized status
	if err := ValidateStatus(ContentTypeComputation, "unknown-status"); err == nil {
		t.Error("expected ValidateStatus(ContentTypeComputation, 'unknown-status') to fail")
	}

	// Free tags on generic and computation concepts pass ValidateStatusFreeTags
	if err := ValidateStatusFreeTags(ContentTypeWiki, []string{"draft", "completed", "archived"}); err != nil {
		t.Errorf("ValidateStatusFreeTags(ContentTypeWiki) failed: %v", err)
	}
	if err := ValidateStatusFreeTags(ContentTypeComputation, []string{"finance", "revenue"}); err != nil {
		t.Errorf("ValidateStatusFreeTags(ContentTypeComputation) failed: %v", err)
	}
}

// TestOKFAttestedComputation verifies Rule 7 (§10):
// - ContentTypeComputation = "Attested Computation"
// - Contract fields: Runtime, Parameters, Computation, Executor, Attester
func TestOKFAttestedComputation(t *testing.T) {
	raw := []byte(`---
type: Attested Computation
title: Revenue for fiscal year
description: Recognized revenue for a fiscal year.
status: stable
runtime: bigquery
parameters:
  - { name: year, type: integer, required: true }
computation: references/computations/revenue.sql
executor:
  resource: references/skills/run-on-bq.md
  receipt: [job_id, executed_sql, result]
attester:
  resource: references/attesters/revenue.py
generated: { by: reference_agent/gemini-2.5-pro, at: 2026-06-20T22:53:05Z }
verified: { by: human:ahormati, at: 2026-06-25T09:00:00Z }
stale_after: 2026-09-23T00:00:00Z
sources:
  - id: rev-policy
    resource: https://wiki.acme/finance/revenue-recognition
    title: Revenue recognition policy
---
# Computation
SELECT 1;
`)

	art, err := parseArticleFile(raw, true)
	if err != nil {
		t.Fatalf("parse Attested Computation failed: %v", err)
	}

	if art.Type != ContentTypeComputation {
		t.Errorf("expected type %q, got %q", ContentTypeComputation, art.Type)
	}
	if art.Runtime != "bigquery" {
		t.Errorf("expected runtime %q, got %q", "bigquery", art.Runtime)
	}
	if len(art.Parameters) != 1 {
		t.Fatalf("expected 1 parameter, got %d", len(art.Parameters))
	}
	if art.Parameters[0].Name != "year" || art.Parameters[0].Type != "integer" || !art.Parameters[0].Required {
		t.Errorf("unexpected parameter: %+v", art.Parameters[0])
	}
	if art.Computation != "references/computations/revenue.sql" {
		t.Errorf("expected computation path %q, got %q", "references/computations/revenue.sql", art.Computation)
	}
	if art.Executor == nil || art.Executor.Resource != "references/skills/run-on-bq.md" {
		t.Errorf("unexpected executor: %+v", art.Executor)
	}
	if len(art.Executor.Receipt) != 3 || art.Executor.Receipt[0] != "job_id" {
		t.Errorf("unexpected receipt contract: %+v", art.Executor.Receipt)
	}
	if art.Attester == nil || art.Attester.Resource != "references/attesters/revenue.py" {
		t.Errorf("unexpected attester: %+v", art.Attester)
	}
	if art.TrustTier != TrustTierHumanReviewed {
		t.Errorf("expected trust tier %q, got %q", TrustTierHumanReviewed, art.TrustTier)
	}

	// Round-trip serialization
	serialized := serializeFrontMatter(art)
	if !strings.Contains(serialized, "type: Attested Computation") {
		t.Errorf("expected 'type: Attested Computation' in:\n%s", serialized)
	}
	if !strings.Contains(serialized, "runtime: bigquery") {
		t.Errorf("expected 'runtime: bigquery' in:\n%s", serialized)
	}
	if !strings.Contains(serialized, "references/attesters/revenue.py") {
		t.Errorf("expected attester resource in:\n%s", serialized)
	}

	reparsed, err := parseArticleFile([]byte(serialized+art.Content), true)
	if err != nil {
		t.Fatalf("re-parse failed: %v", err)
	}
	if reparsed.Runtime != "bigquery" || reparsed.Computation != "references/computations/revenue.sql" {
		t.Errorf("round-trip Attested Computation mismatch: %+v", reparsed)
	}
}

// TestOKFStorageRoundTripAndRevert verifies storage persistence, edits, and reverts with OKF v0.2 fields.
func TestOKFStorageRoundTripAndRevert(t *testing.T) {
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	staleInstant := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	verified := []OKFVerification{
		{By: "human:ahormati", At: time.Date(2026, 6, 25, 9, 0, 0, 0, time.UTC)},
	}
	sources := []OKFSource{
		{
			ID:       "source-one",
			Resource: "https://example.com/one",
			Title:    "Source One",
		},
	}

	// Save article with overrides
	art, err := storage.SaveArticleWithOverrides(
		"",
		"Verified Doc",
		"# Content v1",
		"Summary v1",
		"",
		"https://example.com/canonical",
		"v1",
		[]string{"test"},
		ContentTypeWiki,
		ArticleOverrides{
			Sources:    &sources,
			StaleAfter: &staleInstant,
			Verified:   &verified,
		},
	)
	if err != nil {
		t.Fatalf("SaveArticleWithOverrides failed: %v", err)
	}
	if art.TrustTier != TrustTierHumanReviewed {
		t.Errorf("expected trust tier human-reviewed, got %q", art.TrustTier)
	}
	if len(art.Sources) != 1 || art.Sources[0].ID != "source-one" {
		t.Errorf("unexpected sources: %+v", art.Sources)
	}
	if art.Source != "https://example.com/one" {
		t.Errorf("expected art.Source %q, got %q", "https://example.com/one", art.Source)
	}

	// Load back from storage
	loaded, err := storage.GetArticle("verified-doc")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if loaded.TrustTier != TrustTierHumanReviewed {
		t.Errorf("expected loaded trust tier human-reviewed, got %q", loaded.TrustTier)
	}
	if len(loaded.Sources) != 1 || loaded.Sources[0].ID != "source-one" {
		t.Errorf("expected loaded sources preserved, got %+v", loaded.Sources)
	}
	if !loaded.StaleAfter.Equal(staleInstant) {
		t.Errorf("expected loaded StaleAfter preserved, got %v", loaded.StaleAfter)
	}

	// Edit article via ApplyArticleEdit
	newSummary := "Summary v2"
	edited, err := storage.ApplyArticleEdit("verified-doc", ArticleEdit{
		Title:         "Verified Doc",
		Content:       "# Content v2",
		Description:   &newSummary,
		LoadedVersion: 1,
	})
	if err != nil {
		t.Fatalf("ApplyArticleEdit failed: %v", err)
	}
	if edited.Version != 2 {
		t.Errorf("expected version 2, got %d", edited.Version)
	}
	if edited.TrustTier != TrustTierHumanReviewed {
		t.Errorf("expected trust tier preserved across edit, got %q", edited.TrustTier)
	}
	if len(edited.Sources) != 1 {
		t.Errorf("expected sources preserved across edit, got %+v", edited.Sources)
	}

	// Revert back to v1
	reverted, err := storage.RevertArticle("verified-doc", 1)
	if err != nil {
		t.Fatalf("RevertArticle failed: %v", err)
	}
	if reverted.Version != 3 {
		t.Errorf("expected revert to increment version to 3, got %d", reverted.Version)
	}
	if reverted.Content != "# Content v1" {
		t.Errorf("expected content restored to v1, got %q", reverted.Content)
	}
	if reverted.TrustTier != TrustTierHumanReviewed {
		t.Errorf("expected trust tier restored on revert, got %q", reverted.TrustTier)
	}
	if len(reverted.Sources) != 1 || reverted.Sources[0].ID != "source-one" {
		t.Errorf("expected sources restored on revert, got %+v", reverted.Sources)
	}
}

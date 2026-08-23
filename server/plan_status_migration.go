package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// planStatusMigrationMarker is the version marker for the one-time plan status sweep. Its
// presence in the data directory means the migration already ran; it must never re-run on boot,
// because it stamps status_changed_at with "now" and a re-run would restart every plan's
// lifecycle clock.
const planStatusMigrationMarker = ".plan-status-migration-v1"

// MigratePlanStatuses performs the one-time sweep that brings a pre-lifecycle wiki onto the
// closed plan status vocabulary:
//
//   - every AI-Agent-Plan has its legacy status synonyms remapped (wip → implementing, done →
//     completed, todo/ready → draft, …) and is collapsed to exactly one status, defaulting to
//     draft when it carried none;
//   - status_changed_at is backfilled to the migration date, NOT the article timestamp — most of
//     a mature wiki's plans are completed and months old, and backfilling from the timestamp
//     against a 90-day archive window would auto-archive the majority of the corpus within days
//     of shipping;
//   - non-plan documents are stripped of plan-exclusive statuses ("archived" excepted — it stays
//     wiki-wide).
//
// The sweep runs exactly once, gated by a marker file, and each change is logged to stderr.
func (s *Storage) MigratePlanStatuses() error {
	markerPath := filepath.Join(s.DataDir, planStatusMigrationMarker)
	if _, err := os.Stat(markerPath); err == nil {
		return nil // already ran
	}

	metas, err := s.ListArticles()
	if err != nil {
		return fmt.Errorf("plan status migration: listing failed: %w", err)
	}

	// Decide from the cached metadata pass (tags, type, and status_changed_at are all in the
	// front matter); a document's body is only read when it actually needs the corrective write.
	// A full-body scan here regressed boot time on large corpora.
	migrated := 0
	for _, meta := range metas {
		var newTags []string
		var summary string

		switch {
		case meta.Type == ContentTypePlan:
			newTags = normalizeLegacyPlanStatusTags(meta.Tags)
			tagsChanged := !equalTagSets(meta.Tags, newTags)
			// A plan whose tags are already valid still needs one write to backfill its
			// status_changed_at clock (saveArticleLocked stamps a zero value with "now").
			if !tagsChanged && !meta.StatusChangedAt.IsZero() {
				continue
			}
			summary = "Plan status lifecycle migration: backfilled status_changed_at"
			if tagsChanged {
				summary = fmt.Sprintf("Plan status lifecycle migration: %s → %s, status_changed_at backfilled",
					strings.Join(meta.Tags, ","), strings.Join(newTags, ","))
			}

		default:
			// Non-plan sweep: plan-exclusive statuses may no longer appear on other types.
			var dropped []string
			for _, t := range meta.Tags {
				if planExclusiveStatus(t) {
					dropped = append(dropped, t)
				} else {
					newTags = append(newTags, t)
				}
			}
			if len(dropped) == 0 {
				continue
			}
			summary = fmt.Sprintf("Plan status lifecycle migration: removed plan-exclusive status tag(s) %s from a %s document",
				strings.Join(dropped, ","), meta.Type)
		}

		art, err := s.GetArticle(meta.Slug)
		if err != nil {
			continue
		}
		if _, err := s.SaveArticle(art.Slug, art.Title, art.Content, art.Description, art.Source, art.Resource, summary, newTags, art.Type); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: plan status migration failed for '%s': %v\n", art.Slug, err)
			continue
		}
		_, _ = fmt.Fprintf(os.Stderr, "Plan status migration: '%s' %s\n", art.Slug, summary)
		migrated++
	}

	if err := os.WriteFile(markerPath, []byte("plan status migration v1 completed\n"), 0644); err != nil {
		return fmt.Errorf("plan status migration ran but the marker could not be written (it would re-run on next boot): %w", err)
	}
	if migrated > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "Plan status migration: complete, %d document(s) updated\n", migrated)
	}
	return nil
}

// equalTagSets compares two tag lists order-insensitively and case-insensitively.
func equalTagSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]int, len(a))
	for _, t := range a {
		set[strings.ToLower(strings.TrimSpace(t))]++
	}
	for _, t := range b {
		set[strings.ToLower(strings.TrimSpace(t))]--
	}
	for _, n := range set {
		if n != 0 {
			return false
		}
	}
	return true
}

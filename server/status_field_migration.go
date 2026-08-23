package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// statusFieldMigrationMarker is the version marker for the one-time sweep that moved lifecycle
// status out of tags and into the `status` front-matter field. Its presence in the data directory
// means the migration already ran; it must never re-run on boot, because it stamps
// status_changed_at with "now" and a re-run would restart every plan's lifecycle clock.
const statusFieldMigrationMarker = ".status-field-migration-v1"

// MigrateStatusToField performs the one-time sweep that lifts lifecycle status out of the tag list
// and into the `status` field:
//
//   - AI-Agent-Plan: legacy words are remapped onto the closed vocabulary (wip → implementing,
//     done → completed, todo/ready → draft, …), several statuses collapse to the one that is most
//     true, a plan with none becomes draft, and status_changed_at is backfilled to the migration
//     date — NOT the article timestamp, since most of a mature wiki's plans are completed and
//     months old, and backfilling from the timestamp against a 90-day window would auto-archive
//     the majority of the corpus within days of shipping.
//   - AI-Agent-Skill: the same remapping onto draft/ready/archived. A skill with no status keeps
//     none.
//   - Wiki articles and agent memories: a recognized status word moves from tags into the field
//     verbatim — no vocabulary is imposed on them, and any other tag is left exactly as it is.
//     `archived` is the deliberate exception: on those types it is not a label but a *mechanism*
//     (it stamps archived_at and hides the document from search), so it stays a tag.
//
// The sweep runs exactly once, gated by a marker file, and every change is logged to stderr.
func (s *Storage) MigrateStatusToField() error {
	markerPath := filepath.Join(s.DataDir, statusFieldMigrationMarker)
	if _, err := os.Stat(markerPath); err == nil {
		return nil // already ran
	}

	metas, err := s.ListArticles()
	if err != nil {
		return fmt.Errorf("status field migration: listing failed: %w", err)
	}

	// Decide from the cached metadata pass (tags, type, status, and status_changed_at all live in
	// the front matter); a document's body is only read when it actually needs the corrective
	// write. A full-body scan here regressed boot time on large corpora.
	migrated := 0
	for _, meta := range metas {
		status, remainingTags := ExtractLegacyStatus(meta.Type, meta.Tags)
		if meta.Status != "" {
			status = meta.Status // already migrated or authored with the field
		}
		tagsChanged := len(remainingTags) != len(meta.Tags)
		statusChanged := status != meta.Status

		// A plan whose status is already correct still needs one write to backfill its
		// status_changed_at clock (saveArticleLocked stamps a zero value with "now"). Other types
		// have no clock, so an unchanged document needs no write at all.
		needsClock := meta.Type == ContentTypePlan && meta.StatusChangedAt.IsZero()
		if !tagsChanged && !statusChanged && !needsClock {
			continue
		}

		summary := "Status field migration: backfilled status_changed_at"
		if tagsChanged || statusChanged {
			summary = fmt.Sprintf("Status field migration: status '%s' moved out of tags [%s]",
				status, strings.Join(meta.Tags, ","))
		}

		art, err := s.GetArticle(meta.Slug)
		if err != nil {
			continue
		}
		if _, err := s.SaveArticleWithStatus(art.Slug, art.Title, art.Content, art.Description, art.Source, art.Resource,
			summary, remainingTags, art.Type, &status); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: status field migration failed for '%s': %v\n", art.Slug, err)
			continue
		}
		_, _ = fmt.Fprintf(os.Stderr, "Status field migration: '%s' %s\n", art.Slug, summary)
		migrated++
	}

	if err := os.WriteFile(markerPath, []byte("status field migration v1 completed\n"), 0644); err != nil {
		return fmt.Errorf("status field migration ran but the marker could not be written (it would re-run on next boot): %w", err)
	}
	if migrated > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "Status field migration: complete, %d document(s) updated\n", migrated)
	}
	return nil
}

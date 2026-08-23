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
//   - Wiki articles and agent memories have no lifecycle status, so a retired status *tag* is
//     simply removed — there is nowhere to move it to and nothing to replace it with. Every other
//     tag survives untouched, `archived` and `inbox` included: neither describes a document's
//     state (one is the archival mechanism, the other marks a raw capture awaiting compilation).
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

		// Carrying a status tag is the *only* trigger. A plan that never had one is deliberately
		// left alone: rewriting it costs a file write, a gzip history entry, and a reindex per
		// document — measured at 201 writes and ~1s of boot on a 2,000-document corpus, a
		// regression every deployment would pay to fix a handful of documents. Such a plan is
		// defaulted to draft the first time anything writes it, and the lifecycle worker backfills
		// it off the boot path on its first sweep.
		if len(remainingTags) == len(meta.Tags) {
			continue
		}

		summary := fmt.Sprintf("Status field migration: removed retired status tag(s) from [%s]",
			strings.Join(meta.Tags, ","))
		if status != "" {
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

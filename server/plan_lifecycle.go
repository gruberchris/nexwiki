package server

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// The plan lifecycle worker drives the automatic tail of the plan state machine:
//
//	completed / superseded --(NEXWIKI_PLAN_ARCHIVE_AFTER_DAYS)--> archived
//	archived --(NEXWIKI_PLAN_DELETE_AFTER_DAYS)--> permanently deleted
//
// `parked` and `evergreen` are timer-exempt by design — that exemption is their purpose.
// Timers run off StatusChangedAt, never the article Timestamp: fixing a typo in a completed plan
// must not restart its archive clock. A plan without StatusChangedAt is "not yet eligible".
//
// The deletion stage has no human in the loop, so it carries three guards: a dry-run mode that
// only logs, a stderr + activity-log line naming every plan it touches, and a backlink guard that
// refuses to delete any plan other documents still link to — silently deleting a referenced
// document is exactly the class of unattended damage this worker must never cause.

// PlanLifecycleConfig holds the worker's timers. A zero ArchiveAfterDays or DeleteAfterDays
// disables that stage.
type PlanLifecycleConfig struct {
	IntervalDays     int
	ArchiveAfterDays int
	DeleteAfterDays  int
	DryRun           bool
}

// Environment variables configuring the worker (all NEXWIKI_-prefixed per the governance rule).
const (
	envPlanLifecycleInterval = "NEXWIKI_PLAN_LIFECYCLE_INTERVAL_DAYS"
	envPlanArchiveAfter      = "NEXWIKI_PLAN_ARCHIVE_AFTER_DAYS"
	envPlanDeleteAfter       = "NEXWIKI_PLAN_DELETE_AFTER_DAYS"
	envPlanLifecycleDryRun   = "NEXWIKI_PLAN_LIFECYCLE_DRY_RUN"
)

// LoadPlanLifecycleConfig reads the worker configuration from the environment,
// applying the documented defaults (interval 1 day, archive after 90, delete after 365).
func LoadPlanLifecycleConfig() PlanLifecycleConfig {
	readDays := func(name string, def int) int {
		v := os.Getenv(name)
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: invalid %s=%q; using default %d\n", name, v, def)
			return def
		}
		return n
	}
	cfg := PlanLifecycleConfig{
		IntervalDays:     readDays(envPlanLifecycleInterval, 1),
		ArchiveAfterDays: readDays(envPlanArchiveAfter, 90),
		DeleteAfterDays:  readDays(envPlanDeleteAfter, 365),
		DryRun:           os.Getenv(envPlanLifecycleDryRun) == "true" || os.Getenv(envPlanLifecycleDryRun) == "1",
	}
	if cfg.IntervalDays < 1 {
		cfg.IntervalDays = 1
	}
	return cfg
}

// PlanLifecycleWorker sweeps AI-Agent-Plan documents and applies due transitions.
type PlanLifecycleWorker struct {
	Storage *Storage
	Bus     *EventBus
	Cfg     PlanLifecycleConfig

	// Now is the worker's clock, injectable so tests exercise 90-day timers without waiting
	// 90 days. Nil means time.Now.
	Now func() time.Time

	// Log receives the worker's report lines, injectable so tests can check what a sweep said
	// about a plan it left alone. Nil means os.Stderr.
	Log io.Writer
}

func (w *PlanLifecycleWorker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *PlanLifecycleWorker) logf(format string, args ...interface{}) {
	out := w.Log
	if out == nil {
		out = os.Stderr
	}
	_, _ = fmt.Fprintf(out, format, args...)
}

// Run sweeps once immediately, then on the configured interval, until ctx is canceled.
// It sweeps at startup because a daily ticker alone means a server restarted every morning
// never fires.
//
// Cancellation stops a sweep in progress between plans, never inside a save, and Run returns once
// it has: shutdown waits for that return before closing storage.
func (w *PlanLifecycleWorker) Run(ctx context.Context) {
	w.logf(
		"Plan lifecycle worker: sweeping every %dd (archive completed/superseded after %dd, delete archived after %dd, dry-run=%t)\n",
		w.Cfg.IntervalDays, w.Cfg.ArchiveAfterDays, w.Cfg.DeleteAfterDays, w.Cfg.DryRun)

	w.sweep(ctx)

	ticker := time.NewTicker(time.Duration(w.Cfg.IntervalDays) * 24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.logf("Plan lifecycle worker: stopped\n")
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

// Sweep applies every due transition once. Each write takes the storage lock per article rather
// than holding it across the whole sweep, so an agent mid-edit is never blocked behind a scan.
func (w *PlanLifecycleWorker) Sweep() {
	w.sweep(context.Background())
}

// sweep is Sweep, stopping before the next plan once ctx is canceled. A sweep over a large wiki can
// outlast the shutdown deadline, and each plan's transition is complete on its own.
func (w *PlanLifecycleWorker) sweep(ctx context.Context) {
	metas, err := w.Storage.ListArticles()
	if err != nil {
		w.logf("Plan lifecycle worker: listing failed: %v\n", err)
		return
	}
	now := w.now()

	for _, meta := range metas {
		if ctx.Err() != nil {
			w.logf("Plan lifecycle worker: canceled mid-sweep; the next sweep picks up the plans it did not reach\n")
			return
		}
		if meta.Type != ContentTypePlan {
			continue
		}
		art, err := w.Storage.GetArticle(meta.Slug)
		if err != nil {
			continue
		}
		// A plan written before the status field existed gets one here. The migration deliberately
		// leaves these alone so first boot stays fast; this sweep runs in the background after the
		// server is already serving, which is the right place to pay for it.
		if art.Status == "" {
			w.backfillStatus(art)
			continue
		}

		// Absent means "not yet eligible", never "infinitely old" — a parsing gap on a legacy
		// plan must not be able to trigger an archive, let alone a deletion.
		if art.StatusChangedAt.IsZero() {
			continue
		}
		age := now.Sub(art.StatusChangedAt)

		switch art.Status {
		case "completed", "superseded":
			if w.Cfg.ArchiveAfterDays > 0 && age > daysToDuration(w.Cfg.ArchiveAfterDays) {
				w.archivePlan(art, art.Status)
			}
		case StatusArchived:
			if w.Cfg.DeleteAfterDays > 0 && age > daysToDuration(w.Cfg.DeleteAfterDays) {
				w.deletePlan(art)
			}
		}
		// draft, implementing, blocked, parked, evergreen: never auto-transition.
	}
}

// backfillStatus gives a pre-field plan the default status, so it stops being invisible to the
// dashboard's open-work filter and to the lifecycle timers.
func (w *PlanLifecycleWorker) backfillStatus(art *Article) {
	if w.Cfg.DryRun {
		w.logf("Plan lifecycle worker (dry-run): would set plan '%s' to '%s' (no status on disk)\n",
			art.Slug, DefaultPlanStatus)
		return
	}
	updated, err := w.Storage.SetStatus(art.Slug, DefaultPlanStatus, 0, "Backfilled the default status: this plan predates the status field")
	if err != nil {
		w.logf("Plan lifecycle worker: failed to backfill status for '%s': %v\n", art.Slug, err)
		return
	}
	w.logf("Plan lifecycle worker: set plan '%s' to '%s' (no status on disk)\n", art.Slug, DefaultPlanStatus)
	w.publish("edit", updated.Slug, updated.Title, updated, "article-edited")
}

func daysToDuration(days int) time.Duration {
	return time.Duration(days) * 24 * time.Hour
}

// archivePlan moves a finished plan to archived. The storage layer sets archived_at and restamps
// status_changed_at as part of the same save.
func (w *PlanLifecycleWorker) archivePlan(art *Article, fromStatus string) {
	if w.Cfg.DryRun {
		w.logf("Plan lifecycle worker (dry-run): would archive plan '%s' (%s for %dd)\n",
			art.Slug, fromStatus, w.Cfg.ArchiveAfterDays)
		return
	}
	summary := fmt.Sprintf("Auto-archived by the plan lifecycle worker: %s for more than %d days", fromStatus, w.Cfg.ArchiveAfterDays)
	updated, err := w.Storage.SetStatus(art.Slug, StatusArchived, 0, summary)
	if err != nil {
		w.logf("Plan lifecycle worker: failed to archive plan '%s': %v\n", art.Slug, err)
		return
	}
	w.logf("Plan lifecycle worker: archived plan '%s' (%s → archived)\n", art.Slug, fromStatus)
	w.publish("edit", updated.Slug, updated.Title, updated, "article-edited")
}

// deletePlan permanently removes a long-archived plan — unless other documents still link to it,
// in which case it refuses and reports the plan for a human decision instead. The refusal is
// logged to the activity log so a permanently-skipped plan is visible rather than silently
// immortal.
//
// A backlink scan that skipped entries it could not read, parse, or list is refused the same way: a
// document with broken front matter or in an unreadable folder may be exactly the one that links
// here, and the scan cannot say. So is a misplaced document that does link here (one not stored as
// <slug>.md directly in the article directory): the wiki does not list it, so it is no backlink,
// but deleting the plan would still break its link. Each later sweep checks again, and deletes the
// plan only if a complete scan finds no backlinks and no misplaced document linking here.
func (w *PlanLifecycleWorker) deletePlan(art *Article) {
	scan, err := w.Storage.scanBacklinks(art.Slug)
	if err != nil {
		w.logf("Plan lifecycle worker: backlink check failed for '%s'; not deleting: %v\n", art.Slug, err)
		return
	}
	if len(scan.backlinks) > 0 {
		var linkers []string
		for _, bl := range scan.backlinks {
			linkers = append(linkers, bl.Slug)
		}
		w.refuseDelete(art, fmt.Sprintf("still linked from: %s. Remove the links or delete it by hand.", strings.Join(linkers, ", ")))
		return
	}
	if reasons := scan.incompleteReasons(); len(reasons) > 0 {
		w.refuseDelete(art, strings.Join(reasons, ", and ")+". Later sweeps check again; wiki_health lists what to fix.")
		return
	}
	if w.Cfg.DryRun {
		w.logf("Plan lifecycle worker (dry-run): would permanently delete plan '%s' (archived for more than %dd)\n",
			art.Slug, w.Cfg.DeleteAfterDays)
		return
	}
	if err := w.Storage.DeleteArticle(art.Slug); err != nil {
		w.logf("Plan lifecycle worker: failed to delete plan '%s': %v\n", art.Slug, err)
		return
	}
	// This line is the audit trail for an unrecoverable, unattended action — never drop it.
	w.logf("Plan lifecycle worker: PERMANENTLY DELETED plan '%s' (archived for more than %d days)\n",
		art.Slug, w.Cfg.DeleteAfterDays)
	w.publish("delete", art.Slug, art.Title, art, "article-removed")
}

// refuseDelete reports a plan deletePlan kept, with the reason, and records the refusal in the
// activity log. The event has no field for the reason, so the log line carries it.
func (w *PlanLifecycleWorker) refuseDelete(art *Article, reason string) {
	w.logf("Plan lifecycle worker: refusing to delete plan '%s' — %s\n", art.Slug, reason)
	if w.Bus != nil && !w.Cfg.DryRun {
		w.Bus.PublishActivity("lifecycle", "delete-refused", "plan_lifecycle", art.Slug, art.Title, "NexWiki")
	}
}

// publish emits the activity-log event and the SSE count-sync update for one transition, so open
// browsers reflect it live and the durable log records what the machine did unattended.
func (w *PlanLifecycleWorker) publish(action, slug, title string, art *Article, updateType string) {
	if w.Bus == nil {
		return
	}
	w.Bus.PublishActivity("lifecycle", action, "plan_lifecycle", slug, title, "NexWiki")
	articles, err := w.Storage.ListArticles()
	if err != nil {
		return
	}
	dirCount := 0
	for _, a := range articles {
		if a.Type == ContentTypePlan {
			dirCount++
		}
	}
	w.Bus.PublishWikiUpdate(WikiUpdate{
		Type:           updateType,
		Slug:           slug,
		Title:          title,
		Tags:           art.Tags,
		Directory:      "aiplans",
		TotalCount:     len(articles),
		DirectoryCount: dirCount,
	})
}

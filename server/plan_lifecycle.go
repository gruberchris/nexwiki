package server

import (
	"context"
	"fmt"
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
}

func (w *PlanLifecycleWorker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// Run sweeps once immediately, then on the configured interval, until ctx is canceled.
// It sweeps at startup because a daily ticker alone means a server restarted every morning
// never fires.
func (w *PlanLifecycleWorker) Run(ctx context.Context) {
	_, _ = fmt.Fprintf(os.Stderr,
		"Plan lifecycle worker: sweeping every %dd (archive completed/superseded after %dd, delete archived after %dd, dry-run=%t)\n",
		w.Cfg.IntervalDays, w.Cfg.ArchiveAfterDays, w.Cfg.DeleteAfterDays, w.Cfg.DryRun)

	w.Sweep()

	ticker := time.NewTicker(time.Duration(w.Cfg.IntervalDays) * 24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_, _ = fmt.Fprintf(os.Stderr, "Plan lifecycle worker: stopped\n")
			return
		case <-ticker.C:
			w.Sweep()
		}
	}
}

// Sweep applies every due transition once. Each write takes the storage lock per article rather
// than holding it across the whole sweep, so an agent mid-edit is never blocked behind a scan.
func (w *PlanLifecycleWorker) Sweep() {
	metas, err := w.Storage.ListArticles()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Plan lifecycle worker: listing failed: %v\n", err)
		return
	}
	now := w.now()

	for _, meta := range metas {
		if meta.Type != ContentTypePlan {
			continue
		}
		art, err := w.Storage.GetArticle(meta.Slug)
		if err != nil {
			continue
		}
		statuses := planStatusesIn(art.Tags)
		if len(statuses) != 1 {
			continue // pre-migration or hand-mangled; validation fixes it on the next real edit
		}
		status := statuses[0]

		// Absent means "not yet eligible", never "infinitely old" — a parsing gap on a legacy
		// plan must not be able to trigger an archive, let alone a deletion.
		if art.StatusChangedAt.IsZero() {
			continue
		}
		age := now.Sub(art.StatusChangedAt)

		switch status {
		case "completed", "superseded":
			if w.Cfg.ArchiveAfterDays > 0 && age > daysToDuration(w.Cfg.ArchiveAfterDays) {
				w.archivePlan(art, status)
			}
		case "archived":
			if w.Cfg.DeleteAfterDays > 0 && age > daysToDuration(w.Cfg.DeleteAfterDays) {
				w.deletePlan(art)
			}
		}
		// draft, implementing, blocked, parked, evergreen: never auto-transition.
	}
}

func daysToDuration(days int) time.Duration {
	return time.Duration(days) * 24 * time.Hour
}

// archivePlan moves a finished plan to archived, swapping its status tag. saveArticleLocked sets
// archived_at and restamps status_changed_at as part of the save.
func (w *PlanLifecycleWorker) archivePlan(art *Article, fromStatus string) {
	if w.Cfg.DryRun {
		_, _ = fmt.Fprintf(os.Stderr, "Plan lifecycle worker (dry-run): would archive plan '%s' (%s for %dd)\n",
			art.Slug, fromStatus, w.Cfg.ArchiveAfterDays)
		return
	}
	newTags := replacePlanStatus(art.Tags, "archived")
	summary := fmt.Sprintf("Auto-archived by the plan lifecycle worker: %s for more than %d days", fromStatus, w.Cfg.ArchiveAfterDays)
	updated, err := w.Storage.UpdateArticleTags(art.Slug, newTags, 0, summary)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Plan lifecycle worker: failed to archive plan '%s': %v\n", art.Slug, err)
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "Plan lifecycle worker: archived plan '%s' (%s → archived)\n", art.Slug, fromStatus)
	w.publish("edit", updated.Slug, updated.Title, updated, "article-edited")
}

// deletePlan permanently removes a long-archived plan — unless other documents still link to it,
// in which case it refuses and reports the plan for a human decision instead. The refusal is
// logged to the activity log so a permanently-skipped plan is visible rather than silently
// immortal.
func (w *PlanLifecycleWorker) deletePlan(art *Article) {
	backlinks, err := w.Storage.GetBacklinks(art.Slug)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Plan lifecycle worker: backlink check failed for '%s'; not deleting: %v\n", art.Slug, err)
		return
	}
	if len(backlinks) > 0 {
		var linkers []string
		for _, bl := range backlinks {
			linkers = append(linkers, bl.Slug)
		}
		_, _ = fmt.Fprintf(os.Stderr,
			"Plan lifecycle worker: refusing to delete plan '%s' — still linked from: %s. Remove the links or delete it by hand.\n",
			art.Slug, strings.Join(linkers, ", "))
		if w.Bus != nil && !w.Cfg.DryRun {
			w.Bus.PublishActivity("lifecycle", "delete-refused", "plan_lifecycle", art.Slug, art.Title, "NexWiki")
		}
		return
	}
	if w.Cfg.DryRun {
		_, _ = fmt.Fprintf(os.Stderr, "Plan lifecycle worker (dry-run): would permanently delete plan '%s' (archived for more than %dd)\n",
			art.Slug, w.Cfg.DeleteAfterDays)
		return
	}
	if err := w.Storage.DeleteArticle(art.Slug); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Plan lifecycle worker: failed to delete plan '%s': %v\n", art.Slug, err)
		return
	}
	// This line is the audit trail for an unrecoverable, unattended action — never drop it.
	_, _ = fmt.Fprintf(os.Stderr, "Plan lifecycle worker: PERMANENTLY DELETED plan '%s' (archived for more than %d days)\n",
		art.Slug, w.Cfg.DeleteAfterDays)
	w.publish("delete", art.Slug, art.Title, art, "article-removed")
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

// replacePlanStatus returns tags with every plan status removed and newStatus appended, keeping
// free tags (project context etc.) untouched.
func replacePlanStatus(tags []string, newStatus string) []string {
	var out []string
	for _, t := range tags {
		lower := strings.ToLower(strings.TrimSpace(t))
		isStatus := false
		for _, s := range PlanStatusTags {
			if lower == s {
				isStatus = true
				break
			}
		}
		if !isStatus {
			out = append(out, t)
		}
	}
	return append(out, newStatus)
}

// legacyPlanStatusSynonyms maps the retired free-form vocabulary onto the closed one.
var legacyPlanStatusSynonyms = map[string]string{
	"wip":         "implementing",
	"in-progress": "implementing",
	"active":      "implementing",
	"done":        "completed",
	"todo":        "draft",
	"ready":       "draft",
}

// planStatusPrecedence resolves a plan carrying several statuses to the single one that is most
// true: a terminal or deliberate state always beats an in-flight one (a plan tagged both
// "superseded" and "completed" is superseded — only the first is still true).
var planStatusPrecedence = []string{
	"superseded", "archived", "parked", "evergreen", "completed", "blocked", "implementing", "draft",
}

// normalizeLegacyPlanStatusTags maps legacy synonyms onto the closed vocabulary and collapses the
// result to exactly one plan status (defaulting to "draft" when none is present). Free tags pass
// through untouched. Used by the startup migration, OKF bundle import, and version revert — the
// three paths that can legitimately hand the write layer a pre-lifecycle tag set.
func normalizeLegacyPlanStatusTags(tags []string) []string {
	var free []string
	statusSet := make(map[string]bool)
	for _, t := range tags {
		lower := strings.ToLower(strings.TrimSpace(t))
		if mapped, ok := legacyPlanStatusSynonyms[lower]; ok {
			statusSet[mapped] = true
			continue
		}
		isStatus := false
		for _, s := range PlanStatusTags {
			if lower == s {
				isStatus = true
				break
			}
		}
		if isStatus {
			statusSet[lower] = true
		} else {
			free = append(free, t)
		}
	}

	chosen := "draft"
	for _, s := range planStatusPrecedence {
		if statusSet[s] {
			chosen = s
			break
		}
	}
	return append(free, chosen)
}

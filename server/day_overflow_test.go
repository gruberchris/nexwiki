package server

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Day counts from configuration and tool arguments become durations, and past 106751 days (about
// 292 years) the multiplication used to wrap negative. Every age exceeds a negative threshold, so a
// setting meant as "effectively never" deleted or archived everything at once (issue #176).

// hugeDayValues are day counts too long for a time.Duration, as they would be written in the
// environment.
func hugeDayValues() []string {
	return []string{"106752", "200000", strconv.Itoa(math.MaxInt)}
}

// ancientDays is an age no time.Duration can measure: time.Since saturates for it, so the cap on a
// threshold must still read as "never" there rather than as "exactly reached".
const ancientDays = 150000

func TestDaysToDuration(t *testing.T) {
	day := 24 * time.Hour
	type dayCase struct {
		days int
		want time.Duration
	}
	cases := []dayCase{
		{0, 0},
		{1, day},
		{365, 365 * day},
		{106751, 106751 * day},
		{106752, math.MaxInt64},
		{200000, math.MaxInt64},
		{math.MaxInt, math.MaxInt64},
		{-1, -day},
		{-106751, -106751 * day},
		{-106752, math.MinInt64},
		{math.MinInt, math.MinInt64},
	}
	if strconv.IntSize == 64 {
		// 2^48 days is a multiple of 2^64 nanoseconds, so an unchecked product wraps to exactly zero.
		n, _ := strconv.Atoi("281474976710656")
		cases = append(cases, dayCase{n, math.MaxInt64})
	}
	for _, tc := range cases {
		if got := daysToDuration(tc.days); got != tc.want {
			t.Errorf("daysToDuration(%d) = %v, want %v", tc.days, got, tc.want)
		}
	}
}

// seedArchivedForStartup builds a data directory holding an article archived 400 days ago and one
// archived ancientDays ago, and returns it closed, ready for a startup to clean up.
func seedArchivedForStartup(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv(envAutoDeleteArchived, "")
	seed, err := NewStorage(dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	saveArchived(t, seed, "Recent Archive", "# kept")
	saveArchived(t, seed, "Ancient Archive", "# kept")
	backdateArchived(t, seed.ArticleDir, "recent-archive", 400, time.Now())
	backdateArchived(t, seed.ArticleDir, "ancient-archive", ancientDays, time.Now().Add(-time.Minute))
	if err := seed.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	return dataDir
}

// startStorage opens dataDir as startup does, running the archived article cleanup.
func startStorage(t *testing.T, dataDir string) *Storage {
	t.Helper()
	s, err := NewStorage(dataDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCleanupArchivedHugeDelayDeletesNothing(t *testing.T) {
	for _, value := range hugeDayValues() {
		t.Run(value, func(t *testing.T) {
			dataDir := seedArchivedForStartup(t)
			t.Setenv(envAutoDeleteArchived, value)
			buf := captureLog(t)

			s := startStorage(t, dataDir)

			for _, slug := range []string{"recent-archive", "ancient-archive"} {
				if !exists(t, s, slug) {
					t.Errorf("a %s-day delay must never delete %s", value, slug)
				}
			}
			if strings.Contains(buf.String(), "Deleted archived article") {
				t.Errorf("nothing may be deleted, got %q", buf.String())
			}
		})
	}
}

// TestCleanupArchivedLargestDelayIsReal pins the other side of the cap: 106751 days fits in a
// duration, so it is an ordinary delay that a document archived longer ago has passed.
func TestCleanupArchivedLargestDelayIsReal(t *testing.T) {
	dataDir := seedArchivedForStartup(t)
	t.Setenv(envAutoDeleteArchived, "106751")
	captureLog(t)

	s := startStorage(t, dataDir)

	if !exists(t, s, "recent-archive") {
		t.Error("an article archived 400 days ago is not due after 106751 days")
	}
	if exists(t, s, "ancient-archive") {
		t.Errorf("an article archived %d days ago is due after 106751 days", ancientDays)
	}
}

// seedAgedPlans seeds a completed and an archived plan whose status changed 400 days ago, and a
// pair whose status changed ancientDays ago.
func seedAgedPlans(t *testing.T, s *Storage) {
	t.Helper()
	for _, p := range []struct {
		title, status string
		days          int
	}{
		{"Recent Completed", "completed", 400},
		{"Ancient Completed", "completed", ancientDays},
		{"Recent Archived", StatusArchived, 400},
		{"Ancient Archived", StatusArchived, ancientDays},
	} {
		art := savePlan(t, s, p.title, p.status)
		backdateStatusChange(t, s, art.Slug, p.days)
	}
}

func TestLifecycleWorkerHugeTimersKeepPlans(t *testing.T) {
	for _, value := range hugeDayValues() {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envPlanArchiveAfter, value)
			t.Setenv(envPlanDeleteAfter, value)
			cfg := LoadPlanLifecycleConfig()
			if strconv.Itoa(cfg.ArchiveAfterDays) != value || strconv.Itoa(cfg.DeleteAfterDays) != value {
				t.Fatalf("config = %+v, want both timers at %s days as set", cfg, value)
			}
			s := newLifecycleStorage(t)
			seedAgedPlans(t, s)

			w := &PlanLifecycleWorker{Storage: s, Cfg: cfg}
			out := captureWorkerLog(w)
			w.Sweep()

			for _, slug := range []string{"recent-completed", "ancient-completed"} {
				art, err := s.GetArticle(slug)
				if err != nil {
					t.Fatalf("plan %s missing: %v", slug, err)
				}
				if art.Status != "completed" {
					t.Errorf("a %s-day archive timer must never archive %s, got status %q", value, slug, art.Status)
				}
			}
			for _, slug := range []string{"recent-archived", "ancient-archived"} {
				if !exists(t, s, slug) {
					t.Errorf("a %s-day delete timer must never delete %s", value, slug)
				}
			}
			if out.String() != "" {
				t.Errorf("a sweep with nothing due must report nothing, got %q", out.String())
			}
		})
	}
}

// TestLifecycleWorkerLargestTimersAreReal pins that 106751 days is an ordinary timer, which plans
// whose status changed longer ago have passed.
func TestLifecycleWorkerLargestTimersAreReal(t *testing.T) {
	s := newLifecycleStorage(t)
	seedAgedPlans(t, s)

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 106751, DeleteAfterDays: 106751}}
	captureWorkerLog(w)
	w.Sweep()

	if art, err := s.GetArticle("recent-completed"); err != nil || art.Status != "completed" {
		t.Errorf("recent-completed is not due after 106751 days, got %+v, %v", art, err)
	}
	if art, err := s.GetArticle("ancient-completed"); err != nil || art.Status != StatusArchived {
		t.Errorf("ancient-completed is due for archiving after 106751 days, got %+v, %v", art, err)
	}
	if !exists(t, s, "recent-archived") {
		t.Error("recent-archived is not due for deletion after 106751 days")
	}
	if exists(t, s, "ancient-archived") {
		t.Error("ancient-archived is due for deletion after 106751 days")
	}
}

// TestLifecycleWorkerHugeIntervalRuns pins that an interval too long for a duration starts the
// worker. Wrapped negative or to zero it made time.NewTicker panic, taking the server down.
func TestLifecycleWorkerHugeIntervalRuns(t *testing.T) {
	values := hugeDayValues()
	if strconv.IntSize == 64 {
		values = append(values, "281474976710656") // 2^48 days, which wrapped to exactly zero
	}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envPlanLifecycleInterval, value)
			cfg := LoadPlanLifecycleConfig()
			if strconv.Itoa(cfg.IntervalDays) != value {
				t.Fatalf("interval = %d days, want %s as set", cfg.IntervalDays, value)
			}
			out := &lockedBuffer{}
			w := &PlanLifecycleWorker{Storage: newLifecycleStorage(t), Cfg: cfg, Log: out}

			// Canceled up front: Run still sweeps once and starts its ticker before it sees that.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			returned := make(chan interface{}, 1)
			go func() {
				defer func() { returned <- recover() }()
				w.Run(ctx)
			}()
			select {
			case p := <-returned:
				if p != nil {
					t.Fatalf("Run panicked with a %s-day interval: %v", value, p)
				}
			case <-time.After(testWaitLimit):
				t.Fatalf("Run did not return within %s of cancellation", testWaitLimit)
			}
			if !strings.Contains(out.String(), "Plan lifecycle worker: stopped") {
				t.Errorf("the worker returned without reporting that it stopped; log:\n%s", out.String())
			}
		})
	}
}

// TestWikiHealthHugeDayArguments pins that stale_days and cold_days of centuries find nothing. The
// largest of them used to put the cutoff in the future, reporting a plan edited today as stale and
// every memory as cold.
func TestWikiHealthHugeDayArguments(t *testing.T) {
	srv := newMCPServer(t)
	for _, title := range []string{"Fresh Plan", "Aged Plan"} {
		status := "implementing"
		if _, err := srv.Storage.SaveArticleWithStatus("", title, "# plan", "d", "", "", "seed", []string{"project"}, ContentTypePlan, &status); err != nil {
			t.Fatalf("seeding %q failed: %v", title, err)
		}
	}
	ageDocument(t, srv, "aged-plan", 400)
	if _, err := srv.Storage.SaveArticle("", "Aged Memory", "# body", "d", "src", "", "seed", []string{"memory-project"}, ContentTypeMemory); err != nil {
		t.Fatalf("seeding memory failed: %v", err)
	}
	ageDocument(t, srv, "aged-memory", 400)
	al, err := OpenActivityLog(srv.Storage.DataDir)
	if err != nil {
		t.Fatalf("OpenActivityLog failed: %v", err)
	}
	// A log reaching back 30 days, so the cold scan can run for shorter thresholds.
	if err := al.Append(LogEvent{Timestamp: time.Now().AddDate(0, 0, -30), Action: "read", Slug: "something"}); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if err := al.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// The fixture does produce findings, so the empty reports below are the thresholds at work.
	control := healthReport(t, srv, `{"stale_days":1,"cold_days":7}`)
	if slugs := findingSlugs(control.StalePlans); len(slugs) != 1 || !slugs["aged-plan"] {
		t.Fatalf("fixture: want only aged-plan stale after 1 day, got %+v", control.StalePlans)
	}
	if slugs := findingSlugs(control.ColdMemories); !control.ColdMemoryScanRan || len(slugs) != 1 || !slugs["aged-memory"] {
		t.Fatalf("fixture: want the cold scan to run and find aged-memory after 7 days, got %+v", control)
	}

	for _, value := range hugeDayValues() {
		t.Run(value, func(t *testing.T) {
			var args struct {
				StaleDays int `json:"stale_days"`
				ColdDays  int `json:"cold_days"`
			}
			args.StaleDays, _ = strconv.Atoi(value)
			args.ColdDays = args.StaleDays
			raw, err := json.Marshal(args)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}

			out := healthReport(t, srv, string(raw))

			if out.StaleDays != args.StaleDays || out.ColdDays != args.ColdDays {
				t.Errorf("thresholds reported as stale_days=%d cold_days=%d, want %s as given", out.StaleDays, out.ColdDays, value)
			}
			if out.StalePlanCount != 0 {
				t.Errorf("no plan is stale after %s days, got %+v", value, out.StalePlans)
			}
			if out.ColdMemoryScanRan || out.ColdMemoryCount != 0 {
				t.Errorf("a 30-day log cannot show a memory cold for %s days, got ran=%t %+v", value, out.ColdMemoryScanRan, out.ColdMemories)
			}
		})
	}
}

// TestLifecycleWorkerZeroTimersKeepFreshPlans pins the strict "older than" semantics at zero: a
// 0-day retention is disabled, and a plan whose status changed this instant — age exactly zero,
// the one age an "at least as old as" comparison would admit against a 0-day threshold — is
// neither archived nor deleted. Storage's own 0-day case is TestCleanupArchivedDisabled.
func TestLifecycleWorkerZeroTimersKeepFreshPlans(t *testing.T) {
	s := newLifecycleStorage(t)
	fresh := map[string]string{
		"fresh-completed": "completed",
		"fresh-archived":  StatusArchived,
	}
	for title, status := range fresh {
		art := savePlan(t, s, title, status)
		if art.StatusChangedAt.IsZero() {
			t.Fatalf("seeding %q did not stamp status_changed_at", title)
		}
	}

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{}}
	out := captureWorkerLog(w)
	w.Sweep()

	for slug, want := range fresh {
		art, err := s.GetArticle(slug)
		if err != nil {
			t.Fatalf("plan %s missing: %v", slug, err)
		}
		if art.Status != want {
			t.Errorf("a 0-day retention must leave %s at %q, got %q", slug, want, art.Status)
		}
	}
	if out.String() != "" {
		t.Errorf("a 0-day retention must report nothing, got %q", out.String())
	}
}

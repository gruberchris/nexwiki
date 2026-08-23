package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newLifecycleStorage(t *testing.T) *Storage {
	t.Helper()
	storage, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	return storage
}

// backdateStatusChange rewrites a plan's status_changed_at on disk, bypassing SaveArticle (which
// would restamp it), so timer tests can age a status without waiting for it.
func backdateStatusChange(t *testing.T, s *Storage, slug string, days int) {
	t.Helper()
	path := filepath.Join(s.ArticleDir, slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", slug, err)
	}
	art, err := parseArticleFile(raw, true)
	if err != nil {
		t.Fatalf("parse %s: %v", slug, err)
	}
	art.StatusChangedAt = time.Now().AddDate(0, 0, -days)
	// The article cache validates by mtime+size, so this on-disk rewrite is picked up directly.
	if err := os.WriteFile(path, []byte(serializeFrontMatter(art)+art.Content), 0644); err != nil {
		t.Fatalf("write %s: %v", slug, err)
	}
}

func TestValidatePlanStatus(t *testing.T) {
	cases := []struct {
		name    string
		docType string
		tags    []string
		wantErr string // empty means valid
	}{
		{"plan with one status", ContentTypePlan, []string{"nexwiki", "implementing"}, ""},
		{"plan with archived", ContentTypePlan, []string{"archived"}, ""},
		{"plan with no status", ContentTypePlan, []string{"nexwiki"}, "got none"},
		{"plan with empty tags", ContentTypePlan, nil, "got none"},
		{"plan with two statuses", ContentTypePlan, []string{"superseded", "completed"}, "got 2"},
		{"plan with general tags plus one status", ContentTypePlan, []string{"review", "draft"}, ""},
		{"wiki article with plan status", ContentTypeWiki, []string{"draft"}, "reserved for AI-Agent-Plan"},
		{"skill with plan status", ContentTypeSkill, []string{"implementing"}, "reserved for AI-Agent-Plan"},
		{"memory with plan status", ContentTypeMemory, []string{"parked"}, "reserved for AI-Agent-Plan"},
		{"wiki article with archived is exempt", ContentTypeWiki, []string{"archived"}, ""},
		{"wiki article with general status", ContentTypeWiki, []string{"wip", "review"}, ""},
		{"case-insensitive detection", ContentTypePlan, []string{"Completed", "Superseded"}, "got 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePlanStatus(tc.docType, tc.tags)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("expected valid, got: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestPlanStatusChangeStamping(t *testing.T) {
	s := newLifecycleStorage(t)

	art, err := s.SaveArticle("", "Stamp Plan", "# p", "", "", "", "", []string{"draft"}, ContentTypePlan)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if art.StatusChangedAt.IsZero() {
		t.Fatal("a new plan must get status_changed_at stamped")
	}
	firstStamp := art.StatusChangedAt

	// A content edit that keeps the status must NOT restart the clock — fixing a typo in a
	// completed plan restarting its archive timer is the exact defect this field exists to avoid.
	time.Sleep(10 * time.Millisecond)
	art2, err := s.SaveArticle(art.Slug, art.Title, "# p\n\ntypo fixed", "", "", "", "", []string{"draft"}, ContentTypePlan)
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	// Compare at second precision: the stamp round-trips through RFC3339 on disk.
	if !art2.StatusChangedAt.Equal(firstStamp.Truncate(time.Second)) {
		t.Errorf("content edit restarted the status clock: %v → %v", firstStamp, art2.StatusChangedAt)
	}

	// A status change restamps it.
	time.Sleep(1100 * time.Millisecond)
	art3, err := s.SaveArticle(art.Slug, art.Title, art2.Content, "", "", "", "", []string{"implementing"}, ContentTypePlan)
	if err != nil {
		t.Fatalf("status change failed: %v", err)
	}
	if !art3.StatusChangedAt.After(firstStamp) {
		t.Error("status change did not restamp status_changed_at")
	}
}

func TestPlanArchivedAtMirrorsStatus(t *testing.T) {
	s := newLifecycleStorage(t)

	art, _ := s.SaveArticle("", "Mirror Plan", "# p", "", "", "", "", []string{"completed"}, ContentTypePlan)
	if !art.ArchivedAt.IsZero() {
		t.Fatal("a completed plan is not archived yet")
	}

	archived, err := s.UpdateArticleTags(art.Slug, []string{"archived"}, 0, "archive")
	if err != nil {
		t.Fatalf("archive failed: %v", err)
	}
	if archived.ArchivedAt.IsZero() {
		t.Fatal("entering archived must set archived_at")
	}

	// Revival must clear archived_at — otherwise IsArchived stays true and the plan sits at
	// status implementing while remaining hidden from search and on the deletion clock.
	revived, err := s.UpdateArticleTags(art.Slug, []string{"implementing"}, 0, "revive")
	if err != nil {
		t.Fatalf("revive failed: %v", err)
	}
	if !revived.ArchivedAt.IsZero() {
		t.Errorf("revival out of archived must clear archived_at, got %v", revived.ArchivedAt)
	}
	if IsArchived(revived) {
		t.Error("a revived plan must not still count as archived")
	}
}

func TestLifecycleWorkerArchivesFinishedPlans(t *testing.T) {
	s := newLifecycleStorage(t)
	for _, tc := range []struct{ title, status string }{
		{"Old Completed", "completed"},
		{"Old Superseded", "superseded"},
		{"Old Parked", "parked"},
		{"Old Evergreen", "evergreen"},
		{"Old Draft", "draft"},
		{"Old Implementing", "implementing"},
		{"Old Blocked", "blocked"},
		{"Fresh Completed", "completed"},
	} {
		if _, err := s.SaveArticle("", tc.title, "# p", "", "", "", "", []string{tc.status}, ContentTypePlan); err != nil {
			t.Fatalf("seed %q: %v", tc.title, err)
		}
	}
	for _, slug := range []string{"old-completed", "old-superseded", "old-parked", "old-evergreen", "old-draft", "old-implementing", "old-blocked"} {
		backdateStatusChange(t, s, slug, 120)
	}

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365}}
	w.Sweep()

	wantStatus := map[string]string{
		"old-completed":    "archived",
		"old-superseded":   "archived",
		"old-parked":       "parked",    // timer-exempt by design — that exemption is its purpose
		"old-evergreen":    "evergreen", // likewise
		"old-draft":        "draft",
		"old-implementing": "implementing",
		"old-blocked":      "blocked",
		"fresh-completed":  "completed", // 90 days have not elapsed
	}
	for slug, want := range wantStatus {
		art, err := s.GetArticle(slug)
		if err != nil {
			t.Fatalf("plan %q missing after sweep: %v", slug, err)
		}
		got := planStatusesIn(art.Tags)
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s: status = %v, want %s", slug, got, want)
		}
		if want == "archived" && art.ArchivedAt.IsZero() {
			t.Errorf("%s: auto-archive must set archived_at", slug)
		}
	}
}

func TestLifecycleWorkerDeletesLongArchivedPlans(t *testing.T) {
	s := newLifecycleStorage(t)
	if _, err := s.SaveArticle("", "Ancient Archived", "# p", "", "", "", "", []string{"archived"}, ContentTypePlan); err != nil {
		t.Fatalf("seed: %v", err)
	}
	backdateStatusChange(t, s, "ancient-archived", 400)

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365}}
	w.Sweep()

	if _, err := s.GetArticle("ancient-archived"); err == nil {
		t.Error("a plan archived past the deletion window must be deleted")
	}
}

func TestLifecycleWorkerRefusesToDeleteLinkedPlans(t *testing.T) {
	s := newLifecycleStorage(t)
	if _, err := s.SaveArticle("", "Referenced Archived", "# p", "", "", "", "", []string{"archived"}, ContentTypePlan); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if _, err := s.SaveArticle("", "Pointer Page", "# doc\n\nSee [[Referenced Archived]].", "", "", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatalf("seed linker: %v", err)
	}
	backdateStatusChange(t, s, "referenced-archived", 400)

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365}}
	w.Sweep()

	if _, err := s.GetArticle("referenced-archived"); err != nil {
		t.Error("a plan other documents link to must never be auto-deleted")
	}
}

func TestLifecycleWorkerDryRunTouchesNothing(t *testing.T) {
	s := newLifecycleStorage(t)
	_, _ = s.SaveArticle("", "Dry Completed", "# p", "", "", "", "", []string{"completed"}, ContentTypePlan)
	_, _ = s.SaveArticle("", "Dry Archived", "# p", "", "", "", "", []string{"archived"}, ContentTypePlan)
	backdateStatusChange(t, s, "dry-completed", 120)
	backdateStatusChange(t, s, "dry-archived", 400)

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365, DryRun: true}}
	w.Sweep()

	art, err := s.GetArticle("dry-completed")
	if err != nil {
		t.Fatal("dry run deleted a plan")
	}
	if got := planStatusesIn(art.Tags); len(got) != 1 || got[0] != "completed" {
		t.Errorf("dry run changed a status: %v", got)
	}
	if _, err := s.GetArticle("dry-archived"); err != nil {
		t.Error("dry run deleted the archived plan")
	}
}

func TestLifecycleWorkerSkipsPlansWithoutStatusClock(t *testing.T) {
	s := newLifecycleStorage(t)
	art, _ := s.SaveArticle("", "Clockless Completed", "# p", "", "", "", "", []string{"completed"}, ContentTypePlan)

	// Strip the stamp on disk, simulating a plan written by an external tool.
	path := filepath.Join(s.ArticleDir, art.Slug+".md")
	raw, _ := os.ReadFile(path)
	parsed, _ := parseArticleFile(raw, true)
	parsed.StatusChangedAt = time.Time{}
	_ = os.WriteFile(path, []byte(serializeFrontMatter(parsed)+parsed.Content), 0644)

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365}}
	w.Sweep()

	got, err := s.GetArticle(art.Slug)
	if err != nil {
		t.Fatal("plan disappeared")
	}
	if statuses := planStatusesIn(got.Tags); statuses[0] != "completed" {
		t.Errorf("a plan without status_changed_at must be 'not yet eligible', got %v", statuses)
	}
}

func TestMigrationRemapsLegacyStatusesOnce(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStorage(dir)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}

	// Write legacy documents straight to disk — SaveArticle would reject them now.
	writeRaw := func(a *Article, body string) {
		a.Content = body
		if err := os.WriteFile(filepath.Join(s.ArticleDir, a.Slug+".md"), []byte(serializeFrontMatter(a)+body), 0644); err != nil {
			t.Fatalf("write %s: %v", a.Slug, err)
		}
	}
	now := time.Now()
	writeRaw(&Article{Type: ContentTypePlan, Title: "Wip Plan", Slug: "wip-plan", Tags: []string{"nexwiki", "wip"}, CreatedAt: now, Timestamp: now, Version: 1}, "# p")
	writeRaw(&Article{Type: ContentTypePlan, Title: "Statusless Plan", Slug: "statusless-plan", Tags: []string{"nexwiki"}, CreatedAt: now, Timestamp: now, Version: 1}, "# p")
	writeRaw(&Article{Type: ContentTypePlan, Title: "Double Plan", Slug: "double-plan", Tags: []string{"superseded", "completed"}, CreatedAt: now, Timestamp: now, Version: 1}, "# p")
	writeRaw(&Article{Type: ContentTypeWiki, Title: "Draft Article", Slug: "draft-article", Tags: []string{"draft", "topic"}, CreatedAt: now, Timestamp: now, Version: 1}, "# a")

	// Remove the marker NewStorage wrote for the empty directory, then re-run the sweep.
	if err := os.Remove(filepath.Join(dir, planStatusMigrationMarker)); err != nil {
		t.Fatalf("remove marker: %v", err)
	}
	if err := s.MigratePlanStatuses(); err != nil {
		t.Fatalf("migration: %v", err)
	}
	_ = s.Close()

	// Reopen: the marker exists now, so the migration must not run again (idempotence).
	s2, err := NewStorage(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	check := func(slug, wantStatus string) *Article {
		t.Helper()
		art, err := s2.GetArticle(slug)
		if err != nil {
			t.Fatalf("get %s: %v", slug, err)
		}
		got := planStatusesIn(art.Tags)
		if len(got) != 1 || got[0] != wantStatus {
			t.Errorf("%s: status %v, want %s", slug, got, wantStatus)
		}
		if art.StatusChangedAt.IsZero() {
			t.Errorf("%s: migration must backfill status_changed_at", slug)
		}
		return art
	}
	check("wip-plan", "implementing")
	check("statusless-plan", "draft")
	// Precedence: the terminal state wins — only "superseded" is still true of a replaced plan.
	check("double-plan", "superseded")

	wipPlan, _ := s2.GetArticle("wip-plan")
	versionAfterFirstRun := wipPlan.Version

	art, err := s2.GetArticle("draft-article")
	if err != nil {
		t.Fatalf("get draft-article: %v", err)
	}
	for _, tag := range art.Tags {
		if strings.EqualFold(tag, "draft") {
			t.Errorf("non-plan sweep must strip plan-exclusive statuses, still has: %v", art.Tags)
		}
	}

	// The reopen above would have re-migrated without the marker; version stability proves it
	// did not.
	wipPlan2, _ := s2.GetArticle("wip-plan")
	if wipPlan2.Version != versionAfterFirstRun {
		t.Errorf("migration re-ran despite its marker: version %d → %d", versionAfterFirstRun, wipPlan2.Version)
	}
}

func TestSearchArchivedTagFacetImpliesIncludeArchived(t *testing.T) {
	s := newLifecycleStorage(t)
	_, _ = s.SaveArticle("", "Zzarchived Doc", "# zzuniquebody archived content", "", "", "", "", []string{"archived"}, ContentTypeWiki)

	// The trap this fixes: asking for archived documents the obvious way returned zero results,
	// because the archived filter discarded every hit before the tag filter could match it.
	results, err := s.SearchArticlesWithOptions("zzuniquebody", SearchOptions{Tags: []string{"archived"}})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 || results[0].Slug != "zzarchived-doc" {
		t.Errorf("search_wiki(tags:[archived]) must return archived documents, got %v", results)
	}
}

func TestDeleteTagGloballyRefusesPlanStatuses(t *testing.T) {
	s := newLifecycleStorage(t)
	if err := s.DeleteTagGlobally("completed"); err == nil || !strings.Contains(err.Error(), "plan lifecycle status") {
		t.Errorf("deleting a plan status globally must be refused, got: %v", err)
	}
}

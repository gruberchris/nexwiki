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

// savePlan seeds a plan in a given lifecycle state.
func savePlan(t *testing.T, s *Storage, title, status string, tags ...string) *Article {
	t.Helper()
	art, err := s.SaveArticleWithStatus("", title, "# plan", "", "", "", "seed", tags, ContentTypePlan, &status)
	if err != nil {
		t.Fatalf("seeding plan %q failed: %v", title, err)
	}
	return art
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

func TestValidateStatus(t *testing.T) {
	cases := []struct {
		name    string
		docType string
		status  string
		wantErr string // empty means valid
	}{
		{"plan with a plan status", ContentTypePlan, "implementing", ""},
		{"plan archived", ContentTypePlan, "archived", ""},
		{"plan with no status", ContentTypePlan, "", "must have a status"},
		{"plan with a skill status", ContentTypePlan, "ready", "use 'draft' instead"},
		{"plan with a legacy synonym", ContentTypePlan, "wip", "use 'implementing' instead"},
		{"plan with an invented status", ContentTypePlan, "in-flight", "do not invent new ones"},
		{"plan status is case-insensitive", ContentTypePlan, "Implementing", ""},

		{"skill draft", ContentTypeSkill, "draft", ""},
		{"skill ready", ContentTypeSkill, "ready", ""},
		{"skill archived", ContentTypeSkill, "archived", ""},
		{"skill with no status is fine", ContentTypeSkill, "", ""},
		{"skill with a plan status", ContentTypeSkill, "implementing", "use 'draft' instead"},
		{"skill with an invented status", ContentTypeSkill, "polished", "do not invent new ones"},

		// Wiki articles and memories have no lifecycle. Nothing writes their status field and no
		// UI offers it, but a hand-written value is tolerated rather than policed.
		{"wiki article is never validated", ContentTypeWiki, "ready", ""},
		{"wiki article may hold anything", ContentTypeWiki, "needs-diagrams", ""},
		{"memory is never validated", ContentTypeMemory, "whatever", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateStatus(tc.docType, tc.status)
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

// TestStatusWordsAreRejectedAsTags is the guard that keeps status single-sourced: an agent must not
// be able to describe a plan's state in its tag list, where it could contradict the field.
func TestStatusWordsAreRejectedAsTags(t *testing.T) {
	s := newLifecycleStorage(t)

	for _, tag := range []string{"completed", "wip", "ready", "archived", "in-progress"} {
		if _, err := s.SaveArticle("", "Tagged Plan "+tag, "# p", "", "", "", "", []string{"nexwiki", tag}, ContentTypePlan); err == nil {
			t.Errorf("tag %q on a plan should be rejected — status belongs in the field", tag)
		}
		if _, err := s.SaveArticle("", "Tagged Skill "+tag, "# s", "", "", "", "", []string{tag}, ContentTypeSkill); err == nil {
			t.Errorf("tag %q on a skill should be rejected — status belongs in the field", tag)
		}
	}

	// Free tags are untouched on both, and wiki articles may tag anything at all.
	if _, err := s.SaveArticle("", "Project Tagged Plan", "# p", "", "", "", "", []string{"nexwiki", "postgres"}, ContentTypePlan); err != nil {
		t.Errorf("project tags must still pass on a plan: %v", err)
	}
	if _, err := s.SaveArticle("", "Ready Article", "# a", "", "", "", "", []string{"ready", "golang"}, ContentTypeWiki); err != nil {
		t.Errorf("a wiki article may tag anything, including 'ready': %v", err)
	}
}

func TestPlanStatusDefaultsAndPreservation(t *testing.T) {
	s := newLifecycleStorage(t)

	// A new plan with no status given enters the lifecycle at draft.
	art, err := s.SaveArticle("", "Defaulting Plan", "# p", "", "", "", "", nil, ContentTypePlan)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if art.Status != DefaultPlanStatus {
		t.Fatalf("a new plan starts in draft, got %q", art.Status)
	}

	completed, err := s.SetStatus(art.Slug, "completed", 0, "shipped")
	if err != nil {
		t.Fatalf("SetStatus failed: %v", err)
	}
	if completed.Status != "completed" {
		t.Fatalf("status = %q, want completed", completed.Status)
	}

	// An ordinary content edit must never reset the state — this is why SaveArticle takes no
	// status at all and preserves what is on disk.
	edited, err := s.SaveArticle(art.Slug, art.Title, "# p\n\ntypo fixed", "", "", "", "", nil, ContentTypePlan)
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	if edited.Status != "completed" {
		t.Errorf("a content edit reset the status to %q — it must preserve 'completed'", edited.Status)
	}
}

func TestPlanStatusChangeStamping(t *testing.T) {
	s := newLifecycleStorage(t)
	art := savePlan(t, s, "Stamp Plan", "draft")
	if art.StatusChangedAt.IsZero() {
		t.Fatal("a new plan must get status_changed_at stamped")
	}
	firstStamp := art.StatusChangedAt

	// A content edit that keeps the status must NOT restart the clock — fixing a typo in a
	// completed plan restarting its archive timer is the exact defect this field exists to avoid.
	time.Sleep(10 * time.Millisecond)
	art2, err := s.SaveArticle(art.Slug, art.Title, "# p\n\ntypo fixed", "", "", "", "", nil, ContentTypePlan)
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	// Compare at second precision: the stamp round-trips through RFC3339 on disk.
	if !art2.StatusChangedAt.Equal(firstStamp.Truncate(time.Second)) {
		t.Errorf("content edit restarted the status clock: %v → %v", firstStamp, art2.StatusChangedAt)
	}

	time.Sleep(1100 * time.Millisecond)
	art3, err := s.SetStatus(art.Slug, "implementing", 0, "work begins")
	if err != nil {
		t.Fatalf("status change failed: %v", err)
	}
	if !art3.StatusChangedAt.After(firstStamp) {
		t.Error("status change did not restamp status_changed_at")
	}
}

func TestArchivedStatusMirrorsArchivedAt(t *testing.T) {
	for _, docType := range []string{ContentTypePlan, ContentTypeSkill} {
		t.Run(docType, func(t *testing.T) {
			s := newLifecycleStorage(t)
			start := "completed"
			if docType == ContentTypeSkill {
				start = "ready"
			}
			art, err := s.SaveArticleWithStatus("", "Mirror Doc", "# d", "", "", "", "", nil, docType, &start)
			if err != nil {
				t.Fatalf("seed failed: %v", err)
			}
			if !art.ArchivedAt.IsZero() {
				t.Fatal("a live document is not archived")
			}

			archived, err := s.SetStatus(art.Slug, StatusArchived, 0, "archive")
			if err != nil {
				t.Fatalf("archive failed: %v", err)
			}
			if archived.ArchivedAt.IsZero() {
				t.Fatal("entering archived must set archived_at")
			}
			if !IsArchived(archived) {
				t.Error("a document with status archived must count as archived")
			}

			// Revival must clear archived_at — otherwise IsArchived stays true and the document
			// sits in a live state while remaining hidden from search and on the deletion clock.
			revived, err := s.SetStatus(art.Slug, start, 0, "revive")
			if err != nil {
				t.Fatalf("revive failed: %v", err)
			}
			if !revived.ArchivedAt.IsZero() {
				t.Errorf("revival must clear archived_at, got %v", revived.ArchivedAt)
			}
			if IsArchived(revived) {
				t.Error("a revived document must not still count as archived")
			}
		})
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
		savePlan(t, s, tc.title, tc.status)
	}
	for _, slug := range []string{"old-completed", "old-superseded", "old-parked", "old-evergreen", "old-draft", "old-implementing", "old-blocked"} {
		backdateStatusChange(t, s, slug, 120)
	}

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365}}
	w.Sweep()

	want := map[string]string{
		"old-completed":    "archived",
		"old-superseded":   "archived",
		"old-parked":       "parked",    // timer-exempt by design — that exemption is its purpose
		"old-evergreen":    "evergreen", // likewise
		"old-draft":        "draft",
		"old-implementing": "implementing",
		"old-blocked":      "blocked",
		"fresh-completed":  "completed", // 90 days have not elapsed
	}
	for slug, wantStatus := range want {
		art, err := s.GetArticle(slug)
		if err != nil {
			t.Fatalf("plan %q missing after sweep: %v", slug, err)
		}
		if art.Status != wantStatus {
			t.Errorf("%s: status = %q, want %q", slug, art.Status, wantStatus)
		}
		if wantStatus == StatusArchived && art.ArchivedAt.IsZero() {
			t.Errorf("%s: auto-archive must set archived_at", slug)
		}
	}
}

func TestLifecycleWorkerDeletesLongArchivedPlans(t *testing.T) {
	s := newLifecycleStorage(t)
	savePlan(t, s, "Ancient Archived", StatusArchived)
	backdateStatusChange(t, s, "ancient-archived", 400)

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365}}
	w.Sweep()

	if _, err := s.GetArticle("ancient-archived"); err == nil {
		t.Error("a plan archived past the deletion window must be deleted")
	}
}

func TestLifecycleWorkerRefusesToDeleteLinkedPlans(t *testing.T) {
	s := newLifecycleStorage(t)
	savePlan(t, s, "Referenced Archived", StatusArchived)
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
	savePlan(t, s, "Dry Completed", "completed")
	savePlan(t, s, "Dry Archived", StatusArchived)
	backdateStatusChange(t, s, "dry-completed", 120)
	backdateStatusChange(t, s, "dry-archived", 400)

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365, DryRun: true}}
	w.Sweep()

	art, err := s.GetArticle("dry-completed")
	if err != nil {
		t.Fatal("dry run deleted a plan")
	}
	if art.Status != "completed" {
		t.Errorf("dry run changed a status to %q", art.Status)
	}
	if _, err := s.GetArticle("dry-archived"); err != nil {
		t.Error("dry run deleted the archived plan")
	}
}

// TestLifecycleWorkerBackfillsStatuslessPlans covers the other half of the migration decision:
// boot stays fast because statusless plans are left alone there, and the worker — which already
// sweeps at startup, off the boot path — is what gives them a status.
func TestLifecycleWorkerBackfillsStatuslessPlans(t *testing.T) {
	s := newLifecycleStorage(t)

	// Write a pre-field plan straight to disk; SaveArticle would default it on the way in.
	now := time.Now()
	a := &Article{Type: ContentTypePlan, Title: "Legacy Plan", Slug: "legacy-plan", Tags: []string{"nexwiki"},
		CreatedAt: now, Timestamp: now, Version: 1, Content: "# p"}
	if err := os.WriteFile(filepath.Join(s.ArticleDir, "legacy-plan.md"), []byte(serializeFrontMatter(a)+a.Content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	w := &PlanLifecycleWorker{Storage: s, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365}}
	w.Sweep()

	got, err := s.GetArticle("legacy-plan")
	if err != nil {
		t.Fatalf("plan disappeared: %v", err)
	}
	if got.Status != DefaultPlanStatus {
		t.Errorf("the worker must give a statusless plan a status, got %q", got.Status)
	}
	if got.StatusChangedAt.IsZero() {
		t.Error("backfilling a status must stamp status_changed_at")
	}
	if len(got.Tags) != 1 || got.Tags[0] != "nexwiki" {
		t.Errorf("free tags must survive the backfill, got %v", got.Tags)
	}

	// A dry run proposes it without writing.
	s2 := newLifecycleStorage(t)
	if err := os.WriteFile(filepath.Join(s2.ArticleDir, "legacy-plan.md"), []byte(serializeFrontMatter(a)+a.Content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	dry := &PlanLifecycleWorker{Storage: s2, Cfg: PlanLifecycleConfig{ArchiveAfterDays: 90, DeleteAfterDays: 365, DryRun: true}}
	dry.Sweep()
	if again, _ := s2.GetArticle("legacy-plan"); again.Status != "" {
		t.Errorf("a dry run must not write a status, got %q", again.Status)
	}
}

func TestLifecycleWorkerSkipsPlansWithoutStatusClock(t *testing.T) {
	s := newLifecycleStorage(t)
	art := savePlan(t, s, "Clockless Completed", "completed")

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
	if got.Status != "completed" {
		t.Errorf("a plan without status_changed_at must be 'not yet eligible', got %q", got.Status)
	}
}

func TestMigrationTakesStatusOutOfTags(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStorage(dir)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}

	// Write pre-field documents straight to disk — SaveArticle would reject the plan/skill ones now.
	now := time.Now()
	writeRaw := func(docType, title, slug string, tags []string) {
		a := &Article{Type: docType, Title: title, Slug: slug, Tags: tags, CreatedAt: now, Timestamp: now, Version: 1, Content: "# body"}
		if err := os.WriteFile(filepath.Join(s.ArticleDir, slug+".md"), []byte(serializeFrontMatter(a)+a.Content), 0644); err != nil {
			t.Fatalf("write %s: %v", slug, err)
		}
	}
	writeRaw(ContentTypePlan, "Wip Plan", "wip-plan", []string{"nexwiki", "wip"})
	writeRaw(ContentTypePlan, "Statusless Plan", "statusless-plan", []string{"nexwiki"})
	writeRaw(ContentTypePlan, "Double Plan", "double-plan", []string{"superseded", "completed"})
	writeRaw(ContentTypeSkill, "Ready Skill", "ready-skill", []string{"ready", "second-brain"})
	writeRaw(ContentTypeSkill, "Retired Skill", "retired-skill", []string{"nexwiki", "archived"})
	writeRaw(ContentTypeWiki, "Ready Article", "ready-article", []string{"ready", "golang"})
	writeRaw(ContentTypeWiki, "Retired Article", "retired-article", []string{"archived", "golang"})
	writeRaw(ContentTypeWiki, "Inbox Capture", "inbox-capture", []string{"inbox", "raw"})
	writeRaw(ContentTypeMemory, "Scoped Memory", "scoped-memory", []string{"memory-nexwiki", "wip"})

	// Remove the marker NewStorage wrote for the empty directory, then run the sweep.
	if err := os.Remove(filepath.Join(dir, statusFieldMigrationMarker)); err != nil {
		t.Fatalf("remove marker: %v", err)
	}
	if err := s.MigrateStatusToField(); err != nil {
		t.Fatalf("migration: %v", err)
	}
	_ = s.Close()

	// Reopen: the marker exists now, so the migration must not run again (idempotence).
	s2, err := NewStorage(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	check := func(slug, wantStatus string, wantTags ...string) *Article {
		t.Helper()
		art, err := s2.GetArticle(slug)
		if err != nil {
			t.Fatalf("get %s: %v", slug, err)
		}
		if art.Status != wantStatus {
			t.Errorf("%s: status = %q, want %q", slug, art.Status, wantStatus)
		}
		if len(art.Tags) != len(wantTags) {
			t.Errorf("%s: tags = %v, want %v", slug, art.Tags, wantTags)
		}
		return art
	}

	plan := check("wip-plan", "implementing", "nexwiki")
	if plan.StatusChangedAt.IsZero() {
		t.Error("migration must backfill status_changed_at on plans")
	}
	// A plan that never carried a status tag is deliberately left alone by the migration: there
	// is nothing to move, and rewriting every such plan costs a write, a history entry, and a
	// reindex each on first boot. It gets its status from the worker or its next edit instead.
	check("statusless-plan", "", "nexwiki")
	// Precedence: the terminal state wins — only "superseded" is still true of a replaced plan.
	check("double-plan", "superseded")
	check("ready-skill", "ready", "second-brain")
	check("retired-skill", StatusArchived, "nexwiki")

	// Wiki articles have no lifecycle: the retired status tag is simply removed, and every other
	// tag survives. `archived` stays because on an article it is the archival mechanism
	// (archived_at, search hiding), not a label.
	check("ready-article", "", "golang")
	retired := check("retired-article", "", "archived", "golang")
	if !IsArchived(retired) {
		t.Error("an archived article must still count as archived after the migration")
	}
	// `inbox` marks a raw capture awaiting compilation — a workflow marker, not a status label.
	check("inbox-capture", "", "inbox", "raw")
	// Memories get the same treatment, and their tool-managed scope tag is never touched.
	check("scoped-memory", "", "memory-nexwiki")

	versionAfterFirstRun := plan.Version
	again, _ := s2.GetArticle("wip-plan")
	if again.Version != versionAfterFirstRun {
		t.Errorf("migration re-ran despite its marker: version %d → %d", versionAfterFirstRun, again.Version)
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

package server

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Story 09 tests: the Trained Skill Result report. The terminal gate (a report
// is generated only when the run ENDS — never at claim time, and never for a
// live job), generation idempotency (exactly one report per run, including the
// crash-window recovery between the article write and the job stamp), the
// agent immutability guard (every agent write tool refuses with
// ReportImmutableError, the storage choke point re-asserts the marker tag, and
// DeleteTagGlobally refuses it outright), and the honest no-promotion shape
// (the skill stays byte-identical; the report carries the real terminal
// outcome and its end reason).

// endedReportFixture seeds a skill plus a queued job and stops the run the way
// `end` says, landing that terminal stop's story-09 report. Returns the
// reloaded job and the report article.
func endedReportFixture(t *testing.T, s *Storage, title, end string) (*EvolutionJob, *Article) {
	t.Helper()
	skill := seedJobSkill(t, s, title)
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	switch end {
	case "cancel":
		if _, err := s.CancelEvolutionJob(job.ID, "operator stopped it"); err != nil {
			t.Fatalf("CancelEvolutionJob failed: %v", err)
		}
	case "complete":
		if _, err := s.ClaimEvolutionJob(job.ID, token, "runner"); err != nil {
			t.Fatalf("ClaimEvolutionJob failed: %v", err)
		}
		if _, err := s.CompleteEvolutionJob(job.ID, token, "complete", ""); err != nil {
			t.Fatalf("CompleteEvolutionJob failed: %v", err)
		}
	default:
		t.Fatalf("unknown end mode %q", end)
	}
	fresh, err := s.GetEvolutionJob(job.ID)
	if err != nil {
		t.Fatalf("job reload failed: %v", err)
	}
	report, err := s.EnsureSkillResultReport(job.ID)
	if err != nil {
		t.Fatalf("EnsureSkillResultReport failed: %v", err)
	}
	if report == nil || fresh.ResultReportSlug != report.Slug {
		t.Fatalf("the terminal stop must land its report: job %+v report %+v", fresh, report)
	}
	return fresh, report
}

// taggedReportCount counts the wiki articles carrying the generated-report
// marker tag.
func taggedReportCount(t *testing.T, s *Storage) int {
	t.Helper()
	metas, err := s.ListArticles()
	if err != nil {
		t.Fatalf("ListArticles failed: %v", err)
	}
	n := 0
	for _, meta := range metas {
		if meta.Type == ContentTypeWiki && hasTag(meta.Tags, SkillResultReportTag) {
			n++
		}
	}
	return n
}

// assertNoResultReport fails the test unless the run has produced no report at
// all: no stamp, no tagged article.
func assertNoResultReport(t *testing.T, s *Storage, job *EvolutionJob) {
	t.Helper()
	fresh, err := s.GetEvolutionJob(job.ID)
	if err != nil {
		t.Fatalf("job reload failed: %v", err)
	}
	if fresh.ResultReportSlug != "" {
		t.Fatalf("no report may be stamped on a live run: %+v", fresh)
	}
	if found, ferr := s.findResultReportForJob(fresh); ferr != nil || found != nil {
		t.Fatalf("no report article may exist for a live run: %v %+v", ferr, found)
	}
}

func TestResultReportTerminalGate(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedJobSkill(t, s, "Gate Skill")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	before := skill.Content

	// Queued: the run has not started, let alone ended.
	if _, gerr := s.EnsureSkillResultReport(job.ID); gerr == nil {
		t.Fatal("ensuring a report for a queued job must be refused")
	}
	assertNoResultReport(t, s, job)

	// Claimed: a claim is never a run end — the claim-time-report regression.
	if _, cerr := s.ClaimEvolutionJob(job.ID, token, "runner"); cerr != nil {
		t.Fatalf("ClaimEvolutionJob failed: %v", cerr)
	}
	if _, gerr := s.EnsureSkillResultReport(job.ID); gerr == nil {
		t.Fatal("ensuring a report for a claimed job must be refused")
	}
	assertNoResultReport(t, s, job)

	// The live skill is byte-identical at its original version: no report path
	// may touch it mid-run.
	live, err := s.GetArticle(skill.Slug)
	if err != nil {
		t.Fatalf("skill reload failed: %v", err)
	}
	if live.Version != 1 || live.Content != before {
		t.Fatalf("a claim must leave the skill untouched: v%d %q", live.Version, live.Content)
	}

	// Once the run ends, the same ensure generates the report.
	if _, cerr := s.CancelEvolutionJob(job.ID, "operator stopped it"); cerr != nil {
		t.Fatalf("CancelEvolutionJob failed: %v", cerr)
	}
	report, gerr := s.EnsureSkillResultReport(job.ID)
	if gerr != nil {
		t.Fatalf("ensure after the stop must generate: %v", gerr)
	}
	if report == nil || !strings.Contains(report.Content, "Outcome: **cancelled**") {
		t.Fatalf("the ended run must carry its terminal outcome: %+v", report)
	}
}

func TestResultReportGenerationIsIdempotent(t *testing.T) {
	s := newLifecycleStorage(t)
	job, report := endedReportFixture(t, s, "Idempotent Skill", "complete")

	// Generating again returns the same article — no second report.
	again, err := s.EnsureSkillResultReport(job.ID)
	if err != nil {
		t.Fatalf("re-ensure failed: %v", err)
	}
	if again.Slug != report.Slug {
		t.Fatalf("re-ensure = %q, want the same report %q", again.Slug, report.Slug)
	}
	if n := taggedReportCount(t, s); n != 1 {
		t.Fatalf("tagged reports = %d, want exactly one", n)
	}

	// Crash-window recovery: a run that wrote its report but crashed before
	// stamping the job re-finds the article instead of duplicating it.
	dangling := *job
	dangling.ResultReportSlug = ""
	if err := s.writeEvolutionJob(&dangling); err != nil {
		t.Fatalf("stamp clear failed: %v", err)
	}
	recovered, err := s.EnsureSkillResultReport(job.ID)
	if err != nil {
		t.Fatalf("recovery ensure failed: %v", err)
	}
	if recovered.Slug != report.Slug {
		t.Fatalf("recovery = %q, want the original report %q", recovered.Slug, report.Slug)
	}
	if n := taggedReportCount(t, s); n != 1 {
		t.Fatalf("tagged reports after recovery = %d, want exactly one", n)
	}
	fresh, err := s.GetEvolutionJob(job.ID)
	if err != nil || fresh.ResultReportSlug != report.Slug {
		t.Fatalf("recovery must re-stamp the job: %+v %v", fresh, err)
	}
}

func TestResultReportReflectsRunEnd(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedJobSkill(t, s, "Cancel Report Skill")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}

	// The claim lands no report; the stop does.
	if _, cerr := s.ClaimEvolutionJob(job.ID, token, "runner"); cerr != nil {
		t.Fatalf("ClaimEvolutionJob failed: %v", cerr)
	}
	assertNoResultReport(t, s, job)

	if _, cerr := s.CancelEvolutionJob(job.ID, "operator aborted the run"); cerr != nil {
		t.Fatalf("CancelEvolutionJob failed: %v", cerr)
	}
	report, gerr := s.EnsureSkillResultReport(job.ID)
	if gerr != nil {
		t.Fatalf("EnsureSkillResultReport failed: %v", gerr)
	}
	if !strings.Contains(report.Content, "Outcome: **cancelled**") {
		t.Fatal("the outcome label must be the real terminal outcome")
	}
	if !strings.Contains(report.Content, "operator aborted the run") {
		t.Fatal("the end reason must be included in the report")
	}
	if !strings.Contains(report.Content, "/articles/"+skill.Slug) {
		t.Fatal("the report must link back to its skill")
	}

	// No promotion: the skill is untouched — the report→skill link suffices.
	live, err := s.GetArticle(skill.Slug)
	if err != nil {
		t.Fatalf("skill reload failed: %v", err)
	}
	if live.Version != 1 || live.Content != "# Cancel Report Skill body" {
		t.Fatalf("a no-promotion stop must leave the skill untouched: v%d %q", live.Version, live.Content)
	}
}

func TestResultReportLandsOnRunCapRefusedClaim(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedJobSkill(t, s, "Run Cap Report Skill")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	// Backdate the run deadline: the run cap expired before any claim.
	stale := *job
	stale.RunDeadlineAt = time.Now().UTC().Add(-time.Second)
	if err := s.writeEvolutionJob(&stale); err != nil {
		t.Fatalf("deadline backdate failed: %v", err)
	}

	if _, cerr := s.ClaimEvolutionJob(job.ID, token, "runner"); cerr == nil {
		t.Fatal("a claim past the run cap must be refused")
	}
	fresh, err := s.GetEvolutionJob(job.ID)
	if err != nil || fresh.Status != EvolutionJobTimeout {
		t.Fatalf("the refused claim must mark the job timeout: %+v %v", fresh, err)
	}

	// That terminal stop lands its report, labeled with the real outcome.
	report, gerr := s.EnsureSkillResultReport(job.ID)
	if gerr != nil {
		t.Fatalf("the run-cap stop must land its report: %v", gerr)
	}
	if !strings.Contains(report.Content, "Outcome: **exhausted**") {
		t.Fatalf("a run-cap stop reads exhausted: %q", report.Content[:400])
	}
	if !strings.Contains(report.Content, "run cap exceeded before claim") {
		t.Fatal("the end reason must be included in the report")
	}
	live, err := s.GetArticle(skill.Slug)
	if err != nil || live.Version != 1 {
		t.Fatalf("no promotion on a run that never drove: %+v %v", live, err)
	}
}

func TestResultReportAgentWritesRefused(t *testing.T) {
	srv := newMCPServer(t)
	s := srv.Storage
	job, report := endedReportFixture(t, s, "Guard Skill", "cancel")
	_ = job

	// Edit refused at the MCP tool layer.
	resp := toolCall(t, srv, fmt.Sprintf(`{"name":"edit_wiki_article","arguments":{"slug":%q,"title":%q,"content":"# tampered report","loaded_version":%d}}`, report.Slug, report.Title, report.Version))
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "agents cannot edit it") {
		t.Fatalf("agent edit must be refused with the immutability convention: %+v", resp)
	}
	fresh, err := s.GetArticle(report.Slug)
	if err != nil || fresh.Version != report.Version || fresh.Content != report.Content {
		t.Fatalf("the refused edit must not rewrite the report: %+v %v", fresh, err)
	}

	// A tag-strip edit is refused too: the marker is not the agent's to drop.
	resp = toolCall(t, srv, fmt.Sprintf(`{"name":"update_article_tags","arguments":{"slug":%q,"tags":["misc"],"loaded_version":%d}}`, report.Slug, fresh.Version))
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "Trained Skill Result report") {
		t.Fatalf("agent tag-strip must be refused: %+v", resp)
	}
	if after, _ := s.GetArticle(report.Slug); !hasTag(after.Tags, SkillResultReportTag) {
		t.Fatal("the marker must survive the refused strip")
	}

	// A revert would rewrite history outside the generation path: refused.
	resp = toolCall(t, srv, fmt.Sprintf(`{"name":"revert_article_version","arguments":{"slug":%q,"version":%d}}`, report.Slug, report.Version))
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "Trained Skill Result report") {
		t.Fatalf("agent revert must be refused: %+v", resp)
	}

	// The report is the run's record — deletion is refused as well.
	resp = toolCall(t, srv, fmt.Sprintf(`{"name":"delete_wiki_article","arguments":{"slug":%q}}`, report.Slug))
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "Trained Skill Result report") {
		t.Fatalf("agent deletion must be refused: %+v", resp)
	}

	// Storage choke point: a write path that drops the marker cannot strip it.
	rewritten, err := s.saveArticleLocked(report.Slug, report.Title, strings.Replace(report.Content, "## Amendments", "## Tampered", 1), report.Description,
		report.Source, report.Resource, "attempt to drop the report marker", []string{"unrelated"}, ContentTypeWiki, ArticleOverrides{})
	if err != nil {
		t.Fatalf("storage save failed: %v", err)
	}
	if !hasTag(rewritten.Tags, SkillResultReportTag) {
		t.Fatal("saveArticleLocked must re-assert the tool-managed report marker")
	}

	// DeleteTagGlobally refuses the marker outright.
	if derr := s.DeleteTagGlobally(SkillResultReportTag); derr == nil {
		t.Fatal("deleting the report marker globally must be refused")
	} else if !strings.Contains(derr.Error(), "tool-managed") {
		t.Fatalf("the refusal must name the convention: %v", derr)
	}
}

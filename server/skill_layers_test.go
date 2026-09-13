package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Story 10 tests: the three-layer guards. WikiSkill keeps three layers distinct
// and the asymmetry between them is the design — permanent memory versus
// reversible action — so these tests hold the asymmetry, not merely the code.
//
//   - RAW layer: every evolution artifact is evidence, so the integrity scan
//     finds hash mismatches, unparseable records, and vanished references —
//     and finds NOTHING on a clean wiki, where a missing loop state is normal.
//   - WIKI layer: wiki-scope articles are accumulative — patch and append
//     stay open, revert and delete are refused, the marker cannot be stripped,
//     nothing auto-deletes them, and supersession is a declared pointer that
//     wiki_health reports without ever following.
//   - SKILLS layer: a trained skill without a recorded motivating pattern is a
//     WARNING — a link in either direction counts, the generated report's own
//     link is boilerplate and does not count, and an untrained skill is never
//     flagged.

// --- shared helpers -------------------------------------------------------------------------

// seedWikiScopeArticle saves a wiki article carrying the tool-managed
// wiki-scope marker, plus any extra tags.
func seedWikiScopeArticle(t *testing.T, s *Storage, title, content string, tags ...string) *Article {
	t.Helper()
	all := append([]string{}, tags...)
	if !hasTag(all, WikiskillWikiTag) {
		all = append(all, WikiskillWikiTag)
	}
	art, err := s.SaveArticle("", title, content, "", "", "", "seed", all, ContentTypeWiki)
	if err != nil {
		t.Fatalf("seeding wiki-scope article %q failed: %v", title, err)
	}
	return art
}

// setSupersededBy writes the supersession pointer through the storage edit
// path (what edit_wiki_article and the REST editor both reach).
func setSupersededBy(t *testing.T, s *Storage, art *Article, successor string) *Article {
	t.Helper()
	val := successor
	updated, err := s.ApplyArticleEdit(art.Slug, ArticleEdit{
		Title:         art.Title,
		Content:       art.Content,
		LoadedVersion: art.Version,
		SupersededBy:  &val,
	})
	if err != nil {
		t.Fatalf("setting superseded_by on %q failed: %v", art.Slug, err)
	}
	return updated
}

// graphSlugs returns the link graph's document slugs in the sorted order the
// health tool walks them.
func graphSlugs(graph *LinkGraph) []string {
	slugs := make([]string, 0, len(graph.Meta))
	for slug := range graph.Meta {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs
}

// purposeFlagged runs the SKILLS-layer scan and returns the flagged slugs.
func purposeFlagged(t *testing.T, s *Storage) map[string]bool {
	t.Helper()
	graph, err := s.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	flagged := map[string]bool{}
	for _, f := range scanSkillsWithoutPurpose(graphSlugs(graph), graph, s) {
		flagged[f.Slug] = true
	}
	return flagged
}

// supersededBySlug indexes the WIKI-layer scan's findings by slug.
func supersededBySlug(t *testing.T, s *Storage) (map[string]HealthFinding, int) {
	t.Helper()
	graph, err := s.ScanLinkGraph()
	if err != nil {
		t.Fatalf("ScanLinkGraph failed: %v", err)
	}
	findings, actionable := scanSupersededWiki(graphSlugs(graph), graph)
	bySlug := map[string]HealthFinding{}
	for _, f := range findings {
		bySlug[f.Slug] = f
	}
	return bySlug, actionable
}

// findRawFinding returns the one finding of a kind for an ID, failing the test
// unless the scan produced exactly that shape.
func findRawFinding(t *testing.T, findings []RawIntegrityFinding, kind, id string) RawIntegrityFinding {
	t.Helper()
	var hits []RawIntegrityFinding
	for _, f := range findings {
		if f.Kind == kind && f.ID == id {
			hits = append(hits, f)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("expected exactly one %q finding for %q, got %d in: %+v", kind, id, len(hits), findings)
	}
	return hits[0]
}

// backdateArchivedAt rewrites a document's archived_at on disk so the archived
// cleanup sees it as past its retention window without sleeping.
func backdateArchivedAt(t *testing.T, s *Storage, slug string, days int) {
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
	art.ArchivedAt = time.Now().AddDate(0, 0, -days)
	if err := os.WriteFile(path, []byte(serializeFrontMatter(art)+art.Content), 0644); err != nil {
		t.Fatalf("write %s: %v", slug, err)
	}
}

// --- RAW layer: the integrity scan ----------------------------------------------------------

func TestLayersRawIntegrityCleanStorage(t *testing.T) {
	s := newLifecycleStorage(t)

	findings, err := s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("ScanRawArtifactIntegrity failed: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("a clean wiki must report no findings, got: %+v", findings)
	}

	// Real records with nothing to verify yet are still clean: a queued job
	// has no artifacts, eval data, or loop state yet, and a pending candidate
	// has never been gated so it carries no recorded hash to check against.
	skill := seedSkill(t, s, "Clean Layer Skill", "# body")
	if _, _, err := s.CreateEvolutionJob(skill.Slug, "", "opencode"); err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	if _, err := s.CreateSkillCandidate(skill.Slug, "# a proposed body", "", nil, "tester"); err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	findings, err = s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("ScanRawArtifactIntegrity failed: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("queued job + pending candidate must stay clean, got: %+v", findings)
	}
}

func TestLayersRawIntegrityTamperedArtifact(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Artifact Integrity Skill", "# body")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	if _, err := s.ClaimEvolutionJob(job.ID, token, "runner"); err != nil {
		t.Fatalf("ClaimEvolutionJob failed: %v", err)
	}
	body := "# variant body produced by the run"
	if _, err := s.UploadJobArtifact(job.ID, token, "variant.md", body, shaHex(body), ""); err != nil {
		t.Fatalf("UploadJobArtifact failed: %v", err)
	}

	// Untampered, the scan is silent — the finding must be earned.
	findings, err := s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("baseline scan failed: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("an untouched artifact must not be flagged, got: %+v", findings)
	}

	// Tamper the stored bytes: the upload-time hash check cannot see this, the
	// standing scan can.
	artPath := filepath.Join(s.JobDir, job.ID+".files", "artifacts", "variant.md")
	if err := os.WriteFile(artPath, []byte("# tampered body"), 0644); err != nil {
		t.Fatalf("tamper write failed: %v", err)
	}
	findings, err = s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	f := findRawFinding(t, findings, "artifact", job.ID)
	wantPath := filepath.Join("skill_jobs", job.ID+".files", "artifacts", "variant.md")
	if f.Path != wantPath {
		t.Errorf("finding path = %q, want the data-directory-relative %q", f.Path, wantPath)
	}
	if !strings.Contains(f.Detail, "variant.md") || !strings.Contains(f.Detail, "changed after upload") {
		t.Errorf("finding must name the artifact and the hash drift, got: %s", f.Detail)
	}
	if len(findings) != 1 {
		t.Errorf("tampering one artifact must produce exactly one finding, got %+v", findings)
	}
}

func TestLayersRawIntegrityMissingArtifactFile(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Missing Artifact Skill", "# body")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	if _, err := s.ClaimEvolutionJob(job.ID, token, "runner"); err != nil {
		t.Fatalf("ClaimEvolutionJob failed: %v", err)
	}
	body := "# variant body"
	if _, err := s.UploadJobArtifact(job.ID, token, "out.md", body, shaHex(body), ""); err != nil {
		t.Fatalf("UploadJobArtifact failed: %v", err)
	}

	// The job record still references the artifact; the file is gone.
	if err := os.Remove(filepath.Join(s.JobDir, job.ID+".files", "artifacts", "out.md")); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	findings, err := s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	f := findRawFinding(t, findings, "artifact", job.ID)
	if !strings.Contains(f.Detail, "missing") {
		t.Errorf("a vanished artifact must be reported as missing, got: %s", f.Detail)
	}
	if !strings.Contains(f.Path, filepath.Join("skill_jobs", job.ID+".files", "artifacts")) {
		t.Errorf("finding must locate the artifact directory, got: %s", f.Path)
	}
}

func TestLayersRawIntegrityTamperedEvalSplits(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "2")
	t.Setenv(EvalMinValEnv, "1")
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Eval Integrity Skill", "# body")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	content := `{"input": "case one", "expected": "alpha beta"}
{"input": "case two", "expected": "gamma delta"}
{"input": "case three", "expected": "epsilon zeta"}`
	if _, err := s.UploadEvolutionEvalSet(job.ID, token, "eval.jsonl", content); err != nil {
		t.Fatalf("UploadEvolutionEvalSet failed: %v", err)
	}

	findings, err := s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("baseline scan failed: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("an accepted eval upload must verify clean, got: %+v", findings)
	}

	// Rewrite a stored split: every gate decision that compared against this
	// set becomes suspect, so the finding says so.
	trainPath := filepath.Join(s.JobDir, job.ID+".files", "eval", "train.jsonl")
	if err := os.WriteFile(trainPath, []byte("{\"input\": \"case one\", \"expected\": \"REWRITTEN\"}\n"), 0644); err != nil {
		t.Fatalf("tamper write failed: %v", err)
	}
	findings, err = s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	f := findRawFinding(t, findings, "eval", job.ID)
	if f.Path != filepath.Join("skill_jobs", job.ID+".files", "eval") {
		t.Errorf("finding path = %q, want the eval directory", f.Path)
	}
	if !strings.Contains(f.Detail, "changed after upload") || !strings.Contains(f.Detail, "gate decision") {
		t.Errorf("finding must name the drift and its consequence, got: %s", f.Detail)
	}
	if len(findings) != 1 {
		t.Errorf("one tampered split must produce exactly one finding, got %+v", findings)
	}
}

func TestLayersRawIntegrityDecidedCandidateTamper(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Candidate Integrity Skill", "# v1 body")
	promoted, c := promoteCandidate(t, s, skill, "# v2 trained body")
	_ = promoted

	findings, err := s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("baseline scan failed: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("a decided candidate must verify clean, got: %+v", findings)
	}

	// Tamper the proposal body: the record no longer hashes to the value the
	// gate's decision recorded.
	rec, err := s.GetSkillCandidate(c.ID)
	if err != nil {
		t.Fatalf("GetSkillCandidate failed: %v", err)
	}
	rec.ProposedBody = "# tampered proposal"
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.CandidateDir, c.ID+".json"), raw, 0644); err != nil {
		t.Fatalf("tamper write failed: %v", err)
	}
	findings, err = s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	f := findRawFinding(t, findings, "candidate", c.ID)
	if f.Path != filepath.Join("skill_candidates", c.ID+".json") {
		t.Errorf("finding path = %q, want the candidate record path", f.Path)
	}
	if !strings.Contains(f.Detail, "content changed after the audit decision") {
		t.Errorf("finding must name the post-decision drift, got: %s", f.Detail)
	}
	if len(findings) != 1 {
		t.Errorf("one tampered candidate must produce exactly one finding, got %+v", findings)
	}
}

func TestLayersRawIntegrityLostCandidateAndDecision(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Lost Candidate Skill", "# v1 body")
	_, c := promoteCandidate(t, s, skill, "# v2 trained body")

	// A gate decision whose candidate record vanished: the raw layer holds the
	// decision, so the record's absence is a loss signal, not a cleanup.
	if err := os.Remove(filepath.Join(s.CandidateDir, c.ID+".json")); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	findings, err := s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	f := findRawFinding(t, findings, "candidate", c.ID)
	if !strings.Contains(f.Detail, "no candidate record on disk") {
		t.Errorf("a vanished decided candidate must be reported, got: %s", f.Detail)
	}

	// The mirror image: a decided candidate whose audit decision is gone. The
	// candidate file stays, the trail loses its record — flagged either way.
	s2 := newLifecycleStorage(t)
	skill2 := seedSkill(t, s2, "Unaudited Candidate Skill", "# v1 body")
	_, c2 := promoteCandidate(t, s2, skill2, "# v2 trained body")
	auditPath := SkillAuditPath(s2.DataDir)
	rawAudit, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit trail failed: %v", err)
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimRight(string(rawAudit), "\n"), "\n") {
		if line != "" && !strings.Contains(line, c2.ID) {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(auditPath, []byte(strings.Join(kept, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("rewrite audit trail failed: %v", err)
	}
	findings2, err := s2.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	f2 := findRawFinding(t, findings2, "candidate", c2.ID)
	if !strings.Contains(f2.Detail, "holds no decision record") {
		t.Errorf("a decided candidate without its audit record must be reported, got: %s", f2.Detail)
	}
}

func TestLayersRawIntegrityCorruptAuditLines(t *testing.T) {
	s := newLifecycleStorage(t)

	// The audit trail does not exist yet — absence of the file is normal.
	findings, err := s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("no audit trail must not be a finding, got: %+v", findings)
	}

	// A line that no longer parses vanishes silently from
	// ListSkillAuditRecords; the raw line count makes it visible again.
	auditPath := SkillAuditPath(s.DataDir)
	if err := os.WriteFile(auditPath, []byte("this line was never valid json\n"), 0644); err != nil {
		t.Fatalf("write audit trail failed: %v", err)
	}
	findings, err = s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	f := findRawFinding(t, findings, "audit", SkillAuditFilename)
	if f.Path != SkillAuditFilename {
		t.Errorf("finding path = %q, want the trail's data-relative name %q", f.Path, SkillAuditFilename)
	}
	if !strings.Contains(f.Detail, "1 line(s)") || !strings.Contains(f.Detail, "no longer parse") {
		t.Errorf("finding must count the corrupt lines, got: %s", f.Detail)
	}
	if len(findings) != 1 {
		t.Errorf("one corrupt line must produce exactly one finding, got %+v", findings)
	}
}

func TestLayersRawIntegrityLoopRecords(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Loop Integrity Skill", "# body")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	if _, err := s.ClaimEvolutionJob(job.ID, token, "runner"); err != nil {
		t.Fatalf("ClaimEvolutionJob failed: %v", err)
	}

	// A job that never entered the loop produces neither loop.json nor
	// iterations/ — absence of both is normal and must never be a finding.
	findings, err := s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("missing loop state is normal, not a finding, got: %+v", findings)
	}

	// Well-formed loop records are equally unremarkable.
	filesDir, err := s.jobFilesDir(job.ID)
	if err != nil {
		t.Fatalf("jobFilesDir failed: %v", err)
	}
	now := time.Now().UTC()
	if err := os.MkdirAll(filesDir, 0755); err != nil {
		t.Fatalf("mkdir files dir failed: %v", err)
	}
	state := LoopState{JobID: job.ID, SkillSlug: skill.Slug, Status: LoopStatusRunning,
		CurrentIteration: 1, CurrentPhase: "inference", StartedAt: now, UpdatedAt: now}
	rawState, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal loop state failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filesDir, "loop.json"), rawState, 0644); err != nil {
		t.Fatalf("write loop state failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(filesDir, "iterations"), 0755); err != nil {
		t.Fatalf("mkdir iterations failed: %v", err)
	}
	iter := IterationRecord{JobID: job.ID, SkillSlug: skill.Slug, Iteration: 1, StartedAt: now,
		Outcome: IterationOutcomeRejected, ValScore: 0.3, RBest: 0,
		Phases: []LoopPhaseEntry{{Phase: "inference", EnteredAt: now, Complete: true, ExitedAt: now}}}
	rawIter, err := json.Marshal(iter)
	if err != nil {
		t.Fatalf("marshal iteration failed: %v", err)
	}
	iterPath, err := s.iterationRecordPath(job.ID, 1)
	if err != nil {
		t.Fatalf("iterationRecordPath failed: %v", err)
	}
	if err := os.WriteFile(iterPath, rawIter, 0644); err != nil {
		t.Fatalf("write iteration failed: %v", err)
	}
	findings, err = s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("well-formed loop records must verify clean, got: %+v", findings)
	}

	// Corruption, however, is visible: the loop carries no hashes of its own,
	// so parse failure is what the scan can catch.
	if err := os.WriteFile(filepath.Join(filesDir, "loop.json"), []byte("{ not json"), 0644); err != nil {
		t.Fatalf("tamper write failed: %v", err)
	}
	if err := os.WriteFile(iterPath, []byte("[ broken"), 0644); err != nil {
		t.Fatalf("tamper write failed: %v", err)
	}
	findings, err = s.ScanRawArtifactIntegrity()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	loopFinding := findRawFinding(t, findings, "loop", job.ID)
	if !strings.Contains(loopFinding.Detail, "no longer parses as JSON") {
		t.Errorf("corrupt loop state must be reported, got: %s", loopFinding.Detail)
	}
	iterFinding := findRawFinding(t, findings, "iteration", job.ID)
	if !strings.Contains(iterFinding.Detail, "1 iteration record(s)") {
		t.Errorf("corrupt iteration records must be counted, got: %s", iterFinding.Detail)
	}
	if len(findings) != 2 {
		t.Errorf("expected exactly the loop and iteration findings, got %+v", findings)
	}
}

// TestWikiHealthRawArtifactIntegrityIntegration pins the other half of "never
// silent": the startup scan's findings surface again in wiki_health, where an
// agent doing maintenance already looks.
func TestWikiHealthRawArtifactIntegrityIntegration(t *testing.T) {
	srv := newMCPServer(t)
	s := srv.Storage

	out := healthReport(t, srv, `{}`)
	if out.RawArtifactIntegrityCount != 0 || len(out.RawArtifactIssues) != 0 {
		t.Fatalf("a clean wiki must report no raw integrity issues, got %+v", out.RawArtifactIssues)
	}

	skill := seedSkill(t, s, "Health Integrity Skill", "# body")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	if _, err := s.ClaimEvolutionJob(job.ID, token, "runner"); err != nil {
		t.Fatalf("ClaimEvolutionJob failed: %v", err)
	}
	body := "# run output"
	if _, err := s.UploadJobArtifact(job.ID, token, "evidence.md", body, shaHex(body), ""); err != nil {
		t.Fatalf("UploadJobArtifact failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.JobDir, job.ID+".files", "artifacts", "evidence.md"), []byte("# altered"), 0644); err != nil {
		t.Fatalf("tamper write failed: %v", err)
	}

	out = healthReport(t, srv, `{}`)
	if out.RawArtifactIntegrityCount != 1 || len(out.RawArtifactIssues) != 1 {
		t.Fatalf("raw integrity issues = %d, list = %+v, want exactly one", out.RawArtifactIntegrityCount, out.RawArtifactIssues)
	}
	issue := out.RawArtifactIssues[0]
	if issue.Slug != job.ID {
		t.Errorf("issue slug = %q, want the owning job %q", issue.Slug, job.ID)
	}
	if !strings.Contains(issue.Title, "artifact") {
		t.Errorf("issue must name the checked kind, got %q", issue.Title)
	}
	if !strings.Contains(issue.Detail, filepath.Join("skill_jobs", job.ID+".files", "artifacts", "evidence.md")) {
		t.Errorf("issue must locate the artifact for the operator, got: %s", issue.Detail)
	}
	if !strings.Contains(out.Content(), "Raw artifact integrity issues (WikiSkill evolution data): 1") {
		t.Errorf("prose must carry the category count, got:\n%s", out.Content())
	}
}

// --- WIKI layer: accumulative-only ----------------------------------------------------------

func TestWikiScopeTagHelpers(t *testing.T) {
	in := []string{"alpha"}
	out := ensureWikiScopeTag(in)
	if len(out) != 2 || out[1] != WikiskillWikiTag {
		t.Errorf("ensureWikiScopeTag must append the marker, got %v", out)
	}
	if len(in) != 1 || in[0] != "alpha" {
		t.Errorf("the input tag slice must not be mutated, got %v", in)
	}
	if dedup := ensureWikiScopeTag([]string{"WikiSkill-Wiki"}); len(dedup) != 1 {
		t.Errorf("the marker must dedupe case-insensitively, got %v", dedup)
	}

	if isWikiScopeArticle(nil) {
		t.Error("nil is not wiki-scope")
	}
	if isWikiScopeArticle(&Article{Type: ContentTypeWiki}) {
		t.Error("an untagged wiki article is not wiki-scope")
	}
	if isWikiScopeArticle(&Article{Type: ContentTypeSkill, Tags: []string{WikiskillWikiTag}}) {
		t.Error("the scope is one tag on Wiki documents only")
	}
	report := &Article{Slug: "report-ledger", Type: ContentTypeWiki, Tags: []string{SkillResultReportTag, WikiskillWikiTag}}
	if !isWikiScopeArticle(report) || !isReportArticle(report) {
		t.Error("a generated report is both wiki-scope and report-tagged")
	}
	if isReportArticle(&Article{Type: ContentTypeWiki, Tags: []string{WikiskillWikiTag}}) {
		t.Error("a plain wiki-scope article is not a report")
	}

	// The refusal guides to the declared alternative, names what was refused,
	// and only fires on wiki-scope articles.
	err := checkWikiScopeAccumulative(report, "delete")
	var scopeErr *WikiScopeError
	if !errors.As(err, &scopeErr) {
		t.Fatalf("expected *WikiScopeError, got %v", err)
	}
	if scopeErr.Slug != report.Slug || scopeErr.Action != "delete" {
		t.Errorf("error must carry slug and action, got %+v", scopeErr)
	}
	if !strings.Contains(scopeErr.Error(), "superseded_by") {
		t.Errorf("refusal must point at superseded_by as the alternative, got: %s", scopeErr.Error())
	}
	if err := checkWikiScopeAccumulative(&Article{Type: ContentTypeWiki, Slug: "plain"}, "delete"); err != nil {
		t.Errorf("a non-wiki-scope article must pass the guard, got %v", err)
	}
}

func TestWikiScopeDeleteAndRevertRefusedEditAllowed(t *testing.T) {
	srv := newMCPServer(t)

	resp := toolCall(t, srv, `{"name":"create_wiki_article","arguments":{"title":"Pattern Ledger","content":"# Pattern Ledger\n\nlesson one","tags":["wikiskill-wiki"],"edit_summary":"seed"}}`)
	if resp.IsError {
		t.Fatalf("an agent may set the marker on its own wiki article, refused: %s", resp.Content[0].Text)
	}
	art, err := srv.Storage.GetArticle("pattern-ledger")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if !hasTag(art.Tags, WikiskillWikiTag) {
		t.Fatalf("the set marker must stick, tags: %v", art.Tags)
	}

	// Deletion is refused: the wiki layer never resets.
	resp = toolCall(t, srv, `{"name":"delete_wiki_article","arguments":{"slug":"pattern-ledger"}}`)
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "wiki-scope") {
		t.Fatalf("agent deletion must be refused with the wiki-scope convention, got: %+v", resp)
	}
	if !strings.Contains(resp.Content[0].Text, "superseded_by") {
		t.Errorf("the refusal must offer superseded_by as the path forward, got: %s", resp.Content[0].Text)
	}
	if _, err := srv.Storage.GetArticle("pattern-ledger"); err != nil {
		t.Fatalf("the refused delete must not have removed the article: %v", err)
	}

	// A revert would roll back accumulated knowledge: refused too.
	resp = toolCall(t, srv, `{"name":"revert_article_version","arguments":{"slug":"pattern-ledger","version":1}}`)
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "wiki-scope") {
		t.Fatalf("agent revert must be refused with the wiki-scope convention, got: %+v", resp)
	}

	// Patching and appending stay open — that is the asymmetry.
	resp = toolCall(t, srv, `{"name":"edit_wiki_article","arguments":{"slug":"pattern-ledger","title":"Pattern Ledger","content":"# Pattern Ledger\n\nlesson one\n\nlesson two (appended)","loaded_version":1,"edit_summary":"append"}}`)
	if resp.IsError {
		t.Fatalf("agent edits to a wiki-scope article must succeed, got: %s", resp.Content[0].Text)
	}
	after, err := srv.Storage.GetArticle("pattern-ledger")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if after.Version != 2 || !strings.Contains(after.Content, "lesson two") {
		t.Errorf("the append must have landed as v2, got v%d: %q", after.Version, after.Content)
	}
	if !hasTag(after.Tags, WikiskillWikiTag) {
		t.Errorf("edits must keep the marker, tags: %v", after.Tags)
	}
}

func TestWikiScopeTagCannotBeStripped(t *testing.T) {
	srv := newMCPServer(t)
	s := srv.Storage
	art := seedWikiScopeArticle(t, s, "Guarded Ledger", "# guarded ledger")

	// MCP tool path: a tag edit that drops the marker is re-asserted.
	resp := toolCall(t, srv, fmt.Sprintf(`{"name":"update_article_tags","arguments":{"slug":%q,"tags":["misc"],"loaded_version":%d}}`, art.Slug, art.Version))
	if resp.IsError {
		t.Fatalf("update_article_tags failed: %s", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, WikiskillWikiTag) {
		t.Errorf("the response must show the re-asserted marker, got: %s", resp.Content[0].Text)
	}
	kept, err := s.GetArticle(art.Slug)
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if !hasTag(kept.Tags, WikiskillWikiTag) || !hasTag(kept.Tags, "misc") {
		t.Errorf("marker must survive and the legitimate tag must land, tags: %v", kept.Tags)
	}

	// The same rule at the storage choke point, for every other write path
	// (revert, import, a REST tag swap): a write that arrives without the
	// marker cannot unguard the article.
	rewritten, err := s.saveArticleLocked(kept.Slug, kept.Title, kept.Content, kept.Description,
		kept.Source, kept.Resource, "attempt to drop the marker", []string{"unrelated"}, ContentTypeWiki, ArticleOverrides{})
	if err != nil {
		t.Fatalf("storage save failed: %v", err)
	}
	if !hasTag(rewritten.Tags, WikiskillWikiTag) {
		t.Errorf("saveArticleLocked must re-assert the tool-managed wiki-scope marker, tags: %v", rewritten.Tags)
	}
}

func TestWikiScopeDeleteTagGloballyRefused(t *testing.T) {
	s := newLifecycleStorage(t)
	art := seedWikiScopeArticle(t, s, "Swept Ledger", "# swept ledger")

	// Stripping the marker globally would unguard every wiki-scope article at
	// once, so it is refused like the other tool-managed markers.
	if err := s.DeleteTagGlobally(WikiskillWikiTag); err == nil {
		t.Fatal("global deletion of the wiki-scope marker tag must be refused")
	} else if !strings.Contains(err.Error(), "tool-managed") {
		t.Fatalf("the refusal must name the convention, got: %v", err)
	}
	// Case-insensitive: the marker is not deletable under any spelling.
	if err := s.DeleteTagGlobally("WIKISKILL-WIKI"); err == nil {
		t.Fatal("the refusal must not be case-sensitive")
	}
	if live, err := s.GetArticle(art.Slug); err != nil || !hasTag(live.Tags, WikiskillWikiTag) {
		t.Fatalf("the refused sweep must leave the article guarded: %v %+v", err, live)
	}
}

func TestWikiScopeCleanupNeverAutoDeletes(t *testing.T) {
	s := newLifecycleStorage(t)
	// Two archived wiki articles past any retention window: one wiki-scope,
	// one an ordinary page. Only the ordinary page may go.
	guarded := seedWikiScopeArticle(t, s, "Old Pattern Page", "# old pattern", StatusArchived)
	backdateArchivedAt(t, s, guarded.Slug, 30)
	plain, err := s.SaveArticle("", "Old Plain Page", "# old plain", "", "", "", "seed", []string{StatusArchived}, ContentTypeWiki)
	if err != nil {
		t.Fatalf("seeding the control failed: %v", err)
	}
	backdateArchivedAt(t, s, plain.Slug, 30)

	t.Setenv("NEXWIKI_AUTO_DELETE_ARCHIVED_AFTER_DAYS", "1")
	if err := s.CleanupArchivedArticles(); err != nil {
		t.Fatalf("CleanupArchivedArticles failed: %v", err)
	}
	if _, err := s.GetArticle(guarded.Slug); err != nil {
		t.Fatalf("a wiki-scope article must never be auto-deleted: %v", err)
	}
	if _, err := s.GetArticle(plain.Slug); err == nil {
		t.Fatal("the control (an old archived plain page) should have been deleted")
	}
}

func TestWikiScopeReportCarriesMarker(t *testing.T) {
	s := newLifecycleStorage(t)
	job, report := endedReportFixture(t, s, "Marked Report Skill", "cancel")
	_ = job

	// The generated result report is wiki-layer content: both markers at
	// creation, and both survive a write that drops them.
	if !hasTag(report.Tags, SkillResultReportTag) || !hasTag(report.Tags, WikiskillWikiTag) {
		t.Fatalf("the report must carry both markers, tags: %v", report.Tags)
	}
	rewritten, err := s.saveArticleLocked(report.Slug, report.Title, report.Content, report.Description,
		report.Source, report.Resource, "attempt to drop the markers", []string{"misc"}, ContentTypeWiki, ArticleOverrides{})
	if err != nil {
		t.Fatalf("storage save failed: %v", err)
	}
	if !hasTag(rewritten.Tags, SkillResultReportTag) || !hasTag(rewritten.Tags, WikiskillWikiTag) {
		t.Errorf("saveArticleLocked must re-assert both markers, tags: %v", rewritten.Tags)
	}
	// And the wiki-scope guard holds it like any other wiki-layer page.
	if err := checkWikiScopeAccumulative(rewritten, "delete"); err == nil {
		t.Error("the report must refuse deletion as a wiki-scope article too")
	}
}

// --- WIKI layer: supersession is a declaration, not an action --------------

func TestSupersededByEditToolRoundTrip(t *testing.T) {
	srv := newMCPServer(t)
	s := srv.Storage
	seedWikiScopeArticle(t, s, "Evolution Ledger", "# ledger")
	successor := seedWikiScopeArticle(t, s, "Evolution Ledger Two", "# ledger two")

	// Set through the edit tool.
	resp := toolCall(t, srv, `{"name":"edit_wiki_article","arguments":{"slug":"evolution-ledger","title":"Evolution Ledger","content":"# Evolution Ledger","loaded_version":1,"superseded_by":"evolution-ledger-two"}}`)
	if resp.IsError {
		t.Fatalf("edit with superseded_by failed: %s", resp.Content[0].Text)
	}
	art, err := s.GetArticle("evolution-ledger")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if art.SupersededBy != successor.Slug {
		t.Fatalf("superseded_by = %q, want %q", art.SupersededBy, successor.Slug)
	}

	// It is real front matter and survives a reparse.
	raw, err := os.ReadFile(filepath.Join(s.ArticleDir, "evolution-ledger.md"))
	if err != nil {
		t.Fatalf("read article failed: %v", err)
	}
	if !strings.Contains(string(raw), "superseded_by: "+successor.Slug) {
		t.Errorf("front matter must carry superseded_by, got:\n%s", raw)
	}
	reparsed, err := parseArticleFile(raw, true)
	if err != nil {
		t.Fatalf("reparse failed: %v", err)
	}
	if reparsed.SupersededBy != successor.Slug {
		t.Errorf("superseded_by did not round-trip, got %q", reparsed.SupersededBy)
	}

	// Omitting it preserves; an empty string clears it — the same pointer
	// semantics stale_after already established.
	resp = toolCall(t, srv, `{"name":"edit_wiki_article","arguments":{"slug":"evolution-ledger","title":"Evolution Ledger","content":"# Evolution Ledger v3","loaded_version":2}}`)
	if resp.IsError {
		t.Fatalf("edit without superseded_by failed: %s", resp.Content[0].Text)
	}
	if kept, _ := s.GetArticle("evolution-ledger"); kept.SupersededBy != successor.Slug {
		t.Errorf("an edit that omits superseded_by must preserve it, got %q", kept.SupersededBy)
	}
	resp = toolCall(t, srv, `{"name":"edit_wiki_article","arguments":{"slug":"evolution-ledger","title":"Evolution Ledger","content":"# Evolution Ledger v4","loaded_version":3,"superseded_by":""}}`)
	if resp.IsError {
		t.Fatalf("clearing edit failed: %s", resp.Content[0].Text)
	}
	cleared, _ := s.GetArticle("evolution-ledger")
	if cleared.SupersededBy != "" {
		t.Errorf("superseded_by \"\" must clear the pointer, got %q", cleared.SupersededBy)
	}
	rawCleared, _ := os.ReadFile(filepath.Join(s.ArticleDir, "evolution-ledger.md"))
	if strings.Contains(string(rawCleared), "superseded_by:") {
		t.Errorf("the cleared key must not be serialized, got:\n%s", rawCleared)
	}
}

func TestSupersededByRestRoundTrip(t *testing.T) {
	srv := newTestServer(t)
	s := srv.Storage
	art := seedWikiScopeArticle(t, s, "Ledger Page", "# v1")
	successor := seedWikiScopeArticle(t, s, "Ledger Page Successor", "# v1 successor")

	// The human-facing REST editor sets the pointer too (the human amend path
	// the accumulative rule deliberately keeps open).
	put := func(body string) *Article {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/api/articles/"+art.Slug, strings.NewReader(body))
		req.SetPathValue("slug", art.Slug)
		w := httptest.NewRecorder()
		srv.HandleUpdateArticle(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("PUT superseded_by failed: %d %s", w.Code, w.Body.String())
		}
		fresh, err := s.GetArticle(art.Slug)
		if err != nil {
			t.Fatalf("GetArticle failed: %v", err)
		}
		return fresh
	}

	fresh := put(fmt.Sprintf(`{"title":%q,"content":%q,"loaded_version":%d,"superseded_by":%q}`,
		art.Title, art.Content, art.Version, successor.Slug))
	if fresh.SupersededBy != successor.Slug {
		t.Fatalf("REST superseded_by = %q, want %q", fresh.SupersededBy, successor.Slug)
	}

	// REST semantics mirror stale_after: only a non-empty value is applied, so
	// an empty body preserves rather than clears.
	fresh = put(fmt.Sprintf(`{"title":%q,"content":%q,"loaded_version":%d,"superseded_by":""}`,
		art.Title, art.Content, fresh.Version))
	if fresh.SupersededBy != successor.Slug {
		t.Errorf("REST empty superseded_by must preserve the pointer, got %q", fresh.SupersededBy)
	}

	// A free-text successor is slug-normalized like every pointer in this wiki.
	fresh = put(fmt.Sprintf(`{"title":%q,"content":%q,"loaded_version":%d,"superseded_by":"Ledger Page Successor"}`,
		art.Title, art.Content, fresh.Version))
	if fresh.SupersededBy != successor.Slug {
		t.Errorf("REST superseded_by must slug-normalize, got %q", fresh.SupersededBy)
	}

	// And a tags-only REST edit still cannot strip the marker.
	fresh = put(fmt.Sprintf(`{"title":%q,"content":%q,"loaded_version":%d,"tags":["misc"]}`,
		art.Title, art.Content, fresh.Version))
	if !hasTag(fresh.Tags, WikiskillWikiTag) {
		t.Errorf("REST tag edit must not strip the marker, tags: %v", fresh.Tags)
	}
}

func TestSupersededWikiScanCases(t *testing.T) {
	s := newLifecycleStorage(t)

	// An intact chain: the old page names its live successor. Reported so the
	// chain stays visible, but nothing to act on.
	oldArt := seedWikiScopeArticle(t, s, "Ledger Old", "# old")
	current := seedWikiScopeArticle(t, s, "Ledger Current", "# current")
	setSupersededBy(t, s, oldArt, current.Slug)

	// A broken link: the named successor does not exist.
	broken := seedWikiScopeArticle(t, s, "Ledger Broken", "# broken")
	setSupersededBy(t, s, broken, "ledger-never-written")

	// A superseded tag with no successor pointer: the chain stops here.
	taggedOnly := seedWikiScopeArticle(t, s, "Ledger Tagged", "# tagged", "superseded")

	// Out of scope by construction: the chain check is about the wiki layer,
	// so a plain page carrying the same pointer is not the chain's business.
	plain, err := s.SaveArticle("", "Plain Superseded", "# plain", "", "", "", "seed", nil, ContentTypeWiki)
	if err != nil {
		t.Fatalf("seeding the plain page failed: %v", err)
	}
	setSupersededBy(t, s, plain, current.Slug)

	bySlug, actionable := supersededBySlug(t, s)
	if len(bySlug) != 3 {
		t.Fatalf("expected 3 chain findings, got %d: %+v", len(bySlug), bySlug)
	}
	if actionable != 2 {
		t.Fatalf("expected exactly the broken and tag-only links actionable, got %d: %+v", actionable, bySlug)
	}

	// Intact: listed with the successor named, and marked as never-pruned.
	if f, ok := bySlug[oldArt.Slug]; !ok {
		t.Errorf("the intact chain link must be listed, got %+v", bySlug)
	} else {
		if !strings.Contains(f.Detail, "/articles/"+current.Slug) {
			t.Errorf("the intact finding must name the successor, got: %s", f.Detail)
		}
		if !strings.Contains(f.Detail, "Nothing is pruned automatically") {
			t.Errorf("the intact finding must say nothing is auto-pruned, got: %s", f.Detail)
		}
	}
	// Broken: actionable, naming the missing successor.
	if f, ok := bySlug[broken.Slug]; !ok {
		t.Errorf("the broken chain link must be listed, got %+v", bySlug)
	} else if !strings.Contains(f.Detail, "does not exist") || !strings.Contains(f.Detail, "ledger-never-written") {
		t.Errorf("the broken finding must name the missing successor, got: %s", f.Detail)
	}
	// Tag-only: actionable, pointing at the fix.
	if f, ok := bySlug[taggedOnly.Slug]; !ok {
		t.Errorf("the superseded-tag-without-successor case must be listed, got %+v", bySlug)
	} else if !strings.Contains(f.Detail, "superseded_by") {
		t.Errorf("the tag-only finding must name the superseded_by fix, got: %s", f.Detail)
	}
	if _, ok := bySlug[plain.Slug]; ok {
		t.Errorf("a non-wiki-scope article is not the wiki layer's chain, got: %+v", bySlug[plain.Slug])
	}

	// An archived wiki-scope page is deliberately out of every health check,
	// including this one: archiving is a human decision, and reporting a
	// decision is noise.
	retired := seedWikiScopeArticle(t, s, "Ledger Retired", "# retired", StatusArchived)
	setSupersededBy(t, s, retired, current.Slug)
	bySlug, actionable = supersededBySlug(t, s)
	if len(bySlug) != 3 || actionable != 2 {
		t.Fatalf("archived pages must stay out of the chain report, got %d findings / %d actionable: %+v", len(bySlug), actionable, bySlug)
	}
	if _, ok := bySlug[retired.Slug]; ok {
		t.Errorf("an archived wiki-scope page must not be reported, got: %+v", bySlug[retired.Slug])
	}
}

// TestWikiHealthSupersededAndPurposeFields pins the two new wiki_health
// categories end to end through the tool: the supersession chain is reported
// with its actionable count, and the trained skill's missing PURPOSE link is
// reported as a warning.
func TestWikiHealthSupersededAndPurposeFields(t *testing.T) {
	srv := newMCPServer(t)
	s := srv.Storage

	oldArt := seedWikiScopeArticle(t, s, "Chain Ledger", "# old")
	current := seedWikiScopeArticle(t, s, "Chain Ledger Two", "# current")
	setSupersededBy(t, s, oldArt, current.Slug)
	broken := seedWikiScopeArticle(t, s, "Chain Ledger Broken", "# broken")
	setSupersededBy(t, s, broken, "chain-ledger-never-written")

	skill := seedSkill(t, s, "Chain Unlinked Skill", "# v1 body")
	promoted, _ := promoteCandidate(t, s, skill, "# v2 trained body")

	out := healthReport(t, srv, `{}`)
	if out.SupersededWikiCount != 2 || out.SupersededWikiActionable != 1 {
		t.Fatalf("superseded chain wrong: count=%d actionable=%d findings=%+v", out.SupersededWikiCount, out.SupersededWikiActionable, out.SupersededWikiArticles)
	}
	if out.PurposeBacklinkCount != 1 || len(out.PurposeBacklinkFind) != 1 {
		t.Fatalf("purpose backlink wrong: count=%d findings=%+v", out.PurposeBacklinkCount, out.PurposeBacklinkFind)
	}
	if out.PurposeBacklinkFind[0].Slug != promoted.Slug {
		t.Errorf("purpose finding must name the trained skill, got %+v", out.PurposeBacklinkFind[0])
	}
	prose := out.Content()
	for _, want := range []string{
		"Raw artifact integrity issues (WikiSkill evolution data): 0",
		"Superseded wiki-scope articles (reported, never auto-pruned): 2",
		"Trained skills without pattern backlinks (warning): 1",
	} {
		if !strings.Contains(prose, want) {
			t.Errorf("prose is missing %q:\n%s", want, prose)
		}
	}
}

// --- SKILLS layer: purpose-linked, reversible ----------------------------------------------

func TestPurposeBacklinkScanCases(t *testing.T) {
	t.Run("unlinked trained skill is flagged", func(t *testing.T) {
		s := newLifecycleStorage(t)
		skill := seedSkill(t, s, "Purpose Skill", "# v1 body")
		promoted, _ := promoteCandidate(t, s, skill, "# v2 trained body")
		flagged := purposeFlagged(t, s)
		if !flagged[promoted.Slug] {
			t.Fatalf("a trained skill with no pattern link must be flagged, got %v", flagged)
		}
	})

	t.Run("outbound skill to pattern link suppresses", func(t *testing.T) {
		s := newLifecycleStorage(t)
		pattern := seedWikiScopeArticle(t, s, "Ledger Pattern", "# pattern ledger")
		skill := seedSkill(t, s, "Outbound Purpose Skill", "# v1 body")
		promoted, _ := promoteCandidate(t, s, skill, "# v2 trained body")
		if flagged := purposeFlagged(t, s); !flagged[promoted.Slug] {
			t.Fatalf("the skill must be flagged before it records a purpose, got %v", flagged)
		}
		// The skill's PURPOSE section links the pattern that drove it.
		if _, err := s.ApplyArticleEdit(promoted.Slug, ArticleEdit{
			Title:         promoted.Title,
			Content:       promoted.Content + "\n\nPurpose: follow [the pattern ledger](/articles/" + pattern.Slug + ").",
			LoadedVersion: promoted.Version,
		}); err != nil {
			t.Fatalf("linking the pattern failed: %v", err)
		}
		if flagged := purposeFlagged(t, s); flagged[promoted.Slug] {
			t.Fatalf("an outbound purpose link must suppress the warning, got %v", flagged)
		}
	})

	t.Run("inbound pattern to skill link suppresses", func(t *testing.T) {
		s := newLifecycleStorage(t)
		skill := seedSkill(t, s, "Inbound Purpose Skill", "# v1 body")
		promoted, _ := promoteCandidate(t, s, skill, "# v2 trained body")
		// A link from an ordinary wiki page is not recorded motivation: the
		// wiki layer is where patterns live.
		plain, err := s.SaveArticle("", "Inbound Plain Page", "# plain", "", "", "", "seed", nil, ContentTypeWiki)
		if err != nil {
			t.Fatalf("seeding plain page failed: %v", err)
		}
		if _, err := s.ApplyArticleEdit(plain.Slug, ArticleEdit{
			Title:         plain.Title,
			Content:       plain.Content + "\n\nSee [the skill](/articles/" + promoted.Slug + ").",
			LoadedVersion: plain.Version,
		}); err != nil {
			t.Fatalf("linking from the plain page failed: %v", err)
		}
		if flagged := purposeFlagged(t, s); !flagged[promoted.Slug] {
			t.Fatalf("only a wiki-scope pattern link counts, got %v", flagged)
		}
		// A pattern backlink proper — what get_backlinks returns — counts.
		pattern := seedWikiScopeArticle(t, s, "Backlink Pattern", "# pattern")
		if _, err := s.ApplyArticleEdit(pattern.Slug, ArticleEdit{
			Title:         pattern.Title,
			Content:       pattern.Content + "\n\nDriven by [the skill](/articles/" + promoted.Slug + ").",
			LoadedVersion: pattern.Version,
		}); err != nil {
			t.Fatalf("linking from the pattern failed: %v", err)
		}
		if flagged := purposeFlagged(t, s); flagged[promoted.Slug] {
			t.Fatalf("an inbound pattern link must suppress the warning, got %v", flagged)
		}
	})

	t.Run("report link does not suppress", func(t *testing.T) {
		s := newLifecycleStorage(t)
		skill := seedSkill(t, s, "Report Purpose Skill", "# v1 body")
		promoted, _ := promoteCandidate(t, s, skill, "# v2 trained body")
		// The run's generated result report links the skill in its Run table —
		// but every run produces one, so it is boilerplate, not motivation.
		job, _, err := s.CreateEvolutionJob(promoted.Slug, "", "opencode")
		if err != nil {
			t.Fatalf("CreateEvolutionJob failed: %v", err)
		}
		if _, err := s.CancelEvolutionJob(job.ID, "operator stopped it"); err != nil {
			t.Fatalf("CancelEvolutionJob failed: %v", err)
		}
		report, err := s.EnsureSkillResultReport(job.ID)
		if err != nil || report == nil {
			t.Fatalf("EnsureSkillResultReport failed: %v %v", report, err)
		}
		if !strings.Contains(report.Content, "/articles/"+promoted.Slug) {
			t.Fatalf("fixture broken: the report must link its skill, got %q", report.Content)
		}
		if flagged := purposeFlagged(t, s); !flagged[promoted.Slug] {
			t.Fatalf("the report's own link must not count as a purpose link, got %v", flagged)
		}
	})

	t.Run("untrained skill is not flagged", func(t *testing.T) {
		s := newLifecycleStorage(t)
		seedSkill(t, s, "Untrained Purpose Skill", "# fresh body")
		seedWikiScopeArticle(t, s, "Purpose Ledger", "# ledger")
		if flagged := purposeFlagged(t, s); len(flagged) != 0 {
			t.Fatalf("untrained skills are outside the evolution flow and must not be flagged, got %v", flagged)
		}
	})
}

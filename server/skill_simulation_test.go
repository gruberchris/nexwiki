package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Story 11 (B4) tests: the pre-live simulation. The computation (gate math
// without gating), the immutable hash-linked trail, every refusal class, and
// the REST shapes.

// simFixture seeds a skill, a queued job, an eval set with a sandboxed overlap
// scorer, and one pending candidate whose body covers the val vocabulary.
// Returns the job, token, candidate, and the eval hash.
func simFixture(t *testing.T) (*Server, *EvolutionJob, string, *SkillCandidate, string) {
	t.Helper()
	srv := newTestServer(t)
	if _, err := srv.Storage.SaveArticle("", "Sim Skill", "# v1 body", "scores eval cases for the simulation tests", "", "", "", []string{"aiagent-skill"}, ContentTypeSkill); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	job, token, err := srv.Storage.CreateEvolutionJob("sim-skill", "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	t.Setenv(EvalMinTrainEnv, "1")
	t.Setenv(EvalMinValEnv, "1")
	const scorer = `overlap(expected, output) >= 0.5`
	content := `{"train":[{"input":"train one","expected":"alpha","scorer":"` + scorer + `"},{"input":"train two","expected":"omega","scorer":"` + scorer + `"}],"val":[{"input":"val one","expected":"alpha","scorer":"` + scorer + `"},{"input":"val two","expected":"gamma","scorer":"` + scorer + `"}]}`
	if _, err := srv.Storage.UploadEvolutionEvalSet(job.ID, token, "eval.json", content); err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	cand, err := srv.Storage.CreateSkillCandidate("sim-skill", "# v2 body alpha gamma", "--- a/x\n+++ b/x", []string{"p1"}, "proposer-harness")
	if err != nil {
		t.Fatalf("candidate failed: %v", err)
	}
	hash, err := srv.Storage.latestEvalHashForSkill("sim-skill")
	if err != nil || hash == "" {
		t.Fatalf("eval hash missing: %v", err)
	}
	return srv, job, token, cand, hash
}

func TestSkillSimulationWithoutGating(t *testing.T) {
	srv, job, _, cand, evalHash := simFixture(t)

	rec, err := srv.Storage.RunSkillSimulation(job.ID, "")
	if err != nil {
		t.Fatalf("RunSkillSimulation failed: %v", err)
	}
	if rec.JobID != job.ID || rec.SkillSlug != "sim-skill" {
		t.Errorf("record identity wrong: %+v", rec)
	}
	if rec.Sequence != 1 {
		t.Errorf("first simulation sequence = %d, want 1", rec.Sequence)
	}
	if rec.CandidateID != cand.ID || rec.CandidateParentVersion != cand.ParentVersion {
		t.Errorf("candidate identity lost: %+v", rec)
	}
	if rec.CandidateHash != SkillCandidateContentHash(cand) {
		t.Errorf("candidate content hash lost: %q", rec.CandidateHash)
	}
	if rec.EvalHash != evalHash {
		t.Errorf("eval hash = %q, want %q", rec.EvalHash, evalHash)
	}
	if rec.ScorerVersion == "" || rec.ScorerVersion == SkillAuditScorerVersion {
		t.Errorf("scorer version must be the eval set's derived one, got %q", rec.ScorerVersion)
	}
	if rec.ValCases != 2 || rec.ValCasesCapped {
		t.Errorf("val cases = %d capped=%v, want 2", rec.ValCases, rec.ValCasesCapped)
	}
	// The body covers both val cases: 1.0 resolution, 2 passes. Per-case rows
	// carry previews and the same pass flag.
	if rec.ResolutionRate != 1 {
		t.Errorf("resolution rate = %v, want 1", rec.ResolutionRate)
	}
	if rec.PassCount != 2 || len(rec.Cases) != 2 {
		t.Fatalf("per-case rows wrong: pass=%d rows=%d", rec.PassCount, len(rec.Cases))
	}
	for i, c := range rec.Cases {
		if c.Index != i || c.Score != 1 || !c.Pass || c.InputPreview == "" || c.ExpectedPreview == "" {
			t.Errorf("case row %d wrong: %+v", i, c)
		}
	}
	// The trail is stamped on the job record.
	stored, err := srv.Storage.GetEvolutionJob(job.ID)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if stored.SimulationCount != 1 || stored.LatestSimulationHash != rec.ResultHash {
		t.Errorf("job trail stamp wrong: %+v", stored)
	}
	// Nothing gated: the job is still queued and no audit record exists.
	if stored.Status != EvolutionJobQueued {
		t.Errorf("simulation must not change the job status: %q", stored.Status)
	}
	audit, _ := srv.Storage.ListSkillAuditRecords()
	if len(audit) != 0 {
		t.Errorf("a simulation is not a gate decision — no audit record may land: %+v", audit)
	}
}

func TestSkillSimulationResolutionRateMatchesGateMath(t *testing.T) {
	srv, job, _, cand, _ := simFixture(t)
	rec, err := srv.Storage.RunSkillSimulation(job.ID, "")
	if err != nil {
		t.Fatalf("RunSkillSimulation failed: %v", err)
	}
	// The resolution rate is exactly what the gate's val-split scorer produces
	// for the same body — same set, same rounding.
	gate, err := srv.Storage.EvalGateForSkill("sim-skill")
	if err != nil {
		t.Fatalf("EvalGateForSkill failed: %v", err)
	}
	if got := gate.ValScore(cand.ProposedBody); got != rec.ResolutionRate {
		t.Errorf("resolution rate %v != gate val score %v", rec.ResolutionRate, got)
	}
}

func TestSkillSimulationImmutableHashChain(t *testing.T) {
	srv, job, _, _, _ := simFixture(t)

	first, err := srv.Storage.RunSkillSimulation(job.ID, "")
	if err != nil {
		t.Fatalf("first simulation failed: %v", err)
	}
	rawFirst, err := os.ReadFile(filepath.Join(srv.Storage.JobDir, job.ID+".files", "simulations", "sim-001.json"))
	if err != nil {
		t.Fatalf("record missing: %v", err)
	}
	if !strings.Contains(string(rawFirst), "result_hash") {
		t.Error("the record must carry its result hash")
	}

	// Re-running the identical simulation appends a new record — history is
	// kept, like the append-only audit trail — and the chain links.
	second, err := srv.Storage.RunSkillSimulation(job.ID, "")
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	if second.Sequence != 2 || second.PrevHash != first.ResultHash {
		t.Fatalf("chain link wrong: prev=%q want=%q", second.PrevHash, first.ResultHash)
	}
	if second.ResultHash == first.ResultHash {
		t.Error("two records must never share a result hash (computed_at differs)")
	}
	rawFirst2, _ := os.ReadFile(filepath.Join(srv.Storage.JobDir, job.ID+".files", "simulations", "sim-001.json"))
	if string(rawFirst) != string(rawFirst2) {
		t.Error("the first record must be byte-identical after later runs (immutable)")
	}

	// The hashes reproduce from the records themselves: hash the stored record
	// with result_hash cleared and it must regenerate.
	probe := *second
	probe.ResultHash = ""
	raw, _ := json.Marshal(probe)
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != second.ResultHash {
		t.Errorf("result hash does not regenerate: %q", second.ResultHash)
	}
	recs, err := srv.Storage.ListSkillSimulations(job.ID)
	if err != nil || len(recs) != 2 {
		t.Fatalf("listing wrong: %d %v", len(recs), err)
	}
}

func TestSkillSimulationRefusals(t *testing.T) {
	srv, job, token, cand, _ := simFixture(t)

	// In-flight run: refused so the simulation never describes a state that is
	// about to change under the live gate.
	if _, err := srv.Storage.ClaimEvolutionJob(job.ID, token, "runner"); err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if _, err := srv.Storage.RunSkillSimulation(job.ID, ""); err == nil || !strings.Contains(err.Error(), "in flight") {
		t.Errorf("a claimed job must refuse, got %v", err)
	}
	if _, err := srv.Storage.CancelEvolutionJob(job.ID, "sim refusal fixtures"); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}

	// A terminal job simulates freely (the record trail is append-only).
	if _, err := srv.Storage.RunSkillSimulation(job.ID, cand.ID); err != nil {
		t.Errorf("a terminal job must simulate: %v", err)
	}

	// No eval set at all.
	bare := seedSkill(t, srv.Storage, "Bare Sim Skill", "# bare")
	job2, _, err := srv.Storage.CreateEvolutionJob(bare.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, err := srv.Storage.RunSkillSimulation(job2.ID, ""); err == nil || !strings.Contains(err.Error(), "no eval set stored") {
		t.Errorf("a job without eval data must refuse, got %v", err)
	}

	// No pending candidate anywhere.
	quiet := seedSkill(t, srv.Storage, "Quiet Sim Skill", "# quiet")
	job3, token3, err := srv.Storage.CreateEvolutionJob(quiet.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	const scorer = `overlap(expected, output) >= 0.5`
	content := `{"train":[{"input":"t","expected":"alpha","scorer":"` + scorer + `"}],"val":[{"input":"v","expected":"gamma","scorer":"` + scorer + `"}]}`
	if _, err := srv.Storage.UploadEvolutionEvalSet(job3.ID, token3, "eval.json", content); err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	if _, err := srv.Storage.RunSkillSimulation(job3.ID, ""); err == nil || !strings.Contains(err.Error(), "no pending candidate") {
		t.Errorf("a job with no pending candidate must refuse, got %v", err)
	}

	// A decided candidate cannot simulate.
	if _, err := srv.Storage.CreateSkillCandidate(quiet.Slug, "# body alpha", "", nil, "harness"); err != nil {
		t.Fatalf("candidate failed: %v", err)
	}
	pending, err := srv.Storage.CreateSkillCandidate(quiet.Slug, "# body alpha gamma", "", nil, "harness")
	if err != nil {
		t.Fatalf("candidate failed: %v", err)
	}
	if _, err := srv.Storage.RejectSkillCandidate(pending.ID); err != nil {
		t.Fatalf("reject failed: %v", err)
	}
	if _, err := srv.Storage.RunSkillSimulation(job3.ID, pending.ID); err == nil || !strings.Contains(err.Error(), "only pending candidates simulate") {
		t.Errorf("a decided candidate must refuse, got %v", err)
	}

	// A candidate from another skill is refused.
	other := seedSkill(t, srv.Storage, "Other Sim Skill", "# other")
	otherCand, err := srv.Storage.CreateSkillCandidate(other.Slug, "# other body", "", nil, "harness")
	if err != nil {
		t.Fatalf("candidate failed: %v", err)
	}
	if _, err := srv.Storage.RunSkillSimulation(job3.ID, otherCand.ID); err == nil || !strings.Contains(err.Error(), "must run a candidate of this job's skill") {
		t.Errorf("a cross-skill candidate must refuse, got %v", err)
	}

	// A diff-only candidate carries no body to score.
	diffOnly, err := srv.Storage.CreateSkillCandidate(quiet.Slug, "", "--- a/x\n+++ b/x", nil, "harness")
	if err != nil {
		t.Fatalf("candidate failed: %v", err)
	}
	if _, err := srv.Storage.RunSkillSimulation(job3.ID, diffOnly.ID); err == nil || !strings.Contains(err.Error(), "no body to score") {
		t.Errorf("a diff-only candidate must refuse, got %v", err)
	}
}

func TestHandleRunEvolutionSimulation(t *testing.T) {
	srv, job, token, cand, _ := simFixture(t)

	// Empty body: the newest pending candidate.
	req := httptest.NewRequest("POST", "/api/evolution/jobs/"+job.ID+"/simulate", nil)
	req.SetPathValue("id", job.ID)
	w := httptest.NewRecorder()
	srv.HandleRunEvolutionSimulation(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var rec SkillSimulationRecord
	if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if rec.CandidateID != cand.ID || rec.ResolutionRate != 1 || len(rec.Cases) != 2 {
		t.Errorf("simulation body wrong: %+v", rec)
	}

	// Named candidate through the body.
	body, _ := json.Marshal(map[string]string{"candidate_id": cand.ID})
	req2 := httptest.NewRequest("POST", "/api/evolution/jobs/"+job.ID+"/simulate", strings.NewReader(string(body)))
	req2.SetPathValue("id", job.ID)
	w2 := httptest.NewRecorder()
	srv.HandleRunEvolutionSimulation(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("named candidate: expected 200, got %d", w2.Code)
	}

	// Unknown job reads 404; a claimed job reads 409.
	req3 := httptest.NewRequest("POST", "/api/evolution/jobs/nope/simulate", nil)
	req3.SetPathValue("id", "nope")
	w3 := httptest.NewRecorder()
	srv.HandleRunEvolutionSimulation(w3, req3)
	if w3.Code != http.StatusNotFound {
		t.Errorf("unknown job: expected 404, got %d", w3.Code)
	}
	if _, err := srv.Storage.ClaimEvolutionJob(job.ID, token, "runner"); err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	req4 := httptest.NewRequest("POST", "/api/evolution/jobs/"+job.ID+"/simulate", nil)
	req4.SetPathValue("id", job.ID)
	w4 := httptest.NewRecorder()
	srv.HandleRunEvolutionSimulation(w4, req4)
	if w4.Code != http.StatusConflict {
		t.Errorf("in-flight job: expected 409, got %d", w4.Code)
	}

	// The history endpoint lists both records, oldest first.
	srv.Storage.CancelEvolutionJob(job.ID, "fixture cleanup")
	req5 := httptest.NewRequest("GET", "/api/evolution/jobs/"+job.ID+"/simulations", nil)
	req5.SetPathValue("id", job.ID)
	w5 := httptest.NewRecorder()
	srv.HandleListEvolutionSimulations(w5, req5)
	if w5.Code != http.StatusOK {
		t.Fatalf("history: expected 200, got %d", w5.Code)
	}
	var history []SkillSimulationRecord
	if err := json.Unmarshal(w5.Body.Bytes(), &history); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(history) != 2 || history[0].Sequence != 1 || history[1].Sequence != 2 {
		t.Errorf("history wrong: %+v", history)
	}

	// Unknown job history reads 404.
	req6 := httptest.NewRequest("GET", "/api/evolution/jobs/nope/simulations", nil)
	req6.SetPathValue("id", "nope")
	w6 := httptest.NewRecorder()
	srv.HandleListEvolutionSimulations(w6, req6)
	if w6.Code != http.StatusNotFound {
		t.Errorf("unknown job history: expected 404, got %d", w6.Code)
	}
}

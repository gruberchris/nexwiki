package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// This file tests the story 08 wizard REST seam in skill_wizard_http.go:
// the read-mostly job/loop/eval/candidate endpoints and the three audited
// human loop controls. The storage pieces underneath are the stories 01-07
// records; these tests pin the HTTP shapes the browser consumes.

// wizardTestServer builds a server with one skill and one evolution job for
// it, returning the job ID and token for the eval/loop fixtures.
func wizardTestServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	srv := newTestServer(t)
	if _, err := srv.Storage.SaveArticle("", "Stability Protocol", "# Stability\n\nA skill for test rounds.", "", "", "", "", []string{"aiagent-skill"}, ContentTypeSkill); err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	job, token, err := srv.Storage.CreateEvolutionJob("stability-protocol", "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	return srv, job.ID, token
}

func TestHandleGetSkillTrainedState(t *testing.T) {
	srv, _, _ := wizardTestServer(t)

	req := httptest.NewRequest("GET", "/api/skills/stability-protocol/trained-state", nil)
	req.SetPathValue("slug", "stability-protocol")
	w := httptest.NewRecorder()
	srv.HandleGetSkillTrainedState(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var state SkillTrainedStateEntry
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if state.Slug != "stability-protocol" || state.State != TrainedStateUntrained {
		t.Errorf("expected untrained stability-protocol, got %s/%s", state.Slug, state.State)
	}

	// A slug that is not a skill reads 404, not 500.
	_, _ = srv.Storage.SaveArticle("", "Plain Article", "# content", "", "", "", "", nil, ContentTypeWiki)
	req2 := httptest.NewRequest("GET", "/api/skills/plain-article/trained-state", nil)
	req2.SetPathValue("slug", "plain-article")
	w2 := httptest.NewRecorder()
	srv.HandleGetSkillTrainedState(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("non-skill slug: expected 404, got %d", w2.Code)
	}
}

func TestHandleListAndGetEvolutionJobs(t *testing.T) {
	srv, jobID, _ := wizardTestServer(t)

	req := httptest.NewRequest("GET", "/api/evolution/jobs", nil)
	w := httptest.NewRecorder()
	srv.HandleListEvolutionJobs(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var jobs []EvolutionJobView
	if err := json.Unmarshal(w.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != jobID {
		t.Fatalf("expected exactly the test job, got %d jobs", len(jobs))
	}
	if jobs[0].SkillSlug != "stability-protocol" || jobs[0].Status != EvolutionJobQueued {
		t.Errorf("job view fields lost: %+v", jobs[0])
	}
	// The withholding must hold at the wire, not just in the decoded struct:
	// the raw body carries no token_hash key anywhere (a json:"-" shadow on an
	// embedded record would not achieve this).
	if strings.Contains(w.Body.String(), "token_hash") {
		t.Errorf("list body must not carry token_hash: %s", w.Body.String())
	}

	// The skill filter narrows to the same job.
	req2 := httptest.NewRequest("GET", "/api/evolution/jobs?skill=stability-protocol", nil)
	w2 := httptest.NewRecorder()
	srv.HandleListEvolutionJobs(w2, req2)
	var filtered []EvolutionJobView
	if err := json.Unmarshal(w2.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("expected 1 job for the skill filter, got %d", len(filtered))
	}

	req3 := httptest.NewRequest("GET", "/api/evolution/jobs?skill=unknown-skill", nil)
	w3 := httptest.NewRecorder()
	srv.HandleListEvolutionJobs(w3, req3)
	var none []EvolutionJobView
	if err := json.Unmarshal(w3.Body.Bytes(), &none); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 jobs for an unknown skill, got %d", len(none))
	}

	// Single-job GET: same view, token hash withheld.
	req4 := httptest.NewRequest("GET", "/api/evolution/jobs/"+jobID, nil)
	req4.SetPathValue("id", jobID)
	w4 := httptest.NewRecorder()
	srv.HandleGetEvolutionJob(w4, req4)
	if w4.Code != http.StatusOK {
		t.Fatalf("get job: expected 200, got %d", w4.Code)
	}
	var got EvolutionJobView
	if err := json.Unmarshal(w4.Body.Bytes(), &got); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if got.ID != jobID || got.SkillSlug != "stability-protocol" || got.Status != EvolutionJobQueued {
		t.Errorf("unexpected job view: %+v", got)
	}
	if strings.Contains(w4.Body.String(), "token_hash") {
		t.Errorf("get body must not carry token_hash: %s", w4.Body.String())
	}

	// Unknown job reads 404.
	req5 := httptest.NewRequest("GET", "/api/evolution/jobs/nope", nil)
	req5.SetPathValue("id", "nope")
	w5 := httptest.NewRecorder()
	srv.HandleGetEvolutionJob(w5, req5)
	if w5.Code != http.StatusNotFound {
		t.Errorf("unknown job: expected 404, got %d", w5.Code)
	}
}

func TestHandleGetEvolutionLoop(t *testing.T) {
	srv, jobID, _ := wizardTestServer(t)

	// No loop state yet: the response is a valid zero-position body, not an error.
	req := httptest.NewRequest("GET", "/api/evolution/jobs/"+jobID+"/loop", nil)
	req.SetPathValue("id", jobID)
	w := httptest.NewRecorder()
	srv.HandleGetEvolutionLoop(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp EvolutionLoopResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if resp.Loop != nil || len(resp.Iterations) != 0 {
		t.Errorf("expected empty loop position, got %+v", resp)
	}
	if resp.MaxIterations != evolutionJobMaxIterations || resp.PlateauLimit != evolutionPlateauDefault {
		t.Errorf("stop rules missing from response: max=%d plateau=%d", resp.MaxIterations, resp.PlateauLimit)
	}

	// Seed one iteration record and a loop state; they must round-trip.
	if _, err := srv.Storage.mutateLoopState(jobID, true, func(st *LoopState) {
		st.CurrentIteration = 2
		st.CurrentPhase = LoopPhaseProposing
		st.PhaseStatus = LoopStatusRunning
	}); err != nil {
		t.Fatalf("mutateLoopState failed: %v", err)
	}
	if err := srv.Storage.writeIterationRecordLocked(&IterationRecord{
		JobID: jobID, SkillSlug: "stability-protocol", Iteration: 1,
		StartedAt: time.Now().UTC(), Outcome: IterationOutcomeRejected,
		ValScore: 0.4, RBest: 0.5,
	}); err != nil {
		t.Fatalf("writeIterationRecordLocked failed: %v", err)
	}

	w2 := httptest.NewRecorder()
	srv.HandleGetEvolutionLoop(w2, req)
	var resp2 EvolutionLoopResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if resp2.Loop == nil || resp2.Loop.CurrentIteration != 2 || resp2.Loop.CurrentPhase != LoopPhaseProposing {
		t.Errorf("loop state lost: %+v", resp2.Loop)
	}
	if len(resp2.Iterations) != 1 || resp2.Iterations[0].Outcome != IterationOutcomeRejected {
		t.Errorf("iteration records lost: %+v", resp2.Iterations)
	}
	if resp2.PlateauCount != 1 {
		t.Errorf("expected plateau count 1, got %d", resp2.PlateauCount)
	}
}

func TestHandleGetEvolutionEvalAndCandidate(t *testing.T) {
	srv, jobID, token := wizardTestServer(t)

	// No upload yet: 404 is the waiting state.
	req := httptest.NewRequest("GET", "/api/evolution/jobs/"+jobID+"/eval", nil)
	req.SetPathValue("id", jobID)
	w := httptest.NewRecorder()
	srv.HandleGetEvolutionEval(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 before upload, got %d", w.Code)
	}

	// Upload through the real story-05 gate with the eval tests' small thresholds.
	t.Setenv(EvalMinTrainEnv, "6")
	t.Setenv(EvalMinValEnv, "2")
	if _, err := srv.Storage.UploadEvolutionEvalSet(jobID, token, "cases.json", evalCasesJSON(evalFixture(8))); err != nil {
		t.Fatalf("UploadEvolutionEvalSet failed: %v", err)
	}
	w2 := httptest.NewRecorder()
	srv.HandleGetEvolutionEval(w2, req)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 after upload, got %d", w2.Code)
	}
	var meta EvalMeta
	if err := json.Unmarshal(w2.Body.Bytes(), &meta); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if meta.TrainCount != 6 || meta.ValCount != 2 || meta.EvalHash == "" || meta.ScorerVersion != SkillAuditScorerVersion {
		t.Errorf("eval meta incomplete: %+v", meta)
	}
	if meta.BaselineS0 < 0 || meta.BaselineS0 > 1 {
		t.Errorf("S0 baseline = %v, want in [0,1]", meta.BaselineS0)
	}
	if len(meta.DryRun) != 2 || meta.Estimate.BudgetEnforced {
		t.Errorf("dry run/estimate wrong: dry=%d enforced=%v", len(meta.DryRun), meta.Estimate.BudgetEnforced)
	}

	// Candidate endpoint round-trips the record the iteration card renders.
	cand, err := srv.Storage.CreateSkillCandidate("stability-protocol", "# proposed\nnew body", "--- a/skill\n+++ b/skill\n+new", []string{"pattern-one"}, "harness")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	req2 := httptest.NewRequest("GET", "/api/evolution/candidates/"+cand.ID, nil)
	req2.SetPathValue("id", cand.ID)
	w3 := httptest.NewRecorder()
	srv.HandleGetEvolutionCandidate(w3, req2)
	if w3.Code != http.StatusOK {
		t.Fatalf("candidate: expected 200, got %d", w3.Code)
	}
	var got SkillCandidate
	if err := json.Unmarshal(w3.Body.Bytes(), &got); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if got.ID != cand.ID || got.ParentSlug != "stability-protocol" || got.Diff == "" {
		t.Errorf("candidate record lost: %+v", got)
	}
	if len(got.PatternSlugs) != 1 || got.PatternSlugs[0] != "pattern-one" {
		t.Errorf("pattern slugs lost: %+v", got.PatternSlugs)
	}
}

func TestHandleEvolutionLoopControls(t *testing.T) {
	srv, jobID, _ := wizardTestServer(t)

	// Pause: a queued job with no driver parks immediately and the control is
	// audited by the story 07 wrapper.
	req := httptest.NewRequest("POST", "/api/evolution/jobs/"+jobID+"/pause", strings.NewReader(`{"checkpoint":"before val upload"}`))
	req.SetPathValue("id", jobID)
	w := httptest.NewRecorder()
	srv.HandlePauseEvolutionJob(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("pause: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var paused EvolutionJobView
	if err := json.Unmarshal(w.Body.Bytes(), &paused); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if paused.Status != EvolutionJobPaused || paused.Checkpoint != "before val upload" {
		t.Errorf("pause did not park the job: %+v", paused)
	}

	// An empty body is a valid control call.
	reqEmpty := httptest.NewRequest("POST", "/api/evolution/jobs/"+jobID+"/pause", strings.NewReader(""))
	reqEmpty.SetPathValue("id", jobID)
	wEmpty := httptest.NewRecorder()
	srv.HandlePauseEvolutionJob(wEmpty, reqEmpty)
	if wEmpty.Code != http.StatusOK {
		t.Errorf("empty-body pause: expected 200, got %d", wEmpty.Code)
	}

	// Pause on a terminal job reads 409.
	srv, jobID2, _ := wizardTestServer(t)
	_, _ = srv.Storage.CancelEvolutionJob(jobID2, "test")
	reqT := httptest.NewRequest("POST", "/api/evolution/jobs/"+jobID2+"/pause", nil)
	reqT.SetPathValue("id", jobID2)
	wT := httptest.NewRecorder()
	srv.HandlePauseEvolutionJob(wT, reqT)
	if wT.Code != http.StatusConflict {
		t.Errorf("terminal pause: expected 409, got %d", wT.Code)
	}

	// Unknown job reads 404.
	reqU := httptest.NewRequest("POST", "/api/evolution/jobs/nope/pause", nil)
	reqU.SetPathValue("id", "nope")
	wU := httptest.NewRecorder()
	srv.HandlePauseEvolutionJob(wU, reqU)
	if wU.Code != http.StatusNotFound {
		t.Errorf("unknown pause: expected 404, got %d", wU.Code)
	}

	// Approve-early on a paused job finishes the run through the real gate.
	srv3, jobID3, _ := wizardTestServer(t)
	_, _ = srv3.Storage.PauseEvolutionJob(jobID3, "")
	reqA := httptest.NewRequest("POST", "/api/evolution/jobs/"+jobID3+"/approve", nil)
	reqA.SetPathValue("id", jobID3)
	wA := httptest.NewRecorder()
	srv3.HandleApproveEvolutionJob(wA, reqA)
	if wA.Code != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d: %s", wA.Code, wA.Body.String())
	}
	var approved EvolutionJobView
	if err := json.Unmarshal(wA.Body.Bytes(), &approved); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if approved.Status != EvolutionJobComplete || approved.LoopOutcome != LoopOutcomeCompleted {
		t.Errorf("approve-early did not finalize the run: %+v", approved)
	}

	// Approve-early on a terminal job reads 409 — history is not rewritten.
	wA2 := httptest.NewRecorder()
	srv3.HandleApproveEvolutionJob(wA2, reqA)
	if wA2.Code != http.StatusConflict {
		t.Errorf("re-approve: expected 409, got %d", wA2.Code)
	}

	// Abort (cancel path) lands the cancelled status with the operator reason.
	srv4, jobID4, _ := wizardTestServer(t)
	reqB := httptest.NewRequest("POST", "/api/evolution/jobs/"+jobID4+"/abort", strings.NewReader(`{"reason":"operator stopped the round"}`))
	reqB.SetPathValue("id", jobID4)
	wB := httptest.NewRecorder()
	srv4.HandleAbortEvolutionJob(wB, reqB)
	if wB.Code != http.StatusOK {
		t.Fatalf("abort: expected 200, got %d: %s", wB.Code, wB.Body.String())
	}
	var aborted EvolutionJobView
	if err := json.Unmarshal(wB.Body.Bytes(), &aborted); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if aborted.Status != EvolutionJobCancelled || aborted.CancelReason != "operator stopped the round" {
		t.Errorf("abort did not cancel with reason: %+v", aborted)
	}
}

func TestHandleEvolutionLoopControlsAudited(t *testing.T) {
	// The controls must land in the activity log (story 07's audited wrappers),
	// so the wizard inherits the trail instead of writing its own — each of
	// pause, abort, and approve is checked on its own fresh server.
	srv, jobID, _ := wizardTestServer(t)

	req := httptest.NewRequest("POST", "/api/evolution/jobs/"+jobID+"/pause", nil)
	req.SetPathValue("id", jobID)
	w := httptest.NewRecorder()
	srv.HandlePauseEvolutionJob(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("pause: expected 200, got %d", w.Code)
	}
	assertLoopControlAudited(t, srv, "pause", jobID)

	// A fresh server so the abort is the only control recorded.
	srv2, jobID2, _ := wizardTestServer(t)
	req2 := httptest.NewRequest("POST", "/api/evolution/jobs/"+jobID2+"/abort", nil)
	req2.SetPathValue("id", jobID2)
	w2 := httptest.NewRecorder()
	srv2.HandleAbortEvolutionJob(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("abort: expected 200, got %d", w2.Code)
	}
	assertLoopControlAudited(t, srv2, "abort", jobID2)

	// Approve-early rides the same audited wrapper: the job is parked first
	// (storage-level, unaudited) so the approve is the only HTTP control.
	srv3, jobID3, _ := wizardTestServer(t)
	_, _ = srv3.Storage.PauseEvolutionJob(jobID3, "")
	reqA := httptest.NewRequest("POST", "/api/evolution/jobs/"+jobID3+"/approve", nil)
	reqA.SetPathValue("id", jobID3)
	wA := httptest.NewRecorder()
	srv3.HandleApproveEvolutionJob(wA, reqA)
	if wA.Code != http.StatusOK {
		t.Fatalf("approve: expected 200, got %d: %s", wA.Code, wA.Body.String())
	}
	assertLoopControlAudited(t, srv3, "approve", jobID3)
}

// assertLoopControlAudited scans the bus history for one control's audit event.
// The wrapper publishes the audit event on the bus, which the activity-log
// persist hook (main.go) writes durably in production; the test server keeps
// it in the bus history instead.
func assertLoopControlAudited(t *testing.T, srv *Server, action, jobID string) {
	t.Helper()
	for _, ev := range srv.EventBus.GetHistory() {
		if ev.Source == "api" && ev.Action == action && ev.Slug == jobID {
			return
		}
	}
	t.Errorf("%s control was not audited in the activity log", action)
}

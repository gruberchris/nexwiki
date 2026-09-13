package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Story 07 tests: the evolution loop stepper backend. The happy path (eval set
// → iterations → gate accept → promoted skill + trained marker + audit trail
// + coherent iteration history), every stop condition (max iterations,
// plateau, perfect score, run cap via the story 04 machinery), the human
// controls (pause/abort/approve-early with their audit), pause/resume safety
// (never re-running a completed iteration, never losing an accepted version),
// and the auto-revert guarantee (a rejection leaves the live skill
// byte-identical).

// loopFixture seeds a skill plus a queued job with shrunken caps so tests
// exercise stop conditions without long runs. Returns the job, its token, and
// the seeded skill.
func loopFixture(t *testing.T, s *Storage, title string, maxIters, plateau int) (*EvolutionJob, string, *Article) {
	t.Helper()
	skill := seedJobSkill(t, s, title)
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	job.MaxIterations = maxIters
	job.PlateauLimit = plateau
	if err := s.writeEvolutionJob(job); err != nil {
		t.Fatalf("shrinking caps failed: %v", err)
	}
	return job, token, skill
}

// step drives exactly one loop boundary and fails the test on error.
func step(t *testing.T, r *Runner, jobID, token string, input JobInput) LoopStepResult {
	t.Helper()
	res, err := r.LoopStep(jobID, token, input)
	if err != nil {
		t.Fatalf("LoopStep failed: %v", err)
	}
	return res
}

// makeCandidate records one proposal. Scores under the story 03 v0 scorer:
// body 0.4 + diff 0.2 + min(patterns,3)*0.1 + min(len(body)/100000, 0.1).
func makeCandidate(t *testing.T, s *Storage, slug, body, diff string, patterns []string) *SkillCandidate {
	t.Helper()
	c, err := s.CreateSkillCandidate(slug, body, diff, patterns, "proposer-harness")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	return c
}

// perfectCandidate scores a flat 1.0 under the v0 scorer: body present (0.4) +
// diff (0.2) + more than 3 patterns (0.3) + a body of at least 10,000
// characters (0.1).
func perfectCandidate(t *testing.T, s *Storage, slug string) *SkillCandidate {
	t.Helper()
	return makeCandidate(t, s, slug, "# perfect "+strings.Repeat("x", 10000), "--- a/x\n+++ b/x",
		[]string{"p1", "p2", "p3", "p4"})
}

// weakCandidate is a body-only proposal scoring 0.401 (body 0.4 + a 100-char
// body's 0.001 bonus, no diff, no patterns) — always below a seeded strong
// best, and trivially without pattern gain. The story 03 gate refuses
// diff-only candidates before scoring, so a weak proposal carries a short
// body.
func weakCandidate(t *testing.T, s *Storage, slug string, patterns []string) *SkillCandidate {
	t.Helper()
	return makeCandidate(t, s, slug, strings.Repeat("w", 100), "", patterns)
}

func loopStateOK(t *testing.T, s *Storage, jobID string) *LoopState {
	t.Helper()
	st, err := s.GetLoopState(jobID)
	if err != nil {
		t.Fatalf("GetLoopState failed: %v", err)
	}
	if st == nil {
		t.Fatal("loop state missing")
	}
	return st
}

func evalSetUpload(t *testing.T, s *Storage, job *EvolutionJob, token string) string {
	t.Helper()
	t.Setenv(EvalMinTrainEnv, "1")
	t.Setenv(EvalMinValEnv, "1")
	content := `{"train":[{"input":"train one","expected":"e1"},{"input":"train two","expected":"e2"}],"val":[{"input":"val one","expected":"e1"}]}`
	res, err := s.UploadEvolutionEvalSet(job.ID, token, "eval.json", content)
	if err != nil {
		t.Fatalf("UploadEvolutionEvalSet failed: %v", err)
	}
	if res.Meta.EvalHash == "" {
		t.Fatal("eval upload must record a hash")
	}
	return res.Meta.EvalHash
}

func TestLoopHappyPathTwoAcceptedIterations(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, skill := loopFixture(t, s, "Happy Path Skill", 2, 5)
	evalHash := evalSetUpload(t, s, job, token)

	binDir := fakeCLI(t, `{"type":"progress","progress":0.5,"note":"phase work done"}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve", Data: map[string]string{"skill": "# body"}}

	// Iteration 1: the three harness phases, a proposal arriving over the
	// proposer's MCP path between the proposing step and the gate, then the
	// server-side gate.
	if res := step(t, r, job.ID, token, input); !res.Advanced || res.Phase != LoopPhaseInference || res.Iteration != 1 {
		t.Fatalf("inference step wrong: %+v", res)
	}
	if res := step(t, r, job.ID, token, input); !res.Advanced || res.Phase != LoopPhaseMaintaining {
		t.Fatalf("maintaining step wrong: %+v", res)
	}
	if res := step(t, r, job.ID, token, input); !res.Advanced || res.Phase != LoopPhaseProposing {
		t.Fatalf("proposing step wrong: %+v", res)
	}
	// Body 314 chars + diff + one pattern: 0.4 + 0.2 + 0.1 + 0.00314 = 0.7031.
	c1 := makeCandidate(t, s, skill.Slug, "# improved v1 "+strings.Repeat("a", 300), "--- a/x\n+++ b/x", []string{"p1"})
	if res := step(t, r, job.ID, token, input); !res.Advanced || res.Phase != LoopPhaseGating {
		t.Fatalf("gating step wrong: %+v", res)
	}

	rec1, err := s.GetIterationRecord(job.ID, 1)
	if err != nil || rec1 == nil {
		t.Fatalf("iteration 1 record missing: %v", err)
	}
	if rec1.Outcome != IterationOutcomeAccepted || rec1.CandidateID != c1.ID || rec1.ResultVersion != 2 {
		t.Fatalf("iteration 1 record wrong: %+v", rec1)
	}
	if rec1.ValScore != 0.7031 || rec1.RBest != 0.7031 {
		t.Fatalf("iteration 1 scores wrong: %+v", rec1)
	}
	if len(rec1.PatternSlugs) != 1 || rec1.PatternSlugs[0] != "p1" {
		t.Fatalf("iteration 1 patterns wrong: %v", rec1.PatternSlugs)
	}
	if len(rec1.Phases) != 4 {
		t.Fatalf("iteration 1 must carry four phase entries: %+v", rec1.Phases)
	}
	for _, ph := range rec1.Phases {
		if !ph.Complete || ph.ExitedAt.IsZero() {
			t.Fatalf("iteration 1 phase not complete: %+v", ph)
		}
	}
	if rec1.CompletedAt.IsZero() {
		t.Fatal("iteration 1 record must be stamped completed")
	}

	// Iteration 2 repeats the shape and re-trains the marker. Four patterns
	// this time: 0.4 + 0.2 + 0.3 + 0.00314 = 0.9031, past the new best.
	for _, phase := range []string{LoopPhaseInference, LoopPhaseMaintaining, LoopPhaseProposing} {
		if res := step(t, r, job.ID, token, input); !res.Advanced || res.Phase != phase || res.Iteration != 2 {
			t.Fatalf("iteration 2 %s step wrong: %+v", phase, res)
		}
	}
	c2 := makeCandidate(t, s, skill.Slug, "# improved v2 "+strings.Repeat("b", 300), "--- a/x\n+++ b/x", []string{"p1", "p2", "p3", "p4"})
	if res := step(t, r, job.ID, token, input); !res.Advanced || res.Phase != LoopPhaseGating {
		t.Fatalf("iteration 2 gating step wrong: %+v", res)
	}

	// The budget is spent: the next boundary stops with "exhausted".
	res := step(t, r, job.ID, token, input)
	if !res.Stopped || res.Outcome != LoopOutcomeExhausted {
		t.Fatalf("max-iteration stop wrong: %+v", res)
	}

	fresh, err := s.GetEvolutionJob(job.ID)
	if err != nil {
		t.Fatalf("reload job failed: %v", err)
	}
	if fresh.Status != EvolutionJobComplete || fresh.LoopOutcome != LoopOutcomeExhausted {
		t.Fatalf("terminal job wrong: %+v", fresh)
	}

	// The live skill is the last accepted body, two versions past the seed.
	live, err := s.GetArticle(skill.Slug)
	if err != nil {
		t.Fatalf("reload skill failed: %v", err)
	}
	if live.Version != 3 || live.Content != c2.ProposedBody {
		t.Fatalf("promoted skill wrong: v%d %q", live.Version, live.Content)
	}

	// The story 06 marker names the final acceptance and the eval hash that
	// trained it.
	entry, err := s.GetSkillTrainedState(skill.Slug)
	if err != nil {
		t.Fatalf("trained state failed: %v", err)
	}
	if entry.State != TrainedStateTrained {
		t.Fatalf("marker state = %s (%s), want trained", entry.State, entry.Reason)
	}
	if entry.TrainedVersion != 3 || entry.TrainedCandidate != c2.ID || entry.CurrentVersion != 3 {
		t.Fatalf("marker metadata wrong: %+v", entry)
	}
	if entry.TrainedEvalHash != evalHash {
		t.Fatalf("marker eval hash %q != uploaded %q", entry.TrainedEvalHash, evalHash)
	}

	// The story 03 audit trail carries one accepted gate decision per
	// iteration, with a monotonic R_best.
	records, err := s.ListSkillAuditRecords()
	if err != nil {
		t.Fatalf("audit read failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("audit records = %d, want 2 accepted", len(records))
	}
	for _, rec := range records {
		if rec.Outcome != SkillAuditAccepted || rec.Decider != SkillAuditDeciderGate {
			t.Fatalf("audit record wrong: %+v", rec)
		}
	}
	if records[1].RBestBefore != records[0].ValidationScore || records[1].RBestAfter != records[1].ValidationScore {
		t.Fatalf("R_best trail not monotonic: %+v", records)
	}

	// Both candidates are promoted and linked to their result versions.
	for i, id := range []string{c1.ID, c2.ID} {
		c, err := s.GetSkillCandidate(id)
		if err != nil {
			t.Fatalf("candidate reload failed: %v", err)
		}
		if c.Status != SkillCandidatePromoted || c.ResultVersion != i+2 {
			t.Fatalf("candidate %s wrong: %+v", id, c)
		}
	}

	// The loop state reads terminal, and the story 09 report can walk two
	// complete iteration records.
	st := loopStateOK(t, s, job.ID)
	if st.Status != LoopStatusTerminal || st.Outcome != LoopOutcomeExhausted {
		t.Fatalf("loop state wrong: %+v", st)
	}
	recs, err := s.ListIterationRecords(job.ID)
	if err != nil {
		t.Fatalf("ListIterationRecords failed: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("iteration records = %d, want 2", len(recs))
	}
}

func TestLoopExhaustedWithRejectionsKeepsSkillAndMarker(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, skill := loopFixture(t, s, "Reject Skill", 2, 5)

	// Seed a strong best before the loop: the weak proposals below can never
	// beat it, so every iteration is rejected and the skill must not move.
	rich := makeCandidate(t, s, skill.Slug, "# strong body "+strings.Repeat("s", 400), "--- a/x\n+++ b/x", []string{"p1", "p2", "p3", "p4"})
	if _, err := s.PromoteSkillCandidate(rich.ID); err != nil {
		t.Fatalf("seed promote failed: %v", err)
	}
	live, _ := s.GetArticle(skill.Slug)
	before := live.Content

	binDir := fakeCLI(t, `{"type":"progress","progress":0.4,"note":"work"}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}

	for i := 1; i <= 2; i++ {
		for _, phase := range []string{LoopPhaseInference, LoopPhaseMaintaining, LoopPhaseProposing} {
			if res := step(t, r, job.ID, token, input); !res.Advanced || res.Phase != phase {
				t.Fatalf("iteration %d %s step wrong: %+v", i, phase, res)
			}
		}
		weakCandidate(t, s, skill.Slug, nil)
		res := step(t, r, job.ID, token, input)
		if !res.Advanced || res.Phase != LoopPhaseGating {
			t.Fatalf("iteration %d gating wrong: %+v", i, res)
		}
		rec, _ := s.GetIterationRecord(job.ID, i)
		if rec.Outcome != IterationOutcomeRejected || rec.ValScore != 0.401 || rec.RBest != 0.9041 {
			t.Fatalf("iteration %d record wrong: %+v", i, rec)
		}
		if rec.CandidateID == "" {
			t.Fatalf("iteration %d must record its rejected candidate", i)
		}
	}
	if res := step(t, r, job.ID, token, input); !res.Stopped || res.Outcome != LoopOutcomeExhausted {
		t.Fatalf("budget stop wrong: %+v", res)
	}

	// The live skill is byte-identical to its last accepted version — the
	// auto-revert guarantee — and the trained marker still names it, un-stale.
	live, _ = s.GetArticle(skill.Slug)
	if live.Version != 2 || live.Content != before {
		t.Fatalf("rejection moved the live skill: v%d %q", live.Version, live.Content)
	}
	entry, err := s.GetSkillTrainedState(skill.Slug)
	if err != nil {
		t.Fatalf("trained state failed: %v", err)
	}
	if entry.State != TrainedStateTrained {
		t.Fatalf("marker must stay trained through rejections: %+v", entry)
	}
	records, _ := s.ListSkillAuditRecords()
	if len(records) != 3 {
		t.Fatalf("audit records = %d, want 1 seed accept + 2 gate rejects", len(records))
	}
	for _, rec := range records[1:] {
		if rec.Outcome != SkillAuditRejected || rec.Decider != SkillAuditDeciderGate {
			t.Fatalf("gate reject audit wrong: %+v", rec)
		}
	}
}

func TestLoopPlateauStop(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, skill := loopFixture(t, s, "Plateau Skill", 12, 2)

	// A strong best so every weak proposal is rejected; the proposals carry no
	// patterns, so there is never a pattern gain and the plateau counter
	// climbs on every rejected iteration.
	rich := makeCandidate(t, s, skill.Slug, "# strong "+strings.Repeat("s", 400), "--- a/x\n+++ b/x", []string{"p1", "p2", "p3", "p4"})
	if _, err := s.PromoteSkillCandidate(rich.ID); err != nil {
		t.Fatalf("seed promote failed: %v", err)
	}

	binDir := fakeCLI(t, `{"type":"progress","progress":0.3,"note":"grinding"}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}

	for i := 1; i <= 2; i++ {
		for range loopPhaseOrder {
			step(t, r, job.ID, token, input)
		}
		weakCandidate(t, s, skill.Slug, nil)
		res := step(t, r, job.ID, token, input)
		if i < 2 && !res.Advanced {
			t.Fatalf("iteration %d gating must advance: %+v", i, res)
		}
		if i == 2 && (!res.Stopped || res.Outcome != LoopOutcomePlateaued) {
			t.Fatalf("plateau stop wrong: %+v", res)
		}
	}

	// The loop stopped without starting iteration 3.
	recs, _ := s.ListIterationRecords(job.ID)
	if len(recs) != 2 {
		t.Fatalf("iteration records = %d, want 2 (no iteration 3)", len(recs))
	}
	st := loopStateOK(t, s, job.ID)
	if st.Status != LoopStatusTerminal || st.Outcome != LoopOutcomePlateaued {
		t.Fatalf("terminal state wrong: %+v", st)
	}
	if st.CurrentIteration != 2 {
		t.Fatalf("state iteration = %d, want 2 (stopped at the gate)", st.CurrentIteration)
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobComplete || fresh.LoopOutcome != LoopOutcomePlateaued {
		t.Fatalf("terminal job wrong: %+v", fresh)
	}
}

func TestLoopPatternGainResetsPlateau(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, skill := loopFixture(t, s, "Explore Skill", 12, 2)

	rich := makeCandidate(t, s, skill.Slug, "# strong "+strings.Repeat("s", 400), "--- a/x\n+++ b/x", []string{"p1", "p2", "p3", "p4"})
	if _, err := s.PromoteSkillCandidate(rich.ID); err != nil {
		t.Fatalf("seed promote failed: %v", err)
	}

	binDir := fakeCLI(t, `{"type":"progress","progress":0.3}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}

	// Iteration 1 rejects with a fresh pattern: exploration, not plateau.
	for range loopPhaseOrder {
		step(t, r, job.ID, token, input)
	}
	weakCandidate(t, s, skill.Slug, []string{"fresh-pattern"})
	if res := step(t, r, job.ID, token, input); !res.Advanced {
		t.Fatalf("a rejected iteration with pattern gain must not plateau: %+v", res)
	}
	// Iterations 2 and 3 repeat the same pattern: two no-gain rejections in a
	// row trip the limit of 2.
	for i := 2; i <= 3; i++ {
		for range loopPhaseOrder {
			step(t, r, job.ID, token, input)
		}
		weakCandidate(t, s, skill.Slug, []string{"fresh-pattern"})
		res := step(t, r, job.ID, token, input)
		if i < 3 && !res.Advanced {
			t.Fatalf("iteration %d must not plateau yet: %+v", i, res)
		}
		if i == 3 && (!res.Stopped || res.Outcome != LoopOutcomePlateaued) {
			t.Fatalf("plateau stop after no-gain run wrong: %+v", res)
		}
	}
	recs, _ := s.ListIterationRecords(job.ID)
	if len(recs) != 3 {
		t.Fatalf("iteration records = %d, want 3", len(recs))
	}
}

func TestLoopPerfectScoreEarlyStop(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, skill := loopFixture(t, s, "Perfect Skill", 12, 3)

	binDir := fakeCLI(t, `{"type":"progress","progress":0.9,"note":"polished"}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}

	for range loopPhaseOrder {
		step(t, r, job.ID, token, input)
	}
	perfectCandidate(t, s, skill.Slug)
	res := step(t, r, job.ID, token, input)
	if !res.Stopped || res.Outcome != LoopOutcomeCompleted {
		t.Fatalf("perfect score must stop completed: %+v", res)
	}

	recs, _ := s.ListIterationRecords(job.ID)
	if len(recs) != 1 {
		t.Fatalf("early stop must not start iteration 2: %d records", len(recs))
	}
	if recs[0].Outcome != IterationOutcomeAccepted || recs[0].ValScore != 1.0 {
		t.Fatalf("perfect iteration record wrong: %+v", recs[0])
	}
	st := loopStateOK(t, s, job.ID)
	if st.Outcome != LoopOutcomeCompleted || !strings.Contains(st.OutcomeReason, "1.0") {
		t.Fatalf("terminal state wrong: %+v", st)
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobComplete || fresh.LoopOutcome != LoopOutcomeCompleted {
		t.Fatalf("terminal job wrong: %+v", fresh)
	}
	live, _ := s.GetArticle(skill.Slug)
	if live.Version != 2 {
		t.Fatalf("perfect candidate must promote: v%d", live.Version)
	}
}

func TestLoopPauseAtPhaseBoundaryResumesAtNextPhase(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, skill := loopFixture(t, s, "Pause Skill", 4, 5)

	binDir := fakeCLI(t, `{"type":"progress","progress":0.5,"note":"inference done"}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}
	filesDir := filepath.Join(s.JobDir, job.ID+".files")

	step(t, r, job.ID, token, input) // inference of iteration 1

	// The operator pauses between boundaries; nothing is driving, so the
	// story 04 park applies immediately.
	if _, err := s.PauseEvolutionJob(job.ID, "boundary pause"); err != nil {
		t.Fatalf("pause failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filesDir, "step-1-maintaining.stdout.log")); !os.IsNotExist(err) {
		t.Fatalf("maintaining step must not run while paused (stat err: %v)", err)
	}
	res := step(t, r, job.ID, token, input)
	if !res.Parked {
		t.Fatalf("stepping a parked job must report parked: %+v", res)
	}
	st := loopStateOK(t, s, job.ID)
	if st.Status != LoopStatusPaused || st.CurrentPhase != LoopPhaseMaintaining || st.PhaseStatus != "" {
		t.Fatalf("paused state wrong: %+v", st)
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobPaused {
		t.Fatalf("job must be paused: %+v", fresh)
	}

	// Resume: the loop continues at the NEXT phase — maintaining runs now,
	// inference never re-runs.
	if _, err := s.ResumeEvolutionJob(job.ID); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if res := step(t, r, job.ID, token, input); !res.Advanced || res.Phase != LoopPhaseMaintaining {
		t.Fatalf("resume must continue at maintaining: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(filesDir, "step-1-maintaining.stdout.log")); err != nil {
		t.Fatalf("maintaining step log missing after resume: %v", err)
	}
	step(t, r, job.ID, token, input) // proposing
	makeCandidate(t, s, skill.Slug, "# resumed improvement "+strings.Repeat("r", 300), "--- a/x\n+++ b/x", []string{"p1"})
	step(t, r, job.ID, token, input) // gating → accepted

	rec, _ := s.GetIterationRecord(job.ID, 1)
	if rec.Outcome != IterationOutcomeAccepted {
		t.Fatalf("resumed iteration must accept: %+v", rec)
	}
	if len(rec.Phases) != 4 {
		t.Fatalf("resumed iteration must hold four phase entries: %+v", rec.Phases)
	}
	for _, ph := range rec.Phases {
		if !ph.Complete {
			t.Fatalf("no phase entry may stay incomplete after a clean run: %+v", ph)
		}
	}
	// The loop moved on to iteration 2 without re-running completed work.
	if res := step(t, r, job.ID, token, input); !res.Advanced || res.Iteration != 2 || res.Phase != LoopPhaseInference {
		t.Fatalf("next iteration must start fresh: %+v", res)
	}
}

func TestLoopPauseMidStepInterruptsAndRerunsPhase(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, skill := loopFixture(t, s, "Mid Step Skill", 4, 5)

	// A slow harness: the pause lands while the CLI is running, so the park
	// fires at the FIRST progress callback and the rest of that step's output
	// is dropped — the phase is interrupted, not complete.
	dir := t.TempDir()
	script := "#!/bin/sh\nsleep 0.5\nprintf '%s\\n' '{\"type\":\"progress\",\"progress\":0.2,\"note\":\"first\"}' '{\"type\":\"progress\",\"progress\":0.8,\"note\":\"second\"}'\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(script), 0755); err != nil {
		t.Fatalf("fixture failed: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}

	type stepOut struct {
		res LoopStepResult
		err error
	}
	out := make(chan stepOut, 1)
	go func() {
		res, err := r.LoopStep(job.ID, token, input)
		out <- stepOut{res, err}
	}()
	time.Sleep(150 * time.Millisecond)
	if _, err := s.PauseEvolutionJob(job.ID, "mid-step pause"); err != nil {
		t.Fatalf("pause failed: %v", err)
	}
	got := <-out
	if got.err != nil {
		t.Fatalf("LoopStep failed: %v", got.err)
	}
	if !got.res.Parked {
		t.Fatalf("mid-step park must report parked: %+v", got.res)
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobPaused {
		t.Fatalf("job must be paused: %+v", fresh)
	}
	st := loopStateOK(t, s, job.ID)
	if st.CurrentPhase != LoopPhaseInference || st.PhaseStatus != LoopPhaseStatusInterrupted {
		t.Fatalf("interrupted state wrong: %+v", st)
	}
	rec, _ := s.GetIterationRecord(job.ID, 1)
	if len(rec.Phases) != 1 || rec.Phases[0].Complete {
		t.Fatalf("the interrupted phase entry must stand open: %+v", rec.Phases)
	}

	// Resume: the phase re-runs (it was never completed) and the record keeps
	// the interrupted trace ahead of the successful re-run.
	if _, err := s.ResumeEvolutionJob(job.ID); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if res := step(t, r, job.ID, token, input); !res.Advanced || res.Phase != LoopPhaseInference {
		t.Fatalf("resume must re-run the interrupted phase: %+v", res)
	}
	rec, _ = s.GetIterationRecord(job.ID, 1)
	if len(rec.Phases) != 2 {
		t.Fatalf("re-run must append a fresh entry: %+v", rec.Phases)
	}
	if rec.Phases[0].Complete || !rec.Phases[1].Complete {
		t.Fatalf("trace wrong: %+v", rec.Phases)
	}
	// The rest of the iteration proceeds normally.
	step(t, r, job.ID, token, input) // maintaining
	step(t, r, job.ID, token, input) // proposing
	makeCandidate(t, s, skill.Slug, "# after interruption "+strings.Repeat("i", 300), "--- a/x\n+++ b/x", []string{"p1"})
	step(t, r, job.ID, token, input) // gating → accepted
	rec, _ = s.GetIterationRecord(job.ID, 1)
	if rec.Outcome != IterationOutcomeAccepted {
		t.Fatalf("iteration must complete after the re-run: %+v", rec)
	}
}

func TestLoopAbortMidRun(t *testing.T) {
	srv := newMCPServer(t)
	s := srv.Storage
	job, token, skill := loopFixture(t, s, "Abort Skill", 4, 5)
	// A pending proposal exists when the abort lands: it must survive as a
	// data record while the live skill stays untouched.
	pending := weakCandidate(t, s, skill.Slug, nil)

	dir := t.TempDir()
	script := "#!/bin/sh\nsleep 2\necho should-never-print\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(script), 0755); err != nil {
		t.Fatalf("fixture failed: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(150 * time.Millisecond)
		if _, err := srv.AbortEvolutionLoop(job.ID, "operator aborted"); err != nil {
			t.Errorf("abort failed: %v", err)
		}
	}()
	_, err := r.LoopStep(job.ID, token, input)
	<-done
	if err == nil {
		t.Fatal("the killed step must surface an error")
	}

	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobCancelled || fresh.LoopOutcome != LoopOutcomeCancelled {
		t.Fatalf("aborted job wrong: %+v", fresh)
	}
	st := loopStateOK(t, s, job.ID)
	if st.Status != LoopStatusTerminal || st.Outcome != LoopOutcomeCancelled {
		t.Fatalf("aborted loop state wrong: %+v", st)
	}
	rec, _ := s.GetIterationRecord(job.ID, 1)
	if rec.Outcome != IterationOutcomeInterrupted || !strings.Contains(rec.Note, "operator aborted") {
		t.Fatalf("in-flight iteration must read interrupted: %+v", rec)
	}
	live, _ := s.GetArticle(skill.Slug)
	if live.Version != 1 || live.Content != "# Abort Skill body" {
		t.Fatalf("abort must leave the skill untouched: %+v", live)
	}
	c, _ := s.GetSkillCandidate(pending.ID)
	if c.Status != SkillCandidatePending {
		t.Fatalf("abort must not decide the pending candidate: %+v", c)
	}
	// The abort is audited.
	audited := false
	for _, ev := range srv.EventBus.GetHistory() {
		if ev.Action == "abort" && ev.Slug == job.ID {
			audited = true
		}
	}
	if !audited {
		t.Fatal("abort must be audited in the activity log")
	}
}

func TestLoopApproveEarlyMidRunAcceptsCurrentBest(t *testing.T) {
	srv := newMCPServer(t)
	s := srv.Storage
	job, token, skill := loopFixture(t, s, "Approve Skill", 12, 3)

	binDir := fakeCLI(t, `{"type":"progress","progress":0.5,"note":"working"}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}

	for range loopPhaseOrder {
		step(t, r, job.ID, token, input)
	}
	c1 := makeCandidate(t, s, skill.Slug, "# approved improvement "+strings.Repeat("v", 300), "--- a/x\n+++ b/x", []string{"p1", "p2", "p3", "p4"})

	// Approve while the loop is between boundaries: the request flag is set,
	// and the next boundary finishes with the current best accepted.
	if _, err := srv.ApproveEarlyEvolutionLoop(job.ID); err != nil {
		t.Fatalf("approve-early failed: %v", err)
	}
	res := step(t, r, job.ID, token, input)
	if !res.Stopped || res.Outcome != LoopOutcomeCompleted {
		t.Fatalf("approve-early must finish completed: %+v", res)
	}

	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobComplete || fresh.LoopOutcome != LoopOutcomeCompleted {
		t.Fatalf("approved job wrong: %+v", fresh)
	}
	live, _ := s.GetArticle(skill.Slug)
	if live.Version != 2 || live.Content != c1.ProposedBody {
		t.Fatalf("current best must be promoted: v%d", live.Version)
	}
	entry, _ := s.GetSkillTrainedState(skill.Slug)
	if entry.State != TrainedStateTrained || entry.TrainedCandidate != c1.ID {
		t.Fatalf("marker must stamp the approved acceptance: %+v", entry)
	}
	rec, _ := s.GetIterationRecord(job.ID, 1)
	if rec.Outcome != IterationOutcomeAccepted || rec.CandidateID != c1.ID {
		t.Fatalf("approved iteration record wrong: %+v", rec)
	}
	st := loopStateOK(t, s, job.ID)
	if st.Outcome != LoopOutcomeCompleted || !strings.Contains(st.OutcomeReason, "approved early") {
		t.Fatalf("terminal state wrong: %+v", st)
	}
	audited := false
	for _, ev := range srv.EventBus.GetHistory() {
		if ev.Action == "approve" && ev.Slug == job.ID {
			audited = true
		}
	}
	if !audited {
		t.Fatal("approve-early must be audited")
	}
}

func TestLoopApproveEarlyOnPausedJobFinishesImmediately(t *testing.T) {
	srv := newMCPServer(t)
	s := srv.Storage
	job, token, skill := loopFixture(t, s, "Approve Paused Skill", 12, 3)

	binDir := fakeCLI(t, `{"type":"progress","progress":0.5}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}

	step(t, r, job.ID, token, input)
	if _, err := s.PauseEvolutionJob(job.ID, "pausing before approve"); err != nil {
		t.Fatalf("pause failed: %v", err)
	}
	// Nothing is driving, so the approval applies immediately with no pending
	// candidate: the run finishes with the skill at its current version.
	if _, err := srv.ApproveEarlyEvolutionLoop(job.ID); err != nil {
		t.Fatalf("approve-early failed: %v", err)
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobComplete || fresh.LoopOutcome != LoopOutcomeCompleted {
		t.Fatalf("paused approve must finish the run: %+v", fresh)
	}
	live, _ := s.GetArticle(skill.Slug)
	if live.Version != 1 {
		t.Fatalf("no candidate, no promotion: v%d", live.Version)
	}
	rec, _ := s.GetIterationRecord(job.ID, 1)
	if rec.Outcome != IterationOutcomeInterrupted {
		t.Fatalf("the parked iteration must read interrupted: %+v", rec)
	}
	st := loopStateOK(t, s, job.ID)
	if st.Status != LoopStatusTerminal || st.Outcome != LoopOutcomeCompleted {
		t.Fatalf("terminal state wrong: %+v", st)
	}
}

func TestLoopHarnessCompleteCallbackEndsRun(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, skill := loopFixture(t, s, "Harness Complete Skill", 12, 3)

	// A story-04-style shim that declares the run complete during the first
	// phase: the loop must stop and reconcile rather than keep scheduling.
	binDir := fakeCLI(t,
		`{"type":"progress","progress":0.4,"note":"halfway"}`,
		`{"type":"complete","outcome":"complete"}`,
	)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})

	finalState, err := r.RunLoop(job.ID, token, JobInput{Task: "evolve"})
	if err != nil {
		t.Fatalf("RunLoop failed: %v", err)
	}
	if finalState.Status != LoopStatusTerminal || finalState.Outcome != LoopOutcomeCompleted {
		t.Fatalf("RunLoop must end completed: %+v", finalState)
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobComplete {
		t.Fatalf("job must be complete: %+v", fresh)
	}
	rec, _ := s.GetIterationRecord(job.ID, 1)
	if rec.Outcome != IterationOutcomeInterrupted || len(rec.Phases) != 1 || rec.Phases[0].Complete {
		t.Fatalf("the interrupted iteration must trace honestly: %+v", rec)
	}
	live, _ := s.GetArticle(skill.Slug)
	if live.Version != 1 {
		t.Fatalf("no promotion without a gate: v%d", live.Version)
	}
}

func TestRunLoopDriverRunsToPlateauStop(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, _ := loopFixture(t, s, "Driver Skill", 1, 1)

	binDir := fakeCLI(t, `{"type":"progress","progress":0.5,"note":"chugging"}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})

	finalState, err := r.RunLoop(job.ID, token, JobInput{Task: "evolve"})
	if err != nil {
		t.Fatalf("RunLoop failed: %v", err)
	}
	// One iteration, no proposal ever, plateau limit 1: the driver stops
	// without the test touching anything between boundaries.
	if finalState.Outcome != LoopOutcomePlateaued {
		t.Fatalf("driver must stop plateaued: %+v", finalState)
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobComplete || fresh.LoopOutcome != LoopOutcomePlateaued {
		t.Fatalf("terminal job wrong: %+v", fresh)
	}
	recs, _ := s.ListIterationRecords(job.ID)
	if len(recs) != 1 || recs[0].Outcome != IterationOutcomeRejected || recs[0].CandidateID != "" {
		t.Fatalf("no-candidate iteration must read rejected: %+v", recs)
	}
}

func TestLoopGateCrashWindowAdvancesWithoutRegating(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, skill := loopFixture(t, s, "Crash Window Skill", 12, 3)

	binDir := fakeCLI(t, `{"type":"progress","progress":0.5}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}

	for range loopPhaseOrder {
		step(t, r, job.ID, token, input)
	}
	makeCandidate(t, s, skill.Slug, "# accepted "+strings.Repeat("c", 300), "--- a/x\n+++ b/x", []string{"p1"})
	step(t, r, job.ID, token, input) // gating → accepted
	auditBefore, _ := s.ListSkillAuditRecords()
	if len(auditBefore) != 1 {
		t.Fatalf("one accepted audit expected, got %d", len(auditBefore))
	}

	// Simulate the crash window: the record is resolved but the loop state
	// still stands at the gating phase of iteration 1.
	if _, err := s.mutateLoopState(job.ID, false, func(st *LoopState) {
		st.CurrentIteration = 1
		st.CurrentPhase = LoopPhaseGating
	}); err != nil {
		t.Fatalf("state regression failed: %v", err)
	}
	res := step(t, r, job.ID, token, input)
	if !res.Advanced || res.Phase != LoopPhaseGating {
		t.Fatalf("crash-window advance wrong: %+v", res)
	}
	auditAfter, _ := s.ListSkillAuditRecords()
	if len(auditAfter) != len(auditBefore) {
		t.Fatalf("a resolved iteration must never gate twice: %d -> %d", len(auditBefore), len(auditAfter))
	}
	st := loopStateOK(t, s, job.ID)
	if st.CurrentIteration != 2 || st.CurrentPhase != LoopPhaseQueued {
		t.Fatalf("state must advance past the resolved iteration: %+v", st)
	}
}

func TestLoopUnknownProfileFailsRun(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedJobSkill(t, s, "Bad Profile Skill")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "no-such-profile")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	res, err := r.LoopStep(job.ID, token, JobInput{Task: "evolve"})
	if err == nil {
		t.Fatalf("unknown profile must fail the step: %+v", res)
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobFailed {
		t.Fatalf("unknown profile must fail the run: %+v", fresh)
	}
	// The profile is resolved before any phase is marked entered, so no
	// iteration record exists — the honest trace of a run that never started.
	rec, _ := s.GetIterationRecord(job.ID, 1)
	if rec != nil {
		t.Fatalf("no iteration record may exist for a run that never stepped: %+v", rec)
	}
}

func TestLoopHumanControlsAreAudited(t *testing.T) {
	srv := newMCPServer(t)
	s := srv.Storage
	job, _, _ := loopFixture(t, s, "Audit Controls Skill", 4, 3)

	if _, err := srv.PauseEvolutionLoop(job.ID, "hold on"); err != nil {
		t.Fatalf("pause control failed: %v", err)
	}
	if _, err := srv.ApproveEarlyEvolutionLoop(job.ID); err != nil {
		t.Fatalf("approve control failed: %v", err)
	}
	seen := map[string]bool{}
	for _, ev := range srv.EventBus.GetHistory() {
		if ev.Source == "api" && ev.Slug == job.ID {
			seen[ev.Action] = true
		}
	}
	if !seen["pause"] || !seen["approve"] {
		t.Fatalf("human controls must be audited, saw %v", seen)
	}
	// Approving or aborting a terminal job is refused — history is not
	// rewritten.
	if _, err := srv.ApproveEarlyEvolutionLoop(job.ID); err == nil {
		t.Fatal("approve on a terminal job must be refused")
	}
	if _, err := srv.AbortEvolutionLoop(job.ID, "too late"); err == nil {
		t.Fatal("abort on a terminal job must be refused")
	}
}

func TestGetEvolutionIterationsTool(t *testing.T) {
	// Agent roles are denied: the loop state is harness-owned.
	for _, role := range []string{WikiskillRoleInference, WikiskillRoleMaintainer, WikiskillRoleProposer} {
		srv := roleServer(t, role)
		resp := roleCall(t, srv, "agent", `{"name":"get_evolution_iterations","arguments":{"id":"x","job_token":"y"}}`)
		if !resp.IsError || !strings.Contains(resp.Content[0].Text, "lacks scope") {
			t.Errorf("role %s must be scope-denied, got: %+v", role, resp)
		}
	}

	// The operator reads a driven loop through the full MCP path.
	s := newLifecycleStorage(t)
	skill := seedJobSkill(t, s, "Loop Read Skill")
	job, token, _ := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	binDir := fakeCLI(t, `{"type":"progress","progress":0.5,"note":"stepping"}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}
	for range loopPhaseOrder {
		step(t, r, job.ID, token, input)
	}
	makeCandidate(t, s, skill.Slug, "# read me "+strings.Repeat("z", 300), "--- a/x\n+++ b/x", []string{"p1"})
	step(t, r, job.ID, token, input)

	loopSrv := NewServer(s, "Test Wiki", "light", false, NewEventBus(), "1.0.0", "")
	resp := toolCall(t, loopSrv, fmt.Sprintf(`{"name":"get_evolution_iterations","arguments":{"id":%q,"job_token":%q}}`, job.ID, token))
	if resp.IsError {
		t.Fatalf("tool error: %s", resp.Content[0].Text)
	}
	text := resp.Content[0].Text
	for _, want := range []string{
		"iteration 2", "phase " + LoopPhaseQueued, "iteration history",
		IterationOutcomeAccepted, "R_best",
	} {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
			t.Errorf("loop readout missing %q:\n%s", want, text)
		}
	}

	// A forged token is denied and audited like every other job tool.
	resp = toolCall(t, loopSrv, fmt.Sprintf(`{"name":"get_evolution_iterations","arguments":{"id":%q,"job_token":"forged"}}`, job.ID))
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "access denied") {
		t.Fatalf("forged token must be denied: %+v", resp)
	}
	denied := false
	for _, ev := range loopSrv.EventBus.GetHistory() {
		if ev.Action == DenyAction && ev.Tool == "get_evolution_iterations" && ev.Slug == job.ID {
			denied = true
		}
	}
	if !denied {
		t.Fatal("forged-token denial must be audited")
	}
}

func TestLoopStateSurvivesAcrossStepperRestarts(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, _ := loopFixture(t, s, "Restart Skill", 12, 3)

	binDir := fakeCLI(t, `{"type":"progress","progress":0.4,"note":"before restart"}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r1 := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}
	step(t, r1, job.ID, token, input)
	step(t, r1, job.ID, token, input)

	// A "new process" (a fresh Runner on the same storage) picks the loop up
	// from the persisted state at the next phase.
	r2 := testRunner(t, s, CLIProfile{Binary: "opencode"})
	if res := step(t, r2, job.ID, token, input); !res.Advanced || res.Phase != LoopPhaseProposing {
		t.Fatalf("restart must continue at proposing: %+v", res)
	}
	st := loopStateOK(t, s, job.ID)
	if st.CurrentPhase != LoopPhaseGating || st.CurrentIteration != 1 {
		t.Fatalf("state after restart wrong: %+v", st)
	}
	rec, _ := s.GetIterationRecord(job.ID, 1)
	if len(rec.Phases) != 3 {
		t.Fatalf("all three phases must be recorded across runners: %+v", rec.Phases)
	}
}

func TestLoopConcurrentBoundariesStayCoherent(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, _ := loopFixture(t, s, "Race Skill", 12, 3)
	binDir := fakeCLI(t, `{"type":"progress","progress":0.1}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.LoopStep(job.ID, token, input)
		}()
	}
	wg.Wait()
	// Whatever won, the state must still parse and the records must be intact.
	if _, err := s.GetLoopState(job.ID); err != nil {
		t.Fatalf("loop state corrupted under concurrency: %v", err)
	}
	if _, err := s.ListIterationRecords(job.ID); err != nil {
		t.Fatalf("iteration records corrupted under concurrency: %v", err)
	}
}

func TestLoopStopViaRunCapTimeout(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, _ := loopFixture(t, s, "Run Cap Skill", 12, 3)

	// A run cap already in the past: the story 04 claim marks the job timeout
	// and the loop consumes it as a budget stop instead of rebuilding
	// enforcement.
	job.RunDeadlineAt = time.Now().UTC().Add(-time.Minute)
	if err := s.writeEvolutionJob(job); err != nil {
		t.Fatalf("deadline edit failed: %v", err)
	}

	binDir := fakeCLI(t, `{"type":"progress","progress":0.5}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})

	res, err := r.LoopStep(job.ID, token, JobInput{Task: "evolve"})
	if err != nil {
		t.Fatalf("LoopStep failed: %v", err)
	}
	if !res.Stopped || res.Outcome != LoopOutcomeExhausted {
		t.Fatalf("run cap must stop the loop exhausted: %+v", res)
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobTimeout {
		t.Fatalf("the story 04 machinery must own the timeout status: %+v", fresh)
	}
	if fresh.LoopOutcome != LoopOutcomeExhausted {
		t.Fatalf("budget stop must stamp the loop outcome: %+v", fresh)
	}
}

func TestLoopRejectCandidateCarriesGateNumbers(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, skill := loopFixture(t, s, "Gate Numbers Skill", 12, 3)

	rich := makeCandidate(t, s, skill.Slug, "# strong "+strings.Repeat("s", 400), "--- a/x\n+++ b/x", []string{"p1", "p2", "p3", "p4"})
	if _, err := s.PromoteSkillCandidate(rich.ID); err != nil {
		t.Fatalf("seed promote failed: %v", err)
	}
	binDir := fakeCLI(t, `{"type":"progress","progress":0.5}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}
	for range loopPhaseOrder {
		step(t, r, job.ID, token, input)
	}
	weak := weakCandidate(t, s, skill.Slug, []string{"p1"})
	step(t, r, job.ID, token, input)

	rec, _ := s.GetIterationRecord(job.ID, 1)
	if rec.Outcome != IterationOutcomeRejected {
		t.Fatalf("weak proposal must be rejected: %+v", rec)
	}
	if rec.ValScore != 0.501 || rec.RBest != 0.9041 {
		t.Fatalf("gate numbers must be the story 03 audit numbers: %+v", rec)
	}
	// The candidate itself is decided, and its own audit entry exists.
	c, _ := s.GetSkillCandidate(weak.ID)
	if c.Status != SkillCandidateRejected {
		t.Fatalf("rejected candidate status wrong: %+v", c)
	}
	records, _ := s.ListSkillAuditRecords()
	if len(records) != 2 || records[1].CandidateID != rec.CandidateID {
		t.Fatalf("gate reject audit must name the candidate: %+v", records)
	}
}

func TestLoopApproveEarlyGatesStalePendingCandidate(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, _ := loopFixture(t, s, "Stale Pending Skill", 12, 3)
	stale := weakCandidate(t, s, job.SkillSlug, []string{"p1"})

	binDir := fakeCLI(t, `{"type":"progress","progress":0.5}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}
	step(t, r, job.ID, token, input) // start iteration 1

	if _, err := s.ApproveEarlyEvolutionJob(job.ID); err != nil {
		t.Fatalf("approve failed: %v", err)
	}
	// The run is driving, so only the request flag is set.
	driving, _ := s.GetEvolutionJob(job.ID)
	if driving.Status != EvolutionJobRunning || !driving.ApproveRequested {
		t.Fatalf("driving approve must set only the flag: %+v", driving)
	}
	// The next boundary applies it: the stale candidate goes through the gate
	// (diff-only scores 0.2, the trail is empty → accepted) and the run
	// finishes completed with the skill promoted.
	res := step(t, r, job.ID, token, input)
	if !res.Stopped || res.Outcome != LoopOutcomeCompleted {
		t.Fatalf("approve boundary wrong: %+v", res)
	}
	c, _ := s.GetSkillCandidate(stale.ID)
	if c.Status != SkillCandidatePromoted {
		t.Fatalf("approve must gate the pending candidate: %+v", c)
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobComplete || fresh.LoopOutcome != LoopOutcomeCompleted {
		t.Fatalf("approve must finish the run: %+v", fresh)
	}
	rec, _ := s.GetIterationRecord(job.ID, 1)
	if rec.Outcome != IterationOutcomeAccepted || rec.CandidateID != stale.ID {
		t.Fatalf("the in-flight record takes the approve decision: %+v", rec)
	}
}

func TestLoopApproveRequestedFlagSurvivesAndStepperAppliesIt(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, skill := loopFixture(t, s, "Flag Skill", 12, 3)
	binDir := fakeCLI(t, `{"type":"progress","progress":0.5}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}

	// Approve before anything is dispatched: the flag sits on the queued job.
	if _, err := s.ApproveEarlyEvolutionJob(job.ID); err != nil {
		t.Fatalf("approve failed: %v", err)
	}
	queued, _ := s.GetEvolutionJob(job.ID)
	if queued.Status != EvolutionJobQueued || !queued.ApproveRequested {
		t.Fatalf("queued approve must set only the flag: %+v", queued)
	}
	// The first dispatch boundary applies it — no harness steps ever run.
	res := step(t, r, job.ID, token, input)
	if !res.Stopped || res.Outcome != LoopOutcomeCompleted {
		t.Fatalf("flagged approve must finish at the first boundary: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(s.JobDir, job.ID+".files", "step-1-inference.stdout.log")); !os.IsNotExist(err) {
		t.Fatalf("no harness step may run after an early approval (stat err: %v)", err)
	}
	live, _ := s.GetArticle(skill.Slug)
	if live.Version != 1 {
		t.Fatalf("nothing to accept on a fresh job: v%d", live.Version)
	}
}

// TestLoopLeaseLossMidStepResumesAtInterruptedPhase is the regression test for
// the lease-loss wedge: a callback-less phase step outliving the 60s lease
// loses the lease at the step's final heartbeat, the story 04 sweep requeues
// the job, and the run must RESUME at that phase boundary on re-dispatch —
// no fabricated "failed" outcome, no terminal loop.json, no "a stopped loop
// stays stopped" refusal. Before the fix, res.done routed the requeued job
// into reconcileLoopTerminal, which terminal-ized loop.json with a fake
// failed outcome and permanently wedged the run.
func TestLoopLeaseLossMidStepResumesAtInterruptedPhase(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token, skill := loopFixture(t, s, "Lease Loss Skill", 1, 3)

	// A callback-less harness that outlives the lease: it prints nothing and
	// exits cleanly, so the step's final heartbeat is the only heartbeat —
	// and it reports the lease loss instead of renewing.
	dir := t.TempDir()
	script := "#!/bin/sh\nsleep 1\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(script), 0755); err != nil {
		t.Fatalf("fixture failed: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	input := JobInput{Task: "evolve"}

	type stepOut struct {
		res LoopStepResult
		err error
	}
	out := make(chan stepOut, 1)
	go func() {
		res, err := r.LoopStep(job.ID, token, input)
		out <- stepOut{res, err}
	}()
	// The lease dies while the step is mid-flight — the window a sweep requeues.
	time.Sleep(150 * time.Millisecond)
	dying, err := s.GetEvolutionJob(job.ID)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if dying.Status != EvolutionJobClaimed {
		t.Fatalf("expected the claimed job mid-step, got %s", dying.Status)
	}
	dying.LeaseExpiresAt = time.Now().UTC().Add(-time.Second)
	if err := s.writeEvolutionJob(dying); err != nil {
		t.Fatalf("lease backdate failed: %v", err)
	}

	got := <-out
	if got.err != nil {
		t.Fatalf("a lease loss must not fail the step: %v", got.err)
	}
	if !got.res.Parked || got.res.Stopped || got.res.Outcome != "" {
		t.Fatalf("lease loss must park at the interrupted phase, not stop the loop: %+v", got.res)
	}
	if got.res.Iteration != 1 || got.res.Phase != LoopPhaseInference {
		t.Fatalf("lease loss must report the in-flight phase: %+v", got.res)
	}

	// The job is still live, carries no fabricated failed outcome, and the
	// loop state is non-terminal with the phase honestly interrupted.
	fresh, err := s.GetEvolutionJob(job.ID)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if evolutionJobTerminal(fresh.Status) || fresh.LoopOutcome != "" {
		t.Fatalf("a requeued-after-lease-loss job must stay live and unstamped: %+v", fresh)
	}
	st := loopStateOK(t, s, job.ID)
	if st.Status != LoopStatusPaused || st.CurrentPhase != LoopPhaseInference || st.PhaseStatus != LoopPhaseStatusInterrupted {
		t.Fatalf("interrupted loop state wrong: %+v", st)
	}
	if st.Outcome != "" || st.OutcomeReason != "" {
		t.Fatalf("no outcome may be fabricated on a resumable loop: %+v", st)
	}
	rec, err := s.GetIterationRecord(job.ID, 1)
	if err != nil || rec == nil {
		t.Fatalf("iteration record missing: %v", err)
	}
	if rec.Outcome != "" || len(rec.Phases) != 1 || rec.Phases[0].Complete {
		t.Fatalf("the interrupted phase must stand open on the in-flight iteration: %+v", rec)
	}

	// Re-dispatch: the sweep requeues the stale claim, the stepper re-claims,
	// and the loop resumes AT the interrupted phase — re-running it. Before
	// the fix this refused with "A stopped loop stays stopped".
	if res := step(t, r, job.ID, token, input); !res.Advanced || res.Iteration != 1 || res.Phase != LoopPhaseInference {
		t.Fatalf("re-dispatch must resume by re-running the interrupted phase: %+v", res)
	}
	rec, _ = s.GetIterationRecord(job.ID, 1)
	if len(rec.Phases) != 2 || rec.Phases[0].Complete || !rec.Phases[1].Complete {
		t.Fatalf("the re-run must append a fresh entry over the interrupted trace: %+v", rec.Phases)
	}

	// The iteration completes normally: the remaining phases, the gate, and
	// the budget stop — with no failed outcome anywhere in the trail.
	step(t, r, job.ID, token, input) // maintaining
	step(t, r, job.ID, token, input) // proposing
	makeCandidate(t, s, skill.Slug, "# recovered "+strings.Repeat("r", 300), "--- a/x\n+++ b/x", []string{"p1"})
	if res := step(t, r, job.ID, token, input); !res.Advanced || res.Phase != LoopPhaseGating {
		t.Fatalf("gating step wrong: %+v", res)
	}
	res := step(t, r, job.ID, token, input)
	if !res.Stopped || res.Outcome != LoopOutcomeExhausted {
		t.Fatalf("the iteration cap must stop the run exhausted: %+v", res)
	}
	finished, _ := s.GetEvolutionJob(job.ID)
	if finished.Status != EvolutionJobComplete || finished.LoopOutcome != LoopOutcomeExhausted {
		t.Fatalf("a resumed run must finish honestly: %+v", finished)
	}
	st = loopStateOK(t, s, job.ID)
	if st.Status != LoopStatusTerminal || st.Outcome != LoopOutcomeExhausted {
		t.Fatalf("final loop state wrong: %+v", st)
	}
	rec, _ = s.GetIterationRecord(job.ID, 1)
	if rec.Outcome != IterationOutcomeAccepted {
		t.Fatalf("the resumed iteration must gate accepted: %+v", rec)
	}
}

// TestLoopReconcileRequiresTerminalJob pins the reconcile guard: a still-
// active job (the sweep requeued it after a lease loss) ends no loop and gets
// no outcome stamped on it; genuinely terminal jobs keep their mappings.
func TestLoopReconcileRequiresTerminalJob(t *testing.T) {
	s := newLifecycleStorage(t)
	job, _, _ := loopFixture(t, s, "Reconcile Guard Skill", 4, 3)
	seedRunningState := func(job *EvolutionJob) {
		t.Helper()
		if _, err := s.mutateLoopState(job.ID, true, func(st *LoopState) {
			*st = LoopState{JobID: job.ID, SkillSlug: job.SkillSlug, Status: LoopStatusRunning,
				CurrentIteration: 1, CurrentPhase: LoopPhaseProposing, StartedAt: time.Now().UTC()}
		}); err != nil {
			t.Fatalf("seed loop state failed: %v", err)
		}
	}

	// A still-active job (a lease loss requeues it to queued; claimed again)
	// must not terminal-ize the loop or fabricate an outcome.
	for _, status := range []string{EvolutionJobQueued, EvolutionJobClaimed, EvolutionJobRunning} {
		seedRunningState(job)
		live := *job
		live.Status = status
		out, err := s.reconcileLoopTerminal(&live)
		if err != nil {
			t.Fatalf("reconcile of a %s job failed: %v", status, err)
		}
		if out != "" {
			t.Fatalf("reconcile of a %s job must return no outcome, got %q", status, out)
		}
		st := loopStateOK(t, s, job.ID)
		if st.Status == LoopStatusTerminal || st.Outcome != "" {
			t.Fatalf("a %s job must not terminal-ize the loop: %+v", status, st)
		}
		if st.CurrentPhase != LoopPhaseProposing || st.PhaseStatus != LoopPhaseStatusInterrupted {
			t.Fatalf("the in-flight phase must read interrupted at its boundary: %+v", st)
		}
		liveJob, _ := s.GetEvolutionJob(job.ID)
		if liveJob.LoopOutcome != "" {
			t.Fatalf("a %s job must carry no loop outcome: %+v", status, liveJob)
		}
	}

	// Genuinely terminal jobs keep the status→outcome mapping.
	for i, tc := range []struct{ status, want string }{
		{EvolutionJobCancelled, LoopOutcomeCancelled},
		{EvolutionJobComplete, LoopOutcomeCompleted},
		{EvolutionJobTimeout, LoopOutcomeExhausted},
		{EvolutionJobFailed, LoopOutcomeFailed},
	} {
		job, _, _ := loopFixture(t, s, fmt.Sprintf("Reconcile Mapping Skill %d", i), 4, 3)
		seedRunningState(job)
		live := *job
		live.Status = tc.status
		out, err := s.reconcileLoopTerminal(&live)
		if err != nil {
			t.Fatalf("reconcile of a %s job failed: %v", tc.status, err)
		}
		if out != tc.want {
			t.Fatalf("reconcile of a %s job = %q, want %q", tc.status, out, tc.want)
		}
		st := loopStateOK(t, s, job.ID)
		if st.Status != LoopStatusTerminal || st.Outcome != tc.want {
			t.Fatalf("a %s job must terminal-ize the loop with its outcome: %+v", tc.status, st)
		}
		finished, _ := s.GetEvolutionJob(job.ID)
		if tc.status == EvolutionJobFailed {
			if finished.LoopOutcome != "" {
				t.Fatalf("a failed run is not a designed stop: %+v", finished)
			}
		} else if finished.LoopOutcome != tc.want {
			t.Fatalf("designed stop outcome not stamped: %+v", finished)
		}
	}
}

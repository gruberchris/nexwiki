package server

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Story 12 tests (Phase E/E5): the Stability S0 pilot. The scenario fixture's
// math (TestPilotScenarioFixtureMath), the end-to-end in-repo demonstration
// (TestStabilityPilot: real eval gate → real jobs → real loop under a
// scripted deterministic shim → trained marker → result report, with the E5
// bar asserted mechanically), the shim entry the demonstration re-execs, and
// the runbook accuracy check (tool names, env names, endpoints, and the docs
// mirror).

// pilotFixture seeds the whole S0 pilot into a fresh test storage: the
// stability protocol skill at its baseline (v1), a queued evolution job, and
// the real story-05 eval upload through the format gate with the split floors
// lowered to the seeded set's size via env.
func pilotFixture(t *testing.T) (*Storage, *EvolutionJob, string, *Article, *EvalUploadResult) {
	t.Helper()
	s := newLifecycleStorage(t)
	t.Setenv(EvalMinTrainEnv, "6")
	t.Setenv(EvalMinValEnv, "4")

	ready := "ready"
	skill, err := s.SaveArticleWithStatus("", PilotSkillTitle, PilotBaselineBody(), PilotSkillDescription,
		"", "", "Seed the condensed KimmyDB Cluster Stability Protocol excerpt (S0 pilot baseline)",
		nil, ContentTypeSkill, &ready)
	if err != nil {
		t.Fatalf("seeding the stability protocol skill failed: %v", err)
	}
	if skill.Slug != PilotSkillSlug {
		t.Fatalf("skill slug = %q, want %q", skill.Slug, PilotSkillSlug)
	}

	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	// Bound the scripted run: the schedule needs three iterations; anything
	// past that is a bug to stop loudly, not budget to keep burning.
	job.MaxIterations = 5
	if err := s.writeEvolutionJob(job); err != nil {
		t.Fatalf("shrinking the iteration cap failed: %v", err)
	}

	res, err := s.UploadEvolutionEvalSet(job.ID, token, PilotEvalFilename, PilotEvalUploadJSON())
	if err != nil {
		t.Fatalf("UploadEvolutionEvalSet through the real format gate failed: %v", err)
	}
	return s, job, token, skill, res
}

func TestPilotScenarioFixtureMath(t *testing.T) {
	train, val := PilotTrainScenarios(), PilotValScenarios()
	if len(train) != 6 || len(val) != 4 {
		t.Fatalf("seeded splits = %d train / %d val, want 6 / 4", len(train), len(val))
	}
	seen := map[string]bool{}
	for _, sc := range append(append([]PilotScenario{}, train...), val...) {
		if seen[sc.Key] {
			t.Fatalf("scenario key %q seeded twice", sc.Key)
		}
		seen[sc.Key] = true
		if sc.Verdict != PilotVerdictBug && sc.Verdict != PilotVerdictNoBug {
			t.Fatalf("scenario %s verdict %q is not a verdict", sc.Key, sc.Verdict)
		}
		if !strings.Contains(sc.Input, sc.Key) {
			t.Fatalf("scenario %s input does not name its key", sc.Key)
		}
	}
	if got := Slugify(PilotSkillTitle); got != PilotSkillSlug {
		t.Fatalf("PilotSkillSlug = %q, want the Slugify of the title %q", PilotSkillSlug, got)
	}

	// The verdict scorer must compile under the real sandboxed registry.
	if _, err := resolveSkillScorer(PilotVerdictScorer); err != nil {
		t.Fatalf("verdict scorer refused by the registry: %v", err)
	}

	scoreSplit := func(t *testing.T, cases []EvalCase, body string) float64 {
		t.Helper()
		set, err := compileScorerSet(cases)
		if err != nil {
			t.Fatalf("compileScorerSet failed: %v", err)
		}
		return set.scoreEvalSplit(cases, body)
	}
	baseline := PilotBaselineBody()
	// S0: the baseline catches every true bug and flags every trap — 4/6
	// train, 2/4 val.
	if got := scoreSplit(t, evalCasesOf(train), baseline); got != 0.6667 {
		t.Fatalf("baseline train fit = %.4f, want 0.6667", got)
	}
	if got := scoreSplit(t, evalCasesOf(val), baseline); got != 0.5 {
		t.Fatalf("baseline S0 = %.4f, want 0.5", got)
	}

	// The staged revisions score exactly what the schedule claims.
	for _, st := range PilotCandidateStages() {
		trainScore := scoreSplit(t, evalCasesOf(train), st.Body)
		valScore := scoreSplit(t, evalCasesOf(val), st.Body)
		switch st.Iteration {
		case 1:
			if trainScore != 1.0 || valScore != 0.75 {
				t.Fatalf("stage 1 scores = train %.4f / val %.4f, want 1.0 / 0.75", trainScore, valScore)
			}
		case 2:
			if trainScore != 1.0 || valScore != 0.5 {
				t.Fatalf("stage 2 scores = train %.4f / val %.4f, want 1.0 / 0.5", trainScore, valScore)
			}
		case 3:
			if trainScore != 1.0 || valScore != 1.0 {
				t.Fatalf("stage 3 scores = train %.4f / val %.4f, want 1.0 / 1.0", trainScore, valScore)
			}
		}
		if !strings.Contains(st.Body, PilotMarkerPhrase) {
			t.Fatalf("stage %d body lost the stable marker phrase", st.Iteration)
		}
	}

	// The E5 bar for the baseline against the final stage: halved false bugs
	// and zero true-detection loss.
	valCases := evalCasesOf(val)
	e5 := PilotE5MetricsFor(valCases, baseline, PilotCandidateStages()[2].Body)
	if !e5.Success {
		t.Fatalf("E5 bar must clear for the final stage: %+v", e5)
	}
	if e5.BaselineFalseBugs != 2 || e5.FinalFalseBugs != 0 {
		t.Fatalf("false bugs = %d → %d, want 2 → 0", e5.BaselineFalseBugs, e5.FinalFalseBugs)
	}
	if e5.TrueBugCases != 2 || e5.BaselineTrueBugsCaught != 2 || e5.FinalTrueBugsCaught != 2 || e5.TrueDetectionLoss != 0 {
		t.Fatalf("true detection wrong: %+v", e5)
	}
	if e5.FalseBugTrapCases != 2 {
		t.Fatalf("trap cases = %d, want 2", e5.FalseBugTrapCases)
	}
	// The stage-2 draft must NOT beat the stage-1 accept — the rejection the
	// pilot's audit trail preserves.
	if e5Final, e5Draft := PilotE5MetricsFor(valCases, baseline, PilotCandidateStages()[2].Body), PilotE5MetricsFor(valCases, baseline, PilotCandidateStages()[1].Body); e5Draft.FinalValScore >= e5Final.FinalValScore {
		t.Fatalf("the over-trimmed draft scores %.4f, must sit below the accepted fix %.4f", e5Draft.FinalValScore, e5Final.FinalValScore)
	}
}

// evalCasesOf renders scenarios as their eval cases.
func evalCasesOf(scenarios []PilotScenario) []EvalCase {
	out := make([]EvalCase, 0, len(scenarios))
	for _, sc := range scenarios {
		out = append(out, PilotEvalCaseFor(sc))
	}
	return out
}

// TestStabilityPilot is the in-repo pilot demonstration: the full evolution
// loop (RunLoop) driven in-process under a deterministic scripted harness
// profile — no network, no live cluster, no model — ending with the E5
// success bar asserted mechanically and the summary printed. This is the
// reproducible pilot proof; the real pilot follows the runbook
// (server/skill_pilot_runbook.go, docs/aiagent_skills.md).
func TestStabilityPilot(t *testing.T) {
	s, job, token, skill, evalRes := pilotFixture(t)

	// The fixture's own invariants: the real format gate stored exactly the
	// seeded splits and scored the real baseline.
	if evalRes.Meta.SplitMode != "user" || evalRes.Meta.TrainCount != 6 || evalRes.Meta.ValCount != 4 {
		t.Fatalf("eval upload stored wrong splits: %+v", evalRes.Meta)
	}
	if evalRes.Meta.BaselineS0 != 0.5 {
		t.Fatalf("recorded S0 = %.4f, want 0.5", evalRes.Meta.BaselineS0)
	}
	if evalRes.Meta.BaselineTrain != 0.6667 {
		t.Fatalf("recorded train fit = %.4f, want 0.6667", evalRes.Meta.BaselineTrain)
	}
	wantScorerVersion := EvalScorerVersionFor([]string{PilotVerdictScorer})
	if evalRes.Meta.ScorerVersion != wantScorerVersion {
		t.Fatalf("scorer version = %q, want %q", evalRes.Meta.ScorerVersion, wantScorerVersion)
	}

	// Stage the bodies (single source of truth for the shim and the proposer)
	// and install the scripted profile: a PATH wrapper that re-execs this
	// package's test binary as the shim.
	stageDir := t.TempDir()
	if err := StagePilotBodies(stageDir); err != nil {
		t.Fatalf("staging the pilot bodies failed: %v", err)
	}
	t.Setenv(PilotShimOptInEnv, "1")
	t.Setenv(PilotShimDirEnv, stageDir)
	t.Setenv("PATH", pilotShimOnPath(t)+string(os.PathListSeparator)+os.Getenv("PATH"))

	r := testRunner(t, s, CLIProfile{Binary: "opencode", Args: []string{"run", "--format", "json"}})

	// The proposer goroutine is the harness-side half of the proposal
	// handshake: it registers each scheduled candidate through the same
	// storage path the propose_skill_candidate MCP tool wraps, exactly when
	// the shim's proposing step asks for it.
	stages := PilotCandidateStages()
	proposals := make(chan pilotProposal, len(stages)+1)
	proposerErrs := make(chan error, len(stages)+1)
	stop := make(chan struct{})
	proposerDone := make(chan struct{})
	go func() {
		defer close(proposerDone)
		runPilotProposer(s, stageDir, job.ID, stages, stop, proposals, proposerErrs)
	}()

	finalState, runErr := r.RunLoop(job.ID, token, JobInput{
		Task: PilotTaskInstruction,
		Data: map[string]string{"skill": PilotBaselineBody()},
	})
	close(stop)
	<-proposerDone

	var created []pilotProposal
	for len(proposals) > 0 {
		created = append(created, <-proposals)
	}
	for len(proposerErrs) > 0 {
		t.Fatalf("pilot proposer failed: %v", <-proposerErrs)
	}
	if runErr != nil {
		t.Fatalf("RunLoop failed: %v", runErr)
	}
	if len(created) != len(stages) {
		t.Fatalf("proposer registered %d candidates, want %d", len(created), len(stages))
	}

	// The loop ended terminal/completed via the perfect-score early stop.
	if finalState.Status != LoopStatusTerminal || finalState.Outcome != LoopOutcomeCompleted {
		t.Fatalf("loop state wrong: %+v", finalState)
	}
	fresh, err := s.GetEvolutionJob(job.ID)
	if err != nil {
		t.Fatalf("reload job failed: %v", err)
	}
	if fresh.Status != EvolutionJobComplete || fresh.LoopOutcome != LoopOutcomeCompleted {
		t.Fatalf("terminal job wrong: %+v", fresh)
	}
	if fresh.Runner != "loop-stepper" {
		t.Fatalf("runner = %q, want loop-stepper", fresh.Runner)
	}
	if fresh.ResultReportSlug == "" {
		t.Fatal("a terminal run must carry its result report slug")
	}

	// Iteration history: accept at 0.75, the preserved reject at 0.50, then
	// the accept at 1.0 that stops the loop.
	recs, err := s.ListIterationRecords(job.ID)
	if err != nil {
		t.Fatalf("ListIterationRecords failed: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("iteration records = %d, want 3", len(recs))
	}
	for i, want := range []string{IterationOutcomeAccepted, IterationOutcomeRejected, IterationOutcomeAccepted} {
		if recs[i].Outcome != want {
			t.Fatalf("iteration %d outcome = %q, want %q", recs[i].Iteration, recs[i].Outcome, want)
		}
		if recs[i].Iteration != i+1 {
			t.Fatalf("iteration record #%d carries iteration %d", i, recs[i].Iteration)
		}
	}
	if recs[0].ValScore != 0.75 || recs[0].RBest != 0.75 {
		t.Fatalf("iteration 1 scores wrong: %+v", recs[0])
	}
	if recs[1].ValScore != 0.5 || recs[1].RBest != 0.75 {
		t.Fatalf("iteration 2 scores wrong: %+v", recs[1])
	}
	if recs[2].ValScore != 1.0 || recs[2].ResultVersion != 3 {
		t.Fatalf("iteration 3 record wrong: %+v", recs[2])
	}
	// The scoring context is stamped on every decided iteration (story 11).
	for _, rec := range recs {
		if rec.ScorerVersion != wantScorerVersion || rec.EvalHash != evalRes.Meta.EvalHash {
			t.Fatalf("iteration %d scoring context wrong: %+v", rec.Iteration, rec)
		}
	}
	if recs[0].TrainScore != 1.0 {
		t.Fatalf("iteration 1 train fit = %.4f, want 1.0", recs[0].TrainScore)
	}

	// Each gated candidate is exactly the schedule's staged body.
	for i, prop := range created {
		if recs[i].CandidateID != prop.Candidate.ID {
			t.Fatalf("iteration %d gated candidate %q, want %q", recs[i].Iteration, recs[i].CandidateID, prop.Candidate.ID)
		}
		decided, err := s.GetSkillCandidate(prop.Candidate.ID)
		if err != nil {
			t.Fatalf("candidate reload failed: %v", err)
		}
		if decided.ProposedBody != stages[i].Body {
			t.Fatalf("iteration %d candidate body is not the staged body", recs[i].Iteration)
		}
	}

	// The rejected attempt is preserved: its candidate record stays rejected
	// with the scoring context, and the audit trail carries the gate's
	// rejection alongside the accepts, with a monotonic R_best.
	rejectedCand, err := s.GetSkillCandidate(created[1].Candidate.ID)
	if err != nil {
		t.Fatalf("rejected candidate reload failed: %v", err)
	}
	if rejectedCand.Status != SkillCandidateRejected || rejectedCand.DecidedAt.IsZero() || rejectedCand.ResultVersion != 0 {
		t.Fatalf("rejected candidate wrong: %+v", rejectedCand)
	}
	if rejectedCand.ValidationScorer != wantScorerVersion || rejectedCand.ValidationEvalHash != evalRes.Meta.EvalHash {
		t.Fatalf("rejected candidate scoring context wrong: %+v", rejectedCand)
	}
	auditAll, err := s.ListSkillAuditRecords()
	if err != nil {
		t.Fatalf("audit read failed: %v", err)
	}
	var audit []SkillAuditRecord
	for _, rec := range auditAll {
		if rec.SkillSlug == skill.Slug {
			audit = append(audit, rec)
		}
	}
	if len(audit) != 3 {
		t.Fatalf("audit records = %d, want 3 (two accepts + one preserved reject)", len(audit))
	}
	if audit[0].Outcome != SkillAuditAccepted || audit[0].ValidationScore != 0.75 || audit[0].RBestBefore != 0 || audit[0].RBestAfter != 0.75 {
		t.Fatalf("audit record 1 wrong: %+v", audit[0])
	}
	if audit[1].Outcome != SkillAuditRejected || audit[1].Decider != SkillAuditDeciderGate ||
		audit[1].ValidationScore != 0.5 || audit[1].RBestBefore != 0.75 || audit[1].RBestAfter != 0.75 {
		t.Fatalf("audit record 2 wrong (the preserved reject): %+v", audit[1])
	}
	if audit[2].Outcome != SkillAuditAccepted || audit[2].ValidationScore != 1.0 || audit[2].RBestBefore != 0.75 || audit[2].RBestAfter != 1.0 {
		t.Fatalf("audit record 3 wrong: %+v", audit[2])
	}

	// The live skill: the final accepted body (only the report backlink may
	// follow it), the stable marker phrase intact, and the trained marker
	// naming this run's acceptance.
	live, err := s.GetArticle(skill.Slug)
	if err != nil {
		t.Fatalf("reload skill failed: %v", err)
	}
	// v1 seed → v2 (stage 1 promoted) → v3 (stage 3 promoted) → v4 (the
	// story-09 report backlink appended by EnsureSkillResultReport).
	if live.Version != 4 {
		t.Fatalf("live version = %d, want 4", live.Version)
	}
	if !strings.HasPrefix(live.Content, stages[2].Body) {
		t.Fatalf("the live skill must carry the final accepted body byte-identical (before the report backlink)")
	}
	if !strings.Contains(live.Content, PilotMarkerPhrase) {
		t.Fatal("the stable marker phrase did not survive training")
	}
	entry, err := s.GetSkillTrainedState(skill.Slug)
	if err != nil {
		t.Fatalf("trained state failed: %v", err)
	}
	if entry.State != TrainedStateTrained || entry.TrainedVersion != live.Version || entry.CurrentVersion != live.Version {
		t.Fatalf("trained marker wrong: %+v", entry)
	}
	if entry.TrainedValScore != 1.0 || entry.TrainedEvalHash != evalRes.Meta.EvalHash {
		t.Fatalf("trained marker metadata wrong: %+v", entry)
	}

	// Exactly ONE result report: generated once at the run's end, idempotent
	// under repeated finalization, tagged, linked both ways, and citing the
	// run's real outcome and scores.
	if _, err := s.EnsureSkillResultReport(job.ID); err != nil {
		t.Fatalf("re-ensuring the report failed: %v", err)
	}
	report, err := s.GetArticle(fresh.ResultReportSlug)
	if err != nil {
		t.Fatalf("report article missing: %v", err)
	}
	if !hasTag(report.Tags, SkillResultReportTag) || !hasTag(report.Tags, WikiskillWikiTag) {
		t.Fatalf("report tags wrong: %v", report.Tags)
	}
	if !strings.Contains(report.Title, PilotSkillTitle) {
		t.Fatalf("report title %q must cite the skill", report.Title)
	}
	if !strings.Contains(report.Content, "Outcome: **completed**") {
		t.Fatal("report must carry the real terminal outcome")
	}
	if !strings.Contains(report.Content, "/articles/"+skill.Slug) {
		t.Fatal("report must link back to its skill")
	}
	if !strings.Contains(report.Content, "100.00% (R_best via this run's gate decisions)") {
		t.Fatal("report must carry the final R_best score cell")
	}
	if !strings.Contains(live.Content, "## Trained Skill Result reports") {
		t.Fatal("a promoting run must link its report from the skill")
	}

	// The E5 bar, re-derived from the stored records by the runbook's own
	// step-6 verifier.
	sum, err := s.PilotSummaryFor(fresh, PilotBaselineBody())
	if err != nil {
		t.Fatalf("PilotSummaryFor failed: %v", err)
	}
	if !sum.Success {
		t.Fatalf("the pilot summary reports failure:\n%s", sum)
	}
	// The re-derived baseline must reproduce the gate's recorded S0 exactly —
	// the same scorers, the same rounding, the same math.
	if sum.E5.BaselineValScore != evalRes.Meta.BaselineS0 {
		t.Fatalf("re-derived baseline val score %.4f != recorded S0 %.4f", sum.E5.BaselineValScore, evalRes.Meta.BaselineS0)
	}
	if sum.FinalValScore != 1.0 || sum.E5.FinalValScore != 1.0 {
		t.Fatalf("final val score wrong: summary %.4f / E5 %.4f, want 1.0", sum.FinalValScore, sum.E5.FinalValScore)
	}
	if sum.ReportCount != 1 || sum.ReportSlug != fresh.ResultReportSlug {
		t.Fatalf("report accounting wrong: slug %q count %d", sum.ReportSlug, sum.ReportCount)
	}
	if sum.RejectedIters != 1 || sum.AuditRejects != 1 || !sum.RejectsPreserved {
		t.Fatalf("reject preservation wrong: %+v", sum)
	}

	// The scripted shim's per-scenario verdicts reached the wire: every
	// directive of the gated candidate appears in that iteration's captured
	// proposing-step stdout.
	for i := range created {
		raw, err := os.ReadFile(filepath.Join(s.JobDir, job.ID+".files",
			fmt.Sprintf("step-%d-proposing.stdout.log", recs[i].Iteration)))
		if err != nil {
			t.Fatalf("proposing step log for iteration %d: %v", recs[i].Iteration, err)
		}
		for _, directive := range pilotBodyVerdictLines(stages[i].Body) {
			if !strings.Contains(string(raw), "verdict "+directive) {
				t.Fatalf("iteration %d: shim verdict %q missing from the proposing step log", recs[i].Iteration, directive)
			}
		}
	}

	line := sum.String()
	t.Log(line)
	fmt.Fprintf(os.Stderr, "[stability-pilot]\n%s\n", line)
}

// --- The scripted harness profile ------------------------------------------
//
// The shim contract (skill_runner.go) is one CLI step per evolution phase:
// the payload on stdin, JSON Lines on stdout. The scripted profile re-execs
// this package's test binary through a PATH wrapper (the story-04 fake-CLI
// pattern, extended): the shim reads the real payload, emits per-scenario
// verdicts for the candidate it wants proposed, and — during the proposing
// phase — performs the want/done handshake with the in-process proposer
// goroutine below, the deterministic stand-in for proposing over MCP.

// TestPilotShimEntry is the shim entry point. It runs only when re-exec'd by
// the pilot harness (the opt-in env); in a normal package run it skips.
func TestPilotShimEntry(t *testing.T) {
	if os.Getenv(PilotShimOptInEnv) != "1" {
		t.Skip("pilot shim entry runs only when re-exec'd by the stability pilot harness")
	}
	if err := runPilotShim(os.Stdin, os.Stdout); err != nil {
		t.Fatalf("pilot shim failed: %v", err)
	}
}

// runPilotShim executes one scripted harness step from the payload.
func runPilotShim(in io.Reader, out io.Writer) error {
	raw, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("pilot shim: read payload: %w", err)
	}
	var payload JobStdinPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("pilot shim: payload is not a JobStdinPayload: %w", err)
	}
	emit := func(cb shimCallback) {
		line, err := json.Marshal(cb)
		if err != nil {
			return
		}
		fmt.Fprintln(out, string(line))
	}
	progress := func(p float64, note string) {
		emit(shimCallback{Type: "progress", Iteration: payload.Iteration, Progress: p, Note: note})
	}

	switch payload.Phase {
	case LoopPhaseProposing:
		progress(0.75, "pilot shim: proposing step started")
		dir := os.Getenv(PilotShimDirEnv)
		if dir == "" {
			return fmt.Errorf("pilot shim: %s is not set", PilotShimDirEnv)
		}
		body, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf(pilotStageFileTmpl, payload.Iteration)))
		if err != nil {
			return fmt.Errorf("pilot shim: staged body for iteration %d: %w", payload.Iteration, err)
		}
		// Emit the per-scenario verdicts the staged candidate carries — the
		// captured step log becomes the evidence the demo cross-checks.
		for _, directive := range pilotBodyVerdictLines(string(body)) {
			emit(shimCallback{Type: "log", Message: "verdict " + directive})
		}
		// Ask the harness to register the proposal, then wait for the done
		// marker. A timeout fails the run loudly instead of hanging the loop.
		wantPath := filepath.Join(dir, fmt.Sprintf(pilotWantFileTmpl, payload.Iteration))
		donePath := filepath.Join(dir, fmt.Sprintf(pilotDoneFileTmpl, payload.Iteration))
		if err := os.WriteFile(wantPath, []byte("propose"), 0644); err != nil {
			return fmt.Errorf("pilot shim: want marker for iteration %d: %w", payload.Iteration, err)
		}
		deadline := time.Now().Add(pilotHandshakeLimit)
		for {
			if _, err := os.Stat(donePath); err == nil {
				break
			}
			if time.Now().After(deadline) {
				emit(shimCallback{Type: "complete", Outcome: EvolutionJobOutcomeFailed,
					Error: fmt.Sprintf("pilot shim: proposal handshake timed out for iteration %d (no done marker at %s)", payload.Iteration, donePath)})
				return nil
			}
			time.Sleep(2 * time.Millisecond)
		}
		progress(0.9, fmt.Sprintf("pilot shim: candidate registered by harness (iteration %d)", payload.Iteration))
		return nil
	case LoopPhaseInference:
		progress(0.25, "pilot shim: inference step complete (scenarios quoted in the data block)")
		return nil
	case LoopPhaseMaintaining:
		progress(0.5, "pilot shim: maintaining step complete")
		return nil
	default:
		// Plain story-04 dispatches and unexpected phases: one progress
		// callback and a clean exit keep the contract.
		progress(0.1, "pilot shim: step complete")
		return nil
	}
}

// pilotShimOnPath installs the PATH wrapper: an executable named `opencode`
// that re-execs this package's test binary as the shim entry. Returns the
// directory prepended to PATH.
func pilotShimOnPath(t *testing.T) string {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatalf("pilot shim: locate the test binary: %v", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(bin); rerr == nil {
		bin = resolved
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nexec \"$" + PilotShimBinEnv + "\" -test.run=TestPilotShimEntry\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(script), 0755); err != nil {
		t.Fatalf("pilot shim wrapper failed: %v", err)
	}
	t.Setenv(PilotShimBinEnv, bin)
	return dir
}

// pilotProposerIdentity is the proposer recorded on every scripted candidate.
const pilotProposerIdentity = "pilot-harness"

// pilotProposal is one registered scripted candidate.
type pilotProposal struct {
	Iteration int
	Candidate *SkillCandidate
}

// runPilotProposer is the harness-side proposer: it watches the staged
// directory for the shim's want markers, registers the scheduled candidate
// through the same storage path the propose_skill_candidate MCP tool wraps
// (the in-process stand-in for proposing over MCP), and signals the shim with
// the done marker. Deterministic by construction: the gate only runs after
// the proposing step completes (after the done marker), and every earlier
// candidate was decided by then, so exactly one pending candidate exists at
// each gate.
func runPilotProposer(s *Storage, stageDir, jobID string, stages []PilotCandidateStage, stop <-chan struct{}, out chan<- pilotProposal, errs chan<- error) {
	deadline := time.Now().Add(2 * pilotHandshakeLimit)
	for {
		select {
		case <-stop:
			return
		default:
		}
		if time.Now().After(deadline) {
			errs <- fmt.Errorf("pilot proposer: deadline exceeded waiting for the shim handshake")
			return
		}
		// Best-effort early exit once the run is over. A read can tear against
		// a concurrent job-file write (the record path is lock-free); that is
		// not a proposer failure — the next tick re-reads, and the stop
		// channel remains the authoritative exit after RunLoop returns.
		if job, gerr := s.GetEvolutionJob(jobID); gerr == nil && evolutionJobTerminal(job.Status) {
			return
		}
		for _, st := range stages {
			wantPath := filepath.Join(stageDir, fmt.Sprintf(pilotWantFileTmpl, st.Iteration))
			if _, err := os.Stat(wantPath); err != nil {
				continue
			}
			body, err := os.ReadFile(filepath.Join(stageDir, st.File))
			if err != nil {
				errs <- fmt.Errorf("pilot proposer: staged body %s: %w", st.File, err)
				return
			}
			cand, err := s.CreateSkillCandidate(PilotSkillSlug, string(body), "", st.Patterns, pilotProposerIdentity)
			if err != nil {
				errs <- fmt.Errorf("pilot proposer: propose iteration %d: %w", st.Iteration, err)
				return
			}
			donePath := filepath.Join(stageDir, fmt.Sprintf(pilotDoneFileTmpl, st.Iteration))
			if err := os.WriteFile(donePath, []byte("registered "+cand.ID), 0644); err != nil {
				errs <- fmt.Errorf("pilot proposer: done marker for iteration %d: %w", st.Iteration, err)
				return
			}
			_ = os.Remove(wantPath)
			out <- pilotProposal{Iteration: st.Iteration, Candidate: cand}
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// --- The runbook accuracy check --------------------------------------------

// backtickSpan matches one `quoted span` (the runbook avoids triple-backtick
// fences so this extraction is exact).
var backtickSpan = regexp.MustCompile("`([^`\n]+)`")

// snakeName matches a lowercase snake_case token — the shape of tool and
// argument names.
var snakeName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// TestPilotRunbookAccuracy holds the runbook against the actual code paths:
// every step heading, every backticked tool-shaped token must be a registered
// MCP tool (or a documented argument name), the env names and endpoints must
// be real, the scenario tables must be inline — and the docs mirror must stay
// in sync with all of it.
func TestPilotRunbookAccuracy(t *testing.T) {
	runbook := PilotRunbook()

	// The step headings, in both copies.
	docsRaw, err := os.ReadFile(filepath.Join("..", "docs", "aiagent_skills.md"))
	if err != nil {
		t.Fatalf("read docs/aiagent_skills.md: %v", err)
	}
	docs := string(docsRaw)
	for _, step := range pilotRunbookSteps {
		if !strings.Contains(runbook, step) {
			t.Errorf("runbook missing step heading %q", step)
		}
		if !strings.Contains(docs, step) {
			t.Errorf("docs runbook section missing step heading %q", step)
		}
	}

	// Tool-shaped backticked tokens must be real registry names.
	tools := map[string]bool{}
	for _, def := range mcpToolRegistry {
		if name, ok := def.Schema["name"].(string); ok {
			tools[name] = true
		}
	}
	nonTools := map[string]bool{
		"job_token": true, "skill_slug": true, "candidate_id": true,
		"max_iterations": true, "injection_mode": true, "scenario_key": true,
	}
	for _, m := range backtickSpan.FindAllStringSubmatch(runbook, -1) {
		span := m[1]
		if !snakeName.MatchString(span) || !strings.Contains(span, "_") {
			continue
		}
		if !tools[span] && !nonTools[span] {
			t.Errorf("runbook references %q, which is not a registered MCP tool", span)
		}
	}

	// The procedure's required tools, referenced by name in both copies.
	for _, name := range []string{
		"create_agent_skill", "list_agent_skills", "get_skill_trained_state",
		"create_evolution_job", "upload_evolution_eval_set",
		"propose_skill_candidate", "get_evolution_iterations",
	} {
		if !tools[name] {
			t.Errorf("%q is not in the MCP registry", name)
		}
		if !strings.Contains(runbook, "`"+name+"`") {
			t.Errorf("runbook must reference the real tool %q", name)
		}
		if !strings.Contains(docs, "`"+name+"`") {
			t.Errorf("docs runbook section must reference the real tool %q", name)
		}
	}

	// Real env names and registered endpoints.
	for _, needle := range []string{
		EvalMinTrainEnv, EvalMinValEnv, JobProfilesFileEnv,
		"/api/evolution/jobs/{id}/loop", "/api/evolution/jobs/{id}/report",
		"/api/skills/{slug}/audit", "/api/skills/{slug}/rollback",
		"/api/evolution/jobs/{id}/approve", "/api/evolution/jobs/{id}/pause",
		"/api/evolution/jobs/{id}/abort",
	} {
		if !strings.Contains(runbook, needle) {
			t.Errorf("runbook missing %q", needle)
		}
	}

	// The scenario tables inline, with round tags and verdicts.
	for _, sc := range append(append([]PilotScenario{}, PilotTrainScenarios()...), PilotValScenarios()...) {
		for _, needle := range []string{sc.Key, sc.Verdict, sc.Round} {
			if !strings.Contains(runbook, needle) {
				t.Errorf("runbook missing %q for scenario %s", needle, sc.Key)
			}
		}
		if !strings.Contains(docs, "`"+sc.Key+"`") {
			t.Errorf("docs runbook section missing scenario %s", sc.Key)
		}
	}

	// The docs mirror names the skill, the slug, the bar, and the eval floors.
	for _, needle := range []string{PilotSkillTitle, PilotSkillSlug, PilotSkillSlug + "", EvalMinTrainEnv, EvalMinValEnv, "PilotSummaryFor", "TestStabilityPilot"} {
		if !strings.Contains(docs, needle) {
			t.Errorf("docs runbook section missing %q", needle)
		}
	}
}

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Story 05 tests: training-data upload + format gate + baseline. Each refusal
// class (bad schema, too-small, dupes, leakage, secrets/PII, bad extension,
// terminal job) gets its own test asserting specific per-issue errors and
// that nothing was stored.

// evalFixture builds n distinct plain cases. Words only — no credential
// shapes, no @-signs, no phone-like runs — so the fixtures pass the scan.
func evalFixture(n int) []EvalCase {
	out := make([]EvalCase, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, EvalCase{
			Input:    fmt.Sprintf("summarize workstream number %d for the weekly review", i),
			Expected: fmt.Sprintf("weekly review summary of workstream number %d with action items", i),
		})
	}
	return out
}

func evalCasesJSON(cases []EvalCase) string {
	raw, err := json.Marshal(cases)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func seedEvalJob(t *testing.T, s *Storage) (*EvolutionJob, string) {
	t.Helper()
	skill := seedSkill(t, s, "Eval Skill", "# eval skill body covering weekly review summary workstream action items")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	return job, token
}

func evalDirExists(s *Storage, jobID string) bool {
	dir, err := s.evalFilesDir(jobID)
	if err != nil {
		return false
	}
	_, err = os.Stat(dir)
	return err == nil
}

func TestEvalUploadAutoSplitValid(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "6")
	t.Setenv(EvalMinValEnv, "2")
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)

	// 8 cases auto-split: last quarter (2) to val, 6 to train.
	res, err := s.UploadEvolutionEvalSet(job.ID, token, "cases.json", evalCasesJSON(evalFixture(8)))
	if err != nil {
		t.Fatalf("UploadEvolutionEvalSet failed: %v", err)
	}
	if res.Meta.SplitMode != "auto" {
		t.Errorf("split mode = %q, want auto", res.Meta.SplitMode)
	}
	if res.Meta.TrainCount != 6 || res.Meta.ValCount != 2 {
		t.Errorf("splits = %d/%d, want 6/2", res.Meta.TrainCount, res.Meta.ValCount)
	}
	if res.Meta.EvalHash == "" {
		t.Error("eval hash must be recorded")
	}
	if res.Meta.ScorerVersion != SkillAuditScorerVersion {
		t.Errorf("scorer version = %q, want %q", res.Meta.ScorerVersion, SkillAuditScorerVersion)
	}
	if res.Meta.BaselineS0 < 0 || res.Meta.BaselineS0 > 1 {
		t.Errorf("S0 baseline = %v, want in [0,1]", res.Meta.BaselineS0)
	}
	if len(res.Meta.DryRun) != 2 { // only 2 val cases exist
		t.Errorf("dry run holds %d samples, want 2", len(res.Meta.DryRun))
	}
	if res.Meta.Estimate.BudgetEnforced {
		t.Error("cost estimate stub must never be enforced")
	}
	if res.Meta.Estimate.EstimatedValCases != 2 {
		t.Errorf("estimate val cases = %d, want 2", res.Meta.Estimate.EstimatedValCases)
	}

	// Stored splits round-trip with the recorded hash.
	train, val, meta, err := s.ReadEvolutionEvalSet(job.ID)
	if err != nil {
		t.Fatalf("ReadEvolutionEvalSet failed: %v", err)
	}
	if len(train) != 6 || len(val) != 2 {
		t.Fatalf("stored splits = %d/%d, want 6/2", len(train), len(val))
	}
	if meta.EvalHash != res.Meta.EvalHash || evalSetHash(train, val) != res.Meta.EvalHash {
		t.Error("stored splits must reproduce the recorded eval hash")
	}

	// The job record carries the summary.
	stored, err := s.GetEvolutionJob(job.ID)
	if err != nil {
		t.Fatalf("GetEvolutionJob failed: %v", err)
	}
	if stored.EvalHash != res.Meta.EvalHash || stored.EvalTrainCount != 6 || stored.EvalValCount != 2 {
		t.Errorf("job eval summary wrong: %+v", stored)
	}
	if stored.BaselineS0 != res.Meta.BaselineS0 || stored.BaselineScorer != SkillAuditScorerVersion {
		t.Errorf("job baseline wrong: S0=%v scorer=%q", stored.BaselineS0, stored.BaselineScorer)
	}
	if stored.EvalUploadedAt.IsZero() {
		t.Error("job must record the eval upload time")
	}
}

func TestEvalUploadFiveSampleDryRun(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "6")
	t.Setenv(EvalMinValEnv, "2")
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)

	// 24 cases auto-split → 18 train / 6 val; dry run caps at 5.
	res, err := s.UploadEvolutionEvalSet(job.ID, token, "cases.json", evalCasesJSON(evalFixture(24)))
	if err != nil {
		t.Fatalf("UploadEvolutionEvalSet failed: %v", err)
	}
	if len(res.Meta.DryRun) != 5 {
		t.Fatalf("dry run holds %d samples, want 5", len(res.Meta.DryRun))
	}
	for i, sample := range res.Meta.DryRun {
		if sample.Index != i {
			t.Errorf("dry run sample %d has index %d", i, sample.Index)
		}
		if sample.InputPreview == "" || sample.ExpectedPreview == "" {
			t.Errorf("dry run sample %d must quote previews", i)
		}
	}
}

func TestEvalUploadJSONUserSplits(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "4")
	t.Setenv(EvalMinValEnv, "2")
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)

	trainCases := evalFixture(4)
	valCases := evalFixture(0)
	for i := 100; i < 102; i++ {
		valCases = append(valCases, EvalCase{
			Input:    fmt.Sprintf("summarize workstream number %d for the weekly review", i),
			Expected: fmt.Sprintf("weekly review summary of workstream number %d with action items", i),
		})
	}
	envelope, _ := json.Marshal(map[string]interface{}{"train": trainCases, "val": valCases})
	res, err := s.UploadEvolutionEvalSet(job.ID, token, "splits.json", string(envelope))
	if err != nil {
		t.Fatalf("UploadEvolutionEvalSet failed: %v", err)
	}
	if res.Meta.SplitMode != "user" {
		t.Errorf("split mode = %q, want user", res.Meta.SplitMode)
	}
	if res.Meta.TrainCount != 4 || res.Meta.ValCount != 2 {
		t.Errorf("splits = %d/%d, want 4/2", res.Meta.TrainCount, res.Meta.ValCount)
	}
}

func TestEvalUploadCSVAndJSONL(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "6")
	t.Setenv(EvalMinValEnv, "2")
	s := newLifecycleStorage(t)

	job, token := seedEvalJob(t, s)
	var csvBody strings.Builder
	csvBody.WriteString("input,expected,scorer\n")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&csvBody, "\"summarize workstream number %d for review\",\"weekly review summary of workstream number %d\",v0\n", i, i)
	}
	if _, err := s.UploadEvolutionEvalSet(job.ID, token, "cases.csv", csvBody.String()); err != nil {
		t.Fatalf("CSV upload failed: %v", err)
	}

	skill := seedSkill(t, s, "Eval Skill Two", "# body weekly review summary workstream")
	job2, token2, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	var jsonlBody strings.Builder
	for i := 0; i < 8; i++ {
		raw, _ := json.Marshal(map[string]string{
			"input":    fmt.Sprintf("draft memo number %d for council briefing", i),
			"expected": fmt.Sprintf("council briefing memo number %d finalized", i),
		})
		jsonlBody.WriteString(string(raw) + "\n")
	}
	res, err := s.UploadEvolutionEvalSet(job2.ID, token2, "cases.jsonl", jsonlBody.String())
	if err != nil {
		t.Fatalf("JSONL upload failed: %v", err)
	}
	if res.Meta.TrainCount != 6 || res.Meta.ValCount != 2 {
		t.Errorf("JSONL splits = %d/%d, want 6/2", res.Meta.TrainCount, res.Meta.ValCount)
	}
}

func TestEvalUploadJSONLSplitFieldAndText(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "3")
	t.Setenv(EvalMinValEnv, "2")
	s := newLifecycleStorage(t)

	job, token := seedEvalJob(t, s)
	var body strings.Builder
	for i := 0; i < 3; i++ {
		raw, _ := json.Marshal(map[string]string{
			"input":    fmt.Sprintf("triage ticket number %d for support queue", i),
			"expected": fmt.Sprintf("support queue triage of ticket number %d done", i),
			"split":    "train",
		})
		body.WriteString(string(raw) + "\n")
	}
	for i := 50; i < 52; i++ {
		raw, _ := json.Marshal(map[string]string{
			"input":    fmt.Sprintf("triage ticket number %d for support queue", i),
			"expected": fmt.Sprintf("support queue triage of ticket number %d done", i),
			"split":    "val",
		})
		body.WriteString(string(raw) + "\n")
	}
	res, err := s.UploadEvolutionEvalSet(job.ID, token, "cases.jsonl", body.String())
	if err != nil {
		t.Fatalf("JSONL split-field upload failed: %v", err)
	}
	if res.Meta.SplitMode != "user" || res.Meta.TrainCount != 3 || res.Meta.ValCount != 2 {
		t.Errorf("splits = %d/%d mode %q, want 3/2 user", res.Meta.TrainCount, res.Meta.ValCount, res.Meta.SplitMode)
	}

	skill := seedSkill(t, s, "Eval Skill Three", "# body")
	job2, token2, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	var textBody strings.Builder
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&textBody, "outline chapter number %d for handbook ||| handbook chapter number %d outline approved\n", i, i)
	}
	res2, err := s.UploadEvolutionEvalSet(job2.ID, token2, "notes.txt", textBody.String())
	if err != nil {
		t.Fatalf("text upload failed: %v", err)
	}
	if res2.Meta.TrainCount != 6 || res2.Meta.ValCount != 2 {
		t.Errorf("text splits = %d/%d, want 6/2", res2.Meta.TrainCount, res2.Meta.ValCount)
	}
}

func TestEvalRefusesBadSchema(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "1")
	t.Setenv(EvalMinValEnv, "1")
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)

	bad := `[{"input": "has input but no expected"}, {"expected": "has expected but no input"}]`
	_, err := s.UploadEvolutionEvalSet(job.ID, token, "bad.json", bad)
	var verr *EvalValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *EvalValidationError, got %v", err)
	}
	joined := strings.Join(verr.Issues, "\n")
	if !strings.Contains(joined, `"expected"`) || !strings.Contains(joined, `"input"`) {
		t.Errorf("schema refusal must name the missing fields, got:\n%s", joined)
	}
	if evalDirExists(s, job.ID) {
		t.Error("refused upload must store nothing")
	}

	// Text lines without a separator are per-line schema errors.
	_, err = s.UploadEvolutionEvalSet(job.ID, token, "notes.txt", "just a line with no separator\n")
	if !errors.As(err, &verr) {
		t.Fatalf("expected *EvalValidationError for bad text, got %v", err)
	}
	if !strings.Contains(strings.Join(verr.Issues, "\n"), "line 1") {
		t.Errorf("text refusal must cite the line number, got: %v", verr.Issues)
	}
}

func TestEvalRefusesTooSmall(t *testing.T) {
	// Defaults: 30 train / 10 val. Three cases cannot pass.
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)

	_, err := s.UploadEvolutionEvalSet(job.ID, token, "tiny.json", evalCasesJSON(evalFixture(3)))
	var verr *EvalValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *EvalValidationError, got %v", err)
	}
	joined := strings.Join(verr.Issues, "\n")
	if !strings.Contains(joined, "too small") || !strings.Contains(joined, EvalMinTrainEnv) || !strings.Contains(joined, EvalMinValEnv) {
		t.Errorf("too-small refusal must cite counts and env knobs, got:\n%s", joined)
	}
	if evalDirExists(s, job.ID) {
		t.Error("refused upload must store nothing")
	}
}

func TestEvalRefusesDuplicates(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "2")
	t.Setenv(EvalMinValEnv, "1")
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)

	cases := evalFixture(4)
	cases = append(cases, cases[1]) // exact duplicate of row 2
	_, err := s.UploadEvolutionEvalSet(job.ID, token, "dupes.json", evalCasesJSON(cases))
	var verr *EvalValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *EvalValidationError, got %v", err)
	}
	if !strings.Contains(strings.Join(verr.Issues, "\n"), "duplicate") {
		t.Errorf("dupe refusal must say duplicate, got: %v", verr.Issues)
	}
	if evalDirExists(s, job.ID) {
		t.Error("refused upload must store nothing")
	}
}

// The dupe/leakage refusal lines quote case inputs. When an input itself
// carries a credential, quoting it back would reproduce the exact harm the
// secret scan exists to prevent, so the preview must be redacted to a
// class-named placeholder and the value must appear nowhere in the error.
func TestEvalDupeRefusalRedactsSecret(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "2")
	t.Setenv(EvalMinValEnv, "1")
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)

	secretInput := "review the deploy key AKIA1234567890ABCDEF for the weekly audit"
	cases := evalFixture(3)
	cases = append(cases,
		EvalCase{Input: secretInput, Expected: "the weekly audit review outcome"},
		EvalCase{Input: secretInput, Expected: "the weekly audit review outcome"},
	)
	_, err := s.UploadEvolutionEvalSet(job.ID, token, "dupe-secret.json", evalCasesJSON(cases))
	var verr *EvalValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *EvalValidationError, got %v", err)
	}
	joined := strings.Join(verr.Issues, "\n")
	if !strings.Contains(joined, "duplicate") {
		t.Errorf("duplicated set must be refused for the dupes, got:\n%s", joined)
	}
	if strings.Contains(joined, "AKIA1234567890ABCDEF") {
		t.Errorf("dupe refusal must never echo the secret value, got:\n%s", joined)
	}
	if !strings.Contains(joined, "<redacted: AWS access key ID>") {
		t.Errorf("dupe refusal must quote a class-named redaction placeholder, got:\n%s", joined)
	}
	if evalDirExists(s, job.ID) {
		t.Error("refused upload must store nothing")
	}
}

func TestEvalRefusesLeakage(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "2")
	t.Setenv(EvalMinValEnv, "2")
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)

	train := evalFixture(2)
	leaked := train[0]
	leaked.Expected = "a rewritten answer that still leaks the input"
	val := []EvalCase{leaked, {
		Input:    "a genuinely fresh input for the val split",
		Expected: "a genuinely fresh expected answer",
	}}
	envelope, _ := json.Marshal(map[string]interface{}{"train": train, "val": val})
	_, err := s.UploadEvolutionEvalSet(job.ID, token, "leak.json", string(envelope))
	var verr *EvalValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *EvalValidationError, got %v", err)
	}
	joined := strings.Join(verr.Issues, "\n")
	if !strings.Contains(joined, "leakage") && !strings.Contains(joined, "overlap") {
		t.Errorf("leakage refusal must name the overlap, got:\n%s", joined)
	}
	if !strings.Contains(joined, truncateQuoted(train[0].Input, evalLeakPreviewLen)) {
		t.Errorf("leakage refusal must quote the overlapping input, got:\n%s", joined)
	}
	if evalDirExists(s, job.ID) {
		t.Error("refused upload must store nothing")
	}
}

func TestEvalRefusesSecretsAndStoresNothing(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "2")
	t.Setenv(EvalMinValEnv, "1")
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)

	cases := evalFixture(3)
	cases[1].Expected = "rotate the deploy credential AKIA1234567890ABCDEF immediately"
	_, err := s.UploadEvolutionEvalSet(job.ID, token, "secret.json", evalCasesJSON(cases))
	var verr *EvalValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *EvalValidationError, got %v", err)
	}
	joined := strings.Join(verr.Issues, "\n")
	if !strings.Contains(joined, "AWS access key ID") {
		t.Errorf("secret refusal must name the credential class, got:\n%s", joined)
	}
	if strings.Contains(joined, "AKIA1234567890ABCDEF") {
		t.Error("refusal must never echo the secret value")
	}
	if evalDirExists(s, job.ID) {
		t.Error("secret-bearing upload must store nothing")
	}
	// The raw upload must not linger in the job record either.
	stored, _ := s.GetEvolutionJob(job.ID)
	if stored.EvalHash != "" {
		t.Error("job record must not reference a refused eval set")
	}
}

func TestEvalRefusesPII(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "2")
	t.Setenv(EvalMinValEnv, "1")
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)

	cases := evalFixture(3)
	cases[0].Input = "email jane.doe.8391@retail-mail.com the weekly wrap"
	_, err := s.UploadEvolutionEvalSet(job.ID, token, "pii.json", evalCasesJSON(cases))
	var verr *EvalValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *EvalValidationError, got %v", err)
	}
	if !strings.Contains(strings.Join(verr.Issues, "\n"), "email address") {
		t.Errorf("PII refusal must name the class, got: %v", verr.Issues)
	}
	if evalDirExists(s, job.ID) {
		t.Error("PII-bearing upload must store nothing")
	}
}

func TestEvalRefusesBadExtensionAndTerminal(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "1")
	t.Setenv(EvalMinValEnv, "1")
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)

	_, err := s.UploadEvolutionEvalSet(job.ID, token, "cases.exe", evalCasesJSON(evalFixture(2)))
	var verr *EvalValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *EvalValidationError for bad extension, got %v", err)
	}

	if _, err := s.ClaimEvolutionJob(job.ID, token, "runner"); err != nil {
		t.Fatalf("ClaimEvolutionJob failed: %v", err)
	}
	if _, err := s.CompleteEvolutionJob(job.ID, token, "complete", ""); err != nil {
		t.Fatalf("CompleteEvolutionJob failed: %v", err)
	}
	_, err = s.UploadEvolutionEvalSet(job.ID, token, "cases.json", evalCasesJSON(evalFixture(2)))
	if err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Errorf("upload to a terminal job must be refused, got %v", err)
	}
}

func TestEvalRequiresJobToken(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "1")
	t.Setenv(EvalMinValEnv, "1")
	s := newLifecycleStorage(t)
	job, _ := seedEvalJob(t, s)

	_, err := s.UploadEvolutionEvalSet(job.ID, "wrong-token", "cases.json", evalCasesJSON(evalFixture(2)))
	var authErr *JobAuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected *JobAuthError for a bad token, got %v", err)
	}
}

func TestEvalScopeGateAndTokenBypass(t *testing.T) {
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)
	srv := &Server{Storage: s, WikiskillRole: WikiskillRoleProposer}

	// An agent-phase role without the job token is denied the eval tool.
	withoutToken, _ := json.Marshal(map[string]string{"id": job.ID, "job_token": "", "filename": "a.json", "content": "[]"})
	if scopeErr := srv.checkWikiskillScope("upload_evolution_eval_set", withoutToken); scopeErr == nil {
		t.Error("proposer role without a job token must be denied upload_evolution_eval_set")
	} else if !strings.Contains(scopeErr.Error(), ScopeJobsManage) {
		t.Errorf("denial must name %q, got %v", ScopeJobsManage, scopeErr)
	}

	// The job's own token authorizes regardless of process role.
	withToken, _ := json.Marshal(map[string]string{"id": job.ID, "job_token": token, "filename": "a.json", "content": "[]"})
	if scopeErr := srv.checkWikiskillScope("upload_evolution_eval_set", withToken); scopeErr != nil {
		t.Errorf("job token must bypass the process role, got %v", scopeErr)
	}

	// A cross-job token is still denied (second skill: one run per skill).
	otherSkill := seedSkill(t, s, "Eval Skill Other", "# other body")
	other, otherToken, err := s.CreateEvolutionJob(otherSkill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	_ = other
	crossArgs, _ := json.Marshal(map[string]string{"id": job.ID, "job_token": otherToken, "filename": "a.json", "content": "[]"})
	if scopeErr := srv.checkWikiskillScope("upload_evolution_eval_set", crossArgs); scopeErr == nil {
		t.Error("a cross-job token must not authorize another job's eval upload")
	}
}

func TestEvalHashDeterministic(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "2")
	t.Setenv(EvalMinValEnv, "1")
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)
	content := evalCasesJSON(evalFixture(4))
	first, err := s.UploadEvolutionEvalSet(job.ID, token, "cases.json", content)
	if err != nil {
		t.Fatalf("first upload failed: %v", err)
	}

	skill := seedSkill(t, s, "Eval Skill Hash Two", "# body")
	job2, token2, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	second, err := s.UploadEvolutionEvalSet(job2.ID, token2, "cases.json", content)
	if err != nil {
		t.Fatalf("second upload failed: %v", err)
	}
	if first.Meta.EvalHash != second.Meta.EvalHash {
		t.Error("identical eval content must yield identical hashes")
	}

	// Re-uploading the same file to the same job is a stable replace.
	again, err := s.UploadEvolutionEvalSet(job.ID, token, "cases.json", content)
	if err != nil {
		t.Fatalf("re-upload failed: %v", err)
	}
	if again.Meta.EvalHash != first.Meta.EvalHash {
		t.Error("re-upload of identical content must reproduce the hash")
	}
}

func TestEvalMixedSplitsRefused(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "1")
	t.Setenv(EvalMinValEnv, "1")
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)

	lines := []string{
		`{"input": "alpha case input", "expected": "alpha expected", "split": "train"}`,
		`{"input": "beta case input", "expected": "beta expected"}`,
	}
	_, err := s.UploadEvolutionEvalSet(job.ID, token, "mixed.jsonl", strings.Join(lines, "\n"))
	var verr *EvalValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *EvalValidationError for mixed splits, got %v", err)
	}
	if !strings.Contains(strings.Join(verr.Issues, "\n"), "mixed split") {
		t.Errorf("mixed-split refusal must say so, got: %v", verr.Issues)
	}
}

func TestEvalToolHandlerEndToEnd(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "2")
	t.Setenv(EvalMinValEnv, "1")
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)
	srv := &Server{Storage: s}

	args, _ := json.Marshal(map[string]string{
		"id": job.ID, "job_token": token,
		"filename": "cases.json", "content": evalCasesJSON(evalFixture(4)),
	})
	resp, rpcErr := srv.toolUploadEvolutionEvalSet(args)
	if rpcErr != nil {
		t.Fatalf("handler RPC error: %v", rpcErr)
	}
	toolResp, ok := resp.(ToolResponse)
	if !ok || toolResp.IsError {
		t.Fatalf("handler must succeed, got %+v", resp)
	}
	text := toolResp.Content[0].Text
	for _, want := range []string{"Eval hash", "S0 baseline", "Dry run", "Cost estimate"} {
		if !strings.Contains(text, want) {
			t.Errorf("handler response must include %q, got:\n%s", want, text)
		}
	}

	// A refused upload surfaces as a tool error naming the issue.
	badArgs, _ := json.Marshal(map[string]string{
		"id": job.ID, "job_token": token,
		"filename": "tiny.json", "content": evalCasesJSON(evalFixture(1)),
	})
	resp, rpcErr = srv.toolUploadEvolutionEvalSet(badArgs)
	if rpcErr != nil {
		t.Fatalf("handler RPC error on refusal: %v", rpcErr)
	}
	toolResp, ok = resp.(ToolResponse)
	if !ok || !toolResp.IsError {
		t.Fatalf("refused upload must be a tool error, got %+v", resp)
	}
	if !strings.Contains(toolResp.Content[0].Text, "too small") {
		t.Errorf("refusal text must name the issue, got:\n%s", toolResp.Content[0].Text)
	}
}

func TestEvalInvalidEnvConfigFailsClosed(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "not-a-number")
	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s)

	_, err := s.UploadEvolutionEvalSet(job.ID, token, "cases.json", evalCasesJSON(evalFixture(4)))
	if err == nil || !strings.Contains(err.Error(), EvalMinTrainEnv) {
		t.Errorf("bad env config must fail closed naming %s, got %v", EvalMinTrainEnv, err)
	}
	if evalDirExists(s, job.ID) {
		t.Error("config refusal must store nothing")
	}
}

// The MCP registry test pins the count; this pins the new tool's place in it.
func TestEvalToolRegisteredWithJobScope(t *testing.T) {
	s := newLifecycleStorage(t)
	srv := &Server{Storage: s}
	def, ok := toolsByName["upload_evolution_eval_set"]
	if !ok || def.Handler == nil {
		t.Fatal("upload_evolution_eval_set must be registered with a handler")
	}
	scopes := srv.requiredWikiskillScopes("upload_evolution_eval_set", json.RawMessage(`{}`))
	if len(scopes) != 1 || scopes[0] != ScopeJobsManage {
		t.Errorf("eval upload must require %q, got %v", ScopeJobsManage, scopes)
	}
	if !jobLifecycleTokenTools["upload_evolution_eval_set"] {
		t.Error("eval upload must accept the job's own token like the other lifecycle tools")
	}
}

func TestEvalBaselineSensitiveToSkillBody(t *testing.T) {
	t.Setenv(EvalMinTrainEnv, "2")
	t.Setenv(EvalMinValEnv, "1")
	content := evalCasesJSON(evalFixture(4))

	s := newLifecycleStorage(t)
	job, token := seedEvalJob(t, s) // skill body covers the fixture vocabulary
	covered, err := s.UploadEvolutionEvalSet(job.ID, token, "cases.json", content)
	if err != nil {
		t.Fatalf("covered upload failed: %v", err)
	}

	bare := seedSkill(t, s, "Bare Skill", "# unrelated body about pottery glazing kilns")
	job2, token2, err := s.CreateEvolutionJob(bare.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	uncovered, err := s.UploadEvolutionEvalSet(job2.ID, token2, "cases.json", content)
	if err != nil {
		t.Fatalf("bare upload failed: %v", err)
	}
	if covered.Meta.BaselineS0 <= uncovered.Meta.BaselineS0 {
		t.Errorf("S0 must reflect skill coverage: covering=%v bare=%v", covered.Meta.BaselineS0, uncovered.Meta.BaselineS0)
	}
	if got := filepath.Base(covered.TrainPath); got != "train.jsonl" {
		t.Errorf("train path base = %q", got)
	}
}

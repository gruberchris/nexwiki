package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This file tests the story 13 operator-run endpoints in
// skill_wizard_operator_http.go: the wizard's train/start, eval upload
// (through the REAL story-05 gate), and loop dispatch. The pieces underneath
// are stories 04/05/07 unchanged; these tests pin the HTTP shapes the wizard
// consumes, the auth/attribution contract, the once-only token flow, and the
// verbatim gate refusals.

// operatorSkill seeds one skill for the operator tests.
func operatorSkill(t *testing.T, srv *Server, title string) *Article {
	t.Helper()
	art, err := srv.Storage.SaveArticle("", title, "# "+title+"\n\nA skill for the wizard flow.", "", "", "", "", []string{"aiagent-skill"}, ContentTypeSkill)
	if err != nil {
		t.Fatalf("SaveArticle failed: %v", err)
	}
	return art
}

// postOperator POSTs a JSON body to a handler with one path value set.
func postOperator(t *testing.T, srv *Server, handler func(http.ResponseWriter, *http.Request), path, pathKey, pathValue string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest("POST", path, rdr)
	req.SetPathValue(pathKey, pathValue)
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestHandleStartSkillTraining(t *testing.T) {
	srv := newTestServer(t)
	operatorSkill(t, srv, "Stability Protocol")

	// First train/start: 201 with the job view and the token, job queued.
	w := postOperator(t, srv, srv.HandleStartSkillTraining, "/api/skills/stability-protocol/train/start", "slug", "stability-protocol", "")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var start WizardTrainStartResponse
	if err := json.Unmarshal(w.Body.Bytes(), &start); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if start.Job.SkillSlug != "stability-protocol" || start.Job.Status != EvolutionJobQueued {
		t.Errorf("job view wrong: %+v", start.Job)
	}
	if strings.Contains(w.Body.String(), "token_hash") {
		t.Errorf("start response must not carry token_hash: %s", w.Body.String())
	}
	if len(start.Token) < 32 {
		t.Errorf("expected a real harness token, got %q", start.Token)
	}

	// Nothing secret is persisted: the job record on disk carries the hash,
	// never the raw token.
	jobPath := filepath.Join(srv.Storage.JobDir, start.Job.ID+".json")
	raw, err := os.ReadFile(jobPath)
	if err != nil {
		t.Fatalf("job record missing: %v", err)
	}
	if strings.Contains(string(raw), start.Token) {
		t.Error("raw token must never land in the job record")
	}
	var job EvolutionJob
	if err := json.Unmarshal(raw, &job); err != nil {
		t.Fatalf("job record unreadable: %v", err)
	}
	if job.TokenHash == "" {
		t.Error("job record must still carry the token hash")
	}

	// One run per skill: the second start refuses with 409 naming the live job.
	w2 := postOperator(t, srv, srv.HandleStartSkillTraining, "/api/skills/stability-protocol/train/start", "slug", "stability-protocol", "")
	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409 on the second start, got %d: %s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(w2.Body.String(), "already has an active job") {
		t.Errorf("confusal body must name the lock: %s", w2.Body.String())
	}

	// Unknown skill and non-skill slugs read 404, not 500.
	_, _ = srv.Storage.SaveArticle("", "Plain Article", "# content", "", "", "", "", nil, ContentTypeWiki)
	for _, slug := range []string{"ghost-skill", "plain-article"} {
		w3 := postOperator(t, srv, srv.HandleStartSkillTraining, "/api/skills/"+slug+"/train/start", "slug", slug, "")
		if w3.Code != http.StatusNotFound {
			t.Errorf("slug %s: expected 404, got %d", slug, w3.Code)
		}
	}

	// A typo'd injection mode is a 400 naming the choice, not a silent fallback.
	if _, err := srv.Storage.CancelEvolutionJob(start.Job.ID, "test teardown"); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	w5 := postOperator(t, srv, srv.HandleStartSkillTraining, "/api/skills/stability-protocol/train/start", "slug", "stability-protocol", `{"injection_mode":"retreive"}`)
	if w5.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid injection mode, got %d: %s", w5.Code, w5.Body.String())
	}
	if !strings.Contains(w5.Body.String(), "invalid injection_mode") {
		t.Errorf("400 body must name the mode error: %s", w5.Body.String())
	}
}

func TestHandleStartSkillTrainingAttribution(t *testing.T) {
	srv := newTestServer(t)
	operatorSkill(t, srv, "Attribution Skill")
	_ = postOperator(t, srv, srv.HandleStartSkillTraining, "/api/skills/attribution-skill/train/start", "slug", "attribution-skill", "")

	// The creation lands in the activity log as an api action over the job,
	// attributed like every other operator action.
	found := false
	srv.EventBus.mu.Lock()
	for _, ev := range srv.EventBus.buffer {
		if ev.Source == "api" && ev.Action == "create" && ev.Slug != "" && strings.HasPrefix(ev.Slug, "job-attribution-skill") {
			found = true
		}
	}
	srv.EventBus.mu.Unlock()
	if !found {
		t.Error("train/start did not land its api/create activity event")
	}
}

func TestHandleStartSkillTrainingNoTokenInLogsOrDisk(t *testing.T) {
	srv := newTestServer(t)
	operatorSkill(t, srv, "Log Safety Skill")

	// Capture everything the server writes to Stderr during the flow.
	capture, err := os.Create(filepath.Join(t.TempDir(), "stderr.log"))
	if err != nil {
		t.Fatalf("stderr capture failed: %v", err)
	}
	oldStderr := os.Stderr
	os.Stderr = capture
	defer func() { os.Stderr = oldStderr; _ = capture.Close() }()

	w := postOperator(t, srv, srv.HandleStartSkillTraining, "/api/skills/log-safety-skill/train/start", "slug", "log-safety-skill", "")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	var start WizardTrainStartResponse
	if err := json.Unmarshal(w.Body.Bytes(), &start); err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	_ = capture.Sync()
	raw, err := os.ReadFile(capture.Name())
	if err != nil {
		t.Fatalf("read capture failed: %v", err)
	}
	if strings.Contains(string(raw), start.Token) {
		t.Errorf("the token must never reach Stderr logs: %s", string(raw))
	}

	// And the durable activity queue carries no token either.
	srv.EventBus.mu.Lock()
	for _, ev := range srv.EventBus.buffer {
		if strings.Contains(ev.Slug, start.Token) || strings.Contains(ev.Title, start.Token) {
			t.Errorf("activity event carries the token: %+v", ev)
		}
	}
	srv.EventBus.mu.Unlock()
}

func TestHandleUploadEvolutionEvalThroughRealGate(t *testing.T) {
	srv := newTestServer(t)
	operatorSkill(t, srv, "Eval Gate Skill")
	t.Setenv(EvalMinTrainEnv, "2")
	t.Setenv(EvalMinValEnv, "1")
	job, token, err := srv.Storage.CreateEvolutionJob("eval-gate-skill", "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	srv.rememberJobToken(job.ID, token)

	content := fmt.Sprintf(`{"train":[{"input":"train one","expected":"flow","scorer":%[1]q},{"input":"train two","expected":"omega","scorer":%[1]q}],"val":[{"input":"val one","expected":"wizard","scorer":%[1]q}]}`, `overlap(expected, output) >= 0.5`)
	w := postOperator(t, srv, srv.HandleUploadEvolutionEval, "/api/evolution/jobs/"+job.ID+"/eval", "id", job.ID,
		fmt.Sprintf(`{"filename":"cases.json","content":%q}`, content))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var meta EvalMeta
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if meta.TrainCount != 2 || meta.ValCount != 1 || meta.EvalHash == "" {
		t.Errorf("meta wrong: %+v", meta)
	}
	// S0 is scored at upload by the eval's own scorers: the skill body names
	// "wizard", so the val case scores a full 1 and S0 reads 1.
	if meta.BaselineS0 != 1 {
		t.Errorf("the gate must record the S0 baseline at upload, got %v: %+v", meta.BaselineS0, meta)
	}
	if meta.ScorerVersion == "" || meta.ScorerVersion == "v0" {
		t.Errorf("a custom scorer must derive a versioned scorer set: %+v", meta)
	}

	// The job record reads readiness, exactly as the storage path stamps it.
	fresh, err := srv.Storage.GetEvolutionJob(job.ID)
	if err != nil {
		t.Fatalf("GetEvolutionJob failed: %v", err)
	}
	if fresh.EvalHash != meta.EvalHash || fresh.EvalTrainCount != 2 {
		t.Errorf("job record not stamped: %+v", fresh)
	}

	// The stored meta is served by the story-08 read endpoint unchanged.
	req := httptest.NewRequest("GET", "/api/evolution/jobs/"+job.ID+"/eval", nil)
	req.SetPathValue("id", job.ID)
	wr := httptest.NewRecorder()
	srv.HandleGetEvolutionEval(wr, req)
	if wr.Code != http.StatusOK {
		t.Fatalf("GET eval: expected 200, got %d", wr.Code)
	}
	var served EvalMeta
	if err := json.Unmarshal(wr.Body.Bytes(), &served); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if served.EvalHash != meta.EvalHash {
		t.Errorf("served meta disagrees: %+v", served)
	}
}

func TestHandleUploadEvolutionEvalIssuesVerbatim(t *testing.T) {
	srv := newTestServer(t)
	operatorSkill(t, srv, "Verbatim Gate Skill")
	t.Setenv(EvalMinTrainEnv, "2")
	t.Setenv(EvalMinValEnv, "1")
	job, token, err := srv.Storage.CreateEvolutionJob("verbatim-gate-skill", "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	srv.rememberJobToken(job.ID, token)

	// One duplicate pair and one leaked input: the gate refuses with
	// per-issue fix-its. A single issue per category keeps the list order
	// deterministic, so the verbatim comparison below is exact.
	content := `{"train":[{"input":"one","expected":"alpha"},{"input":"one","expected":"alpha"}],"val":[{"input":"one","expected":"beta"},{"input":"two","expected":"gamma"}]}`
	w := postOperator(t, srv, srv.HandleUploadEvolutionEval, "/api/evolution/jobs/"+job.ID+"/eval", "id", job.ID,
		fmt.Sprintf(`{"filename":"cases.json","content":%q}`, content))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var issues WizardEvalIssues
	if err := json.Unmarshal(w.Body.Bytes(), &issues); err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	// The SAME input through the SAME gate functions must produce the SAME
	// issue list — the REST surface adds no re-implementation.
	rows, perr := parseEvalContent("cases.json", content)
	if perr != nil {
		t.Fatalf("parseEvalContent refused: %v", perr)
	}
	minTrain, minVal, cerr := evalMinCounts()
	if cerr != nil {
		t.Fatalf("evalMinCounts failed: %v", cerr)
	}
	_, gerr := gateEvalSet(rows, minTrain, minVal)
	var gate *EvalValidationError
	if !errors.As(gerr, &gate) {
		t.Fatalf("expected a gate refusal, got %v", gerr)
	}
	if len(issues.Issues) != len(gate.Issues) {
		t.Fatalf("issue count mismatch: %d served vs %d gate\nserved: %v\ngate: %v", len(issues.Issues), len(gate.Issues), issues.Issues, gate.Issues)
	}
	for i := range gate.Issues {
		if issues.Issues[i] != gate.Issues[i] {
			t.Errorf("issue %d not verbatim:\nserved: %s\ngate:   %s", i, issues.Issues[i], gate.Issues[i])
		}
	}
	if issues.Parsed != gate.Parsed {
		t.Errorf("parsed count mismatch: %d vs %d", issues.Parsed, gate.Parsed)
	}

	// Nothing was stored: the job record stays eval-free and the read
	// endpoint still answers 404.
	fresh, err := srv.Storage.GetEvolutionJob(job.ID)
	if err != nil {
		t.Fatalf("GetEvolutionJob failed: %v", err)
	}
	if fresh.EvalHash != "" {
		t.Errorf("a refused upload must store nothing: %+v", fresh)
	}
	req := httptest.NewRequest("GET", "/api/evolution/jobs/"+job.ID+"/eval", nil)
	req.SetPathValue("id", job.ID)
	wr := httptest.NewRecorder()
	srv.HandleGetEvolutionEval(wr, req)
	if wr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for the refused upload, got %d", wr.Code)
	}
}

func TestHandleUploadEvolutionEvalAuth(t *testing.T) {
	srv := newTestServer(t)
	operatorSkill(t, srv, "Eval Auth Skill")
	t.Setenv(EvalMinTrainEnv, "1")
	t.Setenv(EvalMinValEnv, "1")
	job, token, err := srv.Storage.CreateEvolutionJob("eval-auth-skill", "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	content := `{"train":[{"input":"one","expected":"alpha"}],"val":[{"input":"two","expected":"beta"}]}`

	// The token was minted storage-side, so the server's cache is empty: a
	// call with neither a cached nor a supplied token is refused with 403.
	w := postOperator(t, srv, srv.HandleUploadEvolutionEval, "/api/evolution/jobs/"+job.ID+"/eval", "id", job.ID,
		fmt.Sprintf(`{"filename":"cases.json","content":%q}`, content))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without a known token, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "access denied") {
		t.Errorf("403 body must name the denial: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), token) {
		t.Errorf("the denial must not leak the correct token: %s", w.Body.String())
	}

	// A wrong supplied token is refused the same way.
	w2 := postOperator(t, srv, srv.HandleUploadEvolutionEval, "/api/evolution/jobs/"+job.ID+"/eval", "id", job.ID,
		fmt.Sprintf(`{"filename":"cases.json","content":%q,"job_token":%q}`, content, "wrong-token"))
	if w2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a wrong token, got %d", w2.Code)
	}

	// The operator's own token — the one the start response returned once —
	// unlocks the after-a-restart path.
	w3 := postOperator(t, srv, srv.HandleUploadEvolutionEval, "/api/evolution/jobs/"+job.ID+"/eval", "id", job.ID,
		fmt.Sprintf(`{"filename":"cases.json","content":%q,"job_token":%q}`, content, token))
	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200 with the operator-supplied token, got %d: %s", w3.Code, w3.Body.String())
	}
}

func TestHandleUploadEvolutionEvalTerminalJob(t *testing.T) {
	srv := newTestServer(t)
	operatorSkill(t, srv, "Terminal Eval Skill")
	job, token, err := srv.Storage.CreateEvolutionJob("terminal-eval-skill", "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	srv.rememberJobToken(job.ID, token)
	if _, err := srv.Storage.CancelEvolutionJob(job.ID, "test"); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	w := postOperator(t, srv, srv.HandleUploadEvolutionEval, "/api/evolution/jobs/"+job.ID+"/eval", "id", job.ID, `{"filename":"cases.json","content":"x"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 on a terminal job, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "already terminal") {
		t.Errorf("409 body must name the terminal status: %s", w.Body.String())
	}
}

// waitDispatchDone polls until the server's dispatch marker clears and the
// job went terminal, failing the test on timeout.
func waitDispatchDone(t *testing.T, srv *Server, jobID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !srv.dispatchInFlight(jobID) {
			job, err := srv.Storage.GetEvolutionJob(jobID)
			if err != nil {
				t.Fatalf("GetEvolutionJob failed: %v", err)
			}
			if evolutionJobTerminal(job.Status) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("dispatch never finished within the test deadline")
}

func TestHandleDispatchEvolutionJobRunsTheLoop(t *testing.T) {
	srv := newTestServer(t)
	operatorSkill(t, srv, "Dispatch Flow Skill")
	t.Setenv(EvalMinTrainEnv, "1")
	t.Setenv(EvalMinValEnv, "1")
	job, token, err := srv.Storage.CreateEvolutionJob("dispatch-flow-skill", "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	srv.rememberJobToken(job.ID, token)

	// A completing harness: the fake CLI echoes its token (to prove the
	// scrub still runs on the wizard path) and ends the run immediately.
	binDir := fakeCLI(t, `{"type":"complete","outcome":"complete"}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	srv.wizardRunner = testRunner(t, srv.Storage, CLIProfile{Binary: "opencode"})

	// Stderr capture for the never-in-logs acceptance.
	capture, err := os.Create(filepath.Join(t.TempDir(), "stderr.log"))
	if err != nil {
		t.Fatalf("stderr capture failed: %v", err)
	}
	oldStderr := os.Stderr
	os.Stderr = capture
	defer func() { os.Stderr = oldStderr; _ = capture.Close() }()

	w := postOperator(t, srv, srv.HandleDispatchEvolutionJob, "/api/evolution/jobs/"+job.ID+"/dispatch", "id", job.ID, `{"task":"improve the skill"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp EvolutionLoopResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if resp.MaxIterations != evolutionJobMaxIterations || resp.PlateauLimit != evolutionPlateauDefault {
		t.Errorf("loop snapshot wrong: %+v", resp)
	}

	waitDispatchDone(t, srv, job.ID)
	fresh, err := srv.Storage.GetEvolutionJob(job.ID)
	if err != nil {
		t.Fatalf("GetEvolutionJob failed: %v", err)
	}
	if fresh.Status != EvolutionJobComplete {
		t.Errorf("expected a complete run, got %s (%s)", fresh.Status, fresh.Error)
	}
	if fresh.LoopOutcome != LoopOutcomeCompleted {
		t.Errorf("expected the completed loop outcome, got %q", fresh.LoopOutcome)
	}

	// The harness echoed the token into its captured stdout; the runner must
	// have scrubbed it, and nothing else may have logged it.
	_ = capture.Sync()
	logged, err := os.ReadFile(capture.Name())
	if err != nil {
		t.Fatalf("read capture failed: %v", err)
	}
	if strings.Contains(string(logged), token) {
		t.Errorf("the token reached Stderr: %s", string(logged))
	}
	stdoutLog, err := os.ReadFile(filepath.Join(srv.Storage.JobDir, job.ID+".files", "step-1-inference.stdout.log"))
	if err == nil && strings.Contains(string(stdoutLog), token) {
		t.Error("the captured step log still carries the raw token — scrub failed")
	}
}

func TestHandleDispatchEvolutionJobResumesPaused(t *testing.T) {
	srv := newTestServer(t)
	operatorSkill(t, srv, "Resume Flow Skill")
	t.Setenv(EvalMinTrainEnv, "1")
	t.Setenv(EvalMinValEnv, "1")
	job, token, err := srv.Storage.CreateEvolutionJob("resume-flow-skill", "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	srv.rememberJobToken(job.ID, token)
	if _, err := srv.Storage.PauseEvolutionJob(job.ID, "checkpoint one"); err != nil {
		t.Fatalf("PauseEvolutionJob failed: %v", err)
	}

	binDir := fakeCLI(t, `{"type":"complete","outcome":"complete"}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	srv.wizardRunner = testRunner(t, srv.Storage, CLIProfile{Binary: "opencode"})

	w := postOperator(t, srv, srv.HandleDispatchEvolutionJob, "/api/evolution/jobs/"+job.ID+"/dispatch", "id", job.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	waitDispatchDone(t, srv, job.ID)
	fresh, err := srv.Storage.GetEvolutionJob(job.ID)
	if err != nil {
		t.Fatalf("GetEvolutionJob failed: %v", err)
	}
	if fresh.Status != EvolutionJobComplete {
		t.Errorf("a paused run must resume and complete, got %s", fresh.Status)
	}
	if fresh.Checkpoint != "checkpoint one" {
		t.Errorf("resume must keep the checkpoint: %+v", fresh)
	}
}

func TestHandleDispatchEvolutionJobRefusals(t *testing.T) {
	srv := newTestServer(t)
	operatorSkill(t, srv, "Refusal Skill")
	job, token, err := srv.Storage.CreateEvolutionJob("refusal-skill", "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	srv.rememberJobToken(job.ID, token)

	// A claimed job belongs to whoever claimed it: dispatch refuses with 409.
	if _, err := srv.Storage.ClaimEvolutionJob(job.ID, token, "external-harness"); err != nil {
		t.Fatalf("ClaimEvolutionJob failed: %v", err)
	}
	w := postOperator(t, srv, srv.HandleDispatchEvolutionJob, "/api/evolution/jobs/"+job.ID+"/dispatch", "id", job.ID, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 on a claimed job, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "already claimed") {
		t.Errorf("409 body must name the claim: %s", w.Body.String())
	}

	// A terminal job is refused — history is not rewritten.
	if _, err := srv.Storage.CancelEvolutionJob(job.ID, "test"); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	w2 := postOperator(t, srv, srv.HandleDispatchEvolutionJob, "/api/evolution/jobs/"+job.ID+"/dispatch", "id", job.ID, "")
	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409 on a terminal job, got %d", w2.Code)
	}

	// An unknown job is 404.
	w3 := postOperator(t, srv, srv.HandleDispatchEvolutionJob, "/api/evolution/jobs/ghost/dispatch", "id", "ghost", "")
	if w3.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown job, got %d", w3.Code)
	}
}

func TestHandleDispatchEvolutionJobDoubleDispatch(t *testing.T) {
	srv := newTestServer(t)
	operatorSkill(t, srv, "Double Dispatch Skill")
	job, token, err := srv.Storage.CreateEvolutionJob("double-dispatch-skill", "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	srv.rememberJobToken(job.ID, token)

	// A harness that hangs past the step timeout: the first dispatch stays
	// in flight long enough for a second one to arrive.
	binDir := t.TempDir()
	hangScript := "#!/bin/sh\necho \"saw token=$NEXWIKI_JOB_TOKEN\"\nsleep 5\n"
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(hangScript), 0755); err != nil {
		t.Fatalf("hang CLI failed: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	srv.wizardRunner = testRunner(t, srv.Storage, CLIProfile{Binary: "opencode", StepTimeoutSecs: 1})

	w := postOperator(t, srv, srv.HandleDispatchEvolutionJob, "/api/evolution/jobs/"+job.ID+"/dispatch", "id", job.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !srv.dispatchInFlight(job.ID) {
		t.Fatal("the first dispatch must be marked in flight")
	}

	w2 := postOperator(t, srv, srv.HandleDispatchEvolutionJob, "/api/evolution/jobs/"+job.ID+"/dispatch", "id", job.ID, "")
	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409 on the second dispatch, got %d: %s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(w2.Body.String(), "already driving") {
		t.Errorf("409 body must name the in-flight dispatch: %s", w2.Body.String())
	}

	// The hung step ends at its 1s timeout and fails the run; the marker
	// clears so a later dispatch is possible again (a new run, by then).
	waitDispatchDone(t, srv, job.ID)
	fresh, err := srv.Storage.GetEvolutionJob(job.ID)
	if err != nil {
		t.Fatalf("GetEvolutionJob failed: %v", err)
	}
	if fresh.Status != EvolutionJobFailed {
		t.Errorf("expected the hung step to fail the run, got %s", fresh.Status)
	}
}

func TestHandleDispatchEvolutionJobAttribution(t *testing.T) {
	srv := newTestServer(t)
	operatorSkill(t, srv, "Dispatch Audit Skill")
	t.Setenv(EvalMinTrainEnv, "1")
	t.Setenv(EvalMinValEnv, "1")
	job, token, err := srv.Storage.CreateEvolutionJob("dispatch-audit-skill", "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	srv.rememberJobToken(job.ID, token)
	binDir := fakeCLI(t, `{"type":"complete","outcome":"complete"}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	srv.wizardRunner = testRunner(t, srv.Storage, CLIProfile{Binary: "opencode"})

	_ = postOperator(t, srv, srv.HandleDispatchEvolutionJob, "/api/evolution/jobs/"+job.ID+"/dispatch", "id", job.ID, "")
	waitDispatchDone(t, srv, job.ID)

	found := false
	srv.EventBus.mu.Lock()
	for _, ev := range srv.EventBus.buffer {
		if ev.Source == "api" && ev.Action == "dispatch" && ev.Slug == job.ID {
			found = true
		}
	}
	srv.EventBus.mu.Unlock()
	if !found {
		t.Error("dispatch did not land its api/dispatch activity event")
	}
}

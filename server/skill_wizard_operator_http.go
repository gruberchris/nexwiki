package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// This file holds story 13 of the wikiskill evolution support plan: the
// operator-run REST endpoints that make the evolution wizard user-initiated
// at every step. Story 08's seam (skill_wizard_http.go) was deliberately
// read-mostly — job creation, eval upload, and dispatch stayed harness-side
// over MCP — and live testing showed exactly where that leaves a browser
// operator: stuck on the wizard's first step, waiting on an unseen process.
// This file closes that gap with three thin orchestrations of the existing
// storage and runner functions; it introduces no new state machines, no new
// gate math, and no bypass of the story-05 gate or the story-04/07 lifecycle:
//
//	POST /api/skills/{slug}/train/start    — CreateEvolutionJobWithMode (the
//	  skill lock and the one-run-per-skill refusal ride along unchanged).
//	POST /api/evolution/jobs/{id}/eval     — UploadEvolutionEvalSet, the REAL
//	  story-05 gate, 1:1; per-issue refusals surface verbatim.
//	POST /api/evolution/jobs/{id}/dispatch — Runner.RunLoop in a background
//	  goroutine, the story-07 stepper claiming and advancing exactly as the
//	  harness-side dispatch path does (the sweep runs first, a paused run
//	  resumes first, every drive is audited).
//
// THE TOKEN FLOW: train/start mints the per-harness token (story 04) and
// returns it ONCE in the response body, for the operator who prefers their
// own CLI. The server keeps the raw token in memory only (wizardJobTokens on
// Server) so its own eval-upload and dispatch calls can present it — nothing
// secret is ever persisted (the job record keeps only the SHA-256 hash, as
// story 04 built it) and nothing is ever logged. A server restart loses the
// cache by design; the operator's tab still holds the one the start response
// returned and may pass it back as "job_token" in the request body, where it
// is verified against the job's hash exactly like any harness credential.
//
// Like every REST surface this file changes, the routes are registered in
// main.go next to the /api/skills registry and documented in
// docs/aiagent_skills.md.

// WizardTrainStartResponse is the body of a successful train/start: the new
// job view plus the per-harness token, returned ONCE — the server keeps it in
// memory for its own dispatch/eval calls, and nothing persists or logs it.
type WizardTrainStartResponse struct {
	Job   EvolutionJobView `json:"job"`
	Token string           `json:"token"`
}

// wizardTrainStartRequest is the optional body of train/start. Everything
// defaults: the profile falls back to the runner's default, and the injection
// mode falls back to "all". No secret fields, nothing else.
type wizardTrainStartRequest struct {
	Profile       string `json:"profile,omitempty"`
	InjectionMode string `json:"injection_mode,omitempty"`
}

// HandleStartSkillTraining creates the evolution job server-side — the
// wizard's step-2 "Start training run" button. One run per skill: an active
// job on the same skill refuses with 409 naming the existing run, exactly
// like the storage-level lock. The browser is the operator, so the activity
// trail reads "api" / "create" like every other operator action.
func (srv *Server) HandleStartSkillTraining(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "skill slug is required")
		return
	}
	var req wizardTrainStartRequest
	if r.Body != nil {
		if derr := json.NewDecoder(r.Body).Decode(&req); derr != nil && !errors.Is(derr, io.EOF) {
			writeDecodeError(w, derr)
			return
		}
	}
	job, token, err := srv.Storage.CreateEvolutionJobWithMode(slug, "", req.Profile, req.InjectionMode)
	if err != nil {
		writeError(w, wizardJobErrorStatus(err), err.Error())
		return
	}
	// The raw token is kept in memory for the server's own eval/dispatch calls
	// and returned once to the browser. It is never persisted and never logged.
	srv.rememberJobToken(job.ID, token)
	srv.EventBus.PublishActivity("api", "create", "", job.ID, job.SkillSlug, DefaultAgentName)
	writeJSON(w, http.StatusCreated, WizardTrainStartResponse{Job: viewOfJob(job), Token: token})
}

// wizardEvalUploadRequest is the body of the eval POST: the SAME payloads the
// MCP upload_evolution_eval_set takes (filename + content), plus an optional
// job_token for the after-a-restart case above.
type wizardEvalUploadRequest struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
	JobToken string `json:"job_token,omitempty"`
}

// WizardEvalIssues is the per-issue fix-it list the story-05 gate refuses
// with, surfaced verbatim so the wizard can render one fix per line. Nothing
// was stored when this is returned.
type WizardEvalIssues struct {
	Error      string   `json:"error"`
	Issues     []string `json:"issues"`
	Parsed     int      `json:"parsed"`
	TrainCount int      `json:"train_count"`
	ValCount   int      `json:"val_count"`
	SplitMode  string   `json:"split_mode"`
}

// HandleUploadEvolutionEval uploads the training-data file through the REAL
// story-05 gate (skill_eval.go), 1:1 — the same parsing, the same
// secret/PII/dedupe/leakage checks, the same S0 baseline computed at upload.
// A gate refusal answers 400 with every issue found in one pass, verbatim.
func (srv *Server) HandleUploadEvolutionEval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "evolution job id is required")
		return
	}
	var req wizardEvalUploadRequest
	if r.Body != nil {
		if derr := json.NewDecoder(r.Body).Decode(&req); derr != nil && !errors.Is(derr, io.EOF) {
			writeDecodeError(w, derr)
			return
		}
	}
	job, err := srv.Storage.GetEvolutionJob(id)
	if err != nil {
		writeError(w, wizardJobErrorStatus(err), err.Error())
		return
	}
	token, tokErr := srv.wizardJobToken(job, req.JobToken)
	if tokErr != nil {
		writeError(w, http.StatusForbidden, tokErr.Error())
		return
	}
	res, err := srv.Storage.UploadEvolutionEvalSet(job.ID, token, req.Filename, req.Content)
	if err != nil {
		var valErr *EvalValidationError
		if errors.As(err, &valErr) {
			writeJSON(w, http.StatusBadRequest, WizardEvalIssues{
				Error:      valErr.Error(),
				Issues:     valErr.Issues,
				Parsed:     valErr.Parsed,
				TrainCount: valErr.TrainCount,
				ValCount:   valErr.ValCount,
				SplitMode:  valErr.SplitMode,
			})
			return
		}
		writeError(w, wizardJobErrorStatus(err), err.Error())
		return
	}
	srv.EventBus.PublishActivity("api", "edit", "", job.ID, job.SkillSlug, DefaultAgentName)
	writeJSON(w, http.StatusOK, res.Meta)
}

// wizardDispatchRequest is the optional body of dispatch: the operator's
// task line (the ONLY instructions the harness may follow — the JobInput
// boundary from skill_runner.go) and an optional job_token for the
// after-a-restart case.
type wizardDispatchRequest struct {
	Task     string `json:"task,omitempty"`
	JobToken string `json:"job_token,omitempty"`
}

// HandleDispatchEvolutionJob starts the loop server-side: the story-04/07
// dispatch path (sweep → claim → phase steps → server-side gate) driven by a
// background goroutine, so the browser never waits on the run. A paused run
// resumes first (the wizard's Resume control is this same endpoint), and
// every dispatch is audited like the pause/abort/approve controls.
func (srv *Server) HandleDispatchEvolutionJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "evolution job id is required")
		return
	}
	var req wizardDispatchRequest
	if r.Body != nil {
		if derr := json.NewDecoder(r.Body).Decode(&req); derr != nil && !errors.Is(derr, io.EOF) {
			writeDecodeError(w, derr)
			return
		}
	}
	job, err := srv.Storage.GetEvolutionJob(id)
	if err != nil {
		writeError(w, wizardJobErrorStatus(err), err.Error())
		return
	}
	// In-flight check BEFORE the sweep: the sweep requeues lease-expired jobs,
	// which must never touch a run this very server is driving.
	if srv.dispatchInFlight(job.ID) {
		writeError(w, http.StatusConflict, fmt.Sprintf("cannot dispatch evolution job '%s': the server is already driving its loop", job.ID))
		return
	}
	// The story-04 sweep runs before every dispatch: expired leases requeue,
	// past-deadline jobs go terminal. A crashed dispatch re-dispatches cleanly.
	if _, _, err := srv.Storage.RequeueExpiredJobs(time.Now().UTC()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	job, err = srv.Storage.GetEvolutionJob(id)
	if err != nil {
		writeError(w, wizardJobErrorStatus(err), err.Error())
		return
	}
	if evolutionJobTerminal(job.Status) {
		writeError(w, http.StatusConflict, fmt.Sprintf("cannot dispatch evolution job '%s': already terminal (%s)", job.ID, job.Status))
		return
	}
	if job.Status == EvolutionJobClaimed || job.Status == EvolutionJobRunning {
		writeError(w, http.StatusConflict, fmt.Sprintf("cannot dispatch evolution job '%s': status is %s — the job is already claimed (a harness or runner holds it)", job.ID, job.Status))
		return
	}
	if job.Status == EvolutionJobPaused || job.PauseRequested {
		if _, err := srv.Storage.ResumeEvolutionJob(job.ID); err != nil {
			writeError(w, wizardJobErrorStatus(err), err.Error())
			return
		}
	}
	token, tokErr := srv.wizardJobToken(job, req.JobToken)
	if tokErr != nil {
		writeError(w, http.StatusForbidden, tokErr.Error())
		return
	}
	runner, err := srv.wizardRunnerFor()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !srv.claimDispatch(job.ID) {
		writeError(w, http.StatusConflict, fmt.Sprintf("cannot dispatch evolution job '%s': the server is already driving its loop", job.ID))
		return
	}
	input := JobInput{Task: strings.TrimSpace(req.Task)}
	srv.EventBus.PublishActivity("api", "dispatch", "", job.ID, job.SkillSlug, DefaultAgentName)
	go func() {
		defer srv.releaseDispatch(job.ID)
		// RunLoop blocks until the loop parks or stops; the wizard follows it
		// through the polling seam. The token travels only through the runner
		// (which scrubs it from captured logs) — never into this message.
		if _, err := runner.RunLoop(job.ID, token, input); err != nil {
			logRunnerf("wizard-dispatched loop for job %s ended with error: %v", job.ID, err)
		}
	}()
	resp, err := srv.evolutionLoopSnapshot(job.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// rememberJobToken caches the raw token train/start minted, in memory only.
func (srv *Server) rememberJobToken(jobID, token string) {
	srv.wizardMu.Lock()
	defer srv.wizardMu.Unlock()
	if srv.wizardJobTokens == nil {
		srv.wizardJobTokens = map[string]string{}
	}
	srv.wizardJobTokens[jobID] = token
}

// wizardJobToken resolves the credential an operator-run call needs: the
// server's cached token first, then one the operator supplied in the request
// body (surviving a server restart). Both are verified against the job's
// stored hash; the failure is a denial naming the job, never a token leak.
func (srv *Server) wizardJobToken(job *EvolutionJob, supplied string) (string, error) {
	srv.wizardMu.Lock()
	token := srv.wizardJobTokens[job.ID]
	srv.wizardMu.Unlock()
	if verifyJobToken(job, token) {
		return token, nil
	}
	supplied = strings.TrimSpace(supplied)
	if verifyJobToken(job, supplied) {
		return supplied, nil
	}
	return "", &JobAuthError{JobID: job.ID, Action: "operator run"}
}

// dispatchInFlight reports whether this server is currently driving the job's
// loop. Read path.
func (srv *Server) dispatchInFlight(jobID string) bool {
	srv.wizardMu.Lock()
	defer srv.wizardMu.Unlock()
	return srv.wizardDispatches[jobID]
}

// claimDispatch marks a job as server-driven, refusing when a dispatch is
// already in flight. Check-and-set under one lock, so two concurrent POSTs
// cannot both start a loop.
func (srv *Server) claimDispatch(jobID string) bool {
	srv.wizardMu.Lock()
	defer srv.wizardMu.Unlock()
	if srv.wizardDispatches == nil {
		srv.wizardDispatches = map[string]bool{}
	}
	if srv.wizardDispatches[jobID] {
		return false
	}
	srv.wizardDispatches[jobID] = true
	return true
}

// releaseDispatch clears the in-flight marker when the loop goroutine ends.
func (srv *Server) releaseDispatch(jobID string) {
	srv.wizardMu.Lock()
	defer srv.wizardMu.Unlock()
	delete(srv.wizardDispatches, jobID)
}

// wizardRunner builds the headless runner once, from the server's validated
// story-04 configuration, and caches it (including a construction failure, so
// every attempt reads the same error).
func (srv *Server) wizardRunnerFor() (*Runner, error) {
	srv.wizardMu.Lock()
	defer srv.wizardMu.Unlock()
	if srv.wizardRunner != nil {
		return srv.wizardRunner, nil
	}
	if srv.wizardRunnerErr != nil {
		return nil, srv.wizardRunnerErr
	}
	runner, err := NewRunner(srv.Storage, nil)
	if err != nil {
		srv.wizardRunnerErr = err
		return nil, err
	}
	srv.wizardRunner = runner
	return runner, nil
}

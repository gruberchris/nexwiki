package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// This file holds story 08 of the wikiskill evolution support plan: the REST
// seam the wizard UI (Phase D/D1 + D2) reads through. The loop stepper's human
// controls stayed internal Go APIs in story 07 precisely so this file could
// wire them to HTTP following the handlers.go patterns, audited through the
// same Server-level wrappers that story 07 already lands in the activity log.
//
// The surface is read-mostly: everything the wizard renders comes from job
// records, loop state, iteration records, eval meta, and candidates — the
// files stories 01-07 already persist. Three POST routes expose the human
// controls (pause, abort, approve-early); each calls the audited story-07
// wrapper, so the trail lands in the activity log exactly like every other
// operator action, and none of them touches the harness's per-job token.
// Every route rides under /api, so it inherits the same origin-gated access
// (EnableCORS + loopback defaults in security.go) as the rest of the REST API.
//
// NOT BUILT HERE: job creation, dispatch, resume-after-pause, and eval upload
// remain harness-side (MCP tools over the per-job token) — NexWiki is the pure
// orchestrator, and a browser tab never holds a harness token. The wizard
// shows where the run stands and how to drive it; it does not mint tokens.
//
// Like every REST surface this file changes, the new routes are registered in
// main.go next to the /api/skills registry and documented in
// docs/aiagent_skills.md.

// EvolutionJobView is the browser-facing projection of an EvolutionJob: the
// job record with the per-harness token hash withheld. The copy is field by
// field on purpose — embedding EvolutionJob and tagging a shadowing TokenHash
// would not withhold anything, because a struct tag on an outer field does not
// override the promoted one and the embedded record would still serialize
// "token_hash" to the browser. The hash is not the token itself, but a browser
// tab has no business verifying harness credentials, so the REST surface
// simply never carries it.
type EvolutionJobView struct {
	ID               string            `json:"id"`
	SkillSlug        string            `json:"skill_slug"`
	CandidateID      string            `json:"candidate_id,omitempty"`
	Profile          string            `json:"profile"`
	Status           string            `json:"status"`
	Iteration        int               `json:"iteration"`
	MaxIterations    int               `json:"max_iterations"`
	Runner           string            `json:"runner,omitempty"`
	PID              int               `json:"pid,omitempty"`
	Progress         float64           `json:"progress,omitempty"`
	ProgressNote     string            `json:"progress_note,omitempty"`
	Artifacts        []JobArtifact     `json:"artifacts,omitempty"`
	IdempotencyKeys  map[string]string `json:"idempotency_keys,omitempty"`
	EvalHash         string            `json:"eval_hash,omitempty"`
	EvalTrainCount   int               `json:"eval_train_count,omitempty"`
	EvalValCount     int               `json:"eval_val_count,omitempty"`
	BaselineS0       float64           `json:"baseline_s0,omitempty"`
	BaselineScorer   string            `json:"baseline_scorer,omitempty"`
	EvalUploadedAt   time.Time         `json:"eval_uploaded_at,omitzero"`
	LoopOutcome      string            `json:"loop_outcome,omitempty"`
	ApproveRequested bool              `json:"approve_requested,omitempty"`
	PlateauLimit     int               `json:"plateau_limit,omitempty"`
	Checkpoint       string            `json:"checkpoint,omitempty"`
	PauseRequested   bool              `json:"pause_requested,omitempty"`
	CancelReason     string            `json:"cancel_reason,omitempty"`
	Error            string            `json:"error,omitempty"`
	// Story 09: the Trained Skill Result report article this run generated.
	ResultReportSlug string `json:"result_report_slug,omitempty"`
	// Story 11: the injection toggle config and the pre-live simulation trail
	// the job carries (count + the chain head's result hash).
	InjectionMode        string    `json:"injection_mode,omitempty"`
	SimulationCount      int       `json:"simulation_count,omitempty"`
	LatestSimulationHash string    `json:"latest_simulation_hash,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
	ClaimedAt            time.Time `json:"claimed_at,omitzero"`
	LastHeartbeat        time.Time `json:"last_heartbeat,omitzero"`
	LeaseExpiresAt       time.Time `json:"lease_expires_at,omitzero"`
	RunDeadlineAt        time.Time `json:"run_deadline_at"`
	CompletedAt          time.Time `json:"completed_at,omitzero"`
}

// viewOfJob projects a job record for REST responses: every field copied
// explicitly, and the token hash simply has no destination in the view.
func viewOfJob(job *EvolutionJob) EvolutionJobView {
	return EvolutionJobView{
		ID:                   job.ID,
		SkillSlug:            job.SkillSlug,
		CandidateID:          job.CandidateID,
		Profile:              job.Profile,
		Status:               job.Status,
		Iteration:            job.Iteration,
		MaxIterations:        job.MaxIterations,
		Runner:               job.Runner,
		PID:                  job.PID,
		Progress:             job.Progress,
		ProgressNote:         job.ProgressNote,
		Artifacts:            job.Artifacts,
		IdempotencyKeys:      job.IdempotencyKeys,
		EvalHash:             job.EvalHash,
		EvalTrainCount:       job.EvalTrainCount,
		EvalValCount:         job.EvalValCount,
		BaselineS0:           job.BaselineS0,
		BaselineScorer:       job.BaselineScorer,
		EvalUploadedAt:       job.EvalUploadedAt,
		LoopOutcome:          job.LoopOutcome,
		ApproveRequested:     job.ApproveRequested,
		PlateauLimit:         job.PlateauLimit,
		Checkpoint:           job.Checkpoint,
		PauseRequested:       job.PauseRequested,
		CancelReason:         job.CancelReason,
		Error:                job.Error,
		ResultReportSlug:     job.ResultReportSlug,
		InjectionMode:        job.InjectionMode,
		SimulationCount:      job.SimulationCount,
		LatestSimulationHash: job.LatestSimulationHash,
		CreatedAt:            job.CreatedAt,
		UpdatedAt:            job.UpdatedAt,
		ClaimedAt:            job.ClaimedAt,
		LastHeartbeat:        job.LastHeartbeat,
		LeaseExpiresAt:       job.LeaseExpiresAt,
		RunDeadlineAt:        job.RunDeadlineAt,
		CompletedAt:          job.CompletedAt,
	}
}

// wizardJobErrorStatus maps a job operation error onto a REST status code: an
// unknown job is 404, an illegal transition on an existing one is 409, and
// everything else is a server fault. Message matching follows the same
// contains-pattern the article handlers use.
func wizardJobErrorStatus(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return http.StatusNotFound
	case strings.Contains(msg, "already terminal"),
		strings.Contains(msg, "only paused jobs resume"),
		strings.Contains(msg, "status is"),
		strings.Contains(msg, "cannot finalize"),
		// Story 11 simulation refusals: missing preconditions on an existing
		// job are conflicts, not faults.
		strings.Contains(msg, "no eval set stored"),
		strings.Contains(msg, "holds no val cases"),
		strings.Contains(msg, "no pending candidate"),
		strings.Contains(msg, "only pending candidates simulate"),
		strings.Contains(msg, "diff-only candidates carry no body"),
		strings.Contains(msg, "must run a candidate of this job's skill"):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// HandleGetSkillTrainedState serves the derived trained state of one skill
// (untrained/trained/stale with reason and marker metadata) — the same state
// the registry rows carry, so the wizard's picker can refresh one skill
// without refetching the whole registry.
func (srv *Server) HandleGetSkillTrainedState(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "skill slug is required")
		return
	}
	state, err := srv.Storage.GetSkillTrainedState(slug)
	if err != nil {
		// A missing skill and a slug that is not a skill both read 404.
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// HandleListEvolutionJobs lists evolution jobs, oldest first, optionally
// narrowed to one skill with ?skill=<slug>. The wizard polls this to discover
// the run for the skill it is open on; a token-holding harness lists the same
// records over MCP.
func (srv *Server) HandleListEvolutionJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := srv.Storage.ListEvolutionJobs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if skillSlug := strings.TrimSpace(r.URL.Query().Get("skill")); skillSlug != "" {
		filtered := make([]EvolutionJobView, 0, len(jobs))
		for _, job := range jobs {
			if job.SkillSlug == skillSlug {
				filtered = append(filtered, viewOfJob(&job))
			}
		}
		writeJSON(w, http.StatusOK, filtered)
		return
	}
	out := make([]EvolutionJobView, 0, len(jobs))
	for i := range jobs {
		out = append(out, viewOfJob(&jobs[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

// HandleGetEvolutionJob serves one job record (token hash withheld).
func (srv *Server) HandleGetEvolutionJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "evolution job id is required")
		return
	}
	job, err := srv.Storage.GetEvolutionJob(id)
	if err != nil {
		writeError(w, wizardJobErrorStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, viewOfJob(job))
}

// EvolutionLoopResponse is the wizard's one-poll body: the loop stepper's
// current position plus every iteration record and the derived plateau count,
// so the live view renders "where is the run right now" from a single request.
type EvolutionLoopResponse struct {
	Loop          *LoopState        `json:"loop"`
	Iterations    []IterationRecord `json:"iterations"`
	PlateauCount  int               `json:"plateau_count"`
	MaxIterations int               `json:"max_iterations"`
	PlateauLimit  int               `json:"plateau_limit"`
}

// HandleGetEvolutionLoop serves the loop stepper state for one job — the
// browser equivalent of get_evolution_iterations' data (it needs no job
// token: the browser is the operator, not the harness).
func (srv *Server) HandleGetEvolutionLoop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "evolution job id is required")
		return
	}
	job, err := srv.Storage.GetEvolutionJob(id)
	if err != nil {
		writeError(w, wizardJobErrorStatus(err), err.Error())
		return
	}
	st, err := srv.Storage.GetLoopState(job.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	recs, err := srv.Storage.ListIterationRecords(job.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	plateau, err := srv.Storage.computePlateauCount(job.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if recs == nil {
		recs = []IterationRecord{}
	}
	writeJSON(w, http.StatusOK, EvolutionLoopResponse{
		Loop:          st,
		Iterations:    recs,
		PlateauCount:  plateau,
		MaxIterations: maxIterationsFor(job),
		PlateauLimit:  plateauLimitFor(job),
	})
}

// HandleGetEvolutionEval serves the stored eval metadata for one job: the
// split summary, S0 baseline, dry-run samples, and cost estimate the wizard's
// Data and Baseline steps render. Returns 404 when the job has no accepted
// upload yet — that is the "waiting for training data" state, not a fault.
func (srv *Server) HandleGetEvolutionEval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "evolution job id is required")
		return
	}
	if _, err := srv.Storage.GetEvolutionJob(id); err != nil {
		writeError(w, wizardJobErrorStatus(err), err.Error())
		return
	}
	_, _, meta, err := srv.Storage.ReadEvolutionEvalSet(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

// HandleGetEvolutionCandidate serves one skill candidate record — the
// iteration card's diff and pattern slugs. The diff is inert data rendered
// read-only by the wizard; nothing here writes to the wiki.
func (srv *Server) HandleGetEvolutionCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "skill candidate id is required")
		return
	}
	cand, err := srv.Storage.GetSkillCandidate(id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cand)
}

// wizardControlRequest is the optional body of a loop control call: a pause
// carries the checkpoint note, an abort carries the reason, and approve-early
// takes none. Every field is optional because every control has a working
// default.
type wizardControlRequest struct {
	Checkpoint string `json:"checkpoint,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// decodeWizardControl reads the optional body of a loop control call. An
// empty body (a control POSTed without JSON) decodes cleanly.
func decodeWizardControl(r *http.Request) (wizardControlRequest, error) {
	var req wizardControlRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if errors.Is(err, io.EOF) {
		return req, nil
	}
	return req, err
}

// HandlePauseEvolutionJob pauses the evolution loop: the story 07 audited
// wrapper parks the run at its next boundary (immediately when nothing is
// driving). <2s response — the park is a flag and a state write, never a
// process kill.
func (srv *Server) HandlePauseEvolutionJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "evolution job id is required")
		return
	}
	req, derr := decodeWizardControl(r)
	if derr != nil {
		writeDecodeError(w, derr)
		return
	}
	job, err := srv.PauseEvolutionLoop(id, req.Checkpoint)
	if err != nil {
		writeError(w, wizardJobErrorStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, viewOfJob(job))
}

// HandleAbortEvolutionJob aborts the run: the story 04 cancel path (SIGTERM,
// 10s grace, SIGKILL) reconciled with the loop records, audited. The skill
// stays at its last accepted version.
func (srv *Server) HandleAbortEvolutionJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "evolution job id is required")
		return
	}
	req, derr := decodeWizardControl(r)
	if derr != nil {
		writeDecodeError(w, derr)
		return
	}
	job, err := srv.AbortEvolutionLoop(id, req.Reason)
	if err != nil {
		writeError(w, wizardJobErrorStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, viewOfJob(job))
}

// HandleApproveEvolutionJob approves the current best early: the story 07
// audited wrapper gates the newest pending candidate and finishes the run
// completed. Accept promotes (stamping the trained marker); a gate rejection
// leaves the skill at its last accepted version.
func (srv *Server) HandleApproveEvolutionJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "evolution job id is required")
		return
	}
	job, err := srv.ApproveEarlyEvolutionLoop(id)
	if err != nil {
		writeError(w, wizardJobErrorStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, viewOfJob(job))
}

// simulateRequest is the optional body of a pre-live simulation POST: naming a
// candidate_id runs that candidate; an empty body targets the newest pending
// candidate for the job's skill.
type simulateRequest struct {
	CandidateID string `json:"candidate_id,omitempty"`
}

// HandleRunEvolutionSimulation runs the story-11 pre-live simulation: the
// candidate is scored against the job's stored historical val cases WITHOUT
// gating, and the immutable hash-linked result is returned. Pure
// server-side math over stored data — responds in well under 2 s.
func (srv *Server) HandleRunEvolutionSimulation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "evolution job id is required")
		return
	}
	var req simulateRequest
	if r.Body != nil {
		derr := json.NewDecoder(r.Body).Decode(&req)
		if derr != nil && !errors.Is(derr, io.EOF) {
			writeDecodeError(w, derr)
			return
		}
	}
	rec, err := srv.RunEvolutionSimulation(id, req.CandidateID)
	if err != nil {
		writeError(w, wizardJobErrorStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// HandleListEvolutionSimulations serves a job's simulation history, oldest
// first — the immutable, hash-linked trail the Review step renders.
func (srv *Server) HandleListEvolutionSimulations(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "evolution job id is required")
		return
	}
	if _, err := srv.Storage.GetEvolutionJob(id); err != nil {
		writeError(w, wizardJobErrorStatus(err), err.Error())
		return
	}
	recs, err := srv.Storage.ListSkillSimulations(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, recs)
}

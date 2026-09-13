package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// This file holds story 07 of the wikiskill evolution support plan: the
// evolution loop stepper backend (Phase C3) — the orchestrating iteration
// engine between a running evolution job and the story 03 validation gate. It
// is the backend the wizard's stepper UI (story 08) renders; there is
// deliberately no UI, no eval versioning, and no budget accounting here.
//
// POSITION IN THE STACK: the story 04 job lifecycle (claim/heartbeat/park,
// leases, run cap) is unchanged and stays underneath. The stepper is the
// per-iteration supervisor above it: it drives one CLI step per evolution
// phase, gates the iteration's candidate server-side, persists per-iteration
// records, and enforces the loop's stop conditions. The stepper consumes the
// story 04 budget machinery (run cap, iteration cap, leases) — it never
// rebuilds it.
//
// ITERATION STATE MACHINE — for each iteration k:
//
//	inference → maintaining → proposing → gating → accepted | rejected
//
// Each phase (except gating, which is pure server-side) is one harness step:
// the runner spawns the CLI once with the phase named in the stdin payload
// (`phase` field), exactly as story 04 spawns it, and the existing
// claim/heartbeat/park semantics apply throughout. When the gate resolves the
// iteration, the loop either continues at iteration k+1 or stops.
//
// STORAGE (per job, alongside the story 04/05 files — data records, never
// wiki content, never indexed):
//
//	data/skill_jobs/<id>.files/loop.json             — the current loop state
//	data/skill_jobs/<id>.files/iterations/iter-001.json … — one record per iteration
//
// LoopState is what the UI reads for "where is the run right now": current
// iteration, current phase, sub-step note, pause position. An IterationRecord
// carries the iteration number, every phase transition with timestamps and
// notes, the candidate it produced, the pattern slugs it touched, the
// validation score against R_best, and the outcome. Records are written before
// the state that depends on them advances, so a resume never re-runs a
// resolved iteration and never loses a resolved gate decision.
//
// STOP CONDITIONS (each terminal on the job, which releases the per-skill
// lock) and the loop outcome they record on the job (`loop_outcome`):
//
//	max iterations (job config, default 12)   → exhausted
//	plateau: N consecutive rejected iterations with no pattern gain
//	                                          (job config, default 3) → plateaued
//	score 1.0 early stop after an acceptance  → completed
//	human approve-early                       → completed
//	human abort (the story 04 cancel path)    → cancelled
//	run cap exceeded (the story 04 sweep)     → exhausted (timeout status)
//
// "Pattern gain" means the rejected iteration touched at least one wiki
// pattern slug no earlier iteration of this run touched; exploring new
// patterns resets the plateau counter, repeating the same ones burns it down.
// The counter is derived from the iteration records, never stored, so a crash
// cannot lose it. A loop outcome is the reason the run ENDED, not a quality
// verdict: exhausting the budget after a final acceptance still reads
// "exhausted" — the iteration history carries the accepts.
//
// AUTO-REVERT ON REJECT: rejection goes through the story 03 gate's reject
// path, which never touches the live skill — the skill stays at its last
// accepted version byte-identical, and only a gate accept promotes (and with
// it stamps the story 06 trained marker). Abort and approve-early likewise
// leave the skill and the wiki untouched unless the gate accepts.
//
// HUMAN CONTROLS stay internal Go APIs (story 08 wires HTTP): pause is the
// story 04 cooperative park integrated with the loop state, abort is the
// story 04 cancel reconciled with the loop records, and approve-early is new.
// Each is audited in the activity log by the Server-level wrappers below;
// none is an MCP tool, keeping the surface minimal.
//
// NOT BUILT HERE: the wizard UI and SSE feed (story 08), the result report
// (story 09), eval-store/scorer versioning (story 11), budget accounting (F3).

// Evolution loop phases, in drive order. "queued" is the pre-iteration state:
// the loop sits there between resolving iteration k and starting k+1.
const (
	LoopPhaseQueued      = "queued"
	LoopPhaseInference   = "inference"
	LoopPhaseMaintaining = "maintaining"
	LoopPhaseProposing   = "proposing"
	LoopPhaseGating      = "gating"
)

// loopPhaseOrder is the drive order of the harness phases within one
// iteration. Gating is not in it: the stepper runs it itself, server-side,
// after the proposing step completes.
var loopPhaseOrder = []string{LoopPhaseInference, LoopPhaseMaintaining, LoopPhaseProposing}

// nextLoopPhase returns the phase that follows phase, "" at the end of the
// harness phases (the caller transitions to gating there).
func nextLoopPhase(phase string) string {
	for i, p := range loopPhaseOrder {
		if p == phase && i+1 < len(loopPhaseOrder) {
			return loopPhaseOrder[i+1]
		}
	}
	return ""
}

// Iteration outcomes. "" while the iteration is in flight; "interrupted" when
// a run ended (abort, harness failure, timeout) before the iteration's gate
// resolved. Accepted/rejected are the gate's own vocabulary.
const (
	IterationOutcomeAccepted    = "accepted"
	IterationOutcomeRejected    = "rejected"
	IterationOutcomeInterrupted = "interrupted"
)

// Loop outcomes recorded on the job when the loop stops. Exactly the four the
// story brief defines — "failed" is a loop-state-only value for a run that
// died of a harness/storage error (the job's own status carries "failed"; no
// loop outcome is stamped on it because a failure is not a designed stop).
const (
	LoopOutcomeCompleted = "completed"
	LoopOutcomeExhausted = "exhausted"
	LoopOutcomePlateaued = "plateaued"
	LoopOutcomeCancelled = "cancelled"
	LoopOutcomeFailed    = "failed"
)

// Loop state statuses.
const (
	LoopStatusRunning  = "running"
	LoopStatusPaused   = "paused"
	LoopStatusTerminal = "terminal"
)

// LoopPhaseStatusInterrupted marks a phase the stepper entered but never
// completed (a park fired mid-step): a resume re-runs it, appending a fresh
// entry rather than rewriting the interrupted trace.
const LoopPhaseStatusInterrupted = "interrupted"

// evolutionPlateauDefault is the plateau stop rule's default: 3 consecutive
// rejected iterations with no pattern gain.
const evolutionPlateauDefault = 3

// LoopPhaseEntry is one phase transition inside an iteration: when the phase
// was entered, when (and whether) it completed, and the harness's last
// progress note inside it. An entry without ExitedAt is an interruption — the
// honest trace of a park mid-step or a run that died there. A phase may
// appear more than once when a resume re-runs an interrupted phase.
type LoopPhaseEntry struct {
	Phase     string    `json:"phase"`
	EnteredAt time.Time `json:"entered_at"`
	ExitedAt  time.Time `json:"exited_at,omitzero"`
	Note      string    `json:"note,omitempty"`
	Complete  bool      `json:"complete"`
}

// IterationRecord is one complete iteration of the evolution loop: the
// per-iteration record the wizard's history and the story 09 report render.
type IterationRecord struct {
	JobID     string `json:"job_id"`
	SkillSlug string `json:"skill_slug"`
	Iteration int    `json:"iteration"`

	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitzero"`

	// Phases lists every phase transition of this iteration in order,
	// including re-runs after an interrupted resume.
	Phases []LoopPhaseEntry `json:"phases"`

	// The candidate this iteration produced and gated. Empty on an iteration
	// whose proposing phase produced nothing (recorded as rejected, no
	// candidate) and on an interrupted one.
	CandidateID  string   `json:"candidate_id,omitempty"`
	PatternSlugs []string `json:"pattern_slugs,omitempty"`

	// Validation score versus R_best at this iteration's gate, from the gate's
	// own decision (the story 03 audit numbers, not a harness claim).
	ValScore float64 `json:"val_score"`
	RBest    float64 `json:"r_best"`

	// Outcome: accepted | rejected | interrupted | "" (in flight).
	Outcome string `json:"outcome"`

	// ResultVersion is the promoted skill version when the gate accepted.
	ResultVersion int    `json:"result_version,omitempty"`
	Note          string `json:"note,omitempty"`
}

// LoopState is the loop stepper's current position, persisted per job so a
// pause/resume (or a crash) continues exactly where the run left off. It is
// the file the wizard's live view polls.
type LoopState struct {
	JobID     string `json:"job_id"`
	SkillSlug string `json:"skill_slug"`

	// Status: running (a stepper is between claim and stop), paused (parked at
	// a boundary), terminal (a stop condition ended the loop).
	Status string `json:"status"`

	// CurrentIteration is the iteration in progress or next to start (1-based).
	// CurrentPhase is the phase the loop is at; PhaseStatus qualifies it:
	// "" (pending), "running" (a CLI step is mid-flight), or "interrupted"
	// (parked mid-step — resume re-runs this phase).
	CurrentIteration int    `json:"current_iteration"`
	CurrentPhase     string `json:"current_phase"`
	PhaseStatus      string `json:"phase_status,omitempty"`

	// Note is the latest sub-step/progress note (mirrored from the job's
	// progress note at phase boundaries).
	Note string `json:"note,omitempty"`

	// Outcome/OutcomeReason are set on terminal: the loop outcome vocabulary
	// above plus the reason the loop stopped.
	Outcome       string `json:"outcome,omitempty"`
	OutcomeReason string `json:"outcome_reason,omitempty"`

	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// loopSubPath validates a job ID and joins it under the job's files directory,
// confining loop reads and writes to that directory the way jobPath does.
func (s *Storage) loopSubPath(id string, elems ...string) (string, error) {
	base := filepath.Base(strings.TrimSpace(id))
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, `/\`) {
		return "", fmt.Errorf("invalid evolution job id")
	}
	if s.JobDir == "" {
		return "", fmt.Errorf("evolution job storage is not configured")
	}
	return filepath.Join(s.JobDir, base+".files", filepath.Join(elems...)), nil
}

// loopStatePath resolves data/skill_jobs/<id>.files/loop.json.
func (s *Storage) loopStatePath(id string) (string, error) {
	return s.loopSubPath(id, "loop.json")
}

// iterationsDir resolves data/skill_jobs/<id>.files/iterations.
func (s *Storage) iterationsDir(id string) (string, error) {
	return s.loopSubPath(id, "iterations")
}

// iterationRecordPath resolves the per-iteration record file, zero-padded so
// the directory sorts by iteration.
func (s *Storage) iterationRecordPath(id string, iteration int) (string, error) {
	if iteration <= 0 {
		return "", fmt.Errorf("iteration number must be positive")
	}
	return s.loopSubPath(id, "iterations", fmt.Sprintf("iter-%03d.json", iteration))
}

// loadLoopState reads the loop state file; nil, nil when the job has none
// (the stepper never drove it, or it was driven before this story).
func (s *Storage) loadLoopState(jobID string) (*LoopState, error) {
	path, err := s.loopStatePath(jobID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var st LoopState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("invalid loop state for job '%s': %w", jobID, err)
	}
	return &st, nil
}

// GetLoopState returns the loop stepper's state for a job, or nil when the
// stepper has not driven it. Read path: no lock, mirroring job reads.
func (s *Storage) GetLoopState(jobID string) (*LoopState, error) {
	return s.loadLoopState(jobID)
}

// writeLoopStateLocked persists the loop state. Callers hold Storage.writeMu.
func (s *Storage) writeLoopStateLocked(st *LoopState) error {
	path, err := s.loopStatePath(st.JobID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create job files directory: %w", err)
	}
	st.UpdatedAt = time.Now().UTC()
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode loop state: %w", err)
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		return fmt.Errorf("failed to write loop state: %w", err)
	}
	return nil
}

// initLoopState loads the loop state for a running stepper, creating it on
// first drive and re-marking it running after a resume. Terminal states are
// returned untouched — a stopped loop never restarts silently.
func (s *Storage) initLoopState(job *EvolutionJob) (*LoopState, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	st, err := s.loadLoopState(job.ID)
	if err != nil {
		return nil, err
	}
	if st == nil {
		st = &LoopState{
			JobID:            job.ID,
			SkillSlug:        job.SkillSlug,
			Status:           LoopStatusRunning,
			CurrentIteration: 1,
			CurrentPhase:     LoopPhaseQueued,
			StartedAt:        time.Now().UTC(),
		}
	} else if st.Status != LoopStatusTerminal {
		st.Status = LoopStatusRunning
	}
	if err := s.writeLoopStateLocked(st); err != nil {
		return nil, err
	}
	return st, nil
}

// pauseLoopStateLocked marks a driving loop paused, keeping its position.
// Callers hold Storage.writeMu (the story 04 pause path calls this inline).
func (s *Storage) pauseLoopStateLocked(jobID string) error {
	st, err := s.loadLoopState(jobID)
	if err != nil {
		return err
	}
	if st == nil || st.Status == LoopStatusTerminal {
		return nil
	}
	st.Status = LoopStatusPaused
	return s.writeLoopStateLocked(st)
}

// mutateLoopState applies fn to the loop state and persists it, creating a
// state on demand when create is set. Used by the stepper and the approve
// path, which hold no lock of their own here.
func (s *Storage) mutateLoopState(jobID string, create bool, fn func(*LoopState)) (*LoopState, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	st, err := s.loadLoopState(jobID)
	if err != nil {
		return nil, err
	}
	if st == nil {
		if !create {
			return nil, nil
		}
		job, err := s.GetEvolutionJob(jobID)
		if err != nil {
			return nil, err
		}
		st = &LoopState{
			JobID:            job.ID,
			SkillSlug:        job.SkillSlug,
			Status:           LoopStatusRunning,
			CurrentIteration: 1,
			CurrentPhase:     LoopPhaseQueued,
			StartedAt:        time.Now().UTC(),
		}
	}
	fn(st)
	if err := s.writeLoopStateLocked(st); err != nil {
		return nil, err
	}
	return st, nil
}

// loadIterationRecord reads one iteration record; nil, nil when absent.
func (s *Storage) loadIterationRecord(jobID string, iteration int) (*IterationRecord, error) {
	path, err := s.iterationRecordPath(jobID, iteration)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rec IterationRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("invalid iteration record for job '%s' iteration %d: %w", jobID, iteration, err)
	}
	return &rec, nil
}

// GetIterationRecord returns one iteration's record, or nil when the
// iteration has not started. Read path: no lock.
func (s *Storage) GetIterationRecord(jobID string, iteration int) (*IterationRecord, error) {
	return s.loadIterationRecord(jobID, iteration)
}

// writeIterationRecordLocked persists an iteration record. Callers hold
// Storage.writeMu. Records are small (a dozen phase entries at most), so a
// whole-file rewrite per transition is cheaper than any journal and keeps the
// file always parseable.
func (s *Storage) writeIterationRecordLocked(rec *IterationRecord) error {
	path, err := s.iterationRecordPath(rec.JobID, rec.Iteration)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create iteration directory: %w", err)
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode iteration record: %w", err)
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		return fmt.Errorf("failed to write iteration record: %w", err)
	}
	return nil
}

// ensureIterationRecord returns the iteration's record, creating it (with its
// start timestamp) when missing. Callers hold Storage.writeMu.
func (s *Storage) ensureIterationRecordLocked(job *EvolutionJob, iteration int) (*IterationRecord, error) {
	rec, err := s.loadIterationRecord(job.ID, iteration)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		rec = &IterationRecord{
			JobID:     job.ID,
			SkillSlug: job.SkillSlug,
			Iteration: iteration,
			StartedAt: time.Now().UTC(),
			Phases:    []LoopPhaseEntry{},
		}
		if err := s.writeIterationRecordLocked(rec); err != nil {
			return nil, err
		}
	}
	return rec, nil
}

// appendPhaseEntry records that the stepper entered phase within iteration
// iteration, creating the record on demand.
func (s *Storage) appendPhaseEntry(job *EvolutionJob, iteration int, phase string) (*IterationRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	rec, err := s.ensureIterationRecordLocked(job, iteration)
	if err != nil {
		return nil, err
	}
	rec.Phases = append(rec.Phases, LoopPhaseEntry{Phase: phase, EnteredAt: time.Now().UTC()})
	if err := s.writeIterationRecordLocked(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// closePhaseEntry marks the iteration's most recent entry for phase complete
// (or explicitly leaves the interruption standing by only stamping the note).
// The LAST entry wins: after an interrupted resume the record holds the stale
// incomplete entry followed by the re-run's fresh one, and only the fresh one
// closes.
func (s *Storage) closePhaseEntry(jobID string, iteration int, phase, note string, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	rec, err := s.loadIterationRecord(jobID, iteration)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("no iteration record for job '%s' iteration %d", jobID, iteration)
	}
	for i := len(rec.Phases) - 1; i >= 0; i-- {
		if rec.Phases[i].Phase == phase {
			if rec.Phases[i].Complete {
				return nil
			}
			rec.Phases[i].ExitedAt = now
			rec.Phases[i].Complete = true
			if strings.TrimSpace(note) != "" {
				rec.Phases[i].Note = strings.TrimSpace(note)
			}
			break
		}
	}
	return s.writeIterationRecordLocked(rec)
}

// resolveIterationRecord stamps the gate's decision onto the iteration record
// and marks it complete. Callers hold no lock; the write is serialized.
func (s *Storage) resolveIterationRecord(jobID string, iteration int, apply func(*IterationRecord)) (*IterationRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	rec, err := s.loadIterationRecord(jobID, iteration)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, fmt.Errorf("no iteration record for job '%s' iteration %d", jobID, iteration)
	}
	apply(rec)
	rec.CompletedAt = time.Now().UTC()
	if err := s.writeIterationRecordLocked(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// ListIterationRecords returns every iteration record of a job ordered by
// iteration number. Read path: no lock.
func (s *Storage) ListIterationRecords(jobID string) ([]IterationRecord, error) {
	dir, err := s.iterationsDir(jobID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []IterationRecord{}, nil
		}
		return nil, err
	}
	var out []IterationRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "iter-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSuffix(e.Name(), ".json"), "iter-%d", &n); err != nil || n <= 0 {
			continue
		}
		rec, err := s.loadIterationRecord(jobID, n)
		if err != nil || rec == nil {
			continue
		}
		out = append(out, *rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Iteration < out[j].Iteration })
	if out == nil {
		out = []IterationRecord{}
	}
	return out, nil
}

// patternsTouchedBefore returns the union of pattern slugs every iteration
// before `iteration` of this job touched — the baseline a rejected
// iteration's pattern gain is measured against.
func (s *Storage) patternsTouchedBefore(jobID string, iteration int) (map[string]bool, error) {
	recs, err := s.ListIterationRecords(jobID)
	if err != nil {
		return nil, err
	}
	touched := map[string]bool{}
	for _, rec := range recs {
		if rec.Iteration >= iteration {
			continue
		}
		for _, slug := range rec.PatternSlugs {
			touched[slug] = true
		}
	}
	return touched, nil
}

// plateauLimitFor resolves the job's plateau stop rule; 0 (jobs that predate
// the field) falls back to the story default.
func plateauLimitFor(job *EvolutionJob) int {
	if job.PlateauLimit > 0 {
		return job.PlateauLimit
	}
	return evolutionPlateauDefault
}

// maxIterationsFor resolves the job's iteration cap; 0 falls back to the
// story 04 default so a hand-edited record cannot loop forever.
func maxIterationsFor(job *EvolutionJob) int {
	if job.MaxIterations > 0 {
		return job.MaxIterations
	}
	return evolutionJobMaxIterations
}

// newestPendingCandidateForSkill returns the most recently created pending
// candidate for a skill, or nil. Read path: no lock.
func (s *Storage) newestPendingCandidateForSkill(skillSlug string) (*SkillCandidate, error) {
	cands, err := s.ListSkillCandidates()
	if err != nil {
		return nil, err
	}
	var newest *SkillCandidate
	for i := range cands {
		c := &cands[i]
		if c.ParentSlug != skillSlug || c.Status != SkillCandidatePending {
			continue
		}
		if newest == nil || c.CreatedAt.After(newest.CreatedAt) {
			newest = c
		}
	}
	return newest, nil
}

// findCandidateForGate resolves the candidate this iteration's gate decides
// on: the newest pending candidate created at or after the iteration's
// proposing phase entered (the iteration's own product — typically proposed
// over MCP by the proposer-phase harness during that step), falling back to a
// still-pending pre-registered candidate the job was created around (story 04
// semantics, consumed once — a decided candidate never comes back).
func (s *Storage) findCandidateForGate(job *EvolutionJob, rec *IterationRecord) (*SkillCandidate, error) {
	var since time.Time
	if rec != nil {
		for _, ph := range rec.Phases {
			if ph.Phase == LoopPhaseProposing && ph.EnteredAt.After(since) {
				since = ph.EnteredAt
			}
		}
	}
	cands, err := s.ListSkillCandidates()
	if err != nil {
		return nil, err
	}
	var newest *SkillCandidate
	for i := range cands {
		c := &cands[i]
		if c.ParentSlug != job.SkillSlug || c.Status != SkillCandidatePending {
			continue
		}
		if !since.IsZero() && c.CreatedAt.Before(since) {
			continue
		}
		if newest == nil || c.CreatedAt.After(newest.CreatedAt) {
			newest = c
		}
	}
	if newest != nil {
		return newest, nil
	}
	if strings.TrimSpace(job.CandidateID) != "" {
		c, err := s.GetSkillCandidate(strings.TrimSpace(job.CandidateID))
		if err == nil && c.ParentSlug == job.SkillSlug && c.Status == SkillCandidatePending {
			return c, nil
		}
	}
	return nil, nil
}

// completeLoopJob finalizes a designed loop stop token-free: the stepper and
// the approve-early path call it (CompleteEvolutionJob is the harness's
// token-scoped equivalent). Idempotent like its sibling: re-finalizing the
// same status is a no-op; a different terminal status is refused — history is
// not rewritten.
func (s *Storage) completeLoopJob(jobID, outcome, reason string) (*EvolutionJob, error) {
	s.writeMu.Lock()
	job, err := s.GetEvolutionJob(jobID)
	if err != nil {
		s.writeMu.Unlock()
		return nil, err
	}
	if evolutionJobTerminal(job.Status) {
		if job.Status != EvolutionJobComplete {
			s.writeMu.Unlock()
			return nil, fmt.Errorf("cannot finalize evolution job '%s': already terminal (%s)", job.ID, job.Status)
		}
		s.writeMu.Unlock()
		return job, nil
	}
	job.Status = EvolutionJobComplete
	job.LoopOutcome = outcome
	job.ProgressNote = strings.TrimSpace(reason)
	job.PID = 0
	job.PauseRequested = false
	job.CompletedAt = time.Now().UTC()
	err = s.writeEvolutionJob(job)
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}

	if _, err := s.mutateLoopState(job.ID, true, func(st *LoopState) {
		st.Status = LoopStatusTerminal
		st.Outcome = outcome
		st.OutcomeReason = strings.TrimSpace(reason)
	}); err != nil {
		return job, err
	}
	return job, nil
}

// reconcileLoopTerminalLocked brings the loop's persisted state in line with a
// job that reached a terminal status outside the stepper's own stop conditions
// (a harness complete callback, an abort, a run-cap timeout, a harness
// failure). The in-flight iteration record is marked interrupted and the loop
// state reads terminal with the outcome mapped from the job status. Callers
// hold Storage.writeMu. Returns the mapped loop outcome ("" when no loop state
// exists — the job was never loop-driven).
//
// A job that is NOT terminal has not ended the run: a lease lost mid-step
// hands the job back to the sweep (requeued, claimable again), and the honest
// reconcile is the pause/interrupt trace, not a terminal state. "" comes back
// so no caller terminal-izes a resumable run behind a fabricated outcome.
func (s *Storage) reconcileLoopTerminalLocked(job *EvolutionJob) (string, error) {
	if !evolutionJobTerminal(job.Status) {
		return s.interruptLoopForRequeueLocked(job)
	}
	st, err := s.loadLoopState(job.ID)
	if err != nil {
		return "", err
	}
	if st == nil {
		// The loop never drove this job (a story 04 dispatch, a run cap that
		// landed before the claim). The stop still reads on the job: stamp the
		// outcome from the status so every terminal stop carries its reason.
		outcome := loopOutcomeForJobStatus(job.Status)
		if outcome != LoopOutcomeFailed && job.LoopOutcome == "" {
			job.LoopOutcome = outcome
			if err := s.writeEvolutionJob(job); err != nil {
				return "", err
			}
		}
		return outcome, nil
	}
	if st.Status == LoopStatusTerminal {
		return st.Outcome, nil
	}

	// Mark the iteration that was in flight when the run ended. Resolved
	// iterations are never rewritten: only an in-flight one becomes
	// interrupted.
	rec, err := s.loadIterationRecord(job.ID, st.CurrentIteration)
	if err != nil {
		return "", err
	}
	if rec != nil && rec.Outcome == "" {
		rec.Outcome = IterationOutcomeInterrupted
		rec.CompletedAt = time.Now().UTC()
		rec.Note = terminalNoteForJob(job)
		if err := s.writeIterationRecordLocked(rec); err != nil {
			return "", err
		}
	}

	outcome := loopOutcomeForJobStatus(job.Status)
	reason := terminalNoteForJob(job)
	st.Status = LoopStatusTerminal
	st.Outcome = outcome
	st.OutcomeReason = reason
	st.Note = reason
	if err := s.writeLoopStateLocked(st); err != nil {
		return "", err
	}
	// Stamp the job-level outcome for the designed-stop vocabulary. A failed
	// run is not a designed stop: the job's own failed status and error carry
	// it, and loop_outcome stays empty.
	if outcome != LoopOutcomeFailed && job.LoopOutcome == "" {
		job.LoopOutcome = outcome
		if err := s.writeEvolutionJob(job); err != nil {
			return outcome, err
		}
	}
	return outcome, nil
}

// interruptLoopForRequeueLocked handles a reconcile call for a job that is
// still active (a lease lost mid-step: the sweep requeues it and a re-dispatch
// re-claims it). The phase the stepper entered stands interrupted exactly like
// the mid-step park trace — the open entry stays, a resume re-runs the phase
// and appends the fresh one — and the loop state stays non-terminal at that
// phase boundary, so the next dispatch resumes there. Callers hold
// Storage.writeMu. Returns "" : a live job ends no loop.
func (s *Storage) interruptLoopForRequeueLocked(job *EvolutionJob) (string, error) {
	st, err := s.loadLoopState(job.ID)
	if err != nil {
		return "", err
	}
	if st == nil {
		// The loop never drove this job; a still-active job gets no outcome
		// stamped on it — the run is not over.
		return "", nil
	}
	if st.Status == LoopStatusTerminal {
		return st.Outcome, nil
	}
	st.Status = LoopStatusPaused
	switch st.CurrentPhase {
	case LoopPhaseInference, LoopPhaseMaintaining, LoopPhaseProposing:
		st.PhaseStatus = LoopPhaseStatusInterrupted
	}
	st.Note = "lease expired mid-phase — the job is re-claimable; re-dispatch re-runs " + st.CurrentPhase
	if err := s.writeLoopStateLocked(st); err != nil {
		return "", err
	}
	return "", nil
}

// reconcileLoopTerminal is the lock-free wrapper the stepper uses.
func (s *Storage) reconcileLoopTerminal(job *EvolutionJob) (string, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.reconcileLoopTerminalLocked(job)
}

// loopOutcomeForJobStatus maps a terminal job status to the loop outcome
// vocabulary. Budget stops read exhausted; the run cap is a budget stop.
func loopOutcomeForJobStatus(status string) string {
	switch status {
	case EvolutionJobComplete:
		return LoopOutcomeCompleted
	case EvolutionJobCancelled:
		return LoopOutcomeCancelled
	case EvolutionJobTimeout:
		return LoopOutcomeExhausted
	default:
		return LoopOutcomeFailed
	}
}

// terminalNoteForJob renders why a job went terminal, for record notes.
func terminalNoteForJob(job *EvolutionJob) string {
	switch {
	case strings.TrimSpace(job.CancelReason) != "":
		return strings.TrimSpace(job.CancelReason)
	case strings.TrimSpace(job.Error) != "":
		return strings.TrimSpace(job.Error)
	case job.Status == EvolutionJobTimeout:
		return "90-minute run cap exceeded"
	default:
		return "run ended (" + job.Status + ")"
	}
}

// ApproveEarlyEvolutionJob is the human approve-early control: accept the
// current best immediately and finish. It sets the request flag so a driving
// stepper applies it at its next boundary (within one phase step), and — when
// nothing is driving (a parked job) — it finishes the run right here: the
// newest pending candidate goes through the normal story 03 gate (an accept
// promotes and stamps the trained marker; a gate rejection leaves the skill
// at its last accepted version), then the run finalizes completed either way.
// Terminal jobs are refused — history is not rewritten.
func (s *Storage) ApproveEarlyEvolutionJob(id string) (*EvolutionJob, error) {
	s.writeMu.Lock()
	job, err := s.GetEvolutionJob(id)
	if err != nil {
		s.writeMu.Unlock()
		return nil, err
	}
	if evolutionJobTerminal(job.Status) {
		s.writeMu.Unlock()
		return nil, fmt.Errorf("cannot approve evolution job '%s' early: already terminal (%s)", job.ID, job.Status)
	}
	job.ApproveRequested = true
	if err := s.writeEvolutionJob(job); err != nil {
		s.writeMu.Unlock()
		return nil, err
	}
	driveNow := job.Status == EvolutionJobPaused
	jobCopy := *job
	s.writeMu.Unlock()

	// Nothing is driving: apply the approval immediately (this must not run
	// under writeMu — the gate path takes it).
	if driveNow {
		if err := s.applyApproveEarly(&jobCopy); err != nil {
			return nil, err
		}
		fresh, err := s.GetEvolutionJob(id)
		if err != nil {
			return nil, err
		}
		return fresh, nil
	}
	return job, nil
}

// applyApproveEarly finishes the run under an approve-early request: the
// newest pending candidate for the skill goes through the normal gate, the
// in-flight iteration record is resolved against it when it belongs to this
// iteration, and the run finalizes completed with the skill at its best
// accepted version. Idempotent: a terminal job is finalized once.
func (s *Storage) applyApproveEarly(job *EvolutionJob) error {
	// The approve may land while the stepper sits anywhere in the state
	// machine; the iteration it resolves is the loop's current one.
	st, err := s.loadLoopState(job.ID)
	if err != nil {
		return err
	}
	iteration := 0
	if st != nil && st.Status != LoopStatusTerminal {
		iteration = st.CurrentIteration
	}

	var cand *SkillCandidate
	var rej *SkillGateRejection
	var promotedVersion int
	pending, err := s.newestPendingCandidateForSkill(job.SkillSlug)
	if err != nil {
		return err
	}
	if pending != nil {
		art, perr := s.PromoteSkillCandidate(pending.ID)
		if perr == nil {
			// Re-read the decided record: the promote path stamped the score
			// and result version on it, not on this in-memory copy.
			cand, err = s.GetSkillCandidate(pending.ID)
			if err != nil {
				return err
			}
			promotedVersion = art.Version
		} else if errors.As(perr, &rej) {
			// The gate refused the current best: approve-early still finishes,
			// with the skill left at its last accepted version.
			cand = pending
		} else {
			// A real fault (audit write failure, vanished parent): leave the
			// request flag set so the stepper retries at its next boundary.
			return perr
		}
	}

	// Attribute the decision to the in-flight iteration when the loop is
	// standing in one; the newest pending candidate is the current best at
	// approve time, so an unresolved, candidate-less record receives it.
	rec, rerr := (func() (*IterationRecord, error) {
		if iteration <= 0 {
			return nil, nil
		}
		return s.loadIterationRecord(job.ID, iteration)
	})()
	if rerr != nil {
		return rerr
	}
	if cand != nil && rec != nil && rec.Outcome == "" && rec.CandidateID == "" {
		// The approve short-circuits the iteration's gating phase: leave a
		// coherent phase trail behind before the record resolves.
		if _, err := s.appendPhaseEntry(job, rec.Iteration, LoopPhaseGating); err != nil {
			return err
		}
		if _, rerr := s.resolveIterationRecord(job.ID, rec.Iteration, func(r *IterationRecord) {
			r.CandidateID = cand.ID
			r.PatternSlugs = cand.PatternSlugs
			if rej != nil {
				r.Outcome = IterationOutcomeRejected
				r.ValScore = rej.Score
				r.RBest = rej.Best
			} else {
				r.Outcome = IterationOutcomeAccepted
				r.ValScore = cand.BestScore
				r.RBest = cand.BestScore
				r.ResultVersion = promotedVersion
			}
			r.Note = "resolved by approve-early"
		}); rerr != nil {
			return rerr
		}
		if err := s.closePhaseEntry(job.ID, rec.Iteration, LoopPhaseGating, "resolved by approve-early", time.Now().UTC()); err != nil {
			return err
		}
	} else if rec != nil && rec.Outcome == "" {
		if _, rerr := s.resolveIterationRecord(job.ID, rec.Iteration, func(r *IterationRecord) {
			r.Outcome = IterationOutcomeInterrupted
			r.Note = "approved early before the iteration gated"
		}); rerr != nil {
			return rerr
		}
	}

	if _, err := s.completeLoopJob(job.ID, LoopOutcomeCompleted, "approved early by operator"); err != nil {
		return err
	}
	return nil
}

// LoopStepResult reports what one stepper boundary did: whether a phase
// advanced (a CLI step ran, or the gate resolved an iteration), whether the
// run parked at the boundary, and whether the loop stopped (with its outcome).
// Story 08's driver and the tests read this instead of re-deriving from files.
type LoopStepResult struct {
	Advanced  bool
	Parked    bool
	Stopped   bool
	Outcome   string
	Iteration int
	Phase     string
}

// LoopStep advances the evolution loop by exactly one boundary: it claims the
// job when queued, runs one harness phase step (or resolves the iteration's
// gate), persists the transition, and honors pause/abort/approve-early at
// every boundary. RunLoop loops it; the tests drive it boundary by boundary
// to interleave candidate proposals the way a real proposer-phase harness
// would over MCP.
func (r *Runner) LoopStep(jobID, token string, input JobInput) (LoopStepResult, error) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	default:
		return LoopStepResult{}, fmt.Errorf("cannot drive evolution job '%s': runner is at capacity (%d concurrent jobs)", jobID, cap(r.sem))
	}
	return r.loopStep(jobID, token, input)
}

// RunLoop drives a job's evolution loop to a stop: it claims the job and
// advances boundary by boundary until the loop stops (a stop condition, an
// approve-early, or an abort observed by reconcile) or the run parks for a
// pause — the caller re-dispatches after a resume. It returns the final loop
// state for the wizard's header. Concurrency is bounded by the runner's
// semaphore for the whole run.
func (r *Runner) RunLoop(jobID, token string, input JobInput) (LoopState, error) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	default:
		return LoopState{}, fmt.Errorf("cannot drive evolution job '%s': runner is at capacity (%d concurrent jobs)", jobID, cap(r.sem))
	}
	for {
		res, err := r.loopStep(jobID, token, input)
		if err != nil {
			// Lease losses hand the job back to the sweep; the caller re-dispatches.
			if _, ok := err.(*JobLeaseExpiredError); ok {
				break
			}
			return r.finalLoopState(jobID), err
		}
		if res.Parked || res.Stopped {
			break
		}
	}
	return r.finalLoopState(jobID), nil
}

// finalLoopState loads the loop state for a return value; a missing state
// (the loop never started) returns zero-valued.
func (r *Runner) finalLoopState(jobID string) LoopState {
	st, err := r.Storage.GetLoopState(jobID)
	if err != nil || st == nil {
		return LoopState{}
	}
	return *st
}

// loopStep is one boundary without semaphore acquisition (RunLoop holds its
// slot for the whole run).
func (r *Runner) loopStep(jobID, token string, input JobInput) (LoopStepResult, error) {
	// The story 04 sweep runs first, exactly as Dispatch runs it: expired
	// leases requeue, past-deadline jobs go terminal. The stepper consumes
	// that machinery — it never rebuilds budget enforcement.
	if _, _, err := r.Storage.RequeueExpiredJobs(time.Now().UTC()); err != nil {
		return LoopStepResult{}, err
	}
	job, err := r.Storage.GetEvolutionJob(jobID)
	if err != nil {
		return LoopStepResult{}, err
	}
	if !verifyJobToken(job, token) {
		return LoopStepResult{}, &JobAuthError{JobID: jobID, Action: "loop step"}
	}

	// A stopped loop stays stopped.
	loopState, loopErr := r.Storage.GetLoopState(job.ID)
	if loopErr == nil && loopState != nil && loopState.Status == LoopStatusTerminal {
		return LoopStepResult{Stopped: true, Outcome: loopState.Outcome, Iteration: loopState.CurrentIteration, Phase: loopState.CurrentPhase}, nil
	}
	loopIteration := func() int {
		if loopState != nil && loopState.CurrentIteration > 0 {
			return loopState.CurrentIteration
		}
		return 1
	}
	loopPhase := func() string {
		if loopState != nil {
			return loopState.CurrentPhase
		}
		return ""
	}

	// The job went terminal outside the stepper (harness complete callback,
	// abort, run cap, failure): reconcile the loop records and stop.
	if evolutionJobTerminal(job.Status) {
		outcome, err := r.Storage.reconcileLoopTerminal(job)
		if err != nil {
			return LoopStepResult{}, err
		}
		return LoopStepResult{Stopped: true, Outcome: outcome}, nil
	}

	if job.Status == EvolutionJobPaused {
		// Parked and not resumed: nothing to advance. The state file may lag
		// if the pause landed between the stepper's writes — reconcile it.
		if _, err := r.Storage.mutateLoopState(job.ID, false, func(st *LoopState) {
			if st.Status == LoopStatusRunning {
				st.Status = LoopStatusPaused
			}
		}); err != nil {
			return LoopStepResult{}, err
		}
		return LoopStepResult{Parked: true, Iteration: loopIteration(), Phase: loopPhase()}, nil
	}

	// Cooperative pause requested (a control call between boundaries, or a
	// flag left set from mid-step): park here, before the next phase spawns.
	if job.PauseRequested {
		if _, err := r.Storage.PauseEvolutionJob(job.ID, fmt.Sprintf("paused at boundary before iteration %d", loopIteration())); err != nil {
			return LoopStepResult{}, err
		}
		return LoopStepResult{Parked: true, Iteration: loopIteration(), Phase: loopPhase()}, nil
	}

	// Human approve-early: finish now with the current best.
	if job.ApproveRequested {
		if err := r.Storage.applyApproveEarly(job); err != nil {
			return LoopStepResult{}, err
		}
		return LoopStepResult{Stopped: true, Outcome: LoopOutcomeCompleted, Iteration: loopIteration(), Phase: loopPhase()}, nil
	}

	// Claim from queued — the first boundary of a run, and every resume.
	if job.Status == EvolutionJobQueued {
		if _, err := r.Storage.ClaimEvolutionJob(job.ID, token, "loop-stepper"); err != nil {
			// The claim itself can go terminal: a queued job past its run cap
			// is marked timeout by the story 04 claim path. Reconcile the loop
			// and stop — a budget stop, not a stepper error.
			if fresh, gerr := r.Storage.GetEvolutionJob(jobID); gerr == nil && evolutionJobTerminal(fresh.Status) {
				outcome, rerr := r.Storage.reconcileLoopTerminal(fresh)
				if rerr != nil {
					return LoopStepResult{}, rerr
				}
				if outcome != "" {
					return LoopStepResult{Stopped: true, Outcome: outcome, Iteration: job.Iteration}, nil
				}
			}
			return LoopStepResult{}, err
		}
		job, err = r.Storage.GetEvolutionJob(jobID)
		if err != nil {
			return LoopStepResult{}, err
		}
	}

	loop, err := r.Storage.initLoopState(job)
	if err != nil {
		return LoopStepResult{}, err
	}
	// A concurrent or crashed stepper may have left a terminal state behind
	// between the first check and the claim; do not drive past it.
	if loop.Status == LoopStatusTerminal {
		return LoopStepResult{Stopped: true, Outcome: loop.Outcome, Iteration: loop.CurrentIteration, Phase: loop.CurrentPhase}, nil
	}

	// Pre-iteration bookkeeping: stop checks, skip already-resolved iterations
	// (a crash between the record write and the state advance), open the
	// iteration record, and transition to the first phase.
	if loop.CurrentPhase == LoopPhaseQueued {
		for {
			if loop.CurrentIteration > maxIterationsFor(job) {
				if _, err := r.Storage.completeLoopJob(job.ID, LoopOutcomeExhausted,
					fmt.Sprintf("used all %d iterations", maxIterationsFor(job))); err != nil {
					return LoopStepResult{}, err
				}
				return LoopStepResult{Stopped: true, Outcome: LoopOutcomeExhausted, Iteration: loop.CurrentIteration}, nil
			}
			rec, err := r.Storage.GetIterationRecord(job.ID, loop.CurrentIteration)
			if err != nil {
				return LoopStepResult{}, err
			}
			if rec == nil || rec.Outcome == "" {
				break
			}
			loop.CurrentIteration++
		}
		if _, err := r.Storage.mutateLoopState(job.ID, false, func(st *LoopState) {
			st.CurrentIteration = loop.CurrentIteration
			st.CurrentPhase = LoopPhaseInference
			st.PhaseStatus = LoopStatusRunning
		}); err != nil {
			return LoopStepResult{}, err
		}
		loop.CurrentPhase = LoopPhaseInference
	}

	if loop.CurrentPhase == LoopPhaseGating {
		return r.loopGate(job, token, loop)
	}
	return r.loopPhaseStep(job, token, input, loop, loop.CurrentPhase)
}

// loopPhaseStep drives one harness phase: mark it entered, spawn one CLI step
// with the phase named in the payload, then close or interrupt the entry
// according to how the step ended. A park that fired at the step's final
// heartbeat means every callback was applied and the phase is complete — the
// resume continues at the NEXT phase, never re-running completed work. A park
// mid-callbacks drops the rest of the file: the phase is interrupted and a
// resume re-runs it.
func (r *Runner) loopPhaseStep(job *EvolutionJob, token string, input JobInput, loop *LoopState, phase string) (LoopStepResult, error) {
	profile, ok := r.Profiles[job.Profile]
	if !ok {
		_, _ = r.Storage.CompleteEvolutionJob(job.ID, token, EvolutionJobOutcomeFailed, fmt.Sprintf("unknown CLI profile '%s'", job.Profile))
		return LoopStepResult{}, fmt.Errorf("cannot drive evolution job '%s': unknown CLI profile '%s'", job.ID, job.Profile)
	}

	// Mark the phase entered before anything runs, so an interruption is
	// always visible in the record.
	if _, err := r.Storage.appendPhaseEntry(job, loop.CurrentIteration, phase); err != nil {
		return LoopStepResult{}, err
	}

	spec := stepSpec{
		label:     fmt.Sprintf("%d-%s", loop.CurrentIteration, phase),
		phase:     phase,
		iteration: loop.CurrentIteration,
	}
	res, stepErr := r.runStep(job, token, profile, input, spec)
	if stepErr != nil {
		// A lease lost mid-callbacks is a requeue, not a run failure: the
		// sweep owns the job now and re-dispatch resumes at this phase.
		// Failing the run here would stamp a failed outcome on a resumable
		// run and wedge it behind "a stopped loop stays stopped".
		var leaseErr *JobLeaseExpiredError
		fresh, gerr := r.Storage.GetEvolutionJob(job.ID)
		if gerr == nil && !evolutionJobTerminal(fresh.Status) {
			if errors.As(stepErr, &leaseErr) {
				if _, rerr := r.Storage.reconcileLoopTerminal(fresh); rerr != nil {
					logRunnerf("loop reconcile after lease loss for job %s: %v", job.ID, rerr)
				}
				return LoopStepResult{Parked: true, Iteration: loop.CurrentIteration, Phase: phase}, nil
			}
			// The step failed. If nobody has gone terminal yet, fail the run —
			// a broken harness step is a run failure, not a designed stop — then
			// reconcile the loop records either way.
			_, _ = r.Storage.CompleteEvolutionJob(job.ID, token, EvolutionJobOutcomeFailed, stepErr.Error())
			if fresh, gerr = r.Storage.GetEvolutionJob(job.ID); gerr != nil {
				return LoopStepResult{}, gerr
			}
		}
		if gerr == nil {
			if _, rerr := r.Storage.reconcileLoopTerminal(fresh); rerr != nil {
				logRunnerf("loop reconcile after step failure for job %s: %v", job.ID, rerr)
			}
		}
		return LoopStepResult{Iteration: loop.CurrentIteration, Phase: phase}, stepErr
	}

	fresh, err := r.Storage.GetEvolutionJob(job.ID)
	if err != nil {
		return LoopStepResult{}, err
	}
	now := time.Now().UTC()

	switch {
	case res.done && fresh.Status == EvolutionJobPaused:
		if res.applied {
			// The whole phase applied; the park fired at the boundary
			// heartbeat. Close the phase and stand at the next one.
			if err := r.Storage.closePhaseEntry(job.ID, loop.CurrentIteration, phase, fresh.ProgressNote, now); err != nil {
				return LoopStepResult{}, err
			}
			next := nextLoopPhase(phase)
			if next == "" {
				next = LoopPhaseGating
			}
			if _, err := r.Storage.mutateLoopState(job.ID, false, func(st *LoopState) {
				st.Status = LoopStatusPaused
				st.CurrentPhase = next
				st.PhaseStatus = ""
				st.Note = fresh.ProgressNote
			}); err != nil {
				return LoopStepResult{}, err
			}
		} else {
			// Parked mid-callbacks: the phase is interrupted and re-runs on
			// resume. The record already holds the incomplete entry.
			if _, err := r.Storage.mutateLoopState(job.ID, false, func(st *LoopState) {
				st.Status = LoopStatusPaused
				st.CurrentPhase = phase
				st.PhaseStatus = LoopPhaseStatusInterrupted
				st.Note = fresh.ProgressNote
			}); err != nil {
				return LoopStepResult{}, err
			}
		}
		return LoopStepResult{Parked: true, Iteration: loop.CurrentIteration, Phase: phase}, nil

	case res.done:
		// Terminal during the phase (harness complete callback, run cap) — or
		// the lease was lost mid-step and the final heartbeat reported the
		// loss. reconcile reads the ACTUAL job state: a genuinely terminal
		// job maps to its loop outcome; a requeued (non-terminal) job gets ""
		// and the loop stays resumable at this phase boundary instead of
		// wedging behind a fabricated "failed" outcome.
		outcome, rerr := r.Storage.reconcileLoopTerminal(fresh)
		if rerr != nil {
			return LoopStepResult{}, rerr
		}
		if outcome == "" {
			// Not a loop stop: nothing is driving and the phase stands
			// interrupted — report the park shape so the driver re-dispatches.
			return LoopStepResult{Parked: true, Iteration: loop.CurrentIteration, Phase: phase}, nil
		}
		return LoopStepResult{Stopped: true, Outcome: outcome, Iteration: loop.CurrentIteration, Phase: phase}, nil
	}

	// Clean completion: close the phase and stand at the next one.
	if err := r.Storage.closePhaseEntry(job.ID, loop.CurrentIteration, phase, fresh.ProgressNote, now); err != nil {
		return LoopStepResult{}, err
	}
	next := nextLoopPhase(phase)
	if next == "" {
		next = LoopPhaseGating
	}
	if _, err := r.Storage.mutateLoopState(job.ID, false, func(st *LoopState) {
		st.CurrentPhase = next
		st.PhaseStatus = ""
		st.Note = fresh.ProgressNote
	}); err != nil {
		return LoopStepResult{}, err
	}
	return LoopStepResult{Advanced: true, Iteration: loop.CurrentIteration, Phase: phase}, nil
}

// loopGate resolves the iteration's validation gate server-side and either
// advances to iteration k+1 or stops the loop. It runs the story 03 gate by
// promoting the iteration's candidate — an accept promotes (stamping the
// story 06 trained marker and the audit record), a rejection leaves the live
// skill byte-identical and marks the candidate rejected — and derives the
// plateau counter from the iteration records, so a crash cannot lose it.
func (r *Runner) loopGate(job *EvolutionJob, token string, loop *LoopState) (LoopStepResult, error) {
	k := loop.CurrentIteration

	// A crash between the record resolution and the state advance leaves a
	// resolved record under a standing gating phase: advance without gating
	// again, closing the open gating entry.
	rec, err := r.Storage.GetIterationRecord(job.ID, k)
	if err != nil {
		return LoopStepResult{}, err
	}
	if rec != nil && rec.Outcome != "" {
		if cerr := r.Storage.closePhaseEntry(job.ID, k, LoopPhaseGating, "", time.Now().UTC()); cerr != nil {
			return LoopStepResult{}, cerr
		}
		if _, err := r.Storage.mutateLoopState(job.ID, false, func(st *LoopState) {
			st.CurrentIteration = k + 1
			st.CurrentPhase = LoopPhaseQueued
			st.PhaseStatus = ""
		}); err != nil {
			return LoopStepResult{}, err
		}
		return LoopStepResult{Advanced: true, Iteration: k, Phase: LoopPhaseGating}, nil
	}

	if _, err := r.Storage.appendPhaseEntry(job, k, LoopPhaseGating); err != nil {
		return LoopStepResult{}, err
	}

	// Resolve the gate.
	cand, err := r.Storage.findCandidateForGate(job, rec)
	if err != nil {
		return LoopStepResult{}, err
	}
	outcome := IterationOutcomeRejected
	valScore, rBest := 0.0, 0.0
	resultVersion := 0
	var patterns []string
	note := "no candidate proposed during the proposing phase"
	if cand != nil {
		note = ""
		patterns = cand.PatternSlugs
		art, perr := r.Storage.PromoteSkillCandidate(cand.ID)
		if perr == nil {
			decided, derr := r.Storage.GetSkillCandidate(cand.ID)
			if derr != nil {
				return LoopStepResult{}, derr
			}
			outcome = IterationOutcomeAccepted
			valScore = decided.BestScore
			rBest = decided.BestScore
			resultVersion = art.Version
			note = fmt.Sprintf("gate accepted candidate %s", cand.ID)
		} else if gateRej := (*SkillGateRejection)(nil); errors.As(perr, &gateRej) {
			outcome = IterationOutcomeRejected
			valScore = gateRej.Score
			rBest = gateRej.Best
			note = "gate rejected the candidate (score does not beat R_best)"
		} else {
			// A real fault: fail the run, leaving the iteration interrupted.
			fresh, gerr := r.Storage.GetEvolutionJob(job.ID)
			if gerr == nil && !evolutionJobTerminal(fresh.Status) {
				_, _ = r.Storage.CompleteEvolutionJob(job.ID, token, EvolutionJobOutcomeFailed, perr.Error())
				if fresh, gerr = r.Storage.GetEvolutionJob(job.ID); gerr != nil {
					return LoopStepResult{}, gerr
				}
			}
			if gerr == nil {
				if _, rerr := r.Storage.reconcileLoopTerminal(fresh); rerr != nil {
					logRunnerf("loop reconcile after gate failure for job %s: %v", job.ID, rerr)
				}
			}
			return LoopStepResult{Iteration: k, Phase: LoopPhaseGating}, perr
		}
	} else {
		if best, berr := r.Storage.currentRBest(job.SkillSlug); berr == nil {
			rBest = best
		}
	}

	// Stamp the decision on the iteration record before anything advances.
	if _, err := r.Storage.resolveIterationRecord(job.ID, k, func(r *IterationRecord) {
		r.Outcome = outcome
		r.ValScore = valScore
		r.RBest = rBest
		r.CandidateID = candidateIDOrEmpty(cand)
		r.PatternSlugs = patterns
		r.ResultVersion = resultVersion
		r.Note = note
	}); err != nil {
		return LoopStepResult{}, err
	}
	if err := r.Storage.closePhaseEntry(job.ID, k, LoopPhaseGating, "", time.Now().UTC()); err != nil {
		return LoopStepResult{}, err
	}

	// Stop conditions, in the gate's own order: a perfect score stops first,
	// then the plateau rule.
	if outcome == IterationOutcomeAccepted && valScore >= 1.0 {
		if _, err := r.Storage.completeLoopJob(job.ID, LoopOutcomeCompleted,
			fmt.Sprintf("iteration %d reached a perfect validation score of 1.0", k)); err != nil {
			return LoopStepResult{}, err
		}
		return LoopStepResult{Stopped: true, Outcome: LoopOutcomeCompleted, Iteration: k, Phase: LoopPhaseGating}, nil
	}

	plateauCount, err := r.Storage.computePlateauCount(job.ID)
	if err != nil {
		return LoopStepResult{}, err
	}
	limit := plateauLimitFor(job)
	if outcome == IterationOutcomeRejected && plateauCount >= limit {
		if _, err := r.Storage.completeLoopJob(job.ID, LoopOutcomePlateaued,
			fmt.Sprintf("%d consecutive rejected iterations without pattern gain (plateau limit %d)", plateauCount, limit)); err != nil {
			return LoopStepResult{}, err
		}
		return LoopStepResult{Stopped: true, Outcome: LoopOutcomePlateaued, Iteration: k, Phase: LoopPhaseGating}, nil
	}

	if _, err := r.Storage.mutateLoopState(job.ID, false, func(st *LoopState) {
		st.CurrentIteration = k + 1
		st.CurrentPhase = LoopPhaseQueued
		st.PhaseStatus = ""
	}); err != nil {
		return LoopStepResult{}, err
	}
	return LoopStepResult{Advanced: true, Iteration: k, Phase: LoopPhaseGating}, nil
}

// computePlateauCount derives the trailing run of consecutive
// rejected-without-pattern-gain iterations from the records themselves: walk
// back from the highest resolved iteration while the run holds. An accepted
// iteration ends it; a rejected iteration that touched a pattern no earlier
// iteration touched (pattern gain) also ends it.
func (s *Storage) computePlateauCount(jobID string) (int, error) {
	recs, err := s.ListIterationRecords(jobID)
	if err != nil {
		return 0, err
	}
	var resolved []IterationRecord
	for _, rec := range recs {
		if rec.Outcome == IterationOutcomeAccepted || rec.Outcome == IterationOutcomeRejected {
			resolved = append(resolved, rec)
		}
	}
	count := 0
	for i := len(resolved) - 1; i >= 0; i-- {
		rec := resolved[i]
		if rec.Outcome != IterationOutcomeRejected {
			break
		}
		gain := false
		for _, slug := range rec.PatternSlugs {
			seen := false
			for _, prev := range resolved[:i] {
				for _, p := range prev.PatternSlugs {
					if p == slug {
						seen = true
						break
					}
				}
				if seen {
					break
				}
			}
			if !seen {
				gain = true
				break
			}
		}
		if gain {
			break
		}
		count++
	}
	return count, nil
}

// candidateIDOrEmpty keeps the record schema honest: no candidate, empty field.
func candidateIDOrEmpty(c *SkillCandidate) string {
	if c == nil {
		return ""
	}
	return c.ID
}

// PauseEvolutionLoop is the human pause control over the loop stepper: the
// story 04 cooperative park integrated with the loop state. The park takes
// effect at the next step boundary (immediately when nothing is driving).
// Audited in the activity log; story 08 wires HTTP to this.
func (srv *Server) PauseEvolutionLoop(jobID, checkpoint string) (*EvolutionJob, error) {
	job, err := srv.Storage.PauseEvolutionJob(jobID, checkpoint)
	if err != nil {
		return nil, err
	}
	srv.auditLoopControl("pause", job)
	return job, nil
}

// AbortEvolutionLoop is the human abort control: the story 04 cancel path
// (SIGTERM, 10s grace, SIGKILL) reconciled with the loop stepper's records.
// The skill stays at its last accepted version and the wiki is untouched.
// Audited in the activity log.
func (srv *Server) AbortEvolutionLoop(jobID, reason string) (*EvolutionJob, error) {
	job, err := srv.Storage.CancelEvolutionJob(jobID, reason)
	if err != nil {
		return nil, err
	}
	srv.auditLoopControl("abort", job)
	return job, nil
}

// ApproveEarlyEvolutionLoop is the human approve-early control over the loop
// stepper. Audited in the activity log.
func (srv *Server) ApproveEarlyEvolutionLoop(jobID string) (*EvolutionJob, error) {
	job, err := srv.Storage.ApproveEarlyEvolutionJob(jobID)
	if err != nil {
		return nil, err
	}
	srv.auditLoopControl("approve", job)
	return job, nil
}

// auditLoopControl records a human loop control in the durable activity log —
// the same trail every wiki action lands in. These are operator actions over
// the job, not MCP tool calls, so they read as "api" with the control name.
func (srv *Server) auditLoopControl(action string, job *EvolutionJob) {
	if srv == nil || srv.EventBus == nil || job == nil {
		return
	}
	srv.EventBus.PublishActivity("api", action, "", job.ID, job.SkillSlug, DefaultAgentName)
}

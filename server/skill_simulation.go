package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// This file holds story 11 (Phase B4): the pre-live simulation — the "validate
// before accept" number for humans.
//
// WHAT IT DOES: for one evolution job and one candidate, the wiki scores the
// candidate's proposed body against N historical val cases from the job's
// stored eval set — the exact scorers and split the validation gate will use —
// WITHOUT gating. No R_best compare, no audit record, no promotion, no state
// change: the run is purely an informational computation whose result is
// appended as an immutable, hash-linked record alongside the job.
//
// THE RECORD: data/skill_jobs/<id>.files/simulations/sim-001.json … one file
// per simulation, written once (O_EXCL) and never rewritten. Re-running an
// identical simulation appends a new record — history is kept, like the
// append-only audit trail. Every record carries the SHA-256 result hash over
// its canonical content (which includes its prev_hash), and prev_hash chains
// to the previous record's result hash, so the trail is tamper-evident in the
// same way the eval splits are (skill_layers.go).
//
// REFUSALS: an in-flight run (claimed/running) is refused — a simulation
// racing the live gate would describe a state that is about to change;
// queued, paused, and terminal jobs simulate freely. A job without a stored
// eval set has no historical cases to simulate. A candidate that is missing,
// decided, from another skill, or diff-only (no body to score) is refused with
// a specific error.
//
// SURFACE: internal Go API (RunSkillSimulation) plus REST under /api
// (POST /api/evolution/jobs/{id}/simulate, GET /api/evolution/jobs/{id}/
// simulations), following the skill_wizard_http.go patterns. Not an MCP tool —
// the docs-integrity rule keeps the tool surface minimal, and the browser is
// the operator here.

// simMaxValCases bounds one simulation record: the first N stored val cases
// (stored order is deterministic — upload order). Keeps a single record small
// enough to poll from the wizard even for a 5000-case upload.
const simMaxValCases = 200

// RunEvolutionSimulation is the audited operator wrapper over the storage
// path: the computation appends an immutable record and the action lands in
// the durable activity log exactly like the other operator controls.
func (srv *Server) RunEvolutionSimulation(jobID, candidateID string) (*SkillSimulationRecord, error) {
	rec, err := srv.Storage.RunSkillSimulation(jobID, candidateID)
	if err != nil {
		return nil, err
	}
	if srv != nil && srv.EventBus != nil {
		srv.EventBus.PublishActivity("api", "simulate", "", rec.JobID, rec.SkillSlug, DefaultAgentName)
	}
	return rec, nil
}

// SkillSimulationCase is one per-case row of a simulation: quoted, truncated
// previews plus the exact score the gate math would have produced.
type SkillSimulationCase struct {
	Index           int     `json:"index"`
	InputPreview    string  `json:"input_preview"`
	ExpectedPreview string  `json:"expected_preview"`
	Scorer          string  `json:"scorer,omitempty"`
	Score           float64 `json:"score"`
	Pass            bool    `json:"pass"`
}

// SkillSimulationRecord is one immutable pre-live simulation. ResultHash is
// computed over the record's canonical JSON with ResultHash cleared, so the
// hash covers everything including the chain link.
type SkillSimulationRecord struct {
	JobID       string `json:"job_id"`
	SkillSlug   string `json:"skill_slug"`
	Sequence    int    `json:"simulation"`
	CandidateID string `json:"candidate_id"`
	// CandidateParentVersion is the skill version the candidate was cut from;
	// CandidateHash is the story-03 content hash over diff+body.
	CandidateParentVersion int                   `json:"candidate_parent_version"`
	CandidateHash          string                `json:"candidate_content_hash"`
	EvalHash               string                `json:"eval_hash"`
	ScorerVersion          string                `json:"scorer_version"`
	ValCases               int                   `json:"val_cases"`
	ValCasesCapped         bool                  `json:"val_cases_capped,omitempty"`
	ResolutionRate         float64               `json:"resolution_rate"`
	PassCount              int                   `json:"pass_count"`
	Cases                  []SkillSimulationCase `json:"cases"`
	ComputedAt             time.Time             `json:"computed_at"`
	PrevHash               string                `json:"prev_hash"`
	ResultHash             string                `json:"result_hash"`
}

// simulationsDir resolves data/skill_jobs/<id>.files/simulations.
func (s *Storage) simulationsDir(jobID string) (string, error) {
	base, err := s.jobFilesDir(jobID)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "simulations"), nil
}

// simulationRecordPath resolves the zero-padded per-simulation record file.
func (s *Storage) simulationRecordPath(jobID string, sequence int) (string, error) {
	if sequence <= 0 {
		return "", fmt.Errorf("simulation sequence must be positive")
	}
	dir, err := s.simulationsDir(jobID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("sim-%03d.json", sequence)), nil
}

// simulationResultHash hashes the record's canonical JSON with the result
// hash itself cleared — the chain link (PrevHash) is included, so rewriting
// any prior record breaks every later hash.
func simulationResultHash(rec *SkillSimulationRecord) string {
	probe := *rec
	probe.ResultHash = ""
	raw, err := json.Marshal(probe)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ListSkillSimulations returns a job's simulation records, oldest first.
// Corrupt lines do not exist here — one file per record — but a record that
// fails to parse is skipped, as in every other listing.
func (s *Storage) ListSkillSimulations(jobID string) ([]SkillSimulationRecord, error) {
	dir, err := s.simulationsDir(jobID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SkillSimulationRecord{}, nil
		}
		return nil, err
	}
	var out []SkillSimulationRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "sim-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSuffix(e.Name(), ".json"), "sim-%d", &n); err != nil || n <= 0 {
			continue
		}
		path, err := s.simulationRecordPath(jobID, n)
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec SkillSimulationRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	if out == nil {
		out = []SkillSimulationRecord{}
	}
	return out, nil
}

// RunSkillSimulation scores one candidate's proposed body against the job's
// stored val cases, without gating, and appends the immutable, hash-linked
// result. The computation is exactly the gate's own math — same stored eval
// set, same compiled scorers, same 4-decimal rounding — which is what makes
// the number a human can compare against R_best meaningful.
func (s *Storage) RunSkillSimulation(jobID, candidateID string) (*SkillSimulationRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	job, err := s.GetEvolutionJob(jobID)
	if err != nil {
		return nil, err
	}
	switch job.Status {
	case EvolutionJobClaimed, EvolutionJobRunning:
		return nil, fmt.Errorf("cannot run pre-live simulation for evolution job '%s': status is %s — the run is in flight; pause or finish it first", job.ID, job.Status)
	}
	if _, _, meta, err := s.ReadEvolutionEvalSet(job.ID); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("cannot run pre-live simulation for evolution job '%s': no eval set stored — upload training data first (there are no historical cases to simulate)", job.ID)
		}
		return nil, err
	} else if meta.ValCount == 0 {
		return nil, fmt.Errorf("cannot run pre-live simulation for evolution job '%s': the stored eval set holds no val cases", job.ID)
	}
	train, val, meta, err := s.ReadEvolutionEvalSet(job.ID)
	if err != nil {
		return nil, err
	}

	candidateID = strings.TrimSpace(candidateID)
	var cand *SkillCandidate
	if candidateID == "" {
		// Default target: the newest pending candidate for the skill (the
		// candidate the wizard's Review step would approve).
		cand, err = s.newestPendingCandidateForSkill(job.SkillSlug)
		if err != nil {
			return nil, err
		}
		if cand == nil {
			return nil, fmt.Errorf("cannot run pre-live simulation for evolution job '%s': no pending candidate to simulate — propose one or name candidate_id explicitly", job.ID)
		}
	} else {
		cand, err = s.GetSkillCandidate(candidateID)
		if err != nil {
			return nil, err
		}
		if cand.ParentSlug != job.SkillSlug {
			return nil, fmt.Errorf("candidate '%s' evolves '%s', not '%s' — the simulation must run a candidate of this job's skill", cand.ID, cand.ParentSlug, job.SkillSlug)
		}
	}
	if cand.Status != SkillCandidatePending {
		return nil, fmt.Errorf("cannot run pre-live simulation for candidate '%s': status is %s (only pending candidates simulate)", cand.ID, cand.Status)
	}
	if strings.TrimSpace(cand.ProposedBody) == "" {
		return nil, fmt.Errorf("cannot run pre-live simulation for candidate '%s': diff-only candidates carry no body to score — propose a full body", cand.ID)
	}

	// Fail-closed reproduction checks, shared with the gate (EvalGateForSkill):
	// the stored hash and scorer version must regenerate from the files.
	version := evalScorerVersionOfCases(train, val)
	if version != meta.ScorerVersion {
		return nil, fmt.Errorf("eval set for job '%s' failed the scorer-version check: stored files derive %q, meta records %q", job.ID, version, meta.ScorerVersion)
	}
	recomputed := evalSetHash(train, val, version)
	if recomputed != meta.EvalHash {
		return nil, fmt.Errorf("eval set for job '%s' failed the hash check: stored files hash to %.12s, meta records %.12s", job.ID, recomputed, meta.EvalHash)
	}
	set, err := compileScorerSet(append(append([]EvalCase{}, train...), val...))
	if err != nil {
		return nil, fmt.Errorf("eval set for job '%s' has an unscorable scorer: %w", job.ID, err)
	}

	// The simulated pass: N historical val cases, no gating.
	simVal := val
	capped := false
	if len(simVal) > simMaxValCases {
		simVal = simVal[:simMaxValCases]
		capped = true
	}
	cases := make([]SkillSimulationCase, 0, len(simVal))
	passCount := 0
	total := 0.0
	for i, c := range simVal {
		score := set.forCase(c).score(c.Input, c.Expected, cand.ProposedBody)
		if score >= 0.5 {
			passCount++
		}
		total += score
		cases = append(cases, SkillSimulationCase{
			Index:           i,
			InputPreview:    truncateQuoted(c.Input, evalPreviewLen),
			ExpectedPreview: truncateQuoted(c.Expected, evalPreviewLen),
			Scorer:          strings.TrimSpace(c.Scorer),
			Score:           score,
			Pass:            score >= 0.5,
		})
	}

	rec := &SkillSimulationRecord{
		JobID:                  job.ID,
		SkillSlug:              job.SkillSlug,
		CandidateID:            cand.ID,
		CandidateParentVersion: cand.ParentVersion,
		CandidateHash:          SkillCandidateContentHash(cand),
		EvalHash:               meta.EvalHash,
		ScorerVersion:          version,
		ValCases:               len(simVal),
		ValCasesCapped:         capped,
		ResolutionRate:         roundScore4(total / float64(len(simVal))),
		PassCount:              passCount,
		Cases:                  cases,
		ComputedAt:             time.Now().UTC(),
	}

	// Chain link: the previous record's result hash ("" for the first). The
	// directory listing decides the sequence, so a crash between the record
	// write and the job stamp cannot fork the trail — the next run chains
	// onto whatever is actually on disk.
	existing, err := s.ListSkillSimulations(job.ID)
	if err != nil {
		return nil, err
	}
	rec.Sequence = len(existing) + 1
	if len(existing) > 0 {
		rec.PrevHash = existing[len(existing)-1].ResultHash
	}
	rec.ResultHash = simulationResultHash(rec)

	// Immutable write: O_EXCL, never overwrite. A colliding file (a record a
	// crashed writer left complete but unstamped after our listing) bumps the
	// sequence and re-chains instead of rewriting history.
	dir, err := s.simulationsDir(job.ID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create simulations directory: %w", err)
	}
	for {
		path, err := s.simulationRecordPath(job.ID, rec.Sequence)
		if err != nil {
			return nil, err
		}
		raw, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to encode simulation record: %w", err)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			if os.IsExist(err) {
				existing, err = s.ListSkillSimulations(job.ID)
				if err != nil {
					return nil, err
				}
				rec.Sequence = len(existing) + 1
				if len(existing) > 0 {
					rec.PrevHash = existing[len(existing)-1].ResultHash
				}
				rec.ResultHash = simulationResultHash(rec)
				continue
			}
			return nil, fmt.Errorf("failed to store simulation record: %w", err)
		}
		if _, err := f.Write(append(raw, '\n')); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("failed to store simulation record: %w", err)
		}
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("failed to store simulation record: %w", err)
		}

		// Stamp the trail on the job record (under the held writeMu).
		job.SimulationCount = rec.Sequence
		job.LatestSimulationHash = rec.ResultHash
		if err := s.writeEvolutionJob(job); err != nil {
			return nil, err
		}
		logRunnerf("pre-live simulation for job %s: candidate %s over %d val cases, resolution %.4f (%d passed), scorer %s",
			job.ID, cand.ID, rec.ValCases, rec.ResolutionRate, rec.PassCount, rec.ScorerVersion)
		return rec, nil
	}
}

package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// This file holds story 04 of the wikiskill evolution support plan: the headless
// background job runner for skill evolution.
//
// Headless means UI-only control with no terminals and no PTY: the server spawns a
// BYO CLI (opencode, claude-code — see skill_runner.go) as a same-host child process
// with stdout/stderr captured to files, and the operator drives it through the job
// lifecycle below instead of a terminal.
//
// JOB MODEL: an EvolutionJob is a data record, never wiki content — stored as JSON
// under data/skill_jobs, never indexed, never executed. It moves:
//
//	queued → claimed → running → complete | failed | timeout | cancelled
//	                  ↘ paused (cooperative, at a step boundary) → queued on resume
//
// Heartbeats and progress callbacks keep a claimed/running job alive; artifact
// uploads attach hash-verified files. Only terminal records (complete, failed,
// timeout, cancelled) release the per-skill lock.
//
// ONE RUN PER SKILL: creating a job while the skill already has an active
// (queued/claimed/running/paused) job is refused, so two harnesses can never evolve
// the same skill concurrently. A terminal record releases the lock by construction.
//
// LEASES (crash recovery, not wall clocks for correctness):
//
//	60s heartbeat lease — a claimed/running job with no heartbeat inside the lease
//	  is expired: the requeue sweep returns it to queued with its claim cleared.
//	10min claim timeout — a claimed job that never heartbeats within 10 minutes of
//	  its claim is requeued the same way (a stuck claimer cannot pin a skill).
//	90min run cap — a job past its run deadline is marked timeout, never silently
//	  dropped: the timeout record carries the last progress note as the reason.
//	12 iterations max — a progress/heartbeat past the iteration cap fails the job.
//
// The sweep (RequeueExpiredJobs) is called by the runner before every dispatch and
// is operator tooling otherwise; there is no background goroutine owning the wiki.
//
// PER-HARNESS TOKENS: CreateEvolutionJob mints a random token returned ONCE in the
// create response. Every lifecycle call after that (claim/heartbeat/artifact/
// complete) must present it. Tokens are stored as SHA-256 hashes — the raw token
// never lands in a job file, an article, or a log — and a token is valid for its
// job only: presenting job A's token against job B is denied and audited exactly
// like a story-02 scope denial (deny action on the activity log). The story-02
// seam (roleForRequest) is resolved here: a valid job token authorizes the job's
// own lifecycle tools regardless of the process-wide phase role; anything else
// falls through to the normal scope gate, where the jobs.manage scope (granted to
// no phase) keeps agent roles out.
//
// TRAINING-DATA BOUNDARY: wiki content reaches the harness quoted-as-data inside
// the stdin payload's `data` block (see JobStdinPayload in skill_runner.go), never
// as instructions and never interpolated into shell — the profile validator
// (skill_runner.go) rejects templates with interpolation placeholders, and runner
// argv is fixed at server configuration time.

// Evolution job lifecycle states.
const (
	EvolutionJobQueued    = "queued"
	EvolutionJobClaimed   = "claimed"
	EvolutionJobRunning   = "running"
	EvolutionJobPaused    = "paused"
	EvolutionJobComplete  = "complete"
	EvolutionJobFailed    = "failed"
	EvolutionJobTimeout   = "timeout"
	EvolutionJobCancelled = "cancelled"
)

// Evolution job completion outcomes accepted by complete_evolution_job.
const (
	EvolutionJobOutcomeComplete = "complete"
	EvolutionJobOutcomeFailed   = "failed"
)

// Lease and budget parameters. Vars (not consts) so in-package tests can shrink
// them without sleeping on wall-clock timeouts; production values are the spec.
var (
	evolutionJobHeartbeatLease = 60 * time.Second
	evolutionJobClaimTimeout   = 10 * time.Minute
	evolutionJobRunCap         = 90 * time.Minute
	evolutionJobMaxIterations  = 12
	evolutionJobKillGrace      = 10 * time.Second
)

// Artifact caps: a harness must not fill the data directory unchecked.
const (
	evolutionJobMaxArtifactBytes = 1 << 20 // 1 MiB per artifact
	evolutionJobMaxArtifacts     = 32      // artifacts per job
)

// JobArtifact is one hash-verified file attached to a job. Content lives on disk
// under data/skill_jobs/<id>/artifacts/<name>; the record carries the hash the
// runner checked before acceptance (SHA-256 hex, the story-03 content-hash
// convention) plus the idempotency key that deduplicates re-uploads.
type JobArtifact struct {
	Name           string    `json:"name"`
	Path           string    `json:"path"`
	ContentHash    string    `json:"content_hash"`
	Size           int64     `json:"size"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	UploadedAt     time.Time `json:"uploaded_at"`
}

// EvolutionJob is one headless evolution run against a single skill.
type EvolutionJob struct {
	ID              string            `json:"id"`
	SkillSlug       string            `json:"skill_slug"`
	CandidateID     string            `json:"candidate_id,omitempty"`
	Profile         string            `json:"profile"`
	Status          string            `json:"status"`
	Iteration       int               `json:"iteration"`
	MaxIterations   int               `json:"max_iterations"`
	TokenHash       string            `json:"token_hash"`
	Runner          string            `json:"runner,omitempty"`
	PID             int               `json:"pid,omitempty"`
	Progress        float64           `json:"progress,omitempty"`
	ProgressNote    string            `json:"progress_note,omitempty"`
	Artifacts       []JobArtifact     `json:"artifacts,omitempty"`
	IdempotencyKeys map[string]string `json:"idempotency_keys,omitempty"`
	// Story 05 eval intake: summary of the accepted training-data upload.
	// The full splits live under <id>.files/eval/ (train.jsonl, val.jsonl,
	// meta.json); these fields let operators read readiness off the job JSON.
	EvalHash       string    `json:"eval_hash,omitempty"`
	EvalTrainCount int       `json:"eval_train_count,omitempty"`
	EvalValCount   int       `json:"eval_val_count,omitempty"`
	BaselineS0     float64   `json:"baseline_s0,omitempty"`
	BaselineScorer string    `json:"baseline_scorer,omitempty"`
	EvalUploadedAt time.Time `json:"eval_uploaded_at,omitzero"`
	// Story 07 loop stepper: how the evolution loop ended (completed, exhausted,
	// plateaued, cancelled — see skill_loop.go), the human approve-early request,
	// and the plateau stop rule (consecutive rejected iterations with no pattern
	// gain; 0 falls back to the story default of 3).
	LoopOutcome      string    `json:"loop_outcome,omitempty"`
	ApproveRequested bool      `json:"approve_requested,omitempty"`
	PlateauLimit     int       `json:"plateau_limit,omitempty"`
	Checkpoint       string    `json:"checkpoint,omitempty"`
	PauseRequested   bool      `json:"pause_requested,omitempty"`
	CancelReason     string    `json:"cancel_reason,omitempty"`
	Error            string    `json:"error,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	ClaimedAt        time.Time `json:"claimed_at,omitzero"`
	LastHeartbeat    time.Time `json:"last_heartbeat,omitzero"`
	LeaseExpiresAt   time.Time `json:"lease_expires_at,omitzero"`
	RunDeadlineAt    time.Time `json:"run_deadline_at"`
	CompletedAt      time.Time `json:"completed_at,omitzero"`
}

// evolutionJobActive reports whether the job holds the per-skill lock.
func evolutionJobActive(status string) bool {
	switch status {
	case EvolutionJobQueued, EvolutionJobClaimed, EvolutionJobRunning, EvolutionJobPaused:
		return true
	default:
		return false
	}
}

// evolutionJobTerminal reports whether the job released the per-skill lock.
func evolutionJobTerminal(status string) bool {
	return !evolutionJobActive(status)
}

// jobPath validates a job ID and resolves its storage path, confining reads to
// the job directory the way candidatePath does for candidates.
func (s *Storage) jobPath(id string) (string, error) {
	base := filepath.Base(strings.TrimSpace(id))
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, `/\`) {
		return "", fmt.Errorf("invalid evolution job id")
	}
	if s.JobDir == "" {
		return "", fmt.Errorf("evolution job storage is not configured")
	}
	return filepath.Join(s.JobDir, base+".json"), nil
}

// jobFilesDir resolves the per-job working directory (logs, artifacts).
func (s *Storage) jobFilesDir(id string) (string, error) {
	base := filepath.Base(strings.TrimSpace(id))
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, `/\`) {
		return "", fmt.Errorf("invalid evolution job id")
	}
	if s.JobDir == "" {
		return "", fmt.Errorf("evolution job storage is not configured")
	}
	return filepath.Join(s.JobDir, base+".files"), nil
}

func (s *Storage) writeEvolutionJob(job *EvolutionJob) error {
	if s.JobDir == "" {
		return fmt.Errorf("evolution job storage is not configured")
	}
	if err := os.MkdirAll(s.JobDir, 0755); err != nil {
		return fmt.Errorf("failed to create evolution job directory: %w", err)
	}
	path, err := s.jobPath(job.ID)
	if err != nil {
		return err
	}
	job.UpdatedAt = time.Now().UTC()
	raw, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode evolution job: %w", err)
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		return fmt.Errorf("failed to write evolution job: %w", err)
	}
	return nil
}

// GetEvolutionJob loads a job record by ID.
func (s *Storage) GetEvolutionJob(id string) (*EvolutionJob, error) {
	path, err := s.jobPath(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("evolution job not found: %s", id)
		}
		return nil, err
	}
	var job EvolutionJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, fmt.Errorf("invalid evolution job record %s: %w", id, err)
	}
	return &job, nil
}

// ListEvolutionJobs returns every job record, oldest first.
func (s *Storage) ListEvolutionJobs() ([]EvolutionJob, error) {
	entries, err := os.ReadDir(s.JobDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []EvolutionJob{}, nil
		}
		return nil, err
	}
	var out []EvolutionJob
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		job, err := s.GetEvolutionJob(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		out = append(out, *job)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if out == nil {
		out = []EvolutionJob{}
	}
	return out, nil
}

// activeEvolutionJobForSkill returns the live job holding the skill's lock, if any.
func (s *Storage) activeEvolutionJobForSkill(skillSlug string) (*EvolutionJob, error) {
	jobs, err := s.ListEvolutionJobs()
	if err != nil {
		return nil, err
	}
	for i := range jobs {
		if jobs[i].SkillSlug == skillSlug && evolutionJobActive(jobs[i].Status) {
			active := jobs[i]
			return &active, nil
		}
	}
	return nil, nil
}

// mintJobToken generates a random per-harness token and its stored hash. The raw
// token is returned once to the creator; only the hash is persisted.
func mintJobToken() (raw, hash string, err error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", fmt.Errorf("failed to mint job token: %w", err)
	}
	raw = hex.EncodeToString(buf[:])
	sum := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(sum[:]), nil
}

// verifyJobToken reports whether the presented token unlocks this job. Empty is
// never valid — a missing credential is a denial, not a lookup miss.
func verifyJobToken(job *EvolutionJob, token string) bool {
	token = strings.TrimSpace(token)
	if job == nil || token == "" || job.TokenHash == "" {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(job.TokenHash)) == 1
}

// CreateEvolutionJob records a queued evolution run for one skill and mints its
// per-harness token. One run per skill: an active job on the same skill refuses
// creation. The raw token is returned once — it is never stored and never
// retrievable again.
func (s *Storage) CreateEvolutionJob(skillSlug, candidateID, profile string) (*EvolutionJob, string, error) {
	skillSlug = Slugify(skillSlug)
	profile = strings.TrimSpace(profile)
	if skillSlug == "" {
		return nil, "", fmt.Errorf("skill slug is required to create an evolution job")
	}
	if profile == "" {
		profile = DefaultJobProfile
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	skill, err := s.GetArticle(skillSlug)
	if err != nil {
		return nil, "", fmt.Errorf("cannot create evolution job: skill not found: %s", skillSlug)
	}
	if skill.Type != ContentTypeSkill {
		return nil, "", fmt.Errorf("cannot create evolution job for '%s': not a Custom AI Skill", skillSlug)
	}
	if strings.TrimSpace(candidateID) != "" {
		c, err := s.GetSkillCandidate(strings.TrimSpace(candidateID))
		if err != nil {
			return nil, "", fmt.Errorf("cannot create evolution job: %w", err)
		}
		if c.ParentSlug != skill.Slug {
			return nil, "", fmt.Errorf("cannot create evolution job: candidate '%s' evolves '%s', not '%s'", c.ID, c.ParentSlug, skill.Slug)
		}
	}
	if active, err := s.activeEvolutionJobForSkill(skill.Slug); err != nil {
		return nil, "", err
	} else if active != nil {
		return nil, "", fmt.Errorf("cannot create evolution job: skill '%s' already has an active job '%s' (%s) — one run per skill",
			skill.Slug, active.ID, active.Status)
	}

	raw, hash, err := mintJobToken()
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	job := &EvolutionJob{
		SkillSlug:     skill.Slug,
		CandidateID:   strings.TrimSpace(candidateID),
		Profile:       profile,
		Status:        EvolutionJobQueued,
		MaxIterations: evolutionJobMaxIterations,
		PlateauLimit:  evolutionPlateauDefault,
		TokenHash:     hash,
		CreatedAt:     now,
		UpdatedAt:     now,
		RunDeadlineAt: now.Add(evolutionJobRunCap),
	}
	base := fmt.Sprintf("job-%s-%s", skill.Slug, now.Format("20060102-150405"))
	job.ID = base
	for i := 2; ; i++ {
		path, err := s.jobPath(job.ID)
		if err != nil {
			return nil, "", err
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		job.ID = fmt.Sprintf("%s-%d", base, i)
	}
	if err := s.writeEvolutionJob(job); err != nil {
		return nil, "", err
	}
	return job, raw, nil
}

// claimEvolutionJobLocked moves a queued job to claimed. Callers hold writeMu.
func (s *Storage) claimEvolutionJobLocked(job *EvolutionJob, runner string, now time.Time) {
	job.Status = EvolutionJobClaimed
	job.Runner = strings.TrimSpace(runner)
	job.ClaimedAt = now
	job.LastHeartbeat = now
	job.LeaseExpiresAt = now.Add(evolutionJobHeartbeatLease)
}

// ClaimEvolutionJob claims a queued job for a runner. The per-harness token is
// required; a requeued (lease-expired) job is claimable again by design.
func (s *Storage) ClaimEvolutionJob(id, token, runner string) (*EvolutionJob, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	job, err := s.GetEvolutionJob(id)
	if err != nil {
		return nil, err
	}
	if !verifyJobToken(job, token) {
		return nil, &JobAuthError{JobID: job.ID, Action: "claim"}
	}
	if job.Status != EvolutionJobQueued {
		return nil, fmt.Errorf("cannot claim evolution job '%s': status is %s (only queued jobs are claimable)", job.ID, job.Status)
	}
	now := time.Now().UTC()
	if now.After(job.RunDeadlineAt) {
		job.Status = EvolutionJobTimeout
		job.Error = "run cap exceeded before claim"
		job.CompletedAt = now
		if err := s.writeEvolutionJob(job); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("cannot claim evolution job '%s': 90-minute run cap already exceeded — job marked timeout", job.ID)
	}
	s.claimEvolutionJobLocked(job, runner, now)
	if err := s.writeEvolutionJob(job); err != nil {
		return nil, err
	}
	return job, nil
}

// heartbeatEvolutionJobLocked applies a heartbeat/progress callback. It enforces
// the lease, the run cap, and the iteration cap, and it honors a cooperative
// pause: when PauseEvolutionJob set PauseRequested, the next heartbeat parks the
// job instead of advancing it. Returns true when the job parked. Callers hold
// writeMu.
func (s *Storage) heartbeatEvolutionJobLocked(job *EvolutionJob, iteration int, progress float64, note string, now time.Time) (parked bool, err error) {
	if job.Status != EvolutionJobClaimed && job.Status != EvolutionJobRunning {
		return false, fmt.Errorf("cannot heartbeat evolution job '%s': status is %s (only claimed/running jobs heartbeat)", job.ID, job.Status)
	}
	if !job.LeaseExpiresAt.IsZero() && now.After(job.LeaseExpiresAt) {
		return false, &JobLeaseExpiredError{JobID: job.ID}
	}
	if now.After(job.RunDeadlineAt) {
		job.Status = EvolutionJobTimeout
		job.Error = "90-minute run cap exceeded"
		if strings.TrimSpace(note) != "" {
			job.ProgressNote = strings.TrimSpace(note)
		}
		job.CompletedAt = now
		return false, &JobTimeoutError{JobID: job.ID}
	}
	if job.PauseRequested {
		job.Status = EvolutionJobPaused
		if strings.TrimSpace(note) != "" {
			job.Checkpoint = strings.TrimSpace(note)
		}
		if err := s.writeEvolutionJob(job); err != nil {
			return false, err
		}
		return true, nil
	}
	if iteration > 0 {
		if iteration > job.MaxIterations {
			job.Status = EvolutionJobFailed
			job.Error = fmt.Sprintf("iteration cap exceeded: step %d past max %d", iteration, job.MaxIterations)
			job.CompletedAt = now
			if err := s.writeEvolutionJob(job); err != nil {
				return false, err
			}
			return false, fmt.Errorf("evolution job '%s' failed: %s", job.ID, job.Error)
		}
		if iteration > job.Iteration {
			job.Iteration = iteration
		}
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	job.Progress = progress
	if strings.TrimSpace(note) != "" {
		job.ProgressNote = strings.TrimSpace(note)
	}
	job.Status = EvolutionJobRunning
	job.LastHeartbeat = now
	job.LeaseExpiresAt = now.Add(evolutionJobHeartbeatLease)
	return false, nil
}

// HeartbeatEvolutionJob renews a job's lease and records progress. Token-scoped
// to the job. When a pause was requested, the heartbeat parks the job at this
// step boundary and reports parked=true so the harness stops scheduling.
func (s *Storage) HeartbeatEvolutionJob(id, token string, iteration int, progress float64, note string) (*EvolutionJob, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	job, err := s.GetEvolutionJob(id)
	if err != nil {
		return nil, false, err
	}
	if !verifyJobToken(job, token) {
		return nil, false, &JobAuthError{JobID: job.ID, Action: "heartbeat"}
	}
	now := time.Now().UTC()
	parked, hbErr := s.heartbeatEvolutionJobLocked(job, iteration, progress, note, now)
	if hbErr != nil {
		// Lease expiry and timeout are terminal-or-requeue transitions the caller
		// must see persisted even though the heartbeat itself failed.
		if _, ok := hbErr.(*JobTimeoutError); ok {
			_ = s.writeEvolutionJob(job)
		}
		return nil, false, hbErr
	}
	if !parked {
		if err := s.writeEvolutionJob(job); err != nil {
			return nil, false, err
		}
	}
	return job, parked, nil
}

// UploadJobArtifact attaches a file to a claimed/running job. The content hash
// (SHA-256 hex, the story-03 convention) is checked BEFORE acceptance: a
// mismatch refuses the upload and stores nothing. Re-uploads with the same
// idempotency key return the original artifact without duplicating it — unless
// the hash differs, which is a conflict, not a retry.
func (s *Storage) UploadJobArtifact(id, token, name, content, contentHash, idempotencyKey string) (*JobArtifact, error) {
	name = strings.TrimSpace(name)
	contentHash = strings.ToLower(strings.TrimSpace(contentHash))
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if name == "" || strings.ContainsAny(name, `/\`) || filepath.Base(name) != name {
		return nil, fmt.Errorf("artifact name is required and must be a bare filename")
	}
	if contentHash == "" {
		return nil, fmt.Errorf("artifact content_hash is required (SHA-256 hex over the content bytes)")
	}
	if int64(len(content)) > evolutionJobMaxArtifactBytes {
		return nil, fmt.Errorf("artifact '%s' refused: %d bytes exceeds the %d-byte per-artifact cap", name, len(content), evolutionJobMaxArtifactBytes)
	}
	sum := sha256.Sum256([]byte(content))
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(contentHash)) != 1 {
		return nil, fmt.Errorf("artifact '%s' refused: content_hash does not match the content bytes — nothing stored", name)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	job, err := s.GetEvolutionJob(id)
	if err != nil {
		return nil, err
	}
	if !verifyJobToken(job, token) {
		return nil, &JobAuthError{JobID: job.ID, Action: "upload artifact"}
	}
	if job.Status != EvolutionJobClaimed && job.Status != EvolutionJobRunning {
		return nil, fmt.Errorf("cannot upload artifact to evolution job '%s': status is %s (only claimed/running jobs accept artifacts)", job.ID, job.Status)
	}
	if idempotencyKey != "" {
		if prev, ok := job.IdempotencyKeys[idempotencyKey]; ok {
			for i := range job.Artifacts {
				if job.Artifacts[i].Name == prev {
					if job.Artifacts[i].ContentHash != contentHash {
						return nil, fmt.Errorf("artifact idempotency conflict: key '%s' already uploaded '%s' with a different hash", idempotencyKey, prev)
					}
					existing := job.Artifacts[i]
					return &existing, nil
				}
			}
		}
	}
	if len(job.Artifacts) >= evolutionJobMaxArtifacts {
		return nil, fmt.Errorf("artifact '%s' refused: job already holds the maximum %d artifacts", name, evolutionJobMaxArtifacts)
	}
	dir, err := s.jobFilesDir(job.ID)
	if err != nil {
		return nil, err
	}
	artDir := filepath.Join(dir, "artifacts")
	if err := os.MkdirAll(artDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create job artifact directory: %w", err)
	}
	// A colliding artifact name from an earlier upload gets a numeric suffix; the
	// record always points at exactly what was written.
	target := filepath.Join(artDir, name)
	for i := 2; ; i++ {
		if _, err := os.Stat(target); os.IsNotExist(err) {
			break
		}
		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		target = filepath.Join(artDir, fmt.Sprintf("%s-%d%s", stem, i, ext))
	}
	// Artifacts are harness-produced data files, not wiki content: they are stored
	// byte-identical to what the hash check accepted. The job token itself is
	// never persisted anywhere — only its hash — so captured output cannot leak
	// it from the record, and the runner scrubs it from log files on dispatch.
	if err := os.WriteFile(target, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("failed to store job artifact: %w", err)
	}
	art := JobArtifact{
		Name:           filepath.Base(target),
		Path:           target,
		ContentHash:    contentHash,
		Size:           int64(len(content)),
		IdempotencyKey: idempotencyKey,
		UploadedAt:     time.Now().UTC(),
	}
	job.Artifacts = append(job.Artifacts, art)
	if idempotencyKey != "" {
		if job.IdempotencyKeys == nil {
			job.IdempotencyKeys = map[string]string{}
		}
		job.IdempotencyKeys[idempotencyKey] = art.Name
	}
	if err := s.writeEvolutionJob(job); err != nil {
		return nil, err
	}
	return &art, nil
}

// CompleteEvolutionJob marks a claimed/running job complete or failed. Terminal:
// the per-skill lock is released. Re-completing with the same outcome is a
// no-op success (idempotent); a different outcome on a terminal job is refused.
func (s *Storage) CompleteEvolutionJob(id, token, outcome, errMsg string) (*EvolutionJob, error) {
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	var status string
	switch outcome {
	case EvolutionJobOutcomeComplete:
		status = EvolutionJobComplete
	case EvolutionJobOutcomeFailed:
		status = EvolutionJobFailed
	default:
		return nil, fmt.Errorf("outcome must be %q or %q, got %q", EvolutionJobOutcomeComplete, EvolutionJobOutcomeFailed, outcome)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	job, err := s.GetEvolutionJob(id)
	if err != nil {
		return nil, err
	}
	if !verifyJobToken(job, token) {
		return nil, &JobAuthError{JobID: job.ID, Action: "complete"}
	}
	if evolutionJobTerminal(job.Status) {
		if job.Status == status {
			return job, nil
		}
		return nil, fmt.Errorf("cannot complete evolution job '%s': already terminal (%s)", job.ID, job.Status)
	}
	if job.Status != EvolutionJobClaimed && job.Status != EvolutionJobRunning {
		return nil, fmt.Errorf("cannot complete evolution job '%s': status is %s", job.ID, job.Status)
	}
	job.Status = status
	if strings.TrimSpace(errMsg) != "" {
		job.Error = strings.TrimSpace(errMsg)
	}
	job.PID = 0
	job.PauseRequested = false
	job.CompletedAt = time.Now().UTC()
	if err := s.writeEvolutionJob(job); err != nil {
		return nil, err
	}
	return job, nil
}

// CancelEvolutionJob cancels a live job: SIGTERM with a 10s grace, then SIGKILL,
// then the lock is released and the cancelled-run record is persisted (status,
// reason, last progress, completed_at). Cancelling a terminal job is refused —
// history is not rewritten.
func (s *Storage) CancelEvolutionJob(id, reason string) (*EvolutionJob, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	job, err := s.GetEvolutionJob(id)
	if err != nil {
		return nil, err
	}
	if evolutionJobTerminal(job.Status) {
		return nil, fmt.Errorf("cannot cancel evolution job '%s': already terminal (%s)", job.ID, job.Status)
	}
	if job.PID > 0 {
		killJobProcess(job.PID, evolutionJobKillGrace)
		job.PID = 0
	}
	job.Status = EvolutionJobCancelled
	job.CancelReason = strings.TrimSpace(reason)
	if job.CancelReason == "" {
		job.CancelReason = "cancelled by operator"
	}
	job.PauseRequested = false
	job.CompletedAt = time.Now().UTC()
	if err := s.writeEvolutionJob(job); err != nil {
		return nil, err
	}
	// Story 07: an abort is coherent with the loop stepper's records — the
	// in-flight iteration is marked interrupted and the loop state reads
	// terminal/cancelled. Best-effort: the cancel itself must not fail because
	// loop bookkeeping could not be written.
	if _, err := s.reconcileLoopTerminalLocked(job); err != nil {
		logRunnerf("loop reconcile after cancel of job %s failed: %v", job.ID, err)
	}
	return job, nil
}

// PauseEvolutionJob requests a cooperative pause at the next step boundary: it
// sets PauseRequested and records the checkpoint note. A job with no live
// harness (nothing left to schedule — no lease outstanding beyond what a dead
// claimer holds) parks immediately; a running harness parks on its next
// heartbeat, which is the step boundary. Pausing a terminal job is refused.
func (s *Storage) PauseEvolutionJob(id, checkpoint string) (*EvolutionJob, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	job, err := s.GetEvolutionJob(id)
	if err != nil {
		return nil, err
	}
	if evolutionJobTerminal(job.Status) {
		return nil, fmt.Errorf("cannot pause evolution job '%s': already terminal (%s)", job.ID, job.Status)
	}
	if job.Status == EvolutionJobPaused {
		return job, nil
	}
	if strings.TrimSpace(checkpoint) != "" {
		job.Checkpoint = strings.TrimSpace(checkpoint)
	}
	job.PauseRequested = true
	// No live process attached: nothing will heartbeat, so park now instead of
	// waiting on a boundary that will never arrive.
	if job.PID <= 0 {
		job.Status = EvolutionJobPaused
	}
	if err := s.writeEvolutionJob(job); err != nil {
		return nil, err
	}
	// Story 07: reflect the park in the loop stepper's state so the wizard reads
	// one coherent position (iteration/phase kept, status paused).
	if err := s.pauseLoopStateLocked(job.ID); err != nil {
		logRunnerf("loop pause-state update for job %s failed: %v", job.ID, err)
	}
	return job, nil
}

// ResumeEvolutionJob returns a paused job (or a pause-requested one) to queued
// with its checkpoint intact, so the next dispatch resumes from the recorded
// step instead of restarting. The lease is cleared: resume re-claims.
func (s *Storage) ResumeEvolutionJob(id string) (*EvolutionJob, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	job, err := s.GetEvolutionJob(id)
	if err != nil {
		return nil, err
	}
	if job.Status != EvolutionJobPaused && !job.PauseRequested {
		return nil, fmt.Errorf("cannot resume evolution job '%s': status is %s (only paused jobs resume)", job.ID, job.Status)
	}
	job.Status = EvolutionJobQueued
	job.PauseRequested = false
	job.Runner = ""
	job.PID = 0
	job.ClaimedAt = time.Time{}
	job.LastHeartbeat = time.Time{}
	job.LeaseExpiresAt = time.Time{}
	if err := s.writeEvolutionJob(job); err != nil {
		return nil, err
	}
	return job, nil
}

// RequeueExpiredJobs is the crash-recovery sweep: claimed/running jobs whose
// lease expired return to queued with claim cleared (re-claimable), claimed jobs
// silent past the claim timeout are requeued the same way, and anything past its
// run deadline is marked timeout. It returns the IDs it requeued and timed out.
// The runner calls it before every dispatch; operators call it directly.
func (s *Storage) RequeueExpiredJobs(now time.Time) (requeued, timedOut []string, err error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	jobs, err := s.ListEvolutionJobs()
	if err != nil {
		return nil, nil, err
	}
	for i := range jobs {
		job := jobs[i]
		if job.Status != EvolutionJobClaimed && job.Status != EvolutionJobRunning {
			continue
		}
		if !now.After(job.RunDeadlineAt) && (job.LeaseExpiresAt.IsZero() || !now.After(job.LeaseExpiresAt)) {
			// Claimed-but-silent past the claim timeout is a stuck claimer: requeue
			// even though the 60s lease arithmetic below has not caught it yet.
			if job.Status == EvolutionJobClaimed && !job.ClaimedAt.IsZero() && now.After(job.ClaimedAt.Add(evolutionJobClaimTimeout)) {
				// fall through to requeue
			} else {
				continue
			}
		}
		if now.After(job.RunDeadlineAt) {
			job.Status = EvolutionJobTimeout
			job.Error = "90-minute run cap exceeded"
			job.PID = 0
			job.PauseRequested = false
			job.CompletedAt = now
			if err := s.writeEvolutionJob(&job); err != nil {
				return requeued, timedOut, err
			}
			timedOut = append(timedOut, job.ID)
			continue
		}
		job.Status = EvolutionJobQueued
		job.Runner = ""
		job.PID = 0
		job.PauseRequested = false
		job.ClaimedAt = time.Time{}
		job.LastHeartbeat = time.Time{}
		job.LeaseExpiresAt = time.Time{}
		if err := s.writeEvolutionJob(&job); err != nil {
			return requeued, timedOut, err
		}
		requeued = append(requeued, job.ID)
	}
	if requeued == nil {
		requeued = []string{}
	}
	if timedOut == nil {
		timedOut = []string{}
	}
	return requeued, timedOut, nil
}

// killJobProcess ends a harness child: SIGTERM, a grace period, then SIGKILL.
// Best-effort by design — a PID from before a crash may be stale or recycled,
// so a process that is already gone (or unrecognizable) is success, not error.
// Logs go to Stderr only, per the repo convention; the token never appears here.
func killJobProcess(pid int, grace time.Duration) {
	if pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return // already gone (or not ours to signal)
	}
	_ = proc.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return
		}
	}
	_ = proc.Signal(syscall.SIGKILL)
}

// jobLifecycleTokenTools are the lifecycle tools a per-harness token unlocks.
// Creation mints the token and stays on the process path; everything after that
// is token-scoped to its job. The story 07 loop read tool joins the set: a
// token-holding harness may read its own run's iteration state.
var jobLifecycleTokenTools = map[string]bool{
	"claim_evolution_job":       true,
	"heartbeat_evolution_job":   true,
	"upload_job_artifact":       true,
	"upload_evolution_eval_set": true,
	"complete_evolution_job":    true,
	"get_evolution_iterations":  true,
}

// validJobTokenForArgs reports whether a tool call carries the addressed job's
// own token. This is the story-02 seam resolved: token -> authorization for this
// request, without touching the scope map or the gate. Creation (which mints
// the token) and every non-job tool return false here and take the normal
// process-role path. A forged or cross-job token is simply not valid — the gate
// then denies it, and the denial is audited like every other scope refusal.
func (srv *Server) validJobTokenForArgs(toolName string, args json.RawMessage) bool {
	if !jobLifecycleTokenTools[toolName] {
		return false
	}
	var probe struct {
		ID       string `json:"id"`
		JobToken string `json:"job_token"`
	}
	if err := json.Unmarshal(args, &probe); err != nil {
		return false
	}
	if strings.TrimSpace(probe.ID) == "" || strings.TrimSpace(probe.JobToken) == "" {
		return false
	}
	if srv == nil || srv.Storage == nil {
		return false
	}
	job, err := srv.Storage.GetEvolutionJob(probe.ID)
	if err != nil {
		return false
	}
	return verifyJobToken(job, probe.JobToken)
}

// auditJobDenial records a refused job-lifecycle call (bad or cross-job token)
// in the activity log, following the story-02 deny pattern: ToolResponse errors
// skip the success-only logging hook by design, so refusals audit themselves.
func (srv *Server) auditJobDenial(toolName, jobID, agent string, jobErr *JobAuthError) {
	if srv == nil || srv.EventBus == nil || jobErr == nil {
		return
	}
	if agent == "" {
		agent = DefaultAgentName
	}
	srv.EventBus.PublishActivity("mcp", DenyAction, toolName, strings.TrimSpace(jobID), "", agent)
}

// JobAuthError reports a refused lifecycle call: missing, unknown, or
// cross-job token. Like SkillScopeError it names what the harness needs to
// know — the job and the action — without leaking anything about the token.
type JobAuthError struct {
	JobID  string
	Action string
}

func (e *JobAuthError) Error() string {
	return fmt.Sprintf("access denied: invalid or missing job token for job '%s' (%s) — tokens are minted at creation, scoped to their job, and never retrievable again",
		e.JobID, e.Action)
}

// JobLeaseExpiredError reports a heartbeat on a lease the sweeper owns now: the
// harness must stop and re-claim instead of continuing a run nobody tracks.
type JobLeaseExpiredError struct {
	JobID string
}

func (e *JobLeaseExpiredError) Error() string {
	return fmt.Sprintf("evolution job '%s' lease expired: stop work and re-claim the job (a requeued job is claimable again)", e.JobID)
}

// JobTimeoutError reports a heartbeat past the run cap. The timeout transition
// is persisted before this returns.
type JobTimeoutError struct {
	JobID string
}

func (e *JobTimeoutError) Error() string {
	return fmt.Sprintf("evolution job '%s' exceeded the 90-minute run cap and was marked timeout", e.JobID)
}

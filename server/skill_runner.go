package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// This file holds the story 04 BYO (bring-your-own) headless CLI runner: how an
// evolution job actually executes an agentic coding CLI with no terminal and no
// PTY, under UI-only control.
//
// SHIM CONTRACT (the thin wrapper around each CLI):
//
// The server never speaks the CLI's native protocol. Each dispatch step runs:
//
//	<profile binary> <fixed args...>   with JobStdinPayload JSON on stdin
//
// and the child answers on stdout with JSON Lines. Every line is either a
// callback object or plain log text:
//
//	{"type":"progress","iteration":3,"progress":0.5,"note":"drafted variant B"}
//	{"type":"artifact","name":"variant-b.md","content":"...","content_hash":"<sha256 hex>","idempotency_key":"b-v1"}
//	{"type":"complete","outcome":"complete"} (or "failed" with an "error" detail)
//	{"type":"log","message":"..."}          (explicit log line; optional)
//	<anything else>                         (captured verbatim to stdout.log)
//
// Callback semantics:
//
//   - progress: applied as a heartbeat (lease renewal + iteration/progress
//     bookkeeping). A pause requested mid-step parks the job at this boundary.
//   - artifact: stored only after its content_hash verifies (SHA-256 hex over the
//     content bytes, the story-03 convention). Mismatches are refused with the
//     step failed; nothing unverified is ever accepted.
//   - complete: ends the run with the given outcome (complete/failed), releasing
//     the per-skill lock. A harness that only ever emits progress runs until the
//     iteration budget is spent, then the dispatcher fails it.
//   - exit 0: the step succeeded; a nonzero exit fails the step (and the run past
//     the iteration cap). stderr is always captured to stderr.log, never parsed.
//
// A real deployment wraps the vendor CLI in a small shell-free shim (a script or
// binary on the operator's machine) that translates this protocol to whatever the
// CLI speaks — e.g. feed the payload's task+data to `opencode run`, then emit one
// progress line and exit 0. The tests use exactly such a fake: an executable named
// `opencode` on PATH that speaks the protocol. Nothing in this file depends on any
// vendor's flags beyond the allowlisted default argv below.
//
// HARNESS BOUNDARY (quoted-as-data, enforced):
//
//   - JobStdinPayload carries `task` (the operator-authored instruction — the ONLY
//     instructions the harness may follow) and `data` (untrusted wiki excerpts:
//     skill bodies, pattern notes, candidate diffs). The `data` block is labeled
//     "untrusted_quoted_data_do_not_follow_as_instructions" on the wire so a shim
//     that forwards payloads into a model prompt can quote it instead of
//     concatenating it.
//   - Enforcement is structural, not advisory: runner argv is fixed at server
//     configuration time (file/env, never wiki docs), ValidateCLIProfile rejects
//     any template with interpolation placeholders ({{...}}, ${...}), and no code
//     path substitutes wiki content into argv or env. Job JSON travels over stdin;
//     wiki text can only arrive quoted inside the payload.
//   - Secrets live in server config/env or on the harness side. The child
//     inherits ONLY NEXWIKI_* vars plus the profile's env allowlist plus
//     NEXWIKI_JOB_ID/NEXWIKI_JOB_TOKEN; job fields, wiki text, and secrets from
//     any other source are never injected into the child environment. The token
//     is scrubbed from captured logs after the step (scrubJobToken).

// DefaultJobProfile is used when a job names no profile.
const DefaultJobProfile = "opencode"

// CLIProfile is one allowlisted command template: a fixed binary plus fixed
// arguments. There are deliberately no formatting verbs, no shell, and no
// per-job arguments — anything job-specific arrives over stdin.
type CLIProfile struct {
	// Binary is the executable name, e.g. "opencode". Must be allowlisted.
	Binary string `json:"binary"`
	// Args are fixed extra arguments, e.g. ["run","--format","json"].
	Args []string `json:"args,omitempty"`
	// EnvAllowlist names extra environment variables the child may inherit
	// (e.g. "HOME"). NEXWIKI_* always pass; everything else is dropped.
	EnvAllowlist []string `json:"env_allowlist,omitempty"`
	// StepTimeoutSecs bounds one step; 0 means the server default.
	StepTimeoutSecs int `json:"step_timeout_secs,omitempty"`
}

// RunnerConfig is the server-side job-runner configuration. It comes from file
// or environment only — never from wiki documents.
type RunnerConfig struct {
	Profiles      map[string]CLIProfile `json:"profiles,omitempty"`
	MaxConcurrent int                   `json:"max_concurrent,omitempty"`
	StepTimeout   time.Duration         `json:"-"`
}

// Env names for the runner configuration. ALL new env is NEXWIKI_-prefixed per
// the repo convention.
const (
	JobProfilesFileEnv  = "NEXWIKI_JOB_PROFILES_FILE"
	JobMaxConcurrentEnv = "NEXWIKI_JOB_MAX_CONCURRENT"
	JobStepTimeoutEnv   = "NEXWIKI_JOB_STEP_TIMEOUT_SECS"
)

// DefaultRunnerStepTimeout bounds one harness step when neither profile nor env
// says otherwise.
const DefaultRunnerStepTimeout = 10 * time.Minute

// allowedCLIBinaries is the BYO allowlist: `opencode` and `claude-code` first.
// Copilot stays a registered-but-unavailable extension hook — its entry documents
// the seam without permitting execution.
var allowedCLIBinaries = map[string]bool{
	"opencode":    true,
	"claude":      true,
	"claude-code": true,
}

// futureCLIHooks reserves extension points for harnesses not yet supported. A
// profile naming one is rejected with a "not yet supported" error instead of an
// allowlist error, so operators can tell "planned" from "forbidden".
var futureCLIHooks = map[string]bool{
	"copilot": true,
}

// shellBinaries are never valid profile binaries: a template that needs a shell
// is arbitrary-shell execution wearing a profile costume.
var shellBinaries = map[string]bool{
	"sh": true, "bash": true, "dash": true, "zsh": true, "fish": true,
	"cmd": true, "powershell": true, "pwsh": true,
}

// DefaultRunnerProfiles returns the built-in profiles: opencode and claude-code.
// Operators override or extend them through NEXWIKI_JOB_PROFILES_FILE.
func DefaultRunnerProfiles() map[string]CLIProfile {
	return map[string]CLIProfile{
		"opencode":    {Binary: "opencode", Args: []string{"run", "--format", "json"}},
		"claude-code": {Binary: "claude", Args: []string{"-p", "--output-format", "json"}},
	}
}

// ValidateCLIProfile rejects anything that is not a fixed, shell-free,
// interpolation-free command template. It runs at startup (server boot and every
// runner construction), so an arbitrary command template refuses the whole
// process rather than failing one job later.
func ValidateCLIProfile(name string, p CLIProfile) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("job profile has no name")
	}
	base := strings.ToLower(strings.TrimSpace(p.Binary))
	if base == "" {
		return fmt.Errorf("job profile '%s' refused: binary is required", name)
	}
	// Absolute/relative paths escape the allowlist's meaning ("my" opencode in
	// /tmp is not the operator's opencode): bare names only, resolved via PATH.
	if strings.ContainsAny(base, `/\`) {
		return fmt.Errorf("job profile '%s' refused: binary must be a bare name resolved via PATH, not a path: %q", name, p.Binary)
	}
	if shellBinaries[base] {
		return fmt.Errorf("job profile '%s' refused: shell execution is forbidden (binary %q) — profiles spawn the CLI directly, no shell", name, p.Binary)
	}
	if futureCLIHooks[base] {
		return fmt.Errorf("job profile '%s' refused: harness '%s' is a planned extension hook, not yet supported — no command is executed", name, p.Binary)
	}
	if !allowedCLIBinaries[base] {
		return fmt.Errorf("job profile '%s' refused: binary %q is not allowlisted (allowlisted: opencode, claude-code)", name, p.Binary)
	}
	for _, arg := range p.Args {
		if strings.Contains(arg, "{{") || strings.Contains(arg, "}}") || strings.Contains(arg, "${") {
			return fmt.Errorf("job profile '%s' refused: argument %q contains an interpolation placeholder — wiki content is never interpolated into shell, job JSON travels over stdin", name, arg)
		}
		if strings.Contains(arg, ";") || strings.Contains(arg, "\n") || strings.Contains(arg, "$(") || strings.Contains(arg, "`") {
			return fmt.Errorf("job profile '%s' refused: argument %q contains shell metacharacters — argv is fixed strings only", name, arg)
		}
	}
	if p.StepTimeoutSecs < 0 {
		return fmt.Errorf("job profile '%s' refused: negative step timeout", name)
	}
	if timeout := time.Duration(p.StepTimeoutSecs) * time.Second; timeout > evolutionJobRunCap {
		return fmt.Errorf("job profile '%s' refused: step timeout %s exceeds the 90-minute run cap", name, timeout)
	}
	return nil
}

// LoadRunnerConfigFromEnv builds the runner configuration from server config
// only: built-in defaults, optionally extended by a JSON profiles file, with
// NEXWIKI_JOB_MAX_CONCURRENT / NEXWIKI_JOB_STEP_TIMEOUT_SECS overrides. Every
// profile is validated before return — an invalid template is a startup error.
func LoadRunnerConfigFromEnv() (*RunnerConfig, error) {
	cfg := &RunnerConfig{
		Profiles:      DefaultRunnerProfiles(),
		MaxConcurrent: 1,
		StepTimeout:   DefaultRunnerStepTimeout,
	}
	if path := strings.TrimSpace(os.Getenv(JobProfilesFileEnv)); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("invalid job-runner configuration: cannot read profiles file %q: %w", path, err)
		}
		var file struct {
			Profiles map[string]CLIProfile `json:"profiles"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			return nil, fmt.Errorf("invalid job-runner configuration: profiles file %q is not valid JSON: %w", path, err)
		}
		for name, p := range file.Profiles {
			cfg.Profiles[strings.TrimSpace(name)] = p
		}
	}
	if raw := strings.TrimSpace(os.Getenv(JobMaxConcurrentEnv)); raw != "" {
		var n int
		if _, err := fmt.Sscanf(raw, "%d", &n); err != nil || n < 1 {
			return nil, fmt.Errorf("invalid job-runner configuration: %s must be a positive integer, got %q", JobMaxConcurrentEnv, raw)
		}
		if n > 2 {
			return nil, fmt.Errorf("invalid job-runner configuration: %s=%d exceeds the maximum of 2 concurrent jobs", JobMaxConcurrentEnv, n)
		}
		cfg.MaxConcurrent = n
	}
	if raw := strings.TrimSpace(os.Getenv(JobStepTimeoutEnv)); raw != "" {
		var secs int
		if _, err := fmt.Sscanf(raw, "%d", &secs); err != nil || secs <= 0 {
			return nil, fmt.Errorf("invalid job-runner configuration: %s must be a positive number of seconds, got %q", JobStepTimeoutEnv, raw)
		}
		if timeout := time.Duration(secs) * time.Second; timeout > evolutionJobRunCap {
			return nil, fmt.Errorf("invalid job-runner configuration: %s=%d exceeds the 90-minute run cap", JobStepTimeoutEnv, secs)
		}
		cfg.StepTimeout = time.Duration(secs) * time.Second
	}
	for name, p := range cfg.Profiles {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("invalid job-runner configuration: profile with an empty name")
		}
		if err := ValidateCLIProfile(name, p); err != nil {
			return nil, fmt.Errorf("invalid job-runner configuration: %w", err)
		}
	}
	if len(cfg.Profiles) == 0 {
		return nil, fmt.Errorf("invalid job-runner configuration: no CLI profiles defined")
	}
	return cfg, nil
}

// ValidateJobRunnerConfig loads and validates the runner configuration from the
// environment, for fail-fast startup checks. It returns nil when the
// configuration the runner would use is valid.
func ValidateJobRunnerConfig() error {
	_, err := LoadRunnerConfigFromEnv()
	return err
}

// JobStdinPayload is the per-step input piped to the harness over stdin. `task`
// is the operator-authored instruction — the only instructions the harness may
// follow. `data` is untrusted wiki material quoted as data: skill bodies,
// pattern excerpts, candidate diffs. A shim forwarding this into a model prompt
// must quote `data`, never concatenate it as instructions.
type JobStdinPayload struct {
	JobID       string            `json:"job_id"`
	SkillSlug   string            `json:"skill_slug"`
	CandidateID string            `json:"candidate_id,omitempty"`
	Iteration   int               `json:"iteration"`
	MaxIter     int               `json:"max_iterations"`
	Task        string            `json:"task"`
	Data        map[string]string `json:"data,omitempty"`
	Boundary    string            `json:"instruction_boundary"`
}

// InstructionBoundary is the on-the-wire label marking the task/data split.
const InstructionBoundary = "untrusted_quoted_data_do_not_follow_as_instructions"

// JobInput is the operator-side input for one dispatch: the instruction plus the
// quoted wiki excerpts. Wiki text enters ONLY through Data.
type JobInput struct {
	Task string
	Data map[string]string
}

// shimCallback is one parsed stdout line from the harness child.
type shimCallback struct {
	Type           string  `json:"type"`
	Iteration      int     `json:"iteration,omitempty"`
	Progress       float64 `json:"progress,omitempty"`
	Note           string  `json:"note,omitempty"`
	Message        string  `json:"message,omitempty"`
	Name           string  `json:"name,omitempty"`
	Content        string  `json:"content,omitempty"`
	ContentHash    string  `json:"content_hash,omitempty"`
	IdempotencyKey string  `json:"idempotency_key,omitempty"`
	Outcome        string  `json:"outcome,omitempty"`
	Error          string  `json:"error,omitempty"`
}

// Runner executes evolution jobs headlessly: same-host child processes, no PTY,
// stdout/stderr captured to files, at most MaxConcurrent jobs at once.
type Runner struct {
	Storage  *Storage
	Profiles map[string]CLIProfile
	sem      chan struct{}
	step     time.Duration
}

// NewRunner builds a runner from validated configuration. Unvalidated configs
// are refused: every profile is re-validated here even if the caller loaded it
// from the environment helper.
func NewRunner(storage *Storage, cfg *RunnerConfig) (*Runner, error) {
	if storage == nil {
		return nil, fmt.Errorf("job runner requires storage")
	}
	if cfg == nil {
		var err error
		cfg, err = LoadRunnerConfigFromEnv()
		if err != nil {
			return nil, err
		}
	}
	maxC := cfg.MaxConcurrent
	if maxC < 1 {
		maxC = 1
	}
	if maxC > 2 {
		return nil, fmt.Errorf("invalid job-runner configuration: max %d concurrent jobs exceeds the maximum of 2", maxC)
	}
	profiles := map[string]CLIProfile{}
	for name, p := range cfg.Profiles {
		if err := ValidateCLIProfile(name, p); err != nil {
			return nil, err
		}
		profiles[name] = p
	}
	if len(profiles) == 0 {
		profiles = DefaultRunnerProfiles()
	}
	step := cfg.StepTimeout
	if step <= 0 {
		step = DefaultRunnerStepTimeout
	}
	return &Runner{Storage: storage, Profiles: profiles, sem: make(chan struct{}, maxC), step: step}, nil
}

// childEnv builds the harness environment: NEXWIKI_* from the server process,
// plus the profile allowlist, plus the job identity. Nothing else passes —
// notably no wiki content and no unrelated secrets.
func (r *Runner) childEnv(profile CLIProfile, jobID, token string) []string {
	allow := map[string]bool{}
	for _, name := range profile.EnvAllowlist {
		allow[strings.TrimSpace(name)] = true
	}
	var env []string
	for _, kv := range os.Environ() {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if strings.HasPrefix(key, "NEXWIKI_") || allow[key] {
			env = append(env, kv)
		}
	}
	env = append(env, "NEXWIKI_JOB_ID="+jobID, "NEXWIKI_JOB_TOKEN="+token)
	return env
}

// stepTimeoutFor resolves the per-step timeout: profile value wins, else runner default.
func (r *Runner) stepTimeoutFor(profile CLIProfile) time.Duration {
	if profile.StepTimeoutSecs > 0 {
		return time.Duration(profile.StepTimeoutSecs) * time.Second
	}
	if r.step > 0 {
		return r.step
	}
	return DefaultRunnerStepTimeout
}

// setJobPID records the live child PID on the job so CancelEvolutionJob can
// signal it. Same package, holds writeMu directly.
func (r *Runner) setJobPID(jobID string, pid int) error {
	r.Storage.writeMu.Lock()
	defer r.Storage.writeMu.Unlock()
	job, err := r.Storage.GetEvolutionJob(jobID)
	if err != nil {
		return err
	}
	job.PID = pid
	return r.Storage.writeEvolutionJob(job)
}

// Dispatch runs one job to a terminal state (or to paused) headlessly: requeue
// sweep first, then claim, then up to the remaining iteration budget of
// spawn-step / parse-callbacks cycles. It blocks; the UI drives it from a
// goroutine or a worker process, never from a terminal. Concurrency is bounded
// by the semaphore: at most MaxConcurrent dispatches run at once.
func (r *Runner) Dispatch(jobID, token string, input JobInput) error {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	default:
		return fmt.Errorf("cannot dispatch evolution job '%s': runner is at capacity (%d concurrent jobs)", jobID, cap(r.sem))
	}
	now := time.Now().UTC()
	if _, _, err := r.Storage.RequeueExpiredJobs(now); err != nil {
		return err
	}
	job, err := r.Storage.GetEvolutionJob(jobID)
	if err != nil {
		return err
	}
	if !verifyJobToken(job, token) {
		return &JobAuthError{JobID: jobID, Action: "dispatch"}
	}
	profile, ok := r.Profiles[job.Profile]
	if !ok {
		_, _ = r.Storage.CompleteEvolutionJob(jobID, token, EvolutionJobOutcomeFailed, fmt.Sprintf("unknown CLI profile '%s'", job.Profile))
		return fmt.Errorf("cannot dispatch evolution job '%s': unknown CLI profile '%s'", jobID, job.Profile)
	}
	if _, err := r.Storage.ClaimEvolutionJob(jobID, token, "runner"); err != nil {
		return err
	}
	// Every executed step consumes budget, even one that reports no progress:
	// a harness emitting nothing (or replaying one iteration) still costs a
	// spawn, so the dispatch is bounded by the job's iteration cap regardless.
	steps := 0
	for {
		job, err = r.Storage.GetEvolutionJob(jobID)
		if err != nil {
			return err
		}
		if evolutionJobTerminal(job.Status) || job.Status == EvolutionJobPaused {
			return nil
		}
		if job.PauseRequested {
			if _, err := r.Storage.PauseEvolutionJob(jobID, fmt.Sprintf("paused at step boundary before step %d", job.Iteration+1)); err != nil {
				return err
			}
			return nil
		}
		if time.Now().UTC().After(job.RunDeadlineAt) {
			_, _, _ = r.Storage.RequeueExpiredJobs(time.Now().UTC())
			return nil
		}
		if job.Iteration >= job.MaxIterations || steps >= job.MaxIterations {
			_, _ = r.Storage.CompleteEvolutionJob(jobID, token, EvolutionJobOutcomeFailed,
				fmt.Sprintf("iteration cap reached: %d of %d steps used", job.Iteration, job.MaxIterations))
			return nil
		}
		steps++
		stepDone, stepErr := r.runStep(job, token, profile, input)
		if stepErr != nil {
			_, _ = r.Storage.CompleteEvolutionJob(jobID, token, EvolutionJobOutcomeFailed, stepErr.Error())
			return stepErr
		}
		if stepDone {
			return nil
		}
	}
}

// runStep spawns one headless step and applies its callbacks. It returns
// done=true when the run reached a terminal state or paused during the step.
func (r *Runner) runStep(job *EvolutionJob, token string, profile CLIProfile, input JobInput) (bool, error) {
	next := job.Iteration + 1
	payload := JobStdinPayload{
		JobID: job.ID, SkillSlug: job.SkillSlug, CandidateID: job.CandidateID,
		Iteration: next, MaxIter: job.MaxIterations,
		Task:     input.Task,
		Data:     input.Data,
		Boundary: InstructionBoundary,
	}
	stdin, err := json.Marshal(payload)
	if err != nil {
		return true, fmt.Errorf("failed to encode job payload: %w", err)
	}
	dir, err := r.Storage.jobFilesDir(job.ID)
	if err != nil {
		return true, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return true, fmt.Errorf("failed to create job working directory: %w", err)
	}
	stdoutPath := filepath.Join(dir, fmt.Sprintf("step-%d.stdout.log", next))
	stderrPath := filepath.Join(dir, fmt.Sprintf("step-%d.stderr.log", next))

	timeout := r.stepTimeoutFor(profile)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, profile.Binary, profile.Args...)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = r.childEnv(profile, job.ID, token)
	// No PTY, no shell: direct exec with pipes. Stdout is both parsed for shim
	// callbacks and captured to a file; stderr is captured, never parsed.
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return true, fmt.Errorf("failed to capture job stdout: %w", err)
	}
	defer func() { _ = stdoutFile.Close() }()
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return true, fmt.Errorf("failed to capture job stderr: %w", err)
	}
	defer func() { _ = stderrFile.Close() }()
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	if err := cmd.Start(); err != nil {
		return true, fmt.Errorf("failed to spawn CLI profile '%s': %w", profile.Binary, err)
	}
	_ = r.setJobPID(job.ID, cmd.Process.Pid)
	runErr := cmd.Wait()
	_ = r.setJobPID(job.ID, 0)
	// The token traveled to the child over env; scrub it from everything
	// captured before any callback is applied or any record persisted.
	scrubJobToken(stdoutPath, token)
	scrubJobToken(stderrPath, token)

	if ctx.Err() == context.DeadlineExceeded {
		return true, fmt.Errorf("step %d timed out after %s (per-step timeout)", next, timeout)
	}
	callbacks, logs, err := parseShimOutput(stdoutPath)
	if err != nil {
		return true, fmt.Errorf("step %d produced unreadable output: %w", next, err)
	}
	_ = logs // captured to the step log file already; parsed callbacks drive state
	for _, cb := range callbacks {
		done, err := r.applyCallback(job.ID, token, next, cb)
		if err != nil {
			return true, fmt.Errorf("step %d callback refused: %w", next, err)
		}
		if done {
			return true, nil
		}
	}
	// No explicit progress callback still advances the lease: a live step is a
	// heartbeat. The iteration only advances on an explicit progress callback
	// naming it, so a chatty-but-idle harness cannot burn the budget.
	if _, _, err := r.Storage.HeartbeatEvolutionJob(job.ID, token, 0, job.Progress, ""); err != nil {
		if _, ok := err.(*JobLeaseExpiredError); ok {
			return true, nil // lease lost mid-step; the sweep owns the job now
		}
		if _, ok := err.(*JobTimeoutError); ok {
			return true, nil
		}
		return true, err
	}
	if runErr != nil {
		return true, fmt.Errorf("step %d CLI exited with an error: %v", next, runErr)
	}
	return false, nil
}

// applyCallback applies one shim callback through the token-scoped storage
// paths. It returns done=true when the run parked (pause at this boundary) and
// the dispatcher must stop scheduling steps.
func (r *Runner) applyCallback(jobID, token string, step int, cb shimCallback) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(cb.Type)) {
	case "progress":
		iter := cb.Iteration
		if iter <= 0 {
			iter = step
		}
		_, parked, err := r.Storage.HeartbeatEvolutionJob(jobID, token, iter, cb.Progress, cb.Note)
		if err != nil {
			return true, err
		}
		return parked, nil
	case "artifact":
		if strings.TrimSpace(cb.Name) == "" || strings.TrimSpace(cb.ContentHash) == "" {
			return true, fmt.Errorf("artifact callback needs a name and a content_hash")
		}
		if _, err := r.Storage.UploadJobArtifact(jobID, token, cb.Name, cb.Content, cb.ContentHash, cb.IdempotencyKey); err != nil {
			return true, err
		}
		return false, nil
	case "complete":
		outcome := strings.ToLower(strings.TrimSpace(cb.Outcome))
		if outcome != EvolutionJobOutcomeComplete && outcome != EvolutionJobOutcomeFailed {
			return true, fmt.Errorf("complete callback needs an outcome of %q or %q", EvolutionJobOutcomeComplete, EvolutionJobOutcomeFailed)
		}
		if _, err := r.Storage.CompleteEvolutionJob(jobID, token, outcome, cb.Error); err != nil {
			return true, err
		}
		return true, nil
	case "log", "":
		return false, nil
	default:
		return true, fmt.Errorf("unknown shim callback type %q (contract: progress, artifact, complete, log)", cb.Type)
	}
}

// parseShimOutput reads a captured step stdout file, splitting shim callbacks
// (one JSON object per line with a "type" field) from plain log lines.
func parseShimOutput(path string) (callbacks []shimCallback, logs []string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var cb shimCallback
		if json.Unmarshal(line, &cb) == nil && strings.TrimSpace(cb.Type) != "" {
			callbacks = append(callbacks, cb)
			continue
		}
		logs = append(logs, string(line))
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return callbacks, logs, nil
}

// scrubJobToken replaces every occurrence of the raw token in a captured log
// file with [redacted]. The token is harness credential material: it must never
// persist in logs even if the child echoed its own environment.
func scrubJobToken(path, token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(raw, []byte(token)) {
		return
	}
	cleaned := bytes.ReplaceAll(raw, []byte(token), []byte("[redacted]"))
	_ = os.WriteFile(path, cleaned, 0644)
}

var runnerLogMu sync.Mutex

// logRunnerf emits runner diagnostics to Stderr only. Stdout stays clean for
// JSON-RPC on stdio transports; the token must never be passed here.
func logRunnerf(format string, args ...interface{}) {
	runnerLogMu.Lock()
	defer runnerLogMu.Unlock()
	_, _ = fmt.Fprintf(os.Stderr, "[job-runner] "+format+"\n", args...)
}

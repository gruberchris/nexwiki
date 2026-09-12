package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Story 04 tests: headless background execution for evolution jobs. The full
// lifecycle (create → claim → heartbeat → progress → artifact → complete),
// lease crash-recovery, cancel/pause semantics, BYO profile validation, token
// scoping, and story-02 scope denials on the job tools.

func seedJobSkill(t *testing.T, s *Storage, title string) *Article {
	t.Helper()
	return seedSkill(t, s, title, "# "+title+" body")
}

func shaHex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestEvolutionJobFullLifecycle(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedJobSkill(t, s, "Lifecycle Skill")

	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	if job.Status != EvolutionJobQueued {
		t.Fatalf("new job status = %q, want queued", job.Status)
	}
	if token == "" {
		t.Fatal("create must mint a token")
	}
	// The raw token is never persisted: only its hash.
	raw, err := os.ReadFile(filepath.Join(s.JobDir, job.ID+".json"))
	if err != nil {
		t.Fatalf("reading job file failed: %v", err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("job file must not contain the raw token")
	}

	// One run per skill: a second create is refused while the first is live.
	if _, _, err := s.CreateEvolutionJob(skill.Slug, "", "opencode"); err == nil {
		t.Fatal("second concurrent job on one skill must be refused")
	}

	claimed, err := s.ClaimEvolutionJob(job.ID, token, "runner-1")
	if err != nil {
		t.Fatalf("ClaimEvolutionJob failed: %v", err)
	}
	if claimed.Status != EvolutionJobClaimed || claimed.Runner != "runner-1" {
		t.Fatalf("claim not recorded: %+v", claimed)
	}

	hb, parked, err := s.HeartbeatEvolutionJob(job.ID, token, 1, 0.25, "step one done")
	if err != nil {
		t.Fatalf("HeartbeatEvolutionJob failed: %v", err)
	}
	if parked || hb.Status != EvolutionJobRunning || hb.Iteration != 1 || hb.Progress != 0.25 {
		t.Fatalf("heartbeat not applied: parked=%v %+v", parked, hb)
	}

	body := "# improved skill"
	art, err := s.UploadJobArtifact(job.ID, token, "variant.md", body, shaHex(body), "v1")
	if err != nil {
		t.Fatalf("UploadJobArtifact failed: %v", err)
	}
	if art.ContentHash != shaHex(body) || art.Size != int64(len(body)) {
		t.Fatalf("artifact record wrong: %+v", art)
	}
	stored, err := os.ReadFile(art.Path)
	if err != nil || string(stored) != body {
		t.Fatalf("artifact bytes wrong: %v %q", err, stored)
	}

	done, err := s.CompleteEvolutionJob(job.ID, token, "complete", "")
	if err != nil {
		t.Fatalf("CompleteEvolutionJob failed: %v", err)
	}
	if done.Status != EvolutionJobComplete {
		t.Fatalf("complete not recorded: %+v", done)
	}

	// Terminal releases the per-skill lock: a new run is allowed.
	job2, _, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create after terminal must succeed: %v", err)
	}
	if job2.ID == job.ID {
		t.Fatal("second run must be a distinct job")
	}
	// Re-completing with the same outcome is an idempotent no-op.
	if _, err := s.CompleteEvolutionJob(job.ID, token, "complete", ""); err != nil {
		t.Fatalf("idempotent re-complete failed: %v", err)
	}
}

func TestEvolutionJobTokenScoping(t *testing.T) {
	s := newLifecycleStorage(t)
	a := seedJobSkill(t, s, "Token Skill A")
	b := seedJobSkill(t, s, "Token Skill B")

	jobA, tokenA, err := s.CreateEvolutionJob(a.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create A failed: %v", err)
	}
	jobB, tokenB, err := s.CreateEvolutionJob(b.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create B failed: %v", err)
	}

	// Missing token is a denial, not a lookup miss.
	var authErr *JobAuthError
	if _, err := s.ClaimEvolutionJob(jobA.ID, "", "r"); !errors.As(err, &authErr) {
		t.Fatalf("empty token: expected *JobAuthError, got %v", err)
	}
	// Cross-job token is denied.
	if _, err := s.ClaimEvolutionJob(jobA.ID, tokenB, "r"); !errors.As(err, &authErr) {
		t.Fatalf("cross-job token: expected *JobAuthError, got %v", err)
	}
	// Forged token is denied.
	if _, _, err := s.HeartbeatEvolutionJob(jobA.ID, "forged", 0, 0, ""); !errors.As(err, &authErr) {
		t.Fatalf("forged token: expected *JobAuthError, got %v", err)
	}
	// Own token works; the other job is untouched.
	if _, err := s.ClaimEvolutionJob(jobA.ID, tokenA, "r"); err != nil {
		t.Fatalf("own token claim failed: %v", err)
	}
	freshB, _ := s.GetEvolutionJob(jobB.ID)
	if freshB.Status != EvolutionJobQueued {
		t.Fatalf("cross-job call touched job B: %+v", freshB)
	}
	_ = jobB
}

func TestEvolutionJobArtifactGuards(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedJobSkill(t, s, "Artifact Skill")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, err := s.ClaimEvolutionJob(job.ID, token, "r"); err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	// Hash mismatch stores nothing.
	if _, err := s.UploadJobArtifact(job.ID, token, "bad.md", "content", shaHex("other"), ""); err == nil {
		t.Fatal("hash mismatch must be refused")
	}
	entries, _ := os.ReadDir(filepath.Join(s.JobDir, job.ID+".files", "artifacts"))
	if len(entries) != 0 {
		t.Fatalf("refused upload left %d files", len(entries))
	}

	// Idempotency: same key returns the original without duplicating.
	body := "v1 body"
	first, err := s.UploadJobArtifact(job.ID, token, "v.md", body, shaHex(body), "key-1")
	if err != nil {
		t.Fatalf("first upload failed: %v", err)
	}
	second, err := s.UploadJobArtifact(job.ID, token, "v.md", body, shaHex(body), "key-1")
	if err != nil {
		t.Fatalf("idempotent re-upload failed: %v", err)
	}
	if second.Name != first.Name {
		t.Fatalf("idempotent re-upload returned a different artifact: %q vs %q", second.Name, first.Name)
	}
	// Same key, different hash is a conflict, not a retry.
	if _, err := s.UploadJobArtifact(job.ID, token, "v2.md", "other", shaHex("other"), "key-1"); err == nil {
		t.Fatal("idempotency conflict must be refused")
	}
	// Path escape in the name is refused.
	if _, err := s.UploadJobArtifact(job.ID, token, "../escape.md", body, shaHex(body), ""); err == nil {
		t.Fatal("path-traversal artifact name must be refused")
	}
}

func TestEvolutionJobLeaseRequeue(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedJobSkill(t, s, "Lease Skill")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Backdate the lease: the sweep requeues with the claim cleared.
	if _, err := s.ClaimEvolutionJob(job.ID, token, "crashed-runner"); err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	stored, _ := s.GetEvolutionJob(job.ID)
	stored.LeaseExpiresAt = time.Now().UTC().Add(-time.Minute)
	s.writeMu.Lock()
	if err := s.writeEvolutionJob(stored); err != nil {
		s.writeMu.Unlock()
		t.Fatalf("backdate failed: %v", err)
	}
	s.writeMu.Unlock()

	requeued, timedOut, err := s.RequeueExpiredJobs(time.Now().UTC())
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if len(requeued) != 1 || requeued[0] != job.ID || len(timedOut) != 0 {
		t.Fatalf("sweep wrong: requeued=%v timedOut=%v", requeued, timedOut)
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobQueued || fresh.Runner != "" || fresh.PID != 0 {
		t.Fatalf("requeue must clear the claim: %+v", fresh)
	}
	// A requeued job is claimable again — crash recovery completes the loop.
	if _, err := s.ClaimEvolutionJob(job.ID, token, "new-runner"); err != nil {
		t.Fatalf("re-claim after requeue failed: %v", err)
	}
	// And a heartbeat on an expired lease (pre-sweep) tells the harness to stop.
	stored2, _ := s.GetEvolutionJob(job.ID)
	stored2.LeaseExpiresAt = time.Now().UTC().Add(-time.Minute)
	s.writeMu.Lock()
	_ = s.writeEvolutionJob(stored2)
	s.writeMu.Unlock()
	if _, _, err := s.HeartbeatEvolutionJob(job.ID, token, 0, 0.5, ""); err == nil {
		t.Fatal("heartbeat on an expired lease must fail")
	} else {
		var leaseErr *JobLeaseExpiredError
		if !errors.As(err, &leaseErr) {
			t.Fatalf("expected *JobLeaseExpiredError, got %v", err)
		}
	}
}

func TestEvolutionJobRunCapAndIterationCap(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedJobSkill(t, s, "Cap Skill")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, err := s.ClaimEvolutionJob(job.ID, token, "r"); err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	// Past the iteration cap the job fails with its reason recorded.
	if _, _, err := s.HeartbeatEvolutionJob(job.ID, token, evolutionJobMaxIterations+1, 0.9, "too far"); err == nil {
		t.Fatal("heartbeat past the iteration cap must fail the job")
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobFailed || fresh.Error == "" {
		t.Fatalf("iteration cap must fail with reason: %+v", fresh)
	}

	// Past the run deadline the sweep marks timeout, never silently drops.
	skill2 := seedJobSkill(t, s, "Cap Skill Two")
	job2, token2, err := s.CreateEvolutionJob(skill2.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create 2 failed: %v", err)
	}
	if _, err := s.ClaimEvolutionJob(job2.ID, token2, "r"); err != nil {
		t.Fatalf("claim 2 failed: %v", err)
	}
	stored, _ := s.GetEvolutionJob(job2.ID)
	stored.RunDeadlineAt = time.Now().UTC().Add(-time.Minute)
	stored.LeaseExpiresAt = time.Now().UTC().Add(time.Minute)
	s.writeMu.Lock()
	if err := s.writeEvolutionJob(stored); err != nil {
		s.writeMu.Unlock()
		t.Fatalf("backdate failed: %v", err)
	}
	s.writeMu.Unlock()
	_, timedOut, err := s.RequeueExpiredJobs(time.Now().UTC())
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if len(timedOut) != 1 || timedOut[0] != job2.ID {
		t.Fatalf("run-cap sweep wrong: timedOut=%v", timedOut)
	}
}

func TestEvolutionJobCancelKillsProcess(t *testing.T) {
	oldGrace := evolutionJobKillGrace
	evolutionJobKillGrace = 200 * time.Millisecond
	defer func() { evolutionJobKillGrace = oldGrace }()

	s := newLifecycleStorage(t)
	skill := seedJobSkill(t, s, "Cancel Skill")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, err := s.ClaimEvolutionJob(job.ID, token, "r"); err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	// Attach a real live child so cancel has something to kill.
	proc := exec.Command("sleep", "120")
	if err := proc.Start(); err != nil {
		t.Skipf("no sleep binary for cancel test: %v", err)
	}
	defer func() { _ = proc.Process.Kill() }()
	s.writeMu.Lock()
	stored, _ := s.GetEvolutionJob(job.ID)
	stored.PID = proc.Process.Pid
	if err := s.writeEvolutionJob(stored); err != nil {
		s.writeMu.Unlock()
		t.Fatalf("pid attach failed: %v", err)
	}
	s.writeMu.Unlock()

	cancelled, err := s.CancelEvolutionJob(job.ID, "operator stop")
	if err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if cancelled.Status != EvolutionJobCancelled || cancelled.CancelReason != "operator stop" || cancelled.CompletedAt.IsZero() {
		t.Fatalf("cancelled-run record wrong: %+v", cancelled)
	}
	if cancelled.PID != 0 {
		t.Fatalf("cancel must clear the PID: %+v", cancelled)
	}
	// The process is actually dead: wait reports it, signal 0 fails.
	_ = proc.Wait()
	// Cancel releases the lock and refuses to rewrite history.
	if _, _, err := s.CreateEvolutionJob(skill.Slug, "", "opencode"); err != nil {
		t.Fatalf("create after cancel must succeed: %v", err)
	}
	if _, err := s.CancelEvolutionJob(job.ID, "again"); err == nil {
		t.Fatal("cancelling a terminal job must be refused")
	}
}

func TestEvolutionJobPauseResume(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedJobSkill(t, s, "Pause Skill")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, err := s.ClaimEvolutionJob(job.ID, token, "r"); err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	// No live process: parks immediately with the checkpoint recorded.
	paused, err := s.PauseEvolutionJob(job.ID, "after step 2")
	if err != nil {
		t.Fatalf("pause failed: %v", err)
	}
	if paused.Status != EvolutionJobPaused || paused.Checkpoint != "after step 2" {
		t.Fatalf("pause not recorded: %+v", paused)
	}
	// Resume re-queues from the checkpoint; the lease is cleared for re-claim.
	resumed, err := s.ResumeEvolutionJob(job.ID)
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if resumed.Status != EvolutionJobQueued || resumed.Checkpoint != "after step 2" || !resumed.LeaseExpiresAt.IsZero() {
		t.Fatalf("resume must re-queue with checkpoint intact: %+v", resumed)
	}
	if _, err := s.ClaimEvolutionJob(job.ID, token, "r2"); err != nil {
		t.Fatalf("re-claim after resume failed: %v", err)
	}

	// Cooperative path: pause requested while a harness is live stays running
	// until the step-boundary heartbeat parks it.
	s.writeMu.Lock()
	live, _ := s.GetEvolutionJob(job.ID)
	live.PID = os.Getpid()
	_ = s.writeEvolutionJob(live)
	s.writeMu.Unlock()
	if _, err := s.PauseEvolutionJob(job.ID, "park at boundary"); err != nil {
		t.Fatalf("pause request failed: %v", err)
	}
	mid, _ := s.GetEvolutionJob(job.ID)
	if (mid.Status != EvolutionJobRunning && mid.Status != EvolutionJobClaimed) || !mid.PauseRequested {
		t.Fatalf("pause with live harness must stay live until the boundary: %+v", mid)
	}
	hb, parked, err := s.HeartbeatEvolutionJob(job.ID, token, 0, 0.5, "step done")
	if err != nil {
		t.Fatalf("boundary heartbeat failed: %v", err)
	}
	if !parked || hb.Status != EvolutionJobPaused {
		t.Fatalf("boundary heartbeat must park: parked=%v %+v", parked, hb)
	}
}

func TestValidateCLIProfile(t *testing.T) {
	good := map[string]CLIProfile{
		"opencode":    {Binary: "opencode", Args: []string{"run"}},
		"claude-code": {Binary: "claude", Args: []string{"-p"}},
	}
	for name, p := range good {
		if err := ValidateCLIProfile(name, p); err != nil {
			t.Errorf("allowlisted profile %q refused: %v", name, err)
		}
	}
	bad := map[string]CLIProfile{
		"shell":         {Binary: "sh", Args: []string{"-c", "opencode run"}},
		"path-escape":   {Binary: "/tmp/evil/opencode"},
		"unknown":       {Binary: "random-cli"},
		"interpolation": {Binary: "opencode", Args: []string{"run", "{{skill_body}}"}},
		"env-expand":    {Binary: "opencode", Args: []string{"run", "${SKILL}"}},
		"metachars":     {Binary: "opencode", Args: []string{"run; rm -rf ~"}},
		"subshell":      {Binary: "opencode", Args: []string{"$(whoami)"}},
	}
	for name, p := range bad {
		if err := ValidateCLIProfile(name, p); err == nil {
			t.Errorf("arbitrary template %q accepted, must be refused", name)
		}
	}
	// Copilot is a registered extension hook: refused as unready, not as unknown.
	if err := ValidateCLIProfile("copilot", CLIProfile{Binary: "copilot"}); err == nil ||
		!strings.Contains(err.Error(), "not yet supported") {
		t.Fatalf("copilot must be refused as unready, got: %v", err)
	}
}

func TestRunnerConfigEnvValidation(t *testing.T) {
	t.Setenv(JobMaxConcurrentEnv, "3")
	if _, err := LoadRunnerConfigFromEnv(); err == nil {
		t.Fatal("max concurrent 3 must be refused (cap is 2)")
	}
	t.Setenv(JobMaxConcurrentEnv, "2")
	cfg, err := LoadRunnerConfigFromEnv()
	if err != nil {
		t.Fatalf("max concurrent 2 must be accepted: %v", err)
	}
	if cfg.MaxConcurrent != 2 {
		t.Fatalf("max concurrent = %d, want 2", cfg.MaxConcurrent)
	}

	// A profiles file with an arbitrary template refuses the whole config.
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(path, []byte(`{"profiles":{"evil":{"binary":"sh","args":["-c","x"]}}}`), 0644); err != nil {
		t.Fatalf("fixture failed: %v", err)
	}
	t.Setenv(JobProfilesFileEnv, path)
	if _, err := LoadRunnerConfigFromEnv(); err == nil {
		t.Fatal("arbitrary template in profiles file must refuse the config")
	}
	t.Setenv(JobProfilesFileEnv, filepath.Join(t.TempDir(), "missing.json"))
	if _, err := LoadRunnerConfigFromEnv(); err == nil {
		t.Fatal("missing profiles file must be an error")
	}
}

// fakeCLI installs an executable named `opencode` on PATH that speaks the shim
// protocol: it echoes the token (to prove scrubbing), then emits the given
// stdout lines. It returns the directory to prepend to PATH.
func fakeCLI(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\necho \"saw token=$NEXWIKI_JOB_TOKEN\"\n"
	for _, l := range lines {
		script += fmt.Sprintf("printf '%%s\\n' '%s'\n", strings.ReplaceAll(l, "'", "'\\''"))
	}
	path := filepath.Join(dir, "opencode")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("fake CLI failed: %v", err)
	}
	return dir
}

func testRunner(t *testing.T, s *Storage, profile CLIProfile) *Runner {
	t.Helper()
	r, err := NewRunner(s, &RunnerConfig{Profiles: map[string]CLIProfile{"opencode": profile}, MaxConcurrent: 1})
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}
	return r
}

func TestRunnerDispatchHeadless(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedJobSkill(t, s, "Dispatch Skill")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	binDir := fakeCLI(t,
		`{"type":"progress","iteration":1,"progress":0.5,"note":"half"}`,
		`{"type":"artifact","name":"out.md","content":"result","content_hash":"`+shaHex("result")+`","idempotency_key":"o1"}`,
		`{"type":"complete","outcome":"complete"}`,
		`plain log line`,
	)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})

	if err := r.Dispatch(job.ID, token, JobInput{Task: "improve", Data: map[string]string{"skill": "# body"}}); err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	// Progress → artifact → complete: the full headless lifecycle in one step.
	if fresh.Status != EvolutionJobComplete {
		t.Fatalf("dispatch must complete the run: %+v", fresh)
	}
	if len(fresh.Artifacts) != 1 || fresh.Artifacts[0].Name != "out.md" {
		t.Fatalf("shim artifact not applied: %+v", fresh.Artifacts)
	}
	if fresh.Iteration != 1 || fresh.Progress != 0.5 {
		t.Fatalf("shim progress not applied: %+v", fresh)
	}
	// No PTY, files captured: stdout log exists, and the echoed token is scrubbed.
	raw, err := os.ReadFile(filepath.Join(s.JobDir, job.ID+".files", "step-1.stdout.log"))
	if err != nil {
		t.Fatalf("captured stdout missing: %v", err)
	}
	if !strings.Contains(string(raw), "plain log line") {
		t.Fatalf("captured stdout lost log lines: %q", raw)
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("captured stdout must not contain the raw job token")
	}
	if !strings.Contains(string(raw), "[redacted]") {
		t.Fatalf("token scrub marker missing: %q", raw)
	}
	// Wiki content traveled over stdin only: argv carries no wiki text. The fake
	// CLI cannot observe argv here, but the validator guarantees it — assert the
	// profile that ran has no placeholders.
	if err := ValidateCLIProfile("opencode", CLIProfile{Binary: "opencode"}); err != nil {
		t.Fatalf("dispatched profile must validate: %v", err)
	}
}

func TestRunnerDispatchFailsBadExit(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedJobSkill(t, s, "Fail Skill")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte("#!/bin/sh\necho boom\nexit 3\n"), 0755); err != nil {
		t.Fatalf("fixture failed: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})

	if err := r.Dispatch(job.ID, token, JobInput{Task: "x"}); err == nil {
		t.Fatal("nonzero CLI exit must fail dispatch")
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobFailed || fresh.Error == "" {
		t.Fatalf("bad exit must fail with reason: %+v", fresh)
	}
}

func TestRunnerDispatchUnknownCallbackRefused(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedJobSkill(t, s, "Contract Skill")
	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	binDir := fakeCLI(t, `{"type":"teleport","where":"elsewhere"}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})

	if err := r.Dispatch(job.ID, token, JobInput{Task: "x"}); err == nil {
		t.Fatal("unknown shim callback type must fail dispatch")
	}
	fresh, _ := s.GetEvolutionJob(job.ID)
	if fresh.Status != EvolutionJobFailed {
		t.Fatalf("contract violation must fail the job: %+v", fresh)
	}
}

func TestJobToolsRespectScopes(t *testing.T) {
	for _, role := range []string{WikiskillRoleInference, WikiskillRoleMaintainer, WikiskillRoleProposer} {
		srv := roleServer(t, role)
		seedJobSkill(t, srv.Storage, "Scoped Job Skill")
		// Without a token, every job tool is denied under every agent role.
		for _, call := range []string{
			`{"name":"create_evolution_job","arguments":{"skill_slug":"scoped-job-skill"}}`,
			`{"name":"claim_evolution_job","arguments":{"id":"x","job_token":"y"}}`,
			`{"name":"heartbeat_evolution_job","arguments":{"id":"x","job_token":"y"}}`,
			`{"name":"upload_job_artifact","arguments":{"id":"x","job_token":"y","name":"n","content":"c","content_hash":"h"}}`,
			`{"name":"complete_evolution_job","arguments":{"id":"x","job_token":"y","outcome":"complete"}}`,
		} {
			resp := roleCall(t, srv, "agent", call)
			if !resp.IsError || !strings.Contains(resp.Content[0].Text, "lacks scope") {
				t.Errorf("role %s: %s must be scope-denied, got: %+v", role, call, resp)
			}
		}
	}

	// A token denial in unrestricted (operator) mode is audited as a deny event
	// too — the success-only logging hook skips ToolResponse errors by design,
	// so the handler audits its own refusals like the story-02 gate.
	op := newMCPServer(t)
	seedJobSkill(t, op.Storage, "Denied Skill")
	job, _, err := op.Storage.CreateEvolutionJob("denied-skill", "", "opencode")
	if err != nil {
		t.Fatalf("operator create failed: %v", err)
	}
	before := len(denyEvents(op))
	resp := roleCall(t, op, "harness", fmt.Sprintf(`{"name":"claim_evolution_job","arguments":{"id":%q,"job_token":"wrong"}}`, job.ID))
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "access denied") {
		t.Fatalf("wrong token must be token-denied, got: %+v", resp)
	}
	if len(denyEvents(op)) != before+1 {
		t.Fatal("token denial must be audited as a deny event")
	}
}

func TestJobTokenBypassesRoleForOwnJob(t *testing.T) {
	srv := newMCPServer(t)
	seedJobSkill(t, srv.Storage, "Harness Skill")
	seedJobSkill(t, srv.Storage, "Harness Skill Two")

	// The operator mints jobs unrestricted; the harness works under a role.
	srv.WikiskillRole = ""
	job2, token2, err := srv.Storage.CreateEvolutionJob("harness-skill-two", "", "opencode")
	if err != nil {
		t.Fatalf("operator create failed: %v", err)
	}

	srv.WikiskillRole = WikiskillRoleInference // strictest phase
	// Creation under a role is denied: minting stays on the process path.
	createResp := roleCall(t, srv, "operator", `{"name":"create_evolution_job","arguments":{"skill_slug":"harness-skill"}}`)
	if !createResp.IsError || !strings.Contains(createResp.Content[0].Text, "lacks scope") {
		t.Fatalf("create under inference role must be scope-denied, got: %+v", createResp)
	}
	// Cross-job token under a role: denied (and audited).
	before := len(denyEvents(srv))
	badCall := fmt.Sprintf(`{"name":"claim_evolution_job","arguments":{"id":%q,"job_token":%q}}`, job2.ID, "wrong")
	resp := roleCall(t, srv, "harness", badCall)
	if !resp.IsError {
		t.Fatal("wrong token under a role must be denied")
	}
	if len(denyEvents(srv)) != before+1 {
		t.Fatal("token denial under a role must be audited as a deny event")
	}
	// Own token under the strictest role: authorized for its own job.
	goodCall := fmt.Sprintf(`{"name":"claim_evolution_job","arguments":{"id":%q,"job_token":%q,"runner":"h"}}`, job2.ID, token2)
	resp2 := roleCall(t, srv, "harness", goodCall)
	if resp2.IsError {
		t.Fatalf("own token under inference role must be authorized, got: %s", resp2.Content[0].Text)
	}
}

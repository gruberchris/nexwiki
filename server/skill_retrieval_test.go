package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Story 11 (B3) tests: the injection toggle. Mode validation, the fail-closed
// top-K env, the BM25 ranking over name/description (deterministic order, top-K
// cut, the self pin, the zero-overlap degrade), the ranking trace, and the
// payload wiring through the runner for both modes.

func TestNormalizeInjectionMode(t *testing.T) {
	for _, in := range []string{"", "all", "ALL", " retrieve ", "Retrieve"} {
		if _, err := normalizeInjectionMode(in); err != nil {
			t.Errorf("mode %q must be accepted, got %v", in, err)
		}
	}
	if m, _ := normalizeInjectionMode("all"); m != InjectionModeAll {
		t.Errorf("all = %q", m)
	}
	if m, _ := normalizeInjectionMode(""); m != InjectionModeAll {
		t.Errorf("empty must default to all, got %q", m)
	}
	_, err := normalizeInjectionMode("hybrid")
	if err == nil || !strings.Contains(err.Error(), "injection_mode") {
		t.Errorf("an unknown mode must refuse naming the field, got %v", err)
	}
}

func TestInjectionModeDefaultsToAll(t *testing.T) {
	// A record that predates the field (empty mode) reads "all"; a hand-edited
	// invalid value falls back to the unchanged safe behavior.
	if got := injectionModeFor(&EvolutionJob{}); got != InjectionModeAll {
		t.Errorf("default = %q, want all", got)
	}
	if got := injectionModeFor(&EvolutionJob{InjectionMode: "garbage"}); got != InjectionModeAll {
		t.Errorf("hand-edited garbage = %q, want the safe default all", got)
	}
	if got := injectionModeFor(&EvolutionJob{InjectionMode: "retrieve"}); got != InjectionModeRetrieve {
		t.Errorf("retrieve = %q", got)
	}
}

func TestRetrievalTopKFailClosed(t *testing.T) {
	t.Setenv(JobRetrievalTopKEnv, "3")
	n, err := retrievalTopK()
	if err != nil || n != 3 {
		t.Fatalf("topK = %d/%v, want 3", n, err)
	}
	t.Setenv(JobRetrievalTopKEnv, "zero")
	if _, err := retrievalTopK(); err == nil || !strings.Contains(err.Error(), JobRetrievalTopKEnv) {
		t.Errorf("an unparseable value must fail closed naming the env, got %v", err)
	}
	t.Setenv(JobRetrievalTopKEnv, "0")
	if _, err := retrievalTopK(); err == nil {
		t.Error("a non-positive value must refuse")
	}
	t.Setenv(JobRetrievalTopKEnv, "")
	if n, err := retrievalTopK(); err != nil || n != DefaultRetrievalTopK {
		t.Fatalf("unset must default: %d/%v", n, err)
	}
}

func TestRunnerStartupValidatesRetrievalTopK(t *testing.T) {
	s := newLifecycleStorage(t)
	t.Setenv(JobRetrievalTopKEnv, "nope")
	if _, err := NewRunner(s, nil); err == nil || !strings.Contains(err.Error(), JobRetrievalTopKEnv) {
		t.Errorf("startup must refuse a bad top-K naming the env, got %v", err)
	}
}

func retrievalRegistry(t *testing.T, s *Storage) *Article {
	t.Helper()
	if _, err := s.SaveArticle("", "Stability Protocol", "# stability body", "keeps test rounds stable with heartbeat and lease checks", "", "", "", []string{"aiagent-skill"}, ContentTypeSkill); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if _, err := s.SaveArticle("", "Docker Cleanup", "# docker body", "prunes unused docker containers images and volumes safely", "", "", "", []string{"aiagent-skill"}, ContentTypeSkill); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if _, err := s.SaveArticle("", "Git Flow", "# git body", "conventional commit messages for git commits and pull requests", "", "", "", []string{"aiagent-skill"}, ContentTypeSkill); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if _, err := s.SaveArticle("", "Plain Wiki", "# not a skill", "", "", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	stability, err := s.GetArticle("stability-protocol")
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	return stability
}

func TestSelectSkillsForRetrievalRanksAndCuts(t *testing.T) {
	s := newLifecycleStorage(t)
	self := retrievalRegistry(t, s)

	topK, err := retrievalTopK() // default 5
	if err != nil {
		t.Fatalf("topK failed: %v", err)
	}
	block, err := s.selectSkillsForRetrieval(self.Slug, "prune the docker containers before the git commit", topK)
	if err != nil {
		t.Fatalf("selection failed: %v", err)
	}
	if block.Mode != InjectionModeRetrieve {
		t.Errorf("mode = %q", block.Mode)
	}
	// Deterministic rank order: the docker skill's description shares the most
	// task tokens, then git, then the stability skill (zero overlap of its
	// name/description with the task, so it appears only as the self pin).
	if len(block.Skills) < 3 {
		t.Fatalf("expected the two matches plus the self pin, got %+v", block.Skills)
	}
	if block.Skills[0].Slug != "docker-cleanup" {
		t.Errorf("first ranked = %q, want docker-cleanup", block.Skills[0].Slug)
	}
	if block.Skills[1].Slug != "git-flow" {
		t.Errorf("second ranked = %q, want git-flow", block.Skills[1].Slug)
	}
	last := block.Skills[len(block.Skills)-1]
	if !last.Self || last.Slug != self.Slug {
		t.Errorf("the skill under evolution must be pinned last when it has no keyword match: %+v", last)
	}
	// The wiki article that is not a skill must never enter the registry rank.
	for _, sk := range block.Skills {
		if sk.Slug == "plain-wiki" {
			t.Error("a non-skill document must not be injected")
		}
		if sk.Body == "" {
			t.Errorf("selected skill %s must carry its body quoted as data", sk.Slug)
		}
		if sk.Description == "" && !sk.Self {
			t.Errorf("selected skill %s must carry its description", sk.Slug)
		}
	}
	// The trace names the filter, the selection, and nothing else's body.
	if !strings.Contains(block.Trace, "retrieve mode") || !strings.Contains(block.Trace, "docker-cleanup") {
		t.Errorf("ranking trace incomplete: %q", block.Trace)
	}
	if strings.Contains(block.Trace, "# docker body") {
		t.Error("the trace must not quote skill bodies")
	}
	// The trace is also the iteration record's view (same content, structured).
	trace := blockTrace(block)
	if len(trace.Selected) != len(block.Skills) {
		t.Errorf("trace selection count = %d, want %d", len(trace.Selected), len(block.Skills))
	}
}

func blockTrace(block *SkillInjectionBlock) RetrievalTrace {
	trace := RetrievalTrace{Mode: block.Mode, Trace: block.Trace}
	for _, sk := range block.Skills {
		trace.Selected = append(trace.Selected, sk.Slug)
	}
	return trace
}

func TestSelectSkillsForRetrievalTopKCut(t *testing.T) {
	s := newLifecycleStorage(t)
	self := retrievalRegistry(t, s)

	// top-K 1 with two scoring skills: only the best match plus the self pin
	// ride the payload, and the trace names the excluded remainder.
	block, err := s.selectSkillsForRetrieval(self.Slug, "prune the docker containers and fix the git commit messages", 1)
	if err != nil {
		t.Fatalf("selection failed: %v", err)
	}
	if len(block.Skills) != 2 {
		t.Fatalf("top-1 + self = %d skills, want 2: %+v", len(block.Skills), block.Skills)
	}
	if !block.Skills[1].Self {
		t.Fatalf("the self pin must be appended after the retrieved subset: %+v", block.Skills)
	}
	if first := block.Skills[0].Slug; first != "docker-cleanup" && first != "git-flow" {
		t.Fatalf("best ranked match wrong: %+v", block.Skills)
	}
	if !strings.Contains(block.Trace, "excluded") {
		t.Errorf("the trace must name the excluded remainder: %q", block.Trace)
	}
}

func TestSelectSkillsForRetrievalSelfInTopK(t *testing.T) {
	s := newLifecycleStorage(t)
	self := retrievalRegistry(t, s)

	// A task about the skill under evolution itself: it must rank in and not
	// be duplicated by a pin.
	block, err := s.selectSkillsForRetrieval(self.Slug, "keep the test rounds stable with heartbeat and lease checks", 3)
	if err != nil {
		t.Fatalf("selection failed: %v", err)
	}
	selfCount := 0
	for _, sk := range block.Skills {
		if sk.Self {
			selfCount++
		}
	}
	if selfCount != 1 {
		t.Fatalf("self must appear exactly once: %+v", block.Skills)
	}
	if block.Skills[0].Slug != self.Slug {
		t.Errorf("self must rank first for its own vocabulary: %+v", block.Skills)
	}
}

func TestSelectSkillsForRetrievalDegradesOnNoMatch(t *testing.T) {
	s := newLifecycleStorage(t)
	self := retrievalRegistry(t, s)

	// An empty task matches nothing: the subset degrades to the self pin and
	// the trace says so — never an empty payload, never a silent fallback.
	block, err := s.selectSkillsForRetrieval(self.Slug, "", 5)
	if err != nil {
		t.Fatalf("selection failed: %v", err)
	}
	if len(block.Skills) != 1 || !block.Skills[0].Self {
		t.Fatalf("degraded selection = %+v, want the self pin alone", block.Skills)
	}
	if !strings.Contains(block.Trace, "degraded") {
		t.Errorf("the trace must name the degradation: %q", block.Trace)
	}
}

func TestRetrievalInvalidTopKFailsTheStep(t *testing.T) {
	s := newLifecycleStorage(t)
	self := retrievalRegistry(t, s)
	job, token, err := s.CreateEvolutionJobWithMode(self.Slug, "", "opencode", InjectionModeRetrieve)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	t.Setenv(JobRetrievalTopKEnv, "bad")
	if _, err := retrievalTopK(); err == nil {
		t.Fatal("fixture must be invalid")
	}
	// A retrieve-mode step with a broken env fails the step fail-closed rather
	// than silently widening the injection.
	binDir := fakeCLI(t, `{"type":"progress","progress":0.5}`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	res, err := r.LoopStep(job.ID, token, JobInput{Task: "anything"})
	if err == nil || !strings.Contains(err.Error(), JobRetrievalTopKEnv) {
		t.Fatalf("step must fail naming the env, got %+v err=%v", res, err)
	}
}

// runnerEchoShim is a fake CLI that echoes its stdin payload to stdout — the
// captured step log then holds the exact payload the runner piped over stdin —
// and emits one progress callback so the step completes cleanly.
func runnerEchoShim(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncat\nprintf '%s\\n' '{\"type\":\"progress\",\"progress\":0.5,\"note\":\"echoed\"}'\n"
	path := filepath.Join(dir, "opencode")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("echo shim failed: %v", err)
	}
	return dir
}

func readStdinPayload(t *testing.T, s *Storage, jobID, label string) JobStdinPayload {
	t.Helper()
	path := filepath.Join(s.JobDir, jobID+".files", "step-"+label+".stdout.log")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("captured stdout missing: %v", err)
	}
	// The echo shim writes the payload (no trailing newline) and then the
	// progress callback, so the two JSON objects can share a physical line. A
	// streaming decoder walks concatenated objects without caring.
	dec := json.NewDecoder(bytes.NewReader(raw))
	for {
		var payload JobStdinPayload
		if err := dec.Decode(&payload); err != nil {
			break
		}
		if payload.JobID != "" && payload.SkillSlug != "" {
			return payload
		}
	}
	t.Fatalf("no stdin payload echoed into %q: %s", path, raw)
	return JobStdinPayload{}
}

func TestRunnerPayloadAllModeUnchanged(t *testing.T) {
	s := newLifecycleStorage(t)
	self := retrievalRegistry(t, s)
	job, token, err := s.CreateEvolutionJob(self.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if got := job.InjectionMode; got != InjectionModeAll {
		t.Fatalf("default mode = %q, want all", got)
	}

	binDir := runnerEchoShim(t)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	if res := step(t, r, job.ID, token, JobInput{Task: "evolve the skill"}); !res.Advanced || res.Phase != LoopPhaseInference {
		t.Fatalf("inference step wrong: %+v", res)
	}

	payload := readStdinPayload(t, s, job.ID, "1-inference")
	if payload.SkillInjection != nil {
		t.Errorf("the default all mode must leave the payload unchanged, got %+v", payload.SkillInjection)
	}
	// No iteration record carries a retrieval trace in all mode.
	rec, _ := s.GetIterationRecord(job.ID, 1)
	if rec != nil && rec.Retrieval != nil {
		t.Errorf("all mode must record no retrieval trace: %+v", rec.Retrieval)
	}
}

func TestRunnerPayloadRetrieveModeInjectsSubset(t *testing.T) {
	s := newLifecycleStorage(t)
	self := retrievalRegistry(t, s)
	job, token, err := s.CreateEvolutionJobWithMode(self.Slug, "", "opencode", "retrieve")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	binDir := runnerEchoShim(t)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := testRunner(t, s, CLIProfile{Binary: "opencode"})
	if res := step(t, r, job.ID, token, JobInput{Task: "prune the docker containers before the git commit"}); !res.Advanced || res.Phase != LoopPhaseInference {
		t.Fatalf("inference step wrong: %+v", res)
	}

	payload := readStdinPayload(t, s, job.ID, "1-inference")
	if payload.SkillInjection == nil || payload.SkillInjection.Mode != InjectionModeRetrieve {
		t.Fatalf("retrieve payload missing the injection block: %+v", payload.SkillInjection)
	}
	// The subset: top matches first, the skill under evolution always last
	// here (it has no keyword overlap with the task).
	slugs := []string{}
	for _, sk := range payload.SkillInjection.Skills {
		slugs = append(slugs, sk.Slug)
	}
	if len(slugs) < 2 || slugs[0] != "docker-cleanup" {
		t.Fatalf("injected subset wrong: %v", slugs)
	}
	if slugs[len(slugs)-1] != self.Slug {
		t.Errorf("self must be injected: %v", slugs)
	}
	if payload.SkillInjection.Trace == "" {
		t.Error("the payload must carry the ranking trace for the harness")
	}
	// The iteration record carries the structured trace.
	rec, err := s.GetIterationRecord(job.ID, 1)
	if err != nil || rec == nil {
		t.Fatalf("iteration record missing: %v", err)
	}
	if rec.Retrieval == nil || rec.Retrieval.Mode != InjectionModeRetrieve {
		t.Fatalf("iteration record retrieval trace missing: %+v", rec.Retrieval)
	}
	if rec.Retrieval.Selected[len(rec.Retrieval.Selected)-1] != self.Slug {
		t.Errorf("recorded selection wrong: %v", rec.Retrieval.Selected)
	}
	if rec.Retrieval.Trace == "" {
		t.Error("the record must keep the human-readable trace")
	}
}

package server

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Story 03 tests: the harness-owned audit trail. Every gate decision emits a
// complete record; all three agent roles are refused on the audit-writing
// path; forged appends are impossible via agent paths.

// auditRecords is a helper returning the current trail, failing on read errors.
func auditRecords(t *testing.T, s *Storage) []SkillAuditRecord {
	t.Helper()
	recs, err := s.ListSkillAuditRecords()
	if err != nil {
		t.Fatalf("ListSkillAuditRecords failed: %v", err)
	}
	return recs
}

// assertCompleteRecord fails when any required audit field is missing or wrong.
func assertCompleteRecord(t *testing.T, rec SkillAuditRecord, c *SkillCandidate, score, before, after float64, outcome, decider string) {
	t.Helper()
	if rec.CandidateID != c.ID {
		t.Errorf("candidate id = %q, want %q", rec.CandidateID, c.ID)
	}
	if rec.SkillSlug != c.ParentSlug {
		t.Errorf("skill slug = %q, want %q", rec.SkillSlug, c.ParentSlug)
	}
	if rec.ParentVersion != c.ParentVersion {
		t.Errorf("parent version = %d, want %d", rec.ParentVersion, c.ParentVersion)
	}
	if rec.ContentHash != SkillCandidateContentHash(c) {
		t.Errorf("content hash = %q, want diff-or-body hash %q", rec.ContentHash, SkillCandidateContentHash(c))
	}
	if rec.ContentHash == "" || len(rec.ContentHash) != 64 {
		t.Errorf("content hash must be a 64-hex SHA-256, got %q", rec.ContentHash)
	}
	if rec.ValidationScore != score {
		t.Errorf("validation score = %.4f, want %.4f", rec.ValidationScore, score)
	}
	if rec.RBestBefore != before {
		t.Errorf("R_best before = %.4f, want %.4f", rec.RBestBefore, before)
	}
	if rec.RBestAfter != after {
		t.Errorf("R_best after = %.4f, want %.4f", rec.RBestAfter, after)
	}
	if rec.Outcome != outcome {
		t.Errorf("outcome = %q, want %q", rec.Outcome, outcome)
	}
	if rec.Decider != decider {
		t.Errorf("decider = %q, want %q", rec.Decider, decider)
	}
	if rec.ScorerVersion == "" {
		t.Error("scorer version must be stamped on every record")
	}
	if rec.Timestamp.IsZero() {
		t.Error("timestamp must be set on every record")
	}
	if err := rec.validate(); err != nil {
		t.Errorf("emitted record must validate, got: %v", err)
	}
}

func TestGateAcceptEmitsCompleteRecord(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Audited Skill", "# v1 body")
	c, err := s.CreateSkillCandidate(skill.Slug, "# v2 body", "--- a\n+++ b", []string{"pattern-note"}, "test-agent")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	wantScore := RBestScore(c)

	if _, err := s.PromoteSkillCandidate(c.ID); err != nil {
		t.Fatalf("PromoteSkillCandidate failed: %v", err)
	}
	recs := auditRecords(t, s)
	if len(recs) != 1 {
		t.Fatalf("expected exactly one audit record, got %d: %+v", len(recs), recs)
	}
	assertCompleteRecord(t, recs[0], c, wantScore, 0, wantScore, SkillAuditAccepted, SkillAuditDeciderGate)
}

func TestGateRejectEmitsCompleteRecord(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Gated Skill", "# v1 body")

	strong, err := s.CreateSkillCandidate(skill.Slug, "# strong body", "--- strong diff", []string{"p1", "p2", "p3"}, "op")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	if _, err := s.PromoteSkillCandidate(strong.ID); err != nil {
		t.Fatalf("promoting the strong candidate failed: %v", err)
	}
	best := RBestScore(strong)

	liveBefore, err := s.GetArticle(skill.Slug)
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	weak, err := s.CreateSkillCandidate(skill.Slug, "# weak", "", nil, "op")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	weakScore := RBestScore(weak)
	if weakScore >= best {
		t.Fatalf("test setup broken: weak score %.4f must sit below best %.4f", weakScore, best)
	}

	if _, err := s.PromoteSkillCandidate(weak.ID); err == nil {
		t.Fatal("promoting a below-best candidate must fail the gate")
	} else if !strings.Contains(err.Error(), "validation gate") {
		t.Errorf("gate refusal must say so, got: %v", err)
	}
	decided, err := s.GetSkillCandidate(weak.ID)
	if err != nil {
		t.Fatalf("GetSkillCandidate failed: %v", err)
	}
	if decided.Status != SkillCandidateRejected {
		t.Errorf("gate-rejected candidate must be marked rejected, got %q", decided.Status)
	}
	liveAfter, err := s.GetArticle(skill.Slug)
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if liveAfter.Content != liveBefore.Content || liveAfter.Version != liveBefore.Version {
		t.Error("a gate rejection must leave the live skill byte-identical")
	}

	recs := auditRecords(t, s)
	if len(recs) != 2 {
		t.Fatalf("expected accept + reject records, got %d: %+v", len(recs), recs)
	}
	assertCompleteRecord(t, recs[1], weak, weakScore, best, best, SkillAuditRejected, SkillAuditDeciderGate)
}

func TestExplicitRejectEmitsHumanRecord(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Human Gated Skill", "# v1 body")
	c, err := s.CreateSkillCandidate(skill.Slug, "# unwanted", "--- d", []string{"p1"}, "op")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	if _, err := s.RejectSkillCandidate(c.ID); err != nil {
		t.Fatalf("RejectSkillCandidate failed: %v", err)
	}
	recs := auditRecords(t, s)
	if len(recs) != 1 {
		t.Fatalf("expected exactly one audit record, got %d", len(recs))
	}
	assertCompleteRecord(t, recs[0], c, RBestScore(c), 0, 0, SkillAuditRejected, SkillAuditDeciderHuman)
}

func TestEarlyStopPerfectScoreAccepts(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Ceiling Skill", "# v1 body")
	perfectBody := strings.Repeat("# perfect line\n", 800) // >= 10000 chars: length bonus caps at 0.1

	first, err := s.CreateSkillCandidate(skill.Slug, perfectBody, "--- d", []string{"p1", "p2", "p3"}, "op")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	if got := RBestScore(first); got != 1.0 {
		t.Fatalf("test setup broken: want a perfect 1.0 score, got %.4f", got)
	}
	if _, err := s.PromoteSkillCandidate(first.ID); err != nil {
		t.Fatalf("promoting the perfect candidate failed: %v", err)
	}

	// R_best now sits at the ceiling. A strictly-greater compare would deadlock
	// here; the 1.0 early-stop hook must still accept.
	second, err := s.CreateSkillCandidate(skill.Slug, perfectBody+"# more\n", "--- d2", []string{"p1", "p2", "p3"}, "op")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	if got := RBestScore(second); got != 1.0 {
		t.Fatalf("test setup broken: want a perfect 1.0 score, got %.4f", got)
	}
	if _, err := s.PromoteSkillCandidate(second.ID); err != nil {
		t.Fatalf("early-stop accept at the ceiling failed: %v", err)
	}
	recs := auditRecords(t, s)
	if len(recs) != 2 {
		t.Fatalf("expected two accept records, got %d", len(recs))
	}
	for i, rec := range recs {
		if rec.Outcome != SkillAuditAccepted {
			t.Errorf("record %d: want accepted, got %+v", i, rec)
		}
		if rec.RBestAfter != 1.0 {
			t.Errorf("record %d: want R_best after 1.0, got %+v", i, rec)
		}
		if err := rec.validate(); err != nil {
			t.Errorf("record %d must validate: %v", i, err)
		}
	}
}

func TestAuditRecordValidationRefusesMissingFields(t *testing.T) {
	s := newLifecycleStorage(t)
	seedSkill(t, s, "Validation Skill", "# body")
	c, err := s.CreateSkillCandidate("validation-skill", "# v2", "--- d", nil, "op")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	complete := SkillAuditRecord{
		CandidateID: c.ID, SkillSlug: c.ParentSlug, ParentVersion: c.ParentVersion,
		ContentHash: SkillCandidateContentHash(c), ValidationScore: 0.5,
		RBestBefore: 0, RBestAfter: 0.5, Outcome: SkillAuditAccepted,
		Decider: SkillAuditDeciderGate, ScorerVersion: SkillAuditScorerVersion,
		Timestamp: time.Now().UTC(),
	}
	if err := complete.validate(); err != nil {
		t.Fatalf("complete record must validate: %v", err)
	}

	mutate := func(r SkillAuditRecord) []SkillAuditRecord {
		bad := []SkillAuditRecord{}
		empty := r
		empty.CandidateID = ""
		bad = append(bad, empty)
		empty = r
		empty.SkillSlug = ""
		bad = append(bad, empty)
		empty = r
		empty.ParentVersion = 0
		bad = append(bad, empty)
		empty = r
		empty.ContentHash = ""
		bad = append(bad, empty)
		empty = r
		empty.ValidationScore = -0.1
		bad = append(bad, empty)
		empty = r
		empty.ValidationScore = 1.1
		bad = append(bad, empty)
		empty = r
		empty.RBestBefore = -1
		bad = append(bad, empty)
		empty = r
		empty.RBestAfter = 2
		bad = append(bad, empty)
		empty = r
		empty.Outcome = "maybe"
		bad = append(bad, empty)
		empty = r
		empty.Decider = "llm"
		bad = append(bad, empty)
		empty = r
		empty.ScorerVersion = ""
		bad = append(bad, empty)
		empty = r
		empty.Timestamp = time.Time{}
		bad = append(bad, empty)
		return bad
	}
	for i, bad := range mutate(complete) {
		if err := s.appendSkillAuditRecord(bad); err == nil {
			t.Errorf("case %d: missing-field record must be refused: %+v", i, bad)
		}
	}
	if recs := auditRecords(t, s); len(recs) != 0 {
		t.Errorf("no missing-field record may reach the trail, got %+v", recs)
	}
}

// TestAgentRolesRefusedAuditWrites exercises the forgery refusal per role: the
// only MCP path that writes audit records is promote_skill_candidate, which
// needs audit.write — a scope no phase holds. Each role's attempt is denied
// naming that scope, and no record lands.
func TestAgentRolesRefusedAuditWrites(t *testing.T) {
	for _, role := range []string{WikiskillRoleInference, WikiskillRoleMaintainer, WikiskillRoleProposer} {
		srv := roleServer(t, role)
		resp := roleCall(t, srv, "phase-agent", `{"name":"promote_skill_candidate","arguments":{"id":"forged-id"}}`)
		assertScopeDenial(t, resp, role, ScopeAuditWrite)
		deniedFor(t, srv, "promote_skill_candidate")
		if recs := auditRecords(t, srv.Storage); len(recs) != 0 {
			t.Errorf("role %q: a denied promote must not write audit records, got %+v", role, recs)
		}
	}
}

// TestAuditWriteScopeHeldByNoPhase locks the story 02 invariant story 03
// relies on: audit.write (and skills.promote) belong to no evolution phase,
// so the promote path stays operator/harness-only.
func TestAuditWriteScopeHeldByNoPhase(t *testing.T) {
	for role, grants := range wikiskillRoleGrants {
		if grants[ScopeAuditWrite] {
			t.Errorf("role %q must not hold %q", role, ScopeAuditWrite)
		}
		if grants[ScopeSkillsPromote] {
			t.Errorf("role %q must not hold %q", role, ScopeSkillsPromote)
		}
	}
}

// TestNoDirectAuditWriteToolExists proves forged score appends impossible via
// agent paths: no registered MCP tool besides promote_skill_candidate maps to
// the audit.write scope, so there is no direct audit-write tool to escalate to.
func TestNoDirectAuditWriteToolExists(t *testing.T) {
	srv := newMCPServer(t)
	for _, tool := range mcpToolRegistry {
		name, _ := tool.Schema["name"].(string)
		if name == "" || name == "promote_skill_candidate" {
			continue
		}
		for _, scope := range srv.requiredWikiskillScopes(name, json.RawMessage(`{}`)) {
			if scope == ScopeAuditWrite {
				t.Errorf("tool %q maps to %q: direct audit writes must stay harness-only", name, scope)
			}
		}
	}
	if scopes := srv.requiredWikiskillScopes("promote_skill_candidate", json.RawMessage(`{"id":"x"}`)); len(scopes) == 0 {
		t.Error("promote_skill_candidate must require scopes (it is the gated audit-writing path)")
	} else {
		found := false
		for _, scope := range scopes {
			if scope == ScopeAuditWrite {
				found = true
			}
		}
		if !found {
			t.Errorf("promote_skill_candidate must require %q, got %v", ScopeAuditWrite, scopes)
		}
	}
}

func TestRBestScoreDeterministic(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Determinism Skill", "# body")
	c, err := s.CreateSkillCandidate(skill.Slug, "# v2 body", "--- d", []string{"p1"}, "op")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	first := RBestScore(c)
	loaded, err := s.GetSkillCandidate(c.ID)
	if err != nil {
		t.Fatalf("GetSkillCandidate failed: %v", err)
	}
	if second := RBestScore(loaded); second != first {
		t.Errorf("RBestScore must be deterministic across reloads: %.4f vs %.4f", first, second)
	}
	if first < 0 || first > 1 {
		t.Errorf("RBestScore must stay in [0,1], got %.4f", first)
	}
	if RBestScore(nil) != 0 {
		t.Errorf("RBestScore(nil) must be 0, got %.4f", RBestScore(nil))
	}
}

func TestPromoteAlreadyDecidedEmitsNoSecondRecord(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Repromote Skill", "# v1 body")
	c, err := s.CreateSkillCandidate(skill.Slug, "# v2 body", "--- d", []string{"p1"}, "op")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	if _, err := s.PromoteSkillCandidate(c.ID); err != nil {
		t.Fatalf("first PromoteSkillCandidate failed: %v", err)
	}
	first, err := s.GetSkillCandidate(c.ID)
	if err != nil {
		t.Fatalf("GetSkillCandidate failed: %v", err)
	}
	if first.Status != SkillCandidatePromoted {
		t.Fatalf("want status promoted after first promote, got %q", first.Status)
	}
	if recs := auditRecords(t, s); len(recs) != 1 {
		t.Fatalf("expected one record after first promote, got %d", len(recs))
	}

	if _, err := s.PromoteSkillCandidate(c.ID); err == nil {
		t.Fatal("second promote of a decided candidate must fail")
	} else if !strings.Contains(err.Error(), "already decided") {
		t.Fatalf("re-promote must report already decided, got: %v", err)
	}
	decided, err := s.GetSkillCandidate(c.ID)
	if err != nil {
		t.Fatalf("GetSkillCandidate failed: %v", err)
	}
	if decided.Status != SkillCandidatePromoted {
		t.Errorf("re-promote must not clobber status, got %q", decided.Status)
	}
	if decided.ResultVersion != first.ResultVersion {
		t.Errorf("re-promote must not clobber result version: %d vs %d", decided.ResultVersion, first.ResultVersion)
	}
	if recs := auditRecords(t, s); len(recs) != 1 {
		t.Errorf("re-promote must not append a second record, got %d: %+v", len(recs), recs)
	}

	// A rejected candidate re-promoted must hit the same guard.
	other, err := s.CreateSkillCandidate(skill.Slug, "# v3 body", "--- d3", []string{"p1"}, "op")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	if _, err := s.RejectSkillCandidate(other.ID); err != nil {
		t.Fatalf("RejectSkillCandidate failed: %v", err)
	}
	nBefore := len(auditRecords(t, s))
	if _, err := s.PromoteSkillCandidate(other.ID); err == nil {
		t.Fatal("promoting a rejected candidate must fail")
	} else if !strings.Contains(err.Error(), "already decided") {
		t.Fatalf("promote-after-reject must report already decided, got: %v", err)
	}
	if decided, err := s.GetSkillCandidate(other.ID); err != nil {
		t.Fatalf("GetSkillCandidate failed: %v", err)
	} else if decided.Status != SkillCandidateRejected {
		t.Errorf("promote-after-reject must not clobber status, got %q", decided.Status)
	}
	if recs := auditRecords(t, s); len(recs) != nBefore {
		t.Errorf("promote-after-reject must not append a record, got %d want %d", len(recs), nBefore)
	}
}

func TestDiffOnlyPromoteRefusalIsUnaudited(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Diffonly Skill", "# v1 body")
	c, err := s.CreateSkillCandidate(skill.Slug, "", "--- diff only", nil, "op")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	if _, err := s.PromoteSkillCandidate(c.ID); err == nil {
		t.Fatal("diff-only promote must fail")
	} else if !strings.Contains(err.Error(), "diff-only") {
		t.Fatalf("diff-only refusal must say so, got: %v", err)
	}
	if recs := auditRecords(t, s); len(recs) != 0 {
		t.Errorf("structural diff-only refusal is unaudited (gate decisions only), got %d records: %+v", len(recs), recs)
	}
	if decided, err := s.GetSkillCandidate(c.ID); err != nil {
		t.Fatalf("GetSkillCandidate failed: %v", err)
	} else if decided.Status != SkillCandidatePending {
		t.Errorf("diff-only refusal must leave the candidate pending, got %q", decided.Status)
	}
}

func TestCurrentRBestPropagatesListErrors(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Rbest IO Skill", "# v1 body")
	c, err := s.CreateSkillCandidate(skill.Slug, "# v2 body", "--- d", []string{"p1"}, "op")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	auditPath := SkillAuditPath(s.DataDir)
	_ = os.Remove(auditPath)
	if err := os.Mkdir(auditPath, 0755); err != nil {
		t.Fatalf("setup: mkdir at audit path failed: %v", err)
	}
	defer func() { _ = os.Remove(auditPath) }()

	if _, err := s.currentRBest(skill.Slug); err == nil {
		t.Error("currentRBest must propagate trail read errors instead of swallowing to 0")
	}
	if _, err := s.PromoteSkillCandidate(c.ID); err == nil {
		t.Fatal("promote must abort on audit-trail I/O failure")
	} else if !strings.Contains(err.Error(), "audit trail") {
		t.Errorf("promote I/O abort must name the audit trail, got: %v", err)
	}
	if _, err := s.RejectSkillCandidate(c.ID); err == nil {
		t.Fatal("reject must abort on audit-trail I/O failure")
	} else if !strings.Contains(err.Error(), "audit trail") {
		t.Errorf("reject I/O abort must name the audit trail, got: %v", err)
	}
	_ = os.Remove(auditPath)
	if decided, err := s.GetSkillCandidate(c.ID); err != nil {
		t.Fatalf("GetSkillCandidate failed: %v", err)
	} else if decided.Status != SkillCandidatePending {
		t.Errorf("I/O-aborted gate must leave the candidate pending, got %q", decided.Status)
	}
	if _, err := s.ListSkillAuditRecords(); err != nil {
		t.Fatalf("trail must be readable after cleanup: %v", err)
	}
}

func TestSkillCandidateContentHashNilGuard(t *testing.T) {
	if got := SkillCandidateContentHash(nil); got != "" {
		t.Errorf("SkillCandidateContentHash(nil) must return %q, got %q", "", got)
	}
}

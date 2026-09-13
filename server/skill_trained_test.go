package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Story 06 tests: the harness-managed trained marker, the trained-state
// picker, and the rollback/unlock paths. A gate accept stamps the marker
// atomically with the promoted body; agent tag edits can neither forge nor
// strip it; staleness covers post-train edits and eval-set changes; rollback
// restores the pre-training body while preserving marker history and audit.

func promoteCandidate(t *testing.T, s *Storage, skill *Article, body string) (*Article, *SkillCandidate) {
	t.Helper()
	created, err := s.CreateSkillCandidate(skill.Slug, body, "", []string{"pattern-note"}, "test-agent")
	if err != nil {
		t.Fatalf("CreateSkillCandidate failed: %v", err)
	}
	promoted, err := s.PromoteSkillCandidate(created.ID)
	if err != nil {
		t.Fatalf("PromoteSkillCandidate failed: %v", err)
	}
	// PromoteSkillCandidate mutates its own copy of the record, so the local
	// candidate from CreateSkillCandidate is stale (BestScore empty). Re-fetch
	// the decided record before any comparison against it.
	c, err := s.GetSkillCandidate(created.ID)
	if err != nil {
		t.Fatalf("GetSkillCandidate failed: %v", err)
	}
	return promoted, c
}

// srvForStorage wraps an existing Storage in a bare Server so MCP tool paths can
// be exercised against storage another test seeded.
func srvForStorage(t *testing.T, s *Storage) *Server {
	t.Helper()
	return NewServer(s, "Test Wiki", "light", false, NewEventBus(), "1.0.0", "")
}

func assertTrainedMarker(t *testing.T, art *Article, c *SkillCandidate, parentVersion int) {
	t.Helper()
	if !hasTag(art.Tags, TrainedMarkerTag) {
		t.Errorf("promoted skill must carry the %q marker tag, tags: %v", TrainedMarkerTag, art.Tags)
	}
	if art.TrainedCandidate != c.ID {
		t.Errorf("trained_candidate = %q, want %q", art.TrainedCandidate, c.ID)
	}
	if art.TrainedVersion != art.Version {
		t.Errorf("trained_version = %d, want the promoted version %d", art.TrainedVersion, art.Version)
	}
	if art.TrainedParentVersion != parentVersion {
		t.Errorf("trained_parent_version = %d, want %d", art.TrainedParentVersion, parentVersion)
	}
	if art.TrainedValScore != c.BestScore {
		t.Errorf("trained_val_score = %.4f, want the gate score %.4f", art.TrainedValScore, c.BestScore)
	}
	if art.TrainedAt.IsZero() {
		t.Error("trained_at must be stamped")
	}
	if !art.TrainedRevokedAt.IsZero() {
		t.Error("a fresh marker must not be revoked")
	}
}

func TestGateAcceptStampsTrainedMarker(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Trainable Skill", "# v1 body")

	promoted, c := promoteCandidate(t, s, skill, "# v2 trained body")
	assertTrainedMarker(t, promoted, c, skill.Version)

	// The stamp is part of the promotion save: exactly one version bump, and the
	// edit summary still names the candidate.
	if promoted.Version != skill.Version+1 {
		t.Errorf("stamp must not add a version: got v%d, want v%d", promoted.Version, skill.Version+1)
	}
	if !strings.Contains(promoted.EditSummary, c.ID) {
		t.Errorf("edit summary must link the candidate, got %q", promoted.EditSummary)
	}

	// The marker metadata is real OKF front matter and survives a reparse.
	raw, err := os.ReadFile(filepath.Join(s.ArticleDir, promoted.Slug+".md"))
	if err != nil {
		t.Fatalf("read skill file: %v", err)
	}
	for _, key := range []string{"trained_at:", "trained_version:", "trained_candidate:", "trained_val_score:"} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("front matter must carry %s, got:\n%s", key, raw)
		}
	}
	reparsed, err := parseArticleFile(raw, true)
	if err != nil {
		t.Fatalf("reparse failed: %v", err)
	}
	if reparsed.TrainedVersion != promoted.Version || reparsed.TrainedCandidate != c.ID {
		t.Errorf("marker did not round-trip: %+v", reparsed)
	}
	if !hasTag(reparsed.Tags, TrainedMarkerTag) {
		t.Errorf("marker tag must round-trip, tags: %v", reparsed.Tags)
	}

	// The gate decision trail still carries exactly the accept record.
	recs, err := s.ListSkillAuditRecords()
	if err != nil {
		t.Fatalf("ListSkillAuditRecords failed: %v", err)
	}
	if len(recs) != 1 || recs[0].Outcome != SkillAuditAccepted {
		t.Errorf("expected one accepted gate record, got %+v", recs)
	}
}

func TestMarkerStampsCurrentEvalHash(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Eval Trained Skill", "# v1 body")
	t.Setenv(EvalMinTrainEnv, "1")
	t.Setenv(EvalMinValEnv, "1")

	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	content := `{"input": "case one", "expected": "alpha beta"}
{"input": "case two", "expected": "gamma delta"}
{"input": "case three", "expected": "epsilon zeta"}
{"input": "case four", "expected": "eta theta"}`
	if _, err := s.UploadEvolutionEvalSet(job.ID, token, "eval.jsonl", content); err != nil {
		t.Fatalf("UploadEvolutionEvalSet failed: %v", err)
	}
	wantHash, err := s.latestEvalHashForSkill(skill.Slug)
	if err != nil || wantHash == "" {
		t.Fatalf("eval hash must be resolvable after upload: %q %v", wantHash, err)
	}

	promoted, _ := promoteCandidate(t, s, skill, "# v2 trained on eval")
	if promoted.TrainedEvalHash != wantHash {
		t.Errorf("trained_eval_hash = %q, want the stored hash %.12s", promoted.TrainedEvalHash, wantHash)
	}
}

func TestRetrainOverwritesMarker(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Retrained Skill", "# v1 body")

	first, c1 := promoteCandidate(t, s, skill, "# v2 trained body")
	// The gate must strictly beat R_best, so the re-train body is strictly
	// better per the v0 scorer (a longer body earns a larger substance bonus).
	betterBody := strings.Repeat("# v3 retrained body with more substance.\n\n", 40)
	second, c2 := promoteCandidate(t, s, first, betterBody)

	assertTrainedMarker(t, second, c2, first.Version)
	if second.TrainedVersion == first.TrainedVersion || second.TrainedCandidate == c1.ID {
		t.Errorf("re-train must overwrite the marker: %+v", second)
	}
	if !strings.Contains(second.EditSummary, c2.ID) {
		t.Errorf("re-train edit summary must name the new candidate, got %q", second.EditSummary)
	}
}

func TestTrainedMarkerSurvivesAgentTagEdits(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Marked Skill", "# v1 body")
	promoted, _ := promoteCandidate(t, s, skill, "# v2 trained body")

	// Storage choke point: a tag-only write that arrives without the marker
	// cannot strip it (UpdateArticleTags refuses locked skills, so unlock first).
	if _, err := s.UnlockSkill(promoted.Slug); err != nil {
		t.Fatalf("UnlockSkill failed: %v", err)
	}
	kept, err := s.UpdateArticleTags(promoted.Slug, []string{"agent", "tags"}, 0, "agent edit")
	if err != nil {
		t.Fatalf("UpdateArticleTags failed: %v", err)
	}
	if !hasTag(kept.Tags, TrainedMarkerTag) {
		t.Errorf("storage guard must re-assert a stripped marker tag, tags: %v", kept.Tags)
	}

	// MCP tool path: same rule through update_article_tags.
	resp := toolCall(t, srvForStorage(t, s), fmt.Sprintf(`{"name":"update_article_tags","arguments":{"slug":%q,"tags":["only","this"],"loaded_version":%d}}`, kept.Slug, kept.Version))
	if resp.IsError {
		t.Fatalf("update_article_tags failed: %s", resp.Content[0].Text)
	}
	live, err := s.GetArticle(promoted.Slug)
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if !hasTag(live.Tags, TrainedMarkerTag) {
		t.Errorf("agent tag edit must not strip the marker tag, tags: %v", live.Tags)
	}
}

func TestTrainedMarkerUnforgeableViaTools(t *testing.T) {
	srv := newMCPServer(t)
	seedSkill(t, srv.Storage, "Unmarked Skill", "# body")

	resp := toolCall(t, srv, `{"name":"update_article_tags","arguments":{"slug":"unmarked-skill","tags":["trained-wikiskill","real"],"loaded_version":1}}`)
	if resp.IsError {
		t.Fatalf("update_article_tags failed: %s", resp.Content[0].Text)
	}
	art, err := srv.Storage.GetArticle("unmarked-skill")
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if hasTag(art.Tags, TrainedMarkerTag) {
		t.Errorf("agents must not forge the marker tag, tags: %v", art.Tags)
	}
	if !hasTag(art.Tags, "real") {
		t.Errorf("legitimate tags must survive the cleaning, tags: %v", art.Tags)
	}
}

func TestDeleteTagGloballyRefusesTrainedMarker(t *testing.T) {
	s := newLifecycleStorage(t)
	if err := s.DeleteTagGlobally(TrainedMarkerTag); err == nil {
		t.Fatal("global deletion of the trained marker tag must be refused")
	} else if !strings.Contains(err.Error(), "tool-managed") {
		t.Errorf("refusal must explain the marker is tool-managed, got: %v", err)
	}
}

func TestTrainedStatePickerStates(t *testing.T) {
	s := newLifecycleStorage(t)
	untrained := seedSkill(t, s, "Fresh Skill", "# fresh body")
	marked := seedSkill(t, s, "Solid Skill", "# v1 body")
	markedPromoted, _ := promoteCandidate(t, s, marked, "# v2 trained body")

	states, err := s.ListSkillTrainedStates()
	if err != nil {
		t.Fatalf("ListSkillTrainedStates failed: %v", err)
	}
	bySlug := map[string]SkillTrainedStateEntry{}
	for _, e := range states {
		bySlug[e.Slug] = e
	}
	if got := bySlug[untrained.Slug]; got.State != TrainedStateUntrained || got.Reason != "" {
		t.Errorf("untrained state wrong: %+v", got)
	}
	if got := bySlug[markedPromoted.Slug]; got.State != TrainedStateTrained || got.Reason != "" {
		t.Errorf("trained state wrong: %+v", got)
	}
}

func TestStaleAfterPostTrainEdit(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Edited Skill", "# v1 body")
	promoted, _ := promoteCandidate(t, s, skill, "# v2 trained body")

	if _, err := s.UnlockSkill(promoted.Slug); err != nil {
		t.Fatalf("UnlockSkill failed: %v", err)
	}
	// UnlockSkill bumped the version; re-fetch so LoadedVersion is current.
	live, err := s.GetArticle(promoted.Slug)
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	edited, err := s.ApplyArticleEdit(promoted.Slug, ArticleEdit{Title: live.Title, Content: "# hijacked after train", LoadedVersion: live.Version})
	if err != nil {
		t.Fatalf("post-train edit failed: %v", err)
	}

	state, err := s.GetSkillTrainedState(promoted.Slug)
	if err != nil {
		t.Fatalf("GetSkillTrainedState failed: %v", err)
	}
	if state.State != TrainedStateStale {
		t.Fatalf("expected stale after a post-train edit, got %+v", state)
	}
	if !strings.Contains(state.Reason, fmt.Sprintf("trained at v%d, now v%d", promoted.Version, edited.Version)) {
		t.Errorf("staleness reason must name both versions, got %q", state.Reason)
	}
	// Flagged, not withdrawn: the marker tag and metadata stay usable.
	if !hasTag(edited.Tags, TrainedMarkerTag) {
		t.Errorf("stale marker must stay tagged, tags: %v", edited.Tags)
	}
	if state.TrainedVersion != promoted.Version {
		t.Errorf("stale marker must keep its metadata, got %+v", state)
	}
}

func TestStaleAfterEvalSetChange(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Moved Baseline Skill", "# v1 body")
	t.Setenv(EvalMinTrainEnv, "1")
	t.Setenv(EvalMinValEnv, "1")

	promoted, _ := promoteCandidate(t, s, skill, "# v2 trained body")

	job, token, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	// Two distinct cases are the smallest set the gate accepts with the 1/1
	// floors above: the auto-split takes the last quarter as val (1 train / 1
	// val). A single case cannot fill both splits, so the gate refuses it.
	content := `{"input": "later case", "expected": "new vocabulary"}
{"input": "another case", "expected": "fresh wording"}`
	if _, err := s.UploadEvolutionEvalSet(job.ID, token, "eval.jsonl", content); err != nil {
		t.Fatalf("UploadEvolutionEvalSet failed: %v", err)
	}

	state, err := s.GetSkillTrainedState(promoted.Slug)
	if err != nil {
		t.Fatalf("GetSkillTrainedState failed: %v", err)
	}
	if state.State != TrainedStateStale {
		t.Fatalf("expected stale after an eval re-upload, got %+v", state)
	}
	if !strings.Contains(state.Reason, "eval set changed after training") {
		t.Errorf("staleness reason must name the eval change, got %q", state.Reason)
	}
	if state.CurrentEvalHash == "" || state.CurrentEvalHash == state.TrainedEvalHash {
		t.Errorf("the compared hashes must be visible on the row: %+v", state)
	}
}

func TestStaleWhenTagAndMetadataDisagree(t *testing.T) {
	s := newLifecycleStorage(t)

	// A plain untrained skill — no marker tag, no metadata — is untrained, not
	// a tamper. Only a DISAGREEMENT between tag and metadata is hand-editing.
	untrained := seedSkill(t, s, "Tampered Skill", "# body")
	state, err := s.GetSkillTrainedState(untrained.Slug)
	if err != nil {
		t.Fatalf("GetSkillTrainedState failed: %v", err)
	}
	if state.State != TrainedStateUntrained {
		t.Errorf("a skill with no marker at all is untrained, got %+v", state)
	}

	// Tamper 1: marker tag present without trained metadata (hand-stamped tag).
	if _, err := s.SaveArticle("", "Tagged Only", "# body", "", "", "", "seed", []string{TrainedMarkerTag}, ContentTypeSkill); err != nil {
		t.Fatalf("seeding tagged-only skill failed: %v", err)
	}

	// Tamper 2: trained metadata present but the marker tag hand-stripped from
	// the front matter (the save-path guard would re-assert it, so this state
	// can only arise by editing the file, which is exactly the threat).
	metadataOnly, _ := promoteCandidate(t, s, seedSkill(t, s, "Stripped Skill", "# v1 body"), "# v2 trained body")
	path := filepath.Join(s.ArticleDir, metadataOnly.Slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read skill file: %v", err)
	}
	stripped := strings.Replace(string(raw), "    - trained-wikiskill\n", "", 1)
	if stripped == string(raw) {
		t.Fatalf("marker tag line not found to strip, file:\n%s", raw)
	}
	if err := os.WriteFile(path, []byte(stripped), 0644); err != nil {
		t.Fatalf("write tampered skill file: %v", err)
	}

	for _, slug := range []string{"tagged-only", metadataOnly.Slug} {
		state, err := s.GetSkillTrainedState(slug)
		if err != nil {
			t.Fatalf("GetSkillTrainedState(%s) failed: %v", slug, err)
		}
		if state.State != TrainedStateStale {
			t.Errorf("expected stale for %s, got %+v", slug, state)
		}
		if !strings.Contains(state.Reason, "hand-edited") {
			t.Errorf("expected a hand-edited reason for %s, got %q", slug, state.Reason)
		}
	}
}

func TestGetSkillTrainedStateRefusesNonSkill(t *testing.T) {
	s := newLifecycleStorage(t)
	if _, err := s.SaveArticle("", "Plain Article", "# w", "", "", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatalf("seeding wiki failed: %v", err)
	}
	if _, err := s.GetSkillTrainedState("plain-article"); err == nil {
		t.Error("trained state of a wiki article must be refused")
	}
	if _, err := s.GetSkillTrainedState("missing-skill"); err == nil {
		t.Error("trained state of a missing slug must be refused")
	}
}

func TestRollbackTrainedSkill(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Rolling Back Skill", "# v1 body")
	promoted, c := promoteCandidate(t, s, skill, "# v2 trained body")

	rolled, err := s.RollbackTrainedSkill(promoted.Slug, "regressed on val")
	if err != nil {
		t.Fatalf("RollbackTrainedSkill failed: %v", err)
	}

	// The pre-training body is back.
	if rolled.Content != "# v1 body" {
		t.Errorf("rollback must restore the pre-training body, got %q", rolled.Content)
	}
	// The marker becomes revoked with its reason, never silently deleted.
	if !hasTag(rolled.Tags, TrainedMarkerTag) {
		t.Errorf("revoked marker must keep its tag, tags: %v", rolled.Tags)
	}
	if rolled.TrainedVersion != promoted.Version || rolled.TrainedParentVersion != skill.Version || rolled.TrainedCandidate != c.ID {
		t.Errorf("rollback must preserve the marker metadata, got %+v", rolled)
	}
	if rolled.TrainedRevokedAt.IsZero() || !strings.Contains(rolled.TrainedRevokedReason, "regressed on val") {
		t.Errorf("revocation reason not recorded: %q at %v", rolled.TrainedRevokedReason, rolled.TrainedRevokedAt)
	}

	// The audit trail carries the revocation.
	recs, err := s.ListSkillAuditRecords()
	if err != nil {
		t.Fatalf("ListSkillAuditRecords failed: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected accept + revoke records, got %d: %+v", len(recs), recs)
	}
	revoke := recs[1]
	if revoke.Outcome != SkillAuditRevoked || revoke.Decider != SkillAuditDeciderHuman {
		t.Errorf("revoke record wrong: %+v", revoke)
	}
	if revoke.Reason != "regressed on val" {
		t.Errorf("revoke record must carry the reason, got %q", revoke.Reason)
	}
	if revoke.CandidateID != c.ID || revoke.SkillSlug != promoted.Slug || revoke.ParentVersion != promoted.Version {
		t.Errorf("revoke record linkage wrong: %+v", revoke)
	}
	if revoke.ContentHash == "" || len(revoke.ContentHash) != 64 {
		t.Errorf("revoke record must hash the withdrawn trained body, got %q", revoke.ContentHash)
	}
	if err := revoke.validate(); err != nil {
		t.Errorf("emitted revoke record must validate, got: %v", err)
	}

	// The picker reads the rollback as staleness with the recorded reason.
	state, err := s.GetSkillTrainedState(promoted.Slug)
	if err != nil {
		t.Fatalf("GetSkillTrainedState failed: %v", err)
	}
	if state.State != TrainedStateStale || !strings.Contains(state.Reason, "regressed on val") {
		t.Errorf("picker must report the revoked marker stale with its reason, got %+v", state)
	}
}

func TestRollbackRefusals(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Refusal Skill", "# v1 body")

	// Nothing to roll back.
	if _, err := s.RollbackTrainedSkill(skill.Slug, "no marker yet"); err == nil {
		t.Error("rollback without a marker must be refused")
	}
	// A reason is required.
	if _, err := s.RollbackTrainedSkill(skill.Slug, ""); err == nil {
		t.Error("rollback without a reason must be refused")
	}

	promoted, _ := promoteCandidate(t, s, skill, "# v2 trained body")
	// One run per skill: no history rewrite mid-run.
	job, _, err := s.CreateEvolutionJob(skill.Slug, "", "opencode")
	if err != nil {
		t.Fatalf("CreateEvolutionJob failed: %v", err)
	}
	if _, err := s.RollbackTrainedSkill(promoted.Slug, "mid-run"); err == nil {
		t.Error("rollback while an evolution job is live must be refused")
	} else if !strings.Contains(err.Error(), job.ID) {
		t.Errorf("refusal must name the live job, got: %v", err)
	}

	// A non-skill target is refused.
	if _, err := s.SaveArticle("", "Rollback Wiki", "# w", "", "", "", "", nil, ContentTypeWiki); err != nil {
		t.Fatalf("seeding wiki failed: %v", err)
	}
	if _, err := s.RollbackTrainedSkill("rollback-wiki", "not a skill"); err == nil {
		t.Error("rollback of a non-skill must be refused")
	}
}

func TestUnlockTrainedMarker(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Withdrawable Skill", "# v1 body")

	// Refused when there is nothing to unlock — this must run before the
	// promote below, because promotion writes a new version of the SAME slug
	// (saveArticleLocked on parent.Slug), stamping its marker there.
	if _, err := s.UnlockTrainedMarker(skill.Slug, "nothing there"); err == nil {
		t.Error("unlock without a marker must be refused")
	}

	promoted, c := promoteCandidate(t, s, skill, "# v2 trained body")
	if promoted.Slug != skill.Slug {
		t.Fatalf("promote must write a new version of the same slug, got %q", promoted.Slug)
	}
	unlocked, err := s.UnlockTrainedMarker(promoted.Slug, "withdrawing the marker")
	if err != nil {
		t.Fatalf("UnlockTrainedMarker failed: %v", err)
	}
	if hasTag(unlocked.Tags, TrainedMarkerTag) {
		t.Errorf("unlock must remove the marker tag, tags: %v", unlocked.Tags)
	}
	if !unlocked.TrainedAt.IsZero() || unlocked.TrainedVersion != 0 || unlocked.TrainedCandidate != "" || unlocked.TrainedEvalHash != "" {
		t.Errorf("unlock must clear every trained field, got %+v", unlocked)
	}

	// The removal is audited, prefixing "unlock" to separate it from a rollback.
	recs, err := s.ListSkillAuditRecords()
	if err != nil {
		t.Fatalf("ListSkillAuditRecords failed: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected accept + unlock records, got %d: %+v", len(recs), recs)
	}
	audit := recs[1]
	if audit.Outcome != SkillAuditRevoked || audit.Decider != SkillAuditDeciderHuman {
		t.Errorf("unlock record wrong: %+v", audit)
	}
	if audit.Reason != "unlock: withdrawing the marker" {
		t.Errorf("unlock record must carry the prefixed reason, got %q", audit.Reason)
	}
	if audit.CandidateID != c.ID || audit.SkillSlug != promoted.Slug {
		t.Errorf("unlock record linkage wrong: %+v", audit)
	}
	if err := audit.validate(); err != nil {
		t.Errorf("emitted unlock record must validate, got: %v", err)
	}

	// A second unlock has nothing left to withdraw.
	if _, err := s.UnlockTrainedMarker(promoted.Slug, "again"); err == nil {
		t.Error("second unlock must be refused")
	}
}

func TestRollbackThenRetrainRestoresActiveMarker(t *testing.T) {
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Cycle Skill", "# v1 body")
	promoted, _ := promoteCandidate(t, s, skill, "# v2 trained body")

	if _, err := s.RollbackTrainedSkill(promoted.Slug, "bad run"); err != nil {
		t.Fatalf("RollbackTrainedSkill failed: %v", err)
	}
	// The gate must strictly beat R_best, so the re-train body is strictly
	// better per the v0 scorer; the restored active state is read from
	// GetSkillTrainedState below.
	betterBody := strings.Repeat("# v3 trained again with more substance.\n\n", 40)
	retrained, _ := promoteCandidate(t, s, skill, betterBody)
	if retrained.TrainedRevokedAt != (time.Time{}) || retrained.TrainedRevokedReason != "" {
		t.Errorf("re-training must clear the revocation, got %+v", retrained)
	}
	if !hasTag(retrained.Tags, TrainedMarkerTag) {
		t.Errorf("re-trained marker must be tagged, tags: %v", retrained.Tags)
	}
	if retrained.TrainedVersion != retrained.Version {
		t.Errorf("re-trained marker must name the new version, got %+v", retrained)
	}
	state, err := s.GetSkillTrainedState(skill.Slug)
	if err != nil {
		t.Fatalf("GetSkillTrainedState failed: %v", err)
	}
	if state.State != TrainedStateTrained {
		t.Errorf("picker must see the marker active again, got %+v", state)
	}
}

func TestTrainedMarkerRidesOkfBundle(t *testing.T) {
	src := newLifecycleStorage(t)
	skill := seedSkill(t, src, "Exported Skill", "# v1 body")
	promoted, _ := promoteCandidate(t, src, skill, "# v2 trained body")

	bundle, err := src.ExportOKFBundle()
	if err != nil {
		t.Fatalf("ExportOKFBundle failed: %v", err)
	}

	dst, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })
	report, err := dst.ImportOKFBundle(bundle)
	if err != nil {
		t.Fatalf("ImportOKFBundle failed: %v", err)
	}
	if report.Imported == 0 {
		t.Fatalf("nothing imported: %+v", report)
	}

	state, err := dst.GetSkillTrainedState(promoted.Slug)
	if err != nil {
		t.Fatalf("GetSkillTrainedState failed: %v", err)
	}
	// Tag presence is asserted via a storage read: the picker row carries the
	// derived state, not the document's tag list.
	imported, err := dst.GetArticle(promoted.Slug)
	if err != nil {
		t.Fatalf("GetArticle failed: %v", err)
	}
	if !hasTag(imported.Tags, TrainedMarkerTag) {
		t.Errorf("marker tag must ride the bundle, tags: %v", imported.Tags)
	}
	if state.TrainedVersion != promoted.Version || state.TrainedCandidate != promoted.TrainedCandidate {
		t.Errorf("marker metadata must ride the bundle, got %+v", state)
	}
	// Front matter carries RFC3339 (second precision), so compare the
	// truncated-to-second values rather than full-precision instants.
	if state.TrainedAt.Unix() != promoted.TrainedAt.Truncate(time.Second).Unix() {
		t.Errorf("trained_at not preserved: %v vs %v", state.TrainedAt, promoted.TrainedAt)
	}
}

func TestMcpGetSkillTrainedState(t *testing.T) {
	srv := newMCPServer(t)
	fresh := seedSkill(t, srv.Storage, "Picker Fresh Skill", "# fresh body")
	marked := seedSkill(t, srv.Storage, "Picker Marked Skill", "# v1 body")
	promoted, _ := promoteCandidate(t, srv.Storage, marked, "# v2 trained body")

	resp := toolCall(t, srv, `{"name":"get_skill_trained_state","arguments":{}}`)
	if resp.IsError {
		t.Fatalf("get_skill_trained_state failed: %s", resp.Content[0].Text)
	}
	if resp.StructuredContent == nil {
		t.Fatal("tool declares an outputSchema but returned no structuredContent")
	}
	var out SkillTrainedStateListOutput
	encoded, err := json.Marshal(resp.StructuredContent)
	if err != nil {
		t.Fatalf("structuredContent does not serialize: %v", err)
	}
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("structuredContent does not decode: %v", err)
	}
	if out.Count != len(out.Skills) || out.Count < 2 {
		t.Fatalf("picker rows wrong: count=%d skills=%+v", out.Count, out.Skills)
	}
	found := map[string]SkillTrainedStateEntry{}
	for _, e := range out.Skills {
		found[e.Slug] = e
	}
	if got := found[fresh.Slug]; got.State != TrainedStateUntrained {
		t.Errorf("fresh skill row wrong: %+v", got)
	}
	if got := got0(promoted.Slug, found); got.State != TrainedStateTrained || got.TrainedCandidate == "" || got.CurrentVersion != promoted.Version {
		t.Errorf("marked skill row wrong: %+v", got)
	}
	if !strings.Contains(resp.Content[0].Text, "untrained") || !strings.Contains(resp.Content[0].Text, "trained (v") {
		t.Errorf("prose half must carry the states, got: %s", resp.Content[0].Text)
	}

	// Single-skill lookup.
	resp2 := toolCall(t, srv, fmt.Sprintf(`{"name":"get_skill_trained_state","arguments":{"slug":%q}}`, fresh.Slug))
	if resp2.IsError {
		t.Fatalf("single lookup failed: %s", resp2.Content[0].Text)
	}
	var single SkillTrainedStateListOutput
	encoded2, _ := json.Marshal(resp2.StructuredContent)
	if err := json.Unmarshal(encoded2, &single); err != nil {
		t.Fatalf("single lookup does not decode: %v", err)
	}
	if single.Count != 1 || len(single.Skills) != 1 || single.Skills[0].Slug != fresh.Slug {
		t.Fatalf("single lookup wrong: %+v", single)
	}

	// A missing slug is answered honestly.
	resp3 := toolCall(t, srv, `{"name":"get_skill_trained_state","arguments":{"slug":"no-such-skill"}}`)
	if !resp3.IsError {
		t.Fatal("missing slug must be an error")
	}
}

func got0(slug string, rows map[string]SkillTrainedStateEntry) SkillTrainedState {
	return rows[slug].SkillTrainedState
}

func TestMcpListAgentSkillsCarriesTrainedLine(t *testing.T) {
	srv := newMCPServer(t)
	fresh := seedSkill(t, srv.Storage, "Index Fresh Skill", "# fresh body")
	marked := seedSkill(t, srv.Storage, "Index Marked Skill", "# v1 body")
	promoted, _ := promoteCandidate(t, srv.Storage, marked, "# v2 trained body")

	resp := toolCall(t, srv, `{"name":"list_agent_skills","arguments":{}}`)
	if resp.IsError {
		t.Fatalf("list_agent_skills failed: %s", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, fresh.Title) || !strings.Contains(resp.Content[0].Text, promoted.Title) {
		t.Fatalf("index must list both skills, got: %s", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, "Trained: untrained") {
		t.Errorf("untrained skill must report so, got: %s", resp.Content[0].Text)
	}
	if !strings.Contains(resp.Content[0].Text, fmt.Sprintf("Trained: trained (v%d", promoted.Version)) {
		t.Errorf("trained marker must be visible in the listing, got: %s", resp.Content[0].Text)
	}
}

func TestRevokedMarkerKeepsLogClean(t *testing.T) {
	// The marker stamping never logs beyond Stderr by construction; this guards
	// the artifact path: the stamped skill file is the only record, and the
	// audit trail stays append-only — the rollback appends exactly one record.
	s := newLifecycleStorage(t)
	skill := seedSkill(t, s, "Clean Log Skill", "# v1 body")
	promoted, c := promoteCandidate(t, s, skill, "# v2 trained body")

	before, err := s.ListSkillAuditRecords()
	if err != nil {
		t.Fatalf("ListSkillAuditRecords failed: %v", err)
	}
	rolled, err := s.RollbackTrainedSkill(promoted.Slug, "log stays clean")
	if err != nil {
		t.Fatalf("RollbackTrainedSkill failed: %v", err)
	}
	after, err := s.ListSkillAuditRecords()
	if err != nil {
		t.Fatalf("ListSkillAuditRecords failed: %v", err)
	}

	// The stamped skill file is the only artifact: the rollback wrote exactly
	// one new version, restoring the pre-training body.
	if rolled.Version != promoted.Version+1 {
		t.Errorf("rollback must add exactly one version, got v%d after v%d", rolled.Version, promoted.Version)
	}
	// The audit record is appended by the operation, not before it.
	if len(after) != len(before)+1 {
		t.Fatalf("rollback must append exactly one audit record, before=%d after=%d", len(before), len(after))
	}
	revoke := after[len(after)-1]
	if revoke.Outcome != SkillAuditRevoked || revoke.Decider != SkillAuditDeciderHuman {
		t.Errorf("appended record must be the human revoke, got %+v", revoke)
	}
	if revoke.CandidateID != c.ID || revoke.SkillSlug != promoted.Slug || revoke.Reason != "log stays clean" {
		t.Errorf("appended record linkage wrong: %+v", revoke)
	}
	if err := revoke.validate(); err != nil {
		t.Errorf("emitted revoke record must validate, got: %v", err)
	}
}

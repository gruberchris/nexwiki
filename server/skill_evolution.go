package server

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// This file holds story 01 of the wikiskill evolution support plan: the skill lock and the
// skill candidate flow.
//
// A locked skill is agent-read-only. Agent-facing edit paths (edit_agent_skill,
// edit_wiki_article via ApplyArticleEdit, update_article_tags via UpdateArticleTags,
// revert_article_version via RevertArticle, delete_wiki_article) refuse writes to a locked
// skill with a SkillLockedError naming the locker and timestamp. The skill advances only
// through candidates: a candidate records a parent skill slug + parent version, a unified
// diff and/or a full proposed body, motivating wiki pattern slugs, a proposer identity, and
// a timestamp. Promoting a candidate writes a new skill version; rejecting one leaves the
// live skill byte-identical.
//
// Deliberately NOT built here (story 04 owns it): the job/lease/BYO-CLI runner.
// The story 03 validation gate, R_best scoring, and the harness-owned audit
// trail (server/skill_audit.go) are wired in below; full eval-store/scorer
// versioning is story 11, which plugs into RBestScore without touching the
// promote flow or the audit shape.

// SkillLockedError reports a refused write to a locked skill. It names the locker and the
// lock timestamp so the agent knows who to ask and how fresh the lock is.
type SkillLockedError struct {
	Slug     string
	LockedBy string
	LockedAt time.Time
}

func (e *SkillLockedError) Error() string {
	when := "unknown time"
	if !e.LockedAt.IsZero() {
		when = e.LockedAt.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf("skill '%s' is locked by %s at %s: agents are read-only on locked skills — create a skill candidate instead of editing directly",
		e.Slug, e.LockedBy, when)
}

// checkSkillLock returns a *SkillLockedError when art is a locked skill, nil otherwise.
// Unlocked skills and non-skill documents are unaffected.
func checkSkillLock(art *Article) error {
	if art == nil || art.Type != ContentTypeSkill || strings.TrimSpace(art.LockedBy) == "" {
		return nil
	}
	return &SkillLockedError{Slug: art.Slug, LockedBy: art.LockedBy, LockedAt: art.LockedAt}
}

// LockSkill marks a skill as agent-read-only. The lock is stored on the skill document
// itself (locked_by/locked_at front-matter keys) so it travels with OKF export/import.
func (s *Storage) LockSkill(slug, locker string) (*Article, error) {
	locker = strings.TrimSpace(locker)
	if locker == "" {
		return nil, fmt.Errorf("locker identity is required to lock a skill")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	existing, err := s.GetArticle(slug)
	if err != nil {
		return nil, err
	}
	if existing.Type != ContentTypeSkill {
		return nil, fmt.Errorf("cannot lock '%s': not a Custom AI Skill (type must be AI-Agent-Skill)", slug)
	}
	now := time.Now().UTC()
	return s.saveArticleLocked(existing.Slug, existing.Title, existing.Content, existing.Description,
		existing.Source, existing.Resource, fmt.Sprintf("Locked by %s", locker), existing.Tags,
		existing.Type, ArticleOverrides{LockedBy: &locker, LockedAt: &now})
}

// UnlockSkill clears the evolution lock. Unlocking is a human/operator action, not an
// agent edit: it runs through saveArticleLocked directly, bypassing the lock guard.
func (s *Storage) UnlockSkill(slug string) (*Article, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	existing, err := s.GetArticle(slug)
	if err != nil {
		return nil, err
	}
	if existing.Type != ContentTypeSkill {
		return nil, fmt.Errorf("cannot unlock '%s': not a Custom AI Skill (type must be AI-Agent-Skill)", slug)
	}
	empty := ""
	zero := time.Time{}
	return s.saveArticleLocked(existing.Slug, existing.Title, existing.Content, existing.Description,
		existing.Source, existing.Resource, "Unlocked", existing.Tags,
		existing.Type, ArticleOverrides{LockedBy: &empty, LockedAt: &zero})
}

// Skill candidate lifecycle states.
const (
	SkillCandidatePending  = "pending"
	SkillCandidatePromoted = "promoted"
	SkillCandidateRejected = "rejected"
)

// SkillCandidate is a proposed skill evolution. It is a data record, never wiki content:
// stored as JSON under data/skill_candidates, never indexed, never executed. The Diff and
// ProposedBody are inert data — nothing in this flow passes wiki content to a shell.
type SkillCandidate struct {
	ID            string    `json:"id"`
	ParentSlug    string    `json:"parent_slug"`
	ParentVersion int       `json:"parent_version"`
	Diff          string    `json:"diff,omitempty"`
	ProposedBody  string    `json:"proposed_body,omitempty"`
	PatternSlugs  []string  `json:"pattern_slugs,omitempty"`
	Proposer      string    `json:"proposer"`
	CreatedAt     time.Time `json:"created_at"`
	Status        string    `json:"status"`
	ResultVersion int       `json:"result_version,omitempty"`
	BestScore     float64   `json:"best_score,omitempty"`
	DecidedAt     time.Time `json:"decided_at,omitzero"`
}

// candidatePath validates a candidate ID and resolves its storage path. IDs are
// server-generated (parent slug + timestamp), but the lookup still confines reads to the
// candidate directory so a crafted ID can never escape it.
func (s *Storage) candidatePath(id string) (string, error) {
	base := filepath.Base(strings.TrimSpace(id))
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, `/\`) {
		return "", fmt.Errorf("invalid skill candidate id")
	}
	if s.CandidateDir == "" {
		return "", fmt.Errorf("skill candidate storage is not configured")
	}
	return filepath.Join(s.CandidateDir, base+".json"), nil
}

func (s *Storage) writeCandidate(c *SkillCandidate) error {
	if err := os.MkdirAll(s.CandidateDir, 0755); err != nil {
		return fmt.Errorf("failed to create skill candidate directory: %w", err)
	}
	path, err := s.candidatePath(c.ID)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode skill candidate: %w", err)
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		return fmt.Errorf("failed to write skill candidate: %w", err)
	}
	return nil
}

// CreateSkillCandidate records a proposed evolution of a skill. The parent skill is read,
// never written: candidates are created, never direct edits to a locked skill.
func (s *Storage) CreateSkillCandidate(parentSlug, proposedBody, diff string, patternSlugs []string, proposer string) (*SkillCandidate, error) {
	parentSlug = Slugify(parentSlug)
	proposer = strings.TrimSpace(proposer)
	if parentSlug == "" {
		return nil, fmt.Errorf("parent skill slug is required")
	}
	if proposer == "" {
		return nil, fmt.Errorf("proposer identity is required")
	}
	if strings.TrimSpace(proposedBody) == "" && strings.TrimSpace(diff) == "" {
		return nil, fmt.Errorf("a candidate needs a unified diff or a full proposed body (or both)")
	}

	parent, err := s.GetArticle(parentSlug)
	if err != nil {
		return nil, fmt.Errorf("parent skill not found: %s", parentSlug)
	}
	if parent.Type != ContentTypeSkill {
		return nil, fmt.Errorf("cannot propose a candidate for '%s': not a Custom AI Skill", parentSlug)
	}

	// Candidates are a write path too, so they are secret-scanned like every other one.
	// A scanned body is refused, never stored.
	if secretScanMode() == SecretScanRefuse {
		if found := scanDocumentFields(proposedBody, "", ""); len(found) > 0 {
			return nil, fmt.Errorf("%s", secretRefusalMessage("skill candidate", found))
		}
	}

	cleanedPatterns := make([]string, 0, len(patternSlugs))
	for _, p := range patternSlugs {
		if sl := Slugify(p); sl != "" {
			cleanedPatterns = append(cleanedPatterns, sl)
		}
	}

	now := time.Now().UTC()
	base := fmt.Sprintf("%s-candidate-%s", parent.Slug, now.Format("20060102-150405"))
	c := &SkillCandidate{
		ParentSlug:    parent.Slug,
		ParentVersion: parent.Version,
		Diff:          diff,
		ProposedBody:  proposedBody,
		PatternSlugs:  cleanedPatterns,
		Proposer:      proposer,
		CreatedAt:     now,
		Status:        SkillCandidatePending,
	}
	// Timestamps collide within a second under test clocks; suffix until the ID is fresh.
	c.ID = base
	for i := 2; ; i++ {
		path, err := s.candidatePath(c.ID)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		c.ID = fmt.Sprintf("%s-%d", base, i)
	}

	if err := s.writeCandidate(c); err != nil {
		return nil, err
	}
	return c, nil
}

// GetSkillCandidate loads a candidate record by ID.
func (s *Storage) GetSkillCandidate(id string) (*SkillCandidate, error) {
	path, err := s.candidatePath(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("skill candidate not found: %s", id)
		}
		return nil, err
	}
	var c SkillCandidate
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("invalid skill candidate record %s: %w", id, err)
	}
	return &c, nil
}

// ListSkillCandidates returns every candidate record, oldest first.
func (s *Storage) ListSkillCandidates() ([]SkillCandidate, error) {
	entries, err := os.ReadDir(s.CandidateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SkillCandidate{}, nil
		}
		return nil, err
	}
	var out []SkillCandidate
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		c, err := s.GetSkillCandidate(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if out == nil {
		out = []SkillCandidate{}
	}
	return out, nil
}

// SkillGateRejection reports a candidate the story 03 validation gate refused
// to promote: its score did not beat the running best. It carries both numbers
// so the operator (and the audit record) can see the margin.
type SkillGateRejection struct {
	CandidateID string
	Score       float64
	Best        float64
}

func (e *SkillGateRejection) Error() string {
	return fmt.Sprintf("skill candidate '%s' rejected by validation gate: score %.4f does not beat R_best %.4f — strengthen the proposal (more evidence, clearer diff) and propose again",
		e.CandidateID, e.Score, e.Best)
}

// ValidateSkillCandidateForPromote is the story 03 validation gate: a pending
// candidate promotes only when its validation score beats the running best
// for the skill. A perfect score (1.0) accepts outright via the early-stop
// hook — even against a perfect best — so a strictly-greater compare can
// never deadlock the trail at the ceiling.
func ValidateSkillCandidateForPromote(c *SkillCandidate, parent *Article, rBestBefore float64) error {
	if c.Status != SkillCandidatePending {
		return fmt.Errorf("skill candidate '%s' is already decided (%s)", c.ID, c.Status)
	}
	score := RBestScore(c)
	if score >= 1.0 {
		return nil
	}
	if score <= rBestBefore {
		return &SkillGateRejection{CandidateID: c.ID, Score: score, Best: rBestBefore}
	}
	return nil
}

// RBestScore is the story 03 R_best scoring function: a minimal deterministic
// heuristic over the candidate's own fields (full body, diff, motivating
// patterns, body substance), rounded to 4 decimals and capped at 1.0 so
// scores compare exactly. Full eval-store/scorer versioning is story 11,
// which plugs in here and bumps SkillAuditScorerVersion — the gate compare
// and the audit shape stay untouched.
// TODO(story-11): versioned scorer + eval store.
func RBestScore(c *SkillCandidate) float64 {
	if c == nil {
		return 0
	}
	score := 0.0
	if strings.TrimSpace(c.ProposedBody) != "" {
		score += 0.4
	}
	if strings.TrimSpace(c.Diff) != "" {
		score += 0.2
	}
	if n := len(c.PatternSlugs); n > 3 {
		score += 0.3
	} else {
		score += 0.1 * float64(n)
	}
	if n := len(c.ProposedBody); n > 0 {
		if bonus := float64(n) / 100000.0; bonus > 0.1 {
			score += 0.1
		} else {
			score += bonus
		}
	}
	if score > 1 {
		score = 1
	}
	return math.Round(score*10000) / 10000
}

// PromoteSkillCandidate writes the candidate's proposed body as a new version of the
// parent skill and links the two (candidate records the parent version it was cut from
// and the result version it produced; the skill's edit summary names the candidate).
// Promotion is the one write path that may change a locked skill: it runs through
// saveArticleLocked directly, bypassing the agent-edit lock guard. The gate decision
// is audited first (story 03): an accept appends an accepted record and proceeds, a
// gate rejection appends a rejected record, marks the candidate rejected, and refuses.
// A gate accept is also the training event (story 06): the same save stamps the
// harness-managed trained-wikiskill marker plus its metadata, so promotion and
// (re-)training cannot disagree.
func (s *Storage) PromoteSkillCandidate(id string) (*Article, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	c, err := s.GetSkillCandidate(id)
	if err != nil {
		return nil, err
	}
	if c.Status != SkillCandidatePending {
		return nil, fmt.Errorf("skill candidate '%s' is already decided (%s)", c.ID, c.Status)
	}
	parent, err := s.GetArticle(c.ParentSlug)
	if err != nil {
		return nil, fmt.Errorf("cannot promote candidate '%s': parent skill not found: %s", c.ID, c.ParentSlug)
	}
	if parent.Type != ContentTypeSkill {
		return nil, fmt.Errorf("cannot promote candidate '%s': parent '%s' is no longer a skill", c.ID, c.ParentSlug)
	}
	// Structural validation is intentionally unaudited: only gate decisions
	// (accept/reject) append audit records, so this diff-only refusal returns
	// before any gate/audit work.
	if strings.TrimSpace(c.ProposedBody) == "" {
		return nil, fmt.Errorf("cannot promote candidate '%s': diff-only candidates need a full proposed body to promote", c.ID)
	}

	// Story 03 gate: score against the running best from the harness-owned
	// trail, then audit the decision — accept or reject — before mutating
	// anything. The audit append comes first so a decision can never land
	// without its record; a failed append aborts the promotion.
	// TODO(story-04): run promotion through the job/lease/BYO-CLI runner instead of inline.
	score := RBestScore(c)
	before, err := s.currentRBest(c.ParentSlug)
	if err != nil {
		return nil, fmt.Errorf("cannot promote candidate '%s': failed to read audit trail: %w", c.ID, err)
	}
	after := before
	outcome := SkillAuditAccepted
	if gateErr := ValidateSkillCandidateForPromote(c, parent, before); gateErr != nil {
		after = before
		outcome = SkillAuditRejected
		if auditErr := s.appendSkillAuditRecord(SkillAuditRecord{
			CandidateID: c.ID, SkillSlug: c.ParentSlug, ParentVersion: c.ParentVersion,
			ContentHash: SkillCandidateContentHash(c), ValidationScore: score,
			RBestBefore: before, RBestAfter: after, Outcome: outcome,
			Decider: SkillAuditDeciderGate, ScorerVersion: SkillAuditScorerVersion,
			Timestamp: time.Now().UTC(),
		}); auditErr != nil {
			return nil, fmt.Errorf("validation gate refused candidate '%s' but the audit write failed: %v (gate: %v)", c.ID, auditErr, gateErr)
		}
		c.Status = SkillCandidateRejected
		c.DecidedAt = time.Now().UTC()
		if err := s.writeCandidate(c); err != nil {
			return nil, err
		}
		return nil, gateErr
	}
	after = score
	stampAt := time.Now().UTC()
	if auditErr := s.appendSkillAuditRecord(SkillAuditRecord{
		CandidateID: c.ID, SkillSlug: c.ParentSlug, ParentVersion: c.ParentVersion,
		ContentHash: SkillCandidateContentHash(c), ValidationScore: score,
		RBestBefore: before, RBestAfter: after, Outcome: outcome,
		Decider: SkillAuditDeciderGate, ScorerVersion: SkillAuditScorerVersion,
		Timestamp: stampAt,
	}); auditErr != nil {
		return nil, fmt.Errorf("validation gate accepted candidate '%s' but the audit write failed: %v", c.ID, auditErr)
	}
	c.BestScore = score

	// Story 06: the gate accept IS the training event. Stamp the trained marker
	// (tag + trained_at/version/parent/candidate/val_score/eval_hash) in the same
	// save that writes the promoted body, so the marker always names the version
	// carrying it and re-training overwrites it. The eval hash is the skill's
	// most recent accepted eval upload — the same baseline the staleness check
	// later compares against — or empty when the skill has never had eval data.
	evalHash, err := s.latestEvalHashForSkill(parent.Slug)
	if err != nil {
		return nil, fmt.Errorf("failed to promote skill candidate '%s': failed to read eval history: %w", c.ID, err)
	}
	summary := fmt.Sprintf("Promoted skill candidate %s (parent %s v%d, proposed by %s)",
		c.ID, c.ParentSlug, c.ParentVersion, c.Proposer)
	art, err := s.saveArticleLocked(parent.Slug, parent.Title, c.ProposedBody, parent.Description,
		parent.Source, parent.Resource, summary, parent.Tags, parent.Type, ArticleOverrides{
			TrainedStamp: &TrainedStamp{
				At:            stampAt,
				ParentVersion: c.ParentVersion,
				CandidateID:   c.ID,
				ValScore:      score,
				EvalHash:      evalHash,
			},
		})
	if err != nil {
		return nil, fmt.Errorf("failed to promote skill candidate '%s': %w", c.ID, err)
	}

	c.Status = SkillCandidatePromoted
	c.ResultVersion = art.Version
	c.DecidedAt = time.Now().UTC()
	if err := s.writeCandidate(c); err != nil {
		return nil, err
	}
	return art, nil
}

// RejectSkillCandidate marks a candidate rejected. The live skill is never touched —
// callers can verify byte-identity by comparing content before and after. The
// explicit human decision is audited (story 03, decider "human") before the
// candidate is marked, so a rejection can never land without its record.
func (s *Storage) RejectSkillCandidate(id string) (*SkillCandidate, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	c, err := s.GetSkillCandidate(id)
	if err != nil {
		return nil, err
	}
	if c.Status != SkillCandidatePending {
		return nil, fmt.Errorf("skill candidate '%s' is already decided (%s)", c.ID, c.Status)
	}
	score := RBestScore(c)
	before, err := s.currentRBest(c.ParentSlug)
	if err != nil {
		return nil, fmt.Errorf("cannot reject candidate '%s': failed to read audit trail: %w", c.ID, err)
	}
	if auditErr := s.appendSkillAuditRecord(SkillAuditRecord{
		CandidateID: c.ID, SkillSlug: c.ParentSlug, ParentVersion: c.ParentVersion,
		ContentHash: SkillCandidateContentHash(c), ValidationScore: score,
		RBestBefore: before, RBestAfter: before, Outcome: SkillAuditRejected,
		Decider: SkillAuditDeciderHuman, ScorerVersion: SkillAuditScorerVersion,
		Timestamp: time.Now().UTC(),
	}); auditErr != nil {
		return nil, fmt.Errorf("cannot reject candidate '%s': audit write failed: %v", c.ID, auditErr)
	}
	c.Status = SkillCandidateRejected
	c.DecidedAt = time.Now().UTC()
	if err := s.writeCandidate(c); err != nil {
		return nil, err
	}
	return c, nil
}

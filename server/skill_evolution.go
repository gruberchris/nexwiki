package server

import (
	"encoding/json"
	"fmt"
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
// Deliberately NOT built here (stories 03/04 own them): the validation gate, R_best
// scoring, skill-impact/logs audit writes, and the job/lease/BYO-CLI runner. Each has a
// stub or a TODO hook point below so later stories have a clean seam to attach to.

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

// ValidateSkillCandidateForPromote is the hook point for the story 03 validation gate.
// It currently approves every pending candidate with a well-formed parent; story 03
// replaces this stub with real validation scoring without touching the promote flow.
func ValidateSkillCandidateForPromote(c *SkillCandidate, parent *Article) error {
	// TODO(story-03): implement the validation gate here (checks + scoring threshold).
	// Until then every structurally valid candidate is promotable.
	if c.Status != SkillCandidatePending {
		return fmt.Errorf("skill candidate '%s' is already decided (%s)", c.ID, c.Status)
	}
	return nil
}

// RBestScore is the hook point for the story 03 R_best scoring function. It is recorded
// on the candidate at promote time but does not gate promotion until story 03 lands.
func RBestScore(c *SkillCandidate) float64 {
	// TODO(story-03): implement R_best scoring (pattern-match strength, proposer history).
	return 0
}

// PromoteSkillCandidate writes the candidate's proposed body as a new version of the
// parent skill and links the two (candidate records the parent version it was cut from
// and the result version it produced; the skill's edit summary names the candidate).
// Promotion is the one write path that may change a locked skill: it runs through
// saveArticleLocked directly, bypassing the agent-edit lock guard.
func (s *Storage) PromoteSkillCandidate(id string) (*Article, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	c, err := s.GetSkillCandidate(id)
	if err != nil {
		return nil, err
	}
	parent, err := s.GetArticle(c.ParentSlug)
	if err != nil {
		return nil, fmt.Errorf("cannot promote candidate '%s': parent skill not found: %s", c.ID, c.ParentSlug)
	}
	if parent.Type != ContentTypeSkill {
		return nil, fmt.Errorf("cannot promote candidate '%s': parent '%s' is no longer a skill", c.ID, c.ParentSlug)
	}
	if err := ValidateSkillCandidateForPromote(c, parent); err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.ProposedBody) == "" {
		return nil, fmt.Errorf("cannot promote candidate '%s': diff-only candidates need a full proposed body to promote", c.ID)
	}

	// TODO(story-03): write the skill-impact/logs audit-trail entries for this promotion.
	// TODO(story-04): run promotion through the job/lease/BYO-CLI runner instead of inline.
	c.BestScore = RBestScore(c)

	summary := fmt.Sprintf("Promoted skill candidate %s (parent %s v%d, proposed by %s)",
		c.ID, c.ParentSlug, c.ParentVersion, c.Proposer)
	art, err := s.saveArticleLocked(parent.Slug, parent.Title, c.ProposedBody, parent.Description,
		parent.Source, parent.Resource, summary, parent.Tags, parent.Type, ArticleOverrides{})
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
// callers can verify byte-identity by comparing content before and after.
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
	c.Status = SkillCandidateRejected
	c.DecidedAt = time.Now().UTC()
	if err := s.writeCandidate(c); err != nil {
		return nil, err
	}
	return c, nil
}

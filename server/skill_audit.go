package server

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file holds story 03 of the wikiskill evolution support plan: the
// harness-owned audit trail for skill evolution.
//
// Every validation gate decision — accept or reject — appends one complete
// SkillAuditRecord carrying the candidate ID, target skill slug, parent
// version, diff-or-body hash, validation score, R_best before/after, the
// accept/reject outcome, the decider identity (gate or human), and a
// timestamp. Story 06 adds the third outcome, "revoked", for the trained
// marker's withdrawal paths (rollback and human unlock); those records carry a
// Reason. Records with a missing field are refused by validate, so a partial
// record can never be written.
//
// OWNERSHIP: the append path (appendSkillAuditRecord) is unexported and the
// only callers are the harness paths in skill_evolution.go
// (PromoteSkillCandidate, RejectSkillCandidate). There is deliberately NO MCP
// tool that writes audit records directly, so agent roles cannot forge score
// appends by construction — there is no agent path to them. The one MCP path
// that triggers audit writes, promote_skill_candidate, requires
// skills.promote + audit.write, which story 02 grants to no phase: promotion
// stays an operator/harness action. The story-02 scope gate refuses all three
// agent roles there with an explicit scope error, exactly like the
// auditScopeDenial pattern for activity-log writes.
//
// STORAGE: records live as JSON Lines in data/skill_audit.jsonl, mirroring
// the activity log (activity.jsonl) and the story 01 candidate convention
// (flat JSON under data/). The file is append-only from every agent path's
// perspective: nothing in the MCP surface reads, edits, deletes, or rewrites
// it. ListSkillAuditRecords is operator-side verification (tests, story 04
// runner tooling), not a new MCP tool — the new tool surface stays minimal
// per the docs-integrity rule.

// Skill audit outcomes.
const (
	SkillAuditAccepted = "accepted"
	SkillAuditRejected = "rejected"
	// SkillAuditRevoked marks a story-06 marker withdrawal: a rollback (the
	// trained metadata is kept and the revocation reason recorded) or an
	// explicit human unlock (the marker cleared). Both are human decisions and
	// both must land with a Reason.
	SkillAuditRevoked = "revoked"
)

// Skill audit deciders: the validation gate, or an explicit human/operator
// rejection. The gate decides promotions (accept and gate-reject alike); a
// direct RejectSkillCandidate call is always a human decision.
const (
	SkillAuditDeciderGate  = "gate"
	SkillAuditDeciderHuman = "human"
)

// SkillAuditScorerVersion stamps the scoring function that produced the
// validation score. The story 03 gate is a minimal deterministic heuristic;
// full eval-store/scorer versioning is story 11, which bumps this version.
// TODO(story-11): version the scorer and eval store; record the version here.
const SkillAuditScorerVersion = "v0"

// SkillAuditFilename is the append-only audit file inside the data directory.
const SkillAuditFilename = "skill_audit.jsonl"

// SkillAuditRecord is one harness-owned gate decision. All fields are
// required: validate refuses a record with any of them missing, so a
// missing-field record is impossible by construction on the write path.
type SkillAuditRecord struct {
	CandidateID     string  `json:"candidate_id"`
	SkillSlug       string  `json:"skill_slug"`
	ParentVersion   int     `json:"parent_version"`
	ContentHash     string  `json:"content_hash"`
	ValidationScore float64 `json:"validation_score"`
	RBestBefore     float64 `json:"r_best_before"`
	RBestAfter      float64 `json:"r_best_after"`
	Outcome         string  `json:"outcome"`
	Decider         string  `json:"decider"`
	ScorerVersion   string  `json:"scorer_version"`
	// Reason is required on a revoked record (story 06 rollback / unlock) and
	// empty on the gate's accept/reject decisions, which need no free-text why.
	Reason    string    `json:"reason,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// validate refuses a record with any required field missing or out of range.
func (r SkillAuditRecord) validate() error {
	if r.CandidateID == "" {
		return fmt.Errorf("skill audit record refuses write: candidate id is required")
	}
	if r.SkillSlug == "" {
		return fmt.Errorf("skill audit record refuses write: skill slug is required")
	}
	if r.ParentVersion <= 0 {
		return fmt.Errorf("skill audit record refuses write: parent version is required")
	}
	if r.ContentHash == "" {
		return fmt.Errorf("skill audit record refuses write: content hash is required")
	}
	if r.ValidationScore < 0 || r.ValidationScore > 1 {
		return fmt.Errorf("skill audit record refuses write: validation score %.4f out of range [0,1]", r.ValidationScore)
	}
	if r.RBestBefore < 0 || r.RBestBefore > 1 {
		return fmt.Errorf("skill audit record refuses write: R_best before %.4f out of range [0,1]", r.RBestBefore)
	}
	if r.RBestAfter < 0 || r.RBestAfter > 1 {
		return fmt.Errorf("skill audit record refuses write: R_best after %.4f out of range [0,1]", r.RBestAfter)
	}
	switch r.Outcome {
	case SkillAuditAccepted, SkillAuditRejected:
	case SkillAuditRevoked:
		if strings.TrimSpace(r.Reason) == "" {
			return fmt.Errorf("skill audit record refuses write: a %q record requires a reason", r.Outcome)
		}
	default:
		return fmt.Errorf("skill audit record refuses write: outcome must be %q, %q, or %q, got %q",
			SkillAuditAccepted, SkillAuditRejected, SkillAuditRevoked, r.Outcome)
	}
	if r.Decider != SkillAuditDeciderGate && r.Decider != SkillAuditDeciderHuman {
		return fmt.Errorf("skill audit record refuses write: decider must be %q or %q, got %q",
			SkillAuditDeciderGate, SkillAuditDeciderHuman, r.Decider)
	}
	if r.ScorerVersion == "" {
		return fmt.Errorf("skill audit record refuses write: scorer version is required")
	}
	if r.Timestamp.IsZero() {
		return fmt.Errorf("skill audit record refuses write: timestamp is required")
	}
	return nil
}

// SkillAuditPath returns the canonical audit file location for a data directory.
func SkillAuditPath(dataDir string) string {
	return filepath.Join(dataDir, SkillAuditFilename)
}

// SkillCandidateContentHash is the diff-or-body hash recorded on every audit
// entry: SHA-256 over the diff and the full proposed body, separator-delimited
// so a diff ending where a body begins cannot collide with another pair.
func SkillCandidateContentHash(c *SkillCandidate) string {
	if c == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(c.Diff + "\x00" + c.ProposedBody))
	return hex.EncodeToString(sum[:])
}

// appendSkillAuditRecord validates and appends one record. Unexported on
// purpose: only the harness paths (promote/reject) may write the trail, never
// an agent-facing handler. Callers hold Storage.writeMu, like writeCandidate.
func (s *Storage) appendSkillAuditRecord(rec SkillAuditRecord) error {
	if err := rec.validate(); err != nil {
		return err
	}
	path := SkillAuditPath(s.DataDir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open skill audit log: %w", err)
	}
	defer func() { _ = f.Close() }()
	raw, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("failed to encode skill audit record: %w", err)
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("failed to append skill audit record: %w", err)
	}
	return nil
}

// ListSkillAuditRecords returns every audit record, oldest first, skipping
// corrupt lines the way the activity-log read path does. Operator-side
// verification (and the story 04 runner seam); not exposed as an MCP tool.
func (s *Storage) ListSkillAuditRecords() ([]SkillAuditRecord, error) {
	path := SkillAuditPath(s.DataDir)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []SkillAuditRecord{}, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []SkillAuditRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec SkillAuditRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // skip corrupt lines, like the activity log reader
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []SkillAuditRecord{}
	}
	return out, nil
}

// currentRBest returns the running best validation score for a skill: the max
// RBestAfter across its audit records, 0 when the skill has no history yet.
// Deterministic, derived only from the harness-owned trail — never from a
// caller-supplied number, so no agent path can inflate the threshold.
// A trail read failure is propagated so the gate aborts instead of promoting
// against a bogus 0 baseline.
func (s *Storage) currentRBest(skillSlug string) (float64, error) {
	records, err := s.ListSkillAuditRecords()
	if err != nil {
		return 0, err
	}
	best := 0.0
	for _, rec := range records {
		if rec.SkillSlug == skillSlug && rec.RBestAfter > best {
			best = rec.RBestAfter
		}
	}
	return best, nil
}

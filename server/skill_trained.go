package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// This file holds story 06 of the wikiskill evolution support plan: the
// harness-managed trained marker, the trained-state picker, and the rollback
// path (Phase C1 + C4).
//
// THE MARKER: when a validation gate accepts a candidate, PromoteSkillCandidate
// stamps the promoted skill with a `trained-wikiskill` marker tag plus
// front-matter metadata (trained_at, trained_version, trained_parent_version,
// trained_candidate, trained_val_score, trained_eval_hash) in the same save
// that writes the promoted body — one version, one write, so the marker can
// never name a version whose body does not match. Re-training overwrites the
// metadata; a rollback keeps it and records a revocation; an explicit human
// unlock clears it. The metadata is never silently deleted.
//
// PROTECTION: the marker tag is tool-managed like the memory-<scope> tag. Two
// layers enforce it. At the agent-facing layer, validateAndCleanUserTags
// (handlers.go) preserves an existing marker tag on any skill tag edit and
// strips a forged one. At the storage choke point, saveArticleLocked re-asserts
// a marker tag a write path tried to drop, and only TrainedUnlock — the
// explicit human unlock, which appends its own audit record — may remove it.
// DeleteTagGlobally refuses the tag outright. Agent-facing writes to a LOCKED
// skill are refused wholesale by the story-01 lock; these layers are what hold
// for trained skills that are not locked.
//
// STALENESS is derived, never stored: a marker whose skill was edited after
// training (current version differs from trained_version), whose eval set was
// re-uploaded after training (latest stored eval hash differs from
// trained_eval_hash), or whose tag and metadata disagree is reported stale with
// a reason — flagged, still usable. Revocation reads as staleness with its
// recorded reason.
//
// NOT BUILT HERE (later stories own these): the wizard UI and badge render
// (story 08), the Result report (story 09), eval-store versioning (story 11).

// TrainedMarkerTag is the tool-managed marker tag stamped on a skill whose
// candidate passed the validation gate. Agents cannot forge it onto an
// untrained skill and cannot strip it from a trained one.
const TrainedMarkerTag = "trained-wikiskill"

// The three trained states the picker reports. There is deliberately no
// separate "revoked" state: a revoked marker is a stale marker whose reason
// says so, keeping the vocabulary exactly the three the wizard picks from.
const (
	TrainedStateUntrained = "untrained"
	TrainedStateTrained   = "trained"
	TrainedStateStale     = "stale"
)

// TrainedStamp stamps a fresh trained marker as part of the version a save
// writes. The promote path leaves VersionOverride zero so the marker records
// the promotion version itself; the bundle importer passes the exported value
// through so an export/import round trip preserves the marker exactly.
type TrainedStamp struct {
	At              time.Time
	ParentVersion   int
	CandidateID     string
	ValScore        float64
	EvalHash        string
	VersionOverride int
}

// TrainedRevoke records a marker revocation (a rollback) against the metadata
// already on the skill. The trained history fields are untouched.
type TrainedRevoke struct {
	At     time.Time
	Reason string
}

// ensureTrainedMarkerTag returns tags with the marker tag asserted, deduped
// case-insensitively. It always copies so a caller's tag slice is never
// mutated through a shared backing array.
func ensureTrainedMarkerTag(tags []string) []string {
	if hasTag(tags, TrainedMarkerTag) {
		return tags
	}
	out := make([]string, len(tags), len(tags)+1)
	copy(out, tags)
	return append(out, TrainedMarkerTag)
}

// skillContentHash is the SHA-256 content hash recorded on revocation and
// unlock audit records: what was on the skill at the moment its marker was
// withdrawn. Same convention as the story-03 candidate hash (SHA-256 hex).
func skillContentHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// latestEvalHashesBySkill maps each skill slug to the eval hash of its most
// recent accepted eval upload, across every job that ever uploaded one (active
// or terminal — an uploaded eval set is the skill's training data of record
// regardless of what the job went on to do). Deterministic: ties on upload
// time keep the first job in the oldest-first listing.
func (s *Storage) latestEvalHashesBySkill() (map[string]string, error) {
	jobs, err := s.ListEvolutionJobs()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	stamped := make(map[string]time.Time)
	for _, j := range jobs {
		if j.EvalHash == "" || j.SkillSlug == "" {
			continue
		}
		at := j.EvalUploadedAt
		if at.IsZero() {
			at = j.CreatedAt
		}
		if prev, ok := stamped[j.SkillSlug]; ok && !at.After(prev) {
			continue
		}
		out[j.SkillSlug] = j.EvalHash
		stamped[j.SkillSlug] = at
	}
	return out, nil
}

// latestEvalHashForSkill resolves the current stored eval hash for one skill —
// the same value the staleness comparison uses, so a marker is stamped with
// exactly the baseline it will later be checked against.
func (s *Storage) latestEvalHashForSkill(skillSlug string) (string, error) {
	hashes, err := s.latestEvalHashesBySkill()
	if err != nil {
		return "", err
	}
	return hashes[skillSlug], nil
}

// SkillTrainedState is the derived trained state of one skill. Every field is
// computed from the skill document plus the stored eval data — nothing here is
// authoritative on its own, which is what makes the state tamper-evident.
type SkillTrainedState struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`

	TrainedAt            time.Time `json:"trained_at,omitzero"`
	TrainedVersion       int       `json:"trained_version,omitempty"`
	TrainedParentVersion int       `json:"trained_parent_version,omitempty"`
	TrainedCandidate     string    `json:"trained_candidate,omitempty"`
	TrainedValScore      float64   `json:"trained_val_score,omitempty"`
	TrainedEvalHash      string    `json:"trained_eval_hash,omitempty"`
	RevokedAt            time.Time `json:"revoked_at,omitzero"`
	RevokedReason        string    `json:"revoked_reason,omitempty"`

	// CurrentVersion/CurrentEvalHash are what the marker was compared against,
	// so a stale reason is checkable by the consumer instead of taken on faith.
	CurrentVersion  int    `json:"current_version"`
	CurrentEvalHash string `json:"current_eval_hash,omitempty"`
}

// SkillTrainedStateEntry is one picker row: the skill's identity plus its
// derived trained state.
type SkillTrainedStateEntry struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	SkillTrainedState
}

// deriveSkillTrainedState computes the picker state for one skill against the
// skill's current stored eval hash.
func deriveSkillTrainedState(art *Article, currentEvalHash string) SkillTrainedState {
	st := SkillTrainedState{
		State:                TrainedStateUntrained,
		TrainedAt:            art.TrainedAt,
		TrainedVersion:       art.TrainedVersion,
		TrainedParentVersion: art.TrainedParentVersion,
		TrainedCandidate:     art.TrainedCandidate,
		TrainedValScore:      art.TrainedValScore,
		TrainedEvalHash:      art.TrainedEvalHash,
		RevokedAt:            art.TrainedRevokedAt,
		RevokedReason:        art.TrainedRevokedReason,
		CurrentVersion:       art.Version,
		CurrentEvalHash:      currentEvalHash,
	}

	markerTag := hasTag(art.Tags, TrainedMarkerTag)
	hasMetadata := !art.TrainedAt.IsZero() || art.TrainedVersion > 0
	if !markerTag && !hasMetadata {
		return st // untrained, no reason
	}

	var reasons []string
	if hasMetadata && !markerTag {
		reasons = append(reasons, "trained metadata is present but the trained-wikiskill marker tag is missing (hand-edited)")
	}
	if markerTag && !hasMetadata {
		reasons = append(reasons, "trained-wikiskill marker tag is present without trained metadata (hand-edited)")
	}
	if !art.TrainedRevokedAt.IsZero() {
		revoked := fmt.Sprintf("marker revoked %s", art.TrainedRevokedAt.UTC().Format(time.RFC3339))
		if strings.TrimSpace(art.TrainedRevokedReason) != "" {
			revoked += ": " + strings.TrimSpace(art.TrainedRevokedReason)
		}
		reasons = append(reasons, revoked)
	}
	if art.TrainedVersion > 0 && art.Version != art.TrainedVersion {
		reasons = append(reasons, fmt.Sprintf("skill changed after training: trained at v%d, now v%d", art.TrainedVersion, art.Version))
	}
	if currentEvalHash != "" && currentEvalHash != art.TrainedEvalHash {
		reasons = append(reasons, fmt.Sprintf("eval set changed after training (trained on %.12s, current %.12s)", art.TrainedEvalHash, currentEvalHash))
	}
	if len(reasons) > 0 {
		st.State = TrainedStateStale
		st.Reason = strings.Join(reasons, "; ")
	} else {
		st.State = TrainedStateTrained
	}
	return st
}

// ListSkillTrainedStates returns the picker rows for every skill, ordered by
// title so the wizard renders a stable list.
func (s *Storage) ListSkillTrainedStates() ([]SkillTrainedStateEntry, error) {
	evalHashes, err := s.latestEvalHashesBySkill()
	if err != nil {
		return nil, err
	}
	articles, err := s.ListArticles()
	if err != nil {
		return nil, err
	}
	var out []SkillTrainedStateEntry
	for _, meta := range articles {
		if meta.Type != ContentTypeSkill {
			continue
		}
		art, err := s.GetArticle(meta.Slug)
		if err != nil {
			continue // unparseable skills drop out, as in every other listing
		}
		out = append(out, SkillTrainedStateEntry{
			Slug:              art.Slug,
			Title:             art.Title,
			SkillTrainedState: deriveSkillTrainedState(art, evalHashes[art.Slug]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	if out == nil {
		out = []SkillTrainedStateEntry{}
	}
	return out, nil
}

// GetSkillTrainedState resolves the trained state for one skill.
func (s *Storage) GetSkillTrainedState(slug string) (*SkillTrainedStateEntry, error) {
	art, err := s.GetArticle(slug)
	if err != nil {
		return nil, err
	}
	if art.Type != ContentTypeSkill {
		return nil, fmt.Errorf("'%s' is not a Custom AI Skill (type must be AI-Agent-Skill)", art.Slug)
	}
	currentEvalHash, err := s.latestEvalHashForSkill(art.Slug)
	if err != nil {
		return nil, err
	}
	return &SkillTrainedStateEntry{
		Slug:              art.Slug,
		Title:             art.Title,
		SkillTrainedState: deriveSkillTrainedState(art, currentEvalHash),
	}, nil
}

// RollbackTrainedSkill restores the skill body the trained candidate was cut
// from (trained_parent_version, falling back to the version before the
// trained one) and records the marker as revoked with the caller's reason —
// never deleted. The trained metadata, the marker tag, and the audit trail all
// survive the rollback, so the training history stays readable. Operator-side
// by design: it runs through saveArticleLocked directly, bypassing the skill
// lock the way promotion and unlock do.
func (s *Storage) RollbackTrainedSkill(slug, reason string) (*Article, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("a rollback reason is required: the revocation is recorded with it")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	art, err := s.GetArticle(slug)
	if err != nil {
		return nil, err
	}
	if art.Type != ContentTypeSkill {
		return nil, fmt.Errorf("cannot roll back '%s': not a Custom AI Skill (type must be AI-Agent-Skill)", art.Slug)
	}
	if art.TrainedVersion <= 0 {
		return nil, fmt.Errorf("skill '%s' has no trained marker to roll back", art.Slug)
	}
	if active, err := s.activeEvolutionJobForSkill(art.Slug); err != nil {
		return nil, err
	} else if active != nil {
		return nil, fmt.Errorf("cannot roll back skill '%s' while evolution job '%s' is %s — cancel or finish it first", art.Slug, active.ID, active.Status)
	}

	target := art.TrainedParentVersion
	if target <= 0 {
		target = art.TrainedVersion - 1
	}
	if target <= 0 {
		return nil, fmt.Errorf("cannot roll back skill '%s': no pre-training version to restore", art.Slug)
	}
	if target >= art.Version {
		return nil, fmt.Errorf("cannot roll back skill '%s': restore target v%d is not before the live version v%d", art.Slug, target, art.Version)
	}
	histArt, err := s.GetArticleVersion(art.Slug, target)
	if err != nil {
		return nil, fmt.Errorf("cannot roll back skill '%s': %w", art.Slug, err)
	}

	// Audit first, like every other skill decision: a revocation can never land
	// without its record. The hash names the trained body being withdrawn, so the
	// trail shows exactly what the rollback removed.
	before, err := s.currentRBest(art.Slug)
	if err != nil {
		return nil, fmt.Errorf("cannot roll back skill '%s': failed to read audit trail: %w", art.Slug, err)
	}
	if auditErr := s.appendSkillAuditRecord(SkillAuditRecord{
		CandidateID:     auditCandidateRef(art.TrainedCandidate, "trained-marker-revoke"),
		SkillSlug:       art.Slug,
		ParentVersion:   art.TrainedVersion,
		ContentHash:     skillContentHash(art.Content),
		ValidationScore: art.TrainedValScore,
		RBestBefore:     before,
		RBestAfter:      before,
		Outcome:         SkillAuditRevoked,
		Decider:         SkillAuditDeciderHuman,
		ScorerVersion:   SkillAuditScorerVersion,
		Reason:          reason,
		Timestamp:       time.Now().UTC(),
	}); auditErr != nil {
		return nil, fmt.Errorf("cannot roll back skill '%s': audit write failed: %v", art.Slug, auditErr)
	}

	// One save restores the pre-training body AND revokes the marker: the new
	// version carries the untrained body with the revoked marker on it. Title,
	// description, source, and live tags are kept — a rollback is about the body,
	// and the marker tag must survive it.
	summary := fmt.Sprintf("Rolled back trained skill to pre-training v%d; trained marker revoked: %s", target, reason)
	return s.saveArticleLocked(art.Slug, art.Title, histArt.Content, art.Description, art.Source, art.Resource,
		summary, art.Tags, art.Type, ArticleOverrides{
			TrainedRevoke: &TrainedRevoke{At: time.Now().UTC(), Reason: reason},
		})
}

// UnlockTrainedMarker is the explicit human removal path for the trained
// marker: the tag and every trained_* field are cleared in one save, and the
// removal lands in the skill audit trail first. Re-training updates the marker
// instead; this is for withdrawing it outright.
func (s *Storage) UnlockTrainedMarker(slug, reason string) (*Article, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("an unlock reason is required: the removal is recorded with it")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	art, err := s.GetArticle(slug)
	if err != nil {
		return nil, err
	}
	if art.Type != ContentTypeSkill {
		return nil, fmt.Errorf("cannot unlock trained marker on '%s': not a Custom AI Skill (type must be AI-Agent-Skill)", art.Slug)
	}
	if !hasTag(art.Tags, TrainedMarkerTag) && art.TrainedAt.IsZero() && art.TrainedVersion <= 0 {
		return nil, fmt.Errorf("skill '%s' has no trained marker to unlock", art.Slug)
	}

	// Audit first: the removal can never land without its record. The trained
	// values are read before the save wipes them, so the record carries what was
	// removed; the reason is prefixed "unlock" to separate it from a rollback's
	// metadata-preserving revocation.
	parentVersion := art.TrainedVersion
	if parentVersion <= 0 {
		parentVersion = art.Version
	}
	before, err := s.currentRBest(art.Slug)
	if err != nil {
		return nil, fmt.Errorf("cannot unlock trained marker on '%s': failed to read audit trail: %w", art.Slug, err)
	}
	if auditErr := s.appendSkillAuditRecord(SkillAuditRecord{
		CandidateID:     auditCandidateRef(art.TrainedCandidate, "trained-marker-unlock"),
		SkillSlug:       art.Slug,
		ParentVersion:   parentVersion,
		ContentHash:     skillContentHash(art.Content),
		ValidationScore: art.TrainedValScore,
		RBestBefore:     before,
		RBestAfter:      before,
		Outcome:         SkillAuditRevoked,
		Decider:         SkillAuditDeciderHuman,
		ScorerVersion:   SkillAuditScorerVersion,
		Reason:          "unlock: " + reason,
		Timestamp:       time.Now().UTC(),
	}); auditErr != nil {
		return nil, fmt.Errorf("cannot unlock trained marker on '%s': audit write failed: %v", art.Slug, auditErr)
	}

	tags := make([]string, 0, len(art.Tags))
	for _, t := range art.Tags {
		if strings.EqualFold(strings.TrimSpace(t), TrainedMarkerTag) {
			continue
		}
		tags = append(tags, t)
	}
	summary := fmt.Sprintf("Unlocked trained marker (removed with audit): %s", reason)
	return s.saveArticleLocked(art.Slug, art.Title, art.Content, art.Description, art.Source, art.Resource,
		summary, tags, art.Type, ArticleOverrides{TrainedUnlock: true})
}

// auditCandidateRef fills the audit record's candidate field: the trained
// candidate when the marker carries one, otherwise a synthetic reference that
// names the marker event. The audit validator requires a non-empty ID, and an
// event record must not invent a candidate that does not exist.
func auditCandidateRef(trainedCandidate, fallback string) string {
	if ref := strings.TrimSpace(trainedCandidate); ref != "" {
		return ref
	}
	return fallback
}

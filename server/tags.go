package server

import (
	"fmt"
	"sort"
	"strings"
)

// PlanStatusTags is the closed vocabulary of plan lifecycle states. Every AI-Agent-Plan document
// carries exactly one of these, enforced on every write path by ValidatePlanStatus. All but
// "archived" are reserved exclusively for plans; "archived" is shared wiki-wide — on a plan it is
// a lifecycle state reached automatically, on any other type it keeps its long-standing manual
// archive-then-delete semantics (see docs/tags.md).
var PlanStatusTags = []string{
	"draft",        // being written, or written and not yet started
	"implementing", // work has begun, not finished
	"blocked",      // started but stuck on an external dependency
	"completed",    // implementation finished; auto-archives after NEXWIKI_PLAN_ARCHIVE_AFTER_DAYS
	"superseded",   // terminal; the work moved to another plan; auto-archives like completed
	"parked",       // deliberately deferred; never auto-transitions — that exemption is its purpose
	"evergreen",    // a running backlog with no finish line; never auto-transitions
	"archived",     // retired, retained for reference; auto-deleted after NEXWIKI_PLAN_DELETE_AFTER_DAYS
}

// GeneralStatusTags are the lifecycle tags recognized on non-plan documents (wiki articles,
// memories, skills). "archived" appears in both lists deliberately — it is the sole shared member.
var GeneralStatusTags = []string{
	"done", "wip", "in-progress", "active", "todo",
	"pending", "review", "ready", "inbox", "archived",
}

// StatusTags is the union of both vocabularies, kept for display purposes: the home dashboard
// prioritizes any recognized status tag on a card, whatever the document type. Prefer the typed
// lists above for validation.
var StatusTags = buildStatusTagUnion()

func buildStatusTagUnion() []string {
	seen := make(map[string]bool)
	var union []string
	for _, list := range [][]string{PlanStatusTags, GeneralStatusTags} {
		for _, t := range list {
			if !seen[t] {
				seen[t] = true
				union = append(union, t)
			}
		}
	}
	sort.Strings(union)
	return union
}

// planExclusiveStatus reports whether a tag is a plan status that may not appear on non-plan
// documents. "archived" is the one shared member of the vocabulary and is exempt.
func planExclusiveStatus(tag string) bool {
	lower := strings.ToLower(tag)
	if lower == "archived" {
		return false
	}
	for _, s := range PlanStatusTags {
		if lower == s {
			return true
		}
	}
	return false
}

// planStatusesIn returns the plan status tags present in a tag list, lowercased, in the canonical
// PlanStatusTags order (so error messages and precedence decisions are deterministic).
func planStatusesIn(tags []string) []string {
	present := make(map[string]bool)
	for _, t := range tags {
		present[strings.ToLower(strings.TrimSpace(t))] = true
	}
	var found []string
	for _, s := range PlanStatusTags {
		if present[s] {
			found = append(found, s)
		}
	}
	return found
}

// ValidatePlanStatus enforces the plan status contract on a document's tag set:
//   - an AI-Agent-Plan must carry exactly one of the eight plan statuses — zero is as invalid as
//     two, because an untagged plan is invisible to the lifecycle worker and the dashboard alike;
//   - any other document type may not carry a plan-exclusive status ("archived" excepted, which
//     stays wiki-wide).
//
// It validates values only. Unusual transitions (e.g. draft straight to archived) are deliberately
// log-and-allow — strict transition enforcement would fight manual corrections in the editor.
func ValidatePlanStatus(docType string, tags []string) error {
	if normalizeType(docType) == ContentTypePlan {
		statuses := planStatusesIn(tags)
		switch len(statuses) {
		case 1:
			return nil
		case 0:
			return fmt.Errorf("a plan must carry exactly one status tag; got none. Recognized plan statuses: %s",
				strings.Join(PlanStatusTags, ", "))
		default:
			return fmt.Errorf("a plan must carry exactly one status tag; got %d (%s). Keep the one that is true and drop the rest",
				len(statuses), strings.Join(statuses, ", "))
		}
	}

	for _, t := range tags {
		if planExclusiveStatus(t) {
			return fmt.Errorf("tag '%s' is a plan lifecycle status, reserved for AI-Agent-Plan documents; use a general status tag instead (%s)",
				strings.ToLower(strings.TrimSpace(t)), strings.Join(GeneralStatusTags, ", "))
		}
	}
	return nil
}

// legalPlanTransitions is the designed state machine. It exists for logging, not enforcement:
// saveArticleLocked warns about a transition outside this map and applies it anyway, because a
// human correcting a mis-tagged plan in the editor must always win.
var legalPlanTransitions = map[string][]string{
	"draft":        {"implementing", "parked", "superseded", "evergreen"},
	"implementing": {"blocked", "completed", "parked", "superseded"},
	"blocked":      {"implementing", "parked", "superseded"},
	"parked":       {"draft", "implementing", "superseded"},
	"completed":    {"archived"},
	"superseded":   {"archived"},
	"archived":     {"draft", "implementing"}, // manual revival
	"evergreen":    {},                        // never transitions
}

// isLegalPlanTransition reports whether from → to is part of the designed lifecycle.
func isLegalPlanTransition(from, to string) bool {
	if from == to {
		return true
	}
	for _, t := range legalPlanTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// Content type discriminators. Every NexWiki document carries exactly one of these
// in its OKF `type` front-matter key. `Wiki` is the only value users/regular tooling
// may set; the three reserved AI-Agent-* values are assigned solely by the agent tools
// (create_agent_memory/_plan/_skill) and may never be reassigned to a non-reserved type.
const (
	ContentTypeWiki   = "Wiki"
	ContentTypeMemory = "AI-Agent-Memory"
	ContentTypePlan   = "AI-Agent-Plan"
	ContentTypeSkill  = "AI-Agent-Skill"
)

// normalizeType canonicalizes a free-form type string to one of the four content type constants,
// defaulting to ContentTypeWiki when empty or unrecognized.
func normalizeType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "ai-agent-memory":
		return ContentTypeMemory
	case "ai-agent-plan":
		return ContentTypePlan
	case "ai-agent-skill":
		return ContentTypeSkill
	case "wiki", "":
		return ContentTypeWiki
	default:
		return ContentTypeWiki
	}
}

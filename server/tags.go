package server

import (
	"fmt"
	"sort"
	"strings"
)

// Lifecycle status is a first-class front-matter field (`status`), not a tag.
//
// It used to be a tag, and that was the source of a long tail of awkwardness: a status is a
// single-valued enum with a state machine, while tags are an unordered folksonomy, so storing one
// inside the other forced "exactly one" counting, precedence tables to collapse duplicates, and a
// denylist to catch an agent writing `wip` when it meant `implementing`. A dedicated field makes
// the invalid states unrepresentable instead of merely detectable.
//
// Only two classes have a lifecycle at all — AI-Agent-Plan and AI-Agent-Skill — and each has a
// closed vocabulary. Wiki articles and agent memories have no status: they describe themselves
// with free tags, which NexWiki never validates, never reserves, and never strips.

// PlanStatusTags is the closed lifecycle vocabulary for AI-Agent-Plan documents. Every plan has
// exactly one. Timers act on completed, superseded, and archived; parked and evergreen are exempt
// by design.
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

// SkillStatusTags is the closed lifecycle vocabulary for AI-Agent-Skill documents: a skill is
// being written, usable, or retired. Unlike a plan, a skill may carry no status at all.
var SkillStatusTags = []string{
	"draft",    // being written or revised; not yet trustworthy to follow
	"ready",    // complete and safe for an agent to load and follow
	"archived", // retired, kept for reference
}

// Wiki articles and agent memories have **no lifecycle status at all**. The `status` field belongs
// to the two classes with a real lifecycle; those documents describe themselves with free tags,
// which NexWiki never validates, never strips, and never reserves.

// A memory has two independent axes, and only one of them is a tag.
//
//   - **scope** — how far the fact reaches. Any project or topic name, so an open vocabulary, so it
//     stays the tool-managed `memory-<scope>` tag it has always been.
//   - **kind** — what sort of fact it is. Four values and no more, so it is a field.
//
// The split follows the rule this file already learned the hard way with status (see above):
// closed vocabularies are fields, open vocabularies are tags. status_field_migration.go exists
// because that rule was discovered late.
//
// The kind axis was missing entirely, and its absence split the second brain in half. Every memory
// in the corpus was a technical fact about a system; the two categories that were absent — who the
// operator is, and corrections the operator has given about how to work — lived only in Claude
// Code's local memory directory, reachable by one client and invisible to every other agent on the
// MCP server. Adding the axis is what lets the two halves merge.

// MemoryKinds is the closed vocabulary for the `memory_kind` field on AI-Agent-Memory documents.
// The two axes are independent and the full cross-product is legal: a `feedback` memory may be
// scoped to a project or carry no scope at all.
var MemoryKinds = []string{
	"project",   // goals and constraints NOT derivable from the repo or its git history
	"reference", // pointers to external resources — dashboards, tickets, hosts, URLs
	"user",      // who the operator is: role, expertise, standing preferences
	"feedback",  // a correction the operator gave, plus why, plus how to apply it
}

// Two shapes to avoid, recorded here so the constraint outlives the plan that set it.
//
// NexWiki is single-tenant today, and a future version with user accounts is planned. `kind`
// survives that transition unchanged — what sort of fact a memory holds does not depend on how
// many people can read it — but *ownership* is a third axis, and it is deliberately not built now
// because it would have exactly one possible value. When accounts do land:
//
//  1. **Do not encode a subject into the kind value** (`user:chris`). That collapses two axes into
//     one string, which then has to be unpicked exactly when the schema is under most pressure.
//  2. **Do not overload `memory-<scope>` with per-person scopes** (`memory-user-chris`). Scope is
//     reach, not ownership. Conflating them makes every scope query ambiguous and puts an
//     access-control property into a free-form tag any agent can rewrite.
//
// Both are the cheap-looking move at the moment accounts arrive. The migration is otherwise
// unusually cheap: every `user` and `feedback` memory in a single-tenant wiki has exactly one
// subject, so the backfill is one value applied uniformly.

// NormalizeMemoryKind canonicalizes a raw kind value for storage and comparison.
func NormalizeMemoryKind(kind string) string {
	return strings.ToLower(strings.TrimSpace(kind))
}

// IsMemoryKind reports whether a value is in the closed vocabulary.
func IsMemoryKind(kind string) bool {
	kind = NormalizeMemoryKind(kind)
	for _, k := range MemoryKinds {
		if kind == k {
			return true
		}
	}
	return false
}

// ValidateMemoryKind checks the `memory_kind` field against the closed vocabulary.
//
// Only AI-Agent-Memory documents carry one; every other class is unconstrained and a value on one
// is ignored rather than policed, matching how ValidateStatus treats the classes with no lifecycle.
//
// `required` distinguishes the two callers. A create must supply a kind — that is the write gate,
// and the whole point of the axis is that it is answered at intake. Everything else passes an
// empty value through, because memories written before the field existed stay valid and are
// reported by wiki_health's unkinded_memories finding until they are classified by hand. That
// mirrors the unsourced_memories precedent exactly: close the intake, report the backlog, do not
// rewrite history.
func ValidateMemoryKind(docType string, kind string, required bool) error {
	if normalizeType(docType) != ContentTypeMemory {
		return nil
	}
	kind = NormalizeMemoryKind(kind)
	if kind == "" {
		if required {
			return fmt.Errorf("a memory must have a memory_kind; valid values: %s — "+
				"'project' for goals and constraints not derivable from the repo, "+
				"'reference' for pointers to external resources, "+
				"'user' for who the operator is, "+
				"'feedback' for a correction the operator gave",
				strings.Join(MemoryKinds, ", "))
		}
		return nil
	}
	if IsMemoryKind(kind) {
		return nil
	}
	return fmt.Errorf("'%s' is not a memory kind. Valid values: %s — do not invent new ones",
		kind, strings.Join(MemoryKinds, ", "))
}

// retiredStatusTagLabels are the words that used to be applied as *status tags* before status
// became a field. The one-time migration removes them from wiki articles and memories — the tag
// simply goes away, since those types have no status to move it into. Nothing enforces them
// afterwards: a wiki article may be tagged anything at all, including these.
//
// `archived` and `inbox` are deliberately absent. Neither is a label describing a document's
// state: `archived` is the mechanism that stamps archived_at and hides a document from search,
// and `inbox` marks a raw capture still queued for compilation. Removing either would break a
// working feature rather than clean up a convention.
var retiredStatusTagLabels = []string{
	"draft", "wip", "in-progress", "active", "todo", "pending", "review", "ready", "done",
}

// StatusTags is the union of the two enforced vocabularies, used for status-badge styling and by
// the /api/status-tags endpoint.
var StatusTags = buildStatusTagUnion()

func buildStatusTagUnion() []string {
	seen := make(map[string]bool)
	var union []string
	for _, list := range [][]string{PlanStatusTags, SkillStatusTags} {
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

// knownStatusWords is every word that could plausibly be meant as a lifecycle status. It exists
// for exactly one purpose: rejecting such a word in a *plan's or skill's* tag list, where it would
// contradict the status field. It is never applied to wiki articles or memories.
var knownStatusWords = buildKnownStatusWords()

func buildKnownStatusWords() map[string]bool {
	words := make(map[string]bool)
	for _, list := range [][]string{PlanStatusTags, SkillStatusTags, retiredStatusTagLabels} {
		for _, t := range list {
			words[t] = true
		}
	}
	for _, replacements := range []map[string]string{planStatusReplacements, skillStatusReplacements} {
		for from := range replacements {
			words[from] = true
		}
	}
	return words
}

// planStatusReplacements maps a lifecycle word that is not a plan status onto the plan status
// meaning the same thing. Two callers: the rejection message ("use 'implementing'") and the
// one-time migration that lifts legacy status tags into the field.
var planStatusReplacements = map[string]string{
	"wip":         "implementing",
	"in-progress": "implementing",
	"active":      "implementing",
	"review":      "implementing",
	"done":        "completed",
	"todo":        "draft",
	"ready":       "draft",
	"inbox":       "draft",
	"pending":     "blocked",
	"deferred":    "parked",
	"on-hold":     "parked",
	"tabled":      "parked",
	"someday":     "parked",
	"deprecated":  "archived",
}

// skillStatusReplacements is the same mapping for skills: work-tracking words collapse to 'draft',
// done-ish words to 'ready', retirement words to 'archived'.
var skillStatusReplacements = map[string]string{
	"wip":          "draft",
	"in-progress":  "draft",
	"implementing": "draft",
	"todo":         "draft",
	"pending":      "draft",
	"review":       "draft",
	"blocked":      "draft",
	"parked":       "draft",
	"inbox":        "draft",
	"active":       "ready",
	"done":         "ready",
	"completed":    "ready",
	"evergreen":    "ready",
	"superseded":   "archived",
	"deprecated":   "archived",
}

// DefaultPlanStatus is where a newly created plan enters the lifecycle.
const DefaultPlanStatus = "draft"

// StatusArchived is the one status value with behavior attached: on a plan or a skill it mirrors
// into archived_at, which is what hides a document from search and puts it on the deletion clock.
const StatusArchived = "archived"

// statusVocabulary returns the enforced vocabulary for a document class, and whether the class
// requires a status at all. A nil vocabulary means the class is unconstrained.
func statusVocabulary(docType string) (vocabulary []string, replacements map[string]string, required bool) {
	switch normalizeType(docType) {
	case ContentTypePlan:
		return PlanStatusTags, planStatusReplacements, true
	case ContentTypeSkill:
		return SkillStatusTags, skillStatusReplacements, false
	default:
		return nil, nil, false
	}
}

// NormalizeStatus canonicalizes a raw status value for storage and comparison.
func NormalizeStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

// ValidateStatus checks a document's `status` field against its class vocabulary:
//
//   - AI-Agent-Plan: must be one of the eight plan statuses (creation defaults to draft).
//   - AI-Agent-Skill: empty, or one of the three skill statuses.
//   - Everything else: not checked. Wiki articles and memories have no lifecycle status; nothing
//     writes the field for them and no UI offers it, but a hand-written value is tolerated rather
//     than policed.
//
// It validates the value only. Unusual *transitions* (draft straight to archived, say) are
// deliberately log-and-allow: strict transition enforcement would fight manual corrections.
func ValidateStatus(docType string, status string) error {
	vocabulary, replacements, required := statusVocabulary(docType)
	if vocabulary == nil {
		return nil
	}
	status = NormalizeStatus(status)
	label := statusClassLabel(docType)

	if status == "" {
		if required {
			return fmt.Errorf("a %s must have a status; valid values: %s", label, strings.Join(vocabulary, ", "))
		}
		return nil
	}
	for _, s := range vocabulary {
		if status == s {
			return nil
		}
	}
	if replacement, known := replacements[status]; known {
		return fmt.Errorf("'%s' is not a %s status; use '%s' instead. Valid values: %s",
			status, label, replacement, strings.Join(vocabulary, ", "))
	}
	return fmt.Errorf("'%s' is not a %s status. Valid values: %s — do not invent new ones",
		status, label, strings.Join(vocabulary, ", "))
}

// ValidateStatusFreeTags rejects a lifecycle word used as a *tag* on a plan or a skill. Status
// lives in the `status` field, and a plan tagged 'completed' whose field says 'implementing' is a
// second, contradictory source of truth — exactly what moving status out of tags removes. Free
// tags (project context, topics) always pass, and other document types are never policed.
func ValidateStatusFreeTags(docType string, tags []string) error {
	if vocabulary, _, _ := statusVocabulary(docType); vocabulary == nil {
		return nil
	}
	label := statusClassLabel(docType)
	for _, t := range tags {
		lower := NormalizeStatus(t)
		if knownStatusWords[lower] {
			return fmt.Errorf("'%s' describes lifecycle state, so it belongs in the %s's 'status' field, not its tags. Keep tags for project context and topics",
				lower, label)
		}
	}
	return nil
}

func statusClassLabel(docType string) string {
	if normalizeType(docType) == ContentTypeSkill {
		return "skill"
	}
	return "plan"
}

// statusPrecedence resolves a document carrying several legacy status words to the single one that
// is most true: a terminal or deliberate state always beats an in-flight one. A plan tagged both
// "superseded" and "completed" is superseded — only the first is still true of it.
var statusPrecedence = map[string][]string{
	ContentTypePlan:  {"superseded", "archived", "parked", "evergreen", "completed", "blocked", "implementing", "draft"},
	ContentTypeSkill: {"archived", "ready", "draft"},
}

// ExtractLegacyStatus separates lifecycle status from a tag list, returning the status and the
// tags that remain. It is how the one-time migration gets status out of tags, and how OKF import
// and version revert accept a document written before the field existed.
//
// The two classes with a lifecycle keep their status: a legacy word is mapped onto their
// vocabulary and returned as the field value. Wiki articles and memories have no status, so a
// retired status label is simply **dropped** — every other tag, `archived` and `inbox` included,
// is left exactly as it was.
func ExtractLegacyStatus(docType string, tags []string) (string, []string) {
	docType = normalizeType(docType)
	vocabulary, replacements, required := statusVocabulary(docType)

	if vocabulary == nil {
		var rest []string
		for _, t := range tags {
			if !isRetiredStatusLabel(NormalizeStatus(t)) {
				rest = append(rest, t)
			}
		}
		return "", rest
	}

	allowed := make(map[string]bool, len(vocabulary))
	for _, s := range vocabulary {
		allowed[s] = true
	}

	var rest []string
	found := make(map[string]bool)
	for _, t := range tags {
		lower := NormalizeStatus(t)
		switch {
		case allowed[lower]:
			found[lower] = true
		case replacements[lower] != "":
			found[replacements[lower]] = true
		default:
			rest = append(rest, t)
		}
	}

	for _, s := range statusPrecedence[docType] {
		if found[s] {
			return s, rest
		}
	}
	if required {
		return DefaultPlanStatus, rest
	}
	return "", rest
}

func isRetiredStatusLabel(word string) bool {
	for _, s := range retiredStatusTagLabels {
		if word == s {
			return true
		}
	}
	return false
}

// legalPlanTransitions is the designed state machine. It exists for logging, not enforcement:
// saveArticleLocked warns about a transition outside this map and applies it anyway, because a
// human correcting a mis-set plan in the editor must always win.
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

// pinnedMemoryKinds are the kinds get_context_overview lists first.
//
// `user` and `feedback` are what an agent needs regardless of the task it is about to start:
// who it is working with, and the corrections that person has already given. Every other kind is
// only relevant once the task is known, so ordering by these two costs nothing and saves the
// agent from reading the whole memory index to find them.
var pinnedMemoryKinds = map[string]bool{"user": true, "feedback": true}

// isPinnedMemoryKind reports whether a kind leads the memory listing in the context overview.
func isPinnedMemoryKind(kind string) bool {
	return pinnedMemoryKinds[NormalizeMemoryKind(kind)]
}

// countPinnedMemories counts how many of these documents carry a pinned kind. Used to decide
// whether the overview explains the ordering at all — a wiki with no user or feedback memories
// should not carry a sentence about an ordering that is not visible.
func countPinnedMemories(docs []Article) int {
	n := 0
	for _, d := range docs {
		if isPinnedMemoryKind(d.MemoryKind) {
			n++
		}
	}
	return n
}

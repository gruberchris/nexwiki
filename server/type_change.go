package server

import (
	"errors"
	"fmt"
	"strings"
)

// A document's type may change on update, but only when a caller names the new type explicitly.
//
// It used to be silently immutable: save_article parsed `type` on an update and never read it, and
// the REST request had no `type` field at all, so a call asking for a relabel returned success and
// changed nothing. A document created as a Wiki could never become the plan it was meant to be, and
// vanished from search_wiki(type: "plans") — list_articles, then — while reading perfectly well by
// slug. Refusing the change would have left delete-and-recreate as the only route, which costs the
// document its history and is exactly the call a cautious agent harness refuses. An omitted `type`
// still means "keep the one it has", so an ordinary edit can never reclassify a document.

// ErrInvalidDocumentType reports a `type` value that names no document type. Callers map it to a
// 4xx / tool error; it is never coerced to Wiki, because a typo that behaves exactly like success
// is indistinguishable from the bug it hides.
var ErrInvalidDocumentType = errors.New("unknown document type")

// ErrInvalidTypeChange reports a lifecycle state that does not survive a type change: the
// document's current plan or skill status is not valid for the type it is becoming, and the
// caller passed no replacement.
var ErrInvalidTypeChange = errors.New("invalid type change")

// ErrInvalidStatus reports an explicit status the document's (resolved) type does not accept.
var ErrInvalidStatus = errors.New("invalid status")

// ResolveDocumentType maps a caller-supplied type name to its canonical OKF type, or "" when it
// names none. It accepts everything search_wiki's type facet accepts (the canonical names,
// case-insensitively, and the friendly aliases "articles", "memories", "plans", "skills"), plus
// Attested Computation, which is a real document class the search facet does not expose.
func ResolveDocumentType(name string) string {
	if canonical := ResolveSearchType(name); canonical != "" {
		return canonical
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "attested computation", "attested-computation", "computation", "computations":
		return ContentTypeComputation
	}
	return ""
}

// DocumentTypeNames lists the canonical document types, for error messages.
func DocumentTypeNames() []string {
	return []string{ContentTypeWiki, ContentTypeMemory, ContentTypePlan, ContentTypeSkill, ContentTypeComputation}
}

// unknownDocumentTypeError names the rejected value and every accepted one.
func unknownDocumentTypeError(name string) error {
	return fmt.Errorf("%w '%s'. Valid values: %s (aliases: %s)", ErrInvalidDocumentType, strings.TrimSpace(name),
		strings.Join(DocumentTypeNames(), ", "), strings.Join(SearchTypeNames(), ", "))
}

// resolveRequestedType resolves an optional `type` argument against the type a document already
// has. Empty keeps current; anything else must name a real type.
func resolveRequestedType(requested, current string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return current, nil
	}
	resolved := ResolveDocumentType(requested)
	if resolved == "" {
		return "", unknownDocumentTypeError(requested)
	}
	return resolved, nil
}

// resolveTypeChangeStatus decides the status override for a save that takes a document from
// oldType (currently at oldStatus) to newType. statusArg is the caller's explicit status: nil
// means none was passed, and a pointer to "" is an explicit clear.
//
// It returns the override to hand to storage (nil preserves the stored status). The rules:
//
//   - An explicit status is validated against the type the document is *becoming*, never the one
//     it is leaving.
//   - Same type, no status: nothing changes.
//   - Into Wiki or AI-Agent-Memory: those types have no lifecycle, so the previous status is
//     dropped.
//   - Out of a type with a lifecycle (plan, skill, computation) into another: the status carries
//     over when the new type accepts it, and otherwise the save is refused — a lifecycle state is
//     never silently coerced into a different one.
//   - Out of a type without a lifecycle: whatever status it carried was never a lifecycle state,
//     so it carries over only if the new type accepts it; otherwise a plan enters at draft and a
//     skill starts with none.
func resolveTypeChangeStatus(oldType, oldStatus, newType string, statusArg *string) (*string, error) {
	if statusArg != nil {
		st := NormalizeStatus(*statusArg)
		if st == "" {
			// An explicit clear. Storage re-enters a plan at draft.
			return &st, nil
		}
		if err := ValidateStatus(newType, st); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidStatus, err.Error())
		}
		return &st, nil
	}
	if newType == oldType {
		return nil, nil
	}

	vocabulary, _, _ := statusVocabulary(newType)
	if vocabulary == nil {
		cleared := ""
		return &cleared, nil
	}

	old := NormalizeStatus(oldStatus)
	if old != "" && ValidateStatus(newType, old) == nil {
		return nil, nil // the current status is valid for the new type; keep it
	}

	if oldVocabulary, _, _ := statusVocabulary(oldType); oldVocabulary != nil && old != "" {
		return nil, fmt.Errorf("%w: changing type from %s to %s: the current status '%s' is not a valid %s status. "+
			"Pass 'status' explicitly (valid values: %s)", ErrInvalidTypeChange, oldType, newType, old,
			statusClassLabel(newType), strings.Join(vocabulary, ", "))
	}

	if newType == ContentTypePlan {
		draft := DefaultPlanStatus
		return &draft, nil
	}
	cleared := ""
	return &cleared, nil
}

// stripMemoryScopeTags drops the tool-managed memory-<scope> tags, which mean nothing on a document
// that is no longer a memory.
func stripMemoryScopeTags(tags []string) []string {
	var kept []string
	for _, t := range tags {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(t)), MemoryScopeTagPrefix) {
			continue
		}
		kept = append(kept, t)
	}
	return kept
}

// typeChangeNote renders the Type line of a save response: the type alone, or the transition.
func typeChangeNote(oldType, newType string) string {
	if oldType != newType {
		return fmt.Sprintf("Type: %s → %s\n", oldType, newType)
	}
	return fmt.Sprintf("Type: %s\n", newType)
}

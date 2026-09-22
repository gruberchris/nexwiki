package server

import (
	"errors"
	"fmt"
	"strings"
)

// ErrMemoryMetadataMissing is returned when a write would bring a memory into existence without
// the metadata every memory must carry. It wraps a message naming each missing field.
var ErrMemoryMetadataMissing = errors.New("a memory needs memory_kind, description and source")

// Per-field explanations, shared by every write path so a refusal reads the same wherever it comes
// from and tells the caller what to put there, not only that something is absent.
const (
	memoryKindRequirement = "'memory_kind' (what sort of fact this is: 'project' for goals and " +
		"constraints not derivable from the repo, 'reference' for a pointer to an external resource, " +
		"'user' for who the operator is, 'feedback' for a correction the operator gave)"
	memoryDescriptionRequirement = "'description' (a one-line summary; it is what search_wiki's index and " +
		"get_wiki_overview show, so a memory without one is invisible when an agent orients itself)"
	memorySourceRequirement = "'source' (where this knowledge came from; a fact with no provenance " +
		"cannot be re-verified later, and its origin cannot be recovered afterwards)"
)

// checkNewMemoryMetadata is the memory write gate: the metadata a document must carry at the moment
// it becomes an AI-Agent-Memory, whether it is created as one or has its type changed into one.
// kind, description and source are the values the document will hold after the write — on a type
// change, the caller's value where it passed one and the document's existing value otherwise.
//
// It deliberately does not run on an ordinary edit of an existing memory. A memory written before
// the kind axis existed stays valid and editable, and an edit that omits a field preserves it; the
// backlog is wiki_health's to report, not every later write's to refuse.
//
// Blank is judged after trimming, because " " satisfies a presence check and fails the wiki_health
// check that motivated the gate — the two must agree on what counts as present. An unknown kind is
// refused with the vocabulary rather than reported as missing.
func checkNewMemoryMetadata(kind, description, source string) error {
	kind = NormalizeMemoryKind(kind)
	var invalidKind error
	if kind != "" {
		invalidKind = ValidateMemoryKind(ContentTypeMemory, kind, true)
	}
	var missing []string
	if kind == "" {
		missing = append(missing, memoryKindRequirement)
	}
	if strings.TrimSpace(description) == "" {
		missing = append(missing, memoryDescriptionRequirement)
	}
	if strings.TrimSpace(source) == "" {
		missing = append(missing, memorySourceRequirement)
	}
	switch {
	case invalidKind != nil && len(missing) > 0:
		return fmt.Errorf("%w. %s. Missing: %s", ErrMemoryMetadataMissing, invalidKind.Error(), strings.Join(missing, "; "))
	case invalidKind != nil:
		return fmt.Errorf("%w. %s", ErrMemoryMetadataMissing, invalidKind.Error())
	case len(missing) > 0:
		return fmt.Errorf("%w. Missing: %s", ErrMemoryMetadataMissing, strings.Join(missing, "; "))
	}
	return nil
}

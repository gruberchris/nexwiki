import React, { useCallback, useMemo, useState } from 'react';
import type { Article } from '../types';

/**
 * Tag editing for the article editor: the current tag set, the autocomplete input, and keyboard
 * navigation over suggestions.
 *
 * Extracted from Editor.tsx, where the logic was spread across three `useState` calls, two memos,
 * a render-time reset, and roughly sixty lines of inline JSX handlers.
 *
 * The reason to gather it rather than merely move it: the rule that users may not create
 * `memory-<scope>` tags was enforced in *three separate places* — while typing, on comma/Enter,
 * and implicitly by filtering suggestions. Those are easy to fall out of step, and the server
 * strips forged memory-scope tags regardless, so a UI that accepts one is offering something it
 * cannot deliver. That rule now lives in one predicate.
 */

/** Prefix reserved for tool-managed memory-scope tags, which users may not author. */
const RESERVED_TAG_PREFIX = 'memory-';

/** Suggestions shown at once; a cap keeps the dropdown from rendering hundreds of nodes. */
const MAX_SUGGESTIONS = 10;

/** Reports whether a tag is reserved for the agent tools and therefore not user-authorable. */
export function isReservedTag(tag: string): boolean {
  return tag.toLowerCase().startsWith(RESERVED_TAG_PREFIX);
}

export interface UseTagEditorResult {
  tags: string[];
  tagInput: string;
  suggestions: string[];
  /** Index of the keyboard-focused suggestion, or -1 when the input itself has focus. */
  focusedIndex: number;
  /** Rejects reserved prefixes as they are typed. */
  handleInputChange: (value: string) => void;
  /** ArrowDown / ArrowUp to move through suggestions, Enter or comma to commit. */
  handleKeyDown: (e: React.KeyboardEvent) => void;
  /** Commits a suggestion the user clicked. */
  selectSuggestion: (tag: string) => void;
  removeTag: (tag: string) => void;
}

export function useTagEditor(initialTags: string[], articles: Article[]): UseTagEditorResult {
  const [tags, setTags] = useState<string[]>(initialTags);
  const [tagInput, setTagInput] = useState('');
  const [focusedIndex, setFocusedIndex] = useState(-1);

  // Every free tag in use across the wiki, minus the reserved ones users cannot author.
  const allExistingTags = useMemo(() => {
    const set = new Set<string>();
    articles.forEach((art) => {
      art.tags?.forEach((tag) => {
        if (!isReservedTag(tag)) set.add(tag);
      });
    });
    return Array.from(set);
  }, [articles]);

  const suggestions = useMemo(() => {
    const query = tagInput.trim().toLowerCase();
    if (!query) return [];
    return allExistingTags
      .filter((tag) => tag.toLowerCase().includes(query) && !tags.some((t) => t.toLowerCase() === tag.toLowerCase()))
      .slice(0, MAX_SUGGESTIONS);
  }, [tagInput, allExistingTags, tags]);

  // Reset the highlight whenever the suggestion set changes, or the index would point at a
  // different tag than the one the user was looking at. Adjusting during render rather than in an
  // effect avoids a second render pass on every keystroke.
  const [prevSuggestionsLength, setPrevSuggestionsLength] = useState(suggestions.length);
  if (suggestions.length !== prevSuggestionsLength) {
    setFocusedIndex(-1);
    setPrevSuggestionsLength(suggestions.length);
  }

  /** Adds a tag unless it is reserved or already present (case-insensitively). */
  const addTag = useCallback((raw: string) => {
    const tag = raw.trim().replace(/,/g, '');
    if (!tag || isReservedTag(tag)) return false;
    let added = false;
    setTags((current) => {
      if (current.some((t) => t.toLowerCase() === tag.toLowerCase())) return current;
      added = true;
      return [...current, tag];
    });
    return added;
  }, []);

  const handleInputChange = useCallback((value: string) => {
    // Refuse the reserved prefix while typing, so the field cannot even display a tag that
    // would be stripped on save.
    if (isReservedTag(value)) return;
    setTagInput(value);
  }, []);

  const selectSuggestion = useCallback(
    (tag: string) => {
      addTag(tag);
      setTagInput('');
      setFocusedIndex(-1);
    },
    [addTag],
  );

  const removeTag = useCallback((tag: string) => {
    setTags((current) => current.filter((t) => t !== tag));
  }, []);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (suggestions.length > 0) {
        // Arrow keys navigate suggestions; Tab keeps its ordinary focus-movement meaning.
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
          e.preventDefault();
          // Cycles through -1 (the input itself) so the user can always get back to free typing.
          setFocusedIndex((prev) =>
            e.key === 'ArrowUp'
              ? prev <= -1 ? suggestions.length - 1 : prev - 1
              : prev >= suggestions.length - 1 ? -1 : prev + 1,
          );
          return;
        }
        if (e.key === 'Enter' && focusedIndex >= 0 && focusedIndex < suggestions.length) {
          e.preventDefault();
          selectSuggestion(suggestions[focusedIndex]);
          return;
        }
      }

      if (e.key === 'Enter' || e.key === ',') {
        e.preventDefault();
        addTag(tagInput);
        setTagInput('');
      }
    },
    [suggestions, focusedIndex, selectSuggestion, addTag, tagInput],
  );

  return {
    tags,
    tagInput,
    suggestions,
    focusedIndex,
    handleInputChange,
    handleKeyDown,
    selectSuggestion,
    removeTag,
  };
}

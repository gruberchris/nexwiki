/**
 * A memory has two axes. Kind — what sort of fact it holds — is a closed four-value field, so it
 * gets a palette here. Scope — how far the fact reaches — stays a free-form `memory-<scope>` tag
 * and renders with the ordinary tag styling.
 *
 * Mirrors MemoryKinds in server/tags.go. Keep the two in step: the server rejects anything outside
 * its vocabulary, so a value offered here that the server does not know is a save that fails.
 */

import { ContentTypes, type ContentType } from './types';

export const MEMORY_KINDS = ['project', 'reference', 'user', 'feedback'] as const;

export type MemoryKind = (typeof MEMORY_KINDS)[number];

/**
 * Badge classes per kind. The two people-facing kinds share a warm hue and the two
 * knowledge-facing kinds a cool one, so the axis reads at a glance rather than as four
 * arbitrary colors. Deliberately disjoint from the status palette: a memory never has a status
 * and a plan never has a kind, but they sit in the same badge row on a card, and two vocabularies
 * that shared a color would suggest a relationship that does not exist.
 */
const KIND_BADGE_CLASSES: Record<string, string> = {
  project: 'bg-indigo-500/10 text-indigo-600 dark:text-indigo-400 border border-indigo-500/30',
  reference: 'bg-cyan-500/10 text-cyan-600 dark:text-cyan-400 border border-cyan-500/30',
  user: 'bg-orange-500/10 text-orange-600 dark:text-orange-400 border border-orange-500/30',
  feedback: 'bg-pink-500/10 text-pink-600 dark:text-pink-400 border border-pink-500/30',
};

const NEUTRAL_BADGE = 'bg-themeAccentBg text-themeAccent border border-themeBorder';

/** Badge classes for a memory kind; anything unrecognized renders neutral. */
export function memoryKindBadgeClass(kind: string): string {
  return KIND_BADGE_CLASSES[kind.toLowerCase()] ?? NEUTRAL_BADGE;
}

/** One-line explanation of each kind, shown as the control's title in the editor. */
export const MEMORY_KIND_HINTS: Record<string, string> = {
  project: 'Goals and constraints not derivable from the repo or its git history',
  reference: 'A pointer to an external resource — dashboard, ticket, host, URL',
  user: 'Who the operator is: role, expertise, standing preferences',
  feedback: 'A correction the operator gave, with why and how to apply it',
};

/**
 * The kind options for a document class, or null when the class has no kind at all. Only memories
 * do — the same shape as statusOptionsFor, so the editor treats the two controls identically.
 */
export function memoryKindOptionsFor(type: ContentType | undefined): readonly string[] | null {
  return type === ContentTypes.Memory ? MEMORY_KINDS : null;
}

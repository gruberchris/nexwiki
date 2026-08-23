/**
 * Lifecycle status is a document field, not a tag, and only agent plans and agent skills have one.
 * Wiki articles and memories describe themselves with free tags instead.
 *
 * The badge palette covers both vocabularies: eight states need colors that read at a glance, not
 * eight variations of gray. Anything unrecognized falls back to the neutral pill.
 */

export const PLAN_STATUSES = [
  'draft',
  'implementing',
  'blocked',
  'completed',
  'superseded',
  'parked',
  'evergreen',
  'archived',
] as const;

export const SKILL_STATUSES = ['draft', 'ready', 'archived'] as const;

export type PlanStatus = (typeof PLAN_STATUSES)[number];

/** Badge classes per known status; anything else renders neutral. */
const STATUS_BADGE_CLASSES: Record<string, string> = {
  draft: 'bg-slate-500/10 text-slate-600 dark:text-slate-300 border border-slate-500/30',
  implementing: 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border border-blue-500/30',
  blocked: 'bg-rose-500/10 text-rose-600 dark:text-rose-400 border border-rose-500/30',
  completed: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/30',
  ready: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/30',
  superseded: 'bg-purple-500/10 text-purple-600 dark:text-purple-400 border border-purple-500/30',
  parked: 'bg-amber-500/10 text-amber-600 dark:text-amber-400 border border-amber-500/30',
  evergreen: 'bg-teal-500/10 text-teal-600 dark:text-teal-400 border border-teal-500/30',
  archived: 'bg-slate-500/10 text-slate-400 dark:text-slate-500 border border-slate-500/20',
};

const NEUTRAL_BADGE = 'bg-themeAccentBg text-themeAccent border border-themeBorder';

/** Badge classes for a status value. */
export function statusBadgeClass(status: string): string {
  return STATUS_BADGE_CLASSES[status.toLowerCase()] ?? NEUTRAL_BADGE;
}

/** The vocabulary a document type chooses from, or null when the type has no status at all. */
export function statusOptionsFor(type: string | undefined): readonly string[] | null {
  if (type === 'AI-Agent-Plan') return PLAN_STATUSES;
  if (type === 'AI-Agent-Skill') return SKILL_STATUSES;
  return null;
}

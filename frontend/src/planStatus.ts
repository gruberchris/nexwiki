/**
 * The closed plan lifecycle vocabulary and its badge palette. Eight states need a legible
 * palette, not eight variations of gray: each family is chosen to read at a glance —
 * neutral for not-started, blue for in motion, rose for stuck, green for done, and muted
 * variants for the states that are deliberately out of the flow.
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

export type PlanStatus = (typeof PLAN_STATUSES)[number];

const PLAN_STATUS_SET = new Set<string>(PLAN_STATUSES);

/** The plan lifecycle status carried by a tag list, or null when none is present. */
export function planStatusOf(tags: string[] | undefined): PlanStatus | null {
  if (!tags) return null;
  for (const tag of tags) {
    const lower = tag.toLowerCase();
    if (PLAN_STATUS_SET.has(lower)) return lower as PlanStatus;
  }
  return null;
}

/** Badge classes per plan status; falls back to the accent pill for non-status tags. */
export const PLAN_STATUS_BADGE_CLASSES: Record<PlanStatus, string> = {
  draft: 'bg-slate-500/10 text-slate-600 dark:text-slate-300 border border-slate-500/30',
  implementing: 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border border-blue-500/30',
  blocked: 'bg-rose-500/10 text-rose-600 dark:text-rose-400 border border-rose-500/30',
  completed: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/30',
  superseded: 'bg-purple-500/10 text-purple-600 dark:text-purple-400 border border-purple-500/30',
  parked: 'bg-amber-500/10 text-amber-600 dark:text-amber-400 border border-amber-500/30',
  evergreen: 'bg-teal-500/10 text-teal-600 dark:text-teal-400 border border-teal-500/30',
  archived: 'bg-slate-500/10 text-slate-400 dark:text-slate-500 border border-slate-500/20 line-through',
};

/** Badge classes for a tag: the status palette when it is a plan status, null otherwise. */
export function planStatusBadgeClass(tag: string): string | null {
  const lower = tag.toLowerCase();
  return PLAN_STATUS_SET.has(lower) ? PLAN_STATUS_BADGE_CLASSES[lower as PlanStatus] : null;
}

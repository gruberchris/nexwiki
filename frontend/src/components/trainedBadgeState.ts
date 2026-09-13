/**
 * Plain helpers behind the trained badge (story 08), split out of
 * TrainedBadge.tsx so the component file only exports the component
 * (react-refresh's fast-refresh rule) and the logic stays independently
 * testable — the same split statusTags.ts uses for lifecycle badges.
 */
import type { SkillTrainedState } from '../types';

export const TRAINED_STATE_LABELS: Record<string, string> = {
  trained: 'Trained',
  stale: 'Stale',
  untrained: 'Untrained',
};

export const TRAINED_STATE_BADGE_CLASSES: Record<string, string> = {
  trained: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/30',
  stale: 'bg-amber-500/10 text-amber-600 dark:text-amber-400 border border-amber-500/30',
  untrained: 'bg-slate-500/10 text-slate-500 dark:text-slate-400 border border-slate-500/30',
};

/** Human-readable tooltip body: what the marker says and why it does not match (when stale). */
export function trainedBadgeTooltip(state: SkillTrainedState): string {
  const lines: string[] = [];
  if (state.state === 'trained' || state.trained_at) {
    const at = state.trained_at && !state.trained_at.startsWith('0001-01-01')
      ? new Date(state.trained_at).toLocaleString(undefined, {
          year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
        })
      : 'time unknown';
    const parts: string[] = [`Trained ${at}`];
    if (state.trained_version) parts.push(`at v${state.trained_version}`);
    if (state.trained_val_score !== undefined) parts.push(`val score ${state.trained_val_score}`);
    lines.push(parts.join(' · '));
  }
  if (state.current_version) lines.push(`Live skill: v${state.current_version}`);
  if (state.revoked_reason) lines.push(`Revoked: ${state.revoked_reason}`);
  if (state.state === 'stale' && state.reason) lines.push(state.reason);
  if (state.state === 'untrained' && !state.trained_at) lines.push('No trained marker yet.');
  return lines.join('\n');
}

import React from 'react';
import type { SkillTrainedState } from '../types';
import { trainedBadgeTooltip, TRAINED_STATE_BADGE_CLASSES, TRAINED_STATE_LABELS } from './trainedBadgeState';

/**
 * The trained badge (story 08): one component everywhere a skill title appears —
 * registry rows, the skill viewer header, and the wizard's picker. It renders the
 * derived trained state from GET /api/skills (`trained_state`, story 06) as a
 * colored pill with the details on its title tooltip: when it was trained, at
 * which version, the marker's validation score, and — when stale — the reason
 * the marker stopped matching the skill or its eval set.
 *
 * There are deliberately only three states, exactly the picker's vocabulary: a
 * revoked marker is a stale one whose reason says so.
 */

interface TrainedBadgeProps {
  state?: SkillTrainedState | null;
  className?: string;
}

export const TrainedBadge: React.FC<TrainedBadgeProps> = ({ state, className = '' }) => {
  if (!state) {
    return (
      <span
        data-testid="trained-badge-loading"
        title="Trained state is loading…"
        className={`inline-flex items-center gap-1 text-[10px] font-bold px-2 py-0.5 rounded-full bg-themeAccentBg text-themeTextMuted border border-themeBorder ${className}`}
      >
        …
      </span>
    );
  }
  const label = TRAINED_STATE_LABELS[state.state] ?? state.state;
  return (
    <span
      data-testid="trained-badge"
      data-trained-state={state.state}
      title={trainedBadgeTooltip(state)}
      className={`inline-flex items-center gap-1.5 text-[10px] font-bold px-2 py-0.5 rounded-full select-none ${TRAINED_STATE_BADGE_CLASSES[state.state] ?? TRAINED_STATE_BADGE_CLASSES.untrained} ${className}`}
    >
      <span className="w-1.5 h-1.5 rounded-full bg-current" />
      {label}
    </span>
  );
};

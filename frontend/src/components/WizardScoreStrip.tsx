import React from 'react';
import type { IterationRecord } from '../types';
import { iterationChipClass } from './wizardChips';

/**
 * The score strip (story 08): one chip per recorded iteration, colored by its
 * outcome, with the gate's score-versus-R_best on its tooltip — the run's
 * trajectory at a glance above the detailed iteration feed.
 */

interface WizardScoreStripProps {
  iterations: IterationRecord[];
  /** The iteration currently in flight (rendered as a live chip when set). */
  currentIteration?: number;
  currentPhase?: string;
}

export const WizardScoreStrip: React.FC<WizardScoreStripProps> = ({ iterations, currentIteration, currentPhase }) => {
  if (iterations.length === 0 && !currentIteration) return null;

  return (
    <div
      data-testid="wizard-score-strip"
      className="flex flex-wrap items-center gap-1.5 rounded-2xl border border-themeBorder bg-themeBgSecondary/60 px-4 py-3 select-none"
    >
      <span className="text-[10px] font-bold uppercase tracking-wider text-themeTextMuted mr-1">Trajectory</span>
      {iterations.map((rec) => (
        <span
          key={rec.iteration}
          title={`Iteration #${rec.iteration}: val ${rec.val_score.toFixed(4)} vs R_best ${rec.r_best.toFixed(4)} — ${rec.outcome || 'running'}`}
          className={`text-[10px] font-bold px-2 py-1 rounded-full ${iterationChipClass(rec.outcome)}`}
        >
          #{rec.iteration}
          {rec.val_score > 0 || rec.r_best > 0 ? (
            <span className="ml-1 font-mono font-semibold opacity-80">
              {(rec.val_score - rec.r_best >= 0 ? '+' : '') + (rec.val_score - rec.r_best).toFixed(2)}
            </span>
          ) : null}
        </span>
      ))}
      {currentIteration !== undefined && !iterations.some((r) => r.iteration === currentIteration) && (
        <span
          title={`Iteration #${currentIteration} in flight${currentPhase ? ` at ${currentPhase}` : ''}`}
          data-testid="wizard-score-strip-live"
          className="text-[10px] font-bold px-2 py-1 rounded-full bg-blue-500/10 text-blue-600 dark:text-blue-400 border border-blue-500/30 animate-pulse-subtle"
        >
          #{currentIteration} live
        </span>
      )}
    </div>
  );
};

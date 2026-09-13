import React from 'react';
import type { IterationRecord, LoopState, SkillCandidate } from '../types';
import { ChevronDown, Clock, GitBranch, AlertTriangle, Hourglass } from 'lucide-react';
import { iterationChipClass, iterationOutcomeLabel, phaseChipClass, phaseLabel, whyWaitingLine } from './wizardChips';
import { WizardDiffView } from './WizardDiffView';

/**
 * One iteration card (story 08): the per-iteration record the loop stepper
 * persisted, rendered honestly — the state-machine chip, every phase entry
 * with its timestamps (including interrupted re-runs), the candidate it
 * produced with its pattern slugs, the validation score against R_best, and
 * the why-waiting line when the loop is standing inside this iteration.
 */

interface WizardIterationCardProps {
  record: IterationRecord;
  /** True when the loop's current iteration is this one (in flight or paused here). */
  isCurrent: boolean;
  loop: LoopState | null;
  candidate?: SkillCandidate;
  candidateLoading?: boolean;
}

function fmtTime(iso?: string): string {
  if (!iso || iso.startsWith('0001-01-01')) return '—';
  return new Date(iso).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function fmtScore(n: number): string {
  return n.toFixed(4);
}

export const WizardIterationCard: React.FC<WizardIterationCardProps> = ({
  record,
  isCurrent,
  loop,
  candidate,
  candidateLoading,
}) => {
  const [diffOpen, setDiffOpen] = React.useState(false);
  const inFlight = record.outcome === '';
  const hasDiff = !!candidate && (!!candidate.diff || !!candidate.proposed_body);

  return (
    <article
      data-testid={`wizard-iteration-card-${record.iteration}`}
      className={`rounded-2xl border p-4 bg-themeBgSecondary/60 transition-colors ${
        isCurrent && inFlight ? 'border-themeAccent/40' : 'border-themeBorder'
      }`}
    >
      {/* Header: iteration number, state chip, score line */}
      <div className="flex flex-wrap items-center gap-2 justify-between">
        <div className="flex flex-wrap items-center gap-2">
          <h4 className="text-xs font-black text-themeTextPrimary">Iteration #{record.iteration}</h4>
          <span
            data-testid={`wizard-iteration-chip-${record.iteration}`}
            className={`text-[10px] font-bold px-2 py-0.5 rounded-full ${iterationChipClass(record.outcome)}`}
          >
            {inFlight && isCurrent && loop?.current_phase
              ? `${iterationOutcomeLabel(record.outcome)} · ${phaseLabel(loop.current_phase)}`
              : iterationOutcomeLabel(record.outcome)}
          </span>
          {record.result_version ? (
            <span className="text-[10px] font-bold px-2 py-0.5 rounded-full bg-themeAccentBg text-themeAccent border border-themeBorder">
              promoted v{record.result_version}
            </span>
          ) : null}
        </div>
        <div className="flex items-center gap-1.5 text-[10px] text-themeTextMuted">
          <Clock size={10} />
          <span>
            {fmtTime(record.started_at)}
            {record.completed_at && !record.completed_at.startsWith('0001-01-01') ? ` → ${fmtTime(record.completed_at)}` : ' → …'}
          </span>
        </div>
      </div>

      {/* Score strip: this iteration's gate numbers, from the gate's own audit decision */}
      <div className="mt-2 flex flex-wrap items-center gap-2 text-[11px]">
        <span className="font-semibold text-themeTextSecondary">val {fmtScore(record.val_score)}</span>
        <span className="text-themeTextMuted">vs R_best {fmtScore(record.r_best)}</span>
        {record.val_score > 0 && (
          <span
            className={`text-[10px] font-bold px-1.5 py-0.5 rounded-full ${
              record.val_score >= record.r_best
                ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                : 'bg-amber-500/10 text-amber-600 dark:text-amber-400'
            }`}
          >
            {record.val_score >= record.r_best ? 'at or above best' : 'below best'}
          </span>
        )}
      </div>

      {/* Phase trail, including interrupted re-runs: an entry without ExitedAt is open */}
      {record.phases.length > 0 && (
        <ol className="mt-3 space-y-1.5">
          {record.phases.map((phase, i) => {
            const open = !phase.complete;
            return (
              <li key={`${phase.phase}-${i}`} className="flex flex-wrap items-center gap-2 text-[11px]">
                <span className={`text-[10px] font-bold px-2 py-0.5 rounded-full ${phaseChipClass(phase.phase)}`}>
                  {phaseLabel(phase.phase)}
                </span>
                <span className="text-themeTextMuted font-mono">
                  {fmtTime(phase.entered_at)}
                  {phase.complete ? ` → ${fmtTime(phase.exited_at)}` : ' → …'}
                </span>
                {open && (
                  <span
                    className="inline-flex items-center gap-1 text-[10px] font-semibold text-amber-600 dark:text-amber-400"
                    title="This phase never completed — a park, a crash, or an abort interrupted it; a resume re-runs it."
                  >
                    <AlertTriangle size={10} />
                    interrupted
                  </span>
                )}
                {phase.note && <span className="text-themeTextMuted truncate max-w-xs">{phase.note}</span>}
              </li>
            );
          })}
        </ol>
      )}

      {/* Candidate: identity, patterns, and the read-only diff */}
      {record.candidate_id && (
        <div className="mt-3 rounded-xl border border-themeBorder bg-themeBgPrimary/60 p-3 space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <GitBranch size={11} className="text-themeAccent shrink-0" />
            <span className="text-[11px] font-mono text-themeTextSecondary break-all">{record.candidate_id}</span>
            {candidateLoading && <span className="text-[10px] text-themeTextMuted">loading candidate…</span>}
            {hasDiff && (
              <button
                type="button"
                onClick={() => setDiffOpen((open) => !open)}
                aria-expanded={diffOpen}
                data-testid={`wizard-iteration-diff-toggle-${record.iteration}`}
                className="ml-auto inline-flex items-center gap-1 text-[10px] font-bold text-themeAccent hover:underline cursor-pointer"
              >
                <ChevronDown size={10} className={`transition-transform duration-200 ${diffOpen ? 'rotate-180' : ''}`} />
                {diffOpen ? 'Hide diff' : 'Show diff'}
              </button>
            )}
          </div>
          {candidate?.pattern_slugs && candidate.pattern_slugs.length > 0 && (
            <div className="flex flex-wrap gap-1">
              {candidate.pattern_slugs.map((slug) => (
                <span
                  key={slug}
                  className="text-[10px] px-1.5 py-0.5 rounded-full bg-themeAccentBg text-themeAccent font-semibold"
                  title="Wiki pattern this candidate drew on"
                >
                  {slug}
                </span>
              ))}
            </div>
          )}
          {diffOpen && candidate && (candidate.diff || candidate.proposed_body) && (
            <WizardDiffView diff={candidate.diff || candidate.proposed_body || ''} />
          )}
        </div>
      )}

      {/* Why-waiting / note */}
      <p data-testid={`wizard-iteration-waiting-${record.iteration}`} className="mt-2.5 flex items-start gap-1.5 text-[11px] text-themeTextMuted leading-relaxed">
        {inFlight && isCurrent ? (
          <>
            <Hourglass size={11} className="shrink-0 mt-0.5 text-themeAccent" />
            <span>{whyWaitingLine(loop)}</span>
          </>
        ) : (
          record.note && <span>{record.note}</span>
        )}
      </p>
    </article>
  );
};

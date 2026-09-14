import React, { useMemo } from 'react';
import type { EvolutionJob, LoopState } from '../types';
import { formatRunElapsed } from '../utils';
import { Pause, Square, CheckCircle2, RefreshCw, Loader2 } from 'lucide-react';
import { jobStatusChipClass, loopOutcomeChipClass, outcomeLabel } from './wizardChips';

/**
 * The live run header (story 08): the persistent always-know-what-is-happening
 * banner over the Evolve step. It renders the server's own state — skill and
 * version, run id, elapsed time, the stop rules the job actually carries — and
 * the three human controls wired to the audited story-07 REST endpoints.
 *
 * Resume-after-pause is the wizard's own dispatch endpoint (story 13), offered
 * by the Evolve step's Resume control above this header; the paused banner
 * points there instead of sending the operator to a harness.
 */

export type WizardControl = 'pause' | 'abort' | 'approve';

interface WizardRunHeaderProps {
  skillTitle: string;
  skillVersion?: number;
  job: EvolutionJob | null;
  loop: LoopState | null;
  plateauCount: number;
  plateauLimit: number;
  maxIterations: number;
  /** Elapsed milliseconds, ticked by the parent so "now" stays live. */
  elapsedMs: number;
  controlBusy: WizardControl | null;
  onPause: () => void;
  onAbort: () => void;
  onApprove: () => void;
  onRefresh: () => void;
}

export const WizardRunHeader: React.FC<WizardRunHeaderProps> = ({
  skillTitle,
  skillVersion,
  job,
  loop,
  plateauCount,
  plateauLimit,
  maxIterations,
  elapsedMs,
  controlBusy,
  onPause,
  onAbort,
  onApprove,
  onRefresh,
}) => {
  const terminal = !!job && ['complete', 'failed', 'timeout', 'cancelled'].includes(job.status);
  const busy = controlBusy !== null;

  const stopRules = useMemo(() => {
    if (!job) return [];
    const rules: string[] = [];
    rules.push(`max ${maxIterations || job.max_iterations || 12} iterations`);
    rules.push(`plateau after ${plateauLimit || job.plateau_limit || 3} rejected`);
    if (job.run_deadline_at && !job.run_deadline_at.startsWith('0001-01-01')) {
      rules.push(`run cap ${new Date(job.run_deadline_at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}`);
    }
    return rules;
  }, [job, maxIterations, plateauLimit]);

  const isRunning = !!job && !terminal && job.status !== 'paused';
  const isPaused = job?.status === 'paused';
  const isQueued = job?.status === 'queued';

  return (
    <section
      data-testid="wizard-run-header"
      className="rounded-2xl border border-themeBorder bg-themeBgSecondary/60 backdrop-blur-sm p-5 select-none"
    >
      <div className="flex flex-col lg:flex-row lg:items-center gap-4 justify-between">
        {/* Identity: skill + version + run id + status */}
        <div className="min-w-0 space-y-1.5">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-sm font-black text-themeTextPrimary truncate max-w-[24rem]">{skillTitle}</h3>
            {skillVersion !== undefined && (
              <span className="text-[10px] font-bold px-2 py-0.5 rounded-full bg-themeAccentBg text-themeAccent border border-themeBorder">
                v{skillVersion}
              </span>
            )}
            {job && (
              <span className={`text-[10px] font-bold px-2 py-0.5 rounded-full ${jobStatusChipClass(job.status)}`}>
                {job.status}
              </span>
            )}
            {loop?.status === 'paused' && !isPaused && (
              <span className="text-[10px] font-bold px-2 py-0.5 rounded-full bg-amber-500/10 text-amber-600 dark:text-amber-400 border border-amber-500/30">
                loop paused
              </span>
            )}
            {loop?.outcome && (
              <span
                data-testid="wizard-run-outcome"
                className={`text-[10px] font-bold px-2 py-0.5 rounded-full ${loopOutcomeChipClass(loop.outcome)}`}
                title={loop.outcome_reason}
              >
                {outcomeLabel(loop.outcome)}
              </span>
            )}
          </div>
          {job && (
            <p className="text-[10px] text-themeTextMuted font-mono truncate">
              run {job.id}
              {loop?.started_at && !loop.started_at.startsWith('0001-01-01') && (
                <span className="font-sans"> · elapsed {formatRunElapsed(elapsedMs)}</span>
              )}
            </p>
          )}
        </div>

        {/* Stop rules the job actually carries */}
        {job && (
          <div className="flex flex-wrap items-center gap-1.5 text-[10px] font-semibold text-themeTextMuted">
            <span className="px-2 py-0.5 rounded-full bg-themeBgSecondary border border-themeBorder">Stop rules:</span>
            {stopRules.map((rule) => (
              <span key={rule} className="px-2 py-0.5 rounded-full bg-themeBgSecondary border border-themeBorder">
                {rule}
              </span>
            ))}
            <span
              className="px-2 py-0.5 rounded-full bg-themeBgSecondary border border-themeBorder"
              title="Consecutive rejected iterations without pattern gain"
            >
              plateau {plateauCount}/{plateauLimit}
            </span>
          </div>
        )}

        {/* Human controls, calling the audited REST endpoints */}
        <div className="flex items-center gap-2 shrink-0">
          <button
            type="button"
            onClick={onRefresh}
            disabled={busy}
            title="Refresh run state now"
            aria-label="Refresh run state"
            className="p-2 rounded-xl border border-themeBorder bg-themeBgPrimary text-themeTextMuted hover:text-themeAccent hover:border-themeAccent/40 disabled:opacity-50 transition-colors cursor-pointer"
          >
            <RefreshCw size={13} />
          </button>
          <button
            type="button"
            onClick={onPause}
            disabled={!job || terminal || isPaused || isQueued || busy}
            data-testid="wizard-pause-button"
            className="flex items-center gap-1.5 py-2 px-3 rounded-xl border border-amber-200 dark:border-amber-900/60 bg-amber-500/10 text-amber-600 dark:text-amber-400 hover:bg-amber-500/20 font-semibold text-xs shadow-xs hover:scale-[1.02] active:scale-95 transition-all duration-200 cursor-pointer disabled:opacity-40 disabled:hover:scale-100"
          >
            {controlBusy === 'pause' ? <Loader2 size={12} className="animate-spin" /> : <Pause size={12} />}
            <span>{controlBusy === 'pause' ? 'Pausing…' : 'Pause'}</span>
          </button>
          <button
            type="button"
            onClick={onApprove}
            disabled={!job || terminal || busy}
            data-testid="wizard-approve-button"
            title={loop?.outcome ? 'Run already finished' : 'Gate the current best now and finish the run'}
            className="flex items-center gap-1.5 py-2 px-3 rounded-xl border border-emerald-200 dark:border-emerald-900/60 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 hover:bg-emerald-500/20 font-semibold text-xs shadow-xs hover:scale-[1.02] active:scale-95 transition-all duration-200 cursor-pointer disabled:opacity-40 disabled:hover:scale-100"
          >
            {controlBusy === 'approve' ? <Loader2 size={12} className="animate-spin" /> : <CheckCircle2 size={12} />}
            <span>{controlBusy === 'approve' ? 'Approving…' : 'Approve early'}</span>
          </button>
          <button
            type="button"
            onClick={onAbort}
            disabled={!job || terminal || busy}
            data-testid="wizard-abort-button"
            title="Abort the run: cancel the job, keep the skill at its last accepted version"
            className="flex items-center gap-1.5 py-2 px-3 rounded-xl border border-rose-200 dark:border-rose-900/60 bg-rose-500/10 text-rose-600 dark:text-rose-400 hover:bg-rose-500/20 font-semibold text-xs shadow-xs hover:scale-[1.02] active:scale-95 transition-all duration-200 cursor-pointer disabled:opacity-40 disabled:hover:scale-100"
          >
            {controlBusy === 'abort' ? <Loader2 size={12} className="animate-spin" /> : <Square size={12} />}
            <span>{controlBusy === 'abort' ? 'Aborting…' : 'Abort'}</span>
          </button>
        </div>
      </div>

      {/* Always-know-what-is-happening line: what the run is standing on right now. */}
      {isRunning && (
        <p data-testid="wizard-run-waiting" className="mt-3 text-[11px] text-themeTextMuted leading-relaxed">
          {job?.pause_requested
            ? 'Pause requested — the run parks at the next step boundary.'
            : job?.approve_requested
              ? 'Approve-early requested — the loop finishes with the current best at its next boundary.'
              : loop?.note
                ? loop.note
                : 'The loop is driving — iterations appear below as the phases resolve.'}
        </p>
      )}
      {isPaused && (
        <p data-testid="wizard-run-waiting" className="mt-3 text-[11px] text-amber-600 dark:text-amber-400 leading-relaxed">
          Paused{job?.checkpoint ? ` — ${job.checkpoint}` : ''}. Use the Resume control above this header —
          the loop continues from its recorded step, never re-running completed work.
        </p>
      )}
      {terminal && job && (
        <p data-testid="wizard-run-waiting" className="mt-3 text-[11px] text-themeTextMuted leading-relaxed">
          Run {job.status}
          {loop?.outcome_reason ? `: ${loop.outcome_reason}` : job.cancel_reason ? `: ${job.cancel_reason}` : job.error ? `: ${job.error}` : '.'}
          {' '}The skill stays at its last accepted version; start a new run from the Data step to retrain.
        </p>
      )}
      {!job && (
        <p data-testid="wizard-run-waiting" className="mt-3 text-[11px] text-themeTextMuted leading-relaxed">
          No evolution run yet. Start one from the Data step — or follow the run from there once it exists.
        </p>
      )}
    </section>
  );
};

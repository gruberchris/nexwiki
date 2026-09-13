/**
 * Badge-class helpers for the evolution wizard (story 08), in the spirit of
 * statusTags.ts: small class maps so chips read at a glance and unrecognized
 * values fall back to the neutral pill. Nothing here renders JSX — these are
 * the Tailwind token strings the wizard's components compose.
 */
import type { EvolutionJob, LoopState } from '../types';

const NEUTRAL_CHIP = 'bg-slate-500/10 text-slate-500 dark:text-slate-400 border border-slate-500/30';
const RUNNING_CHIP = 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border border-blue-500/30';
const DONE_CHIP = 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border border-emerald-500/30';
const BAD_CHIP = 'bg-rose-500/10 text-rose-600 dark:text-rose-400 border border-rose-500/30';
const WAIT_CHIP = 'bg-amber-500/10 text-amber-600 dark:text-amber-400 border border-amber-500/30';

/** Chip classes for an iteration's outcome: the loop's own vocabulary. */
export function iterationChipClass(outcome: string): string {
  switch (outcome) {
    case 'accepted':
      return DONE_CHIP;
    case 'rejected':
      return BAD_CHIP;
    case 'interrupted':
      return WAIT_CHIP;
    default:
      // "" — the iteration is in flight; the live view shows it blue.
      return RUNNING_CHIP;
  }
}

/** Chip classes for a loop phase within an iteration. */
export function phaseChipClass(phase: string): string {
  switch (phase) {
    case 'queued':
      return NEUTRAL_CHIP;
    case 'inference':
    case 'maintaining':
    case 'proposing':
      return RUNNING_CHIP;
    case 'gating':
      return DONE_CHIP;
    default:
      return NEUTRAL_CHIP;
  }
}

/** Chip classes for a job lifecycle status. */
export function jobStatusChipClass(status: string): string {
  switch (status) {
    case 'queued':
      return NEUTRAL_CHIP;
    case 'claimed':
    case 'running':
      return RUNNING_CHIP;
    case 'paused':
      return WAIT_CHIP;
    case 'complete':
      return DONE_CHIP;
    case 'failed':
    case 'timeout':
    case 'cancelled':
      return BAD_CHIP;
    default:
      return NEUTRAL_CHIP;
  }
}

/** Chip classes for a loop outcome (the reason a run ended). */
export function loopOutcomeChipClass(outcome: string): string {
  switch (outcome) {
    case 'completed':
      return DONE_CHIP;
    case 'plateaued':
      return WAIT_CHIP;
    case 'exhausted':
      return NEUTRAL_CHIP;
    case 'cancelled':
      return BAD_CHIP;
    default:
      return NEUTRAL_CHIP;
  }
}

/** Human label for a loop phase. */
export function phaseLabel(phase: string): string {
  switch (phase) {
    case 'queued':
      return 'Queued';
    case 'inference':
      return 'Inference';
    case 'maintaining':
      return 'Maintaining';
    case 'proposing':
      return 'Proposing';
    case 'gating':
      return 'Gating';
    default:
      return phase;
  }
}

/** Human label for a loop outcome. */
export function outcomeLabel(outcome: string): string {
  switch (outcome) {
    case 'completed':
      return 'Completed';
    case 'plateaued':
      return 'Plateaued';
    case 'exhausted':
      return 'Exhausted';
    case 'cancelled':
      return 'Cancelled';
    case 'failed':
      return 'Failed';
    default:
      return outcome;
  }
}

/** Human label for an iteration outcome ("" reads as running). */
export function iterationOutcomeLabel(outcome: string): string {
  switch (outcome) {
    case 'accepted':
      return 'Accepted';
    case 'rejected':
      return 'Rejected';
    case 'interrupted':
      return 'Interrupted';
    case '':
      return 'Running';
    default:
      return outcome;
  }
}

/**
 * The why-waiting line: what the loop is standing on right now, from the loop
 * state's phase, phase_status, and note — so the operator always knows what
 * is happening even when no iteration has resolved yet.
 */
export function whyWaitingLine(
  loop: Pick<LoopState, 'status' | 'current_phase' | 'phase_status' | 'note' | 'outcome'> | null,
): string {
  if (!loop) return 'The loop stepper has not driven this run yet.';
  const phase = phaseLabel(loop.current_phase) || 'start';
  const lower = phase.toLowerCase();
  let line: string;
  if (loop.status === 'terminal') {
    line = 'Run finished' + (loop.current_phase ? ` at ${lower}` : '');
    if (loop.outcome) line += ` — ${outcomeLabel(loop.outcome)}`;
  } else if (loop.status === 'paused') {
    line = loop.phase_status === 'interrupted'
      ? `Paused mid-${lower} — a resume re-runs this phase.`
      : `Paused at the ${lower} boundary.`;
  } else if (loop.phase_status === 'interrupted') {
    line = `${phase} was interrupted — the next step re-runs it.`;
  } else if (loop.phase_status === 'running') {
    line = `${phase} step in flight.`;
  } else {
    line = `Waiting to start the ${lower} step.`;
  }
  if (loop.note) line += ` ${loop.note}`;
  return line;
}

const jobStatusesActive = (status: string) =>
  ['queued', 'claimed', 'running', 'paused'].includes(status);

/** The job this wizard follows: the newest active one, else the newest record. */
export function pickFollowedJob(jobs: EvolutionJob[]): EvolutionJob | null {
  const sorted = [...jobs].sort((a, b) => (a.created_at < b.created_at ? 1 : -1));
  return sorted.find((job) => jobStatusesActive(job.status)) ?? sorted[0] ?? null;
}

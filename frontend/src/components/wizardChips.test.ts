import { describe, it, expect } from 'vitest';
import {
  iterationChipClass,
  phaseChipClass,
  jobStatusChipClass,
  loopOutcomeChipClass,
  phaseLabel,
  outcomeLabel,
  iterationOutcomeLabel,
  whyWaitingLine,
} from './wizardChips';

describe('wizardChips', () => {
  it('colors iteration outcomes with the loop vocabulary', () => {
    expect(iterationChipClass('accepted')).toContain('emerald');
    expect(iterationChipClass('rejected')).toContain('rose');
    expect(iterationChipClass('interrupted')).toContain('amber');
    expect(iterationChipClass('')).toContain('blue');
  });

  it('colors phases: harness steps running, gating done, queued neutral', () => {
    for (const p of ['inference', 'maintaining', 'proposing']) {
      expect(phaseChipClass(p)).toContain('blue');
    }
    expect(phaseChipClass('gating')).toContain('emerald');
    expect(phaseChipClass('queued')).toContain('slate');
  });

  it('colors job statuses including the four terminal ones', () => {
    expect(jobStatusChipClass('running')).toContain('blue');
    expect(jobStatusChipClass('paused')).toContain('amber');
    expect(jobStatusChipClass('complete')).toContain('emerald');
    expect(jobStatusChipClass('cancelled')).toContain('rose');
    expect(jobStatusChipClass('bogus')).toContain('slate');
  });

  it('colors loop outcomes by the reason the run ended', () => {
    expect(loopOutcomeChipClass('completed')).toContain('emerald');
    expect(loopOutcomeChipClass('plateaued')).toContain('amber');
    expect(loopOutcomeChipClass('exhausted')).toContain('slate');
    expect(loopOutcomeChipClass('cancelled')).toContain('rose');
  });

  it('labels the closed vocabularies in human form', () => {
    expect(phaseLabel('inference')).toBe('Inference');
    expect(outcomeLabel('plateaued')).toBe('Plateaued');
    expect(iterationOutcomeLabel('')).toBe('Running');
    expect(iterationOutcomeLabel('interrupted')).toBe('Interrupted');
  });

  it('builds the why-waiting line from the loop state', () => {
    expect(whyWaitingLine(null)).toContain('not driven');
    expect(
      whyWaitingLine({ status: 'running', current_phase: 'inference', phase_status: 'running', note: '' }),
    ).toContain('Inference step in flight');
    expect(
      whyWaitingLine({ status: 'paused', current_phase: 'proposing', phase_status: 'interrupted', note: '' }),
    ).toContain('re-runs');
    expect(
      whyWaitingLine({ status: 'terminal', current_phase: 'gating', phase_status: '', note: '', outcome: 'plateaued' }),
    ).toContain('Plateaued');
    expect(
      whyWaitingLine({ status: 'running', current_phase: 'queued', phase_status: '', note: 'note text' }),
    ).toContain('note text');
  });
});

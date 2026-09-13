import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { WizardIterationCard } from './WizardIterationCard';
import type { IterationRecord, LoopState, SkillCandidate } from '../types';

const record: IterationRecord = {
  job_id: 'job-1',
  skill_slug: 'stability-protocol',
  iteration: 1,
  started_at: '2026-09-13T10:00:00Z',
  completed_at: '2026-09-13T10:05:00Z',
  phases: [
    { phase: 'inference', entered_at: '2026-09-13T10:00:00Z', exited_at: '2026-09-13T10:01:00Z', complete: true, note: '' },
    { phase: 'gating', entered_at: '2026-09-13T10:04:00Z', exited_at: '2026-09-13T10:05:00Z', complete: true, note: 'gate accepted candidate cand-1' },
  ],
  candidate_id: 'cand-1',
  pattern_slugs: ['pattern-a'],
  val_score: 0.8125,
  r_best: 0.4,
  outcome: 'accepted',
  result_version: 4,
};

const loop: LoopState = {
  job_id: 'job-1',
  skill_slug: 'stability-protocol',
  status: 'running',
  current_iteration: 2,
  current_phase: 'inference',
  phase_status: 'running',
  note: 'reading the wiki',
  started_at: '2026-09-13T10:00:00Z',
  updated_at: '2026-09-13T10:00:00Z',
};

const candidate: SkillCandidate = {
  id: 'cand-1',
  parent_slug: 'stability-protocol',
  parent_version: 3,
  diff: '--- a/s\n+++ b/s\n@@ -1 +1 @@\n-old line\n+new line',
  pattern_slugs: ['pattern-a', 'pattern-b'],
  proposer: 'harness',
  created_at: '2026-09-13T10:03:00Z',
  status: 'pending',
};

describe('WizardIterationCard', () => {
  it('renders the resolved record: chip, scores, phases, patterns, promotion', () => {
    render(<WizardIterationCard record={record} isCurrent={false} loop={loop} candidate={candidate} />);
    expect(screen.getByText('Iteration #1')).toBeInTheDocument();
    expect(screen.getByTestId('wizard-iteration-chip-1')).toHaveTextContent('Accepted');
    expect(screen.getByTestId('wizard-iteration-card-1').textContent).toContain('val 0.8125');
    expect(screen.getByTestId('wizard-iteration-card-1').textContent).toContain('R_best 0.4000');
    expect(screen.getByText('promoted v4')).toBeInTheDocument();
    expect(screen.getByText('pattern-b')).toBeInTheDocument();
    expect(screen.getByText('gate accepted candidate cand-1')).toBeInTheDocument();
  });

  it('marks open phase entries as interrupted', () => {
    const interrupted: IterationRecord = {
      ...record,
      outcome: 'interrupted',
      phases: [{ phase: 'maintaining', entered_at: '2026-09-13T10:01:00Z', complete: false }],
    };
    render(<WizardIterationCard record={interrupted} isCurrent={false} loop={null} />);
    expect(screen.getByTestId('wizard-iteration-chip-1')).toHaveTextContent('Interrupted');
    expect(screen.getByText('interrupted')).toBeInTheDocument();
  });

  it('shows the why-waiting line on the current in-flight iteration', () => {
    const inFlight: IterationRecord = { ...record, outcome: '', candidate_id: undefined, result_version: undefined, phases: [] };
    render(<WizardIterationCard record={inFlight} isCurrent loop={loop} />);
    expect(screen.getByTestId('wizard-iteration-chip-1')).toHaveTextContent('Running · Inference');
    expect(screen.getByTestId('wizard-iteration-waiting-1').textContent).toContain('Inference step in flight');
  });

  it('toggles the read-only candidate diff', async () => {
    render(<WizardIterationCard record={record} isCurrent={false} loop={null} candidate={candidate} />);
    expect(screen.queryByTestId('wizard-diff-view')).not.toBeInTheDocument();
    await userEvent.click(screen.getByTestId('wizard-iteration-diff-toggle-1'));
    const diff = screen.getByTestId('wizard-diff-view');
    expect(diff.textContent).toContain('+new line');
    expect(diff.textContent).toContain('-old line');
  });

  it('renders the proposed body as the diff when the candidate ships no unified diff', async () => {
    const bodyOnly: SkillCandidate = { ...candidate, diff: undefined, proposed_body: '# proposed body' };
    render(<WizardIterationCard record={record} isCurrent={false} loop={null} candidate={bodyOnly} />);
    await userEvent.click(screen.getByTestId('wizard-iteration-diff-toggle-1'));
    expect(screen.getByTestId('wizard-diff-view').textContent).toContain('# proposed body');
  });

  it('shows the candidate loading state without a diff toggle', () => {
    render(<WizardIterationCard record={record} isCurrent={false} loop={null} candidateLoading />);
    expect(screen.getByText('loading candidate…')).toBeInTheDocument();
    expect(screen.queryByTestId('wizard-iteration-diff-toggle-1')).not.toBeInTheDocument();
  });
});

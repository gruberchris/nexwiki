import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { WizardRunHeader } from './WizardRunHeader';
import { formatRunElapsed } from '../utils';
import type { EvolutionJob, LoopState } from '../types';

const job: EvolutionJob = {
  id: 'stability-protocol-job-1',
  skill_slug: 'stability-protocol',
  profile: 'opencode',
  status: 'running',
  iteration: 2,
  max_iterations: 12,
  plateau_limit: 3,
  run_deadline_at: '2026-09-13T12:00:00Z',
  created_at: '2026-09-13T10:00:00Z',
  updated_at: '2026-09-13T10:00:00Z',
};

const loop: LoopState = {
  job_id: 'stability-protocol-job-1',
  skill_slug: 'stability-protocol',
  status: 'running',
  current_iteration: 2,
  current_phase: 'gating',
  phase_status: '',
  started_at: '2026-09-13T10:00:00Z',
  updated_at: '2026-09-13T10:00:00Z',
};

const baseProps = {
  skillTitle: 'Stability Protocol',
  skillVersion: 3,
  job,
  loop,
  plateauCount: 0,
  plateauLimit: 3,
  maxIterations: 12,
  elapsedMs: 125000,
  controlBusy: null,
  onPause: vi.fn(),
  onAbort: vi.fn(),
  onApprove: vi.fn(),
  onRefresh: vi.fn(),
};

describe('formatRunElapsed', () => {
  it('formats the elapsed ladder', () => {
    expect(formatRunElapsed(0)).toBe('0:00');
    expect(formatRunElapsed(65000)).toBe('1:05');
    expect(formatRunElapsed(125000)).toBe('2:05');
    expect(formatRunElapsed(3723000)).toBe('1:02:03');
    expect(formatRunElapsed(-5)).toBe('—');
  });
});

describe('WizardRunHeader', () => {
  it('shows skill, version, run id, status, and the stop rules', () => {
    render(<WizardRunHeader {...baseProps} />);
    expect(screen.getByText('Stability Protocol')).toBeInTheDocument();
    expect(screen.getByText('v3')).toBeInTheDocument();
    expect(screen.getByText(/run stability-protocol-job-1/)).toBeInTheDocument();
    expect(screen.getByText('running')).toBeInTheDocument();
    expect(screen.getByText('max 12 iterations')).toBeInTheDocument();
    expect(screen.getByText('plateau after 3 rejected')).toBeInTheDocument();
    expect(screen.getByText('plateau 0/3')).toBeInTheDocument();
  });

  it('enables the controls on a running job', () => {
    render(<WizardRunHeader {...baseProps} />);
    expect(screen.getByTestId('wizard-pause-button')).toBeEnabled();
    expect(screen.getByTestId('wizard-abort-button')).toBeEnabled();
    expect(screen.getByTestId('wizard-approve-button')).toBeEnabled();
  });

  it('disables pause once paused and disables everything on a terminal job', () => {
    const pausedJob: EvolutionJob = { ...job, status: 'paused', checkpoint: 'before val upload' };
    const pausedLoop: LoopState = { ...loop, status: 'paused' };
    const { rerender } = render(
      <WizardRunHeader {...baseProps} job={pausedJob} loop={pausedLoop} />,
    );
    expect(screen.getByTestId('wizard-pause-button')).toBeDisabled();
    expect(screen.getByTestId('wizard-abort-button')).toBeEnabled();
    expect(screen.getByTestId('wizard-run-waiting').textContent).toContain('Paused');

    const terminalJob: EvolutionJob = { ...job, status: 'complete', loop_outcome: 'completed', completed_at: '2026-09-13T11:00:00Z' };
    const terminalLoop: LoopState = { ...loop, status: 'terminal', outcome: 'completed', outcome_reason: 'perfect score' };
    rerender(<WizardRunHeader {...baseProps} job={terminalJob} loop={terminalLoop} />);
    expect(screen.getByTestId('wizard-pause-button')).toBeDisabled();
    expect(screen.getByTestId('wizard-abort-button')).toBeDisabled();
    expect(screen.getByTestId('wizard-approve-button')).toBeDisabled();
    expect(screen.getByTestId('wizard-run-outcome')).toHaveTextContent('Completed');
    expect(screen.getByTestId('wizard-run-waiting').textContent).toContain('perfect score');
  });

  it('fires the control callbacks', async () => {
    const onAbort = vi.fn();
    render(<WizardRunHeader {...baseProps} onAbort={onAbort} />);
    await userEvent.click(screen.getByTestId('wizard-abort-button'));
    expect(onAbort).toHaveBeenCalled();
  });

  it('renders the no-run guidance honestly', () => {
    render(
      <WizardRunHeader {...baseProps} job={null} loop={null} skillVersion={undefined} />,
    );
    expect(screen.getByTestId('wizard-pause-button')).toBeDisabled();
    // Story 13: a run starts from the Data step now, not from the harness.
    expect(screen.getByTestId('wizard-run-waiting').textContent).toContain('Start one from the Data step');
  });
});

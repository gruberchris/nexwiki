import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  WizardStepSelect,
  WizardStepData,
  WizardStepBaseline,
  WizardStepReviewStub,
  WizardStepReportStub,
} from './WizardStepPanels';
import type { EvalMeta, EvolutionJob, SkillRegistryEntry } from '../types';

const skill: SkillRegistryEntry = {
  name: 'stability-protocol',
  title: 'Stability Protocol',
  description: 'The standing procedure.',
  version: 2,
  trained_state: { state: 'untrained', current_version: 2 },
};

const trainedSkill: SkillRegistryEntry = {
  ...skill,
  name: 'docker-cleanup',
  title: 'Docker Cleanup',
  trained_state: { state: 'trained', trained_at: '2026-09-12T14:03:00Z', trained_version: 3, trained_val_score: 0.9, current_version: 3 },
};

const job: EvolutionJob = {
  id: 'job-1',
  skill_slug: 'stability-protocol',
  profile: 'opencode',
  status: 'running',
  iteration: 1,
  max_iterations: 12,
  baseline_s0: 0.4,
  run_deadline_at: '2026-09-13T12:00:00Z',
  created_at: '2026-09-13T10:00:00Z',
  updated_at: '2026-09-13T10:00:00Z',
};

const evalMeta: EvalMeta = {
  job_id: 'job-1',
  skill_slug: 'stability-protocol',
  filename: 'rounds.jsonl',
  split_mode: 'auto',
  train_count: 30,
  val_count: 10,
  eval_hash: 'a'.repeat(64),
  baseline_s0: 0.4,
  scorer_version: 'v0',
  dry_run: [
    { index: 0, input_preview: 'round 1 input', expected_preview: 'expected 1', pass: true },
    { index: 1, input_preview: 'round 2 input', expected_preview: 'expected 2', pass: false },
  ],
  estimate: { estimated_val_cases: 10, estimated_cost_usd: 0.02, budget_enforced: false, note: 'static stub' },
  uploaded_at: '2026-09-13T10:02:00Z',
};

describe('WizardStepSelect', () => {
  it('lists every skill with its trained badge and marks the selection', () => {
    render(
      <WizardStepSelect
        skills={[skill, trainedSkill]}
        selectedSlug="stability-protocol"
        loading={false}
        onSelectSkill={vi.fn()}
      />,
    );
    expect(screen.getByTestId('wizard-picker-stability-protocol')).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByTestId('wizard-picker-docker-cleanup')).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByText('Untrained')).toBeInTheDocument();
    expect(screen.getByText('Trained')).toBeInTheDocument();
  });

  it('calls onSelectSkill on a picker row click', async () => {
    const onSelect = vi.fn();
    render(
      <WizardStepSelect skills={[skill]} selectedSlug="" loading={false} onSelectSkill={onSelect} />,
    );
    await userEvent.click(screen.getByTestId('wizard-picker-stability-protocol'));
    expect(onSelect).toHaveBeenCalledWith('stability-protocol');
  });

  it('renders the empty-state guidance when no skills exist', () => {
    render(<WizardStepSelect skills={[]} selectedSlug="" loading={false} onSelectSkill={vi.fn()} />);
    expect(screen.getByText('No skills registered yet')).toBeInTheDocument();
  });
});

describe('WizardStepData', () => {
  it('renders the no-run guidance honestly', () => {
    render(<WizardStepData job={null} evalMeta={null} />);
    expect(screen.getByText('No evolution run yet')).toBeInTheDocument();
  });

  it('renders the waiting state before the eval upload lands', () => {
    render(<WizardStepData job={job} evalMeta={null} />);
    expect(screen.getByText('No eval set uploaded yet')).toBeInTheDocument();
    expect(screen.getByText(/upload_evolution_eval_set/)).toBeInTheDocument();
  });

  it('renders the accepted upload summary from server state', () => {
    render(<WizardStepData job={job} evalMeta={evalMeta} />);
    expect(screen.getByText('Train cases')).toBeInTheDocument();
    expect(screen.getByText('30')).toBeInTheDocument();
    expect(screen.getByText('10')).toBeInTheDocument();
    expect(screen.getByText('rounds.jsonl')).toBeInTheDocument();
    expect(screen.getAllByText(/eval hash/).length).toBeGreaterThan(0);
  });
});

describe('WizardStepBaseline', () => {
  it('renders the no-run and no-baseline waiting states', () => {
    render(<WizardStepBaseline job={null} evalMeta={null} />);
    expect(screen.getByText('No evolution run yet')).toBeInTheDocument();
    // A job without a scored baseline is still waiting; the fixture job carries
    // S0 only once the eval gate ran.
    const { rerender } = render(<WizardStepBaseline job={{ ...job, baseline_s0: undefined }} evalMeta={null} />);
    expect(screen.getByText('No baseline yet')).toBeInTheDocument();
    rerender(<WizardStepBaseline job={job} evalMeta={evalMeta} />);
    expect(screen.getByText('40%')).toBeInTheDocument();
  });

  it('renders S0, the dry-run samples, and the estimate stub', () => {
    render(<WizardStepBaseline job={job} evalMeta={evalMeta} />);
    expect(screen.getByText('40%')).toBeInTheDocument();
    expect(screen.getByTestId('wizard-baseline-bar')).toBeInTheDocument();
    expect(screen.getByText('Dry run (2 val samples)')).toBeInTheDocument();
    expect(screen.getByText(/\[0\] round 1 input/)).toBeInTheDocument();
    expect(screen.getByText(/Cost estimate/)).toBeInTheDocument();
  });
});

describe('story-09 stubs', () => {
  it('explains what story 09 adds and navigates back without faking data', async () => {
    const onBack = vi.fn();
    render(<WizardStepReviewStub onBack={onBack} />);
    expect(screen.getAllByText(/story 09/i).length).toBeGreaterThan(0);
    await userEvent.click(screen.getByText('Back to the live view'));
    expect(onBack).toHaveBeenCalled();

    render(<WizardStepReportStub onBack={onBack} />);
    expect(screen.getAllByText(/story 09/i).length).toBeGreaterThan(0);
  });
});

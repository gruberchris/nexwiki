import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { TrainedBadge } from './TrainedBadge';
import { trainedBadgeTooltip } from './trainedBadgeState';
import type { SkillTrainedState } from '../types';

const trained: SkillTrainedState = {
  state: 'trained',
  trained_at: '2026-09-12T14:03:00Z',
  trained_version: 3,
  trained_val_score: 0.8125,
  current_version: 3,
};

const stale: SkillTrainedState = {
  ...trained,
  state: 'stale',
  reason: 'skill changed after training: trained at v3, now v5',
};

const untrained: SkillTrainedState = {
  state: 'untrained',
  current_version: 1,
};

describe('TrainedBadge', () => {
  it('renders the trained state with its label', () => {
    render(<TrainedBadge state={trained} />);
    expect(screen.getByTestId('trained-badge')).toHaveAttribute('data-trained-state', 'trained');
    expect(screen.getByText('Trained')).toBeInTheDocument();
  });

  it('renders stale and untrained labels', () => {
    const { rerender } = render(<TrainedBadge state={stale} />);
    expect(screen.getByText('Stale')).toBeInTheDocument();
    rerender(<TrainedBadge state={untrained} />);
    expect(screen.getByText('Untrained')).toBeInTheDocument();
  });

  it('renders a loading pill when no state is known yet', () => {
    render(<TrainedBadge state={null} />);
    expect(screen.getByTestId('trained-badge-loading')).toBeInTheDocument();
  });

  it('puts the training details on the tooltip', () => {
    render(<TrainedBadge state={trained} />);
    const tooltip = screen.getByTestId('trained-badge').getAttribute('title') ?? '';
    expect(tooltip).toContain('Trained');
    expect(tooltip).toContain('v3');
    expect(tooltip).toContain('0.8125');
  });

  it('puts the stale reason on the tooltip', () => {
    render(<TrainedBadge state={stale} />);
    const tooltip = screen.getByTestId('trained-badge').getAttribute('title') ?? '';
    expect(tooltip).toContain('skill changed after training');
  });

  it('builds a tooltip directly for the shared helper', () => {
    const tooltip = trainedBadgeTooltip(untrained);
    expect(tooltip).toContain('No trained marker yet.');
    expect(tooltip).toContain('Live skill: v1');
  });
});

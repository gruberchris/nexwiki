import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { WizardScoreStrip } from './WizardScoreStrip';
import type { IterationRecord } from '../types';

const rec = (n: number, outcome: string, val = 0.5, best = 0.4): IterationRecord => ({
  job_id: 'job-1',
  skill_slug: 's',
  iteration: n,
  started_at: '2026-09-13T10:00:00Z',
  phases: [],
  val_score: val,
  r_best: best,
  outcome,
});

describe('WizardScoreStrip', () => {
  it('renders one chip per recorded iteration with the score delta on the tooltip', () => {
    render(
      <WizardScoreStrip
        iterations={[rec(1, 'accepted', 0.5, 0.4), rec(2, 'rejected', 0.3, 0.5)]}
        currentIteration={3}
        currentPhase="inference"
      />,
    );
    expect(screen.getByText('#1')).toBeInTheDocument();
    expect(screen.getByText('#2')).toBeInTheDocument();
    // The trajectory numbers live on each chip's tooltip, not the strip itself.
    expect(screen.getByTitle(/val 0.5000 vs R_best 0.4000/)).toBeInTheDocument();
    expect(screen.getByTitle(/val 0.3000 vs R_best 0.5000/)).toBeInTheDocument();
  });

  it('renders a live chip for the in-flight iteration', () => {
    render(<WizardScoreStrip iterations={[rec(1, 'accepted')]} currentIteration={2} currentPhase="gating" />);
    expect(screen.getByTestId('wizard-score-strip-live')).toHaveTextContent('#2');
  });

  it('renders nothing without data', () => {
    const { container } = render(<WizardScoreStrip iterations={[]} />);
    expect(container).toBeEmptyDOMElement();
  });
});

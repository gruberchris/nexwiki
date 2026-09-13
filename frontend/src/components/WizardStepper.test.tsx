import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { WizardStepper, type WizardStepInfo } from './WizardStepper';

const steps: WizardStepInfo[] = [
  { id: 1, title: 'Select', hint: 'Pick the skill', status: 'done' },
  { id: 2, title: 'Data', hint: 'Waiting', status: 'done' },
  { id: 3, title: 'Baseline', hint: 'Scored', status: 'available' },
  { id: 4, title: 'Evolve', hint: 'Run running', status: 'available' },
  { id: 5, title: 'Review', hint: 'Story 09', status: 'available' },
  { id: 6, title: 'Report', hint: 'Story 09', status: 'available' },
];

describe('WizardStepper', () => {
  it('renders all six steps with their hints', () => {
    render(<WizardStepper steps={steps} currentStep={4} onSelectStep={vi.fn()} />);
    for (const title of ['Select', 'Data', 'Baseline', 'Evolve', 'Review', 'Report']) {
      expect(screen.getByText(title)).toBeInTheDocument();
    }
    expect(screen.getByText('Run running')).toBeInTheDocument();
  });

  it('marks the current step with aria-current and renders done ticks', () => {
    render(<WizardStepper steps={steps} currentStep={3} onSelectStep={vi.fn()} />);
    expect(screen.getByRole('button', { name: /3 · Baseline|Baseline/ })).toHaveAttribute('aria-current', 'step');
    expect(screen.getByTestId('wizard-step-dot-1')).toHaveTextContent('✓');
    expect(screen.getByTestId('wizard-step-dot-3')).toHaveTextContent('3');
  });

  it('calls onSelectStep for an available step', async () => {
    const onSelect = vi.fn();
    render(<WizardStepper steps={steps} currentStep={1} onSelectStep={onSelect} />);
    await userEvent.click(screen.getByText('Evolve'));
    expect(onSelect).toHaveBeenCalledWith(4);
  });

  it('disables locked steps', async () => {
    const onSelect = vi.fn();
    const locked: WizardStepInfo[] = steps.map((s) =>
      s.id === 4 ? { ...s, status: 'locked' as const } : s,
    );
    render(<WizardStepper steps={locked} currentStep={1} onSelectStep={onSelect} />);
    const lockedButton = screen.getByText('Evolve').closest('button');
    expect(lockedButton).toBeDisabled();
    await userEvent.click(screen.getByText('Evolve'));
    expect(onSelect).not.toHaveBeenCalled();
  });
});

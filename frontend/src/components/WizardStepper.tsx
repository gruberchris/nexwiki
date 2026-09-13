import React from 'react';

/**
 * The wizard's left rail (story 08): the six steps of the WikiSkill evolution
 * wizard with their live readiness — which ones are done, which is active,
 * and which are still locked because the run has not reached them.
 *
 * Readiness comes from server state, never from guessed progress: a step is
 * "done" when the run actually passed it, so a refresh or a crash resume
 * cannot desynchronize the rail from the truth.
 */

export type WizardStepId = 1 | 2 | 3 | 4 | 5 | 6;

export type WizardStepStatus = 'done' | 'active' | 'available' | 'locked';

export interface WizardStepInfo {
  id: WizardStepId;
  title: string;
  hint: string;
  status: WizardStepStatus;
}

interface WizardStepperProps {
  steps: WizardStepInfo[];
  currentStep: WizardStepId;
  onSelectStep: (step: WizardStepId) => void;
}

const STEP_DOT_CLASSES: Record<WizardStepStatus, string> = {
  done: 'bg-emerald-500 text-white dark:bg-emerald-400 dark:text-slate-950',
  active: 'bg-themeAccent text-white',
  available: 'bg-themeBgSecondary text-themeTextMuted border border-themeBorder',
  locked: 'bg-themeBgSecondary text-themeTextMuted border border-themeBorder opacity-50',
};

export const WizardStepper: React.FC<WizardStepperProps> = ({ steps, currentStep, onSelectStep }) => {
  return (
    <nav
      aria-label="Training wizard steps"
      className="flex flex-col gap-1 p-4"
    >
      {steps.map((step, index) => {
        const selectable = step.status !== 'locked';
        const isActive = step.id === currentStep;
        return (
          <div key={step.id} className="relative">
            {index < steps.length - 1 && (
              <span
                aria-hidden="true"
                className="absolute left-[15px] top-9 bottom-0 w-px bg-themeBorder"
              />
            )}
            <button
              type="button"
              onClick={() => onSelectStep(step.id)}
              disabled={!selectable}
              aria-current={isActive ? 'step' : undefined}
              className={`w-full flex items-start gap-3 rounded-xl px-2 py-2 text-left transition-colors cursor-pointer ${
                isActive
                  ? 'bg-themeAccentBg'
                  : selectable
                    ? 'hover:bg-themeAccentBg/60'
                    : 'cursor-not-allowed'
              }`}
            >
              <span
                data-testid={`wizard-step-dot-${step.id}`}
                className={`flex items-center justify-center w-6 h-6 shrink-0 rounded-full text-[11px] font-bold ${STEP_DOT_CLASSES[step.status]}`}
              >
                {step.status === 'done' ? '✓' : step.id}
              </span>
              <span className="min-w-0">
                <span
                  className={`block text-xs font-bold leading-6 ${
                    isActive ? 'text-themeAccent' : selectable ? 'text-themeTextSecondary' : 'text-themeTextMuted'
                  }`}
                >
                  {step.title}
                </span>
                <span className="block text-[10px] text-themeTextMuted leading-tight">{step.hint}</span>
              </span>
            </button>
          </div>
        );
      })}
    </nav>
  );
};

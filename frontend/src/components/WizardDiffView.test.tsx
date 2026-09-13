import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { WizardDiffView } from './WizardDiffView';

const diff = [
  'diff --git a/skill.md b/skill.md',
  'index abc..def 100644',
  '--- a/skill.md',
  '+++ b/skill.md',
  '@@ -1,3 +1,4 @@',
  'unchanged context',
  '-removed line',
  '+added line',
].join('\n');

describe('WizardDiffView', () => {
  it('renders every line read-only', () => {
    render(<WizardDiffView diff={diff} />);
    const view = screen.getByTestId('wizard-diff-view');
    expect(view.textContent).toContain('added line');
    expect(view.textContent).toContain('removed line');
    expect(view.textContent).toContain('unchanged context');
    expect(view.textContent).toContain('Candidate diff (read-only)');
  });

  it('renders an empty diff without crashing', () => {
    render(<WizardDiffView diff="" />);
    expect(screen.getByTestId('wizard-diff-view')).toBeInTheDocument();
  });
});

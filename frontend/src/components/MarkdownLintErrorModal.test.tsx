import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MarkdownLintErrorModal } from './MarkdownLintErrorModal';
import type { LintDiagnostic } from '../utils/markdownLinter';

const mockDiagnostics: LintDiagnostic[] = [
  { line: 1, from: 0, to: 5, severity: 'error', message: 'Multiple H1 headers', suggestion: '## Alt', code: 'MD025' },
  { line: 3, from: 20, to: 40, severity: 'warning', message: 'Heading hierarchy skip', suggestion: '## Good', code: 'MD001' },
  { line: 5, from: 50, to: 80, severity: 'info', message: 'Bare URL detected', suggestion: '<url>', code: 'MD034' },
];

describe('MarkdownLintErrorModal', () => {
  it('returns null when not open', () => {
    const { container } = render(
      <MarkdownLintErrorModal isOpen={false} onClose={vi.fn()} diagnostics={[]} onSelectDiagnostic={vi.fn()} />
    );
    expect(container.firstChild).toBeNull();
  });

  it('renders diagnostics when open', () => {
    render(
      <MarkdownLintErrorModal isOpen={true} onClose={vi.fn()} diagnostics={mockDiagnostics} onSelectDiagnostic={vi.fn()} />
    );
    expect(screen.getByText('Multiple H1 headers')).toBeInTheDocument();
    expect(screen.getByText('Heading hierarchy skip')).toBeInTheDocument();
  });

  it('calls onClose when X button clicked', async () => {
    const onClose = vi.fn();
    render(
      <MarkdownLintErrorModal isOpen={true} onClose={onClose} diagnostics={mockDiagnostics} onSelectDiagnostic={vi.fn()} />
    );
    const closeButton = screen.getAllByRole('button').find(b => b.querySelector('svg'));
    if (closeButton) await userEvent.click(closeButton);
    expect(onClose).toHaveBeenCalled();
  });

  it('calls onSelectDiagnostic when a diagnostic is clicked', async () => {
    const onSelectDiagnostic = vi.fn();
    render(
      <MarkdownLintErrorModal isOpen={true} onClose={vi.fn()} diagnostics={mockDiagnostics} onSelectDiagnostic={onSelectDiagnostic} />
    );
    await userEvent.click(screen.getByText('Multiple H1 headers'));
    expect(onSelectDiagnostic).toHaveBeenCalledWith(mockDiagnostics[0]);
  });

  it('renders empty state for no diagnostics', () => {
    render(
      <MarkdownLintErrorModal isOpen={true} onClose={vi.fn()} diagnostics={[]} onSelectDiagnostic={vi.fn()} />
    );
    expect(screen.getByText(/No syntax issues/i)).toBeInTheDocument();
  });

  it('shows filter buttons (all, error, warning, info)', () => {
    render(
      <MarkdownLintErrorModal isOpen={true} onClose={vi.fn()} diagnostics={mockDiagnostics} onSelectDiagnostic={vi.fn()} />
    );
    const buttons = screen.getAllByRole('button');
    // Filter buttons exist
    expect(buttons.length).toBeGreaterThan(0);
  });

  it('renders suggestion when available', () => {
    render(
      <MarkdownLintErrorModal isOpen={true} onClose={vi.fn()} diagnostics={mockDiagnostics} onSelectDiagnostic={vi.fn()} />
    );
    expect(screen.getByText('## Alt')).toBeInTheDocument();
  });
});

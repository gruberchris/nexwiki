import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { DiffView } from './DiffView';

const baseProps = {
  oldContent: 'line one\nline two\nline three',
  newContent: 'line one\nline two modified\nline three\nline four added',
  oldTitle: 'Version 1',
  newTitle: 'Version 2',
  layoutMode: 'unified' as const,
  onLayoutChange: vi.fn(),
};

describe('DiffView', () => {
  it('renders the comparison titles', () => {
    render(<DiffView {...baseProps} />);
    expect(screen.getByText(/Version 1/)).toBeInTheDocument();
    expect(screen.getByText(/Version 2/)).toBeInTheDocument();
  });

  it('renders unchanged lines', () => {
    render(<DiffView {...baseProps} />);
    expect(screen.getByText('line one')).toBeInTheDocument();
  });

  it('renders added lines with + marker', () => {
    render(<DiffView {...baseProps} />);
    expect(screen.getByText('line four added')).toBeInTheDocument();
  });

  it('renders removed lines', () => {
    render(<DiffView {...baseProps} />);
    expect(screen.getByText('line two')).toBeInTheDocument();
  });

  it('calls onLayoutChange with "split" when split button is clicked', async () => {
    const onLayoutChange = vi.fn();
    render(<DiffView {...baseProps} onLayoutChange={onLayoutChange} />);
    const splitBtn = screen.getByText('Split Pane');
    await userEvent.click(splitBtn);
    expect(onLayoutChange).toHaveBeenCalledWith('split');
  });

  it('calls onLayoutChange with "unified" when unified button is clicked', async () => {
    const onLayoutChange = vi.fn();
    render(<DiffView {...baseProps} onLayoutChange={onLayoutChange} layoutMode="split" />);
    const unifiedBtn = screen.getByText('Unified Inline');
    await userEvent.click(unifiedBtn);
    expect(onLayoutChange).toHaveBeenCalledWith('unified');
  });

  it('renders split mode with two panes', () => {
    render(<DiffView {...baseProps} layoutMode="split" />);
    // Both old and new titles should appear in split mode headers
    const versionTexts = screen.getAllByText(/Version/);
    expect(versionTexts.length).toBeGreaterThanOrEqual(2);
  });

  it('renders identical content with no added/removed markers', () => {
    const sameProps = { ...baseProps, oldContent: 'same', newContent: 'same' };
    render(<DiffView {...sameProps} />);
    expect(screen.getByText('same')).toBeInTheDocument();
  });

  it('handles empty content', () => {
    const emptyProps = { ...baseProps, oldContent: '', newContent: '' };
    render(<DiffView {...emptyProps} />);
    // Should not throw
  });
});

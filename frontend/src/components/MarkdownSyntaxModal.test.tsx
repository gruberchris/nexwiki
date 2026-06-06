import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MarkdownSyntaxModal } from './MarkdownSyntaxModal';

describe('MarkdownSyntaxModal', () => {
  it('returns null when not open', () => {
    const { container } = render(<MarkdownSyntaxModal isOpen={false} onClose={vi.fn()} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders heading categories when open', () => {
    render(<MarkdownSyntaxModal isOpen={true} onClose={vi.fn()} />);
    // "Headings" appears as a category title
    expect(screen.getAllByText('Headings').length).toBeGreaterThan(0);
    // "Emphasis" may appear multiple times (title + icon label)
    expect(screen.getAllByText('Emphasis').length).toBeGreaterThan(0);
  });

  it('renders syntax examples', () => {
    render(<MarkdownSyntaxModal isOpen={true} onClose={vi.fn()} />);
    expect(screen.getByText('# Heading 1')).toBeInTheDocument();
    expect(screen.getByText('## Heading 2')).toBeInTheDocument();
  });

  it('calls onClose when close button is clicked', async () => {
    const onClose = vi.fn();
    render(<MarkdownSyntaxModal isOpen={true} onClose={onClose} />);
    const buttons = screen.getAllByRole('button');
    await userEvent.click(buttons[0]);
    expect(onClose).toHaveBeenCalled();
  });

  it('closes on Escape key when open', async () => {
    const onClose = vi.fn();
    render(<MarkdownSyntaxModal isOpen={true} onClose={onClose} />);
    await userEvent.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalled();
  });

  it('does not close on Escape when not open', async () => {
    const onClose = vi.fn();
    render(<MarkdownSyntaxModal isOpen={false} onClose={onClose} />);
    await userEvent.keyboard('{Escape}');
    expect(onClose).not.toHaveBeenCalled();
  });
});

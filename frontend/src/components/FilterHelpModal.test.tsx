import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { FilterHelpModal } from './FilterHelpModal';

describe('FilterHelpModal', () => {
  it('renders the filter syntax heading', () => {
    render(<FilterHelpModal onClose={vi.fn()} />);
    expect(screen.getByText('Filter Syntax')).toBeInTheDocument();
  });

  it('renders filter syntax examples', () => {
    render(<FilterHelpModal onClose={vi.fn()} />);
    // Multiple elements contain "nexwiki" (code element + description span)
    const nexwikiElements = screen.getAllByText(/nexwiki/);
    expect(nexwikiElements.length).toBeGreaterThan(0);
  });

  it('calls onClose when the close button is clicked', async () => {
    const onClose = vi.fn();
    render(<FilterHelpModal onClose={onClose} />);
    const closeButton = screen.getAllByRole('button')[0];
    await userEvent.click(closeButton);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('calls onClose when the backdrop overlay is clicked', async () => {
    const onClose = vi.fn();
    const { container } = render(<FilterHelpModal onClose={onClose} />);
    const backdrop = container.firstChild as HTMLElement;
    await userEvent.click(backdrop);
    expect(onClose).toHaveBeenCalled();
  });

  it('does not call onClose when clicking inside the modal content', async () => {
    const onClose = vi.fn();
    render(<FilterHelpModal onClose={onClose} />);
    await userEvent.click(screen.getByText('Filter Syntax'));
    expect(onClose).not.toHaveBeenCalled();
  });
});

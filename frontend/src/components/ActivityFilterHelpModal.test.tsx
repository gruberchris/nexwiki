import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ActivityFilterHelpModal } from './ActivityFilterHelpModal';

describe('ActivityFilterHelpModal', () => {
  it('renders activity filter syntax heading', () => {
    render(<ActivityFilterHelpModal onClose={vi.fn()} />);
    expect(screen.getByText('Activity Filter Syntax')).toBeInTheDocument();
  });

  it('calls onClose when backdrop is clicked', async () => {
    const onClose = vi.fn();
    const { container } = render(<ActivityFilterHelpModal onClose={onClose} />);
    const backdrop = container.firstChild as HTMLElement;
    await userEvent.click(backdrop);
    expect(onClose).toHaveBeenCalled();
  });

  it('calls onClose when close button is clicked', async () => {
    const onClose = vi.fn();
    render(<ActivityFilterHelpModal onClose={onClose} />);
    const buttons = screen.getAllByRole('button');
    await userEvent.click(buttons[0]);
    expect(onClose).toHaveBeenCalled();
  });
});

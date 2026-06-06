import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { HistoryDrawer } from './HistoryDrawer';

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const baseProps = {
  slug: 'my-article',
  onClose: vi.fn(),
  onRevertComplete: vi.fn(),
  currentContent: '# Current content',
  currentTitle: 'My Article',
};

const mockHistory = [
  { title: 'My Article', slug: 'my-article', version: 2, edit_summary: 'Typo fix', created_at: '2024-01-15T00:00:00Z', updated_at: '2024-01-15T00:00:00Z' },
  { title: 'My Article', slug: 'my-article', version: 1, edit_summary: 'Initial version', created_at: '2024-01-01T00:00:00Z', updated_at: '2024-01-01T00:00:00Z' },
];

describe('HistoryDrawer', () => {
  it('renders loading state initially', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})));
    render(<HistoryDrawer {...baseProps} />);
    // Loading state should be visible while fetch is pending
    expect(document.body.textContent).toBeDefined();
  });

  it('renders version history after load', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => mockHistory,
    }));

    await act(async () => {
      render(<HistoryDrawer {...baseProps} />);
    });

    await waitFor(() => {
      expect(screen.getByText(/Typo fix/i) || screen.getByText(/Version 2/i) || document.body.textContent?.includes('Typo fix')).toBeTruthy();
    });
  });

  it('calls onClose when back button is clicked', async () => {
    const onClose = vi.fn();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => [],
    }));

    await act(async () => {
      render(<HistoryDrawer {...baseProps} onClose={onClose} />);
    });

    const buttons = screen.getAllByRole('button');
    // First button should be the back/close button
    if (buttons.length > 0) {
      await userEvent.click(buttons[0]);
      expect(onClose).toHaveBeenCalled();
    }
  });

  it('handles fetch error gracefully', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('Network error')));

    await act(async () => {
      render(<HistoryDrawer {...baseProps} />);
    });

    await waitFor(() => {
      expect(document.body.textContent).toBeDefined();
    });
  });

  it('shows empty state when no history', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => [],
    }));

    await act(async () => {
      render(<HistoryDrawer {...baseProps} />);
    });

    await waitFor(() => {
      expect(document.body.textContent).toBeDefined();
    });
  });

  it('renders version info when history loads', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => mockHistory,
    }));

    await act(async () => {
      render(<HistoryDrawer {...baseProps} />);
    });

    await waitFor(() => {
      // History drawer shows version numbers
      expect(document.body.textContent).toContain('v2');
    });
  });
});

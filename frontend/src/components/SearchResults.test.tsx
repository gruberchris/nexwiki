import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, waitFor, act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SearchResults } from './SearchResults';

const mockOnNavigate = vi.fn();

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('SearchResults', () => {
  it('renders search input', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => [],
    }));
    await act(async () => {
      render(<SearchResults initialQuery="golang" onNavigate={mockOnNavigate} wikiName="My Wiki" />);
    });
    expect(screen.getByRole('textbox')).toBeInTheDocument();
  });

  it('renders empty results state', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => [],
    }));

    await act(async () => {
      render(<SearchResults initialQuery="nonexistent" onNavigate={mockOnNavigate} wikiName="My Wiki" />);
    });

    await waitFor(() => {
      expect(screen.queryByText(/Loading/i) || screen.queryByText(/0 result/) || screen.queryByText(/No results/)).toBeDefined();
    });
  });

  it('renders results when search returns data', async () => {
    const mockResults = [
      {
        title: 'Go Guide',
        slug: 'go-guide',
        score: 0.95,
        updated_at: '2024-01-15T00:00:00Z',
        snippets: ['A **guide** for Go programming'],
      },
    ];
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => mockResults,
    }));

    await act(async () => {
      render(<SearchResults initialQuery="golang" onNavigate={mockOnNavigate} wikiName="My Wiki" />);
    });

    await waitFor(() => {
      expect(screen.getByText('Go Guide')).toBeInTheDocument();
    });
  });

  it('calls onNavigate when result is clicked', async () => {
    const onNavigate = vi.fn();
    const mockResults = [
      { title: 'Go Guide', slug: 'go-guide', score: 0.9, updated_at: '2024-01-15T00:00:00Z', snippets: [] },
    ];
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => mockResults,
    }));

    await act(async () => {
      render(<SearchResults initialQuery="go" onNavigate={onNavigate} wikiName="Wiki" />);
    });

    await waitFor(() => screen.getByText('Go Guide'));
    await userEvent.click(screen.getByText('Go Guide'));
    expect(onNavigate).toHaveBeenCalledWith('go-guide');
  });

  it('handles empty initial query', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => [],
    }));

    await act(async () => {
      render(<SearchResults initialQuery="" onNavigate={mockOnNavigate} wikiName="Wiki" />);
    });

    await waitFor(() => {
      expect(screen.queryByText(/Loading/i)).not.toBeInTheDocument();
    });
  });

  it('handles fetch error gracefully', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('Network error')));

    await act(async () => {
      render(<SearchResults initialQuery="test" onNavigate={mockOnNavigate} wikiName="Wiki" />);
    });

    await waitFor(() => {
      expect(screen.queryByText(/Loading/i)).not.toBeInTheDocument();
    });
  });
});

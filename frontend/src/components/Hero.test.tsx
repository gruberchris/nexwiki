import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Hero } from './Hero';
import type { Article } from '../types';

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  sessionStorage.clear();
});

const mockFetch = (data: unknown = { tags: ['completed', 'wip', 'draft'] }) => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
    ok: true,
    json: async () => data,
  }));
};

const mockArticles: Article[] = [
  { type: 'Wiki', title: 'Go Guide', slug: 'go-guide', tags: ['golang'], created_at: '2024-01-01T00:00:00Z', timestamp: '2024-01-15T00:00:00Z', version: 1 },
  { type: 'AI-Agent-Plan', title: 'AI Plan', slug: 'ai-plan', tags: ['nexwiki', 'implementing'], created_at: '2024-01-01T00:00:00Z', timestamp: '2024-01-15T00:00:00Z', version: 1 },
  { type: 'AI-Agent-Skill', title: 'My Skill', slug: 'my-skill', tags: [], created_at: '2024-01-01T00:00:00Z', timestamp: '2024-01-15T00:00:00Z', version: 1 },
  { type: 'AI-Agent-Memory', title: 'AI Memory', slug: 'ai-memory', tags: ['memory-rules'], created_at: '2024-01-01T00:00:00Z', timestamp: '2024-01-15T00:00:00Z', version: 1 },
];

describe('Hero', () => {
  it('renders wiki name', async () => {
    mockFetch();
    await act(async () => {
      render(<Hero articles={[]} onNavigate={vi.fn()} onCreateNew={vi.fn()} wikiName="Test Wiki" />);
    });
    await waitFor(() => expect(screen.getByText('Test Wiki')).toBeInTheDocument());
  });

  it('renders with articles (shows article count)', async () => {
    mockFetch();
    await act(async () => {
      render(<Hero articles={mockArticles} onNavigate={vi.fn()} onCreateNew={vi.fn()} wikiName="Wiki" />);
    });
    // The article count badge shows total articles
    expect(screen.getByText(String(mockArticles.length))).toBeInTheDocument();
  });

  it('shows "Create Wiki Article" action card', async () => {
    mockFetch();
    await act(async () => {
      render(<Hero articles={[]} onNavigate={vi.fn()} onCreateNew={vi.fn()} wikiName="Wiki" />);
    });
    expect(screen.getByText('Create Wiki Article')).toBeInTheDocument();
  });

  it('calls onCreateNew("article") when create article card clicked', async () => {
    const onCreateNew = vi.fn();
    mockFetch();
    await act(async () => {
      render(<Hero articles={[]} onNavigate={vi.fn()} onCreateNew={onCreateNew} wikiName="Wiki" />);
    });
    await userEvent.click(screen.getByText('Create Wiki Article'));
    expect(onCreateNew).toHaveBeenCalledWith('article');
  });

  it('handles fetch error for status tags gracefully', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('Network error')));
    await act(async () => {
      render(<Hero articles={[]} onNavigate={vi.fn()} onCreateNew={vi.fn()} wikiName="Wiki" />);
    });
    // Should not crash
    expect(screen.getByText('Wiki')).toBeInTheDocument();
  });

  it('renders directory sections', async () => {
    mockFetch();
    await act(async () => {
      render(<Hero articles={mockArticles} onNavigate={vi.fn()} onCreateNew={vi.fn()} wikiName="Wiki" />);
    });
    expect(screen.getByText(/Wiki Index/i)).toBeInTheDocument();
  });

  it('shows agent plan card', async () => {
    mockFetch();
    await act(async () => {
      render(<Hero articles={[]} onNavigate={vi.fn()} onCreateNew={vi.fn()} wikiName="Wiki" />);
    });
    expect(screen.getByText('Create Agent Plan')).toBeInTheDocument();
  });

  it('calls onCreateNew("plan") when agent plan card clicked', async () => {
    const onCreateNew = vi.fn();
    mockFetch();
    await act(async () => {
      render(<Hero articles={[]} onNavigate={vi.fn()} onCreateNew={onCreateNew} wikiName="Wiki" />);
    });
    await userEvent.click(screen.getByText('Create Agent Plan'));
    expect(onCreateNew).toHaveBeenCalledWith('plan');
  });
});

const completedPlan: Article = {
  type: 'AI-Agent-Plan', title: 'Old Plan', slug: 'old-plan', tags: ['nexwiki', 'completed'],
  created_at: '2024-01-01T00:00:00Z', timestamp: '2024-01-15T00:00:00Z', version: 1,
};

const renderHero = (articles: Article[], restoreUiState = false) =>
  act(async () => {
    render(<Hero articles={articles} onNavigate={vi.fn()} onCreateNew={vi.fn()} wikiName="Wiki" restoreUiState={restoreUiState} />);
  });

describe('Hero plans filter default', () => {
  it('defaults the Agent Plans filter to the open-work inclusion list, hiding completed plans', async () => {
    mockFetch();
    await renderHero([...mockArticles, completedPlan]);
    await userEvent.click(screen.getByRole('button', { name: /Agent Plans/ }));

    expect(screen.getByPlaceholderText('Filter plans by title or tag...')).toHaveValue('draft || implementing || blocked');
    expect(screen.getByText('AI Plan')).toBeInTheDocument();
    expect(screen.queryByText('Old Plan')).not.toBeInTheDocument();
  });

  // A filter the user did not type must be obvious and clearable, or missing plans look like
  // data loss. The default lives in the input itself, so the standard clear (X) removes it.
  it('clearing the default filter reveals completed plans', async () => {
    mockFetch();
    await renderHero([...mockArticles, completedPlan]);
    await userEvent.click(screen.getByRole('button', { name: /Agent Plans/ }));

    await userEvent.click(screen.getByRole('button', { name: /clear filter/i }));
    expect(screen.getByText('Old Plan')).toBeInTheDocument();
  });
});

describe('Hero dashboard state persistence', () => {
  it('round-trips filters and expanded sections through a simulated back-navigation', async () => {
    mockFetch();
    let unmountHero: () => void;
    await act(async () => {
      const { unmount } = render(
        <Hero articles={mockArticles} onNavigate={vi.fn()} onCreateNew={vi.fn()} wikiName="Wiki" />,
      );
      unmountHero = unmount;
    });
    await userEvent.click(screen.getByRole('button', { name: /Wiki Index/ }));
    await userEvent.type(screen.getByPlaceholderText('Filter articles by title or tag...'), 'go');

    unmountHero!(); // navigating to an article unmounts the dashboard and saves its state

    await renderHero(mockArticles, true); // pressing Back remounts with restoreUiState
    expect(screen.getByPlaceholderText('Filter articles by title or tag...')).toHaveValue('go');
  });

  it('a deliberate navigation home gets a clean dashboard despite saved state', async () => {
    mockFetch();
    sessionStorage.setItem(
      'nexwiki-home-state',
      JSON.stringify({ wikiSearchQuery: 'stale', wikiExpanded: true, plansSearchQuery: 'stale-too' }),
    );

    await renderHero(mockArticles, false);
    // The Wiki Index section is collapsed (its filter input is not even rendered) and the
    // plans filter is back to its default.
    expect(screen.queryByPlaceholderText('Filter articles by title or tag...')).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /Agent Plans/ }));
    expect(screen.getByPlaceholderText('Filter plans by title or tag...')).toHaveValue('draft || implementing || blocked');
  });
});

describe('Hero archived visibility', () => {
  const archivedArticle: Article = {
    type: 'Wiki', title: 'Retired Guide', slug: 'retired-guide', tags: ['archived'],
    created_at: '2024-01-01T00:00:00Z', timestamp: '2024-01-15T00:00:00Z', version: 1,
    archived_at: '2024-02-01T00:00:00Z',
  };

  it('hides archived documents from the dashboard by default, even with a cleared filter', async () => {
    mockFetch();
    await renderHero([...mockArticles, archivedArticle]);
    await userEvent.click(screen.getByRole('button', { name: /Wiki Index/ }));

    expect(screen.getByText('Go Guide')).toBeInTheDocument();
    expect(screen.queryByText('Retired Guide')).not.toBeInTheDocument();
  });

  it('typing "archived" in the filter reveals them', async () => {
    mockFetch();
    await renderHero([...mockArticles, archivedArticle]);
    await userEvent.click(screen.getByRole('button', { name: /Wiki Index/ }));

    await userEvent.type(screen.getByPlaceholderText('Filter articles by title or tag...'), 'archived');
    expect(screen.getByText('Retired Guide')).toBeInTheDocument();
  });
});

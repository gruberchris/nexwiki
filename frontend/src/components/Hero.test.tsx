import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Hero } from './Hero';
import type { Article } from '../types';

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const mockFetch = (data: unknown = { tags: ['completed', 'wip', 'draft'] }) => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
    ok: true,
    json: async () => data,
  }));
};

const mockArticles: Article[] = [
  { title: 'Go Guide', slug: 'go-guide', tags: ['golang'], created_at: '2024-01-01T00:00:00Z', updated_at: '2024-01-15T00:00:00Z', version: 1 },
  { title: 'AI Plan', slug: 'ai-plan', tags: ['aiagent-plan'], created_at: '2024-01-01T00:00:00Z', updated_at: '2024-01-15T00:00:00Z', version: 1 },
  { title: 'My Skill', slug: 'my-skill', tags: ['aiagent-skill'], created_at: '2024-01-01T00:00:00Z', updated_at: '2024-01-15T00:00:00Z', version: 1 },
  { title: 'AI Memory', slug: 'ai-memory', tags: ['aiagent-memory-rules'], created_at: '2024-01-01T00:00:00Z', updated_at: '2024-01-15T00:00:00Z', version: 1 },
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

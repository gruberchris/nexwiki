import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ArticleCard } from './ArticleCard';
import type { Article } from '../types';

const statusTags = new Set(['completed', 'wip', 'draft']);

const mockArt: Article = {
  title: 'Go Programming Guide',
  slug: 'go-programming-guide',
  created_at: '2024-01-01T00:00:00Z',
  updated_at: '2024-01-15T12:00:00Z',
  version: 1,
  tags: ['golang', 'backend'],
};

describe('ArticleCard', () => {
  it('renders the article title', () => {
    render(<ArticleCard art={mockArt} onNavigate={vi.fn()} statusTags={statusTags} />);
    expect(screen.getByText('Go Programming Guide')).toBeInTheDocument();
  });

  it('renders tags', () => {
    render(<ArticleCard art={mockArt} onNavigate={vi.fn()} statusTags={statusTags} />);
    expect(screen.getByText('golang')).toBeInTheDocument();
    expect(screen.getByText('backend')).toBeInTheDocument();
  });

  it('calls onNavigate with slug when clicked', async () => {
    const onNavigate = vi.fn();
    render(<ArticleCard art={mockArt} onNavigate={onNavigate} statusTags={statusTags} />);
    await userEvent.click(screen.getByText('Go Programming Guide'));
    expect(onNavigate).toHaveBeenCalledWith('go-programming-guide');
  });

  it('shows "+N more" when there are more than 3 tags', () => {
    const manyTagsArt = {
      ...mockArt,
      tags: ['tag1', 'tag2', 'tag3', 'tag4', 'tag5'],
    };
    render(<ArticleCard art={manyTagsArt} onNavigate={vi.fn()} statusTags={statusTags} />);
    expect(screen.getByText('+2 more')).toBeInTheDocument();
  });

  it('renders without tags', () => {
    const noTagsArt = { ...mockArt, tags: undefined };
    render(<ArticleCard art={noTagsArt} onNavigate={vi.fn()} statusTags={statusTags} />);
    expect(screen.getByText('Go Programming Guide')).toBeInTheDocument();
  });

  it('renders with empty tags array', () => {
    const emptyTagsArt = { ...mockArt, tags: [] };
    render(<ArticleCard art={emptyTagsArt} onNavigate={vi.fn()} statusTags={statusTags} />);
    expect(screen.getByText('Go Programming Guide')).toBeInTheDocument();
  });

  it('renders with secondary prop', () => {
    render(<ArticleCard art={mockArt} onNavigate={vi.fn()} secondary statusTags={statusTags} />);
    expect(screen.getByText('Go Programming Guide')).toBeInTheDocument();
  });

  it('shows aiagent- tags differently', () => {
    const agentTagArt = { ...mockArt, tags: ['aiagent-plan', 'notes'] };
    const { container } = render(<ArticleCard art={agentTagArt} onNavigate={vi.fn()} statusTags={statusTags} />);
    expect(container.textContent).toContain('aiagent-plan');
  });
});

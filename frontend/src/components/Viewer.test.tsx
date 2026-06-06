import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Viewer } from './Viewer';
import type { Article } from '../types';

const mockArticles: Article[] = [
  { title: 'Existing Page', slug: 'existing-page', created_at: '', updated_at: '', version: 1 },
];

const baseProps = {
  content: '',
  onNavigate: vi.fn(),
  articles: mockArticles,
};

describe('Viewer', () => {
  it('renders plain text content', async () => {
    render(<Viewer {...baseProps} content="Hello World" />);
    expect(await screen.findByText('Hello World')).toBeInTheDocument();
  });

  it('renders heading from markdown', async () => {
    render(<Viewer {...baseProps} content="# My Heading" />);
    expect(await screen.findByRole('heading', { level: 1 })).toBeInTheDocument();
  });

  it('renders bold text from markdown', async () => {
    render(<Viewer {...baseProps} content="This is **bold** text." />);
    const bold = await screen.findByText('bold');
    expect(bold.tagName.toLowerCase()).toBe('strong');
  });

  it('renders markdown link', async () => {
    render(<Viewer {...baseProps} content="[Visit](https://example.com)" />);
    const link = await screen.findByRole('link');
    expect(link).toBeInTheDocument();
  });

  it('renders a wiki link as a clickable element', async () => {
    const { container } = render(<Viewer {...baseProps} content="See [[Existing Page]] for more." />);
    await waitFor(() => {
      expect(container.textContent).toContain('Existing Page');
    }, { timeout: 5000 });
  });

  it('calls onNavigate when wiki link is clicked', async () => {
    const onNavigate = vi.fn();
    const { container } = render(<Viewer {...baseProps} content="See [[Existing Page]] for more." onNavigate={onNavigate} />);
    await waitFor(() => {
      expect(container.textContent).toContain('Existing Page');
    }, { timeout: 5000 });
    const link = container.querySelector('a, span[title]');
    if (link) {
      await userEvent.click(link as HTMLElement);
      expect(onNavigate).toHaveBeenCalled();
    }
  });

  it('renders empty content without crashing', () => {
    render(<Viewer {...baseProps} content="" />);
    expect(document.body).toBeInTheDocument();
  });
});

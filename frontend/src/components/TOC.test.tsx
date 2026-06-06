import { describe, it, expect, vi } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';
import { TOC } from './TOC';

// IntersectionObserver mock for happy-dom
const mockObserve = vi.fn();
const mockDisconnect = vi.fn();
const mockUnobserve = vi.fn();

class MockIntersectionObserver {
  observe = mockObserve;
  unobserve = mockUnobserve;
  disconnect = mockDisconnect;
  constructor(_callback: IntersectionObserverCallback, _options?: IntersectionObserverInit) {}
}

vi.stubGlobal('IntersectionObserver', MockIntersectionObserver);

describe('TOC', () => {
  it('returns null for empty content', () => {
    const { container } = render(<TOC content="" />);
    expect(container.firstChild).toBeNull();
  });

  it('returns null for content without headings', () => {
    const { container } = render(<TOC content="Just plain text.\n\nNo headings here." />);
    expect(container.firstChild).toBeNull();
  });

  it('renders heading anchors for H1/H2/H3', async () => {
    const { container } = render(<TOC content="# Main Title\n\n## Section One\n\n### Subsection A" />);
    // The TOC is hidden by Tailwind's `hidden xl:block`, use container queries
    await waitFor(() => {
      expect(container.textContent).toContain('Main Title');
      expect(container.textContent).toContain('Section One');
      expect(container.textContent).toContain('Subsection A');
    });
  });

  it('renders "On this page" label', async () => {
    const { container } = render(<TOC content="# Title\n\n## Section" />);
    await waitFor(() => {
      expect(container.textContent).toContain('On this page');
    });
  });

  it('strips YAML front matter before parsing', async () => {
    const contentWithFrontMatter = '---\ntitle: Article\n---\n\n# Real Heading\n\n## Section';
    const { container } = render(<TOC content={contentWithFrontMatter} />);
    await waitFor(() => {
      expect(container.textContent).toContain('Real Heading');
    });
  });

  it('ignores headings inside code blocks', async () => {
    const content = '# Real Heading\n\n```\n# Not a heading\n```\n\n## Section';
    const { container } = render(<TOC content={content} />);
    await waitFor(() => {
      expect(container.textContent).toContain('Real Heading');
    });
    expect(container.textContent).not.toContain('Not a heading');
  });

  it('renders anchor links after content loads', async () => {
    const { container } = render(<TOC content="# Go Guide\n\n## Installation" />);
    await waitFor(() => {
      expect(container.textContent).toContain('Go Guide');
      expect(container.textContent).toContain('Installation');
    });
    // Check that anchor links exist in the rendered TOC
    const links = container.querySelectorAll('a');
    expect(links.length).toBeGreaterThan(0);
  });
});

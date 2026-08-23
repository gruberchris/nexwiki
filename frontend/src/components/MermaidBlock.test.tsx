import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MermaidBlock } from './MermaidBlock';
import { Viewer } from './Viewer';

// The real library cannot render in happy-dom (it measures SVG geometry), so these tests mock
// it and cover this component's contract: lazy load, parse-validate, inject SVG, and fall back
// to the source on failure. Real rendering is exercised manually in the app.
vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    parse: vi.fn((code: string) => {
      if (code.includes('not a diagram')) return Promise.reject(new Error('Parse error on line 1'));
      return Promise.resolve(true);
    }),
    render: vi.fn(() => Promise.resolve({ svg: '<svg><text>rendered-diagram</text></svg>' })),
  },
}));

describe('MermaidBlock', () => {
  it('renders a valid diagram as SVG', async () => {
    render(<MermaidBlock code={'graph TD\n  A --> B'} />);
    await waitFor(() => expect(screen.getByTestId('mermaid-diagram')).toBeInTheDocument());
    expect(screen.getByTestId('mermaid-diagram').innerHTML).toContain('rendered-diagram');
  });

  it('falls back to the original code block with an error note on malformed input', async () => {
    render(<MermaidBlock code={'this is not a diagram'} />);
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument());
    // The source must never be lost — a syntax error cannot produce a blank hole.
    expect(screen.getByText('this is not a diagram')).toBeInTheDocument();
    expect(screen.queryByTestId('mermaid-diagram')).not.toBeInTheDocument();
  });
});

describe('Viewer mermaid fence routing', () => {
  it('renders a ```mermaid fence as a diagram', async () => {
    render(
      <Viewer content={'```mermaid\ngraph TD\n  A --> B\n```'} onNavigate={vi.fn()} articles={[]} />,
    );
    await waitFor(() => expect(screen.getByTestId('mermaid-diagram')).toBeInTheDocument());
  });

  it('leaves other fenced languages on the code path', () => {
    render(
      <Viewer content={'```go\nfunc main() {}\n```'} onNavigate={vi.fn()} articles={[]} />,
    );
    expect(screen.queryByTestId('mermaid-diagram')).not.toBeInTheDocument();
    expect(document.querySelector('pre code')).not.toBeNull();
  });
});

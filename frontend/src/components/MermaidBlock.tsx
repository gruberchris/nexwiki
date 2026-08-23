import React, { useEffect, useRef, useState } from 'react';

/**
 * Renders a ```mermaid fenced code block as an SVG diagram.
 *
 * The mermaid library is ~800KB minified, so it is loaded with a dynamic import() the first
 * time a page actually contains a diagram, and the promise is shared so many diagrams on one
 * page trigger a single fetch. Until the SVG is ready — and permanently, when the diagram
 * source is invalid — the original code block stays visible, so a syntax error can never
 * produce a blank hole in an article.
 */

type MermaidModule = typeof import('mermaid').default;

let mermaidPromise: Promise<MermaidModule> | null = null;
function loadMermaid(): Promise<MermaidModule> {
  if (!mermaidPromise) {
    mermaidPromise = import('mermaid').then((m) => m.default);
  }
  return mermaidPromise;
}

/** mermaid.render requires a DOM-unique id; React's useId emits characters invalid in CSS selectors. */
let renderCounter = 0;

/**
 * Tracks the `dark` class on <html>, which useTheme owns. Observing the class rather than
 * calling useTheme keeps this component usable from both the article Viewer and the editor
 * preview without threading theme state through every intermediate component.
 */
function useDocumentDarkClass(): boolean {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'));
  useEffect(() => {
    const observer = new MutationObserver(() =>
      setDark(document.documentElement.classList.contains('dark')),
    );
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
    return () => observer.disconnect();
  }, []);
  return dark;
}

/** Delay before re-rendering after the source changes, so editor typing doesn't thrash. */
const RERENDER_DEBOUNCE_MS = 300;

interface MermaidBlockProps {
  code: string;
}

export const MermaidBlock: React.FC<MermaidBlockProps> = ({ code }) => {
  const darkMode = useDocumentDarkClass();
  const [svg, setSvg] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const hasRenderedOnce = useRef(false);

  useEffect(() => {
    let cancelled = false;

    const renderDiagram = async () => {
      try {
        const mermaid = await loadMermaid();
        if (cancelled) return;
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: 'strict',
          theme: darkMode ? 'dark' : 'default',
        });
        // parse() validates without touching the DOM; render() on bad input can leave
        // stray error nodes behind, so never hand it a diagram that doesn't parse.
        await mermaid.parse(code);
        const { svg: rendered } = await mermaid.render(`nexwiki-mermaid-${renderCounter++}`, code);
        if (cancelled) return;
        setSvg(rendered);
        setError(null);
      } catch (err: unknown) {
        if (cancelled) return;
        setSvg(null);
        setError(err instanceof Error ? err.message : 'Diagram failed to render');
      }
    };

    // First render fires immediately (an article body renders once); later source changes —
    // typing in the editor's live preview — are debounced.
    if (!hasRenderedOnce.current) {
      hasRenderedOnce.current = true;
      void renderDiagram();
      return () => {
        cancelled = true;
      };
    }
    const timer = setTimeout(() => void renderDiagram(), RERENDER_DEBOUNCE_MS);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [code, darkMode]);

  if (svg) {
    return (
      <div
        className="mermaid-diagram overflow-x-auto"
        data-testid="mermaid-diagram"
        // Mermaid sanitizes its own output under securityLevel: 'strict'.
        dangerouslySetInnerHTML={{ __html: svg }}
      />
    );
  }

  // Loading and failure both fall back to the original fence so the source is never lost.
  return (
    <>
      {error && (
        <p className="text-xs font-semibold text-rose-600 dark:text-rose-400" role="alert">
          Mermaid diagram failed to render: {error}
        </p>
      )}
      <pre>
        <code className="language-mermaid">{code}</code>
      </pre>
    </>
  );
};

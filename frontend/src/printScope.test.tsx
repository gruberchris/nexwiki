import { describe, it, expect, vi } from 'vitest';
// The stylesheet is read as text, not loaded: what is being asserted is the rule set itself.
// happy-dom cannot evaluate `@media print` through getComputedStyle, so there is nothing to
// compute against — and `?raw` keeps this to a Vite import rather than pulling in Node types,
// which `tsc -b` type-checks this file against during the production build.
import indexCss from './index.css?raw';
import { render } from '@testing-library/react';
import { ActivityLogDrawer } from './components/ActivityLogDrawer';
import { HistoryDrawer } from './components/HistoryDrawer';
import { withSSEContext } from './test-helpers';

// "Export as PDF" is window.print(), so what lands in the PDF is decided entirely by the
// `@media print` block in index.css. Nothing else scopes it.
//
// The defect this pins: the whole Live Activity Log printed after the article. The drawer is
// mounted at all times and merely translated off-screen, and the print stylesheet's
// container-flattening rule matches on utility classes the drawer also uses (.flex, .h-screen),
// so it rewrote `position: fixed` to `position: static` and dropped the drawer into normal
// document flow directly after the article.
//
// happy-dom cannot evaluate `@media print` through getComputedStyle, so the guarantee is pinned
// as the two halves that compose it: the stylesheet removes fixed-position overlays from print,
// and the overlays really are fixed-position. Either half alone is satisfiable while the bug is
// live, which is why both are asserted here rather than in separate files.

const printBlock = (): string => {
  const css = indexCss;
  const start = css.indexOf('@media print {');
  expect(start, 'index.css must contain an @media print block').toBeGreaterThan(-1);

  // Walk braces from the block's opening one so nested rules do not end the slice early.
  let depth = 0;
  for (let i = css.indexOf('{', start); i < css.length; i++) {
    if (css[i] === '{') depth++;
    if (css[i] === '}' && --depth === 0) return css.slice(start, i + 1);
  }
  throw new Error('@media print block is unterminated');
};

// stripComments removes the explanatory prose so an assertion cannot pass on a comment that
// merely mentions the selector it is looking for.
const stripComments = (css: string): string => css.replace(/\/\*[\s\S]*?\*\//g, '');

describe('print stylesheet scoping', () => {
  it('removes fixed-position overlays from the printed page', () => {
    const rules = stripComments(printBlock());

    const fixedRule = /(^|[,{}\s])\.fixed\s*(,[^{]*)?\{[^}]*\}/m.exec(rules);
    expect(fixedRule, '@media print must hide .fixed overlays').not.toBeNull();
    expect(fixedRule![0]).toMatch(/display:\s*none\s*!important/);
  });

  it('hides overlays outright rather than making them invisible', () => {
    // A flattened drawer keeps its full height, so `visibility: hidden` would trade the activity
    // log for a run of blank pages. Only display:none removes it from the flow.
    const rules = stripComments(printBlock());
    const fixedRule = /(^|[,{}\s])\.fixed\s*(,[^{]*)?\{([^}]*)\}/m.exec(rules);
    expect(fixedRule![3]).not.toMatch(/visibility:\s*hidden/);
  });

  it('keeps hiding anything explicitly marked no-print', () => {
    const rules = stripComments(printBlock());
    expect(rules).toMatch(/\.no-print[^{]*\{[^}]*display:\s*none\s*!important/);
  });
});

describe('overlays the print rule has to catch', () => {
  it('the activity log drawer is fixed-position and always mounted', () => {
    // Always mounted is what made this the one overlay that leaked into every print: a modal
    // rendered only while open cannot be on the page when the user triggers the print dialog.
    const { container } = render(
      withSSEContext(<ActivityLogDrawer isOpen={false} onClose={vi.fn()} onNavigate={vi.fn()} />)
    );

    const drawer = container.querySelector('.fixed');
    expect(drawer, 'the drawer must render even when closed, and must be .fixed').not.toBeNull();
    expect(drawer!.className).toContain('h-screen');
  });

  it('the history drawer is fixed-position', () => {
    // Never resolves: the drawer's own fetch is irrelevant here, and letting it run reaches the
    // network and prints a connection error over the test output.
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})));
    const { container } = render(
      <HistoryDrawer
        slug="any"
        currentContent=""
        currentTitle="Any"
        onClose={vi.fn()}
        onRevertComplete={vi.fn()}
      />
    );
    expect(container.querySelector('.fixed')).not.toBeNull();
  });
});

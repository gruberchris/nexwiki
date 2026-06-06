import { describe, it, expect } from 'vitest';
import { lintMarkdown } from './markdownLinter';

const noArticles: { slug: string; title: string }[] = [];
const articles = [
  { slug: 'existing-page', title: 'Existing Page' },
  { slug: 'another-page', title: 'Another Page' },
];

describe('lintMarkdown – MD001 (heading hierarchy)', () => {
  it('returns no diagnostics for single H1', () => {
    const diags = lintMarkdown('# Heading One', noArticles);
    expect(diags).toHaveLength(0);
  });

  it('returns no diagnostics for valid H1→H2→H3 progression', () => {
    const content = '# Title\n\n## Section\n\n### Subsection';
    const diags = lintMarkdown(content, noArticles);
    const md001 = diags.filter(d => d.code === 'MD001');
    expect(md001).toHaveLength(0);
  });

  it('warns when heading skips a level (H1→H3)', () => {
    const content = '# Title\n\n### Skipped H2';
    const diags = lintMarkdown(content, noArticles);
    const md001 = diags.filter(d => d.code === 'MD001');
    expect(md001).toHaveLength(1);
    expect(md001[0].severity).toBe('warning');
    expect(md001[0].message).toContain('H3');
    expect(md001[0].suggestion).toContain('## Skipped H2');
  });

  it('provides correct line number for MD001 diagnostic', () => {
    const content = '# Title\n\n### Bad H3';
    const diags = lintMarkdown(content, noArticles);
    const md001 = diags.find(d => d.code === 'MD001');
    expect(md001?.line).toBe(3);
  });
});

describe('lintMarkdown – MD025 (multiple H1)', () => {
  it('returns no diagnostic for single H1', () => {
    const diags = lintMarkdown('# Only H1\n\n## Section', noArticles);
    const md025 = diags.filter(d => d.code === 'MD025');
    expect(md025).toHaveLength(0);
  });

  it('flags second H1 with error', () => {
    const content = '# First H1\n\n## Section\n\n# Second H1';
    const diags = lintMarkdown(content, noArticles);
    const md025 = diags.filter(d => d.code === 'MD025');
    expect(md025).toHaveLength(1);
    expect(md025[0].severity).toBe('error');
    expect(md025[0].line).toBe(5);
    expect(md025[0].suggestion).toContain('## Second H1');
  });

  it('flags third H1 as well', () => {
    const content = '# H1\n\n# H1 again\n\n# H1 third';
    const diags = lintMarkdown(content, noArticles);
    const md025 = diags.filter(d => d.code === 'MD025');
    expect(md025).toHaveLength(2);
  });
});

describe('lintMarkdown – MD037 (spaces around emphasis)', () => {
  it('returns no diagnostic for isolated bold emphasis', () => {
    const diags = lintMarkdown('This is **bold** text here.', noArticles);
    const md037 = diags.filter(d => d.code === 'MD037');
    expect(md037).toHaveLength(0);
  });

  it('warns for double-asterisk with surrounding spaces', () => {
    const diags = lintMarkdown('This is ** bold text ** here.', noArticles);
    const md037 = diags.filter(d => d.code === 'MD037');
    expect(md037.length).toBeGreaterThanOrEqual(1);
    expect(md037[0].severity).toBe('warning');
    expect(md037[0].suggestion).toBe('**bold text**');
  });

  it('warns for single-asterisk with surrounding spaces', () => {
    const diags = lintMarkdown('This is * italic * here.', noArticles);
    const md037 = diags.filter(d => d.code === 'MD037');
    expect(md037.length).toBeGreaterThanOrEqual(1);
  });
});

describe('lintMarkdown – MD034 (bare URLs)', () => {
  it('warns for bare http URL', () => {
    const diags = lintMarkdown('Visit https://example.com for more.', noArticles);
    const md034 = diags.filter(d => d.code === 'MD034');
    expect(md034).toHaveLength(1);
    expect(md034[0].severity).toBe('info');
    expect(md034[0].suggestion).toBe('<https://example.com>');
  });

  it('no warning for angle-bracket wrapped URL', () => {
    const diags = lintMarkdown('Visit <https://example.com> for more.', noArticles);
    const md034 = diags.filter(d => d.code === 'MD034');
    expect(md034).toHaveLength(0);
  });

  it('no warning for markdown link', () => {
    const diags = lintMarkdown('[Click here](https://example.com)', noArticles);
    const md034 = diags.filter(d => d.code === 'MD034');
    expect(md034).toHaveLength(0);
  });

  it('no warning for image URL', () => {
    const diags = lintMarkdown('![alt](https://example.com/img.png)', noArticles);
    const md034 = diags.filter(d => d.code === 'MD034');
    expect(md034).toHaveLength(0);
  });
});

describe('lintMarkdown – WikiLinks', () => {
  it('no diagnostic for existing article link', () => {
    const diags = lintMarkdown('See [[Existing Page]] for more.', articles);
    const wl = diags.filter(d => d.code === 'WIKILINK_BROKEN');
    expect(wl).toHaveLength(0);
  });

  it('warns for nonexistent page link', () => {
    const diags = lintMarkdown('See [[Nonexistent Page]] for more.', articles);
    const wl = diags.filter(d => d.code === 'WIKILINK_BROKEN');
    expect(wl).toHaveLength(1);
    expect(wl[0].severity).toBe('warning');
    expect(wl[0].message).toContain('Nonexistent Page');
  });

  it('[[home]] is always valid', () => {
    const diags = lintMarkdown('Go to [[home]] now.', noArticles);
    const wl = diags.filter(d => d.code === 'WIKILINK_BROKEN');
    expect(wl).toHaveLength(0);
  });

  it('[[new]] is always valid', () => {
    const diags = lintMarkdown('Create [[new]] article.', noArticles);
    const wl = diags.filter(d => d.code === 'WIKILINK_BROKEN');
    expect(wl).toHaveLength(0);
  });

  it('handles pipe syntax [[page|Display Text]]', () => {
    const diags = lintMarkdown('See [[Existing Page|click here]] for more.', articles);
    const wl = diags.filter(d => d.code === 'WIKILINK_BROKEN');
    expect(wl).toHaveLength(0);
  });

  it('warns for missing pipe-syntax page', () => {
    const diags = lintMarkdown('See [[Missing|click here]] for more.', articles);
    const wl = diags.filter(d => d.code === 'WIKILINK_BROKEN');
    expect(wl).toHaveLength(1);
  });
});

describe('lintMarkdown – character offsets', () => {
  it('computes correct from offset for heading on first line', () => {
    const content = '# Title';
    const diags = lintMarkdown(content, noArticles);
    // No diagnostics expected for valid heading
    expect(diags).toHaveLength(0);
  });

  it('computes from offset accounting for newlines on second line', () => {
    const content = '# Title\n\n[[Missing Page]]';
    const diags = lintMarkdown(content, noArticles);
    const wl = diags.find(d => d.code === 'WIKILINK_BROKEN');
    expect(wl).toBeDefined();
    // Line 3 starts at offset 9 (8 chars on lines 1-2 + newlines)
    expect(wl!.line).toBe(3);
    expect(wl!.from).toBeGreaterThan(8);
  });
});

describe('lintMarkdown – empty content', () => {
  it('returns no diagnostics for empty string', () => {
    expect(lintMarkdown('', noArticles)).toHaveLength(0);
  });

  it('returns no diagnostics for plain text', () => {
    expect(lintMarkdown('Just plain text, no issues here.', noArticles)).toHaveLength(0);
  });
});

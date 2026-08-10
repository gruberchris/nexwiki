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
    expect(md001[0].fix).toContain('## Skipped H2');
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
    expect(md025[0].fix).toContain('## Second H1');
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
    expect(md037[0].fix).toBe('**bold text**');
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
    expect(md034[0].fix).toBe('<https://example.com>');
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

describe('lintMarkdown – MDLINK_BROKEN (absolute /articles/ links)', () => {
  it('no diagnostic for an existing article link', () => {
    const diags = lintMarkdown('See [the page](/articles/existing-page) for more.', articles);
    expect(diags.filter(d => d.code === 'MDLINK_BROKEN')).toHaveLength(0);
  });

  it('warns for a nonexistent target', () => {
    const diags = lintMarkdown('See [Kotlin](/articles/kotlin) for more.', articles);
    const md = diags.filter(d => d.code === 'MDLINK_BROKEN');
    expect(md).toHaveLength(1);
    expect(md[0].severity).toBe('warning');
    expect(md[0].message).toContain('/articles/kotlin');
  });

  it('carries a hint but no fix, because there is no single correct replacement', () => {
    const diag = lintMarkdown('[Kotlin](/articles/kotlin)', articles).find(d => d.code === 'MDLINK_BROKEN')!;
    expect(diag.fix).toBeUndefined();
    expect(diag.hint).toBeTruthy();
  });

  it('spans the whole link, not the character before it', () => {
    const content = 'See [Kotlin](/articles/kotlin) here.';
    const diag = lintMarkdown(content, articles).find(d => d.code === 'MDLINK_BROKEN')!;
    expect(content.slice(diag.from, diag.to)).toBe('[Kotlin](/articles/kotlin');
  });

  it('ignores the fragment when resolving the target', () => {
    const diags = lintMarkdown('[History](/articles/existing-page#history)', articles);
    expect(diags.filter(d => d.code === 'MDLINK_BROKEN')).toHaveLength(0);
  });

  it('/articles/home is always valid', () => {
    const diags = lintMarkdown('Back to [home](/articles/home).', noArticles);
    expect(diags.filter(d => d.code === 'MDLINK_BROKEN')).toHaveLength(0);
  });

  it('does not flag images', () => {
    const diags = lintMarkdown('![diagram](/articles/kotlin)', articles);
    expect(diags.filter(d => d.code === 'MDLINK_BROKEN')).toHaveLength(0);
  });

  it('does not flag /api/articles/ URLs', () => {
    const diags = lintMarkdown('Call [the API](/api/articles/kotlin).', articles);
    expect(diags.filter(d => d.code === 'MDLINK_BROKEN')).toHaveLength(0);
  });

  it('flags links on a later line with the right line number', () => {
    const diags = lintMarkdown('# Title\n\n[Kotlin](/articles/kotlin)', articles);
    const md = diags.find(d => d.code === 'MDLINK_BROKEN')!;
    expect(md.line).toBe(3);
  });
});

// Both link checks have to agree with the server's link graph, which blanks out code before
// scanning. The two format-template articles in the real corpus document the convention with a
// fenced [Article Title](/articles/slug) example, and flagging those would be a false positive on
// content the health report deliberately ignores.
describe('lintMarkdown – links inside code are not links', () => {
  it('ignores an /articles/ link inside a fenced block', () => {
    const content = '```markdown\n[Article Title](/articles/slug)\n```\n';
    expect(lintMarkdown(content, articles).filter(d => d.code === 'MDLINK_BROKEN')).toHaveLength(0);
  });

  it('ignores a WikiLink inside a fenced block', () => {
    const content = '```cpp\n[[nodiscard]] int f();\n```\n';
    expect(lintMarkdown(content, articles).filter(d => d.code === 'WIKILINK_BROKEN')).toHaveLength(0);
  });

  it('ignores a tilde-fenced block', () => {
    const content = '~~~markdown\n[Article Title](/articles/slug)\n~~~\n';
    expect(lintMarkdown(content, articles).filter(d => d.code === 'MDLINK_BROKEN')).toHaveLength(0);
  });

  it('still flags links after the fence closes', () => {
    const content = '```markdown\n[Example](/articles/slug)\n```\n\n[Kotlin](/articles/kotlin)\n';
    const md = lintMarkdown(content, articles).filter(d => d.code === 'MDLINK_BROKEN');
    expect(md).toHaveLength(1);
    expect(md[0].line).toBe(5);
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

// A `fix` is inserted verbatim over the diagnostic's range by both quick-fix paths — the
// CodeMirror lint action and the editor's right-click menu. Applying every `fix` to its own range
// must therefore leave valid Markdown. When `fix` and `hint` were one `suggestion` field, this
// invariant did not hold: WIKILINK_BROKEN's suggestion was the sentence "Click to create this
// page.", so the Fix button replaced `[[Foo]]` with prose.
describe('lintMarkdown – every fix is safe to insert verbatim', () => {
  const content = [
    '# Title',
    '### Skipped H2',
    '# Second H1',
    'Visit https://example.com for more.',
    'This is ** bold text ** here.',
    'See [[Missing Page]] and [Kotlin](/articles/kotlin).',
  ].join('\n');

  it('never offers a fix that is prose rather than Markdown', () => {
    for (const d of lintMarkdown(content, articles)) {
      if (d.fix === undefined) continue;
      // Every rule that carries a fix replaces a heading, an emphasis run, or a bare URL.
      expect(d.fix).toMatch(/^(#{1,6} |<https?:\/\/|\*{1,2}|_{1,2})/);
      expect(d.fix).not.toMatch(/\bClick\b/);
    }
  });

  it('gives the broken-link rules a hint and no fix', () => {
    const linkDiags = lintMarkdown(content, articles)
      .filter(d => d.code === 'WIKILINK_BROKEN' || d.code === 'MDLINK_BROKEN');
    expect(linkDiags).toHaveLength(2);
    for (const d of linkDiags) {
      expect(d.fix).toBeUndefined();
      expect(d.hint).toBeTruthy();
    }
  });

  it('applying a fix to its own range replaces exactly the flagged text', () => {
    const src = 'This is ** bold text ** here.';
    const d = lintMarkdown(src, noArticles).find(x => x.code === 'MD037')!;
    const applied = src.slice(0, d.from) + d.fix! + src.slice(d.to);
    expect(applied).toBe('This is **bold text** here.');
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

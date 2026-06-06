import { describe, it, expect } from 'vitest';
import type { Article } from './types';
import type { LogEvent } from './context/SSEContextObject';
import {
  evaluateBooleanQuery,
  getAutocompleteSearchTerm,
  applyAutocompleteSelection,
  getTagPriority,
  sortCardTags,
  matchesFilter,
  matchesSidebarFilter,
  matchesLogEvent,
  buildSuggestionsFromArticles,
} from './filterUtils';

// ---------------------------------------------------------------------------
// evaluateBooleanQuery
// ---------------------------------------------------------------------------

describe('evaluateBooleanQuery', () => {
  it('returns true for empty query', () => {
    expect(evaluateBooleanQuery(['anything'], '')).toBe(true);
    expect(evaluateBooleanQuery(['anything'], '   ')).toBe(true);
  });

  it('matches a simple term', () => {
    expect(evaluateBooleanQuery(['Python Guide'], 'python')).toBe(true);
    expect(evaluateBooleanQuery(['JavaScript'], 'python')).toBe(false);
  });

  it('space is implicit OR between positive terms', () => {
    expect(evaluateBooleanQuery(['Python Guide'], 'python javascript')).toBe(true);
    expect(evaluateBooleanQuery(['JavaScript Handbook'], 'python javascript')).toBe(true);
    expect(evaluateBooleanQuery(['Go Programming'], 'python javascript')).toBe(false);
  });

  it('AND / && requires all terms in the group', () => {
    expect(evaluateBooleanQuery(['Python Tutorial'], 'python && tutorial')).toBe(true);
    expect(evaluateBooleanQuery(['Python Tutorial'], 'python AND tutorial')).toBe(true);
    expect(evaluateBooleanQuery(['Python Guide'], 'python && tutorial')).toBe(false);
  });

  it('OR / || between groups', () => {
    expect(evaluateBooleanQuery(['Python Tutorial'], 'go || python')).toBe(true);
    expect(evaluateBooleanQuery(['Go Tutorial'], 'go || python')).toBe(true);
    expect(evaluateBooleanQuery(['JavaScript'], 'go || python')).toBe(false);
    expect(evaluateBooleanQuery(['JavaScript'], 'go OR python')).toBe(false);
  });

  it('! excludes single-word term (bare exclusion)', () => {
    expect(evaluateBooleanQuery(['Draft Article', 'draft'], '!draft')).toBe(false);
    expect(evaluateBooleanQuery(['Published'], '!draft')).toBe(true);
  });

  it("!word1 word2 treats them as separate tokens: exclude word1, OR match word2", () => {
    // field has "programming" — should be excluded
    expect(evaluateBooleanQuery(['programming'], '!programming language')).toBe(false);
    // field has "language" but not "programming" — positive match
    expect(evaluateBooleanQuery(['language guide'], '!programming language')).toBe(true);
    // field has both — excluded wins
    expect(evaluateBooleanQuery(['programming language'], '!programming language')).toBe(false);
    // field has neither — positive term "language" is not satisfied
    expect(evaluateBooleanQuery(['python'], '!programming language')).toBe(false);
  });

  it("quoted single-quote exclusion !'multi word' works without infinite loop", () => {
    // field contains the phrase → excluded
    expect(evaluateBooleanQuery(['programming language tutorial'], "!'programming language'")).toBe(false);
    // field has only "programming" but not the full phrase → not excluded
    expect(evaluateBooleanQuery(['programming'], "!'programming language'")).toBe(true);
  });

  it("quoted double-quote exclusion !\"multi word\" works", () => {
    expect(evaluateBooleanQuery(['programming language'], '!"programming language"')).toBe(false);
    expect(evaluateBooleanQuery(['programming'], '!"programming language"')).toBe(true);
  });

  it("quoted positive term 'multi word' matches the phrase", () => {
    expect(evaluateBooleanQuery(['Getting Started Guide'], "'getting started'")).toBe(true);
    expect(evaluateBooleanQuery(['Getting Guide'], "'getting started'")).toBe(false);
  });

  it("quoted positive term \"multi word\" matches the phrase", () => {
    expect(evaluateBooleanQuery(['Getting Started Guide'], '"getting started"')).toBe(true);
    expect(evaluateBooleanQuery(['Getting Guide'], '"getting started"')).toBe(false);
  });

  it('is case-insensitive', () => {
    expect(evaluateBooleanQuery(['PYTHON GUIDE'], 'python')).toBe(true);
    expect(evaluateBooleanQuery(['python guide'], 'PYTHON')).toBe(true);
  });

  it('handles mixed AND/OR with implicit OR correctly', () => {
    // (python && tutorial) OR go
    expect(evaluateBooleanQuery(['Python Tutorial'], 'python && tutorial go')).toBe(true);
    expect(evaluateBooleanQuery(['Go Handbook'], 'python && tutorial go')).toBe(true);
    expect(evaluateBooleanQuery(['Python Handbook'], 'python && tutorial go')).toBe(false);
  });

  it('handles exclusion combined with positive terms', () => {
    // show python articles that are not drafts
    expect(evaluateBooleanQuery(['Python Draft'], 'python !draft')).toBe(false);
    expect(evaluateBooleanQuery(['Python Guide'], 'python !draft')).toBe(true);
    expect(evaluateBooleanQuery(['JavaScript Guide'], 'python !draft')).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// getAutocompleteSearchTerm
// ---------------------------------------------------------------------------

describe('getAutocompleteSearchTerm', () => {
  it('returns empty string for empty query', () => {
    expect(getAutocompleteSearchTerm('')).toBe('');
  });

  it('returns the single typed term', () => {
    expect(getAutocompleteSearchTerm('pro')).toBe('pro');
  });

  it('strips ! prefix from exclusion term', () => {
    expect(getAutocompleteSearchTerm('!pro')).toBe('pro');
  });

  it('returns the rightmost term when there are multiple tokens', () => {
    expect(getAutocompleteSearchTerm('foo !pro')).toBe('pro');
    expect(getAutocompleteSearchTerm('foo bar')).toBe('bar');
  });

  it('returns empty string when query ends with whitespace (new token not started)', () => {
    expect(getAutocompleteSearchTerm('foo ')).toBe('');
    expect(getAutocompleteSearchTerm('foo bar ')).toBe('');
  });

  it('returns content inside an open-quoted exclusion (user still typing)', () => {
    expect(getAutocompleteSearchTerm("!'programming lang")).toBe('programming lang');
  });

  it('returns empty string when quoted exclusion is complete', () => {
    expect(getAutocompleteSearchTerm("!'programming language'")).toBe('');
  });

  it('returns content inside an open double-quoted term', () => {
    expect(getAutocompleteSearchTerm('"getting start')).toBe('getting start');
  });

  it('returns empty string when double-quoted term is complete', () => {
    expect(getAutocompleteSearchTerm('"getting started"')).toBe('');
  });

  it('returns bare term after operator boundary', () => {
    expect(getAutocompleteSearchTerm('foo&&bar')).toBe('bar');
    expect(getAutocompleteSearchTerm('foo||bar')).toBe('bar');
  });
});

// ---------------------------------------------------------------------------
// applyAutocompleteSelection
// ---------------------------------------------------------------------------

describe('applyAutocompleteSelection', () => {
  it('replaces a bare term with a single-word selection', () => {
    expect(applyAutocompleteSelection('pro', 'python')).toBe('python');
  });

  it('wraps multi-word selection in single quotes', () => {
    expect(applyAutocompleteSelection('pro', 'programming language')).toBe("'programming language'");

  });

  it('preserves ! prefix for exclusion with single-word selection', () => {
    expect(applyAutocompleteSelection('!pro', 'python')).toBe('!python');
  });

  it('preserves ! prefix and wraps multi-word selection in single quotes', () => {
    expect(applyAutocompleteSelection('!pro', 'programming language')).toBe("!'programming language'");
  });

  it('keeps prefix before the active token (single-word)', () => {
    expect(applyAutocompleteSelection('foo !pro', 'python')).toBe('foo !python');
  });

  it('keeps prefix before the active token (multi-word)', () => {
    expect(applyAutocompleteSelection('foo !pro', 'programming language')).toBe("foo !'programming language'");
  });

  it('handles completing an open-quoted exclusion', () => {
    expect(applyAutocompleteSelection("!'programming lang", 'programming language')).toBe("!'programming language'");
  });

  it('handles completing an open-quoted positive term', () => {
    expect(applyAutocompleteSelection('"getting start', 'Getting Started Guide')).toBe("'Getting Started Guide'");
  });

  it('replaces the last term when query has multiple complete tokens', () => {
    expect(applyAutocompleteSelection('python jav', 'javascript')).toBe('python javascript');
  });

  it('works with AND operator in prefix', () => {
    expect(applyAutocompleteSelection('python&&jav', 'javascript')).toBe('python&&javascript');
  });
});

// ---------------------------------------------------------------------------
// getTagPriority
// ---------------------------------------------------------------------------

describe('getTagPriority', () => {
  const statusTags = new Set(['completed', 'wip', 'draft', 'review']);

  it('returns 0 for status tags', () => {
    expect(getTagPriority('completed', statusTags)).toBe(0);
    expect(getTagPriority('WIP', statusTags)).toBe(0);
  });

  it('returns 2 for aiagent- prefixed tags', () => {
    expect(getTagPriority('aiagent-plan', statusTags)).toBe(2);
    expect(getTagPriority('aiagent-memory-rules', statusTags)).toBe(2);
    expect(getTagPriority('AIAGENT-SKILL', statusTags)).toBe(2);
  });

  it('returns 1 for regular tags', () => {
    expect(getTagPriority('golang', statusTags)).toBe(1);
    expect(getTagPriority('backend', statusTags)).toBe(1);
  });
});

// ---------------------------------------------------------------------------
// sortCardTags
// ---------------------------------------------------------------------------

describe('sortCardTags', () => {
  const statusTags = new Set(['completed', 'wip', 'draft']);

  it('sorts status tags first, regular second, aiagent- last', () => {
    const tags = ['golang', 'aiagent-plan', 'completed', 'backend'];
    const sorted = sortCardTags(tags, statusTags);
    expect(sorted[0]).toBe('completed');
    expect(sorted[sorted.length - 1]).toBe('aiagent-plan');
  });

  it('preserves order within same priority group', () => {
    const tags = ['alpha', 'beta'];
    const sorted = sortCardTags(tags, new Set());
    expect(sorted).toEqual(['alpha', 'beta']);
  });

  it('does not mutate the original array', () => {
    const tags = ['b', 'a'];
    const original = [...tags];
    sortCardTags(tags, new Set());
    expect(tags).toEqual(original);
  });
});

// ---------------------------------------------------------------------------
// matchesFilter
// ---------------------------------------------------------------------------

describe('matchesFilter', () => {
  const art = {
    slug: 'golang-guide',
    title: 'Golang Guide',
    tags: ['programming', 'backend'],
    created_at: '',
    updated_at: '',
    version: 1,
  };

  it('returns true when title matches', () => {
    expect(matchesFilter(art, 'golang')).toBe(true);
  });

  it('returns true when tag matches', () => {
    expect(matchesFilter(art, 'programming')).toBe(true);
  });

  it('returns false when nothing matches', () => {
    expect(matchesFilter(art, 'python')).toBe(false);
  });

  it('returns true for empty query', () => {
    expect(matchesFilter(art, '')).toBe(true);
  });

  it('handles article with no tags', () => {
    const noTags = { ...art, tags: undefined };
    expect(matchesFilter(noTags as unknown as Article, 'golang')).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// matchesSidebarFilter
// ---------------------------------------------------------------------------

describe('matchesSidebarFilter', () => {
  const art = {
    slug: 'go-guide',
    title: 'Golang Guide',
    tags: ['backend'],
    created_at: '',
    updated_at: '',
    version: 1,
  };

  it('returns true when slug matches (unlike matchesFilter)', () => {
    expect(matchesSidebarFilter(art, 'go-guide')).toBe(true);
  });

  it('returns true when title matches', () => {
    expect(matchesSidebarFilter(art, 'golang')).toBe(true);
  });

  it('returns false when nothing matches', () => {
    expect(matchesSidebarFilter(art, 'python')).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// matchesLogEvent
// ---------------------------------------------------------------------------

describe('matchesLogEvent', () => {
  const event: LogEvent = {
    id: '1',
    timestamp: new Date().toISOString(),
    source: 'mcp',
    action: 'create',
    tool: 'create_wiki_article',
    slug: 'my-article',
    title: 'My Article',
    agent: 'Claude',
  };

  it('returns true when action matches', () => {
    expect(matchesLogEvent(event, 'create')).toBe(true);
  });

  it('returns true when tool matches', () => {
    expect(matchesLogEvent(event, 'create_wiki')).toBe(true);
  });

  it('returns true when agent matches', () => {
    expect(matchesLogEvent(event, 'claude')).toBe(true);
  });

  it('returns false when nothing matches', () => {
    expect(matchesLogEvent(event, 'python')).toBe(false);
  });

  it('returns true for empty query', () => {
    expect(matchesLogEvent(event, '')).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// buildSuggestionsFromArticles
// ---------------------------------------------------------------------------

describe('buildSuggestionsFromArticles', () => {
  const articles = [
    { slug: 'go-guide', title: 'Go Programming Guide', tags: ['golang', 'backend'], created_at: '', updated_at: '', version: 1 },
    { slug: 'js-guide', title: 'JavaScript Handbook', tags: ['javascript', 'frontend', 'aiagent-skill'], created_at: '', updated_at: '', version: 1 },
    { slug: 'rust-guide', title: 'Rust Book', tags: ['rust', 'systems'], created_at: '', updated_at: '', version: 1 },
  ];

  it('returns empty array for empty search term', () => {
    expect(buildSuggestionsFromArticles(articles, '')).toHaveLength(0);
    expect(buildSuggestionsFromArticles(articles, '   ')).toHaveLength(0);
  });

  it('returns title suggestions when title matches', () => {
    const results = buildSuggestionsFromArticles(articles, 'go');
    const titles = results.filter(r => r.type === 'title').map(r => r.value);
    expect(titles).toContain('Go Programming Guide');
  });

  it('returns tag suggestions when tag matches', () => {
    const results = buildSuggestionsFromArticles(articles, 'golang');
    const tags = results.filter(r => r.type === 'tag').map(r => r.value);
    expect(tags).toContain('golang');
  });

  it('excludes aiagent- prefixed tags', () => {
    const results = buildSuggestionsFromArticles(articles, 'aiagent');
    const tags = results.filter(r => r.type === 'tag').map(r => r.value);
    expect(tags).not.toContain('aiagent-skill');
  });

  it('caps results at 8', () => {
    const manyArticles = Array.from({ length: 20 }, (_, i) => ({
      slug: `article-${i}`,
      title: `Test Article ${i}`,
      tags: [`tag-${i}`],
      created_at: '',
      updated_at: '',
      version: 1,
    }));
    const results = buildSuggestionsFromArticles(manyArticles, 'test');
    expect(results.length).toBeLessThanOrEqual(8);
  });

  it('deduplicates suggestions', () => {
    const dupeArticles = [
      { slug: 'a1', title: 'Go Guide', tags: ['golang'], created_at: '', updated_at: '', version: 1 },
      { slug: 'a2', title: 'Go Guide', tags: ['golang'], created_at: '', updated_at: '', version: 1 },
    ];
    const results = buildSuggestionsFromArticles(dupeArticles, 'go');
    const titles = results.filter(r => r.type === 'title' && r.value === 'Go Guide');
    expect(titles).toHaveLength(1);
  });
});

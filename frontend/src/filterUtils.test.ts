import { describe, it, expect } from 'vitest';
import {
  evaluateBooleanQuery,
  getAutocompleteSearchTerm,
  applyAutocompleteSelection,
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

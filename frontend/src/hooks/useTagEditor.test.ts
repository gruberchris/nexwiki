import { describe, it, expect } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useTagEditor, isReservedTag } from './useTagEditor';
import type { Article } from '../types';

const articles: Article[] = [
  { title: 'A', slug: 'a', created_at: '', timestamp: '', version: 1, tags: ['golang', 'database', 'memory-nexwiki'] },
  { title: 'B', slug: 'b', created_at: '', timestamp: '', version: 1, tags: ['golang', 'wip'] },
];

const key = (k: string, shift = false) =>
  ({ key: k, shiftKey: shift, preventDefault: () => {} }) as React.KeyboardEvent;

describe('reserved tags', () => {
  it('identifies tool-managed memory-scope tags case-insensitively', () => {
    expect(isReservedTag('memory-nexwiki')).toBe(true);
    expect(isReservedTag('MEMORY-Foo')).toBe(true);
    expect(isReservedTag('memorable')).toBe(false);
    expect(isReservedTag('wip')).toBe(false);
  });

  // The server strips forged memory-scope tags regardless, so a UI that accepts one is offering
  // something it cannot deliver. This rule was previously enforced in three separate places.
  it('refuses the reserved prefix while typing', () => {
    const { result } = renderHook(() => useTagEditor([], articles));
    act(() => result.current.handleInputChange('memory-secret'));
    expect(result.current.tagInput).toBe('');

    act(() => result.current.handleInputChange('golang'));
    expect(result.current.tagInput).toBe('golang');
  });

  it('never suggests a reserved tag', () => {
    const { result } = renderHook(() => useTagEditor([], articles));
    act(() => result.current.handleInputChange('mem'));
    expect(result.current.suggestions).not.toContain('memory-nexwiki');
  });
});

describe('adding and removing tags', () => {
  it('commits on Enter and on comma', () => {
    const { result } = renderHook(() => useTagEditor([], articles));

    act(() => result.current.handleInputChange('alpha'));
    act(() => result.current.handleKeyDown(key('Enter')));
    expect(result.current.tags).toEqual(['alpha']);
    expect(result.current.tagInput).toBe('');

    act(() => result.current.handleInputChange('beta'));
    act(() => result.current.handleKeyDown(key(',')));
    expect(result.current.tags).toEqual(['alpha', 'beta']);
  });

  it('rejects duplicates case-insensitively', () => {
    const { result } = renderHook(() => useTagEditor(['Golang'], articles));
    act(() => result.current.handleInputChange('golang'));
    act(() => result.current.handleKeyDown(key('Enter')));
    expect(result.current.tags).toEqual(['Golang']);
  });

  it('ignores blank input', () => {
    const { result } = renderHook(() => useTagEditor([], articles));
    act(() => result.current.handleInputChange('   '));
    act(() => result.current.handleKeyDown(key('Enter')));
    expect(result.current.tags).toEqual([]);
  });

  it('removes a tag', () => {
    const { result } = renderHook(() => useTagEditor(['a', 'b'], articles));
    act(() => result.current.removeTag('a'));
    expect(result.current.tags).toEqual(['b']);
  });
});

describe('suggestions', () => {
  it('matches on substring and excludes tags already applied', () => {
    const { result } = renderHook(() => useTagEditor(['golang'], articles));
    act(() => result.current.handleInputChange('a'));

    expect(result.current.suggestions).toContain('database');
    expect(result.current.suggestions).not.toContain('golang'); // already applied
  });

  it('shows nothing for an empty query', () => {
    const { result } = renderHook(() => useTagEditor([], articles));
    expect(result.current.suggestions).toEqual([]);
  });

  it('deduplicates tags used by several articles', () => {
    const { result } = renderHook(() => useTagEditor([], articles));
    act(() => result.current.handleInputChange('golang'));
    expect(result.current.suggestions.filter((s) => s === 'golang')).toHaveLength(1);
  });
});

describe('keyboard navigation', () => {
  it('cycles forward through suggestions and back to the input', () => {
    const { result } = renderHook(() => useTagEditor([], articles));
    act(() => result.current.handleInputChange('a')); // database, golang
    const count = result.current.suggestions.length;
    expect(count).toBeGreaterThan(1);

    act(() => result.current.handleKeyDown(key('Tab')));
    expect(result.current.focusedIndex).toBe(0);

    for (let i = 1; i < count; i++) {
      act(() => result.current.handleKeyDown(key('Tab')));
    }
    expect(result.current.focusedIndex).toBe(count - 1);

    // Past the end returns to -1 so the user can always get back to free typing.
    act(() => result.current.handleKeyDown(key('Tab')));
    expect(result.current.focusedIndex).toBe(-1);
  });

  it('Shift+Tab moves backwards, wrapping to the last suggestion', () => {
    const { result } = renderHook(() => useTagEditor([], articles));
    act(() => result.current.handleInputChange('a'));
    const count = result.current.suggestions.length;

    act(() => result.current.handleKeyDown(key('Tab', true)));
    expect(result.current.focusedIndex).toBe(count - 1);
  });

  it('Enter commits the focused suggestion rather than the raw text', () => {
    const { result } = renderHook(() => useTagEditor([], articles));
    act(() => result.current.handleInputChange('data'));
    act(() => result.current.handleKeyDown(key('Tab')));

    const focused = result.current.suggestions[result.current.focusedIndex];
    act(() => result.current.handleKeyDown(key('Enter')));

    expect(result.current.tags).toEqual([focused]);
    expect(result.current.focusedIndex).toBe(-1);
  });

  it('resets the highlight when the suggestion set changes', () => {
    const { result } = renderHook(() => useTagEditor([], articles));
    act(() => result.current.handleInputChange('a'));
    act(() => result.current.handleKeyDown(key('Tab')));
    expect(result.current.focusedIndex).toBe(0);

    // Narrowing the query changes which tags are listed, so a stale index would highlight a
    // different tag than the one the user was looking at.
    act(() => result.current.handleInputChange('data'));
    expect(result.current.focusedIndex).toBe(-1);
  });
});

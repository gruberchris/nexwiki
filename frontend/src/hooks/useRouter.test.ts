import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useRouter, parseRoute } from './useRouter';

beforeEach(() => {
  window.history.pushState(null, '', '/');
});

// parseRoute holds all the URL grammar, so it is tested exhaustively as a plain function —
// no rendering required.
describe('parseRoute', () => {
  it.each([
    ['/', '', { route: 'home', slug: '' }],
    ['', '', { route: 'home', slug: '' }],
    ['/articles/go', '', { route: 'article', slug: 'go' }],
    ['/nonsense', '', { route: '404', slug: '' }],
    ['/articles', '', { route: '404', slug: '' }],
  ])('maps %s -> %s', (path, search, expected) => {
    expect(parseRoute(path, search)).toMatchObject(expected);
  });

  it('extracts the /new scaffold parameters', () => {
    const r = parseRoute('/new', '?title=My%20Page&type=plan');
    expect(r).toMatchObject({ route: 'new', prefillTitle: 'My Page', prefillType: 'plan' });
  });

  it('defaults the /new type to article', () => {
    expect(parseRoute('/new', '')).toMatchObject({ prefillType: 'article', prefillTitle: '' });
  });

  it('extracts the search term', () => {
    expect(parseRoute('/search', '?q=bleve')).toMatchObject({ route: 'search', searchQuery: 'bleve' });
  });

  it('handles slugs containing hyphens and dots', () => {
    expect(parseRoute('/articles/some-page.v2', '')).toMatchObject({ route: 'article', slug: 'some-page.v2' });
  });

  // Existence is the caller's concern — parsing only answers "what shape is this URL".
  it('does not decide whether an article exists', () => {
    expect(parseRoute('/articles/does-not-exist', '').route).toBe('article');
  });
});

describe('useRouter', () => {
  it('pushes history and updates path and search', () => {
    const { result } = renderHook(() => useRouter());
    act(() => result.current.navigate('/search?q=go'));

    expect(result.current.currentPath).toBe('/search');
    expect(result.current.currentSearch).toBe('?q=go');
    expect(window.location.pathname).toBe('/search');
  });

  it('accepts a URL without a leading slash', () => {
    const { result } = renderHook(() => useRouter());
    act(() => result.current.navigate('new?type=skill'));
    expect(result.current.currentPath).toBe('/new');
  });

  it('navigateTo resolves intents to URLs', () => {
    const { result } = renderHook(() => useRouter());

    act(() => result.current.navigateTo('home'));
    expect(result.current.currentPath).toBe('/');

    // A bare slug becomes an article URL — callers should not need to know the prefix.
    act(() => result.current.navigateTo('golang'));
    expect(result.current.currentPath).toBe('/articles/golang');

    act(() => result.current.navigateTo('new?title=X'));
    expect(result.current.currentPath).toBe('/new');

    act(() => result.current.navigateTo('search?q=y'));
    expect(result.current.currentPath).toBe('/search');
  });

  it('fires onRouteChange for pushState navigation AND browser back', () => {
    const onRouteChange = vi.fn();
    const { result } = renderHook(() => useRouter(onRouteChange));

    act(() => result.current.navigate('/articles/go'));
    expect(onRouteChange).toHaveBeenCalledTimes(1);

    // App closes the editor here; it must happen however the route changed, which is why the
    // callback is wired into both paths rather than only into navigate().
    act(() => {
      window.history.pushState(null, '', '/');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(onRouteChange).toHaveBeenCalledTimes(2);
    expect(result.current.currentPath).toBe('/');
  });

  it('removes its popstate listener on unmount', () => {
    const onRouteChange = vi.fn();
    const { unmount } = renderHook(() => useRouter(onRouteChange));
    unmount();

    window.dispatchEvent(new PopStateEvent('popstate'));
    expect(onRouteChange).not.toHaveBeenCalled();
  });
});

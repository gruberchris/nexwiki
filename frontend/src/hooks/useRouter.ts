import { useCallback, useEffect, useState } from 'react';

/**
 * NexWiki's hand-rolled router: history state, back/forward handling, and URL parsing.
 *
 * There is no routing library — navigation is `history.pushState` plus a `popstate` listener —
 * and that logic previously sat inline in App.tsx among 29 other pieces of state, where the URL
 * grammar was invisible.
 *
 * Route *parsing* is deliberately a plain function rather than part of the hook. It is where all
 * the branching lives (four route shapes plus query parameters), so making it independently
 * callable means it can be tested exhaustively without rendering anything.
 */

export type RouteName = 'home' | 'new' | 'search' | 'article' | '404';

export interface RouteInfo {
  route: RouteName;
  slug: string;
  /** Pre-filled title for /new, from the ?title= parameter. */
  prefillTitle?: string;
  /** Content scaffold to use for /new: 'article' | 'plan' | 'skill'. */
  prefillType?: string;
  /** The ?q= term for /search. */
  searchQuery?: string;
}

/**
 * Maps a path and query string onto a route. Pure and side-effect free.
 *
 * Note this never returns '404' for an `/articles/:slug` path: whether a slug exists depends on
 * the loaded article list, which is the caller's concern. Parsing answers "what shape is this
 * URL", not "does that article exist".
 */
export function parseRoute(path: string, search: string): RouteInfo {
  if (path === '/' || path === '') {
    return { route: 'home', slug: '' };
  }
  if (path === '/new') {
    const params = new URLSearchParams(search);
    return {
      route: 'new',
      slug: '',
      prefillTitle: params.get('title') || '',
      prefillType: params.get('type') || 'article',
    };
  }
  if (path === '/search') {
    const params = new URLSearchParams(search);
    return { route: 'search', slug: '', searchQuery: params.get('q') || '' };
  }
  if (path.startsWith('/articles/')) {
    return { route: 'article', slug: path.substring('/articles/'.length) };
  }
  return { route: '404', slug: '' };
}

/**
 * How the current route was reached: the initial page load, a pushState navigation (a click),
 * or the browser's back/forward buttons. Consumers use this to decide whether leaving-state
 * should be restored — deliberately navigating somewhere gives a fresh view, going Back gives
 * back the view you left.
 */
export type NavigationKind = 'initial' | 'push' | 'pop';

export interface UseRouterResult {
  currentPath: string;
  currentSearch: string;
  navigationKind: NavigationKind;
  /** Pushes a URL. Accepts a path with or without a leading slash. */
  navigate: (fullUrl: string) => void;
  /**
   * Navigates by intent rather than by URL: 'home', a bare article slug, or a 'new'/'search'
   * path. Callers pass a slug without needing to know it lives under /articles/.
   */
  navigateTo: (target: string) => void;
}

/**
 * @param onRouteChange runs on every navigation, including browser back/forward. App uses it to
 *        close the editor, which must happen however the route changed — a detail easy to miss
 *        when pushState and popstate are handled in separate places.
 */
export function useRouter(onRouteChange?: () => void): UseRouterResult {
  const [currentPath, setCurrentPath] = useState(window.location.pathname);
  const [currentSearch, setCurrentSearch] = useState(window.location.search);
  const [navigationKind, setNavigationKind] = useState<NavigationKind>('initial');

  // The app scrolls inside its own containers, so the browser's automatic scroll restoration
  // has nothing to restore and can only fight the app's explicit restoration on back-navigation.
  useEffect(() => {
    if ('scrollRestoration' in window.history) {
      window.history.scrollRestoration = 'manual';
    }
  }, []);

  const navigate = useCallback(
    (fullUrl: string) => {
      const cleanUrl = fullUrl.startsWith('/') ? fullUrl : '/' + fullUrl;
      window.history.pushState(null, '', cleanUrl);

      const [path, search] = cleanUrl.split('?');
      setCurrentPath(path);
      setCurrentSearch(search ? '?' + search : '');
      setNavigationKind('push');
      onRouteChange?.();
    },
    [onRouteChange],
  );

  const navigateTo = useCallback(
    (target: string) => {
      if (target === 'home') {
        navigate('/');
      } else if (
        target.startsWith('new') || target.startsWith('/new') ||
        target.startsWith('search') || target.startsWith('/search')
      ) {
        navigate(target);
      } else {
        navigate(`/articles/${target}`);
      }
    },
    [navigate],
  );

  // Browser back/forward must drive the same state as pushState navigation.
  useEffect(() => {
    const handlePopState = () => {
      setCurrentPath(window.location.pathname);
      setCurrentSearch(window.location.search);
      setNavigationKind('pop');
      onRouteChange?.();
    };
    window.addEventListener('popstate', handlePopState);
    return () => window.removeEventListener('popstate', handlePopState);
  }, [onRouteChange]);

  return { currentPath, currentSearch, navigationKind, navigate, navigateTo };
}

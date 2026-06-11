import { useEffect } from 'react';

// Watches OS-level prefers-color-scheme changes and calls onSchemeChange(isDark)
// only when the user has not saved an explicit preference to localStorage ('theme' key).
// Once the user manually toggles the mode, localStorage['theme'] is set and this
// hook stops overriding their choice.
export function useBrowserColorScheme(onSchemeChange: (isDark: boolean) => void): void {
  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const handler = (e: MediaQueryListEvent) => {
      if (localStorage.getItem('theme') === null) {
        onSchemeChange(e.matches);
      }
    };
    // Safari only gained MediaQueryList.addEventListener in version 14;
    // fall back to the deprecated addListener API for older versions.
    if (typeof mq.addEventListener === 'function') {
      mq.addEventListener('change', handler);
      return () => mq.removeEventListener('change', handler);
    }
    mq.addListener(handler);
    return () => mq.removeListener(handler);
  }, [onSchemeChange]);
}

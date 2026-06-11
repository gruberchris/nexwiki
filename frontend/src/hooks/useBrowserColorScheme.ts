import { useEffect } from 'react';

// Pre-2020 shape of MediaQueryList (Safari <14), which lacks add/removeEventListener.
// Typed locally so we don't reference the deprecated signatures on MediaQueryList itself.
interface LegacyMediaQueryList {
  addListener(handler: (e: MediaQueryListEvent) => void): void;
  removeListener(handler: (e: MediaQueryListEvent) => void): void;
}

// Watches OS-level prefers-color-scheme changes and calls onSchemeChange(isDark)
// only while the user has not saved an explicit 'light'/'dark' preference to
// localStorage ('theme' key). An absent key or 'auto' both mean "follow the
// system"; once the user picks an explicit mode, this hook stops overriding it.
export function useBrowserColorScheme(onSchemeChange: (isDark: boolean) => void): void {
  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const handler = (e: MediaQueryListEvent) => {
      const saved = localStorage.getItem('theme');
      if (saved === null || saved === 'auto') {
        onSchemeChange(e.matches);
      }
    };
    // Safari only gained MediaQueryList.addEventListener in version 14;
    // fall back to the deprecated addListener API for older versions.
    if (typeof mq.addEventListener === 'function') {
      mq.addEventListener('change', handler);
      return () => mq.removeEventListener('change', handler);
    }
    const legacyMq = mq as unknown as LegacyMediaQueryList;
    legacyMq.addListener(handler);
    return () => legacyMq.removeListener(handler);
  }, [onSchemeChange]);
}

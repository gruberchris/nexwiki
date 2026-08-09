import { useCallback, useEffect, useState } from 'react';
import type { ThemeMode } from '../types';
import type { Theme } from '../components/ThemeManagerModal';
import { useBrowserColorScheme } from './useBrowserColorScheme';

/**
 * Owns everything about how the wiki looks: the light/dark mode, the active palette, the custom
 * theme collection, and the CSS custom properties applied to :root.
 *
 * Extracted from App.tsx, which held all of this inline among 29 unrelated pieces of state.
 * Keeping it together matters because the pieces are coupled by one subtle rule that is easy to
 * break from a distance: the localStorage 'theme' key records an *explicit* user choice only.
 * Writing it at any other time silently stops the app following the OS colour scheme, so every
 * write to that key lives in this file.
 */

/** localStorage key recording an explicit light/dark choice. Absent means "follow the browser". */
const MODE_KEY = 'theme';
/** localStorage key recording the selected palette by name. */
const ACTIVE_THEME_KEY = 'active-theme';

/** Boot-time inputs from GET /api/config, which decide the initially active palette. */
export interface ThemeBootConfig {
  defaultTheme: string;
  scheduledTheme: string;
  schedulingEnabled: boolean;
}

export interface UseThemeResult {
  themes: Theme[];
  activeThemeName: string;
  themeMode: ThemeMode;
  darkMode: boolean;
  themeModalOpen: boolean;
  setThemeModalOpen: (open: boolean) => void;
  /** Cycles Light → Dark → Auto. */
  cycleThemeMode: () => void;
  selectTheme: (name: string) => void;
  saveTheme: (theme: Theme) => Promise<void>;
  deleteTheme: (name: string) => Promise<void>;
  refreshThemes: () => Promise<void>;
  /** Loads themes and resolves the active palette during app boot. */
  initialize: (config: ThemeBootConfig) => Promise<void>;
}

function prefersDark(): boolean {
  return window.matchMedia('(prefers-color-scheme: dark)').matches;
}

function readSavedMode(): 'light' | 'dark' | null {
  const saved = localStorage.getItem(MODE_KEY);
  return saved === 'dark' || saved === 'light' ? saved : null;
}

/**
 * @param onAlert surfaces success messages; injected so this hook does not own notification UI.
 */
export function useTheme(onAlert: (type: 'success' | 'error', text: string) => void): UseThemeResult {
  // 'light' | 'dark' = explicit user choice (persisted); 'auto' = follow the
  // browser's prefers-color-scheme (the default, stored as an absent key).
  const [themeMode, setThemeMode] = useState<ThemeMode>(() => readSavedMode() ?? 'auto');
  const [darkMode, setDarkMode] = useState(() => readSavedMode() === 'dark' || (readSavedMode() === null && prefersDark()));

  const [themes, setThemes] = useState<Theme[]>([]);
  const [activeThemeName, setActiveThemeName] = useState('default');
  const [themeModalOpen, setThemeModalOpen] = useState(false);

  // Follow browser prefers-color-scheme changes unless the user has explicitly chosen a mode.
  useBrowserColorScheme(setDarkMode);

  // Project the active palette's variant onto :root as CSS custom properties.
  useEffect(() => {
    if (themes.length === 0) return;
    const currentTheme = themes.find((t) => t.name === activeThemeName) || themes[0];
    if (!currentTheme) return;

    const variant = darkMode ? currentTheme.dark : currentTheme.light;
    const root = document.documentElement;

    // Apply is-dark or light class for standard tailwind and CodeMirror
    if (darkMode) {
      root.classList.add('dark');
    } else {
      root.classList.remove('dark');
    }

    Object.entries(variant).forEach(([key, val]) => {
      root.style.setProperty(`--${key.replace(/_/g, '-')}`, val);
    });
  }, [activeThemeName, themes, darkMode]);

  const refreshThemes = useCallback(async () => {
    try {
      const res = await fetch('/api/themes');
      if (res.ok) {
        setThemes(((await res.json()) as Theme[] | null) ?? []);
      }
    } catch (err) {
      console.error('Failed to fetch themes:', err);
    }
  }, []);

  const selectTheme = useCallback(
    (name: string) => {
      setActiveThemeName(name);
      localStorage.setItem(ACTIVE_THEME_KEY, name);

      // Apply the palette's default variant, but never persist MODE_KEY here: that key marks an
      // explicit user mode choice, and setting it would stop the app following the OS scheme.
      const targetTheme = themes.find((t) => t.name === name);
      if (targetTheme) {
        setDarkMode(targetTheme.default_mode === 'dark');
      }
    },
    [themes],
  );

  const cycleThemeMode = useCallback(() => {
    const next: ThemeMode = themeMode === 'light' ? 'dark' : themeMode === 'dark' ? 'auto' : 'light';
    setThemeMode(next);
    if (next === 'auto') {
      localStorage.removeItem(MODE_KEY);
      setDarkMode(prefersDark());
    } else {
      localStorage.setItem(MODE_KEY, next);
      setDarkMode(next === 'dark');
    }
  }, [themeMode]);

  const saveTheme = useCallback(
    async (newTheme: Theme) => {
      const res = await fetch('/api/themes', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(newTheme),
      });
      if (!res.ok) {
        const errData = await res.json();
        throw new Error(errData.error || 'Failed to save theme');
      }
      onAlert('success', `Theme "${newTheme.name}" saved successfully!`);
      await refreshThemes();
    },
    [onAlert, refreshThemes],
  );

  const deleteTheme = useCallback(
    async (name: string) => {
      const res = await fetch(`/api/themes/${encodeURIComponent(name)}`, { method: 'DELETE' });
      if (!res.ok) {
        const errData = await res.json();
        throw new Error(errData.error || 'Failed to delete theme');
      }
      onAlert('success', `Theme "${name}" deleted successfully!`);
      if (activeThemeName === name) {
        selectTheme('default');
      }
      await refreshThemes();
    },
    [activeThemeName, onAlert, refreshThemes, selectTheme],
  );

  const initialize = useCallback(
    async (config: ThemeBootConfig) => {
      await refreshThemes();

      // Scheduling wins, then a saved choice, then the server's configured default.
      let finalThemeName = config.defaultTheme;
      if (config.schedulingEnabled && config.scheduledTheme) {
        finalThemeName = config.scheduledTheme;
      } else {
        const savedTheme = localStorage.getItem(ACTIVE_THEME_KEY);
        if (savedTheme) {
          finalThemeName = savedTheme;
        }
      }
      setActiveThemeName(finalThemeName);

      // An explicit saved user choice wins; otherwise keep mirroring the browser scheme, which
      // the darkMode initial state already applied. Never write MODE_KEY here — it must record
      // only an explicit user toggle.
      const savedMode = readSavedMode();
      if (savedMode) {
        setDarkMode(savedMode === 'dark');
      }
    },
    [refreshThemes],
  );

  return {
    themes,
    activeThemeName,
    themeMode,
    darkMode,
    themeModalOpen,
    setThemeModalOpen,
    cycleThemeMode,
    selectTheme,
    saveTheme,
    deleteTheme,
    refreshThemes,
    initialize,
  };
}

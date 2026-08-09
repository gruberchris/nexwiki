import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useTheme } from './useTheme';
import type { Theme } from '../components/ThemeManagerModal';

const palette = (name: string, defaultMode: 'light' | 'dark' = 'light'): Theme =>
  ({
    name,
    default_mode: defaultMode,
    light: { bg_primary: '#fff', text_primary: '#000' },
    dark: { bg_primary: '#000', text_primary: '#fff' },
  }) as unknown as Theme;

function mockMatchMedia(matches: boolean) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn().mockImplementation((query: string) => ({
      matches,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  );
}

beforeEach(() => {
  localStorage.clear();
  document.documentElement.className = '';
  document.documentElement.removeAttribute('style');
  mockMatchMedia(false);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('useTheme mode handling', () => {
  it('defaults to auto and follows the browser when no explicit choice is saved', () => {
    mockMatchMedia(true); // OS prefers dark
    const { result } = renderHook(() => useTheme(vi.fn()));

    expect(result.current.themeMode).toBe('auto');
    expect(result.current.darkMode).toBe(true);
    expect(localStorage.getItem('theme')).toBeNull();
  });

  it('restores an explicitly saved mode over the browser preference', () => {
    localStorage.setItem('theme', 'light');
    mockMatchMedia(true); // OS prefers dark, but the user chose light

    const { result } = renderHook(() => useTheme(vi.fn()));
    expect(result.current.themeMode).toBe('light');
    expect(result.current.darkMode).toBe(false);
  });

  it('cycles light -> dark -> auto, and only persists explicit choices', () => {
    const { result } = renderHook(() => useTheme(vi.fn()));

    act(() => result.current.cycleThemeMode()); // auto -> light
    expect(result.current.themeMode).toBe('light');
    expect(localStorage.getItem('theme')).toBe('light');

    act(() => result.current.cycleThemeMode()); // light -> dark
    expect(result.current.themeMode).toBe('dark');
    expect(result.current.darkMode).toBe(true);
    expect(localStorage.getItem('theme')).toBe('dark');

    act(() => result.current.cycleThemeMode()); // dark -> auto
    expect(result.current.themeMode).toBe('auto');
    // The key must be REMOVED, not set to 'auto'. Leaving any value behind stops the app
    // following OS colour-scheme changes — the subtle coupling this hook exists to contain.
    expect(localStorage.getItem('theme')).toBeNull();
  });
});

describe('useTheme palette selection', () => {
  it('persists the chosen palette and applies its default mode without touching the mode key', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true, json: async () => [palette('default'), palette('midnight', 'dark')],
    }));

    const { result } = renderHook(() => useTheme(vi.fn()));
    await act(async () => { await result.current.refreshThemes(); });

    act(() => result.current.selectTheme('midnight'));

    expect(result.current.activeThemeName).toBe('midnight');
    expect(localStorage.getItem('active-theme')).toBe('midnight');
    expect(result.current.darkMode).toBe(true);
    // Selecting a dark palette is not an explicit *mode* choice.
    expect(localStorage.getItem('theme')).toBeNull();
  });

  it('projects the active palette onto :root as CSS custom properties', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true, json: async () => [palette('default')],
    }));

    const { result } = renderHook(() => useTheme(vi.fn()));
    await act(async () => { await result.current.refreshThemes(); });

    await waitFor(() => {
      expect(document.documentElement.style.getPropertyValue('--bg-primary')).toBe('#fff');
    });
    expect(document.documentElement.classList.contains('dark')).toBe(false);
  });
});

describe('useTheme boot resolution', () => {
  const bootWith = async (config: Parameters<ReturnType<typeof useTheme>['initialize']>[0]) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true, json: async () => [palette('default'), palette('halloween')],
    }));
    const { result } = renderHook(() => useTheme(vi.fn()));
    await act(async () => { await result.current.initialize(config); });
    return result;
  };

  it('prefers a scheduled theme when scheduling is enabled', async () => {
    const result = await bootWith({ defaultTheme: 'default', scheduledTheme: 'halloween', schedulingEnabled: true });
    expect(result.current.activeThemeName).toBe('halloween');
  });

  it('prefers the saved theme over the server default when scheduling is off', async () => {
    localStorage.setItem('active-theme', 'halloween');
    const result = await bootWith({ defaultTheme: 'default', scheduledTheme: '', schedulingEnabled: false });
    expect(result.current.activeThemeName).toBe('halloween');
  });

  it('falls back to the server default with nothing saved', async () => {
    const result = await bootWith({ defaultTheme: 'default', scheduledTheme: 'halloween', schedulingEnabled: false });
    expect(result.current.activeThemeName).toBe('default');
  });
});

describe('useTheme CRUD', () => {
  it('reports a save failure through the thrown error rather than an alert', async () => {
    const onAlert = vi.fn();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: false, json: async () => ({ error: 'name already taken' }),
    }));

    const { result } = renderHook(() => useTheme(onAlert));
    await expect(result.current.saveTheme(palette('dupe'))).rejects.toThrow('name already taken');
    expect(onAlert).not.toHaveBeenCalled();
  });

  it('alerts on a successful save and refreshes the list', async () => {
    const onAlert = vi.fn();
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, json: async () => ({}) })          // POST
      .mockResolvedValueOnce({ ok: true, json: async () => [palette('x')] }); // refresh
    vi.stubGlobal('fetch', fetchMock);

    const { result } = renderHook(() => useTheme(onAlert));
    await act(async () => { await result.current.saveTheme(palette('x')); });

    expect(onAlert).toHaveBeenCalledWith('success', expect.stringContaining('saved successfully'));
    expect(result.current.themes).toHaveLength(1);
  });

  it('falls back to the default palette when the active one is deleted', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => [palette('default')] });
    vi.stubGlobal('fetch', fetchMock);

    const { result } = renderHook(() => useTheme(vi.fn()));
    await act(async () => { await result.current.refreshThemes(); });
    act(() => result.current.selectTheme('doomed'));
    expect(result.current.activeThemeName).toBe('doomed');

    await act(async () => { await result.current.deleteTheme('doomed'); });
    expect(result.current.activeThemeName).toBe('default');
  });
});

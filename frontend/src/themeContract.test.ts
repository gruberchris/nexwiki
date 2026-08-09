import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { readFileSync, readdirSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { useTheme } from './hooks/useTheme';
import type { Theme, ThemeColors } from './components/ThemeManagerModal';

/**
 * The theme contract.
 *
 * NexWiki's colours travel through four places that must all agree on the same ten names:
 *
 *   1. Go        server/themes.go     ThemeColors JSON tags   (bg_primary, ...)
 *   2. TypeScript ThemeManagerModal    ThemeColors interface
 *   3. Runtime   useTheme              --bg-primary, ... set on :root
 *   4. Styling   index.css :root defaults, and the Tailwind theme that exposes each as a utility
 *
 * Nothing in the type system connects them. Drop or rename one in step 4 and every affected
 * utility class silently resolves to nothing — a broken colour in a theme that may not be looked
 * at for months. There are 16 built-in themes with light and dark variants, so 32 colour schemes
 * ride on this and no test previously covered any of it.
 *
 * These tests exist as a *pre-migration baseline* for the Tailwind v4 upgrade, which moves the
 * colour mappings out of tailwind.config.js and into CSS `@theme`. They are written to pass on v3
 * and on v4, so they fail only if the port actually loses something.
 */

const here = dirname(fileURLToPath(import.meta.url));
const readSrc = (rel: string) => readFileSync(resolve(here, rel), 'utf8');

/** The canonical ten, in the JSON form Go emits and the frontend consumes. */
const THEME_COLOR_KEYS: (keyof ThemeColors)[] = [
  'bg_primary',
  'bg_secondary',
  'text_primary',
  'text_secondary',
  'text_muted',
  'border_color',
  'accent_primary',
  'accent_secondary',
  'accent_hover',
  'accent_bg',
];

/** useTheme derives the custom property by swapping underscores for hyphens. */
const cssVarFor = (key: string) => `--${key.replace(/_/g, '-')}`;

/**
 * The region where Tailwind's colour tokens are defined — and nothing else.
 *
 * On v3 that is tailwind.config.js. On v4 it is the `@theme { ... }` block inside index.css.
 * Returning only that region is what makes the assertions meaningful: scanning the whole of
 * index.css would let ordinary CSS rules that happen to reference a variable satisfy a check about
 * the *mapping*. Reading whichever exists lets the same tests run before and after the migration.
 */
function themeMappingSurface(): string {
  let surface = '';
  try {
    surface += readSrc('../tailwind.config.js');
  } catch {
    /* v4: configuration lives in CSS */
  }

  const css = readSrc('./index.css');
  const start = css.indexOf('@theme');
  if (start !== -1) {
    // Brace-match so nested blocks inside @theme are included rather than truncating at the first
    // closing brace.
    let depth = 0;
    for (let i = css.indexOf('{', start); i < css.length; i++) {
      if (css[i] === '{') depth++;
      if (css[i] === '}') {
        depth--;
        if (depth === 0) {
          surface += css.slice(start, i + 1);
          break;
        }
      }
    }
  }
  return surface;
}

/** Every component/hook source file, for scanning which theme utilities are actually referenced. */
function componentSources(): string[] {
  const roots = ['./components', './hooks', '.'];
  const files: string[] = [];
  for (const root of roots) {
    for (const entry of readdirSync(resolve(here, root), { withFileTypes: true })) {
      if (!entry.isFile()) continue;
      if (!/\.(tsx?|jsx?)$/.test(entry.name) || entry.name.includes('.test.')) continue;
      files.push(readFileSync(resolve(here, root, entry.name), 'utf8'));
    }
  }
  return files;
}

/** A fully populated variant, so a missing projection shows up as an unset property. */
const fullColors = (tint: string): ThemeColors =>
  Object.fromEntries(THEME_COLOR_KEYS.map((k) => [k, tint])) as unknown as ThemeColors;

const fullTheme = (name: string): Theme =>
  ({
    name,
    default_mode: 'light',
    light: fullColors('#111111'),
    dark: fullColors('#eeeeee'),
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

describe('theme contract: the ten colours are declared everywhere they are consumed', () => {
  it('index.css declares a :root default for every colour', () => {
    // The defaults matter: useTheme only projects a palette once /api/themes resolves, so these
    // are what the app renders with on first paint and if that request ever fails.
    const css = readSrc('./index.css');
    const rootBlock = css.slice(css.indexOf(':root'), css.indexOf('}', css.indexOf(':root')));

    for (const key of THEME_COLOR_KEYS) {
      expect(rootBlock, `index.css :root is missing ${cssVarFor(key)}`).toContain(
        `${cssVarFor(key)}:`,
      );
    }
  });

  it('the Tailwind theme maps every colour', () => {
    // Scoped to the *mapping region* only — the config file on v3, the `@theme` block on v4.
    // An earlier version of this test concatenated the whole of index.css, which made it useless:
    // index.css references var(--accent-bg) in its own rules, so deleting the Tailwind mapping
    // for it still passed. Verified by deleting one and watching this fail.
    const surface = themeMappingSurface();

    for (const key of THEME_COLOR_KEYS) {
      expect(
        surface,
        `no Tailwind colour maps to ${cssVarFor(key)} — utilities for it resolve to nothing`,
      ).toContain(`var(${cssVarFor(key)})`);
    }
  });

  it('every theme utility the components use is declared', () => {
    // The assertion that actually protects the app. Components reference these tokens by name
    // hundreds of times — text-themeTextMuted alone appears 73 times — and Tailwind silently drops
    // a class whose colour it cannot resolve. Renaming a token during the v4 port (camelCase to
    // kebab-case is the obvious temptation) would strip colour from most of the UI with no build
    // error and no failing unit test.
    const surface = themeMappingSurface();
    const declared = new Set(
      [...surface.matchAll(/\btheme[A-Z]\w*/g)].map((m) => m[0]),
    );

    const used = new Set<string>();
    for (const file of componentSources()) {
      for (const match of file.matchAll(/-(theme[A-Z]\w*)\b/g)) {
        used.add(match[1]);
      }
    }

    expect(used.size, 'found no theme utilities in the components — has the scan broken?').
      toBeGreaterThan(5);

    for (const token of [...used].sort()) {
      expect(
        declared.has(token),
        `components use ${token} but the Tailwind theme does not declare it`,
      ).toBe(true);
    }
  });

  it('the TypeScript interface and the canonical list agree', () => {
    // Guards the direction the other assertions cannot see: a colour added to the interface but
    // never wired into CSS.
    const source = readSrc('./components/ThemeManagerModal.tsx');
    const interfaceBlock = source.slice(
      source.indexOf('export interface ThemeColors'),
      source.indexOf('}', source.indexOf('export interface ThemeColors')),
    );
    const declared = [...interfaceBlock.matchAll(/^\s+(\w+):\s*string;/gm)].map((m) => m[1]);

    expect(declared.sort()).toEqual([...THEME_COLOR_KEYS].sort());
  });
});

describe('theme contract: applying a theme projects every colour onto :root', () => {
  it('sets all ten custom properties in light mode', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({ ok: true, json: async () => [fullTheme('default')] }),
    );

    const { result } = renderHook(() => useTheme(vi.fn()));
    await act(async () => {
      await result.current.refreshThemes();
    });

    await waitFor(() => {
      expect(document.documentElement.style.getPropertyValue('--bg-primary')).toBe('#111111');
    });

    for (const key of THEME_COLOR_KEYS) {
      expect(
        document.documentElement.style.getPropertyValue(cssVarFor(key)),
        `${cssVarFor(key)} was never projected onto :root`,
      ).toBe('#111111');
    }
  });

  it('sets all ten custom properties in dark mode, and toggles the dark class', async () => {
    // Dark mode is the axis the migration is most likely to break: `darkMode: 'class'` in v3
    // becomes a `@custom-variant` in v4, and every theme has a dark variant.
    mockMatchMedia(true);
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({ ok: true, json: async () => [fullTheme('default')] }),
    );

    const { result } = renderHook(() => useTheme(vi.fn()));
    await act(async () => {
      await result.current.refreshThemes();
    });

    await waitFor(() => {
      expect(document.documentElement.classList.contains('dark')).toBe(true);
    });

    for (const key of THEME_COLOR_KEYS) {
      expect(
        document.documentElement.style.getPropertyValue(cssVarFor(key)),
        `${cssVarFor(key)} was never projected in dark mode`,
      ).toBe('#eeeeee');
    }
  });

  it('projects every colour for every theme it is given', async () => {
    // Stands in for the 16 built-in themes: the Go side asserts each one defines all ten colours,
    // and this asserts the projection is per-theme rather than something the first theme happens
    // to leave behind on :root.
    const themes = ['default', 'halloween', 'christmas'].map(fullTheme);
    themes[1].light = fullColors('#ff6600');
    themes[2].light = fullColors('#cc0000');

    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({ ok: true, json: async () => themes }),
    );

    const { result } = renderHook(() => useTheme(vi.fn()));
    await act(async () => {
      await result.current.refreshThemes();
    });
    await waitFor(() => {
      expect(document.documentElement.style.getPropertyValue('--bg-primary')).toBe('#111111');
    });

    for (const [name, tint] of [
      ['halloween', '#ff6600'],
      ['christmas', '#cc0000'],
    ] as const) {
      act(() => {
        result.current.selectTheme(name);
      });
      await waitFor(() => {
        expect(document.documentElement.style.getPropertyValue('--bg-primary')).toBe(tint);
      });
      for (const key of THEME_COLOR_KEYS) {
        expect(
          document.documentElement.style.getPropertyValue(cssVarFor(key)),
          `theme ${name}: ${cssVarFor(key)} did not update`,
        ).toBe(tint);
      }
    }
  });
});

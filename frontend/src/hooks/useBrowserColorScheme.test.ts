import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useBrowserColorScheme } from './useBrowserColorScheme';

// ---------------------------------------------------------------------------
// useBrowserColorScheme
// ---------------------------------------------------------------------------

describe('useBrowserColorScheme', () => {
  type ChangeHandler = (e: MediaQueryListEvent) => void;
  let registeredHandlers: ChangeHandler[];
  let mockMq: {
    matches: boolean;
    addEventListener: ReturnType<typeof vi.fn>;
    removeEventListener: ReturnType<typeof vi.fn>;
  };

  beforeEach(() => {
    registeredHandlers = [];
    mockMq = {
      matches: false,
      addEventListener: vi.fn((_type: string, handler: ChangeHandler) => {
        registeredHandlers.push(handler);
      }),
      removeEventListener: vi.fn((_type: string, handler: ChangeHandler) => {
        registeredHandlers = registeredHandlers.filter(h => h !== handler);
      }),
    };
    vi.spyOn(window, 'matchMedia').mockReturnValue(mockMq as unknown as MediaQueryList);
    localStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
  });

  const fireChange = (matches: boolean) => {
    registeredHandlers.forEach(h => h({ matches } as MediaQueryListEvent));
  };

  it('registers a listener for "(prefers-color-scheme: dark)"', () => {
    const onChange = vi.fn();
    renderHook(() => useBrowserColorScheme(onChange));
    expect(window.matchMedia).toHaveBeenCalledWith('(prefers-color-scheme: dark)');
    expect(mockMq.addEventListener).toHaveBeenCalledWith('change', expect.any(Function));
  });

  it('calls onSchemeChange(true) when OS switches to dark and no user preference is saved', () => {
    const onChange = vi.fn();
    renderHook(() => useBrowserColorScheme(onChange));

    act(() => { fireChange(true); });

    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it('calls onSchemeChange(false) when OS switches to light and no user preference is saved', () => {
    const onChange = vi.fn();
    renderHook(() => useBrowserColorScheme(onChange));

    act(() => { fireChange(false); });

    expect(onChange).toHaveBeenCalledWith(false);
  });

  it('does NOT call onSchemeChange when localStorage has an explicit "light" preference', () => {
    localStorage.setItem('theme', 'light');
    const onChange = vi.fn();
    renderHook(() => useBrowserColorScheme(onChange));

    act(() => { fireChange(true); });

    expect(onChange).not.toHaveBeenCalled();
  });

  it('does NOT call onSchemeChange when localStorage has an explicit "dark" preference', () => {
    localStorage.setItem('theme', 'dark');
    const onChange = vi.fn();
    renderHook(() => useBrowserColorScheme(onChange));

    act(() => { fireChange(false); });

    expect(onChange).not.toHaveBeenCalled();
  });

  it('removes the listener on unmount', () => {
    const onChange = vi.fn();
    const { unmount } = renderHook(() => useBrowserColorScheme(onChange));

    unmount();

    act(() => { fireChange(true); });

    expect(onChange).not.toHaveBeenCalled();
    expect(mockMq.removeEventListener).toHaveBeenCalledWith('change', expect.any(Function));
  });

  it('resumes following OS after localStorage preference is cleared', () => {
    localStorage.setItem('theme', 'light');
    const onChange = vi.fn();
    renderHook(() => useBrowserColorScheme(onChange));

    act(() => { fireChange(true); });
    expect(onChange).not.toHaveBeenCalled();

    // Simulate user clearing their manual preference (e.g., future "follow browser" toggle)
    localStorage.removeItem('theme');
    act(() => { fireChange(true); });
    expect(onChange).toHaveBeenCalledWith(true);
  });
});

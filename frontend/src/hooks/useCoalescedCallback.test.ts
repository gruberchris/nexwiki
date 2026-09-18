import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useCoalescedCallback } from './useCoalescedCallback';

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('useCoalescedCallback', () => {
  it('runs an isolated call immediately, and only once', () => {
    const fn = vi.fn();
    const { result } = renderHook(() => useCoalescedCallback(fn, 250));

    act(() => result.current());
    expect(fn).toHaveBeenCalledTimes(1);

    act(() => vi.advanceTimersByTime(1000));
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('collapses a burst into one immediate run and one trailing run after it goes quiet', () => {
    const fn = vi.fn();
    const { result } = renderHook(() => useCoalescedCallback(fn, 250));

    // A bulk write: one update per document, arriving faster than the delay.
    act(() => {
      for (let i = 0; i < 500; i++) {
        result.current();
        vi.advanceTimersByTime(1);
      }
    });
    expect(fn).toHaveBeenCalledTimes(1);

    // The trailing run waits for the delay after the *last* call.
    act(() => vi.advanceTimersByTime(248));
    expect(fn).toHaveBeenCalledTimes(1);
    act(() => vi.advanceTimersByTime(1));
    expect(fn).toHaveBeenCalledTimes(2);

    act(() => vi.advanceTimersByTime(1000));
    expect(fn).toHaveBeenCalledTimes(2);
  });

  it('runs calls spaced wider than the delay each immediately', () => {
    const fn = vi.fn();
    const { result } = renderHook(() => useCoalescedCallback(fn, 250));

    act(() => result.current());
    act(() => vi.advanceTimersByTime(300));
    act(() => result.current());
    expect(fn).toHaveBeenCalledTimes(2);
  });

  it('uses the latest callback for the trailing run', () => {
    const first = vi.fn();
    const second = vi.fn();
    const { result, rerender } = renderHook(({ fn }) => useCoalescedCallback(fn, 250), {
      initialProps: { fn: first },
    });

    act(() => result.current());
    act(() => result.current());
    rerender({ fn: second });
    act(() => vi.advanceTimersByTime(250));

    expect(first).toHaveBeenCalledTimes(1);
    expect(second).toHaveBeenCalledTimes(1);
  });

  it('returns a stable function across renders', () => {
    const { result, rerender } = renderHook(({ fn }) => useCoalescedCallback(fn, 250), {
      initialProps: { fn: vi.fn() },
    });
    const before = result.current;
    rerender({ fn: vi.fn() });
    expect(result.current).toBe(before);
  });

  it('drops a pending trailing run on unmount', () => {
    const fn = vi.fn();
    const { result, unmount } = renderHook(() => useCoalescedCallback(fn, 250));

    act(() => result.current());
    act(() => result.current());
    unmount();
    act(() => vi.advanceTimersByTime(1000));

    expect(fn).toHaveBeenCalledTimes(1);
  });
});

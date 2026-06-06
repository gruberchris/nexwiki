import { describe, it, expect, vi, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useRef } from 'react';
import { useClickOutside } from './useClickOutside';
import { useEscapeKey } from './useEscapeKey';

afterEach(() => {
  vi.clearAllMocks();
});

// ---------------------------------------------------------------------------
// useClickOutside
// ---------------------------------------------------------------------------

describe('useClickOutside', () => {
  it('does not fire callback when enabled is false', () => {
    const callback = vi.fn();
    const { result } = renderHook(() => {
      const ref = useRef<HTMLElement>(null);
      useClickOutside(ref, callback, false);
      return ref;
    });

    // Dispatch a mousedown anywhere — should not trigger callback
    act(() => {
      document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    });
    expect(callback).not.toHaveBeenCalled();
  });

  it('fires callback when clicking outside the ref element', () => {
    const callback = vi.fn();
    const div = document.createElement('div');
    document.body.appendChild(div);

    const { unmount } = renderHook(() => {
      const ref = useRef<HTMLElement | null>(div);
      useClickOutside(ref, callback, true);
    });

    act(() => {
      document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    });
    expect(callback).toHaveBeenCalledTimes(1);

    unmount();
    document.body.removeChild(div);
  });

  it('does not fire callback when clicking inside the ref element', () => {
    const callback = vi.fn();
    const div = document.createElement('div');
    document.body.appendChild(div);

    renderHook(() => {
      const ref = useRef<HTMLElement | null>(div);
      useClickOutside(ref, callback, true);
    });

    act(() => {
      div.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    });
    expect(callback).not.toHaveBeenCalled();

    document.body.removeChild(div);
  });

  it('removes event listener on unmount', () => {
    const callback = vi.fn();
    const div = document.createElement('div');
    document.body.appendChild(div);

    const { unmount } = renderHook(() => {
      const ref = useRef<HTMLElement | null>(div);
      useClickOutside(ref, callback, true);
    });

    unmount();

    act(() => {
      document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    });
    // After unmount, callback should not be called
    expect(callback).not.toHaveBeenCalled();

    document.body.removeChild(div);
  });
});

// ---------------------------------------------------------------------------
// useEscapeKey
// ---------------------------------------------------------------------------

describe('useEscapeKey', () => {
  it('does not add listener when isOpen is false', () => {
    const onClose = vi.fn();
    renderHook(() => useEscapeKey(false, onClose));

    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    });
    expect(onClose).not.toHaveBeenCalled();
  });

  it('fires callback when Escape key is pressed and isOpen is true', () => {
    const onClose = vi.fn();
    renderHook(() => useEscapeKey(true, onClose));

    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('does not fire callback for non-Escape keys', () => {
    const onClose = vi.fn();
    renderHook(() => useEscapeKey(true, onClose));

    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }));
    });
    expect(onClose).not.toHaveBeenCalled();
  });

  it('removes listener on unmount', () => {
    const onClose = vi.fn();
    const { unmount } = renderHook(() => useEscapeKey(true, onClose));

    unmount();

    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    });
    expect(onClose).not.toHaveBeenCalled();
  });

  it('removes listener when isOpen transitions to false', () => {
    const onClose = vi.fn();
    const { rerender } = renderHook(
      ({ isOpen }) => useEscapeKey(isOpen, onClose),
      { initialProps: { isOpen: true } }
    );

    rerender({ isOpen: false });

    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    });
    expect(onClose).not.toHaveBeenCalled();
  });
});

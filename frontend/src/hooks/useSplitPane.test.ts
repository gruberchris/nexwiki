import { describe, it, expect } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useSplitPane } from './useSplitPane';

/** Gives the hook a container with a known geometry so percentages are predictable. */
function attachContainer(ref: React.RefObject<HTMLDivElement | null>, left: number, width: number) {
  const el = document.createElement('div');
  el.getBoundingClientRect = () =>
    ({ left, width, top: 0, right: left + width, bottom: 0, height: 0, x: left, y: 0, toJSON: () => ({}) });
  (ref as { current: HTMLDivElement | null }).current = el;
}

const mouseDown = { preventDefault: () => {} } as React.MouseEvent;

describe('useSplitPane', () => {
  it('starts at the requested split and is not dragging', () => {
    const { result } = renderHook(() => useSplitPane(40));
    expect(result.current.splitPercentage).toBe(40);
    expect(result.current.isDragging).toBe(false);
  });

  it('tracks the pointer while dragging', () => {
    const { result } = renderHook(() => useSplitPane());
    attachContainer(result.current.containerRef, 0, 1000);

    act(() => result.current.startResizing(mouseDown));
    expect(result.current.isDragging).toBe(true);

    act(() => {
      window.dispatchEvent(new MouseEvent('mousemove', { clientX: 300 }));
    });
    expect(result.current.splitPercentage).toBeCloseTo(30);
  });

  it('clamps to keep both panes usable', () => {
    const { result } = renderHook(() => useSplitPane());
    attachContainer(result.current.containerRef, 0, 1000);
    act(() => result.current.startResizing(mouseDown));

    // Past either bound the value is refused outright rather than clamped to the limit, so the
    // divider stops moving instead of snapping.
    act(() => window.dispatchEvent(new MouseEvent('mousemove', { clientX: 50 })));   // 5%
    expect(result.current.splitPercentage).toBe(50);

    act(() => window.dispatchEvent(new MouseEvent('mousemove', { clientX: 950 })));  // 95%
    expect(result.current.splitPercentage).toBe(50);

    act(() => window.dispatchEvent(new MouseEvent('mousemove', { clientX: 250 })));  // 25% — allowed
    expect(result.current.splitPercentage).toBeCloseTo(25);
  });

  it('ignores pointer movement once the drag ends', () => {
    const { result } = renderHook(() => useSplitPane());
    attachContainer(result.current.containerRef, 0, 1000);

    act(() => result.current.startResizing(mouseDown));
    act(() => window.dispatchEvent(new MouseEvent('mousemove', { clientX: 300 })));
    act(() => window.dispatchEvent(new MouseEvent('mouseup')));
    expect(result.current.isDragging).toBe(false);

    // Listeners live on window, so failing to remove them would keep resizing the pane on every
    // mouse move across the whole page.
    act(() => window.dispatchEvent(new MouseEvent('mousemove', { clientX: 700 })));
    expect(result.current.splitPercentage).toBeCloseTo(30);
  });

  it('detaches its window listeners on unmount', () => {
    const { result, unmount } = renderHook(() => useSplitPane());
    attachContainer(result.current.containerRef, 0, 1000);
    act(() => result.current.startResizing(mouseDown));
    const before = result.current.splitPercentage;

    unmount();
    window.dispatchEvent(new MouseEvent('mousemove', { clientX: 800 }));
    expect(result.current.splitPercentage).toBe(before);
  });

  it('accounts for a container that is not at the viewport origin', () => {
    const { result } = renderHook(() => useSplitPane());
    attachContainer(result.current.containerRef, 200, 800); // sidebar to the left
    act(() => result.current.startResizing(mouseDown));

    act(() => window.dispatchEvent(new MouseEvent('mousemove', { clientX: 600 })));
    // 600 - 200 = 400 of 800 = 50%, not 75% as a viewport-relative calculation would give.
    expect(result.current.splitPercentage).toBeCloseTo(50);
  });
});

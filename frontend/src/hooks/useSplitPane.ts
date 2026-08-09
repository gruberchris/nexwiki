import React, { useCallback, useEffect, useRef, useState } from 'react';

/**
 * Drag-to-resize behavior for the editor's split view.
 *
 * Extracted from Editor.tsx, where it sat among a dozen unrelated concerns. It is worth isolating
 * because it is the one piece of that component that reaches outside React entirely: the drag
 * listeners live on `window`, not on the divider, so a drag keeps tracking after the pointer
 * leaves the handle — and they must be removed when the drag ends or every subsequent mouse move
 * across the page keeps recomputing the layout.
 */

/** Bounds keeping both panes usable; below this the narrower side becomes unreadable. */
const MIN_PERCENTAGE = 20;
const MAX_PERCENTAGE = 80;

export interface UseSplitPaneResult {
  /** Width of the left pane, as a percentage of the container. */
  splitPercentage: number;
  isDragging: boolean;
  /** Attach to the element whose width the percentage is measured against. */
  containerRef: React.RefObject<HTMLDivElement | null>;
  /** Attach to the divider's onMouseDown. */
  startResizing: (e: React.MouseEvent) => void;
}

export function useSplitPane(initialPercentage = 50): UseSplitPaneResult {
  const [splitPercentage, setSplitPercentage] = useState(initialPercentage);
  const [isDragging, setIsDragging] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  const startResizing = useCallback((e: React.MouseEvent) => {
    // Without this the browser starts a text selection drag instead, and the pointer "sticks".
    e.preventDefault();
    setIsDragging(true);
  }, []);

  const stopResizing = useCallback(() => setIsDragging(false), []);

  const resize = useCallback(
    (e: MouseEvent) => {
      if (!isDragging || !containerRef.current) return;

      const containerRect = containerRef.current.getBoundingClientRect();
      const percentage = ((e.clientX - containerRect.left) / containerRect.width) * 100;

      if (percentage >= MIN_PERCENTAGE && percentage <= MAX_PERCENTAGE) {
        setSplitPercentage(percentage);
      }
    },
    [isDragging],
  );

  // Listeners go on window rather than the divider so the drag survives the pointer moving off
  // the handle — including outside the container entirely, which is normal during a fast drag.
  useEffect(() => {
    if (!isDragging) return;

    window.addEventListener('mousemove', resize);
    window.addEventListener('mouseup', stopResizing);
    return () => {
      window.removeEventListener('mousemove', resize);
      window.removeEventListener('mouseup', stopResizing);
    };
  }, [isDragging, resize, stopResizing]);

  return { splitPercentage, isDragging, containerRef, startResizing };
}

import { describe, it, expect, vi, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useWikiUpdates } from './useWikiUpdates';
import type { WikiUpdate } from '../context/SSEContext';

afterEach(() => {
  vi.clearAllMocks();
});

describe('useWikiUpdates', () => {
  it('calls onUpdate when a nexwiki-update event is dispatched', () => {
    const onUpdate = vi.fn();
    renderHook(() => useWikiUpdates(onUpdate));

    const update: WikiUpdate = {
      type: 'article-added',
      slug: 'new-article',
      title: 'New Article',
      tags: [],
      directory: 'wiki',
      total_count: 1,
      directory_count: 1,
    };

    act(() => {
      window.dispatchEvent(new CustomEvent('nexwiki-update', { detail: update }));
    });

    expect(onUpdate).toHaveBeenCalledWith(update);
  });

  it('does not call onUpdate for other event types', () => {
    const onUpdate = vi.fn();
    renderHook(() => useWikiUpdates(onUpdate));

    act(() => {
      window.dispatchEvent(new CustomEvent('other-event', { detail: {} }));
    });

    expect(onUpdate).not.toHaveBeenCalled();
  });

  it('removes event listener on unmount', () => {
    const onUpdate = vi.fn();
    const { unmount } = renderHook(() => useWikiUpdates(onUpdate));

    unmount();

    const update: WikiUpdate = {
      type: 'article-edited',
      slug: 'test',
      title: 'Test',
      tags: [],
      directory: 'wiki',
      total_count: 1,
      directory_count: 1,
    };

    act(() => {
      window.dispatchEvent(new CustomEvent('nexwiki-update', { detail: update }));
    });

    expect(onUpdate).not.toHaveBeenCalled();
  });
});

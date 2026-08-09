import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useArticleActions } from './useArticleActions';
import type { Article } from '../types';

const article: Article = {
  title: 'Bleve Decision', slug: 'bleve-decision', created_at: '', timestamp: '',
  version: 1, content: '# Why Bleve\n\nZero dependencies.',
};

let writeText: ReturnType<typeof vi.fn>;

beforeEach(() => {
  writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const setup = (overrides: Partial<Parameters<typeof useArticleActions>[0]> = {}) => {
  const onAlert = vi.fn();
  const onArticlesImported = vi.fn().mockResolvedValue(undefined);
  const hook = renderHook(() =>
    useArticleActions({ currentArticle: article, onAlert, onArticlesImported, ...overrides }));
  return { hook, onAlert, onArticlesImported };
};

describe('copy actions', () => {
  it('copies the article body and flashes a confirmation that resets', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { hook, onAlert } = setup();

    await act(async () => { await hook.result.current.copyMarkdown(); });

    expect(writeText).toHaveBeenCalledWith(article.content);
    expect(hook.result.current.copiedMd).toBe(true);
    expect(onAlert).toHaveBeenCalledWith('success', expect.stringContaining('copied'));

    // The confirmation must clear itself, or the button reads "Copied!" forever.
    await act(async () => { vi.advanceTimersByTime(2000); });
    expect(hook.result.current.copiedMd).toBe(false);
    vi.useRealTimers();
  });

  it('copies the current URL, independent of any article', async () => {
    const { hook } = setup({ currentArticle: null });
    await act(async () => { await hook.result.current.copyShareLink(); });
    expect(writeText).toHaveBeenCalledWith(window.location.href);
    expect(hook.result.current.copiedUrl).toBe(true);
  });

  it('does nothing when there is no article to copy', async () => {
    const { hook, onAlert } = setup({ currentArticle: null });
    await act(async () => { await hook.result.current.copyMarkdown(); });
    expect(writeText).not.toHaveBeenCalled();
    expect(onAlert).not.toHaveBeenCalled();
  });

  it('reports a clipboard failure instead of flashing success', async () => {
    writeText.mockRejectedValue(new Error('denied'));
    const { hook, onAlert } = setup();
    await act(async () => { await hook.result.current.copyMarkdown(); });
    expect(hook.result.current.copiedMd).toBe(false);
    expect(onAlert).toHaveBeenCalledWith('error', expect.stringContaining('Failed'));
  });
});

describe('exports close the share dropdown', () => {
  // Every export runs from inside the dropdown; leaving it open over the print dialog or a
  // file picker is the bug this pins.
  it.each([
    ['exportPDF'], ['exportDocx'], ['exportMarkdown'],
  ] as const)('%s closes it', async (action) => {
    vi.stubGlobal('print', vi.fn());
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, blob: async () => new Blob() }));
    const { hook } = setup();

    act(() => hook.result.current.setShareDropdownOpen(true));
    expect(hook.result.current.shareDropdownOpen).toBe(true);

    await act(async () => { await hook.result.current[action](); });
    expect(hook.result.current.shareDropdownOpen).toBe(false);
  });
});

describe('backup and restore', () => {
  it('reports a failed backup rather than downloading nothing', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }));
    const { hook, onAlert } = setup();
    await act(async () => { await hook.result.current.exportAll(); });
    expect(onAlert).toHaveBeenCalledWith('error', 'Failed to create backup.');
  });

  it('refreshes the article list after a successful restore', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ imported: 3, skipped: 0, missing_type: [], warnings: [] }),
    }));
    const { hook, onAlert, onArticlesImported } = setup();

    const input = document.createElement('input');
    Object.defineProperty(input, 'files', { value: [new File(['x'], 'backup.zip')] });

    await act(async () => {
      await hook.result.current.handleImportFileChange({
        target: input,
      } as unknown as React.ChangeEvent<HTMLInputElement>);
    });

    expect(onArticlesImported).toHaveBeenCalled();
    expect(onAlert).toHaveBeenCalledWith('success', expect.stringContaining('Restored 3 articles'));
    // Clearing the value lets the same file be re-picked and still fire a change event.
    expect(input.value).toBe('');
  });

  it('surfaces a rejected restore', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }));
    const { hook, onAlert, onArticlesImported } = setup();

    const input = document.createElement('input');
    Object.defineProperty(input, 'files', { value: [new File(['x'], 'bad.zip')] });
    await act(async () => {
      await hook.result.current.handleImportFileChange({
        target: input,
      } as unknown as React.ChangeEvent<HTMLInputElement>);
    });

    expect(onArticlesImported).not.toHaveBeenCalled();
    expect(onAlert).toHaveBeenCalledWith('error', expect.stringContaining('Restore failed'));
  });
});

describe('share dropdown', () => {
  it('closes on an outside click', async () => {
    const { hook } = setup();
    act(() => hook.result.current.setShareDropdownOpen(true));

    await act(async () => {
      document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    });
    await waitFor(() => expect(hook.result.current.shareDropdownOpen).toBe(false));
  });
});

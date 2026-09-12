import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useArticleActions } from './useArticleActions';
import type { Article } from '../types';

const article: Article = {
  title: 'Bleve Decision', slug: 'bleve-decision', created_at: '', timestamp: '',
  version: 1, content: '# Why Bleve\n\nZero dependencies.',
};

let writeText: ReturnType<typeof vi.fn>;
let writeClipboard: ReturnType<typeof vi.fn>;

class MockClipboardItem {
  data: Record<string, Blob>;
  constructor(data: Record<string, Blob>) {
    this.data = data;
  }
}

beforeEach(() => {
  writeText = vi.fn().mockResolvedValue(undefined);
  writeClipboard = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText, write: writeClipboard },
    configurable: true,
  });
  vi.stubGlobal('ClipboardItem', MockClipboardItem);
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

  it('copies rich text with formatting, flashes confirmation, and resets', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const contentEl = document.createElement('div');
    contentEl.className = 'wiki-content';
    contentEl.innerHTML = '<h1>Why Bleve</h1><p>Zero dependencies.</p>';
    document.body.appendChild(contentEl);

    const { hook, onAlert } = setup();

    await act(async () => { await hook.result.current.copyRichText(); });

    expect(writeClipboard).toHaveBeenCalledTimes(1);
    const [items] = writeClipboard.mock.calls[0];
    expect(items).toHaveLength(1);
    const item = items[0] as MockClipboardItem;
    expect(item.data['text/html']).toBeInstanceOf(Blob);
    expect(item.data['text/plain']).toBeInstanceOf(Blob);
    expect(item.data['text/html'].type).toBe('text/html');
    expect(item.data['text/plain'].type).toBe('text/plain');
    await expect(item.data['text/html'].text()).resolves.toBe('<h1>Why Bleve</h1><p>Zero dependencies.</p>');
    await expect(item.data['text/plain'].text()).resolves.toBe(contentEl.innerText || contentEl.textContent || '');

    expect(hook.result.current.copiedRichText).toBe(true);
    expect(onAlert).toHaveBeenCalledWith(
      'success',
      'Rich text copied to clipboard! Ready to paste into Teams, Word, or Outlook.',
    );

    await act(async () => { vi.advanceTimersByTime(2000); });
    expect(hook.result.current.copiedRichText).toBe(false);

    document.body.removeChild(contentEl);
    vi.useRealTimers();
  });

  it('falls back to article content if .wiki-content element is absent when copying rich text', async () => {
    const { hook, onAlert } = setup();

    await act(async () => { await hook.result.current.copyRichText(); });

    expect(writeClipboard).toHaveBeenCalledTimes(1);
    const [items] = writeClipboard.mock.calls[0];
    const item = items[0] as MockClipboardItem;
    await expect(item.data['text/html'].text()).resolves.toBe(article.content);
    await expect(item.data['text/plain'].text()).resolves.toBe(article.content);
    expect(hook.result.current.copiedRichText).toBe(true);
    expect(onAlert).toHaveBeenCalledWith('success', expect.stringContaining('Rich text copied'));
  });

  it('closes the share dropdown when copying rich text', async () => {
    const { hook } = setup();
    act(() => hook.result.current.setShareDropdownOpen(true));
    expect(hook.result.current.shareDropdownOpen).toBe(true);

    await act(async () => { await hook.result.current.copyRichText(); });
    expect(hook.result.current.shareDropdownOpen).toBe(false);
  });

  it('does nothing when copying rich text without an article', async () => {
    const { hook, onAlert } = setup({ currentArticle: null });
    await act(async () => { await hook.result.current.copyRichText(); });
    expect(writeClipboard).not.toHaveBeenCalled();
    expect(onAlert).not.toHaveBeenCalled();
  });

  it('handles clipboard errors when copying rich text and alerts error', async () => {
    writeClipboard.mockRejectedValue(new Error('clipboard error'));
    const { hook, onAlert } = setup();

    await act(async () => { await hook.result.current.copyRichText(); });

    expect(hook.result.current.copiedRichText).toBe(false);
    expect(onAlert).toHaveBeenCalledWith('error', 'Failed to copy formatted rich text.');
  });

  it('uses document.execCommand fallback when ClipboardItem is undefined', async () => {
    vi.stubGlobal('ClipboardItem', undefined);
    const execCommand = vi.fn().mockImplementation(() => {
      const event = new Event('copy', { bubbles: true, cancelable: true }) as ClipboardEvent;
      const dataStore: Record<string, string> = {};
      Object.defineProperty(event, 'clipboardData', {
        value: {
          setData: (format: string, data: string) => { dataStore[format] = data; },
        },
      });
      document.dispatchEvent(event);
      expect(dataStore['text/html']).toBe(article.content);
      expect(dataStore['text/plain']).toBe(article.content);
      return true;
    });
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (document as any).execCommand = execCommand;

    const { hook, onAlert } = setup();
    await act(async () => { await hook.result.current.copyRichText(); });

    expect(execCommand).toHaveBeenCalledWith('copy');
    expect(hook.result.current.copiedRichText).toBe(true);
    expect(onAlert).toHaveBeenCalledWith('success', expect.stringContaining('Rich text copied'));
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

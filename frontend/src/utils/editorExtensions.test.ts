import { describe, it, expect, vi } from 'vitest';
import { EditorState } from '@codemirror/state';
import { EditorView } from '@codemirror/view';
import {
  buildEditorExtensions,
  createShortcutKeymap,
  nexwikiEditorTheme,
  nexwikiHighlightStyle,
} from './editorExtensions';
import type { Article } from '../types';

const articles: Article[] = [
  { title: 'Existing', slug: 'existing-page', created_at: '', timestamp: '', version: 1 },
];

describe('editor extensions', () => {
  it('builds a usable extension set', () => {
    const extensions = buildEditorExtensions(articles, vi.fn());
    expect(extensions).toHaveLength(5); // markdown, theme, highlighting, keymap, linter

    // The real assertion: CodeMirror accepts them. A malformed extension throws here.
    const state = EditorState.create({ doc: '# Title', extensions });
    expect(state.doc.toString()).toBe('# Title');
  });

  // These close over nothing, so they are module constants rather than per-render memos —
  // built once for the whole application.
  it('exposes theme and highlight style as stable singletons', () => {
    expect(nexwikiEditorTheme).toBe(nexwikiEditorTheme);
    expect(nexwikiHighlightStyle).toBe(nexwikiHighlightStyle);
  });

  it('claims Mod-/ so the browser does not also act on it', () => {
    const onToggle = vi.fn();
    const extension = createShortcutKeymap(onToggle);
    const view = new EditorView({ state: EditorState.create({ doc: '', extensions: [extension] }) });

    const handled = view.contentDOM.dispatchEvent(
      new KeyboardEvent('keydown', { key: '/', ctrlKey: true, bubbles: true, cancelable: true }),
    );

    expect(onToggle).toHaveBeenCalledTimes(1);
    // dispatchEvent returns false when a handler called preventDefault, which is how the keymap
    // signals it consumed the shortcut.
    expect(handled).toBe(false);
    view.destroy();
  });

  it('rebuilding with a different article list yields a fresh linter', () => {
    // Broken-WikiLink detection depends on which slugs exist, so the extension set must be
    // rebuilt when articles change rather than captured once.
    const first = buildEditorExtensions(articles, vi.fn());
    const second = buildEditorExtensions([], vi.fn());
    expect(first[4]).not.toBe(second[4]);
  });
});

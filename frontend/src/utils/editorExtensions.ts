import { EditorView, keymap } from '@codemirror/view';
import { linter } from '@codemirror/lint';
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language';
import { markdown } from '@codemirror/lang-markdown';
import { tags as t } from '@lezer/highlight';
import type { Extension } from '@codemirror/state';
import { lintMarkdown } from './markdownLinter';
import type { Article } from '../types';

/**
 * CodeMirror configuration for the article editor: theme, syntax highlighting, keymap, and the
 * Markdown linter bridge.
 *
 * Extracted from Editor.tsx, where roughly a hundred lines of editor configuration sat in the
 * middle of the component's own state and handlers. Two of these are genuine module constants
 * rather than memos: they close over nothing, so building them once for the whole application is
 * both cheaper and clearer than a `useMemo(..., [])` that re-runs a closure on every render.
 *
 * Everything here is driven by CSS custom properties rather than fixed colours, which is what lets
 * the editor follow the active NexWiki theme — including custom palettes — without rebuilding the
 * extension when the user switches light and dark.
 */

// Adaptive editor chrome. Values reference the same CSS variables useTheme writes to :root.
export const nexwikiEditorTheme = EditorView.theme({
    "&": {
      color: "var(--text-secondary)",
      backgroundColor: "var(--bg-secondary)",
      fontSize: "14px",
      height: "100%",
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace",
    },
    ".cm-scroller": { overflow: "auto" },
    ".cm-content": {
      caretColor: "var(--accent-primary)",
      padding: "24px 0",
    },
    ".cm-cursor": {
      borderLeftColor: "var(--accent-primary)",
    },
    "&.cm-focused .cm-cursor": {
      borderLeftColor: "var(--accent-primary)",
    },
    ".cm-selectionBackground, ::selection": {
      backgroundColor: "color-mix(in srgb, var(--accent-primary) 20%, transparent) !important",
    },
    "&.cm-focused .cm-selectionBackground": {
      backgroundColor: "color-mix(in srgb, var(--accent-primary) 30%, transparent) !important",
    },
    ".cm-gutters": {
      backgroundColor: "var(--bg-primary)",
      color: "var(--text-muted)",
      borderRight: "1px solid var(--border-color)",
      paddingTop: "24px",
    },
    ".cm-activeLine": {
      backgroundColor: "color-mix(in srgb, var(--border-color) 15%, transparent)",
    },
    ".cm-activeLineGutter": {
      backgroundColor: "color-mix(in srgb, var(--border-color) 30%, transparent)",
    },
  });

// Markdown token colours, likewise CSS-variable driven so they flip with light/dark automatically.
// (@uiw's default light HighlightStyle is disabled via basicSetup.syntaxHighlighting=false.)
export const nexwikiHighlightStyle = HighlightStyle.define([
    { tag: t.heading, color: "var(--accent-primary)", fontWeight: "bold" },
    { tag: t.strong, color: "var(--text-primary)", fontWeight: "bold" },
    { tag: t.emphasis, color: "var(--text-secondary)", fontStyle: "italic" },
    { tag: [t.link, t.url], color: "var(--accent-secondary)", textDecoration: "underline" },
    { tag: t.monospace, color: "var(--accent-primary)" },
    { tag: t.list, color: "var(--text-secondary)" },
    { tag: t.quote, color: "var(--text-muted)" },
    { tag: [t.meta, t.processingInstruction, t.contentSeparator], color: "var(--text-muted)" },
  ]);

/**
 * Bridges NexWiki's Markdown linter into CodeMirror's lint layer, offering an applicable action
 * only for diagnostics that carry a `fix` — replacement text that is correct to insert verbatim.
 *
 * A diagnostic with only a `hint` gets no action. The two are separate fields precisely because
 * this apply callback cannot tell prose from Markdown: when they shared one `suggestion` field,
 * applying a broken WikiLink's "fix" pasted the sentence "Click to create this page." over
 * `[[Foo]]`. The hint still reaches the user through the diagnostics panel.
 *
 * Depends on the article list because broken-link detection needs to know which slugs exist.
 */
export function createMarkdownLinter(articles: Article[]): Extension {
  return linter((view) => {
    const diagnostics = lintMarkdown(view.state.doc.toString(), articles);
    return diagnostics.map((d) => ({
      from: d.from,
      to: d.to,
      severity: d.severity,
      message: d.hint ? `${d.message} ${d.hint}` : d.message,
      actions: d.fix
        ? [{
            name: `Fix: ${d.fix}`,
            apply: (view: EditorView, from: number, to: number) => {
              view.dispatch({ changes: { from, to, insert: d.fix! } });
            },
          }]
        : [],
    }));
  });
}

/** Ctrl+/ or Cmd+/ toggles the Markdown syntax reference. */
export function createShortcutKeymap(onToggleSyntaxHelp: () => void): Extension {
  return keymap.of([
    {
      key: 'Mod-/',
      run: () => {
        onToggleSyntaxHelp();
        return true; // claim the key so the browser does not also act on it
      },
    },
  ]);
}

/**
 * Assembles the full extension list. Grouping the composition here means a caller adds an
 * extension in one place rather than threading a new memo through the component.
 */
export function buildEditorExtensions(articles: Article[], onToggleSyntaxHelp: () => void): Extension[] {
  return [
    markdown(),
    nexwikiEditorTheme,
    syntaxHighlighting(nexwikiHighlightStyle),
    createShortcutKeymap(onToggleSyntaxHelp),
    createMarkdownLinter(articles),
  ];
}

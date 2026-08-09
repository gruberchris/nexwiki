import React, { useCallback, useEffect, useRef, useState } from 'react';
import type { Article } from '../types';
import { Slugify, saveFile, generateDocxContent } from '../utils';

/**
 * Every "do something with this article" action that is not an edit: copying to the clipboard,
 * exporting to PDF/Word/Markdown, and backing up or restoring the whole wiki as an OKF bundle.
 *
 * Pulled out of App.tsx, where nine handlers and the four transient flags they toggle sat inline.
 * They belong together because they share one pattern — perform a side effect, flash a "copied"
 * confirmation for two seconds, then reset — and because every one of them must close the share
 * dropdown, which is easy to forget when adding a tenth.
 */

/** How long a "Copied!" confirmation stays lit. */
const COPY_FEEDBACK_MS = 2000;

export interface UseArticleActionsOptions {
  /** The article in view; copy/export actions no-op without one. */
  currentArticle: Article | null;
  onAlert: (type: 'success' | 'error', text: string) => void;
  /** Called after a successful restore, so the caller can refresh its article list. */
  onArticlesImported: () => Promise<void> | void;
}

export interface UseArticleActionsResult {
  shareDropdownOpen: boolean;
  setShareDropdownOpen: (open: boolean) => void;
  copiedMd: boolean;
  copiedUrl: boolean;
  copiedTitle: boolean;
  copyMarkdown: () => Promise<void>;
  copyShareLink: () => Promise<void>;
  copyTitle: () => Promise<void>;
  exportPDF: () => void;
  exportDocx: () => Promise<void>;
  exportMarkdown: () => Promise<void>;
  exportAll: () => Promise<void>;
  /** Attach to a hidden <input type="file"> so importBundle can open the picker. */
  importFileRef: React.RefObject<HTMLInputElement | null>;
  triggerImport: () => void;
  handleImportFileChange: (e: React.ChangeEvent<HTMLInputElement>) => Promise<void>;
}

/**
 * Copies text, falling back to a hidden textarea for browsers (and insecure origins) where the
 * async Clipboard API is unavailable.
 */
async function copyText(text: string): Promise<void> {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.style.position = 'fixed';
  document.body.appendChild(textarea);
  textarea.select();
  const cmd = 'execCommand';
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (document as any)[cmd]('copy');
  document.body.removeChild(textarea);
}

export function useArticleActions({
  currentArticle,
  onAlert,
  onArticlesImported,
}: UseArticleActionsOptions): UseArticleActionsResult {
  const [shareDropdownOpen, setShareDropdownOpen] = useState(false);
  const [copiedMd, setCopiedMd] = useState(false);
  const [copiedUrl, setCopiedUrl] = useState(false);
  const [copiedTitle, setCopiedTitle] = useState(false);
  const importFileRef = useRef<HTMLInputElement>(null);

  // Close the share dropdown on an outside click.
  useEffect(() => {
    if (!shareDropdownOpen) return;
    const handleOutsideClick = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (!target.closest('.share-dropdown-container')) {
        setShareDropdownOpen(false);
      }
    };
    document.addEventListener('mousedown', handleOutsideClick);
    return () => document.removeEventListener('mousedown', handleOutsideClick);
  }, [shareDropdownOpen]);

  /** Runs a copy, flashes its confirmation flag, and reports success or failure. */
  const runCopy = useCallback(
    async (
      text: string,
      setFlag: (v: boolean) => void,
      successMsg: string,
      failureMsg: string,
    ) => {
      try {
        await copyText(text);
        setFlag(true);
        onAlert('success', successMsg);
        setTimeout(() => setFlag(false), COPY_FEEDBACK_MS);
      } catch (err) {
        console.error(failureMsg, err);
        onAlert('error', failureMsg);
      }
    },
    [onAlert],
  );

  const copyMarkdown = useCallback(async () => {
    if (!currentArticle) return;
    await runCopy(currentArticle.content || '', setCopiedMd,
      'Article Markdown copied to clipboard!', 'Failed to copy Markdown content.');
  }, [currentArticle, runCopy]);

  const copyShareLink = useCallback(async () => {
    await runCopy(window.location.href, setCopiedUrl,
      'Share link copied to clipboard!', 'Failed to copy share link.');
  }, [runCopy]);

  const copyTitle = useCallback(async () => {
    if (!currentArticle) return;
    await runCopy(currentArticle.title, setCopiedTitle,
      'Article title copied to clipboard!', 'Failed to copy article title.');
  }, [currentArticle, runCopy]);

  const exportPDF = useCallback(() => {
    setShareDropdownOpen(false);
    window.print();
  }, []);

  const exportDocx = useCallback(async () => {
    if (!currentArticle) return;
    setShareDropdownOpen(false);
    try {
      const viewerEl = document.querySelector('.wiki-content');
      const bodyHtml = viewerEl ? viewerEl.innerHTML : '';
      const docxContent = generateDocxContent(currentArticle.title, bodyHtml);
      const suggestedName = Slugify(currentArticle.title) || 'article';
      if (await saveFile(docxContent, suggestedName, 'application/msword', 'docx')) {
        onAlert('success', 'Article exported as Word successfully!');
      }
    } catch (err) {
      console.error('Failed to export DOCX:', err);
      onAlert('error', 'Failed to export as Word document.');
    }
  }, [currentArticle, onAlert]);

  const exportMarkdown = useCallback(async () => {
    if (!currentArticle) return;
    setShareDropdownOpen(false);
    try {
      const suggestedName = Slugify(currentArticle.title) || 'article';
      if (await saveFile(currentArticle.content || '', suggestedName, 'text/markdown', 'md')) {
        onAlert('success', 'Article exported as Markdown successfully!');
      }
    } catch (err) {
      console.error('Failed to export Markdown:', err);
      onAlert('error', 'Failed to export as Markdown file.');
    }
  }, [currentArticle, onAlert]);

  const exportAll = useCallback(async () => {
    try {
      onAlert('success', 'Preparing backup… download will start shortly.');
      const response = await fetch('/api/okf/export');
      if (!response.ok) {
        onAlert('error', 'Failed to create backup.');
        return;
      }
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `nexwiki-backup-${new Date().toISOString().split('T')[0]}.zip`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    } catch {
      onAlert('error', 'Failed to create backup.');
    }
  }, [onAlert]);

  const triggerImport = useCallback(() => importFileRef.current?.click(), []);

  const handleImportFileChange = useCallback(
    async (e: React.ChangeEvent<HTMLInputElement>) => {
      const file = e.target.files?.[0];
      if (!file) return;
      // Reset immediately so re-picking the same file still fires a change event.
      e.target.value = '';
      try {
        onAlert('success', 'Restoring from backup…');
        const form = new FormData();
        form.append('file', file);
        const response = await fetch('/api/okf/import', { method: 'POST', body: form });
        if (!response.ok) {
          onAlert('error', 'Restore failed. Ensure the file is a valid NexWiki backup (.zip).');
          return;
        }
        const report = (await response.json()) as {
          imported: number; skipped: number; missing_type: string[]; warnings: string[];
        };
        await onArticlesImported();
        if (report.warnings.length > 0) console.warn('Import warnings:', report.warnings);
        const warn = report.warnings.length > 0
          ? ` (${report.warnings.length} warning${report.warnings.length > 1 ? 's' : ''} — see console)`
          : '';
        onAlert('success',
          `Restored ${report.imported} article${report.imported !== 1 ? 's' : ''} from backup.${warn}`);
      } catch {
        onAlert('error', 'Restore failed. Ensure the file is a valid NexWiki backup (.zip).');
      }
    },
    [onAlert, onArticlesImported],
  );

  return {
    shareDropdownOpen, setShareDropdownOpen,
    copiedMd, copiedUrl, copiedTitle,
    copyMarkdown, copyShareLink, copyTitle,
    exportPDF, exportDocx, exportMarkdown, exportAll,
    importFileRef, triggerImport, handleImportFileChange,
  };
}

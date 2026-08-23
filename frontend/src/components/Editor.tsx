import React, { useState, useRef, useEffect, useMemo } from 'react';
import { 
  Save, 
  X, 
  Bold, 
  Italic, 
  Code, 
  Heading1, 
  Heading2, 
  Heading3, 
  Link, 
  Image as ImageIcon, 
  Eye, 
  Edit3, 
  Columns,
  Sparkles,
  Link2,
  Tag,
  Wrench,
  ClipboardList,
  BookOpen,
  Info,
  AlertCircle,
  Copy,
  Check
} from 'lucide-react';
import CodeMirror from '@uiw/react-codemirror';
import type { ReactCodeMirrorRef } from '@uiw/react-codemirror';
import { EditorView } from '@codemirror/view';

import { Slugify } from '../utils';
import { Viewer } from './Viewer';
import { lintMarkdown } from '../utils/markdownLinter';
import type { LintDiagnostic } from '../utils/markdownLinter';
import { MarkdownSyntaxModal } from './MarkdownSyntaxModal';
import { MarkdownLintErrorModal } from './MarkdownLintErrorModal';
import type { Article, ContentType } from '../types';
import { statusBadgeClass, statusOptionsFor } from '../statusTags';
import { ContentTypes } from '../types';
import { useSplitPane } from '../hooks/useSplitPane';
import { useTagEditor } from '../hooks/useTagEditor';
import { buildEditorExtensions } from '../utils/editorExtensions';

interface EditorProps {
  initialTitle: string;
  initialContent: string;
  initialTags?: string[];
  initialStatus?: string;
  initialDescription?: string;
  initialSource?: string;
  initialResource?: string;
  articleType?: ContentType;
  slug: string; // empty if new page
  onSave: (title: string, content: string, editSummary: string, tags: string[], description: string, source: string, resource: string, status: string) => Promise<void>;
  onCancel: () => void;
  articles: Article[];
  version?: number;
}

export const Editor: React.FC<EditorProps> = ({
  initialTitle,
  initialContent,
  initialTags,
  initialStatus,
  initialDescription,
  initialSource,
  initialResource,
  articleType,
  slug,
  onSave,
  onCancel,
  articles,
  version
}) => {
  const [title, setTitle] = useState(initialTitle);
  const [content, setContent] = useState(initialContent);
  const [description, setDescription] = useState(initialDescription || '');
  const [source, setSource] = useState(initialSource || '');
  const [resource, setResource] = useState(initialResource || '');
  const [viewMode, setViewMode] = useState<'split' | 'edit' | 'preview'>('split');
  const [isSaving, setIsSaving] = useState(false);
  const [isUploading, setIsUploading] = useState(false);
  const [errorMsg, setErrorMsg] = useState('');
  const [editSummary, setEditSummary] = useState('');

  // Modals state
  const [syntaxModalOpen, setSyntaxModalOpen] = useState(false);
  const [lintModalOpen, setLintModalOpen] = useState(false);
  const [copiedMarkdown, setCopiedMarkdown] = useState(false);

  const handleCopyMarkdown = async () => {
    await navigator.clipboard.writeText(content);
    setCopiedMarkdown(true);
    setTimeout(() => setCopiedMarkdown(false), 2000);
  };

  // Right-click context menu state
  interface ContextMenuState {
    x: number;
    y: number;
    diagnostic: LintDiagnostic;
  }
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null);

  // Document class is carried by the OKF type (tool-assigned), shown as a read-only mode badge.
  const resolvedType: ContentType = articleType || ContentTypes.Wiki;
  const isSkill = resolvedType === ContentTypes.Skill;
  const isPlan = resolvedType === ContentTypes.Plan;

  const editorRef = useRef<ReactCodeMirrorRef>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // Close context menu on window clicks
  useEffect(() => {
    const closeMenu = () => setContextMenu(null);
    window.addEventListener('click', closeMenu);
    return () => window.removeEventListener('click', closeMenu);
  }, []);

  // Drag-to-resize split view; the window-level drag listeners live in the hook.
  const { splitPercentage, isDragging, containerRef, startResizing } = useSplitPane();

  // Compute diagnostics reactively on change
  const diagnostics = useMemo(() => {
    return lintMarkdown(content, articles);
  }, [content, articles]);

  // Tag set, autocomplete, and the reserved-prefix rule all live in useTagEditor.
  const {
    tags, tagInput, suggestions: tagSuggestions, focusedIndex,
    handleInputChange: handleTagInputChange, handleKeyDown: handleTagKeyDown,
    selectSuggestion: selectTagSuggestion, removeTag,
  } = useTagEditor(initialTags || [], articles);

  // Lifecycle status is a field, not a tag, and only plans and skills have one. Wiki articles and
  // memories are not offered the control at all — they describe themselves with free tags.
  const [status, setStatus] = useState(initialStatus || '');
  const statusOptions = statusOptionsFor(resolvedType);

  const errorCount = useMemo(() => diagnostics.filter(d => d.severity === 'error').length, [diagnostics]);
  const warningCount = useMemo(() => diagnostics.filter(d => d.severity === 'warning').length, [diagnostics]);

  // Helper to insert Markdown tags at cursor selection
  const insertMarkdown = (before: string, after: string = '', defaultText: string = '') => {
    const view = editorRef.current?.view;
    if (!view) return;

    const selection = view.state.selection.main;
    const selectedText = view.state.sliceDoc(selection.from, selection.to) || defaultText;
    const inserted = before + selectedText + after;

    view.dispatch({
      changes: { from: selection.from, to: selection.to, insert: inserted },
      selection: {
        anchor: selection.from + before.length,
        head: selection.from + before.length + selectedText.length
      }
    });
    view.focus();
  };

  // Theme, highlighting, keymap, and the linter bridge all live in utils/editorExtensions.
  const extensions = useMemo(
    () => buildEditorExtensions(articles, () => setSyntaxModalOpen((prev) => !prev)),
    [articles],
  );

  // Handle Image uploads
  const handleImageUpload = async (file: File) => {
    if (!file) return;

    const targetSlug = slug || Slugify(title) || 'draft-page';

    setIsUploading(true);
    setErrorMsg('');

    const formData = new FormData();
    formData.append('file', file);

    try {
      const response = await fetch(`/api/articles/${targetSlug}/assets`, {
        method: 'POST',
        body: formData,
      });

      if (!response.ok) {
        const errorData = await response.json();
        setErrorMsg(errorData.error || 'Failed to upload image');
        setIsUploading(false);
        return;
      }

      const data = await response.json();
      insertMarkdown(`![${file.name.split('.')[0]}](${data.url})`, '', '');
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Image upload failed. Is it a valid image file?';
      setErrorMsg(msg);
    } finally {
      setIsUploading(false);
    }
  };

  const onFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files.length > 0) {
      void handleImageUpload(e.target.files[0]);
    }
  };

  // Drag and drop image uploads
  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      const file = e.dataTransfer.files[0];
      if (file.type.startsWith('image/')) {
        void handleImageUpload(file);
      }
    }
  };

  const handleDragOver = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
  };

  // Custom context menu right-click detection
  const handleContextMenu = (e: React.MouseEvent<HTMLDivElement>) => {
    const view = editorRef.current?.view;
    if (!view) return;

    // Resolve click coords to doc position
    const pos = view.posAtCoords({ x: e.clientX, y: e.clientY });
    if (pos === null) return;

    // Search diagnostics for matches covering these cursor pos
    const activeDiag = diagnostics.find(d => pos >= d.from && pos <= d.to);
    if (activeDiag) {
      e.preventDefault();
      setContextMenu({
        x: e.clientX,
        y: e.clientY,
        diagnostic: activeDiag
      });
    } else {
      setContextMenu(null);
    }
  };

  // Selection Jump callback from Linter Modal
  const handleSelectDiagnostic = (diag: LintDiagnostic) => {
    const view = editorRef.current?.view;
    if (view) {
      view.focus();
      view.dispatch({
        selection: { anchor: diag.from, head: diag.to },
        effects: [
          EditorView.scrollIntoView(diag.from, { y: 'center' })
        ]
      });
    }
  };

  // Form submit saving
  const handleSave = async (e: React.SyntheticEvent) => {
    e.preventDefault();
    if (!title.trim()) {
      setErrorMsg('Article Title is required.');
      return;
    }

    setIsSaving(true);
    setErrorMsg('');

    try {
      await onSave(title.trim(), content, editSummary, tags, description.trim(), source.trim(), resource.trim(), status);
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed to save article.';
      setErrorMsg(msg);
      setIsSaving(false);
    }
  };

  const liveSlug = Slugify(title);

  return (
    <div className="flex-1 h-screen flex flex-col bg-slate-50 dark:bg-slate-950/40 min-w-0">
      <form onSubmit={handleSave} className="flex-1 flex flex-col h-full overflow-hidden">
        
        {/* Editor Top Control Bar */}
        <div className="p-4 border-b border-slate-200/80 dark:border-slate-800/80 bg-white/80 dark:bg-slate-900/80 backdrop-blur-md flex items-center justify-between gap-4 select-none relative z-30">
          <div className="flex-1">
            <input
              type="text"
              placeholder="Article Title..."
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              className="w-full text-xl font-bold bg-transparent border-none outline-hidden text-slate-950 dark:text-white placeholder:text-slate-400"
              required
              disabled={isSaving}
            />
            <div className="flex flex-col sm:flex-row gap-1.5 mt-1">
              <input
                type="text"
                placeholder="One-line description (shown in indexes)..."
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                className="flex-1 text-xs bg-transparent border-none outline-hidden text-slate-600 dark:text-slate-300 placeholder:text-slate-400/70"
                disabled={isSaving}
              />
              <input
                type="text"
                placeholder="Source (where the knowledge came from)..."
                value={source}
                onChange={(e) => setSource(e.target.value)}
                className="flex-1 text-xs bg-transparent border-none outline-hidden text-slate-600 dark:text-slate-300 placeholder:text-slate-400/70"
                disabled={isSaving}
              />
              <input
                type="text"
                placeholder="Resource (canonical URI of the concept)..."
                value={resource}
                onChange={(e) => setResource(e.target.value)}
                className="flex-1 text-xs bg-transparent border-none outline-hidden text-slate-600 dark:text-slate-300 placeholder:text-slate-400/70"
                disabled={isSaving}
              />
            </div>
            {title.trim() && (
              <div className="flex flex-col gap-1 mt-1 text-[10px] font-semibold text-slate-400 dark:text-slate-500">
                <div className="flex items-center gap-1.5">
                  <Sparkles size={10} className="text-indigo-400 dark:text-emerald-400 animate-pulse-subtle" />
                  <span>Clean Slug Routing:</span>
                  <span className="font-mono bg-slate-100 dark:bg-slate-900 px-1 rounded text-indigo-500 dark:text-indigo-400">
                    /articles/{liveSlug || '...'}
                  </span>
                  {slug && slug !== liveSlug && (
                    <span className="text-amber-500 font-medium">
                      (Renaming will update all routes & assets)
                    </span>
                  )}
                </div>

                {/* Visual Tag Manager */}
                <div className="flex flex-wrap items-center gap-1.5 mt-2 select-none">
                  {isSkill && (
                    <div className="inline-flex items-center gap-1.5 text-[10px] font-bold text-indigo-650 dark:text-indigo-400 bg-indigo-500/10 dark:bg-indigo-950/20 border border-indigo-550/25 dark:border-indigo-900/50 px-2.5 py-0.5 rounded-full mr-2">
                      <Wrench size={10} className="animate-pulse text-indigo-500" />
                      <span>Custom AI Skill Mode{version ? ` (V${version})` : ''}</span>
                    </div>
                  )}
                  {isPlan && (
                    <div className="inline-flex items-center gap-1.5 text-[10px] font-bold text-emerald-600 dark:text-emerald-400 bg-emerald-500/10 dark:bg-emerald-950/20 border border-emerald-550/25 dark:border-emerald-900/50 px-2.5 py-0.5 rounded-full mr-2">
                      <ClipboardList size={10} className="animate-pulse text-emerald-505" />
                      <span>Collaborative AI Plan Mode{version ? ` (V${version})` : ''}</span>
                    </div>
                  )}
                  {!isSkill && !isPlan && (
                    <div className="inline-flex items-center gap-1.5 text-[10px] font-bold text-slate-650 dark:text-slate-400 bg-slate-500/10 dark:bg-slate-950/20 border border-slate-550/25 dark:border-slate-900/50 px-2.5 py-0.5 rounded-full mr-2">
                      <BookOpen size={10} className="text-slate-400" />
                      <span>Wiki Article Mode{version ? ` (V${version})` : ''}</span>
                    </div>
                  )}

                  {statusOptions && (
                    <>
                      <span className="inline-flex items-center gap-0.5 text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-widest mr-1">
                        Status:
                      </span>
                      <select
                        aria-label="Lifecycle status"
                        value={status}
                        onChange={(e) => setStatus(e.target.value)}
                        className={`text-[10px] font-semibold px-2 py-0.5 rounded-full mr-2 border cursor-pointer ${statusBadgeClass(status)}`}
                      >
                        {/* A plan always has one; a skill may have none. */}
                        {!isPlan && <option value="">(none)</option>}
                        {statusOptions.map((opt) => (
                          <option key={opt} value={opt}>{opt}</option>
                        ))}
                      </select>
                    </>
                  )}

                  <span className="inline-flex items-center gap-0.5 text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-widest mr-1">
                    <Tag size={9} />
                    Tags:
                  </span>
                  {tags.map(tag => {
                    // Tool-managed memory-scope tags are locked (no class tags exist anymore).
                    const isLockedScopeTag = tag.toLowerCase().startsWith('memory-');
                    return (
                      <span
                        key={tag}
                        className={`inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-full font-medium shadow-xs transition-all border ${
                          isLockedScopeTag
                            ? 'bg-indigo-500/10 dark:bg-emerald-400/10 border-indigo-500/30 dark:border-emerald-400/30 text-indigo-650 dark:text-emerald-400'
                            : 'bg-white dark:bg-slate-900 border-slate-200 dark:border-slate-800 text-slate-600 dark:text-slate-300'
                        }`}
                      >
                        {tag}
                        {!isLockedScopeTag && (
                          <button
                            type="button"
                            onClick={() => removeTag(tag)}
                            className="text-slate-400 hover:text-rose-500 transition-colors ml-0.5 cursor-pointer font-bold"
                          >
                            &times;
                          </button>
                        )}
                      </span>
                    );
                  })}
                  <div className="relative">
                    <input
                      type="text"
                      placeholder="Add tag..."
                      value={tagInput}
                      onChange={(e) => handleTagInputChange(e.target.value)}
                      onKeyDown={handleTagKeyDown}
                      className="text-[10px] py-0.5 px-2 rounded-full border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-900 text-slate-800 dark:text-slate-200 outline-hidden w-20 focus:w-28 focus:ring-1 focus:ring-indigo-500 transition-all font-medium"
                    />

                    {/* Auto-complete Tag Suggestions Dropdown */}
                    {tagSuggestions.length > 0 && (
                      <div className="absolute left-0 top-full mt-1 z-[99] bg-white dark:bg-slate-900 backdrop-blur-lg border border-slate-200 dark:border-slate-800 shadow-xl rounded-lg max-h-36 overflow-y-auto min-w-[120px] py-1 select-none font-sans text-[10px]">
                        {tagSuggestions.map((suggestion, idx) => (
                          <div
                            key={suggestion}
                            onClick={() => selectTagSuggestion(suggestion)}
                            className={`px-3 py-1.5 cursor-pointer font-medium transition-colors ${
                              idx === focusedIndex
                                ? 'bg-indigo-500 text-white'
                                : 'hover:bg-indigo-500 hover:text-white dark:hover:bg-themeAccent/85 text-slate-700 dark:text-slate-300'
                            }`}
                          >
                            {suggestion}
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                </div>
              </div>
            )}
          </div>

          <div className="flex items-center gap-2">
            {slug && (
              <input
                type="text"
                placeholder="What did you change? (e.g. Fixed typo)"
                value={editSummary}
                onChange={(e) => setEditSummary(e.target.value)}
                className="py-1.5 px-3.5 rounded-xl border border-slate-200/60 dark:border-slate-800/60 text-xs bg-slate-50 dark:bg-slate-900 outline-hidden text-slate-800 dark:text-slate-200 w-56 focus:ring-1 focus:ring-indigo-500 font-medium transition-all"
                disabled={isSaving}
              />
            )}
            <button
              type="button"
              onClick={onCancel}
              className="flex items-center gap-1.5 py-2 px-3.5 rounded-xl border border-slate-200/60 dark:border-slate-800/60 text-slate-600 dark:text-slate-400 bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800 font-semibold text-sm active:scale-95 transition-all cursor-pointer"
              disabled={isSaving}
            >
              <X size={14} />
              <span>Cancel</span>
            </button>
            <button
              type="submit"
              className="flex items-center gap-1.5 py-2 px-4 rounded-xl bg-gradient-to-r from-indigo-600 to-violet-600 hover:from-indigo-500 hover:to-violet-500 active:scale-95 text-white font-semibold text-sm shadow-md shadow-indigo-100 dark:shadow-none transition-all cursor-pointer"
              disabled={isSaving}
            >
              <Save size={14} className={isSaving ? 'animate-spin' : ''} />
              <span>{isSaving ? 'Saving...' : 'Save Page'}</span>
            </button>
          </div>
        </div>

        {/* Markdown Action Toolbar */}
        <div className="px-4 py-2 border-b border-slate-200/50 dark:border-slate-800/40 bg-slate-50 dark:bg-slate-900/30 flex items-center justify-between select-none">
          {/* Format Controls */}
          <div className="flex items-center gap-0.5">
            <button
              type="button"
              onClick={() => insertMarkdown('# ', '', 'Header 1')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors cursor-pointer"
              title="Header 1"
            >
              <Heading1 size={15} />
            </button>
            <button
              type="button"
              onClick={() => insertMarkdown('## ', '', 'Header 2')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors cursor-pointer"
              title="Header 2"
            >
              <Heading2 size={15} />
            </button>
            <button
              type="button"
              onClick={() => insertMarkdown('### ', '', 'Header 3')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors cursor-pointer"
              title="Header 3"
            >
              <Heading3 size={15} />
            </button>

            <div className="w-[1px] h-4 bg-slate-300 dark:bg-slate-800 mx-2"></div>

            <button
              type="button"
              onClick={() => insertMarkdown('**', '**', 'bold text')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors cursor-pointer"
              title="Bold"
            >
              <Bold size={15} />
            </button>
            <button
              type="button"
              onClick={() => insertMarkdown('*', '*', 'italic text')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors cursor-pointer"
              title="Italic"
            >
              <Italic size={15} />
            </button>
            <button
              type="button"
              onClick={() => insertMarkdown('`', '`', 'code')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors cursor-pointer"
              title="Inline Code"
            >
              <Code size={15} />
            </button>

            <div className="w-[1px] h-4 bg-slate-300 dark:bg-slate-800 mx-2"></div>

            <button
              type="button"
              onClick={() => insertMarkdown('[', '](https://url)', 'Link Text')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors cursor-pointer"
              title="Standard URL Link"
            >
              <Link size={15} />
            </button>
            <button
              type="button"
              onClick={() => insertMarkdown('[[', ']]', 'Wiki Link')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors cursor-pointer"
              title="Internal Wiki Link"
            >
              <Link2 size={15} />
            </button>
            <button
              type="button"
              onClick={() => fileInputRef.current?.click()}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors flex items-center gap-1.5 cursor-pointer font-semibold text-[10px]"
              title="Upload and Embed Image"
              disabled={isUploading}
            >
              <ImageIcon size={15} className={isUploading ? 'animate-bounce text-indigo-500' : ''} />
              <span className="text-[10px] font-semibold">{isUploading ? 'Uploading...' : ''}</span>
            </button>
            <input
              type="file"
              ref={fileInputRef}
              onChange={onFileChange}
              accept="image/*"
              className="hidden"
            />

            <div className="w-[1px] h-4 bg-slate-300 dark:bg-slate-800 mx-2"></div>

            {/* Quick Reference Button */}
            <button
              type="button"
              onClick={() => setSyntaxModalOpen(true)}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors flex items-center gap-1 cursor-pointer"
              title="Markdown Syntax Cheatsheet (Ctrl+/)"
            >
              <Info size={15} />
            </button>

            {/* Copy Raw Markdown Button */}
            <button
              type="button"
              onClick={handleCopyMarkdown}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors flex items-center gap-1 cursor-pointer"
              title="Copy Raw Markdown Content"
            >
              {copiedMarkdown ? <Check size={15} className="text-emerald-500" /> : <Copy size={15} />}
            </button>

            {/* Linter Count Badge */}
            {diagnostics.length > 0 && (
              <button
                type="button"
                onClick={() => setLintModalOpen(true)}
                className="flex items-center gap-1.5 px-2.5 py-1 rounded-xl bg-rose-500/10 dark:bg-rose-950/20 border border-rose-500/20 text-rose-600 dark:text-rose-400 hover:bg-rose-500/20 transition-all font-bold text-[10px] select-none cursor-pointer"
                title="View Markdown Linting Diagnostics"
              >
                <AlertCircle size={12} className={errorCount > 0 ? "animate-pulse text-rose-500" : "text-amber-500"} />
                <span>{errorCount} E</span>
                <span className="text-slate-400">|</span>
                <span>{warningCount} W</span>
              </button>
            )}
          </div>

          {/* Toggle Screen Layout */}
          <div className="flex items-center gap-0.5 rounded-xl bg-slate-200/50 dark:bg-slate-950 p-1">
            <button
              type="button"
              onClick={() => setViewMode('edit')}
              className={`p-1.5 rounded-lg flex items-center gap-1 text-[10px] font-bold transition-all cursor-pointer ${
                viewMode === 'edit'
                  ? 'bg-white dark:bg-slate-800 text-slate-800 dark:text-white shadow-xs'
                  : 'text-slate-500 hover:text-slate-800 dark:hover:text-slate-300'
              }`}
            >
              <Edit3 size={12} />
              <span>Editor</span>
            </button>
            <button
              type="button"
              onClick={() => setViewMode('split')}
              className={`p-1.5 rounded-lg flex items-center gap-1 text-[10px] font-bold transition-all cursor-pointer ${
                viewMode === 'split'
                  ? 'bg-white dark:bg-slate-800 text-slate-800 dark:text-white shadow-xs'
                  : 'text-slate-500 hover:text-slate-800 dark:hover:text-slate-300'
              }`}
            >
              <Columns size={12} />
              <span>Split View</span>
            </button>
            <button
              type="button"
              onClick={() => setViewMode('preview')}
              className={`p-1.5 rounded-lg flex items-center gap-1 text-[10px] font-bold transition-all cursor-pointer ${
                viewMode === 'preview'
                  ? 'bg-white dark:bg-slate-800 text-slate-800 dark:text-white shadow-xs'
                  : 'text-slate-500 hover:text-slate-800 dark:hover:text-slate-300'
              }`}
            >
              <Eye size={12} />
              <span>Preview</span>
            </button>
          </div>
        </div>

        {/* Main Work Area */}
        <div ref={containerRef} className="flex-1 flex overflow-hidden relative min-w-0">
          
          {/* Drag Overlay to prevent mouse event loss or selections during drag */}
          {isDragging && (
            <div className="absolute inset-0 z-50 cursor-col-resize select-none" />
          )}

          {/* Error Banner */}
          {errorMsg && (
            <div className="absolute top-4 left-4 right-4 z-50 bg-rose-50 dark:bg-rose-950/40 text-rose-600 dark:text-rose-400 p-4 border border-rose-200 dark:border-rose-900 rounded-xl text-sm font-medium shadow-md animate-fade-in">
              {errorMsg}
            </div>
          )}

          {/* Left Column (CodeMirror Editor Pane) */}
          {(viewMode === 'edit' || viewMode === 'split') && (
            <div 
              onDrop={handleDrop}
              onDragOver={handleDragOver}
              onContextMenu={handleContextMenu}
              style={{ width: viewMode === 'split' ? `${splitPercentage}%` : undefined }}
              className={`${viewMode === 'split' ? 'flex-shrink-0' : 'flex-1'} h-full overflow-hidden bg-white dark:bg-slate-900 flex flex-col font-mono text-slate-800 dark:text-slate-200 min-w-0`}
            >
              <CodeMirror
                ref={editorRef}
                value={content}
                onChange={(value) => setContent(value)}
                extensions={extensions}
                theme="none"
                basicSetup={{
                  lineNumbers: true,
                  highlightActiveLineGutter: true,
                  highlightSpecialChars: true,
                  history: true,
                  drawSelection: true,
                  dropCursor: true,
                  allowMultipleSelections: false,
                  indentOnInput: true,
                  syntaxHighlighting: false,
                  bracketMatching: true,
                  closeBrackets: true,
                  autocompletion: true,
                  rectangularSelection: false,
                  highlightActiveLine: true,
                  highlightSelectionMatches: true,
                  searchKeymap: true,
                  lintKeymap: true,
                }}
                className="flex-1 h-full outline-hidden focus:ring-0 leading-relaxed text-sm"
                placeholder="Write your markdown here... Drag & Drop images directly into this pane!"
                editable={!isSaving}
              />
              <div className="px-4 py-1.5 border-t border-slate-200/50 dark:border-slate-800/40 bg-slate-50 dark:bg-slate-900/30 text-[10px] text-slate-400 dark:text-slate-500 font-sans flex items-center justify-between select-none">
                <span>Press <span className="font-mono bg-slate-200 dark:bg-slate-800 px-1 py-0.2 rounded font-bold">Ctrl+/</span> for markdown reference</span>
                <span>{content.length} characters</span>
              </div>
            </div>
          )}

          {/* Vertical Separator Handle */}
          {viewMode === 'split' && (
            <div 
              onMouseDown={startResizing}
              className="w-1.5 h-full cursor-col-resize bg-slate-200 dark:bg-slate-800 hover:bg-themeAccent/50 active:bg-themeAccent/80 transition-colors flex-shrink-0 z-10"
              title="Drag to resize pane width"
            />
          )}

          {/* Right Column (Visual Rendered Preview Pane) */}
          {(viewMode === 'preview' || viewMode === 'split') && (
            <div 
              style={{ width: viewMode === 'split' ? `${100 - splitPercentage}%` : undefined }}
              className={`${viewMode === 'split' ? 'flex-shrink-0' : 'flex-1'} h-full overflow-y-auto bg-slate-50 dark:bg-slate-950/20 p-8 min-w-0`}
            >
              <div className="max-w-2xl mx-auto py-2 bg-white dark:bg-slate-900 border border-slate-200/50 dark:border-slate-800/60 p-8 shadow-xs rounded-2xl min-h-full">
                {title.trim() && (
                  <h1 className="text-4xl font-extrabold text-slate-900 dark:text-white border-b border-slate-200 dark:border-slate-800 pb-3 mb-6 tracking-tight">
                    {title}
                  </h1>
                )}
                {content.trim() ? (
                  <Viewer 
                    content={content} 
                    onNavigate={() => {}} // No navigation triggered in preview mode
                    articles={articles}
                  />
                ) : (
                  <div className="text-slate-400 italic text-sm">
                    Live visual preview of your article will render here as you type...
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      </form>

      {/* Cheatsheet Modal */}
      <MarkdownSyntaxModal 
        isOpen={syntaxModalOpen}
        onClose={() => setSyntaxModalOpen(false)}
      />

      {/* Linter Modal */}
      <MarkdownLintErrorModal
        isOpen={lintModalOpen}
        onClose={() => setLintModalOpen(false)}
        diagnostics={diagnostics}
        onSelectDiagnostic={handleSelectDiagnostic}
      />

      {/* Custom Context Menu */}
      {contextMenu && (
        <div
          style={{ top: contextMenu.y, left: contextMenu.x }}
          className="fixed z-50 rounded-xl glass-panel bg-white/95 dark:bg-slate-900/95 border border-slate-200/50 dark:border-slate-800/50 shadow-xl p-1.5 min-w-[185px] select-none text-xs animate-fade-in"
        >
          {/* Only a `fix` is offered as an applicable action — it is replacement text for the
              diagnostic's range. A `hint` is prose and is shown below instead: inserting it would
              overwrite the author's Markdown with a sentence. */}
          {contextMenu.diagnostic.fix && (
            <button
              onClick={() => {
                const view = editorRef.current?.view;
                if (view) {
                  view.dispatch({
                    changes: {
                      from: contextMenu.diagnostic.from,
                      to: contextMenu.diagnostic.to,
                      insert: contextMenu.diagnostic.fix!
                    }
                  });
                }
                setContextMenu(null);
              }}
              className="w-full text-left px-3 py-2 rounded-lg font-semibold hover:bg-slate-100 dark:hover:bg-slate-800 text-indigo-600 dark:text-indigo-400 cursor-pointer"
            >
              Fix: {contextMenu.diagnostic.fix}
            </button>
          )}
          {contextMenu.diagnostic.hint && (
            <p className="px-3 py-2 text-slate-500 dark:text-slate-400 max-w-[240px] cursor-default">
              {contextMenu.diagnostic.hint}
            </p>
          )}
          <button
            onClick={() => {
              setLintModalOpen(true);
              setContextMenu(null);
            }}
            className="w-full text-left px-3 py-2 rounded-lg font-semibold hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-700 dark:text-slate-350 cursor-pointer"
          >
            Show in Error Panel
          </button>
          <div className="h-[1px] bg-slate-200/50 dark:bg-slate-800/50 my-1"></div>
          <button
            onClick={() => setContextMenu(null)}
            className="w-full text-left px-3 py-2 rounded-lg font-semibold hover:bg-slate-100 dark:hover:bg-slate-800 text-rose-500 hover:text-rose-600 cursor-pointer"
          >
            Close Menu
          </button>
        </div>
      )}
    </div>
  );
};

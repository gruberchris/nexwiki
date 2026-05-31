import React, { useState, useRef, useEffect } from 'react';
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
  Link2
} from 'lucide-react';
import { Slugify } from '../utils'; // We will create this simple utility next
import { Viewer } from './Viewer';

interface EditorProps {
  initialTitle: string;
  initialContent: string;
  slug: string; // empty if new page
  onSave: (title: string, content: string, editSummary: string) => Promise<void>;
  onCancel: () => void;
}

export const Editor: React.FC<EditorProps> = ({
  initialTitle,
  initialContent,
  slug,
  onSave,
  onCancel
}) => {
  const [title, setTitle] = useState(initialTitle);
  const [content, setContent] = useState(initialContent);
  const [viewMode, setViewMode] = useState<'split' | 'edit' | 'preview'>('split');
  const [isSaving, setIsSaving] = useState(false);
  const [isUploading, setIsUploading] = useState(false);
  const [errorMsg, setErrorMsg] = useState('');
  const [editSummary, setEditSummary] = useState('');

  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // Auto-resize textarea
  useEffect(() => {
    const textarea = textareaRef.current;
    if (textarea) {
      textarea.style.height = 'auto';
      textarea.style.height = `${textarea.scrollHeight}px`;
    }
  }, [content]);

  // Handle Tab key insertion (standard indentation)
  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Tab') {
      e.preventDefault();
      const textarea = textareaRef.current;
      if (!textarea) return;

      const start = textarea.selectionStart;
      const end = textarea.selectionEnd;
      const value = textarea.value;

      const newContent = value.substring(0, start) + "    " + value.substring(end);
      setContent(newContent);

      // Reset selection position after state batch render
      setTimeout(() => {
        textarea.selectionStart = textarea.selectionEnd = start + 4;
      }, 0);
    }
  };

  // Helper to insert markdown tags at cursor selection
  const insertMarkdown = (before: string, after: string = '', defaultText: string = '') => {
    const textarea = textareaRef.current;
    if (!textarea) return;

    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    const value = textarea.value;
    const selectedText = value.substring(start, end) || defaultText;

    const inserted = before + selectedText + after;
    const newContent = value.substring(0, start) + inserted + value.substring(end);
    setContent(newContent);

    // Focus and select the newly wrapped text
    setTimeout(() => {
      textarea.focus();
      textarea.selectionStart = start + before.length;
      textarea.selectionEnd = start + before.length + selectedText.length;
    }, 0);
  };

  // Handle Image uploads
  const handleImageUpload = async (file: File) => {
    if (!file) return;

    // Determine current slug target (fallback to temporary slug if title is blank)
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
      // Insert Markdown image syntax at current position
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
  const handleDrop = (e: React.DragEvent<HTMLTextAreaElement>) => {
    e.preventDefault();
    if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      const file = e.dataTransfer.files[0];
      if (file.type.startsWith('image/')) {
        void handleImageUpload(file);
      }
    }
  };

  const handleDragOver = (e: React.DragEvent<HTMLTextAreaElement>) => {
    e.preventDefault();
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
      await onSave(title.trim(), content, editSummary);
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed to save article.';
      setErrorMsg(msg);
      setIsSaving(false);
    }
  };

  // Computed slug for live UI preview
  const liveSlug = Slugify(title);

  return (
    <div className="flex-1 h-screen flex flex-col bg-slate-50 dark:bg-slate-950/40">
      <form onSubmit={handleSave} className="flex-1 flex flex-col h-full overflow-hidden">
        
        {/* Editor Top Control Bar */}
        <div className="p-4 border-b border-slate-200/80 dark:border-slate-800/80 bg-white/80 dark:bg-slate-900/80 backdrop-blur-md flex items-center justify-between gap-4 select-none">
          <div className="flex-1">
            <input
              type="text"
              placeholder="Article Title..."
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              className="w-full text-xl font-bold bg-transparent border-none outline-none text-slate-950 dark:text-white placeholder:text-slate-400"
              required
              disabled={isSaving}
            />
            {title.trim() && (
              <div className="flex items-center gap-1.5 mt-1 text-[10px] font-semibold text-slate-400 dark:text-slate-500">
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
            )}
          </div>

          <div className="flex items-center gap-2">
            {slug && (
              <input
                type="text"
                placeholder="What did you change? (e.g. Fixed typo)"
                value={editSummary}
                onChange={(e) => setEditSummary(e.target.value)}
                className="py-1.5 px-3.5 rounded-xl border border-slate-200 dark:border-slate-800 text-xs bg-slate-50 dark:bg-slate-900 outline-none text-slate-800 dark:text-slate-200 w-56 focus:ring-1 focus:ring-indigo-500 font-medium transition-all"
                disabled={isSaving}
              />
            )}
            <button
              type="button"
              onClick={onCancel}
              className="flex items-center gap-1.5 py-2 px-3.5 rounded-xl border border-slate-200 dark:border-slate-800 text-slate-600 dark:text-slate-400 bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800 font-semibold text-sm active:scale-95 transition-all"
              disabled={isSaving}
            >
              <X size={14} />
              <span>Cancel</span>
            </button>
            <button
              type="submit"
              className="flex items-center gap-1.5 py-2 px-4 rounded-xl bg-gradient-to-r from-indigo-600 to-violet-600 hover:from-indigo-500 hover:to-violet-500 active:scale-95 text-white font-semibold text-sm shadow-md shadow-indigo-100 dark:shadow-none transition-all"
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
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors"
              title="Header 1"
            >
              <Heading1 size={15} />
            </button>
            <button
              type="button"
              onClick={() => insertMarkdown('## ', '', 'Header 2')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors"
              title="Header 2"
            >
              <Heading2 size={15} />
            </button>
            <button
              type="button"
              onClick={() => insertMarkdown('### ', '', 'Header 3')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors"
              title="Header 3"
            >
              <Heading3 size={15} />
            </button>

            <div className="w-[1px] h-4 bg-slate-300 dark:bg-slate-800 mx-2"></div>

            <button
              type="button"
              onClick={() => insertMarkdown('**', '**', 'bold text')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors"
              title="Bold"
            >
              <Bold size={15} />
            </button>
            <button
              type="button"
              onClick={() => insertMarkdown('*', '*', 'italic text')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors"
              title="Italic"
            >
              <Italic size={15} />
            </button>
            <button
              type="button"
              onClick={() => insertMarkdown('`', '`', 'code')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors"
              title="Inline Code"
            >
              <Code size={15} />
            </button>

            <div className="w-[1px] h-4 bg-slate-300 dark:bg-slate-800 mx-2"></div>

            <button
              type="button"
              onClick={() => insertMarkdown('[', '](https://url)', 'Link Text')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors"
              title="Standard URL Link"
            >
              <Link size={15} />
            </button>
            <button
              type="button"
              onClick={() => insertMarkdown('[[', ']]', 'Wiki Link')}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors"
              title="Internal Wiki Link"
            >
              <Link2 size={15} />
            </button>
            <button
              type="button"
              onClick={() => fileInputRef.current?.click()}
              className="p-2 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-white transition-colors flex items-center gap-1.5"
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
          </div>

          {/* Toggle Screen Layout */}
          <div className="flex items-center gap-0.5 rounded-xl bg-slate-200/50 dark:bg-slate-950 p-1">
            <button
              type="button"
              onClick={() => setViewMode('edit')}
              className={`p-1.5 rounded-lg flex items-center gap-1 text-[10px] font-bold transition-all ${
                viewMode === 'edit'
                  ? 'bg-white dark:bg-slate-800 text-slate-800 dark:text-white shadow-sm'
                  : 'text-slate-500 hover:text-slate-800 dark:hover:text-slate-300'
              }`}
            >
              <Edit3 size={12} />
              <span>Editor</span>
            </button>
            <button
              type="button"
              onClick={() => setViewMode('split')}
              className={`p-1.5 rounded-lg flex items-center gap-1 text-[10px] font-bold transition-all ${
                viewMode === 'split'
                  ? 'bg-white dark:bg-slate-800 text-slate-800 dark:text-white shadow-sm'
                  : 'text-slate-500 hover:text-slate-800 dark:hover:text-slate-300'
              }`}
            >
              <Columns size={12} />
              <span>Split View</span>
            </button>
            <button
              type="button"
              onClick={() => setViewMode('preview')}
              className={`p-1.5 rounded-lg flex items-center gap-1 text-[10px] font-bold transition-all ${
                viewMode === 'preview'
                  ? 'bg-white dark:bg-slate-800 text-slate-800 dark:text-white shadow-sm'
                  : 'text-slate-500 hover:text-slate-800 dark:hover:text-slate-300'
              }`}
            >
              <Eye size={12} />
              <span>Preview</span>
            </button>
          </div>
        </div>

        {/* Main Work Area */}
        <div className="flex-1 flex overflow-hidden relative">
          
          {/* Error Banner */}
          {errorMsg && (
            <div className="absolute top-4 left-4 right-4 z-50 bg-rose-50 dark:bg-rose-950/40 text-rose-600 dark:text-rose-400 p-4 border border-rose-200 dark:border-rose-900 rounded-xl text-sm font-medium shadow-md animate-fade-in">
              {errorMsg}
            </div>
          )}

          {/* Left Column (Raw Markdown Editor Pane) */}
          {(viewMode === 'edit' || viewMode === 'split') && (
            <div className="flex-1 h-full overflow-y-auto bg-white dark:bg-slate-900 p-6 flex flex-col font-mono text-slate-800 dark:text-slate-200">
              <textarea
                ref={textareaRef}
                value={content}
                onChange={(e) => setContent(e.target.value)}
                onKeyDown={handleKeyDown}
                onDrop={handleDrop}
                onDragOver={handleDragOver}
                placeholder="Write your markdown here... Drag & Drop images directly into this pane!"
                className="w-full flex-1 bg-transparent resize-none border-none outline-none focus:ring-0 leading-relaxed text-sm font-mono placeholder:text-slate-400"
                disabled={isSaving}
              />
            </div>
          )}

          {/* Vertical Separator */}
          {viewMode === 'split' && (
            <div className="w-[1px] h-full bg-slate-200 dark:bg-slate-800"></div>
          )}

          {/* Right Column (Visual Rendered Preview Pane) */}
          {(viewMode === 'preview' || viewMode === 'split') && (
            <div className="flex-1 h-full overflow-y-auto bg-slate-50 dark:bg-slate-950/20 p-8">
              <div className="max-w-2xl mx-auto py-2 bg-white dark:bg-slate-900 border border-slate-200/50 dark:border-slate-800/60 p-8 shadow-sm rounded-2xl min-h-full">
                {title.trim() && (
                  <h1 className="text-4xl font-extrabold text-slate-900 dark:text-white border-b border-slate-200 dark:border-slate-800 pb-3 mb-6 tracking-tight">
                    {title}
                  </h1>
                )}
                {content.trim() ? (
                  <Viewer 
                    content={content} 
                    onNavigate={() => {}} // No navigation triggered in preview mode
                    articles={[]} // Pass empty list to preview
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
    </div>
  );
};

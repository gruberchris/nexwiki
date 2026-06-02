import React, { useRef } from 'react';
import { X, Heading, Bold, List, Link, Table, Code, Info } from 'lucide-react';
import { useEscapeKey } from '../hooks/useEscapeKey';

interface MarkdownSyntaxModalProps {
  isOpen: boolean;
  onClose: () => void;
}

export const MarkdownSyntaxModal: React.FC<MarkdownSyntaxModalProps> = ({ isOpen, onClose }) => {
  const modalRef = useRef<HTMLDivElement>(null);

  useEscapeKey(isOpen, onClose);

  if (!isOpen) return null;

  const categories = [
    {
      title: 'Headings',
      icon: <Heading className="text-indigo-500" size={18} />,
      examples: [
        { syntax: '# Heading 1', desc: 'Main title (use once)' },
        { syntax: '## Heading 2', desc: 'Section header' },
        { syntax: '### Heading 3', desc: 'Subsection header' },
      ],
    },
    {
      title: 'Emphasis',
      icon: <Bold className="text-violet-500" size={18} />,
      examples: [
        { syntax: '**bold text**', desc: 'Strong emphasis' },
        { syntax: '*italic text*', desc: 'Emphasis' },
        { syntax: '~~strikethrough~~', desc: 'Deleted text' },
      ],
    },
    {
      title: 'Lists',
      icon: <List className="text-emerald-500" size={18} />,
      examples: [
        { syntax: '- Item 1\n- Item 2', desc: 'Unordered bullet list' },
        { syntax: '1. First\n2. Second', desc: 'Ordered numbered list' },
        { syntax: '- [x] Completed\n- [ ] Todo', desc: 'Task checklist item' },
      ],
    },
    {
      title: 'Links & Links Types',
      icon: <Link className="text-amber-500" size={18} />,
      examples: [
        { syntax: '[Google](https://google.com)', desc: 'Standard external link' },
        { syntax: '[[Learning Go]]', desc: 'Internal WikiLink (slugifies)' },
        { syntax: '[[target-slug|Custom text]]', desc: 'WikiLink with display text' },
      ],
    },
    {
      title: 'Code blocks',
      icon: <Code className="text-rose-500" size={18} />,
      examples: [
        { syntax: '`inline code`', desc: 'Code within text' },
        { syntax: '```python\nprint("Hello")\n```', desc: 'Fenced code blocks with language' },
      ],
    },
    {
      title: 'Tables & Helpers',
      icon: <Table className="text-blue-500" size={18} />,
      examples: [
        { syntax: '| Header | Title |\n|--------|-------|\n| Cell 1 | Cell 2 |', desc: 'Formatted grid table' },
        { syntax: '> blockquote', desc: 'Indented blockquote callout' },
      ],
    },
  ];

  const handleBackdropClick = (e: React.MouseEvent) => {
    if (modalRef.current && !modalRef.current.contains(e.target as Node)) {
      onClose();
    }
  };

  return (
    <div
      onClick={handleBackdropClick}
      className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/40 dark:bg-slate-950/60 backdrop-blur-sm animate-fade-in p-4"
    >
      <div
        ref={modalRef}
        className="w-full max-w-2xl rounded-2xl glass-panel bg-white/95 dark:bg-slate-900/95 border border-slate-200/50 dark:border-slate-800/50 shadow-2xl p-6 relative flex flex-col max-h-[85vh] overflow-hidden"
      >
        {/* Header */}
        <div className="flex items-center justify-between pb-4 border-b border-slate-100 dark:border-slate-800/60 select-none">
          <div className="flex items-center gap-2">
            <div className="p-1.5 rounded-lg bg-indigo-50 dark:bg-indigo-950/30 text-indigo-600 dark:text-indigo-400">
              <Info size={18} />
            </div>
            <div>
              <h2 className="text-lg font-bold text-slate-900 dark:text-white">Markdown Syntax Guide</h2>
              <p className="text-xs text-slate-500 dark:text-slate-400">Quick reference cheatsheet & hotkeys</p>
            </div>
          </div>
          <button
            onClick={onClose}
            className="p-1.5 rounded-xl border border-slate-200/60 dark:border-slate-800/60 text-slate-500 hover:text-slate-800 dark:hover:text-white bg-slate-50 dark:bg-slate-800 hover:bg-slate-100 dark:hover:bg-slate-700/60 transition-all cursor-pointer"
          >
            <X size={16} />
          </button>
        </div>

        {/* Content Body */}
        <div className="flex-1 overflow-y-auto py-6 grid grid-cols-1 md:grid-cols-2 gap-4 pr-1">
          {categories.map((cat, idx) => (
            <div
              key={idx}
              className="p-4 rounded-xl border border-slate-200/40 dark:border-slate-800/40 bg-slate-50/40 dark:bg-slate-950/20 flex flex-col gap-3"
            >
              <div className="flex items-center gap-2 font-bold text-sm text-slate-800 dark:text-slate-200 select-none">
                {cat.icon}
                <span>{cat.title}</span>
              </div>
              <div className="flex flex-col gap-2">
                {cat.examples.map((ex, eIdx) => (
                  <div key={eIdx} className="text-xs flex flex-col gap-1">
                    <pre className="p-2 rounded-lg bg-white dark:bg-slate-950 border border-slate-100 dark:border-slate-900 text-indigo-600 dark:text-indigo-400 font-mono overflow-x-auto whitespace-pre-wrap">
                      {ex.syntax}
                    </pre>
                    <span className="text-[10px] text-slate-500 dark:text-slate-400 pl-1">
                      {ex.desc}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>

        {/* Footer */}
        <div className="pt-4 border-t border-slate-100 dark:border-slate-800/60 flex items-center justify-between text-[11px] text-slate-400 dark:text-slate-500 select-none">
          <div className="flex items-center gap-1.5 font-medium">
            <span className="px-1.5 py-0.5 rounded bg-slate-150 dark:bg-slate-800 font-mono text-[9px] border border-slate-200 dark:border-slate-700 text-slate-600 dark:text-slate-400">Ctrl+/</span>
            <span>or</span>
            <span className="px-1.5 py-0.5 rounded bg-slate-150 dark:bg-slate-800 font-mono text-[9px] border border-slate-200 dark:border-slate-700 text-slate-600 dark:text-slate-400">Cmd+/</span>
            <span>toggles this reference inside the editor</span>
          </div>
          <button
            onClick={onClose}
            className="text-indigo-600 dark:text-indigo-400 hover:text-indigo-800 dark:hover:text-indigo-300 font-semibold cursor-pointer"
          >
            Got it, thanks!
          </button>
        </div>
      </div>
    </div>
  );
};

import React, { useState } from 'react';
import { X, AlertCircle, AlertTriangle, Info, Copy, Sparkles, ArrowUpDown, Check } from 'lucide-react';
import type { LintDiagnostic } from '../utils/markdownLinter';

interface MarkdownLintErrorModalProps {
  isOpen: boolean;
  onClose: () => void;
  diagnostics: LintDiagnostic[];
  onSelectDiagnostic: (diag: LintDiagnostic) => void;
  markdownContent: string;
}

export const MarkdownLintErrorModal: React.FC<MarkdownLintErrorModalProps> = ({
  isOpen,
  onClose,
  diagnostics,
  onSelectDiagnostic,
  markdownContent,
}) => {
  const [filter, setFilter] = useState<'all' | 'error' | 'warning' | 'info'>('all');
  const [sortBy, setSortBy] = useState<'line' | 'severity'>('line');
  const [sortOrder, setSortOrder] = useState<'asc' | 'desc'>('asc');
  const [copiedAll, setCopiedAll] = useState(false);
  const [copiedAiPrompt, setCopiedAiPrompt] = useState(false);

  if (!isOpen) return null;

  // Filter
  const filtered = diagnostics.filter((d) => {
    if (filter === 'all') return true;
    return d.severity === filter;
  });

  // Sort
  const sorted = [...filtered].sort((a, b) => {
    let cmp: number;
    if (sortBy === 'line') {
      cmp = a.line - b.line;
    } else {
      const severityScore = { error: 3, warning: 2, info: 1 };
      cmp = severityScore[a.severity] - severityScore[b.severity];
    }
    return sortOrder === 'asc' ? cmp : -cmp;
  });

  const toggleSort = (field: 'line' | 'severity') => {
    if (sortBy === field) {
      setSortOrder(sortOrder === 'asc' ? 'desc' : 'asc');
    } else {
      setSortBy(field);
      setSortOrder('asc');
    }
  };

  // Copy All Errors
  const handleCopyAll = async () => {
    const text = diagnostics
      .map((d) => `[${d.severity.toUpperCase()}] Line ${d.line} (${d.code}): ${d.message}${d.suggestion ? ` Suggested fix: ${d.suggestion}` : ''}`)
      .join('\n');
    await navigator.clipboard.writeText(text);
    setCopiedAll(true);
    setTimeout(() => setCopiedAll(false), 2000);
  };

  // Generate AI Correction Prompt
  const handleCopyAiPrompt = async () => {
    const errorsText = diagnostics
      .map((d, index) => `${index + 1}. Line ${d.line} [${d.severity}]: ${d.message}${d.suggestion ? ` (Try: ${d.suggestion})` : ''}`)
      .join('\n');

    const prompt = `You are a meticulous technical documentation editor. I have a draft markdown document with some syntax linter errors and formatting warnings. Please review the markdown and correct the identified issues perfectly, preserving all other text content, headers, and WikiLink structures.

## Linter Diagnostics to Fix:
${errorsText}

## Markdown Document Draft:
\`\`\`markdown
${markdownContent}
\`\`\`

Please output ONLY the updated markdown code block, with all warnings and errors resolved. Do not wrap it in external conversations.`;

    await navigator.clipboard.writeText(prompt);
    setCopiedAiPrompt(true);
    setTimeout(() => setCopiedAiPrompt(false), 2000);
  };

  const getSeverityStyles = (severity: 'error' | 'warning' | 'info') => {
    switch (severity) {
      case 'error':
        return {
          icon: <AlertCircle className="text-rose-500" size={14} />,
          badge: 'bg-rose-500/10 border-rose-500/20 text-rose-600 dark:text-rose-400',
          bg: 'hover:bg-rose-500/5',
        };
      case 'warning':
        return {
          icon: <AlertTriangle className="text-amber-500" size={14} />,
          badge: 'bg-amber-500/10 border-amber-500/20 text-amber-600 dark:text-amber-400',
          bg: 'hover:bg-amber-500/5',
        };
      case 'info':
        return {
          icon: <Info className="text-indigo-500" size={14} />,
          badge: 'bg-indigo-500/10 border-indigo-500/20 text-indigo-600 dark:text-indigo-400',
          bg: 'hover:bg-indigo-500/5',
        };
    }
  };

  const handleBackdropClick = (e: React.MouseEvent) => {
    if ((e.target as HTMLElement).id === 'modal-backdrop') {
      onClose();
    }
  };

  return (
    <div
      id="modal-backdrop"
      onClick={handleBackdropClick}
      className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/40 dark:bg-slate-950/60 backdrop-blur-sm animate-fade-in p-4"
    >
      <div className="w-full max-w-3xl rounded-2xl glass-panel bg-white/95 dark:bg-slate-900/95 border border-slate-200/50 dark:border-slate-800/50 shadow-2xl p-6 relative flex flex-col max-h-[85vh] overflow-hidden">
        {/* Header */}
        <div className="flex items-center justify-between pb-4 border-b border-slate-100 dark:border-slate-800/60 select-none">
          <div className="flex items-center gap-2">
            <div className="p-1.5 rounded-lg bg-rose-50 dark:bg-rose-950/30 text-rose-500 dark:text-rose-400">
              <AlertCircle size={18} />
            </div>
            <div>
              <h2 className="text-lg font-bold text-slate-900 dark:text-white">Markdown Lint Errors</h2>
              <p className="text-xs text-slate-500 dark:text-slate-400">
                Found {diagnostics.length} issues in the active document
              </p>
            </div>
          </div>
          <button
            onClick={onClose}
            className="p-1.5 rounded-xl border border-slate-200/60 dark:border-slate-800/60 text-slate-500 hover:text-slate-800 dark:hover:text-white bg-slate-50 dark:bg-slate-800 hover:bg-slate-100 dark:hover:bg-slate-700/60 transition-all cursor-pointer"
          >
            <X size={16} />
          </button>
        </div>

        {/* Toolbar: Filters & Sorting */}
        <div className="flex flex-wrap items-center justify-between gap-4 py-3 select-none">
          <div className="flex items-center gap-1.5 bg-slate-100 dark:bg-slate-950 p-1 rounded-xl">
            {(['all', 'error', 'warning', 'info'] as const).map((t) => (
              <button
                key={t}
                onClick={() => setFilter(t)}
                className={`px-3 py-1.5 rounded-lg text-xs font-bold capitalize transition-all cursor-pointer ${
                  filter === t
                    ? 'bg-white dark:bg-slate-800 text-slate-800 dark:text-white shadow-sm'
                    : 'text-slate-500 hover:text-slate-700 dark:hover:text-slate-400'
                }`}
              >
                {t}
              </button>
            ))}
          </div>

          <div className="flex items-center gap-2">
            <button
              onClick={() => toggleSort('line')}
              className={`flex items-center gap-1 px-3 py-1.5 rounded-xl border border-slate-200/60 dark:border-slate-800/60 text-xs font-semibold hover:bg-slate-50 dark:hover:bg-slate-800 transition-all cursor-pointer ${
                sortBy === 'line' ? 'text-indigo-600 dark:text-indigo-400 border-indigo-200 dark:border-indigo-850' : 'text-slate-600 dark:text-slate-400'
              }`}
            >
              <ArrowUpDown size={12} />
              <span>Line No</span>
            </button>

            <button
              onClick={() => toggleSort('severity')}
              className={`flex items-center gap-1 px-3 py-1.5 rounded-xl border border-slate-200/60 dark:border-slate-800/60 text-xs font-semibold hover:bg-slate-50 dark:hover:bg-slate-800 transition-all cursor-pointer ${
                sortBy === 'severity' ? 'text-indigo-600 dark:text-indigo-400 border-indigo-200 dark:border-indigo-850' : 'text-slate-600 dark:text-slate-400'
              }`}
            >
              <ArrowUpDown size={12} />
              <span>Severity</span>
            </button>
          </div>
        </div>

        {/* List Content */}
        <div className="flex-1 overflow-y-auto min-h-[300px] border border-slate-100 dark:border-slate-800/60 rounded-2xl bg-slate-50/20 dark:bg-slate-950/10 p-2">
          {sorted.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-20 text-slate-400 italic text-sm">
              <Check className="text-emerald-500 mb-2" size={24} />
              <span>No syntax issues or warnings found in this filter range!</span>
            </div>
          ) : (
            <div className="flex flex-col gap-1.5">
              {sorted.map((diag, index) => {
                const styles = getSeverityStyles(diag.severity);
                return (
                  <div
                    key={index}
                    onClick={() => {
                      onSelectDiagnostic(diag);
                      onClose();
                    }}
                    className={`flex items-start justify-between gap-4 p-3 rounded-xl border border-slate-100 dark:border-slate-800/40 bg-white dark:bg-slate-900 cursor-pointer ${styles.bg} transition-all duration-150`}
                  >
                    <div className="flex-1 flex gap-3">
                      <div className="mt-0.5">{styles.icon}</div>
                      <div className="flex flex-col gap-0.5">
                        <div className="flex items-center gap-2">
                          <span className="text-xs font-bold text-slate-900 dark:text-white">
                            Line {diag.line}
                          </span>
                          <span className={`text-[10px] px-1.5 py-0.2 rounded border font-bold ${styles.badge}`}>
                            {diag.code}
                          </span>
                        </div>
                        <p className="text-xs text-slate-600 dark:text-slate-300 font-medium">
                          {diag.message}
                        </p>
                        {diag.suggestion && (
                          <div className="mt-1 flex items-center gap-1.5 font-mono text-[10px] text-indigo-500 bg-indigo-500/5 dark:bg-indigo-950/20 border border-indigo-500/10 px-2 py-0.5 rounded-lg w-fit">
                            <span className="text-slate-450 uppercase font-semibold text-[8px]">Suggestion:</span>
                            <span className="font-semibold">{diag.suggestion}</span>
                          </div>
                        )}
                      </div>
                    </div>
                    <span className="text-[10px] text-slate-400 font-semibold italic select-none mt-1">
                      Click to jump
                    </span>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* Footer Actions */}
        <div className="pt-4 border-t border-slate-100 dark:border-slate-800/60 flex items-center justify-between gap-4 select-none">
          <span className="text-xs text-slate-400 dark:text-slate-500">
            Clicking an error automatically scrolls editor to that location.
          </span>
          <div className="flex items-center gap-2">
            <button
              onClick={handleCopyAll}
              disabled={diagnostics.length === 0}
              className="flex items-center gap-1.5 py-2 px-3.5 rounded-xl border border-slate-200 dark:border-slate-850 text-slate-700 dark:text-slate-350 hover:bg-slate-50 dark:hover:bg-slate-800 font-semibold text-xs active:scale-95 transition-all cursor-pointer disabled:opacity-50"
            >
              {copiedAll ? <Check size={13} className="text-emerald-500" /> : <Copy size={13} />}
              <span>{copiedAll ? 'Copied!' : 'Copy All'}</span>
            </button>

            <button
              onClick={handleCopyAiPrompt}
              disabled={diagnostics.length === 0}
              className="flex items-center gap-1.5 py-2 px-4 rounded-xl bg-gradient-to-r from-indigo-600 to-violet-600 hover:from-indigo-500 hover:to-violet-500 text-white font-semibold text-xs shadow-md shadow-indigo-100 dark:shadow-none active:scale-95 transition-all cursor-pointer disabled:opacity-50"
            >
              {copiedAiPrompt ? <Check size={13} className="text-emerald-300" /> : <Sparkles size={13} />}
              <span>{copiedAiPrompt ? 'Prompt Copied!' : 'Send to AI Agent'}</span>
            </button>
          </div>
        </div>
      </div>
    </div>
  );
};

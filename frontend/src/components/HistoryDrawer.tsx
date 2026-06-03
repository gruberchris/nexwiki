import React, { useState, useEffect } from 'react';
import { Clock, RotateCcw, Eye, ArrowLeft, Loader2, Calendar, FileText, Sparkles } from 'lucide-react';
import { DiffView } from './DiffView';
import type { Article } from '../types';

interface HistoryDrawerProps {
  slug: string;
  onClose: () => void;
  onRevertComplete: () => void;
  currentContent: string;
  currentTitle: string;
}

export const HistoryDrawer: React.FC<HistoryDrawerProps> = ({
  slug,
  onClose,
  onRevertComplete,
  currentContent,
  currentTitle,
}) => {
  const [history, setHistory] = useState<Article[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [selectedVersion, setSelectedVersion] = useState<Article | null>(null);
  const [isReverting, setIsReverting] = useState(false);
  const [layoutMode, setLayoutMode] = useState<'split' | 'unified'>('split');

  // Load article revision history
  useEffect(() => {
    const loadHistory = async () => {
      try {
        const res = await fetch(`/api/articles/${slug}/history`);
        if (!res.ok) {
          console.error('Error fetching version logs: Failed to load version logs');
          setHistory([]);
        } else {
          const data = await res.json();
          setHistory(data || []);
        }
      } catch (err) {
        console.error('Error fetching version logs:', err);
      } finally {
        setIsLoading(false);
      }
    };
    void loadHistory();
  }, [slug]);

  // Fetch full details of a specific version
  const handleViewVersion = async (version: number) => {
    setIsLoading(true);
    try {
      const res = await fetch(`/api/articles/${slug}/history/${version}`);
      if (!res.ok) {
        alert('Failed to load historical content');
      } else {
        const data = await res.json();
        setSelectedVersion(data);
      }
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Error loading historical version');
    } finally {
      setIsLoading(false);
    }
  };

  // Revert active article to selected version
  const handleRevert = async (version: number) => {
    const confirm = window.confirm(`Are you absolutely sure you want to revert this document back to version ${version}?\nThis will create a new version of the article.`);
    if (!confirm) return;

    setIsReverting(true);
    try {
      const res = await fetch(`/api/articles/${slug}/revert`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ version }),
      });

      if (!res.ok) {
        alert('Server error: failed to perform revert');
        setIsReverting(false);
      } else {
        onRevertComplete();
      }
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Revert failed');
      setIsReverting(false);
    }
  };

  const formatDate = (dateStr?: string) => {
    if (!dateStr) return '';
    return new Date(dateStr).toLocaleString(undefined, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  };

  return (
    <div className="fixed inset-0 z-50 flex justify-end select-none">
      
      {/* Backdrop overlay */}
      <div 
        onClick={onClose}
        className="absolute inset-0 bg-slate-900/40 dark:bg-slate-950/60 backdrop-blur-sm animate-fade-in"
      />

      {/* Drawer Panel Container */}
      <div className="relative w-full max-w-[700px] h-screen bg-slate-50 dark:bg-slate-900 border-l border-slate-200/80 dark:border-slate-800/80 shadow-2xl z-50 flex flex-col p-6 animate-slide-in">
        
        {/* Drawer Header */}
        <div className="flex items-center justify-between pb-4 border-b border-slate-200/80 dark:border-slate-800/80">
          <div className="flex items-center gap-2">
            <Clock className="text-indigo-600 dark:text-emerald-400" size={20} />
            <h2 className="text-base font-extrabold text-slate-900 dark:text-white tracking-tight uppercase">
              Revision Timeline
            </h2>
          </div>
          <button 
            onClick={onClose} 
            className="p-1.5 rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800 text-slate-400 hover:text-slate-600 dark:hover:text-slate-300 text-sm font-semibold transition-all active:scale-90"
          >
            &times; Close
          </button>
        </div>

        {isLoading ? (
          /* Loading Skeleton */
          <div className="flex-1 flex flex-col items-center justify-center text-slate-400">
            <Loader2 className="animate-spin text-indigo-500 mb-2" size={32} />
            <span className="text-xs font-semibold font-mono">Loading revision states...</span>
          </div>
        ) : selectedVersion ? (
          /* ================= COMPARE VERSION DIFF VIEW ================= */
          <div className="flex-1 flex flex-col overflow-hidden py-4 space-y-4 select-text">
            
            {/* Navigation back */}
            <button 
              onClick={() => setSelectedVersion(null)}
              className="flex items-center gap-1 text-xs text-indigo-600 dark:text-indigo-400 font-bold hover:underline transition-all select-none self-start"
            >
              <ArrowLeft size={13} />
              <span>Return to version timeline</span>
            </button>
            
            {/* Version revision information banner */}
            <div className="p-4 rounded-2xl bg-white dark:bg-slate-950 border border-slate-200/50 dark:border-slate-850 shadow-sm flex items-center justify-between gap-4 select-none">
              <div className="space-y-1">
                <div className="flex items-center gap-2">
                  <span className="px-2 py-0.5 rounded-md bg-indigo-500/10 text-indigo-600 dark:text-indigo-400 text-[10px] font-extrabold font-mono uppercase">
                    Version {selectedVersion.version}
                  </span>
                  <span className="text-[10px] text-slate-400 font-semibold tracking-wide uppercase flex items-center gap-1">
                    <Calendar size={10} />
                    {formatDate(selectedVersion.updated_at)}
                  </span>
                </div>
                <div className="text-xs font-medium text-slate-700 dark:text-slate-300 mt-1">
                  Summary: <span className="italic font-normal text-slate-500 dark:text-slate-400">"{selectedVersion.edit_summary}"</span>
                </div>
              </div>

              {/* Action revert button */}
              {selectedVersion.version !== history[0]?.version ? (
                <button
                  onClick={() => handleRevert(selectedVersion.version!)}
                  disabled={isReverting}
                  className="flex items-center gap-1.5 py-2 px-4 rounded-xl bg-gradient-to-r from-emerald-600 to-teal-600 hover:from-emerald-500 hover:to-teal-500 active:scale-95 text-white font-bold text-xs shadow-md shadow-emerald-100 dark:shadow-none transition-all disabled:opacity-50"
                >
                  <RotateCcw size={12} className={isReverting ? 'animate-spin' : ''} />
                  <span>{isReverting ? 'Reverting...' : 'Revert to this version'}</span>
                </button>
              ) : (
                <span className="px-3.5 py-2 rounded-xl bg-indigo-500/10 text-indigo-600 dark:text-indigo-400 text-xs font-bold font-sans">
                  Active Version
                </span>
              )}
            </div>

            {/* Render Visual Diff Engine */}
            <div className="flex-1 overflow-hidden min-h-[300px]">
              <DiffView 
                oldContent={selectedVersion.content || ''} 
                newContent={currentContent} 
                oldTitle={`v${selectedVersion.version} (${selectedVersion.title})`} 
                newTitle={`Active (${currentTitle})`}
                layoutMode={layoutMode}
                onLayoutChange={setLayoutMode}
              />
            </div>
          </div>
        ) : (
          /* ================= TIMELINE LIST VIEW ================= */
          <div className="flex-1 overflow-y-auto py-4 space-y-3 pr-1 select-text">
            {history.length === 0 ? (
              <div className="h-full flex flex-col items-center justify-center text-slate-400 py-12 select-none">
                <FileText className="text-slate-300 dark:text-slate-700 mb-3 animate-pulse" size={40} />
                <h3 className="text-sm font-bold text-slate-700 dark:text-slate-350">No revision history found</h3>
                <p className="text-[10px] text-slate-400 mt-1 max-w-[250px] text-center">Version history tracking will commence upon editing this document.</p>
              </div>
            ) : (
              history.map((h, index) => {
                const isLatest = index === 0;
                return (
                  <div 
                    key={h.version} 
                    className={`relative p-4 rounded-2xl border transition-all flex items-center justify-between gap-4 ${
                      isLatest
                        ? 'border-indigo-500/30 dark:border-emerald-500/20 bg-indigo-500/[0.02] dark:bg-emerald-500/[0.01]'
                        : 'border-slate-200/80 dark:border-slate-800 bg-white dark:bg-slate-950/20'
                    } hover:border-indigo-500/30 dark:hover:border-indigo-400/20 hover:scale-[1.01] shadow-sm`}
                  >
                    {/* Visual left timeline bullet line */}
                    {isLatest && (
                      <div className="absolute top-0 bottom-0 left-0 w-1 bg-gradient-to-b from-indigo-500 to-indigo-600 dark:from-emerald-400 dark:to-teal-500 rounded-l-2xl" />
                    )}

                    <div className="space-y-1.5">
                      <div className="flex flex-wrap items-center gap-2 select-none">
                        <span className="px-2 py-0.5 rounded-md bg-indigo-600/10 dark:bg-indigo-400/10 text-indigo-700 dark:text-indigo-400 text-[10px] font-extrabold font-mono uppercase">
                          v{h.version}
                        </span>
                        
                        {isLatest && (
                          <span className="px-1.5 py-0.5 rounded bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 text-[8px] font-bold tracking-wide uppercase flex items-center gap-0.5">
                            <Sparkles size={8} /> Active
                          </span>
                        )}

                        <span className="text-[9px] text-slate-400 font-semibold tracking-wide uppercase flex items-center gap-0.5">
                          <Calendar size={9} />
                          {formatDate(h.updated_at)}
                        </span>
                      </div>
                      <div className="text-xs text-slate-700 dark:text-slate-350 font-bold leading-tight">
                        {h.edit_summary}
                      </div>
                      {h.title !== currentTitle && (
                        <div className="text-[10px] text-slate-400 font-medium">
                          Historical Title: <span className="font-semibold text-slate-500">{h.title}</span>
                        </div>
                      )}
                    </div>

                    {/* Actions panel */}
                    <div className="flex items-center gap-1.5 shrink-0 select-none">
                      <button
                        onClick={() => handleViewVersion(h.version!)}
                        className="flex items-center gap-1 py-1.5 px-3 rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 hover:bg-slate-55 dark:hover:bg-slate-800 text-indigo-600 dark:text-indigo-400 font-bold text-[10px] active:scale-95 transition-all shadow-sm"
                        title="Compare Diffs"
                      >
                        <Eye size={11} />
                        <span>Inspect</span>
                      </button>
                      {h.version !== undefined && h.version > 1 && (
                        <button
                          onClick={() => handleRevert(h.version! - 1)}
                          className="flex items-center gap-1 py-1.5 px-3 rounded-lg bg-emerald-50 dark:bg-emerald-950/20 hover:bg-emerald-100 dark:hover:bg-emerald-950/40 text-emerald-600 dark:text-emerald-400 font-bold text-[10px] active:scale-95 transition-all"
                          title={`Revert to v${h.version - 1}`}
                        >
                          <RotateCcw size={11} />
                          <span>Revert</span>
                        </button>
                      )}
                    </div>

                  </div>
                );
              })
            )}
          </div>
        )}
        
      </div>
    </div>
  );
};

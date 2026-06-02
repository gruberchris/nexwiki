import React, { useEffect, useRef } from 'react';
import { X, Sparkles, Terminal, Activity, ArrowRight, User } from 'lucide-react';
import { useSSE } from '../hooks/useSSE';
import { useEscapeKey } from '../hooks/useEscapeKey';

interface ActivityLogDrawerProps {
  isOpen: boolean;
  onClose: () => void;
  onNavigate: (slug: string) => void;
}

export const ActivityLogDrawer: React.FC<ActivityLogDrawerProps> = ({
  isOpen,
  onClose,
  onNavigate,
}) => {
  const { activityLog, isConnected } = useSSE();
  const drawerRef = useRef<HTMLDivElement>(null);

  // Close on Escape key
  useEscapeKey(isOpen, onClose);

  // Handle outside click to close
  useEffect(() => {
    const handleOutsideClick = (e: MouseEvent) => {
      if (isOpen && drawerRef.current && !drawerRef.current.contains(e.target as Node)) {
        onClose();
      }
    };
    window.addEventListener('mousedown', handleOutsideClick);
    return () => window.removeEventListener('mousedown', handleOutsideClick);
  }, [isOpen, onClose]);

  const getActionBadge = (action: string) => {
    switch (action.toLowerCase()) {
      case 'create':
        return 'bg-emerald-500/10 border-emerald-500/20 text-emerald-600 dark:text-emerald-450';
      case 'edit':
      case 'revert':
        return 'bg-amber-500/10 border-amber-500/20 text-amber-600 dark:text-amber-450';
      case 'delete':
        return 'bg-rose-500/10 border-rose-500/20 text-rose-600 dark:text-rose-450';
      case 'read':
      default:
        return 'bg-blue-500/10 border-blue-500/20 text-blue-600 dark:text-blue-450';
    }
  };

  const getSourceIcon = (source: string) => {
    if (source.toLowerCase() === 'mcp') {
      return (
        <span
          className="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-full font-bold bg-indigo-500/10 border border-indigo-550/20 text-indigo-650 dark:text-indigo-400 animate-pulse-subtle shadow-sm select-none"
          title="Model Context Protocol AI Agent Tool Execution"
        >
          <Terminal size={10} />
          <span>MCP</span>
        </span>
      );
    }
    return (
      <span
        className="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-full font-bold bg-slate-500/10 border border-slate-550/20 text-slate-650 dark:text-slate-400 shadow-sm select-none"
        title="REST Client Web Interface Operation"
      >
        <User size={10} />
        <span>REST API</span>
      </span>
    );
  };

  return (
    <>
      {/* Dark overlay backdrop */}
      {isOpen && (
        <div className="fixed inset-0 z-40 bg-slate-900/20 dark:bg-slate-950/40 backdrop-blur-[2px] transition-all animate-fade-in" />
      )}

      {/* Slide-in Drawer Container */}
      <div
        ref={drawerRef}
        className={`fixed top-0 right-0 h-screen w-full max-w-md z-50 glass-panel bg-white/90 dark:bg-slate-900/90 border-l border-slate-200/50 dark:border-slate-800/50 shadow-2xl flex flex-col transition-transform duration-300 ease-out transform ${
          isOpen ? 'translate-x-0' : 'translate-x-full'
        }`}
      >
        {/* Header */}
        <div className="p-5 border-b border-slate-100 dark:border-slate-800/60 flex items-center justify-between select-none">
          <div className="flex items-center gap-2.5">
            <div className="p-2 rounded-xl bg-indigo-500/10 dark:bg-indigo-950/30 text-indigo-600 dark:text-indigo-400">
              <Activity size={18} className="animate-pulse" />
            </div>
            <div>
              <h2 className="text-sm font-extrabold text-slate-900 dark:text-white uppercase tracking-wider">
                Live Activity Log
              </h2>
              <div className="flex items-center gap-1.5 mt-0.5">
                <span
                  className={`w-2 h-2 rounded-full ${
                    isConnected ? 'bg-emerald-500 animate-ping-slow' : 'bg-rose-500'
                  }`}
                />
                <span className="text-[10px] font-semibold text-slate-400 dark:text-slate-550">
                  {isConnected ? 'Real-time SSE Stream connected' : 'Disconnected, reconnecting...'}
                </span>
              </div>
            </div>
          </div>
          <button
            onClick={onClose}
            className="p-1.5 rounded-xl border border-slate-200/60 dark:border-slate-800/60 text-slate-500 hover:text-slate-850 dark:hover:text-white bg-slate-50 dark:bg-slate-800 hover:bg-slate-100 dark:hover:bg-slate-700/60 transition-all cursor-pointer"
          >
            <X size={15} />
          </button>
        </div>

        {/* Scrollable Events Queue */}
        <div className="flex-1 overflow-y-auto p-5 space-y-4">
          {!isConnected && activityLog.length === 0 ? (
            // Shimmer skeletons for loading state
            <div className="space-y-4 select-none">
              {[1, 2, 3, 4, 5].map((idx) => (
                <div
                  key={idx}
                  className="p-4 rounded-2xl border border-slate-100 dark:border-slate-800/30 bg-slate-50/40 dark:bg-slate-950/20 flex flex-col gap-2.5 animate-pulse"
                >
                  <div className="flex items-center justify-between">
                    <div className="w-16 h-4 bg-slate-200 dark:bg-slate-800 rounded-full" />
                    <div className="w-24 h-4 bg-slate-200 dark:bg-slate-800 rounded-full" />
                  </div>
                  <div className="w-3/4 h-5 bg-slate-200 dark:bg-slate-800 rounded-lg" />
                  <div className="w-1/2 h-3.5 bg-slate-200 dark:bg-slate-800 rounded-md" />
                </div>
              ))}
            </div>
          ) : activityLog.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-24 text-slate-400 dark:text-slate-500 italic text-xs select-none">
              <Sparkles className="text-indigo-400 mb-2 animate-bounce-slow" size={24} />
              <span>No activity has been captured yet.</span>
              <span className="text-[10px] mt-0.5">Use the wiki or MCP client to trigger events!</span>
            </div>
          ) : (
            <div className="flex flex-col gap-3">
              {activityLog.map((event) => (
                <div
                  key={event.id}
                  className="p-4 rounded-2xl border border-slate-100 dark:border-slate-800/30 bg-slate-50/20 dark:bg-slate-950/10 flex flex-col gap-2 hover:bg-slate-50/40 dark:hover:bg-slate-950/20 hover:border-slate-200/50 dark:hover:border-slate-850/50 transition-all duration-150 relative group"
                >
                  {/* Category sources */}
                  <div className="flex items-center justify-between select-none">
                    <div className="flex items-center gap-1.5">
                      {getSourceIcon(event.source)}
                      <span className={`text-[10px] px-2 py-0.2 rounded border font-bold uppercase tracking-wider ${getActionBadge(event.action)}`}>
                        {event.action}
                      </span>
                    </div>
                    <span className="text-[9px] font-semibold text-slate-400 dark:text-slate-550">
                      {new Date(event.timestamp).toLocaleTimeString([], {
                        hour: '2-digit',
                        minute: '2-digit',
                        second: '2-digit',
                      })}
                    </span>
                  </div>

                  {/* Body & Navigation link */}
                  <div className="flex flex-col gap-1">
                    {event.slug && event.action.toLowerCase() !== 'delete' ? (
                      <button
                        onClick={() => {
                          onNavigate(event.slug);
                          onClose();
                        }}
                        className="text-left font-bold text-xs text-slate-900 dark:text-white hover:text-indigo-600 dark:hover:text-indigo-400 flex items-center gap-1 transition-colors cursor-pointer group-hover:translate-x-0.5 duration-200"
                      >
                        <span>{event.title || event.slug}</span>
                        <ArrowRight size={10} className="opacity-0 group-hover:opacity-100 transition-opacity" />
                      </button>
                    ) : (
                      <span className="font-bold text-xs text-slate-900 dark:text-white">
                        {event.title || event.slug || 'System Operation'}
                      </span>
                    )}
                    
                    {event.tool && (
                      <span className="font-mono text-[9px] bg-slate-100 dark:bg-slate-950 px-1.5 py-0.5 rounded border border-slate-150 dark:border-slate-850 text-indigo-500 w-fit select-all font-semibold">
                        {event.tool}
                      </span>
                    )}
                  </div>

                  {/* Operator Info */}
                  <div className="flex items-center gap-1 text-[10px] text-slate-400 dark:text-slate-500 select-none">
                    <span className="font-semibold text-slate-450 uppercase text-[8px]">Operator:</span>
                    <span className="font-bold text-slate-500 dark:text-slate-400">{event.agent}</span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="p-4 border-t border-slate-100 dark:border-slate-800/60 text-center text-[10px] text-slate-400 dark:text-slate-550 select-none font-medium">
          Captured circular cache of last 200 operations.
        </div>
      </div>
    </>
  );
};

import React, { useRef, useState, useMemo } from 'react';
import { X, Sparkles, Terminal, Activity, ArrowRight, User, Search, Cpu } from 'lucide-react';
import { useSSE } from '../hooks/useSSE';
import { useEscapeKey } from '../hooks/useEscapeKey';
import { useClickOutside } from '../hooks/useClickOutside';
import { matchesLogEvent, getAutocompleteSearchTerm } from '../filterUtils';
import { ActivityFilterHelpModal } from './ActivityFilterHelpModal';
import { FilterInput } from './FilterInput';

interface ActivityLogDrawerProps {
  isOpen: boolean;
  onClose: () => void;
  onNavigate: (slug: string) => void;
}

type SourceFilter = 'all' | 'api' | 'mcp';

export const ActivityLogDrawer: React.FC<ActivityLogDrawerProps> = ({
  isOpen,
  onClose,
  onNavigate,
}) => {
  const { activityLog, isConnected } = useSSE();
  const [activeSource, setActiveSource] = useState<SourceFilter>('all');
  const [searchQuery, setSearchQuery] = useState('');
  const [showFilterHelp, setShowFilterHelp] = useState(false);
  const drawerRef = useRef<HTMLDivElement>(null);

  // Get search term without a "!" prefix for autocomplete suggestions
  const autocompleteTerm = useMemo(() => getAutocompleteSearchTerm(searchQuery), [searchQuery]);

  const logSuggestions = useMemo(() => {
    if (!autocompleteTerm) return [];
    
    const actions = new Set<string>();
    const tools = new Set<string>();
    const agents = new Set<string>();
    const sources = new Set<string>();

    activityLog.forEach(event => {
      // Search in all fields without the ! prefix
      if (event.action && event.action.toLowerCase().includes(autocompleteTerm.toLowerCase())) {
        actions.add(event.action);
      }
      if (event.tool && event.tool.toLowerCase().includes(autocompleteTerm.toLowerCase())) {
        tools.add(event.tool);
      }
      if (event.agent && event.agent.toLowerCase().includes(autocompleteTerm.toLowerCase())) {
        agents.add(event.agent);
      }
      if (event.source && event.source.toLowerCase().includes(autocompleteTerm.toLowerCase())) {
        sources.add(event.source);
      }
    });

    const actionResults = Array.from(actions).map(val => ({ type: 'action', value: val }));
    const toolResults = Array.from(tools).map(val => ({ type: 'tool', value: val }));
    const agentResults = Array.from(agents).map(val => ({ type: 'agent', value: val }));
    const sourceResults = Array.from(sources).map(val => ({ type: 'source', value: val }));

    return [...actionResults, ...toolResults, ...agentResults, ...sourceResults].slice(0, 8);
  }, [autocompleteTerm, activityLog]);

  // Close on Escape key
  useEscapeKey(isOpen, onClose);

  // Handle outside click to close
  useClickOutside(drawerRef, onClose, isOpen);

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

  const filteredLog = activityLog.filter((event) => {
    const sourceMatch = activeSource === 'all' || event.source === activeSource;
    const queryMatch = matchesLogEvent(event, searchQuery);
    return sourceMatch && queryMatch;
  });

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

        {/* Filters Section */}
        <div className="px-5 py-4 border-b border-slate-100 dark:border-slate-800/40 bg-slate-50/30 dark:bg-slate-950/10 space-y-3 relative z-20">
          <div className="flex items-center gap-2">
            <button
              onClick={() => setActiveSource('all')}
              className={`flex-1 flex items-center justify-center gap-1.5 py-1.5 rounded-lg text-[10px] font-bold border transition-all ${
                activeSource === 'all'
                  ? 'bg-indigo-500/10 border-indigo-500/30 text-indigo-600 dark:text-indigo-400 shadow-sm ring-1 ring-indigo-500/20'
                  : 'bg-white dark:bg-slate-900 border-slate-200 dark:border-slate-800 text-slate-500 hover:bg-slate-50 dark:hover:bg-slate-850'
              }`}
            >
              <Activity size={12} />
              <span>All</span>
            </button>
            <button
              onClick={() => setActiveSource('api')}
              className={`flex-1 flex items-center justify-center gap-1.5 py-1.5 rounded-lg text-[10px] font-bold border transition-all ${
                activeSource === 'api'
                  ? 'bg-slate-500/10 border-slate-500/30 text-slate-600 dark:text-slate-400 shadow-sm ring-1 ring-slate-500/20'
                  : 'bg-white dark:bg-slate-900 border-slate-200 dark:border-slate-800 text-slate-500 hover:bg-slate-50 dark:hover:bg-slate-850'
              }`}
            >
              <User size={12} />
              <span>REST API</span>
            </button>
            <button
              onClick={() => setActiveSource('mcp')}
              className={`flex-1 flex items-center justify-center gap-1.5 py-1.5 rounded-lg text-[10px] font-bold border transition-all ${
                activeSource === 'mcp'
                  ? 'bg-indigo-500/10 border-indigo-500/30 text-indigo-600 dark:text-indigo-400 shadow-sm ring-1 ring-indigo-500/20'
                  : 'bg-white dark:bg-slate-900 border-slate-200 dark:border-slate-800 text-slate-500 hover:bg-slate-50 dark:hover:bg-slate-850'
              }`}
            >
              <Cpu size={12} />
              <span>MCP Server</span>
            </button>
          </div>

          <FilterInput
            value={searchQuery}
            onChange={setSearchQuery}
            suggestions={logSuggestions}
            placeholder="Filter by action, agent, tool..."
            onOpenHelp={() => setShowFilterHelp(true)}
            inputClassName="bg-themeBgSecondary shadow-sm"
          />
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
          ) : filteredLog.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-24 text-slate-400 dark:text-slate-500 italic text-xs select-none">
              <Search className="text-slate-300 dark:text-slate-700 mb-2" size={24} />
              <span>No activity matches your filters.</span>
              <button
                onClick={() => {
                  setActiveSource('all');
                  setSearchQuery('');
                }}
                className="text-[10px] mt-2 text-indigo-500 hover:underline cursor-pointer"
              >
                Clear all filters
              </button>
            </div>
          ) : (
            <div className="flex flex-col gap-3">
              {filteredLog.map((event) => (
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

        {showFilterHelp && <ActivityFilterHelpModal onClose={() => setShowFilterHelp(false)} />}
      </div>
    </>
  );
};

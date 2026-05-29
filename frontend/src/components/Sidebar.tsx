import React, { useState } from 'react';
import type { Article } from '../types';
import { 
  FileText, 
  Plus, 
  Search, 
  Moon, 
  Sun, 
  Layers, 
  Clock, 
  ChevronRight,
  BookOpen
} from 'lucide-react';

interface SidebarProps {
  articles: Article[];
  currentSlug: string;
  darkMode: boolean;
  onToggleDarkMode: () => void;
  onNavigate: (slug: string) => void;
  onCreateNew: () => void;
  wikiName: string;
}

// Utility to format relative timestamps cleanly
export function formatRelativeTime(dateStr: string): string {
  const d = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - d.getTime();
  const diffSec = Math.floor(diffMs / 1000);
  const diffMin = Math.floor(diffSec / 60);
  const diffHr = Math.floor(diffMin / 60);
  const diffDays = Math.floor(diffHr / 24);

  if (diffSec < 10) return 'Just now';
  if (diffSec < 60) return `${diffSec}s ago`;
  if (diffMin < 60) return `${diffMin}m ago`;
  if (diffHr < 24) return `${diffHr}h ago`;
  if (diffDays === 1) return 'Yesterday';
  if (diffDays < 7) return `${diffDays}d ago`;
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
}

export const Sidebar: React.FC<SidebarProps> = ({
  articles,
  currentSlug,
  darkMode,
  onToggleDarkMode,
  onNavigate,
  onCreateNew,
  wikiName
}) => {
  const [filterQuery, setFilterQuery] = useState('');

  // Filter articles based on title search
  const filteredArticles = articles.filter(art =>
    art.title.toLowerCase().includes(filterQuery.toLowerCase()) ||
    art.slug.toLowerCase().includes(filterQuery.toLowerCase())
  );

  return (
    <aside className="w-80 h-screen flex flex-col border-r border-slate-200/80 dark:border-slate-800/80 bg-white/80 dark:bg-slate-900/80 backdrop-blur-md transition-all select-none">
      {/* Branding Header */}
      <div className="p-6 pb-4 flex items-center justify-between">
        <div 
          onClick={() => onNavigate('home')} 
          className="flex items-center gap-3 cursor-pointer group"
        >
          <div className="w-9 h-9 rounded-xl bg-gradient-to-tr from-indigo-500 to-violet-600 dark:from-indigo-600 dark:to-emerald-500 flex items-center justify-center text-white font-bold shadow-md shadow-indigo-200 dark:shadow-none group-hover:scale-105 transition-transform duration-200">
            <BookOpen size={18} />
          </div>
          <div className="overflow-hidden">
            <h1 className="text-base font-black text-slate-800 dark:text-white tracking-tight leading-none truncate max-w-[150px]" title={wikiName}>
              {wikiName}
            </h1>
            <span className="text-[10px] text-slate-400 dark:text-slate-500 font-semibold tracking-widest uppercase">Personal Hub</span>
          </div>
        </div>

        {/* Theme Toggle Button */}
        <button
          onClick={onToggleDarkMode}
          className="p-2 rounded-xl text-slate-500 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800 border border-slate-100 dark:border-slate-800 hover:scale-105 active:scale-95 transition-all duration-200"
          aria-label="Toggle Theme"
        >
          {darkMode ? <Sun size={16} className="text-amber-400" /> : <Moon size={16} className="text-indigo-500" />}
        </button>
      </div>

      {/* Action: Create New Page Button */}
      <div className="px-6 py-2">
        <button
          onClick={onCreateNew}
          className="w-full flex items-center justify-center gap-2 py-3 px-4 rounded-xl bg-gradient-to-r from-indigo-600 to-violet-600 hover:from-indigo-500 hover:to-violet-500 active:scale-[0.98] text-white font-semibold text-sm shadow-lg shadow-indigo-100 dark:shadow-none transition-all duration-200 group"
        >
          <Plus size={16} className="group-hover:rotate-90 transition-transform duration-200" />
          <span>New Wiki Page</span>
        </button>
      </div>

      {/* Local Filter input box */}
      <div className="px-6 py-3">
        <div className="relative">
          <Search size={14} className="absolute left-3.5 top-1/2 -translate-y-1/2 text-slate-400 dark:text-slate-500" />
          <input
            type="text"
            placeholder="Quick search articles..."
            value={filterQuery}
            onChange={(e) => setFilterQuery(e.target.value)}
            className="w-full pl-10 pr-4 py-2 text-xs rounded-xl bg-slate-50 dark:bg-slate-950/60 border border-slate-200/80 dark:border-slate-800 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:focus:ring-emerald-500 focus:bg-white dark:focus:bg-slate-950 text-slate-700 dark:text-slate-300 transition-all placeholder:text-slate-400"
          />
        </div>
      </div>

      {/* Scrollable List Area */}
      <div className="flex-1 overflow-y-auto px-4 pb-6 space-y-4">
        {/* Navigation Section */}
        <div>
          <span className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-widest px-3 flex items-center gap-1.5">
            <Layers size={10} />
            Articles ({filteredArticles.length})
          </span>
          <div className="mt-2 space-y-0.5">
            {filteredArticles.length === 0 ? (
              <div className="p-6 text-center text-xs text-slate-400 dark:text-slate-500 border border-dashed border-slate-200 dark:border-slate-800 rounded-xl">
                No matching articles found
              </div>
            ) : (
              filteredArticles.map((art) => {
                const isActive = currentSlug === art.slug;
                return (
                  <button
                    key={art.slug}
                    onClick={() => onNavigate(art.slug)}
                    className={`w-full group flex items-center justify-between p-3 rounded-xl transition-all duration-150 ${
                      isActive
                        ? 'bg-indigo-50 dark:bg-indigo-950/30 text-indigo-600 dark:text-indigo-400 border border-indigo-100/50 dark:border-indigo-950/20 font-medium'
                        : 'text-slate-600 dark:text-slate-400 hover:bg-slate-50 dark:hover:bg-slate-800/40 hover:text-slate-900 dark:hover:text-white border border-transparent'
                    }`}
                  >
                    <div className="flex items-center gap-3 overflow-hidden">
                      <FileText 
                        size={15} 
                        className={`shrink-0 ${
                          isActive 
                            ? 'text-indigo-500 dark:text-indigo-400' 
                            : 'text-slate-400 dark:text-slate-500 group-hover:text-slate-600 dark:group-hover:text-slate-300'
                        }`} 
                      />
                      <span className="truncate text-sm text-left">{art.title}</span>
                    </div>
                    
                    <div className="flex items-center gap-1 shrink-0">
                      {isActive ? (
                        <ChevronRight size={14} className="animate-pulse" />
                      ) : (
                        <div className="flex items-center gap-1 text-[9px] text-slate-400 dark:text-slate-500 opacity-80 group-hover:opacity-100">
                          <Clock size={9} />
                          <span>{formatRelativeTime(art.updated_at)}</span>
                        </div>
                      )}
                    </div>
                  </button>
                );
              })
            )}
          </div>
        </div>
      </div>

      {/* Sidebar Footer */}
      <div className="p-4 border-t border-slate-100 dark:border-slate-800/60 bg-slate-50/50 dark:bg-slate-950/20 flex items-center justify-between text-[10px] text-slate-400 dark:text-slate-500 font-semibold tracking-wide">
        <span>v1.0.0</span>
        <span className="flex items-center gap-1 text-slate-400">
          <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-ping"></span>
          Single-User Mode
        </span>
      </div>
    </aside>
  );
};

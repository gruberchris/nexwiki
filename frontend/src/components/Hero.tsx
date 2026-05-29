import React, { useState } from 'react';
import type { Article } from '../types';
import { formatRelativeTime } from './Sidebar';
import { 
  BookOpen, 
  Plus, 
  Search, 
  FileText, 
  Clock, 
  ArrowRight,
  Terminal,
  Grid,
  Heart
} from 'lucide-react';

interface HeroProps {
  articles: Article[];
  onNavigate: (slug: string) => void;
  onCreateNew: () => void;
  wikiName: string;
}

export const Hero: React.FC<HeroProps> = ({ articles, onNavigate, onCreateNew, wikiName }) => {
  const [searchQuery, setSearchQuery] = useState('');
  const [ftsQuery, setFtsQuery] = useState('');

  // Filter articles based on home search input
  const filteredArticles = articles.filter(art =>
    art.title.toLowerCase().includes(searchQuery.toLowerCase()) ||
    art.slug.toLowerCase().includes(searchQuery.toLowerCase())
  );

  const handleFtsSearch = (e: React.FormEvent) => {
    e.preventDefault();
    if (ftsQuery.trim()) {
      onNavigate(`search?q=${encodeURIComponent(ftsQuery.trim())}`);
    }
  };

  return (
    <div className="flex-1 overflow-y-auto h-screen bg-slate-50 dark:bg-slate-950/40 p-8 sm:p-12 md:p-16 selection:bg-indigo-500 selection:text-white transition-colors">
      <div className="max-w-4xl mx-auto space-y-12 animate-slide-up">
        
        {/* Sleek Serious-yet-Fun Branding Hero Header */}
        <div className="relative text-center sm:text-left flex flex-col sm:flex-row items-center justify-between gap-6 pb-8 border-b border-slate-200/60 dark:border-slate-800/60">
          <div className="space-y-3">
            <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-indigo-50 dark:bg-indigo-950/30 text-indigo-600 dark:text-emerald-400 font-bold text-xs select-none">
              <Terminal size={12} className="animate-pulse" />
              <span>Personal Knowledge Base</span>
            </div>
            <h1 className="text-4xl md:text-5xl font-extrabold tracking-tight text-slate-900 dark:text-white leading-none">
              Welcome to <span className="bg-gradient-to-r from-indigo-600 via-violet-600 to-indigo-500 dark:from-indigo-400 dark:via-emerald-400 dark:to-teal-500 bg-clip-text text-transparent">{wikiName}</span>
            </h1>
            <p className="text-slate-500 dark:text-slate-400 max-w-xl text-sm leading-relaxed font-medium">
              A minimalist, high-fidelity, single-user workspace backed by local Markdown files. Serious enough to organize your projects, fun enough to enjoy writing.
            </p>
          </div>
          
          <div className="shrink-0 flex flex-col items-center justify-center p-6 rounded-2xl glass-panel text-center select-none w-40 h-40">
            <span className="text-5xl font-black text-indigo-600 dark:text-emerald-400 leading-none">
              {articles.length}
            </span>
            <span className="text-xs font-bold text-slate-400 dark:text-slate-500 mt-2 uppercase tracking-widest">
              Wiki Pages
            </span>
          </div>
        </div>

        {/* Prominent Center Search Bar (Google-Style) */}
        <div className="max-w-2xl mx-auto py-2 select-none">
          <form onSubmit={handleFtsSearch} className="space-y-4">
            <div className="relative group">
              <div className="absolute -inset-0.5 bg-gradient-to-r from-indigo-500 to-violet-600 dark:from-indigo-600 dark:to-emerald-500 rounded-2xl blur opacity-15 group-hover:opacity-25 transition duration-300"></div>
              <div className="relative">
                <Search size={18} className="absolute left-5 top-1/2 -translate-y-1/2 text-slate-400 dark:text-slate-500 group-focus-within:text-indigo-500 dark:group-focus-within:text-emerald-400 transition-colors" />
                <input
                  type="text"
                  placeholder={`Search ${wikiName} for articles, keywords, or topics...`}
                  value={ftsQuery}
                  onChange={(e) => setFtsQuery(e.target.value)}
                  className="w-full pl-12 pr-4 py-4 rounded-2xl bg-white dark:bg-slate-900 border border-slate-200/80 dark:border-slate-800 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:focus:ring-emerald-500 text-sm shadow-md text-slate-700 dark:text-slate-200 placeholder:text-slate-400 dark:placeholder:text-slate-500 transition-all font-medium"
                />
              </div>
            </div>
            
            <div className="flex justify-center gap-3">
              <button
                type="submit"
                className="py-2.5 px-6 rounded-xl bg-slate-200/60 hover:bg-slate-200 dark:bg-slate-900 dark:hover:bg-slate-800 text-slate-700 dark:text-slate-300 hover:text-slate-900 dark:hover:text-white font-bold text-xs shadow-sm hover:scale-[1.01] active:scale-95 transition-all select-none border border-slate-200/30 dark:border-slate-800/40"
              >
                Search {wikiName}
              </button>
              <button
                type="button"
                onClick={() => onNavigate('new?title=Markdown%20Playground')}
                className="py-2.5 px-6 rounded-xl bg-slate-200/60 hover:bg-slate-200 dark:bg-slate-900 dark:hover:bg-slate-800 text-slate-700 dark:text-slate-300 hover:text-slate-900 dark:hover:text-white font-bold text-xs shadow-sm hover:scale-[1.01] active:scale-95 transition-all select-none border border-slate-200/30 dark:border-slate-800/40"
              >
                Draft Sandbox
              </button>
            </div>
          </form>
        </div>

        {/* Dashboard Quick Action Cards */}
        <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-6 select-none">
          <div 
            onClick={() => onNavigate('home')}
            className="p-6 rounded-2xl glass-panel hover:border-indigo-500/30 dark:hover:border-indigo-400/30 hover:scale-[1.02] cursor-pointer group transition-all duration-300"
          >
            <div className="w-10 h-10 rounded-xl bg-indigo-50 dark:bg-indigo-950/40 text-indigo-600 dark:text-indigo-400 flex items-center justify-center group-hover:scale-110 transition-transform duration-200 shadow-inner">
              <BookOpen size={20} />
            </div>
            <h3 className="text-base font-bold text-slate-800 dark:text-slate-200 mt-4 group-hover:text-indigo-600 dark:group-hover:text-indigo-400 transition-colors">
              Read Home Page
            </h3>
            <p className="text-xs text-slate-400 dark:text-slate-500 mt-1.5 leading-relaxed font-medium">
              Return to the main welcoming entry index of your wiki workspace.
            </p>
          </div>

          <div 
            onClick={onCreateNew}
            className="p-6 rounded-2xl glass-panel hover:border-violet-500/30 dark:hover:border-emerald-400/30 hover:scale-[1.02] cursor-pointer group transition-all duration-300"
          >
            <div className="w-10 h-10 rounded-xl bg-violet-50 dark:bg-emerald-950/30 text-violet-600 dark:text-emerald-400 flex items-center justify-center group-hover:scale-110 transition-transform duration-200 shadow-inner">
              <Plus size={20} />
            </div>
            <h3 className="text-base font-bold text-slate-800 dark:text-slate-200 mt-4 group-hover:text-violet-600 dark:group-hover:text-emerald-400 transition-colors">
              Create New Article
            </h3>
            <p className="text-xs text-slate-400 dark:text-slate-500 mt-1.5 leading-relaxed font-medium">
              Start writing a clean, markdown-backed wiki page right now.
            </p>
          </div>

          <div 
            onClick={() => onNavigate('new?title=Markdown%20Playground')}
            className="p-6 rounded-2xl glass-panel hover:border-amber-500/30 dark:hover:border-teal-400/30 hover:scale-[1.02] cursor-pointer group transition-all duration-300 sm:col-span-2 md:col-span-1"
          >
            <div className="w-10 h-10 rounded-xl bg-amber-50 dark:bg-teal-950/30 text-amber-600 dark:text-teal-400 flex items-center justify-center group-hover:scale-110 transition-transform duration-200 shadow-inner">
              <Terminal size={20} />
            </div>
            <h3 className="text-base font-bold text-slate-800 dark:text-slate-200 mt-4 group-hover:text-amber-600 dark:group-hover:text-teal-400 transition-colors">
              Draft Playground
            </h3>
            <p className="text-xs text-slate-400 dark:text-slate-500 mt-1.5 leading-relaxed font-medium">
              Open a dynamic, split-pane markdown sandbox to test images and layouts.
            </p>
          </div>
        </div>

        {/* Dashboard Interactive Search and Card Directory */}
        <div className="space-y-6">
          <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
            <h2 className="text-lg font-bold text-slate-800 dark:text-slate-200 flex items-center gap-2 select-none">
              <Grid size={16} className="text-indigo-500 dark:text-emerald-400" />
              Wiki Index Directory
            </h2>

            {/* Dashboard Search bar */}
            <div className="relative w-full sm:w-80">
              <Search size={14} className="absolute left-3.5 top-1/2 -translate-y-1/2 text-slate-400 dark:text-slate-500" />
              <input
                type="text"
                placeholder="Filter index by title or slug..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="w-full pl-10 pr-4 py-2 text-xs rounded-xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:focus:ring-emerald-500 text-slate-700 dark:text-slate-300 shadow-sm transition-all"
              />
            </div>
          </div>

          {/* Cards Grid */}
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {filteredArticles.length === 0 ? (
              <div className="col-span-2 p-12 text-center border-2 border-dashed border-slate-200 dark:border-slate-800 rounded-2xl select-none">
                <FileText size={32} className="mx-auto text-slate-400 dark:text-slate-600 animate-bounce" />
                <h3 className="text-sm font-bold text-slate-600 dark:text-slate-400 mt-3">No articles match your search</h3>
                <p className="text-xs text-slate-400 dark:text-slate-500 mt-1">Try clearing your filters or create a new wiki page!</p>
                <button
                  onClick={onCreateNew}
                  className="mt-4 inline-flex items-center gap-1.5 py-2 px-4 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-semibold text-xs transition-colors"
                >
                  <Plus size={12} />
                  <span>Create "{searchQuery}"</span>
                </button>
              </div>
            ) : (
              filteredArticles.map((art) => (
                <div
                  key={art.slug}
                  onClick={() => onNavigate(art.slug)}
                  className="p-5 rounded-2xl border border-slate-200/60 dark:border-slate-800/80 bg-white/40 dark:bg-slate-900/20 hover:bg-white dark:hover:bg-slate-900 hover:border-slate-300 dark:hover:border-slate-800 shadow-sm hover:shadow-md cursor-pointer group flex flex-col justify-between min-h-[120px] transition-all duration-200"
                >
                  <div className="space-y-1.5">
                    <h3 className="text-sm font-bold text-slate-800 dark:text-slate-200 group-hover:text-indigo-600 dark:group-hover:text-emerald-400 truncate transition-colors">
                      {art.title}
                    </h3>
                    <div className="font-mono text-[10px] text-slate-400 dark:text-slate-500 truncate">
                      /articles/{art.slug}
                    </div>
                  </div>

                  <div className="flex items-center justify-between border-t border-slate-100 dark:border-slate-800/40 pt-3 mt-4 text-[10px] text-slate-400 dark:text-slate-500 select-none">
                    <div className="flex items-center gap-1">
                      <Clock size={11} />
                      <span>Updated {formatRelativeTime(art.updated_at)}</span>
                    </div>
                    <span className="flex items-center gap-0.5 text-indigo-500 dark:text-emerald-400 font-semibold group-hover:translate-x-1 transition-transform">
                      Open <ArrowRight size={10} />
                    </span>
                  </div>
                </div>
              ))
            )}
          </div>
        </div>

        {/* Dashboard Footer Quote */}
        <div className="pt-8 border-t border-slate-200/50 dark:border-slate-800/40 flex items-center justify-center gap-1.5 text-center text-xs text-slate-400 dark:text-slate-500 select-none font-medium">
          <span>Made with</span>
          <Heart size={12} className="text-rose-500 fill-rose-500 hover:scale-125 transition-transform cursor-pointer" />
          <span>using standard library Go + React. Enjoy coding!</span>
        </div>
      </div>
    </div>
  );
};

import React, { useState } from 'react';
import type { Article } from '../types';
import { formatRelativeTime } from '../utils';
import { 
  BookOpen, 
  Plus, 
  Search, 
  FileText, 
  Clock, 
  ArrowRight,
  Terminal,
  Grid,
  Heart,
  ClipboardList,
  Wrench
} from 'lucide-react';

interface HeroProps {
  articles: Article[];
  onNavigate: (slug: string) => void;
  onCreateNew: (type: 'article' | 'plan' | 'skill') => void;
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

  const handleFtsSearch = (e: React.SyntheticEvent) => {
    e.preventDefault();
    if (ftsQuery.trim()) {
      onNavigate(`search?q=${encodeURIComponent(ftsQuery.trim())}`);
    }
  };

  return (
    <div className="flex-1 overflow-y-auto h-screen bg-themeBgPrimary p-8 sm:p-12 md:p-16 selection:bg-themeAccent selection:text-white transition-colors">
      <div className="max-w-4xl mx-auto space-y-12 animate-slide-up">
        
        {/* Sleek Serious-yet-Fun Branding Hero Header */}
        <div className="relative text-center sm:text-left flex flex-col sm:flex-row items-center justify-between gap-6 pb-8 border-b border-themeBorder">
          <div className="space-y-3">
            <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-themeAccentBg text-themeAccent font-bold text-xs select-none">
              <Terminal size={12} className="animate-pulse" />
              <span>Personal Knowledge Base</span>
            </div>
            <h1 className="text-4xl md:text-5xl font-extrabold tracking-tight text-themeTextPrimary leading-none">
              Welcome to <span className="bg-gradient-to-r from-themeAccent via-themeAccentSecondary to-themeAccent bg-clip-text text-transparent">{wikiName}</span>
            </h1>
            <p className="text-themeTextMuted max-w-xl text-sm leading-relaxed font-medium">
              A minimalist, high-fidelity, single-user workspace backed by local Markdown files. Serious enough to organize your projects, fun enough to enjoy writing.
            </p>
          </div>
          
          <div className="shrink-0 flex flex-col items-center justify-center p-6 rounded-2xl glass-panel text-center select-none w-40 h-40">
            <span className="text-5xl font-black text-themeAccent leading-none">
              {articles.length}
            </span>
            <span className="text-xs font-bold text-themeTextMuted mt-2 uppercase tracking-widest">
              Wiki Pages
            </span>
          </div>
        </div>

        {/* Prominent Center Search Bar (Google-Style) */}
        <div className="max-w-2xl mx-auto py-2 select-none">
          <form onSubmit={handleFtsSearch} className="space-y-4">
            <div className="relative group">
              <div className="absolute -inset-0.5 bg-gradient-to-r from-themeAccent to-themeAccentSecondary rounded-2xl blur opacity-15 group-hover:opacity-25 transition duration-300"></div>
              <div className="relative">
                <Search size={18} className="absolute left-5 top-1/2 -translate-y-1/2 text-themeTextMuted group-focus-within:text-themeAccent transition-colors" />
                <input
                  type="text"
                  placeholder={`Search ${wikiName} for articles, keywords, or topics...`}
                  value={ftsQuery}
                  onChange={(e) => setFtsQuery(e.target.value)}
                  className="w-full pl-12 pr-4 py-4 rounded-2xl bg-themeBgSecondary border border-themeBorder focus:outline-none focus:ring-2 focus:ring-themeAccent text-sm shadow-md text-themeTextSecondary placeholder:text-themeTextMuted transition-all font-medium"
                />
              </div>
            </div>
            
            <div className="flex justify-center gap-3">
              <button
                type="submit"
                className="py-2.5 px-6 rounded-xl bg-themeBgPrimary hover:opacity-90 text-themeTextSecondary font-bold text-xs shadow-sm hover:scale-[1.01] active:scale-95 transition-all select-none border border-themeBorder"
              >
                Search {wikiName}
              </button>
              <button
                type="button"
                onClick={() => onNavigate('new?title=Markdown%20Playground')}
                className="py-2.5 px-6 rounded-xl bg-themeBgPrimary hover:opacity-90 text-themeTextSecondary font-bold text-xs shadow-sm hover:scale-[1.01] active:scale-95 transition-all select-none border border-themeBorder"
              >
                Draft Sandbox
              </button>
            </div>
          </form>
        </div>

        {/* Dashboard Quick Action Cards */}
        <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-6 select-none">
          <div 
            onClick={() => onCreateNew('article')}
            className="p-6 rounded-2xl glass-panel hover:border-themeAccent/30 hover:scale-[1.02] cursor-pointer group transition-all duration-300"
          >
            <div className="w-10 h-10 rounded-xl bg-themeAccentBg text-themeAccent flex items-center justify-center group-hover:scale-110 transition-transform duration-200 shadow-inner">
              <BookOpen size={20} />
            </div>
            <h3 className="text-base font-bold text-themeTextSecondary mt-4 group-hover:text-themeAccent transition-colors">
              Create Wiki Article
            </h3>
            <p className="text-xs text-themeTextMuted mt-1.5 leading-relaxed font-medium">
              Start writing a clean, markdown-backed wiki page right now.
            </p>
          </div>

          <div 
            onClick={() => onCreateNew('plan')}
            className="p-6 rounded-2xl glass-panel hover:border-themeAccentSecondary/30 hover:scale-[1.02] cursor-pointer group transition-all duration-300"
          >
            <div className="w-10 h-10 rounded-xl bg-themeAccentBg text-themeAccentSecondary flex items-center justify-center group-hover:scale-110 transition-transform duration-200 shadow-inner">
              <ClipboardList size={20} />
            </div>
            <h3 className="text-base font-bold text-themeTextSecondary mt-4 group-hover:text-themeAccentSecondary transition-colors">
              Create AI Plan
            </h3>
            <p className="text-xs text-themeTextMuted mt-1.5 leading-relaxed font-medium">
              Create a Collaborative AI Plan to coordinate roadmaps and milestones.
            </p>
          </div>

          <div 
            onClick={() => onCreateNew('skill')}
            className="p-6 rounded-2xl glass-panel hover:border-themeAccent/30 hover:scale-[1.02] cursor-pointer group transition-all duration-300"
          >
            <div className="w-10 h-10 rounded-xl bg-themeAccentBg text-themeAccent flex items-center justify-center group-hover:scale-110 transition-transform duration-200 shadow-inner">
              <Wrench size={20} />
            </div>
            <h3 className="text-base font-bold text-themeTextSecondary mt-4 group-hover:text-themeAccent transition-colors">
              Create Custom Skill
            </h3>
            <p className="text-xs text-themeTextMuted mt-1.5 leading-relaxed font-medium">
              Author procedural instructions and rules (SKILL.md) for AI agents.
            </p>
          </div>

          <div 
            onClick={() => onNavigate('new?title=Markdown%20Playground')}
            className="p-6 rounded-2xl glass-panel hover:border-themeAccentSecondary/30 hover:scale-[1.02] cursor-pointer group transition-all duration-300"
          >
            <div className="w-10 h-10 rounded-xl bg-themeAccentBg text-themeAccentSecondary flex items-center justify-center group-hover:scale-110 transition-transform duration-200 shadow-inner">
              <Terminal size={20} />
            </div>
            <h3 className="text-base font-bold text-themeTextSecondary mt-4 group-hover:text-themeAccentSecondary transition-colors">
              Draft Sandbox
            </h3>
            <p className="text-xs text-themeTextMuted mt-1.5 leading-relaxed font-medium">
              Open a dynamic, split-pane markdown sandbox to test images and layouts.
            </p>
          </div>
        </div>

        {/* Dashboard Interactive Search and Card Directory */}
        <div className="space-y-6">
          <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
            <h2 className="text-lg font-bold text-themeTextSecondary flex items-center gap-2 select-none">
              <Grid size={16} className="text-themeAccent" />
              Wiki Index Directory
            </h2>

            {/* Dashboard Filter Search bar */}
            <div className="relative w-full sm:w-80">
              <Search size={14} className="absolute left-3.5 top-1/2 -translate-y-1/2 text-themeTextMuted" />
              <input
                type="text"
                placeholder="Filter index by title or slug..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="w-full pl-10 pr-4 py-2 text-xs rounded-xl bg-themeBgSecondary border border-themeBorder focus:outline-none focus:ring-2 focus:ring-themeAccent text-themeTextSecondary shadow-sm transition-all"
              />
            </div>
          </div>

          {/* Cards Grid */}
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {filteredArticles.length === 0 ? (
              <div className="col-span-2 p-12 text-center border border-dashed border-themeBorder rounded-2xl select-none">
                <FileText size={32} className="mx-auto text-themeTextMuted animate-bounce" />
                <h3 className="text-sm font-bold text-themeTextSecondary mt-3">No articles match your search</h3>
                <p className="text-xs text-themeTextMuted mt-1">Try clearing your filters or create a new wiki page!</p>
                <button
                  onClick={() => onCreateNew('article')}
                  className="mt-4 inline-flex items-center gap-1.5 py-2 px-4 rounded-xl bg-themeAccent hover:opacity-90 text-white font-semibold text-xs transition-colors"
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
                  className="p-5 rounded-2xl border border-themeBorder bg-themeBgSecondary/40 hover:bg-themeBgSecondary hover:border-themeBorder shadow-sm hover:shadow-md cursor-pointer group flex flex-col justify-between min-h-[120px] transition-all duration-200"
                >
                  <div className="space-y-1.5">
                    <h3 className="text-sm font-bold text-themeTextSecondary group-hover:text-themeAccent truncate transition-colors">
                      {art.title}
                    </h3>
                    <div className="font-mono text-[10px] text-themeTextMuted truncate">
                      /articles/{art.slug}
                    </div>
                  </div>

                  <div className="flex items-center justify-between border-t border-themeBorder pt-3 mt-4 text-[10px] text-themeTextMuted select-none">
                    <div className="flex items-center gap-1">
                      <Clock size={11} />
                      <span>Updated {formatRelativeTime(art.updated_at)}</span>
                    </div>
                    <span className="flex items-center gap-0.5 text-themeAccent font-semibold group-hover:translate-x-1 transition-transform">
                      Open <ArrowRight size={10} />
                    </span>
                  </div>
                </div>
              ))
            )}
          </div>
        </div>

        {/* Dashboard Footer Quote */}
        <div className="pt-8 border-t border-themeBorder flex items-center justify-center gap-1.5 text-center text-xs text-themeTextMuted select-none font-medium">
          <span>Made with</span>
          <Heart size={11} className="text-rose-500 fill-rose-500 animate-pulse" />
          <span>for serious and fun brainstorming.</span>
        </div>

      </div>
    </div>
  );
};

import React, { useState, useEffect } from 'react';
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
  Wrench,
  ChevronDown,
  Cpu
} from 'lucide-react';

const MAX_VISIBLE_TAGS = 3;

function getTagPriority(tag: string, statusTags: Set<string>): number {
  const lower = tag.toLowerCase();
  if (statusTags.has(lower)) return 0;
  if (lower.startsWith('aiagent-')) return 2;
  return 1;
}

function sortCardTags(tags: string[], statusTags: Set<string>): string[] {
  return [...tags].sort((a, b) => getTagPriority(a, statusTags) - getTagPriority(b, statusTags));
}

interface ArticleCardProps {
  art: Article;
  onNavigate: (slug: string) => void;
  secondary?: boolean;
  statusTags: Set<string>;
}

function ArticleCard({ art, onNavigate, secondary = false, statusTags }: ArticleCardProps) {
  const accentText = secondary ? 'text-themeAccentSecondary' : 'text-themeAccent';
  const accentHover = secondary ? 'group-hover:text-themeAccentSecondary' : 'group-hover:text-themeAccent';

  return (
    <div
      onClick={() => onNavigate(art.slug)}
      className="p-5 rounded-2xl border border-themeBorder bg-themeBgSecondary/40 hover:bg-themeBgSecondary hover:border-themeBorder shadow-sm hover:shadow-md cursor-pointer group flex flex-col justify-between min-h-[120px] transition-all duration-200"
    >
      <div className="space-y-1.5">
        <h3 className={`text-sm font-bold text-themeTextSecondary ${accentHover} truncate transition-colors`}>
          {art.title}
        </h3>
        {art.tags && art.tags.length > 0 && (
          <div className="flex flex-wrap gap-1">
            {sortCardTags(art.tags, statusTags).slice(0, MAX_VISIBLE_TAGS).map((tag) => {
              const isSystem = tag.toLowerCase().startsWith('aiagent-');
              return (
                <span
                  key={tag}
                  className={isSystem
                    ? "text-[10px] px-1.5 py-0.5 rounded-full bg-themeBgSecondary text-themeTextMuted border border-themeBorder"
                    : "text-[10px] px-1.5 py-0.5 rounded-full bg-themeAccentBg text-themeAccent font-medium"
                  }
                >
                  {tag}
                </span>
              );
            })}
            {art.tags.length > MAX_VISIBLE_TAGS && (
              <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-themeBgSecondary text-themeTextMuted border border-themeBorder">
                +{art.tags.length - MAX_VISIBLE_TAGS} more
              </span>
            )}
          </div>
        )}
      </div>

      <div className="flex items-center justify-between border-t border-themeBorder pt-3 mt-4 text-[10px] text-themeTextMuted select-none">
        <div className="flex items-center gap-1">
          <Clock size={11} />
          <span>Updated {formatRelativeTime(art.updated_at)}</span>
        </div>
        <span className={`flex items-center gap-0.5 ${accentText} font-semibold group-hover:translate-x-1 transition-transform`}>
          Open <ArrowRight size={10} />
        </span>
      </div>
    </div>
  );
}

interface HeroProps {
  articles: Article[];
  onNavigate: (slug: string) => void;
  onCreateNew: (type: 'article' | 'plan' | 'skill') => void;
  wikiName: string;
}

export const Hero: React.FC<HeroProps> = ({ articles, onNavigate, onCreateNew, wikiName }) => {
  const [ftsQuery, setFtsQuery] = useState('');
  const [statusTags, setStatusTags] = useState<Set<string>>(new Set());

  useEffect(() => {
    fetch('/api/status-tags')
      .then(r => r.json())
      .then(data => { if (Array.isArray(data.tags)) setStatusTags(new Set(data.tags)); })
      .catch(() => {});
  }, []);

  // Individual search input states
  const [wikiSearchQuery, setWikiSearchQuery] = useState('');
  const [memoriesSearchQuery, setMemoriesSearchQuery] = useState('');
  const [plansSearchQuery, setPlansSearchQuery] = useState('');
  const [skillsSearchQuery, setSkillsSearchQuery] = useState('');

  // Expansion states
  const [wikiExpanded, setWikiExpanded] = useState(false);
  const [memoriesExpanded, setMemoriesExpanded] = useState(false);
  const [plansExpanded, setPlansExpanded] = useState(false);
  const [skillsExpanded, setSkillsExpanded] = useState(false);

  // Categorize and filter articles into four categories
  const wikiArticles = articles.filter(art => {
    const isAgent = art.tags?.some(tag => tag.toLowerCase().startsWith('aiagent-'));
    return !isAgent;
  });

  const aiMemories = articles.filter(art => {
    const isAgent = art.tags?.some(tag => tag.toLowerCase().startsWith('aiagent-'));
    if (!isAgent) return false;
    const isSkill = art.tags?.some(tag => tag.toLowerCase() === 'aiagent-skill');
    const isPlan = art.tags?.some(tag => tag.toLowerCase() === 'aiagent-plan');
    return !isSkill && !isPlan;
  });

  const aiPlans = articles.filter(art => {
    return art.tags?.some(tag => tag.toLowerCase() === 'aiagent-plan');
  });

  const aiSkills = articles.filter(art => {
    return art.tags?.some(tag => tag.toLowerCase() === 'aiagent-skill');
  });

  // Filtered lists based on category-specific queries
  const filteredWikiArticles = wikiArticles.filter(art => {
    const q = wikiSearchQuery.toLowerCase();
    return art.title.toLowerCase().includes(q) ||
      art.tags?.some(t => t.toLowerCase().includes(q));
  });

  const filteredAiMemories = aiMemories.filter(art => {
    const q = memoriesSearchQuery.toLowerCase();
    return art.title.toLowerCase().includes(q) ||
      art.tags?.some(t => t.toLowerCase().includes(q));
  });

  const filteredAiPlans = aiPlans.filter(art => {
    const q = plansSearchQuery.toLowerCase();
    return art.title.toLowerCase().includes(q) ||
      art.tags?.some(t => t.toLowerCase().includes(q));
  });

  const filteredAiSkills = aiSkills.filter(art => {
    const q = skillsSearchQuery.toLowerCase();
    return art.title.toLowerCase().includes(q) ||
      art.tags?.some(t => t.toLowerCase().includes(q));
  });

  const handleFtsSearch = (e: React.SyntheticEvent) => {
    e.preventDefault();
    if (ftsQuery.trim()) {
      onNavigate(`search?q=${encodeURIComponent(ftsQuery.trim())}`);
    }
  };

  return (
    <div className="flex-1 overflow-y-auto h-screen bg-themeBgPrimary p-8 sm:p-12 md:p-16 selection:bg-themeAccent selection:text-white transition-colors min-w-0">
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
              An elegant, high-fidelity Markdown knowledge base and collaborative AI second brain. Backed by local files with a built-in MCP server. Serious enough to organize your projects, intelligent enough to co-author with AI.
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
              Create Agent Plan
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
              Create Agent Skill
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
        <div className="space-y-10">
          
          {/* Wiki Index Directory */}
          <div className="space-y-6">
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-themeBorder pb-3">
              <button
                onClick={() => setWikiExpanded(!wikiExpanded)}
                className="flex items-center gap-2 text-lg font-bold text-themeTextSecondary hover:text-themeAccent transition-colors select-none group focus:outline-none"
              >
                <ChevronDown 
                  size={18} 
                  className={`transition-transform duration-200 text-themeTextMuted group-hover:text-themeAccent ${wikiExpanded ? 'rotate-0' : '-rotate-90'}`} 
                />
                <Grid size={16} className="text-themeAccent" />
                <span>Wiki Index</span>
                <span className="text-xs bg-themeAccentBg text-themeAccent font-bold px-2.5 py-0.5 rounded-full select-none">
                  {filteredWikiArticles.length}
                </span>
              </button>

              {wikiExpanded && (
                <div className="relative w-full sm:w-80 animate-fade-in">
                  <Search size={14} className="absolute left-3.5 top-1/2 -translate-y-1/2 text-themeTextMuted" />
                  <input
                    type="text"
                    placeholder="Filter articles by title or tag..."
                    value={wikiSearchQuery}
                    onChange={(e) => setWikiSearchQuery(e.target.value)}
                    className="w-full pl-10 pr-4 py-2 text-xs rounded-xl bg-themeBgSecondary border border-themeBorder focus:outline-none focus:ring-2 focus:ring-themeAccent text-themeTextSecondary shadow-sm transition-all"
                  />
                </div>
              )}
            </div>

            {wikiExpanded && (
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4 animate-fade-in">
                {filteredWikiArticles.length === 0 ? (
                  <div className="col-span-2 p-12 text-center border border-dashed border-themeBorder rounded-2xl select-none">
                    <FileText size={32} className="mx-auto text-themeTextMuted animate-bounce" />
                    <h3 className="text-sm font-bold text-themeTextSecondary mt-3">No articles match your search</h3>
                    <p className="text-xs text-themeTextMuted mt-1">Try clearing your filter or create a new wiki page!</p>
                    <button
                      onClick={() => onCreateNew('article')}
                      className="mt-4 inline-flex items-center gap-1.5 py-2 px-4 rounded-xl bg-themeAccent hover:opacity-90 text-white font-semibold text-xs transition-colors cursor-pointer"
                    >
                      <Plus size={12} />
                      <span>Create "{wikiSearchQuery}"</span>
                    </button>
                  </div>
                ) : (
                  filteredWikiArticles.map((art) => (
                    <ArticleCard key={art.slug} art={art} onNavigate={onNavigate} statusTags={statusTags} />
                  ))
                )}
              </div>
            )}
          </div>

          {/* AI Memories Directory */}
          <div className="space-y-6">
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-themeBorder pb-3">
              <button
                onClick={() => setMemoriesExpanded(!memoriesExpanded)}
                className="flex items-center gap-2 text-lg font-bold text-themeTextSecondary hover:text-themeAccent transition-colors select-none group focus:outline-none"
              >
                <ChevronDown 
                  size={18} 
                  className={`transition-transform duration-200 text-themeTextMuted group-hover:text-themeAccent ${memoriesExpanded ? 'rotate-0' : '-rotate-90'}`} 
                />
                <Cpu size={16} className="text-themeAccent" />
                <span>Agent Memories</span>
                <span className="text-xs bg-themeAccentBg text-themeAccent font-bold px-2.5 py-0.5 rounded-full select-none">
                  {filteredAiMemories.length}
                </span>
              </button>

              {memoriesExpanded && (
                <div className="relative w-full sm:w-80 animate-fade-in">
                  <Search size={14} className="absolute left-3.5 top-1/2 -translate-y-1/2 text-themeTextMuted" />
                  <input
                    type="text"
                    placeholder="Filter memories by title or tag..."
                    value={memoriesSearchQuery}
                    onChange={(e) => setMemoriesSearchQuery(e.target.value)}
                    className="w-full pl-10 pr-4 py-2 text-xs rounded-xl bg-themeBgSecondary border border-themeBorder focus:outline-none focus:ring-2 focus:ring-themeAccent text-themeTextSecondary shadow-sm transition-all"
                  />
                </div>
              )}
            </div>

            {memoriesExpanded && (
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4 animate-fade-in">
                {filteredAiMemories.length === 0 ? (
                  <div className="col-span-2 p-12 text-center border border-dashed border-themeBorder rounded-2xl select-none">
                    <Cpu size={32} className="mx-auto text-themeTextMuted animate-bounce" />
                    <h3 className="text-sm font-bold text-themeTextSecondary mt-3">No AI memories found</h3>
                    <p className="text-xs text-themeTextMuted mt-1">Let your AI agent connect and run to generate memories!</p>
                  </div>
                ) : (
                  filteredAiMemories.map((art) => (
                    <ArticleCard key={art.slug} art={art} onNavigate={onNavigate} statusTags={statusTags} />
                  ))
                )}
              </div>
            )}
          </div>

          {/* AI Plans Directory */}
          <div className="space-y-6">
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-themeBorder pb-3">
              <button
                onClick={() => setPlansExpanded(!plansExpanded)}
                className="flex items-center gap-2 text-lg font-bold text-themeTextSecondary hover:text-themeAccentSecondary transition-colors select-none group focus:outline-none"
              >
                <ChevronDown 
                  size={18} 
                  className={`transition-transform duration-200 text-themeTextMuted group-hover:text-themeAccentSecondary ${plansExpanded ? 'rotate-0' : '-rotate-90'}`} 
                />
                <ClipboardList size={16} className="text-themeAccentSecondary" />
                <span>Agent Plans</span>
                <span className="text-xs bg-themeAccentBg text-themeAccentSecondary font-bold px-2.5 py-0.5 rounded-full select-none">
                  {filteredAiPlans.length}
                </span>
              </button>

              {plansExpanded && (
                <div className="relative w-full sm:w-80 animate-fade-in">
                  <Search size={14} className="absolute left-3.5 top-1/2 -translate-y-1/2 text-themeTextMuted" />
                  <input
                    type="text"
                    placeholder="Filter plans by title or tag..."
                    value={plansSearchQuery}
                    onChange={(e) => setPlansSearchQuery(e.target.value)}
                    className="w-full pl-10 pr-4 py-2 text-xs rounded-xl bg-themeBgSecondary border border-themeBorder focus:outline-none focus:ring-2 focus:ring-themeAccentSecondary text-themeTextSecondary shadow-sm transition-all"
                  />
                </div>
              )}
            </div>

            {plansExpanded && (
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4 animate-fade-in">
                {filteredAiPlans.length === 0 ? (
                  <div className="col-span-2 p-12 text-center border border-dashed border-themeBorder rounded-2xl select-none">
                    <ClipboardList size={32} className="mx-auto text-themeTextMuted animate-bounce" />
                    <h3 className="text-sm font-bold text-themeTextSecondary mt-3">No AI plans found</h3>
                    <p className="text-xs text-themeTextMuted mt-1">Create a collaborative AI plan to get started!</p>
                    <button
                      onClick={() => onCreateNew('plan')}
                      className="mt-4 inline-flex items-center gap-1.5 py-2 px-4 rounded-xl bg-themeAccentSecondary hover:opacity-90 text-white font-semibold text-xs transition-colors cursor-pointer"
                    >
                      <Plus size={12} />
                      <span>Create Agent Plan</span>
                    </button>
                  </div>
                ) : (
                  filteredAiPlans.map((art) => (
                    <ArticleCard key={art.slug} art={art} onNavigate={onNavigate} secondary statusTags={statusTags} />
                  ))
                )}
              </div>
            )}
          </div>

          {/* AI Skills Directory */}
          <div className="space-y-6">
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-themeBorder pb-3">
              <button
                onClick={() => setSkillsExpanded(!skillsExpanded)}
                className="flex items-center gap-2 text-lg font-bold text-themeTextSecondary hover:text-themeAccent transition-colors select-none group focus:outline-none"
              >
                <ChevronDown 
                  size={18} 
                  className={`transition-transform duration-200 text-themeTextMuted group-hover:text-themeAccent ${skillsExpanded ? 'rotate-0' : '-rotate-90'}`} 
                />
                <Wrench size={16} className="text-themeAccent" />
                <span>Agent Skills</span>
                <span className="text-xs bg-themeAccentBg text-themeAccent font-bold px-2.5 py-0.5 rounded-full select-none">
                  {filteredAiSkills.length}
                </span>
              </button>

              {skillsExpanded && (
                <div className="relative w-full sm:w-80 animate-fade-in">
                  <Search size={14} className="absolute left-3.5 top-1/2 -translate-y-1/2 text-themeTextMuted" />
                  <input
                    type="text"
                    placeholder="Filter skills by title or tag..."
                    value={skillsSearchQuery}
                    onChange={(e) => setSkillsSearchQuery(e.target.value)}
                    className="w-full pl-10 pr-4 py-2 text-xs rounded-xl bg-themeBgSecondary border border-themeBorder focus:outline-none focus:ring-2 focus:ring-themeAccent text-themeTextSecondary shadow-sm transition-all"
                  />
                </div>
              )}
            </div>

            {skillsExpanded && (
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4 animate-fade-in">
                {filteredAiSkills.length === 0 ? (
                  <div className="col-span-2 p-12 text-center border border-dashed border-themeBorder rounded-2xl select-none">
                    <Wrench size={32} className="mx-auto text-themeTextMuted animate-bounce" />
                    <h3 className="text-sm font-bold text-themeTextSecondary mt-3">No AI skills found</h3>
                    <p className="text-xs text-themeTextMuted mt-1">Create procedural SKILL.md guides for your AI agents!</p>
                    <button
                      onClick={() => onCreateNew('skill')}
                      className="mt-4 inline-flex items-center gap-1.5 py-2 px-4 rounded-xl bg-themeAccent hover:opacity-90 text-white font-semibold text-xs transition-colors cursor-pointer"
                    >
                      <Plus size={12} />
                      <span>Create Agent Skill</span>
                    </button>
                  </div>
                ) : (
                  filteredAiSkills.map((art) => (
                    <ArticleCard key={art.slug} art={art} onNavigate={onNavigate} statusTags={statusTags} />
                  ))
                )}
              </div>
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

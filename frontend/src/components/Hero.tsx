import React, { useState, useEffect } from 'react';
import type { Article } from '../types';
import {
  BookOpen,
  Plus,
  Search,
  FileText,
  Terminal,
  Grid,
  Heart,
  ClipboardList,
  Wrench,
  Cpu,
} from 'lucide-react';
import { matchesFilter } from '../filterUtils';
import { DirectorySection } from './DirectorySection';
import { FilterHelpModal } from './FilterHelpModal';

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

  const [wikiSearchQuery, setWikiSearchQuery] = useState('');
  const [memoriesSearchQuery, setMemoriesSearchQuery] = useState('');
  const [plansSearchQuery, setPlansSearchQuery] = useState('');
  const [skillsSearchQuery, setSkillsSearchQuery] = useState('');
  const [showFilterHelp, setShowFilterHelp] = useState(false);

  const [wikiExpanded, setWikiExpanded] = useState(false);
  const [memoriesExpanded, setMemoriesExpanded] = useState(false);
  const [plansExpanded, setPlansExpanded] = useState(false);
  const [skillsExpanded, setSkillsExpanded] = useState(false);

  const wikiArticles = articles.filter(art =>
    !art.tags?.some(tag => tag.toLowerCase().startsWith('aiagent-'))
  );
  const aiMemories = articles.filter(art => {
    if (!art.tags?.some(tag => tag.toLowerCase().startsWith('aiagent-'))) return false;
    return !art.tags?.some(tag => tag.toLowerCase() === 'aiagent-skill') &&
           !art.tags?.some(tag => tag.toLowerCase() === 'aiagent-plan');
  });
  const aiPlans = articles.filter(art => art.tags?.some(tag => tag.toLowerCase() === 'aiagent-plan'));
  const aiSkills = articles.filter(art => art.tags?.some(tag => tag.toLowerCase() === 'aiagent-skill'));

  const filteredWikiArticles = wikiArticles.filter(art => matchesFilter(art, wikiSearchQuery));
  const filteredAiMemories = aiMemories.filter(art => matchesFilter(art, memoriesSearchQuery));
  const filteredAiPlans = aiPlans.filter(art => matchesFilter(art, plansSearchQuery));
  const filteredAiSkills = aiSkills.filter(art => matchesFilter(art, skillsSearchQuery));

  const handleFtsSearch = (e: React.SyntheticEvent) => {
    e.preventDefault();
    if (ftsQuery.trim()) onNavigate(`search?q=${encodeURIComponent(ftsQuery.trim())}`);
  };

  return (
    <div className="flex-1 overflow-y-auto h-screen bg-themeBgPrimary p-8 sm:p-12 md:p-16 selection:bg-themeAccent selection:text-white transition-colors min-w-0">
      <div className="max-w-4xl mx-auto space-y-12 animate-slide-up">

        {/* Hero Header */}
        <div className="relative text-center sm:text-left flex flex-col sm:flex-row items-center justify-between gap-6 pb-8 border-b border-themeBorder">
          <div className="space-y-3">
            <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-themeAccentBg text-themeAccent font-bold text-xs select-none">
              <Terminal size={12} className="animate-pulse" />
              <span>Personal Knowledge Base</span>
            </div>
            <h1 className="text-4xl md:text-5xl font-extrabold tracking-tight text-themeTextPrimary leading-none">
              Welcome to{' '}
              <span className="bg-gradient-to-r from-themeAccent via-themeAccentSecondary to-themeAccent bg-clip-text text-transparent">
                {wikiName}
              </span>
            </h1>
            <p className="text-themeTextMuted max-w-xl text-sm leading-relaxed font-medium">
              An elegant, high-fidelity Markdown knowledge base and collaborative AI second brain. Backed by local files with a built-in MCP server. Serious enough to organize your projects, intelligent enough to co-author with AI.
            </p>
          </div>
          <div className="shrink-0 flex flex-col items-center justify-center p-6 rounded-2xl glass-panel text-center select-none w-40 h-40">
            <span className="text-5xl font-black text-themeAccent leading-none">{articles.length}</span>
            <span className="text-xs font-bold text-themeTextMuted mt-2 uppercase tracking-widest">Wiki Pages</span>
          </div>
        </div>

        {/* Full-Text Search Bar */}
        <div className="max-w-2xl mx-auto py-2 select-none">
          <form onSubmit={handleFtsSearch} className="space-y-4">
            <div className="relative group">
              <div className="absolute -inset-0.5 bg-gradient-to-r from-themeAccent to-themeAccentSecondary rounded-2xl blur opacity-15 group-hover:opacity-25 transition duration-300" />
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
              <button type="submit" className="py-2.5 px-6 rounded-xl bg-themeBgPrimary hover:opacity-90 text-themeTextSecondary font-bold text-xs shadow-sm hover:scale-[1.01] active:scale-95 transition-all select-none border border-themeBorder">
                Search {wikiName}
              </button>
              <button type="button" onClick={() => onNavigate('new?title=Markdown%20Playground')} className="py-2.5 px-6 rounded-xl bg-themeBgPrimary hover:opacity-90 text-themeTextSecondary font-bold text-xs shadow-sm hover:scale-[1.01] active:scale-95 transition-all select-none border border-themeBorder">
                Draft Sandbox
              </button>
            </div>
          </form>
        </div>

        {/* Quick Action Cards */}
        <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-6 select-none">
          <div onClick={() => onCreateNew('article')} className="p-6 rounded-2xl glass-panel hover:border-themeAccent/30 hover:scale-[1.02] cursor-pointer group transition-all duration-300">
            <div className="w-10 h-10 rounded-xl bg-themeAccentBg text-themeAccent flex items-center justify-center group-hover:scale-110 transition-transform duration-200 shadow-inner">
              <BookOpen size={20} />
            </div>
            <h3 className="text-base font-bold text-themeTextSecondary mt-4 group-hover:text-themeAccent transition-colors">Create Wiki Article</h3>
            <p className="text-xs text-themeTextMuted mt-1.5 leading-relaxed font-medium">Start writing a clean, markdown-backed wiki page right now.</p>
          </div>
          <div onClick={() => onCreateNew('plan')} className="p-6 rounded-2xl glass-panel hover:border-themeAccentSecondary/30 hover:scale-[1.02] cursor-pointer group transition-all duration-300">
            <div className="w-10 h-10 rounded-xl bg-themeAccentBg text-themeAccentSecondary flex items-center justify-center group-hover:scale-110 transition-transform duration-200 shadow-inner">
              <ClipboardList size={20} />
            </div>
            <h3 className="text-base font-bold text-themeTextSecondary mt-4 group-hover:text-themeAccentSecondary transition-colors">Create Agent Plan</h3>
            <p className="text-xs text-themeTextMuted mt-1.5 leading-relaxed font-medium">Create a Collaborative AI Plan to coordinate roadmaps and milestones.</p>
          </div>
          <div onClick={() => onCreateNew('skill')} className="p-6 rounded-2xl glass-panel hover:border-themeAccent/30 hover:scale-[1.02] cursor-pointer group transition-all duration-300">
            <div className="w-10 h-10 rounded-xl bg-themeAccentBg text-themeAccent flex items-center justify-center group-hover:scale-110 transition-transform duration-200 shadow-inner">
              <Wrench size={20} />
            </div>
            <h3 className="text-base font-bold text-themeTextSecondary mt-4 group-hover:text-themeAccent transition-colors">Create Agent Skill</h3>
            <p className="text-xs text-themeTextMuted mt-1.5 leading-relaxed font-medium">Author procedural instructions and rules (SKILL.md) for AI agents.</p>
          </div>
          <div onClick={() => onNavigate('new?title=Markdown%20Playground')} className="p-6 rounded-2xl glass-panel hover:border-themeAccentSecondary/30 hover:scale-[1.02] cursor-pointer group transition-all duration-300">
            <div className="w-10 h-10 rounded-xl bg-themeAccentBg text-themeAccentSecondary flex items-center justify-center group-hover:scale-110 transition-transform duration-200 shadow-inner">
              <Terminal size={20} />
            </div>
            <h3 className="text-base font-bold text-themeTextSecondary mt-4 group-hover:text-themeAccentSecondary transition-colors">Draft Sandbox</h3>
            <p className="text-xs text-themeTextMuted mt-1.5 leading-relaxed font-medium">Open a dynamic, split-pane markdown sandbox to test images and layouts.</p>
          </div>
        </div>

        {/* Directory Sections */}
        <div className="space-y-10">
          <DirectorySection
            title="Wiki Index"
            icon={<Grid size={16} className="text-themeAccent" />}
            isExpanded={wikiExpanded}
            onToggle={() => setWikiExpanded(!wikiExpanded)}
            searchQuery={wikiSearchQuery}
            onSearchChange={setWikiSearchQuery}
            filterPlaceholder="Filter articles by title or tag..."
            onOpenFilterHelp={() => setShowFilterHelp(true)}
            articles={filteredWikiArticles}
            onNavigate={onNavigate}
            statusTags={statusTags}
            emptyContent={
              <>
                <FileText size={32} className="mx-auto text-themeTextMuted animate-bounce" />
                <h3 className="text-sm font-bold text-themeTextSecondary mt-3">No articles match your search</h3>
                <p className="text-xs text-themeTextMuted mt-1">Try clearing your filter or create a new wiki page!</p>
                <button onClick={() => onCreateNew('article')} className="mt-4 inline-flex items-center gap-1.5 py-2 px-4 rounded-xl bg-themeAccent hover:opacity-90 text-white font-semibold text-xs transition-colors cursor-pointer">
                  <Plus size={12} />
                  <span>Create &ldquo;{wikiSearchQuery}&rdquo;</span>
                </button>
              </>
            }
          />

          <DirectorySection
            title="Agent Memories"
            icon={<Cpu size={16} className="text-themeAccent" />}
            isExpanded={memoriesExpanded}
            onToggle={() => setMemoriesExpanded(!memoriesExpanded)}
            searchQuery={memoriesSearchQuery}
            onSearchChange={setMemoriesSearchQuery}
            filterPlaceholder="Filter memories by title or tag..."
            onOpenFilterHelp={() => setShowFilterHelp(true)}
            articles={filteredAiMemories}
            onNavigate={onNavigate}
            statusTags={statusTags}
            emptyContent={
              <>
                <Cpu size={32} className="mx-auto text-themeTextMuted animate-bounce" />
                <h3 className="text-sm font-bold text-themeTextSecondary mt-3">No agent memories found</h3>
                <p className="text-xs text-themeTextMuted mt-1">Let your AI agent connect and run to generate memories!</p>
              </>
            }
          />

          <DirectorySection
            title="Agent Plans"
            icon={<ClipboardList size={16} className="text-themeAccentSecondary" />}
            isExpanded={plansExpanded}
            onToggle={() => setPlansExpanded(!plansExpanded)}
            secondary
            searchQuery={plansSearchQuery}
            onSearchChange={setPlansSearchQuery}
            filterPlaceholder="Filter plans by title or tag..."
            onOpenFilterHelp={() => setShowFilterHelp(true)}
            articles={filteredAiPlans}
            onNavigate={onNavigate}
            statusTags={statusTags}
            emptyContent={
              <>
                <ClipboardList size={32} className="mx-auto text-themeTextMuted animate-bounce" />
                <h3 className="text-sm font-bold text-themeTextSecondary mt-3">No agent plans found</h3>
                <p className="text-xs text-themeTextMuted mt-1">Create a collaborative agent plan to get started!</p>
                <button onClick={() => onCreateNew('plan')} className="mt-4 inline-flex items-center gap-1.5 py-2 px-4 rounded-xl bg-themeAccentSecondary hover:opacity-90 text-white font-semibold text-xs transition-colors cursor-pointer">
                  <Plus size={12} />
                  <span>Create Agent Plan</span>
                </button>
              </>
            }
          />

          <DirectorySection
            title="Agent Skills"
            icon={<Wrench size={16} className="text-themeAccent" />}
            isExpanded={skillsExpanded}
            onToggle={() => setSkillsExpanded(!skillsExpanded)}
            searchQuery={skillsSearchQuery}
            onSearchChange={setSkillsSearchQuery}
            filterPlaceholder="Filter skills by title or tag..."
            onOpenFilterHelp={() => setShowFilterHelp(true)}
            articles={filteredAiSkills}
            onNavigate={onNavigate}
            statusTags={statusTags}
            emptyContent={
              <>
                <Wrench size={32} className="mx-auto text-themeTextMuted animate-bounce" />
                <h3 className="text-sm font-bold text-themeTextSecondary mt-3">No agent skills found</h3>
                <p className="text-xs text-themeTextMuted mt-1">Create procedural SKILL.md guides for your AI agents!</p>
                <button onClick={() => onCreateNew('skill')} className="mt-4 inline-flex items-center gap-1.5 py-2 px-4 rounded-xl bg-themeAccent hover:opacity-90 text-white font-semibold text-xs transition-colors cursor-pointer">
                  <Plus size={12} />
                  <span>Create Agent Skill</span>
                </button>
              </>
            }
          />
        </div>

        {/* Footer */}
        <div className="pt-8 border-t border-themeBorder flex items-center justify-center gap-1.5 text-center text-xs text-themeTextMuted select-none font-medium">
          <span>Made with</span>
          <Heart size={11} className="text-rose-500 fill-rose-500 animate-pulse" />
          <span>for serious and fun brainstorming.</span>
        </div>

      </div>

      {showFilterHelp && <FilterHelpModal onClose={() => setShowFilterHelp(false)} />}
    </div>
  );
};

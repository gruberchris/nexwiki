import React, { useState, useMemo } from 'react';
import type { Article } from '../types';
import { formatRelativeTime } from '../utils';
import { 
  FileText, 
  Search, 
  Moon, 
  Sun, 
  Layers, 
  Clock, 
  ChevronRight,
  BookOpen,
  Cpu,
  Wrench,
  ClipboardList,
  Tag,
  ChevronDown,
  X,
  Palette,
  Archive,
  Activity,
  HelpCircle
} from 'lucide-react';
import { useSSE } from '../hooks/useSSE';
import { matchesSidebarFilter } from '../filterUtils';
import { SidebarFilterHelpModal } from './SidebarFilterHelpModal';

interface SidebarProps {
  articles: Article[];
  currentSlug: string;
  darkMode: boolean;
  onToggleDarkMode: () => void;
  onOpenThemeManager: () => void;
  onNavigate: (slug: string) => void;
  onCreateNew: (type: 'article' | 'plan' | 'skill') => void;
  wikiName: string;
  onExportAll: () => void;
  onOpenActivityLog: () => void;
  version?: string;
}

export const Sidebar: React.FC<SidebarProps> = ({
  articles,
  currentSlug,
  darkMode,
  onToggleDarkMode,
  onOpenThemeManager,
  onNavigate,
  onCreateNew,
  wikiName,
  onExportAll,
  onOpenActivityLog,
  version = '0.1.0'
}) => {
  const { unreadCount } = useSSE();
  const [filterQuery, setFilterQuery] = useState('');
  const [selectedTag, setSelectedTag] = useState<string | null>(null);
  const [showFilterHelp, setShowFilterHelp] = useState(false);
  const [aiMemoriesOpen, setAiMemoriesOpen] = useState(true);
  const [aiSkillsOpen, setAiSkillsOpen] = useState(true);
  const [aiPlansOpen, setAiPlansOpen] = useState(true);

  // Parse all unique user tags (excluding "aiagent-" tags)
  const allUserTags = useMemo(() => {
    const tags = new Set<string>();
    articles.forEach(art => {
      art.tags?.forEach(tag => {
        if (!tag.toLowerCase().startsWith('aiagent-')) {
          tags.add(tag);
        }
      });
    });
    return Array.from(tags).sort();
  }, [articles]);

  // Standard articles filter (excl. agent pages, and matching tag + search query)
  const standardArticles = useMemo(() => {
    return articles.filter(art => {
      const isAgent = art.tags?.some(tag => tag.toLowerCase().startsWith('aiagent-'));
      if (isAgent) return false;

      const matchesQuery = matchesSidebarFilter(art, filterQuery);
      
      const matchesTag = !selectedTag || art.tags?.includes(selectedTag);

      return matchesQuery && matchesTag;
    });
  }, [articles, filterQuery, selectedTag]);

  // AI Agent memories filter (starts with aiagent- but is NOT aiagent-skill and NOT aiagent-plan)
  const aiMemories = useMemo(() => {
    return articles.filter(art => {
      const isAgent = art.tags?.some(tag => tag.toLowerCase().startsWith('aiagent-'));
      if (!isAgent) return false;
      const isSkill = art.tags?.some(tag => tag.toLowerCase() === 'aiagent-skill');
      const isPlan = art.tags?.some(tag => tag.toLowerCase() === 'aiagent-plan');
      if (isSkill || isPlan) return false;

      const matchesQuery = matchesSidebarFilter(art, filterQuery);
      const matchesTag = !selectedTag || art.tags?.includes(selectedTag);
      return matchesQuery && matchesTag;
    });
  }, [articles, filterQuery, selectedTag]);

  // AI Agent skills filter (possesses aiagent-skill tag)
  const aiSkills = useMemo(() => {
    return articles.filter(art => {
      const isSkill = art.tags?.some(tag => tag.toLowerCase() === 'aiagent-skill');
      if (!isSkill) return false;

      const matchesQuery = matchesSidebarFilter(art, filterQuery);
      const matchesTag = !selectedTag || art.tags?.includes(selectedTag);
      return matchesQuery && matchesTag;
    });
  }, [articles, filterQuery, selectedTag]);

  // AI Agent plans filter (possesses aiagent-plan tag)
  const aiPlans = useMemo(() => {
    return articles.filter(art => {
      const isPlan = art.tags?.some(tag => tag.toLowerCase() === 'aiagent-plan');
      if (!isPlan) return false;

      const matchesQuery = matchesSidebarFilter(art, filterQuery);
      const matchesTag = !selectedTag || art.tags?.includes(selectedTag);
      return matchesQuery && matchesTag;
    });
  }, [articles, filterQuery, selectedTag]);

  return (
    <aside className="w-80 h-screen flex flex-col theme-sidebar backdrop-blur-md transition-all select-none relative">
      {/* Branding Header */}
      <div className="p-6 pb-4 flex items-center justify-between">
        <div 
          onClick={() => onNavigate('home')} 
          className="flex items-center gap-2.5 cursor-pointer group"
        >
          <div className="w-9 h-9 rounded-xl bg-gradient-to-tr from-themeAccent to-themeAccentSecondary flex items-center justify-center text-white shadow-md shadow-themeAccent/10 group-hover:scale-105 transition-all duration-200">
            <Layers size={18} className="group-hover:rotate-6 transition-transform duration-200" />
          </div>
          <div className="flex flex-col">
            <h1 className="text-base font-black tracking-tight text-themeTextPrimary leading-tight group-hover:text-themeAccent transition-colors">
              {wikiName}
            </h1>
            <span className="text-[9px] font-bold text-themeTextMuted uppercase tracking-widest leading-none mt-0.5">
              Knowledge Engine
            </span>
          </div>
        </div>

        <div className="flex items-center gap-2">
          {/* Activity Log Drawer Button */}
          <button
            onClick={onOpenActivityLog}
            className="p-2 rounded-xl text-themeTextMuted hover:bg-themeBgPrimary hover:text-themeTextPrimary border border-themeBorder hover:scale-105 active:scale-95 transition-all duration-200 cursor-pointer relative"
            title="Open Live Activity Log"
            aria-label="Open Live Activity Log"
          >
            <Activity size={14} className="text-indigo-500" />
            {unreadCount > 0 && (
              <span className="absolute -top-1.5 -right-1.5 flex h-4 min-w-[16px] px-1 items-center justify-center rounded-full bg-rose-500 text-[8px] font-extrabold text-white animate-pulse shadow-sm">
                +{unreadCount}
              </span>
            )}
          </button>

          {/* Theme Palette Customizer Button */}
          <button
            onClick={onOpenThemeManager}
            className="p-2 rounded-xl text-themeTextMuted hover:bg-themeBgPrimary hover:text-themeTextPrimary border border-themeBorder hover:scale-105 active:scale-95 transition-all duration-200 cursor-pointer"
            title="Configure Theme Palette"
            aria-label="Configure Theme Palette"
          >
            <Palette size={14} className="text-themeAccent" />
          </button>

          {/* Light/Dark Toggle Button */}
          <button
            onClick={onToggleDarkMode}
            className="p-2 rounded-xl text-themeTextMuted hover:bg-themeBgPrimary hover:text-themeTextPrimary border border-themeBorder hover:scale-105 active:scale-95 transition-all duration-200 cursor-pointer"
            title="Toggle Light/Dark"
            aria-label="Toggle Light/Dark"
          >
            {darkMode ? <Sun size={14} className="text-amber-400" /> : <Moon size={14} className="text-themeAccent" />}
          </button>


        </div>
      </div>

      {/* Action: Create New Page buttons */}
      <div className="px-6 py-2 grid grid-cols-3 gap-2">
        <button
          onClick={() => onCreateNew('article')}
          className="flex flex-col items-center justify-center gap-1 py-2 px-1 rounded-xl bg-themeBgPrimary border border-themeBorder hover:bg-themeBgSecondary hover:border-themeAccent/30 hover:scale-[1.02] text-themeTextSecondary active:scale-[0.98] transition-all select-none cursor-pointer"
          title="Create standard Wiki Article"
        >
          <BookOpen size={14} className="text-themeAccent" />
          <span className="text-[10px] font-bold">Article</span>
        </button>
        <button
          onClick={() => onCreateNew('plan')}
          className="flex flex-col items-center justify-center gap-1 py-2 px-1 rounded-xl bg-themeBgPrimary border border-themeBorder hover:bg-themeBgSecondary hover:border-themeAccentSecondary/30 hover:scale-[1.02] text-themeTextSecondary active:scale-[0.98] transition-all select-none cursor-pointer"
          title="Create Collaborative Agent Plan"
        >
          <ClipboardList size={14} className="text-themeAccentSecondary" />
          <span className="text-[10px] font-bold">Agent Plan</span>
        </button>
        <button
          onClick={() => onCreateNew('skill')}
          className="flex flex-col items-center justify-center gap-1 py-2 px-1 rounded-xl bg-themeBgPrimary border border-themeBorder hover:bg-themeBgSecondary hover:border-themeAccent/30 hover:scale-[1.02] text-themeTextSecondary active:scale-[0.98] transition-all select-none cursor-pointer"
          title="Create Custom Agent Skill"
        >
          <Cpu size={14} className="text-themeAccent" />
          <span className="text-[10px] font-bold">Agent Skill</span>
        </button>
      </div>

      {/* Local Filter input box */}
      <div className="px-6 py-3">
        <div className="flex items-center gap-1.5 animate-fade-in">
          <div className="relative flex-1 group">
            <Search size={14} className="absolute left-3.5 top-1/2 -translate-y-1/2 text-themeTextMuted group-focus-within:text-themeAccent transition-colors" />
            <input
              type="text"
              placeholder="Filter sidebar..."
              value={filterQuery}
              onChange={(e) => setFilterQuery(e.target.value)}
              className="w-full pl-10 pr-4 py-2 text-xs rounded-xl bg-themeBgPrimary border border-themeBorder focus:outline-none focus:ring-2 focus:ring-themeAccent focus:bg-themeBgSecondary text-themeTextSecondary transition-all placeholder:text-themeTextMuted"
            />
            {filterQuery && (
              <button
                onClick={() => setFilterQuery('')}
                className="absolute inset-y-0 right-3 flex items-center text-themeTextMuted hover:text-rose-500 transition-colors"
              >
                <X size={12} />
              </button>
            )}
          </div>
          <button
            onClick={() => setShowFilterHelp(true)}
            className="shrink-0 p-1.5 rounded-lg text-themeTextMuted hover:text-themeAccent hover:bg-themeAccentBg transition-colors"
            title="Filter syntax help"
          >
            <HelpCircle size={14} />
          </button>
        </div>
      </div>

      {/* Tag Cloud Filter Section */}
      {allUserTags.length > 0 && (
        <div className="px-6 py-1 space-y-1">
          <div className="flex items-center gap-1 text-[10px] font-bold text-themeTextMuted uppercase tracking-widest">
            <Tag size={10} />
            <span>Filter by Tag</span>
          </div>
          <div className="flex flex-wrap gap-1 mt-1.5 max-h-24 overflow-y-auto pr-1">
            {allUserTags.map(tag => {
              const isSelected = selectedTag === tag;
              return (
                <button
                  key={tag}
                  onClick={() => setSelectedTag(isSelected ? null : tag)}
                  className={`text-[10px] px-2.5 py-0.5 rounded-full border transition-all cursor-pointer font-medium hover:scale-102 ${
                    isSelected
                      ? 'bg-themeAccent border-themeAccent text-white font-bold shadow-sm'
                      : 'bg-themeBgPrimary border-themeBorder text-themeTextMuted hover:bg-themeBgSecondary hover:text-themeTextSecondary'
                  }`}
                >
                  {tag}
                </button>
              );
            })}
            {selectedTag && (
              <button
                onClick={() => setSelectedTag(null)}
                className="text-[10px] px-2 py-0.5 rounded-full bg-rose-50 dark:bg-rose-950/20 border border-rose-200 dark:border-rose-900 text-rose-600 dark:text-rose-400 flex items-center gap-0.5 cursor-pointer font-bold animate-pulse"
              >
                <span>Clear</span>
                <X size={8} />
              </button>
            )}
          </div>
        </div>
      )}

      {/* Scrollable List Area */}
      <div className="flex-1 overflow-y-auto px-4 pb-6 mt-4 space-y-6">
        
        {/* Navigation Section: Standard Articles */}
        <div className="space-y-2">
          <span className="text-[10px] font-bold text-themeTextMuted uppercase tracking-widest px-3 flex items-center gap-1.5">
            <Layers size={10} />
            Articles ({standardArticles.length})
          </span>
          <div className="space-y-0.5">
            {standardArticles.length === 0 ? (
              <div className="p-6 text-center text-xs text-themeTextMuted border border-dashed border-themeBorder rounded-xl">
                No articles found
              </div>
            ) : (
              standardArticles.map((art) => {
                const isActive = currentSlug === art.slug;
                return (
                  <button
                    key={art.slug}
                    onClick={() => onNavigate(art.slug)}
                    className={`w-full group flex items-center justify-between p-3 rounded-xl transition-all duration-150 border ${
                      isActive
                        ? 'bg-themeAccentBg border-themeBorder/40 text-themeAccent font-semibold'
                        : 'text-themeTextSecondary hover:bg-themeBgPrimary hover:text-themeTextPrimary border-transparent'
                    }`}
                  >
                    <div className="flex items-center gap-3 overflow-hidden">
                      <FileText 
                        size={15} 
                        className={`shrink-0 ${
                          isActive 
                            ? 'text-themeAccent' 
                            : 'text-themeTextMuted group-hover:text-themeTextPrimary'
                        }`} 
                      />
                      <span className="truncate text-sm text-left">{art.title}</span>
                    </div>
                    
                    <div className="flex items-center gap-1 shrink-0">
                      {isActive ? (
                        <ChevronRight size={14} className="animate-pulse" />
                      ) : (
                        <div className="flex items-center gap-1 text-[9px] text-themeTextMuted opacity-80 group-hover:opacity-100">
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

        {/* Navigation Section: AI Memories */}
        <div className="space-y-2">
          <button
            onClick={() => setAiMemoriesOpen(!aiMemoriesOpen)}
            className="w-full flex items-center justify-between text-[10px] font-bold text-themeAccent uppercase tracking-widest px-3 py-1.5 bg-themeAccentBg/40 rounded-lg group cursor-pointer hover:bg-themeAccentBg/65"
          >
            <span className="flex items-center gap-1.5">
              <Cpu size={10} className="animate-pulse text-themeAccent" />
              Agent Memories ({aiMemories.length})
            </span>
            <ChevronDown size={12} className={`transition-transform duration-200 text-themeTextMuted ${aiMemoriesOpen ? 'rotate-0' : '-rotate-90'}`} />
          </button>
          
          {aiMemoriesOpen && (
            <div className="space-y-0.5">
              {aiMemories.length === 0 ? (
                <div className="p-4 text-center text-[10px] text-themeTextMuted border border-dashed border-themeBorder rounded-xl">
                  No memories logged
                </div>
              ) : (
                aiMemories.map((art) => {
                  const isActive = currentSlug === art.slug;
                  return (
                    <button
                      key={art.slug}
                      onClick={() => onNavigate(art.slug)}
                      className={`w-full group flex items-center justify-between p-3 rounded-xl transition-all duration-150 border ${
                        isActive
                          ? 'bg-themeAccentBg border-themeBorder/40 text-themeAccent font-semibold shadow-inner'
                          : 'text-themeTextSecondary hover:bg-themeBgPrimary hover:text-themeTextPrimary border-transparent'
                      }`}
                    >
                      <div className="flex items-center gap-3 overflow-hidden">
                        <Cpu 
                          size={14} 
                          className={`shrink-0 ${
                            isActive 
                              ? 'text-themeAccent' 
                              : 'text-themeTextMuted group-hover:text-themeAccent'
                          }`} 
                        />
                        <span className="truncate text-sm text-left">{art.title}</span>
                      </div>
                      
                      <div className="flex items-center gap-1 shrink-0">
                        {isActive ? (
                          <ChevronRight size={14} className="animate-pulse text-themeAccent" />
                        ) : (
                          <div className="flex items-center gap-1 text-[9px] text-themeTextMuted opacity-85 group-hover:opacity-100">
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
          )}
        </div>

        {/* Navigation Section: AI Plans */}
        <div className="space-y-2">
          <button
            onClick={() => setAiPlansOpen(!aiPlansOpen)}
            className="w-full flex items-center justify-between text-[10px] font-bold text-themeAccent uppercase tracking-widest px-3 py-1.5 bg-themeAccentBg/40 rounded-lg group cursor-pointer hover:bg-themeAccentBg/65"
          >
            <span className="flex items-center gap-1.5">
              <ClipboardList size={10} className="animate-pulse text-themeAccent" />
              Agent Plans ({aiPlans.length})
            </span>
            <ChevronDown size={12} className={`transition-transform duration-200 text-themeTextMuted ${aiPlansOpen ? 'rotate-0' : '-rotate-90'}`} />
          </button>
          
          {aiPlansOpen && (
            <div className="space-y-0.5">
              {aiPlans.length === 0 ? (
                <div className="p-4 text-center text-[10px] text-themeTextMuted border border-dashed border-themeBorder rounded-xl">
                  No plans registered
                </div>
              ) : (
                aiPlans.map((art) => {
                  const isActive = currentSlug === art.slug;
                  return (
                    <button
                      key={art.slug}
                      onClick={() => onNavigate(art.slug)}
                      className={`w-full group flex items-center justify-between p-3 rounded-xl transition-all duration-150 border ${
                        isActive
                          ? 'bg-themeAccentBg border-themeBorder/40 text-themeAccent font-semibold shadow-inner'
                          : 'text-themeTextSecondary hover:bg-themeBgPrimary hover:text-themeTextPrimary border-transparent'
                      }`}
                    >
                      <div className="flex items-center gap-3 overflow-hidden">
                        <ClipboardList 
                          size={14} 
                          className={`shrink-0 ${
                            isActive 
                              ? 'text-themeAccent' 
                              : 'text-themeTextMuted group-hover:text-themeAccent'
                          }`} 
                        />
                        <span className="truncate text-sm text-left">{art.title}</span>
                      </div>
                      
                      <div className="flex items-center gap-1 shrink-0">
                        {isActive ? (
                          <ChevronRight size={14} className="animate-pulse text-themeAccent" />
                        ) : (
                          <div className="flex items-center gap-1 text-[9px] text-themeTextMuted opacity-85 group-hover:opacity-100">
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
          )}
        </div>

        {/* Navigation Section: AI Skills */}
        <div className="space-y-2">
          <button
            onClick={() => setAiSkillsOpen(!aiSkillsOpen)}
            className="w-full flex items-center justify-between text-[10px] font-bold text-themeAccent uppercase tracking-widest px-3 py-1.5 bg-themeAccentBg/40 rounded-lg group cursor-pointer hover:bg-themeAccentBg/65"
          >
            <span className="flex items-center gap-1.5">
              <Wrench size={10} className="animate-pulse text-themeAccent" />
              Agent Skills ({aiSkills.length})
            </span>
            <ChevronDown size={12} className={`transition-transform duration-200 text-themeTextMuted ${aiSkillsOpen ? 'rotate-0' : '-rotate-90'}`} />
          </button>
          
          {aiSkillsOpen && (
            <div className="space-y-0.5">
              {aiSkills.length === 0 ? (
                <div className="p-4 text-center text-[10px] text-themeTextMuted border border-dashed border-themeBorder rounded-xl">
                  No skills registered
                </div>
              ) : (
                aiSkills.map((art) => {
                  const isActive = currentSlug === art.slug;
                  return (
                    <button
                      key={art.slug}
                      onClick={() => onNavigate(art.slug)}
                      className={`w-full group flex items-center justify-between p-3 rounded-xl transition-all duration-150 border ${
                        isActive
                          ? 'bg-themeAccentBg border-themeBorder/40 text-themeAccent font-semibold shadow-inner'
                          : 'text-themeTextSecondary hover:bg-themeBgPrimary hover:text-themeTextPrimary border-transparent'
                      }`}
                    >
                      <div className="flex items-center gap-3 overflow-hidden">
                        <Wrench 
                          size={14} 
                          className={`shrink-0 ${
                            isActive 
                              ? 'text-themeAccent' 
                              : 'text-themeTextMuted group-hover:text-themeAccent'
                          }`} 
                        />
                        <span className="truncate text-sm text-left">{art.title}</span>
                      </div>
                      
                      <div className="flex items-center gap-1 shrink-0">
                        {isActive ? (
                          <ChevronRight size={14} className="animate-pulse text-themeAccent" />
                        ) : (
                          <div className="flex items-center gap-1 text-[9px] text-themeTextMuted opacity-85 group-hover:opacity-100">
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
          )}
        </div>

        {/* Bulk Export Button */}
        <div className="pt-4 border-t border-themeBorder/40">
          <button
            onClick={onExportAll}
            className="w-full flex items-center justify-center gap-2 px-4 py-3 text-xs font-bold text-themeTextSecondary bg-themeBgPrimary/40 border border-themeBorder/60 rounded-xl hover:bg-themeAccentBg/40 hover:text-themeAccent hover:border-themeAccent/30 active:scale-[0.98] transition-all select-none cursor-pointer"
            title="Download all Wiki pages as a structured ZIP"
          >
            <Archive size={14} className="text-themeAccent shrink-0" />
            <span>Download All Content (.zip)</span>
          </button>
        </div>

      </div>

      {/* Sidebar Footer */}
      <div className="p-4 border-t border-themeBorder bg-themeBgPrimary/30 flex items-center justify-between text-[10px] text-themeTextMuted font-semibold tracking-wide">
        <span>v{version}</span>
        <span className="flex items-center gap-1 text-themeTextMuted">
          <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-ping"></span>
          Single-User Mode
        </span>
      </div>

      {showFilterHelp && <SidebarFilterHelpModal onClose={() => setShowFilterHelp(false)} />}
    </aside>
  );
};

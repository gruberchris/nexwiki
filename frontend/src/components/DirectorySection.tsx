import React from 'react';
import type { Article } from '../types';
import { Search, ChevronDown, HelpCircle } from 'lucide-react';
import { ArticleCard } from './ArticleCard';

interface DirectorySectionProps {
  title: string;
  icon: React.ReactNode;
  isExpanded: boolean;
  onToggle: () => void;
  secondary?: boolean;
  searchQuery: string;
  onSearchChange: (q: string) => void;
  filterPlaceholder: string;
  onOpenFilterHelp: () => void;
  articles: Article[];
  onNavigate: (slug: string) => void;
  statusTags: Set<string>;
  emptyContent: React.ReactNode;
}

export function DirectorySection({
  title,
  icon,
  isExpanded,
  onToggle,
  secondary = false,
  searchQuery,
  onSearchChange,
  filterPlaceholder,
  onOpenFilterHelp,
  articles,
  onNavigate,
  statusTags,
  emptyContent,
}: DirectorySectionProps) {
  const accentText = secondary ? 'text-themeAccentSecondary' : 'text-themeAccent';
  const accentHover = secondary ? 'hover:text-themeAccentSecondary' : 'hover:text-themeAccent';
  const accentGroupHover = secondary ? 'group-hover:text-themeAccentSecondary' : 'group-hover:text-themeAccent';
  const accentRing = secondary ? 'focus:ring-themeAccentSecondary' : 'focus:ring-themeAccent';

  return (
    <div className="space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-themeBorder pb-3">
        <button
          onClick={onToggle}
          className={`flex items-center gap-2 text-lg font-bold text-themeTextSecondary ${accentHover} transition-colors select-none group focus:outline-none`}
        >
          <ChevronDown
            size={18}
            className={`transition-transform duration-200 text-themeTextMuted ${accentGroupHover} ${isExpanded ? 'rotate-0' : '-rotate-90'}`}
          />
          {icon}
          <span>{title}</span>
          <span className={`text-xs bg-themeAccentBg ${accentText} font-bold px-2.5 py-0.5 rounded-full select-none`}>
            {articles.length}
          </span>
        </button>

        {isExpanded && (
          <div className="flex items-center gap-1.5 w-full sm:w-80 animate-fade-in">
            <div className="relative flex-1">
              <Search size={14} className="absolute left-3.5 top-1/2 -translate-y-1/2 text-themeTextMuted" />
              <input
                type="text"
                placeholder={filterPlaceholder}
                value={searchQuery}
                onChange={(e) => onSearchChange(e.target.value)}
                className={`w-full pl-10 pr-4 py-2 text-xs rounded-xl bg-themeBgSecondary border border-themeBorder focus:outline-none focus:ring-2 ${accentRing} text-themeTextSecondary shadow-sm transition-all`}
              />
            </div>
            <button
              onClick={onOpenFilterHelp}
              className={`shrink-0 p-1.5 rounded-lg text-themeTextMuted ${accentHover} hover:bg-themeAccentBg transition-colors`}
              title="Filter syntax help"
            >
              <HelpCircle size={14} />
            </button>
          </div>
        )}
      </div>

      {isExpanded && (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 animate-fade-in">
          {articles.length === 0 ? (
            <div className="col-span-2 p-12 text-center border border-dashed border-themeBorder rounded-2xl select-none">
              {emptyContent}
            </div>
          ) : (
            articles.map((art) => (
              <ArticleCard
                key={art.slug}
                art={art}
                onNavigate={onNavigate}
                secondary={secondary}
                statusTags={statusTags}
              />
            ))
          )}
        </div>
      )}
    </div>
  );
}

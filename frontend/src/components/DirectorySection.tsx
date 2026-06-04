import React from 'react';
import type { Article } from '../types';
import { ChevronDown } from 'lucide-react';
import { ArticleCard } from './ArticleCard';
import { getAutocompleteSearchTerm, buildSuggestionsFromArticles } from '../filterUtils';
import { FilterInput } from './FilterInput';

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
  allArticles: Article[]; // Original unfiltered articles for autocomplete suggestions
  filteredArticles: Article[]; // Filtered articles for display
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
  allArticles,
  filteredArticles,
  onNavigate,
  statusTags,
  emptyContent,
}: DirectorySectionProps) {
  const accentText = secondary ? 'text-themeAccentSecondary' : 'text-themeAccent';
  const accentHover = secondary ? 'hover:text-themeAccentSecondary' : 'hover:text-themeAccent';
  const accentGroupHover = secondary ? 'group-hover:text-themeAccentSecondary' : 'group-hover:text-themeAccent';
  const accentRing = secondary ? 'focus:ring-themeAccentSecondary' : 'focus:ring-themeAccent';



  const autocompleteTerm = React.useMemo(() => getAutocompleteSearchTerm(searchQuery), [searchQuery]);

  const suggestions = React.useMemo(() => {
    if (!autocompleteTerm) return [];
    
    // Build suggestions directly from article objects to avoid duplicates
    return buildSuggestionsFromArticles(allArticles, autocompleteTerm);
  }, [autocompleteTerm, allArticles]);

  return (
    <div className="space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-themeBorder pb-3 relative z-20">
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
              {allArticles.length}
            </span>
        </button>

        {isExpanded && (
          <FilterInput
            value={searchQuery}
            onChange={onSearchChange}
            suggestions={suggestions}
            placeholder={filterPlaceholder}
            onOpenHelp={onOpenFilterHelp}
            inputClassName="bg-themeBgSecondary shadow-sm"
            className="w-full sm:w-80"
            accentRingClass={accentRing}
            accentHoverClass={accentHover}
          />
        )}
      </div>

      {isExpanded && (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 animate-fade-in">
          {filteredArticles.length === 0 ? (
            <div className="col-span-2 p-12 text-center border border-dashed border-themeBorder rounded-2xl select-none">
              {emptyContent}
            </div>
          ) : (
            filteredArticles.map((art) => (
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

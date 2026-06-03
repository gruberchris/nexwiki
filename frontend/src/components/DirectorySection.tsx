import React from 'react';
import type { Article } from '../types';
import { Search, ChevronDown, HelpCircle, X } from 'lucide-react';
import { ArticleCard } from './ArticleCard';
import { getActiveFilterToken, applyAutocompleteSelection } from '../filterUtils';

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



  const activeToken = React.useMemo(() => getActiveFilterToken(searchQuery), [searchQuery]);

  const suggestions = React.useMemo(() => {
    const query = activeToken.trim().toLowerCase();
    if (!query) return [];

    const matchedTags = new Set<string>();
    const matchedTitles = new Set<string>();

    articles.forEach(art => {
      art.tags?.forEach(tag => {
        if (!tag.toLowerCase().startsWith('aiagent-') && tag.toLowerCase().includes(query)) {
          matchedTags.add(tag);
        }
      });
      if (art.title.toLowerCase().includes(query)) {
        matchedTitles.add(art.title);
      }
    });

    const tagResults = Array.from(matchedTags).map(t => ({ type: 'tag', value: t }));
    const titleResults = Array.from(matchedTitles).map(t => ({ type: 'title', value: t }));

    return [...tagResults, ...titleResults].slice(0, 8);
  }, [activeToken, articles]);

  const handleSelectSuggestion = (selection: string) => {
    const newQuery = applyAutocompleteSelection(searchQuery, selection);
    onSearchChange(newQuery);
  };

  const [focusedIndex, setFocusedIndex] = React.useState<number>(-1);
  const [prevSuggestionsLength, setPrevSuggestionsLength] = React.useState(suggestions.length);

  if (suggestions.length !== prevSuggestionsLength) {
    setFocusedIndex(-1);
    setPrevSuggestionsLength(suggestions.length);
  }

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
            {articles.length}
          </span>
        </button>

        {isExpanded && (
          <div className="flex items-center gap-1.5 w-full sm:w-80 animate-fade-in">
            <div className="relative flex-1 z-30">
              <Search size={14} className="absolute left-3.5 top-1/2 -translate-y-1/2 text-themeTextMuted" />
              <input
                type="text"
                placeholder={filterPlaceholder}
                value={searchQuery}
                onChange={(e) => onSearchChange(e.target.value)}
                onKeyDown={(e) => {
                  if (suggestions.length > 0) {
                    if (e.key === 'Tab') {
                      e.preventDefault();
                      if (e.shiftKey) {
                        setFocusedIndex(prev => (prev <= -1 ? suggestions.length - 1 : prev - 1));
                      } else {
                        setFocusedIndex(prev => (prev >= suggestions.length - 1 ? -1 : prev + 1));
                      }
                      return;
                    }
                    if (e.key === 'Enter' && focusedIndex >= 0 && focusedIndex < suggestions.length) {
                      e.preventDefault();
                      handleSelectSuggestion(suggestions[focusedIndex].value);
                      setFocusedIndex(-1);
                      return;
                    }
                  }
                }}
                className={`w-full pl-10 pr-9 py-2 text-xs rounded-xl bg-themeBgSecondary border border-themeBorder focus:outline-none focus:ring-2 ${accentRing} text-themeTextSecondary shadow-sm transition-all`}
              />
              {searchQuery && (
                <button
                  onClick={() => onSearchChange('')}
                  className="absolute inset-y-0 right-3 flex items-center text-themeTextMuted hover:text-rose-500 transition-colors animate-fade-in"
                >
                  <X size={12} />
                </button>
              )}

              {/* Autocomplete Suggestions Dropdown */}
              {suggestions.length > 0 && (
                <div className="absolute left-0 top-full mt-1.5 z-50 w-full bg-themeBgSecondary backdrop-blur-lg border border-themeBorder shadow-xl rounded-2xl max-h-48 overflow-y-auto py-1.5 select-none font-sans text-xs text-themeTextSecondary">
                  {suggestions.map((s, idx) => (
                    <div
                      key={`${s.type}-${s.value}`}
                      onClick={() => {
                        handleSelectSuggestion(s.value);
                        setFocusedIndex(-1);
                      }}
                      className={`px-3.5 py-2 cursor-pointer flex items-center justify-between transition-colors ${
                        idx === focusedIndex
                          ? 'bg-themeAccentBg text-themeAccent'
                          : 'hover:bg-themeAccentBg hover:text-themeAccent text-themeTextSecondary'
                      }`}
                    >
                      <span className="truncate font-medium">{s.value}</span>
                      <span className="text-[9px] font-bold text-themeTextMuted uppercase tracking-wider ml-2 bg-themeBgPrimary px-1.5 py-0.5 rounded border border-themeBorder">
                        {s.type}
                      </span>
                    </div>
                  ))}
                </div>
              )}
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

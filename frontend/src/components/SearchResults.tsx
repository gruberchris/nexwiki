import React, { useState, useEffect } from 'react';
import { Search, ArrowLeft, Clock, AlertCircle } from 'lucide-react';
import { formatRelativeTime } from './Sidebar';

interface SearchResult {
  title: string;
  slug: string;
  score: number;
  updated_at: string;
  snippets: string[];
}

interface SearchResultsProps {
  initialQuery: string;
  onNavigate: (slug: string) => void;
  wikiName: string;
}

export const SearchResults: React.FC<SearchResultsProps> = ({
  initialQuery,
  onNavigate,
  wikiName
}) => {
  const [query, setQuery] = useState(initialQuery);
  const [activeSearch, setActiveSearch] = useState(initialQuery);
  const [results, setResults] = useState<SearchResult[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [timeTakenMs, setTimeTakenMs] = useState(0);

  // Sync state if URL query changes
  useEffect(() => {
    setQuery(initialQuery);
    setActiveSearch(initialQuery);
  }, [initialQuery]);

  // Execute full-text search against Go API
  useEffect(() => {
    const runSearch = async () => {
      if (!activeSearch.trim()) {
        setResults([]);
        setIsLoading(false);
        return;
      }

      setIsLoading(true);
      const startTime = performance.now();

      try {
        const response = await fetch(`/api/search?q=${encodeURIComponent(activeSearch.trim())}`);
        if (!response.ok) throw new Error('Search request failed');
        const data = await response.json();
        setResults(data || []);
      } catch (err) {
        console.error('FTS Search error:', err);
        setResults([]);
      } finally {
        const endTime = performance.now();
        setTimeTakenMs(Math.round(endTime - startTime));
        setIsLoading(false);
      }
    };

    runSearch();
  }, [activeSearch]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (query.trim()) {
      setActiveSearch(query.trim());
      // Update window history without triggering popstate reload
      const cleanUrl = `/search?q=${encodeURIComponent(query.trim())}`;
      window.history.pushState(null, '', cleanUrl);
    }
  };

  return (
    <div className="flex-1 h-screen flex flex-col bg-slate-50 dark:bg-slate-950/40">
      
      {/* Search Bar Top Header Panel */}
      <div className="p-4 border-b border-slate-200/80 dark:border-slate-800/80 bg-white/80 dark:bg-slate-900/80 backdrop-blur-md flex items-center gap-4 select-none">
        <button
          onClick={() => onNavigate('home')}
          className="p-2 rounded-xl border border-slate-200 dark:border-slate-800 text-slate-500 dark:text-slate-400 hover:bg-slate-50 dark:hover:bg-slate-800 active:scale-95 transition-all"
          title="Return to Dashboard"
        >
          <ArrowLeft size={16} />
        </button>

        <form onSubmit={handleSubmit} className="flex-1 max-w-2xl">
          <div className="relative group">
            <Search size={16} className="absolute left-4 top-1/2 -translate-y-1/2 text-slate-400 dark:text-slate-500" />
            <input
              type="text"
              placeholder={`Search ${wikiName}...`}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="w-full pl-11 pr-4 py-2 rounded-xl bg-slate-50 dark:bg-slate-950 border border-slate-200/80 dark:border-slate-800 focus:outline-none focus:ring-2 focus:ring-indigo-500 dark:focus:ring-emerald-500 text-sm text-slate-700 dark:text-slate-250 transition-all placeholder:text-slate-400"
            />
          </div>
        </form>
      </div>

      {/* Results Main Scroll Panel */}
      <div className="flex-1 overflow-y-auto p-6 sm:p-10">
        <div className="max-w-2xl mx-auto space-y-6">
          
          {/* Search Metrics Bar */}
          {!isLoading && (
            <div className="text-xs text-slate-400 dark:text-slate-500 font-semibold select-none">
              About {results.length} result{results.length === 1 ? '' : 's'} ({timeTakenMs}ms)
            </div>
          )}

          {isLoading ? (
            // Search loading skeleton items
            <div className="space-y-6 animate-pulse select-none">
              {[1, 2, 3].map((i) => (
                <div key={i} className="space-y-2">
                  <div className="h-4 bg-slate-200 dark:bg-slate-800 rounded-md w-2/3"></div>
                  <div className="h-3 bg-slate-200 dark:bg-slate-800 rounded-md w-1/3"></div>
                  <div className="h-3 bg-slate-200 dark:bg-slate-800 rounded-md w-full"></div>
                </div>
              ))}
            </div>
          ) : results.length === 0 ? (
            // No matches found display & tips sheet
            <div className="p-8 border border-dashed border-slate-200 dark:border-slate-800 rounded-2xl bg-white dark:bg-slate-900/40 text-center space-y-4">
              <AlertCircle size={32} className="mx-auto text-slate-400 dark:text-slate-500" />
              <h3 className="text-base font-bold text-slate-800 dark:text-slate-200">
                No results found for "{activeSearch}"
              </h3>
              
              <div className="text-xs text-slate-500 dark:text-slate-400 max-w-sm mx-auto text-left space-y-2">
                <p className="font-semibold text-center mb-2">Search tips to try:</p>
                <ul className="list-disc pl-4 space-y-1">
                  <li>Double check spelling and keywords.</li>
                  <li>Use **wildcards** like `go*` to match "go", "golang", "gopher".</li>
                  <li>Wrap terms in **quotes** `"personal wiki"` to search exact phrases.</li>
                  <li>Join terms with capital **AND / OR** logic like `react OR vue`.</li>
                </ul>
              </div>
            </div>
          ) : (
            // Google-style list of search results
            <div className="space-y-6 pb-12">
              {results.map((hit) => (
                <div
                  key={hit.slug}
                  className="group space-y-1.5 p-4 rounded-xl hover:bg-white dark:hover:bg-slate-900/30 transition-all duration-150 cursor-pointer"
                  onClick={() => onNavigate(hit.slug)}
                >
                  {/* Google blue/indigo Title link */}
                  <h3 className="text-lg font-bold text-indigo-600 dark:text-indigo-400 group-hover:underline underline-offset-4 decoration-2 decoration-indigo-400/50">
                    {hit.title}
                  </h3>

                  {/* Google green URL line */}
                  <div className="font-mono text-[10px] text-emerald-600 dark:text-emerald-500/80 truncate select-all">
                    http://localhost:8080/articles/{hit.slug}
                  </div>

                  {/* Matching snippets containing highlighted mark tags */}
                  <div className="space-y-1.5 pt-1">
                    {hit.snippets.map((snippet, idx) => (
                      <p
                        key={idx}
                        className="text-xs leading-relaxed text-slate-500 dark:text-slate-400 selection:bg-yellow-200 dark:selection:bg-yellow-950/40"
                        dangerouslySetInnerHTML={{
                          __html: `... ${snippet} ...`
                        }}
                      />
                    ))}
                  </div>

                  {/* Relative update stamp */}
                  <div className="flex items-center gap-1 text-[9px] font-bold text-slate-400 dark:text-slate-500 pt-1 select-none">
                    <Clock size={10} />
                    <span>Edited {formatRelativeTime(hit.updated_at)}</span>
                  </div>
                </div>
              ))}
            </div>
          )}

        </div>
      </div>
    </div>
  );
};

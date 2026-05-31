import React, { useState, useEffect, useMemo } from 'react';
import type { Article } from './types';
import { Sidebar } from './components/Sidebar';
import { Viewer } from './components/Viewer';
import { Editor } from './components/Editor';
import { TOC } from './components/TOC';
import { Hero } from './components/Hero';
import { SearchResults } from './components/SearchResults';
import { Slugify } from './utils';
import { HistoryDrawer } from './components/HistoryDrawer';
import { 
  Edit, 
  Trash2, 
  Loader2,
  Calendar,
  Clock,
  BookOpen
} from 'lucide-react';

// Simple check to identify new page creation urls
function isNewRequest(path: string): boolean {
  return path.startsWith('/new');
}

export const App: React.FC = () => {
  // Navigation & routing state
  const [currentPath, setCurrentPath] = useState(window.location.pathname);
  const [currentSearch, setCurrentSearch] = useState(window.location.search);
  
  // Articles state
  const [articles, setArticles] = useState<Article[]>([]);
  const [currentArticle, setCurrentArticle] = useState<Article | null>(null);
  
  // Editor state
  const [isEditing, setIsEditing] = useState(false);
  const [editorSlug, setEditorSlug] = useState('');
  const [editorTitle, setEditorTitle] = useState('');
  const [editorContent, setEditorContent] = useState('');
  const [historyOpen, setHistoryOpen] = useState(false);

  // UI state
  const [isLoading, setIsLoading] = useState(true);
  const [isArticleLoading, setIsArticleLoading] = useState(false);
  const [darkMode, setDarkMode] = useState(() => {
    const savedTheme = localStorage.getItem('theme');
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    return savedTheme === 'dark' || (!savedTheme && prefersDark);
  });
  const [alertMsg, setAlertMsg] = useState<{ type: 'success' | 'error', text: string } | null>(null);
  const [wikiName, setWikiName] = useState('NexWiki');

  // Alert notifier triggers
  const triggerAlert = (type: 'success' | 'error', text: string) => {
    setAlertMsg({ type, text });
    setTimeout(() => {
      setAlertMsg(null);
    }, 4000);
  };

  // Synchronize browser tab document title with configured wiki name
  useEffect(() => {
    document.title = `${wikiName} — Personal Knowledge Engine`;
  }, [wikiName]);

  // Parse current route parameters memoized cleanly
  const routeInfo = useMemo(() => {
    if (currentPath === '/' || currentPath === '') {
      return { route: 'home', slug: '' };
    }
    if (currentPath === '/new') {
      const params = new URLSearchParams(currentSearch);
      return { route: 'new', slug: '', prefillTitle: params.get('title') || '' };
    }
    if (currentPath === '/search') {
      const params = new URLSearchParams(currentSearch);
      return { route: 'search', slug: '', searchQuery: params.get('q') || '' };
    }
    if (currentPath.startsWith('/articles/')) {
      const slug = currentPath.substring('/articles/'.length);
      // Once booting/loading is complete, if the article slug is not present in the list, route to 404
      if (!isLoading && !articles.some(art => art.slug === slug)) {
        return { route: '404', slug: '' };
      }
      return { route: 'article', slug };
    }
    return { route: '404', slug: '' };
  }, [currentPath, currentSearch, isLoading, articles]);

  // Sync state on path/search changes during rendering (avoids useEffect cascading renders)
  const [prevPath, setPrevPath] = useState(currentPath);
  const [prevSearch, setPrevSearch] = useState(currentSearch);
  if (currentPath !== prevPath || currentSearch !== prevSearch) {
    setPrevPath(currentPath);
    setPrevSearch(currentSearch);
    
    if (!(routeInfo.route === 'article' && routeInfo.slug)) {
      setCurrentArticle(null);
    }
    if (routeInfo.route === 'new') {
      setEditorSlug('');
      setEditorTitle(routeInfo.prefillTitle || '');
      setEditorContent('# ' + (routeInfo.prefillTitle || 'New Page') + '\n\nStart typing content here...');
      setIsEditing(true);
    }
  }

  // Synchronize document dark mode class
  useEffect(() => {
    if (darkMode) {
      document.documentElement.classList.add('dark');
    } else {
      document.documentElement.classList.remove('dark');
    }
  }, [darkMode]);

  // Custom navigate routing helper
  const navigate = (fullUrl: string) => {
    const cleanUrl = fullUrl.startsWith('/') ? fullUrl : '/' + fullUrl;
    window.history.pushState(null, '', cleanUrl);
    
    const [path, search] = cleanUrl.split('?');
    setCurrentPath(path);
    setCurrentSearch(search ? '?' + search : '');
    setIsEditing(false); // Close editor on navigation
  };

  // Unified smart navigation handler
  const handleNavigate = (target: string) => {
    if (target === 'home') {
      navigate('/');
    } else if (target.startsWith('new') || target.startsWith('/new')) {
      navigate(target);
    } else if (target.startsWith('search') || target.startsWith('/search')) {
      navigate(target);
    } else {
      navigate(`/articles/${target}`);
    }
  };

  // Sync routing state on browser back/forward buttons
  useEffect(() => {
    const handlePopState = () => {
      setCurrentPath(window.location.pathname);
      setCurrentSearch(window.location.search);
      setIsEditing(false);
    };
    window.addEventListener('popstate', handlePopState);
    return () => window.removeEventListener('popstate', handlePopState);
  }, []);

  // Fetch all articles metadata on load
  const fetchArticles = async (selectSlugAfter?: string) => {
    try {
      const response = await fetch('/api/articles');
      if (!response.ok) {
        triggerAlert('error', 'Failed to load articles index');
        return;
      }
      const data = await response.json();
      setArticles(data || []);

      if (selectSlugAfter) {
        navigate(`/articles/${selectSlugAfter}`);
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed to sync index directory';
      triggerAlert('error', msg);
    }
  };

  // Retrieve single full article content
  const fetchArticleContent = async (slug: string) => {
    setTimeout(() => {
      setIsArticleLoading(true);
    }, 0);
    try {
      const response = await fetch(`/api/articles/${slug}`);
      if (!response.ok) {
        triggerAlert('error', 'Article details not found on server');
        setCurrentArticle(null);
        return;
      }
      const data = await response.json();
      setCurrentArticle(data);
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed to load article content';
      triggerAlert('error', msg);
      setCurrentArticle(null);
    } finally {
      setIsArticleLoading(false);
    }
  };

  // Initial loading boots
  useEffect(() => {
    const bootApp = async () => {
      // Fetch dynamic custom name/title configuration
      try {
        const configRes = await fetch('/api/config');
        if (configRes.ok) {
          const configData = await configRes.json();
          if (configData.wiki_name) {
            setWikiName(configData.wiki_name);
          }
        }
      } catch (err) {
        console.error('Failed to load wiki title configurations:', err);
      }

      await fetchArticles();
      setIsLoading(false);
    };
    void bootApp();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Load article contents dynamically based on route active slug
  useEffect(() => {
    if (routeInfo.route === 'article' && routeInfo.slug) {
      setTimeout(() => {
        void fetchArticleContent(routeInfo.slug);
      }, 0);
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentPath]);

  // Toggle dark/light theme
  const toggleDarkMode = () => {
    if (darkMode) {
      document.documentElement.classList.remove('dark');
      localStorage.setItem('theme', 'light');
      setDarkMode(false);
    } else {
      document.documentElement.classList.add('dark');
      localStorage.setItem('theme', 'dark');
      setDarkMode(true);
    }
  };

  // CRUD: Saving Article edits/creates
  const handleSaveArticle = async (title: string, content: string, editSummary: string) => {
    const targetSlug = editorSlug; // empty if new
    const isNew = targetSlug === '';
    const newComputedSlug = Slugify(title);

    const payload = { 
      title, 
      content,
      edit_summary: editSummary,
      loaded_version: currentArticle ? currentArticle.version : 0
    };
    const url = isNew ? '/api/articles' : `/api/articles/${targetSlug}`;
    const method = isNew ? 'POST' : 'PUT';

    const response = await fetch(url, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });

    if (!response.ok) {
      const errData = await response.json();
      throw new Error(errData.error || 'Failed to persist article');
    }

    triggerAlert('success', `Article "${title}" saved successfully!`);
    setIsEditing(false);
    
    // Refresh index list and automatically redirect to the saved article
    await fetchArticles(newComputedSlug);
  };

  // CRUD: Delete active article
  const handleDeleteArticle = async () => {
    if (!currentArticle) return;
    
    const confirmDelete = window.confirm(
      `Are you absolutely sure you want to delete "${currentArticle.title}"?\nAll embedded files/images and page resources will be permanently removed from disk.`
    );
    if (!confirmDelete) return;

    try {
      const response = await fetch(`/api/articles/${currentArticle.slug}`, {
        method: 'DELETE',
      });

      if (!response.ok) {
        const errData = await response.json();
        triggerAlert('error', errData.error || 'Failed to delete article');
        return;
      }

      triggerAlert('success', `Article "${currentArticle.title}" and assets deleted successfully.`);
      setCurrentArticle(null);
      
      // Refresh list and navigate to homepage dashboard
      await fetchArticles();
      navigate('/');
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Deletion failed';
      triggerAlert('error', msg);
    }
  };

  // Trigger editing modes
  const handleTriggerEdit = () => {
    if (!currentArticle) return;
    setEditorSlug(currentArticle.slug);
    setEditorTitle(currentArticle.title);
    setEditorContent(currentArticle.content || '');
    setIsEditing(true);
  };

  // View renderer formatting dates
  const formatDate = (dateStr?: string) => {
    if (!dateStr) return '';
    return new Date(dateStr).toLocaleDateString(undefined, {
      year: 'numeric',
      month: 'long',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit'
    });
  };

  // Loading Screen Template
  if (isLoading) {
    return (
      <div className="w-screen h-screen flex flex-col items-center justify-center bg-slate-50 dark:bg-slate-950 text-slate-800 dark:text-white transition-colors select-none">
        <Loader2 className="animate-spin text-indigo-600 dark:text-emerald-400 mb-4" size={40} />
        <h2 className="text-base font-bold tracking-tight">Syncing Wiki Workspace...</h2>
      </div>
    );
  }

  return (
    <div className="w-screen h-screen flex overflow-hidden">
      
      {/* Alert banner overlay */}
      {alertMsg && (
        <div className={`fixed top-4 right-4 z-[99] px-6 py-4 rounded-2xl shadow-xl backdrop-blur-md border animate-fade-in text-sm font-semibold flex items-center gap-2 ${
          alertMsg.type === 'success'
            ? 'bg-emerald-50/90 dark:bg-emerald-950/40 border-emerald-200 dark:border-emerald-900 text-emerald-600 dark:text-emerald-400'
            : 'bg-rose-50/90 dark:bg-rose-950/40 border-rose-200 dark:border-rose-900 text-rose-600 dark:text-rose-400'
        }`}>
          <span>{alertMsg.text}</span>
        </div>
      )}

      {/* Sidebar Component */}
      <Sidebar
        articles={articles}
        currentSlug={routeInfo.slug || (routeInfo.route === 'home' ? 'home' : '')}
        darkMode={darkMode}
        onToggleDarkMode={toggleDarkMode}
        onNavigate={handleNavigate}
        onCreateNew={() => navigate('/new')}
        wikiName={wikiName}
      />

      {/* Main Content Area */}
      {isEditing ? (
        // Premium Markdown Split Editor Pane
        <Editor
          initialTitle={editorTitle}
          initialContent={editorContent}
          slug={editorSlug}
          onSave={handleSaveArticle}
          onCancel={() => {
            setIsEditing(false);
            if (isNewRequest(currentPath)) {
              navigate('/');
            }
          }}
        />
      ) : routeInfo.route === 'home' ? (
        // Homepage welcoming Dashboard Hero
        <Hero
          articles={articles}
          onNavigate={handleNavigate}
          onCreateNew={() => navigate('/new')}
          wikiName={wikiName}
        />
      ) : routeInfo.route === 'search' ? (
        // Dedicated Google-Style Search Results View
        <SearchResults
          initialQuery={routeInfo.searchQuery || ''}
          onNavigate={handleNavigate}
          wikiName={wikiName}
        />
      ) : routeInfo.route === '404' ? (
        // 404 Page Template
        <div className="flex-1 h-screen flex flex-col items-center justify-center bg-slate-50 dark:bg-slate-950/40 p-8 select-none">
          <BookOpen size={48} className="text-slate-300 dark:text-slate-700 animate-bounce mb-4" />
          <h1 className="text-2xl font-black text-slate-800 dark:text-white">Page Not Found</h1>
          <p className="text-sm text-slate-500 dark:text-slate-400 mt-2">The URL route you accessed does not map to any active wiki article.</p>
          <button
            onClick={() => navigate('/')}
            className="mt-6 py-2 px-4 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-bold text-sm active:scale-95 transition-all shadow-md shadow-indigo-100 dark:shadow-none"
          >
            Go to Welcome Page
          </button>
        </div>
      ) : (
        // Standard Wiki Article View Pane
        <div className="flex-1 h-screen flex overflow-hidden">
          
          {/* Main article reader column */}
          <div className="flex-1 overflow-y-auto h-full px-8 py-10 sm:px-12 md:px-16 bg-white dark:bg-slate-950/20">
            {isArticleLoading ? (
              // Article Loading Skeleton
              <div className="max-w-2xl mx-auto space-y-6 animate-pulse select-none">
                <div className="h-10 bg-slate-200 dark:bg-slate-800 rounded-xl w-3/4"></div>
                <div className="flex gap-4">
                  <div className="h-4 bg-slate-200 dark:bg-slate-800 rounded-md w-1/4"></div>
                  <div className="h-4 bg-slate-200 dark:bg-slate-800 rounded-md w-1/4"></div>
                </div>
                <hr className="border-slate-200 dark:border-slate-800 my-6" />
                <div className="space-y-3">
                  <div className="h-4 bg-slate-200 dark:bg-slate-800 rounded-md w-full"></div>
                  <div className="h-4 bg-slate-200 dark:bg-slate-800 rounded-md w-11/12"></div>
                  <div className="h-4 bg-slate-200 dark:bg-slate-800 rounded-md w-4/5"></div>
                  <div className="h-4 bg-slate-200 dark:bg-slate-800 rounded-md w-full"></div>
                </div>
              </div>
            ) : currentArticle ? (
              // Active Article Layout
              <article className="max-w-2xl mx-auto space-y-6">
                
                {/* Article Header controls */}
                <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b border-slate-200/60 dark:border-slate-800/60 pb-6 select-none">
                  
                  {/* Title & Metadata */}
                  <div className="space-y-2">
                    <h1 className="text-4xl font-extrabold text-slate-900 dark:text-white tracking-tight leading-tight">
                      {currentArticle.title}
                    </h1>
                    <div className="flex flex-wrap gap-4 text-[10px] text-slate-400 dark:text-slate-500 font-semibold tracking-wide uppercase">
                      <span className="flex items-center gap-1">
                        <Calendar size={11} className="text-indigo-400" />
                        Created {formatDate(currentArticle.created_at)}
                      </span>
                      {currentArticle.updated_at && currentArticle.updated_at !== currentArticle.created_at && (
                        <span className="flex items-center gap-1">
                          <Clock size={11} className="text-emerald-400" />
                          Edited {formatDate(currentArticle.updated_at)}
                        </span>
                      )}
                    </div>
                  </div>
                  {/* Actions buttons */}
                  <div className="flex items-center gap-2 self-start sm:self-center shrink-0">
                    <button
                      onClick={handleTriggerEdit}
                      className="flex items-center gap-1.5 py-2 px-3.5 rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 hover:bg-slate-55 dark:hover:bg-slate-850 text-slate-700 dark:text-slate-300 font-semibold text-xs shadow-sm hover:scale-[1.02] active:scale-95 transition-all duration-200"
                    >
                      <Edit size={12} />
                      <span>Edit Page</span>
                    </button>
                    <button
                      onClick={() => setHistoryOpen(true)}
                      className="flex items-center gap-1.5 py-2 px-3.5 rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 hover:bg-slate-55 dark:hover:bg-slate-850 text-slate-700 dark:text-slate-300 font-semibold text-xs shadow-sm hover:scale-[1.02] active:scale-95 transition-all duration-200"
                    >
                      <Clock size={12} className="text-indigo-500" />
                      <span>History</span>
                    </button>
                    <button
                      onClick={handleDeleteArticle}
                      className="flex items-center gap-1.5 py-2 px-3.5 rounded-xl border border-rose-200 dark:border-rose-950/60 bg-rose-50/50 dark:bg-rose-950/10 hover:bg-rose-50 dark:hover:bg-rose-950/20 text-rose-600 dark:text-rose-400 font-semibold text-xs hover:scale-[1.02] active:scale-95 transition-all duration-200"
                    >
                      <Trash2 size={12} />
                      <span>Delete</span>
                    </button>
                  </div>
                </div>

                {/* Rendered Markdown Body Content */}
                <div className="pb-16 animate-fade-in">
                  <Viewer
                    content={currentArticle.content || ''}
                    onNavigate={handleNavigate}
                    articles={articles}
                  />
                </div>
              </article>
            ) : (
              // Fallback Article Not Found
              <div className="max-w-2xl mx-auto py-12 text-center select-none">
                <h2 className="text-lg font-bold text-slate-800 dark:text-white">Article could not be loaded</h2>
                <p className="text-sm text-slate-500 dark:text-slate-400 mt-2">The requested article body is either blank or was deleted from the disk.</p>
                <button
                  onClick={() => navigate('/')}
                  className="mt-6 py-2 px-4 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-semibold text-sm transition-colors"
                >
                  Return to Dashboard
                </button>
              </div>
            )}
          </div>

          {/* Sticky Table of Contents (TOC) Column */}
          {!isArticleLoading && currentArticle && currentArticle.content && (
            <TOC content={currentArticle.content} />
          )}

        </div>
      )}

      {historyOpen && currentArticle && (
        <HistoryDrawer
          slug={currentArticle.slug}
          currentContent={currentArticle.content || ''}
          currentTitle={currentArticle.title}
          onClose={() => setHistoryOpen(false)}
          onRevertComplete={async () => {
            setHistoryOpen(false);
            triggerAlert('success', `Article "${currentArticle.title}" reverted successfully!`);
            await fetchArticleContent(currentArticle.slug);
            await fetchArticles();
          }}
        />
      )}
    </div>
  );
};



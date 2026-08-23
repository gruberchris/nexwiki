import React, { useState, useEffect, useMemo, useCallback } from 'react';
import type { Article } from './types';
import { ContentTypes, isAgentDoc, isSkill, isPlan, typeLabel } from './types';
import { Sidebar } from './components/Sidebar';
import { Viewer } from './components/Viewer';
import { BacklinksPanel } from './components/BacklinksPanel';
import { Editor } from './components/Editor';
import { TOC } from './components/TOC';
import { Hero } from './components/Hero';
import { SearchResults } from './components/SearchResults';
import { Slugify, formatRelativeTime } from './utils';
import { statusBadgeClass } from './statusTags';
import { HistoryDrawer } from './components/HistoryDrawer';
import { ThemeManagerModal } from './components/ThemeManagerModal';
import { useSSE } from './hooks/useSSE';
import { useWikiUpdates } from './hooks/useWikiUpdates';
import { useTheme } from './hooks/useTheme';
import { useArticleActions } from './hooks/useArticleActions';
import { useRouter, parseRoute } from './hooks/useRouter';
import { ActivityLogDrawer } from './components/ActivityLogDrawer';
import { 
  Edit, 
  Trash2, 
  Loader2,
  Calendar,
  Clock,
  BookOpen,
  Copy,
  Check,
  Share2,
  ChevronDown,
  FileText,
  Printer,
  FileDown,
  Wrench,
  ClipboardList,
  ChevronLeft,
  ChevronRight
} from 'lucide-react';

// Simple check to identify new page creation urls
function isNewRequest(path: string): boolean {
  return path.startsWith('/new');
}

export const App: React.FC = () => {
  // Articles state
  const [articles, setArticles] = useState<Article[]>([]);
  const [currentArticle, setCurrentArticle] = useState<Article | null>(null);
  
  // Editor state
  const [isEditing, setIsEditing] = useState(false);
  const [editorSlug, setEditorSlug] = useState('');
  const [editorTitle, setEditorTitle] = useState('');
  const [editorContent, setEditorContent] = useState('');
  const [editorTags, setEditorTags] = useState<string[]>([]);
  const [historyOpen, setHistoryOpen] = useState(false);

  // Hand-rolled routing: history, back/forward, and URL parsing.
  const closeEditorOnRouteChange = useCallback(() => setIsEditing(false), []);
  const { currentPath, currentSearch, navigationKind, navigate, navigateTo: handleNavigate } =
    useRouter(closeEditorOnRouteChange);

  

  // UI state
  const [isLoading, setIsLoading] = useState(true);
  const [isArticleLoading, setIsArticleLoading] = useState(false);
  const [alertMsg, setAlertMsg] = useState<{ type: 'success' | 'error', text: string } | null>(null);
  const [wikiName, setWikiName] = useState('NexWiki');
  const [version, setVersion] = useState('0.1.0');


  // Activity Log states
  const [isActivityOpen, setIsActivityOpen] = useState(false);

  // Sidebar visibility state
  const [isSidebarOpen, setIsSidebarOpen] = useState(true);

  // Alert notifier triggers
  const triggerAlert = (type: 'success' | 'error', text: string) => {
    setAlertMsg({ type, text });
    setTimeout(() => {
      setAlertMsg(null);
    }, 4000);
  };

  // All look-and-feel state (mode, palette, CSS variables) lives in useTheme.
  const {
    themes, activeThemeName, themeMode, darkMode,
    themeModalOpen, setThemeModalOpen,
    cycleThemeMode, selectTheme, saveTheme, deleteTheme,
    initialize: initializeTheme,
  } = useTheme(triggerAlert);

  // Clipboard, export, and backup/restore actions.
  const {
    shareDropdownOpen, setShareDropdownOpen,
    copiedMd, copiedUrl, copiedTitle,
    copyMarkdown, copyShareLink, copyTitle,
    exportPDF, exportDocx, exportMarkdown, exportAll,
    importFileRef, triggerImport, handleImportFileChange,
  } = useArticleActions({
    currentArticle,
    onAlert: triggerAlert,
    onArticlesImported: () => fetchArticles(),
  });

	 const { resetUnreadCount } = useSSE();

  // Synchronize browser tab document title with configured wiki name
  useEffect(() => {
    document.title = `${wikiName} — Personal Knowledge Engine`;
  }, [wikiName]);

  // Route shape comes from parseRoute; whether the slug actually exists depends on the loaded
  // article list, which parsing cannot know, so that refinement is applied here.
  const routeInfo = useMemo(() => {
    const parsed = parseRoute(currentPath, currentSearch);
    if (parsed.route === 'article' && !isLoading && !articles.some(art => art.slug === parsed.slug)) {
      return { route: '404', slug: '' } as typeof parsed;
    }
    return parsed;
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
      
      // Reserved AI-Agent-* types are assigned only by the agent tools, so UI-created documents are
      // always Wiki articles. The plan/skill scaffolds remain as content templates without class tags.
      const initialTags: string[] = [];
      let defaultContent: string;
      let defaultTitle = routeInfo.prefillTitle || '';

      if (routeInfo.prefillType === 'plan') {
        defaultContent = `# New Collaborative Plan\n\n## Overview\nProvide a description of the goal and milestones.\n\n## Tasks\n- [ ] Task 1: Audit codebase\n- [ ] Task 2: Implement core logic\n- [ ] Task 3: Run validation tests`;
        if (!defaultTitle) defaultTitle = 'New Collaborative Plan';
      } else if (routeInfo.prefillType === 'skill') {
        defaultContent = `# New Custom AI Skill\n\n## Overview\nGuides the agent on how to perform a specialized task.\n\n## When to Use\nDescribe the triggers for this skill.\n\n## Instructions\n1. Step 1\n2. Step 2`;
        if (!defaultTitle) defaultTitle = 'New Custom AI Skill';
      } else {
        if (!defaultTitle) defaultTitle = 'New Page';
        defaultContent = '# ' + defaultTitle + '\n\nStart typing content here...';
      }

      setEditorTitle(defaultTitle);
      setEditorContent(defaultContent);
      setEditorTags(initialTags);
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

  // Synchronize list/stats reactively over SSE (Phase 6)
  useWikiUpdates((update) => {
    console.log('SSE Update received:', update);
    // Refresh articles listing dynamically
    void fetchArticles();

    // If currently reading the updated article, refresh its body content
    if (routeInfo.route === 'article' && routeInfo.slug === update.slug && update.type === 'article-edited') {
      void fetchArticleContent(update.slug);
    }
  });

  // Synchronize CSS custom properties for active theme and variant
  // Initial loading boots
  useEffect(() => {
    const bootApp = async () => {
      // 1. Fetch dynamic custom name/title and theme configuration
      let defaultTheme = 'default';
      let scheduledTheme = '';
      let themeSchedulingEnabled = false;
      try {
        const configRes = await fetch('/api/config');
        if (configRes.ok) {
          const configData = await configRes.json();
          if (configData.wiki_name) {
            setWikiName(configData.wiki_name);
          }
          if (configData.version) {
            setVersion(configData.version);
          }
          if (configData.default_theme) {
            defaultTheme = configData.default_theme;
          }
          if (configData.theme_scheduling_enabled) {
            themeSchedulingEnabled = configData.theme_scheduling_enabled;
          }
          if (configData.scheduled_theme) {
            scheduledTheme = configData.scheduled_theme;
          }
        }
      } catch (err) {
        console.error('Failed to load wiki configurations:', err);
      }

      // 2-4. Load palettes and resolve the active theme (scheduling > saved > server default).
      await initializeTheme({
        defaultTheme,
        scheduledTheme,
        schedulingEnabled: themeSchedulingEnabled,
      });

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

  // CRUD: Saving Article edits/creates
  const handleSaveArticle = async (title: string, content: string, editSummary: string, tags: string[], description: string, source: string, resource: string, status: string) => {
    const targetSlug = editorSlug; // empty if new
    const isNew = targetSlug === '';
    const newComputedSlug = Slugify(title);

    const payload = {
      title,
      content,
      description,
      source,
      resource,
      edit_summary: editSummary,
      loaded_version: currentArticle ? currentArticle.version : 0,
      tags,
      status
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
    await fetchArticleContent(newComputedSlug);
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

  // Clipboard MD copying
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

      <input ref={importFileRef} type="file" accept=".zip" className="hidden" onChange={handleImportFileChange} />

      {/* Alert banner overlay */}
      {alertMsg && (
        <div className={`fixed top-4 right-4 z-[99] px-6 py-4 rounded-2xl shadow-xl backdrop-blur-md border animate-fade-in text-sm font-semibold flex items-center gap-2 no-print ${
          alertMsg.type === 'success'
            ? 'bg-emerald-50/90 dark:bg-emerald-950/40 border-emerald-200 dark:border-emerald-900 text-emerald-600 dark:text-emerald-400'
            : 'bg-rose-50/90 dark:bg-rose-950/40 border-rose-200 dark:border-rose-900 text-rose-600 dark:text-rose-400'
        }`}>
          <span>{alertMsg.text}</span>
        </div>
      )}

      {/* Sidebar Component with Sliding Double-Div Transition */}
      <div 
        className={`no-print transition-all duration-300 ease-in-out flex-shrink-0 relative z-30 ${
          isSidebarOpen ? 'w-80' : 'w-0'
        }`}
      >
        <div 
          className={`h-full transition-transform duration-300 ease-in-out relative ${
            isSidebarOpen ? 'translate-x-0' : '-translate-x-full'
          }`}
          style={{ width: '20rem' }}
        >
          <Sidebar
            articles={articles}
            currentSlug={routeInfo.slug || (routeInfo.route === 'home' ? 'home' : '')}
            themeMode={themeMode}
            onCycleThemeMode={cycleThemeMode}
            onOpenThemeManager={() => setThemeModalOpen(true)}
            onNavigate={handleNavigate}
            onCreateNew={(type: 'article' | 'plan' | 'skill') => navigate(`/new?type=${type}`)}
            wikiName={wikiName}
            onExportAll={exportAll}
            onImport={triggerImport}
            onOpenActivityLog={() => setIsActivityOpen(true)}
            version={version}
          />

          {/* Subtle Border-Aligned Slide Toggle Button */}
          <button
            onClick={() => setIsSidebarOpen(prev => !prev)}
            className="absolute top-1/2 -right-3.5 transform -translate-y-1/2 z-50 w-7 h-7 rounded-full bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 text-slate-500 hover:text-themeAccent dark:hover:text-themeAccent shadow-md hover:scale-110 active:scale-95 transition-all duration-200 cursor-pointer flex items-center justify-center no-print"
            title={isSidebarOpen ? "Minimize Sidebar" : "Expand Sidebar"}
            aria-label={isSidebarOpen ? "Minimize Sidebar" : "Expand Sidebar"}
          >
            {isSidebarOpen ? <ChevronLeft size={14} /> : <ChevronRight size={14} />}
          </button>
        </div>
      </div>

      {/* Main Content Area */}
      {isEditing ? (
        // Premium Markdown Split Editor Pane
        <Editor
          key={editorSlug || currentPath + currentSearch}
          initialTitle={editorTitle}
          initialContent={editorContent}
          initialTags={editorSlug === '' ? editorTags : (currentArticle ? currentArticle.tags : [])}
          initialStatus={editorSlug !== '' && currentArticle ? currentArticle.status : ''}
          initialDescription={editorSlug !== '' && currentArticle ? currentArticle.description : ''}
          initialSource={editorSlug !== '' && currentArticle ? currentArticle.source : ''}
          initialResource={editorSlug !== '' && currentArticle ? currentArticle.resource : ''}
          articleType={editorSlug !== '' && currentArticle ? currentArticle.type : undefined}
          slug={editorSlug}
          onSave={handleSaveArticle}
          onCancel={() => {
            setIsEditing(false);
            if (isNewRequest(currentPath)) {
              navigate('/');
            }
          }}
          articles={articles}
          version={currentArticle ? currentArticle.version : undefined}
        />
      ) : routeInfo.route === 'home' ? (
        // Homepage welcoming Dashboard Hero
        <Hero
          articles={articles}
          onNavigate={handleNavigate}
          onCreateNew={(type: 'article' | 'plan' | 'skill') => navigate(`/new?type=${type}`)}
          wikiName={wikiName}
          // Back/forward and reloads restore the dashboard as it was left; clicking Home
          // deliberately gives a clean one.
          restoreUiState={navigationKind !== 'push'}
        />
      ) : routeInfo.route === 'search' ? (
        // Dedicated Google-Style Search Results View
        <SearchResults
          initialQuery={routeInfo.searchQuery || ''}
          onNavigate={handleNavigate}
          wikiName={wikiName}
        />
      ) : routeInfo.route === '404' ? (
        // 404-Page Template
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
        <div className="flex-1 h-screen flex overflow-hidden min-w-0">
          
          {/* Main article reader column */}
          <div className="flex-1 overflow-y-auto h-full px-8 py-10 sm:px-12 md:px-16 bg-white dark:bg-slate-950/20">
            {isArticleLoading ? (
              // Article Loading Skeleton
              <div className="max-w-2xl lg:max-w-3xl 2xl:max-w-5xl mx-auto space-y-6 animate-pulse select-none">
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
              // The reading column grows with the viewport instead of staying a 672px ribbon on
              // large displays, but keeps a cap: unbounded line length hurts readability too.
              // The skeleton and not-found fallback use the same ladder so nothing reflows.
              <article className="max-w-2xl lg:max-w-3xl 2xl:max-w-5xl mx-auto space-y-6">
                
                {/* Article Header controls */}
                <div className="flex flex-col gap-4 border-b border-slate-200/60 dark:border-slate-800/60 pb-6 select-none no-print">
                  
                  {/* Title & Metadata */}
                  <div className="space-y-2">
                    <div className="flex items-center gap-2">
                      <h1 className="text-4xl font-extrabold text-slate-900 dark:text-white tracking-tight leading-tight">
                        {currentArticle.title}
                      </h1>
                      <button
                        onClick={copyTitle}
                        className="p-1 rounded hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors shrink-0"
                        title="Copy article title to clipboard"
                      >
                        {copiedTitle ? <Check size={16} className="text-emerald-500" /> : <Copy size={16} />}
                      </button>
                    </div>
                    {currentArticle.description && (
                      <p className="text-sm text-slate-500 dark:text-slate-400 leading-snug">
                        {currentArticle.description}
                      </p>
                    )}
                    {currentArticle.source && (
                      <p className="text-[11px] text-slate-400 dark:text-slate-500">
                        Source:{' '}
                        {/^https?:\/\//i.test(currentArticle.source) ? (
                          <a
                            href={currentArticle.source}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="text-indigo-500 dark:text-indigo-400 hover:underline break-all"
                          >
                            {currentArticle.source}
                          </a>
                        ) : (
                          <span className="break-all">{currentArticle.source}</span>
                        )}
                      </p>
                    )}
                    <div className="flex flex-wrap gap-4 text-[10px] text-slate-400 dark:text-slate-500 font-semibold tracking-wide uppercase">
                      <span className="flex items-center gap-1">
                        <Calendar size={11} className="text-indigo-400" />
                        Created {formatDate(currentArticle.created_at)}
                      </span>
                      {currentArticle.timestamp && currentArticle.timestamp !== currentArticle.created_at && (
                        <span className="flex items-center gap-1">
                          <Clock size={11} className="text-emerald-400" />
                          V{currentArticle.version || 1} Edited {formatDate(currentArticle.timestamp)}
                        </span>
                      )}
                      {/* An approaching auto-archive should be visible rather than a surprise, so
                          a plan's header says how long it has held its current status. */}
                      {isPlan(currentArticle) && currentArticle.status_changed_at && currentArticle.status && (
                        <span className="flex items-center gap-1">
                          <ClipboardList size={11} className="text-teal-400" />
                          {currentArticle.status} since {formatRelativeTime(currentArticle.status_changed_at)}
                        </span>
                      )}
                    </div>
                    {/* Read-only type badge + status + tag badges */}
                    {(isAgentDoc(currentArticle) || currentArticle.status || (currentArticle.tags && currentArticle.tags.length > 0)) && (
                      <div className="flex flex-wrap gap-1.5 mt-3 select-none">
                        {currentArticle.status && (
                          <span
                            title="Lifecycle status"
                            className={`inline-flex items-center gap-1.5 text-[10px] font-bold px-2.5 py-0.5 rounded-full shadow-xs ${statusBadgeClass(currentArticle.status)}`}
                          >
                            {currentArticle.status}
                          </span>
                        )}
                        {isAgentDoc(currentArticle) && (
                          <span
                            title="Document type (set by the agent tools)"
                            className="inline-flex items-center gap-1.5 text-[10px] font-bold px-2.5 py-0.5 rounded-full bg-indigo-500/10 dark:bg-emerald-400/10 border border-indigo-500/30 dark:border-emerald-400/30 text-indigo-650 dark:text-emerald-400 shadow-xs"
                          >
                            {currentArticle.type === ContentTypes.Skill ? <Wrench size={10} className="shrink-0" />
                              : currentArticle.type === ContentTypes.Plan ? <ClipboardList size={10} className="shrink-0" />
                              : <span className="w-1.5 h-1.5 rounded-full bg-indigo-500 dark:bg-emerald-400"></span>}
                            {typeLabel(currentArticle.type)}
                          </span>
                        )}
                        {currentArticle.tags && currentArticle.tags.map(tag => {
                          const isScopeTag = tag.toLowerCase().startsWith('memory-');
                          return isScopeTag ? (
                            <span
                              key={tag}
                              title="Tool-managed memory-scope tag"
                              className="inline-flex items-center gap-1.5 text-[10px] font-bold px-2.5 py-0.5 rounded-full bg-indigo-500/10 dark:bg-emerald-400/10 border border-indigo-500/30 dark:border-emerald-400/30 text-indigo-650 dark:text-emerald-400 shadow-xs"
                            >
                              <span className="w-1.5 h-1.5 rounded-full bg-indigo-500 dark:bg-emerald-400"></span>
                              {tag}
                            </span>
                          ) : (
                            <span
                              key={tag}
                              className="inline-flex items-center gap-1.5 text-[10px] font-semibold px-2.5 py-0.5 rounded-full bg-slate-50 dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 text-slate-500 dark:text-slate-400 shadow-xs"
                            >
                              <span className="w-1.5 h-1.5 rounded-full bg-slate-400 dark:bg-slate-600"></span>
                              {tag}
                            </span>
                          );
                        })}
                      </div>
                    )}
                  </div>
                  {/* Actions buttons */}
                  <div className="flex items-center justify-end gap-2 self-stretch no-print mt-1">
                    <button
                      onClick={handleTriggerEdit}
                      className="flex items-center gap-1.5 py-2 px-3.5 rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800/80 text-slate-700 dark:text-slate-300 font-semibold text-xs shadow-xs hover:scale-[1.02] active:scale-95 transition-all duration-200 cursor-pointer"
                    >
                      <Edit size={12} />
                      <span>Edit Page</span>
                    </button>
                    <button
                      onClick={() => setHistoryOpen(true)}
                      className="flex items-center gap-1.5 py-2 px-3.5 rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800/80 text-slate-700 dark:text-slate-300 font-semibold text-xs shadow-xs hover:scale-[1.02] active:scale-95 transition-all duration-200 cursor-pointer"
                    >
                      <Clock size={12} className="text-indigo-500" />
                      <span>History</span>
                    </button>

                    {/* Share & Export Dropdown */}
                    <div className="relative share-dropdown-container">
                      <button
                        onClick={() => setShareDropdownOpen(!shareDropdownOpen)}
                        className={`flex items-center gap-1.5 py-2 px-3.5 rounded-xl border font-semibold text-xs shadow-xs hover:scale-[1.02] active:scale-95 transition-all duration-200 cursor-pointer ${
                          shareDropdownOpen
                            ? 'border-indigo-500 bg-indigo-50/50 dark:bg-indigo-950/20 text-indigo-650 dark:text-indigo-400'
                            : 'border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 hover:bg-slate-50 dark:hover:bg-slate-800/80 text-slate-700 dark:text-slate-300'
                        }`}
                      >
                        <Share2 size={12} />
                        <span>Share & Export</span>
                        <ChevronDown size={10} className={`transition-transform duration-200 ${shareDropdownOpen ? 'rotate-180' : ''}`} />
                      </button>

                      {shareDropdownOpen && (
                        <div className="dropdown-menu">
                          <button
                            onClick={copyMarkdown}
                            className="dropdown-item"
                          >
                            {copiedMd ? <Check size={12} className="text-emerald-500 animate-pulse" /> : <Copy size={12} className="text-indigo-500" />}
                            <span>{copiedMd ? 'Copied Markdown!' : 'Copy Markdown'}</span>
                          </button>
                          <button
                            onClick={copyShareLink}
                            className="dropdown-item"
                          >
                            {copiedUrl ? <Check size={12} className="text-emerald-500 animate-pulse" /> : <Share2 size={12} className="text-indigo-500" />}
                            <span>{copiedUrl ? 'Copied Link!' : 'Copy Share Link'}</span>
                          </button>
                          
                          <div className="my-1 border-t border-slate-100 dark:border-slate-800/40" />
                          
                          <button
                            onClick={exportPDF}
                            className="dropdown-item"
                          >
                            <Printer size={12} className="text-indigo-500" />
                            <span>Export as PDF</span>
                          </button>
                          <button
                            onClick={exportDocx}
                            className="dropdown-item"
                          >
                            <FileText size={12} className="text-indigo-500" />
                            <span>Export as Word</span>
                          </button>
                          <button
                            onClick={exportMarkdown}
                            className="dropdown-item"
                          >
                            <FileDown size={12} className="text-indigo-500" />
                            <span>Export as Markdown</span>
                          </button>
                        </div>
                      )}
                    </div>

                    {/* Compact Delete button */}
                    <button
                      onClick={handleDeleteArticle}
                      title="Delete Article"
                      className="flex items-center justify-center p-2 rounded-xl border border-rose-200 dark:border-rose-950/60 bg-rose-50/50 dark:bg-rose-950/10 hover:bg-rose-100 dark:hover:bg-rose-950/30 text-rose-600 dark:text-rose-400 hover:scale-[1.02] active:scale-95 transition-all duration-200 cursor-pointer"
                    >
                      <Trash2 size={13} />
                    </button>
                  </div>
                </div>

                {/* Rendered Markdown Body Content */}
                <div className="pb-16 animate-fade-in space-y-6">
                  {isSkill(currentArticle) && (
                    <div className="p-5 rounded-2xl bg-gradient-to-tr from-indigo-500/5 to-purple-500/5 border border-indigo-500/25 dark:border-indigo-500/15 text-slate-700 dark:text-slate-300 shadow-xs flex flex-col sm:flex-row gap-4 items-start sm:items-center justify-between no-print select-none backdrop-blur-sm">
                      <div className="flex items-center gap-3">
                        <div className="p-2 rounded-xl bg-indigo-500/10 dark:bg-indigo-400/10 text-indigo-600 dark:text-indigo-400 animate-pulse shrink-0">
                          <Wrench size={18} />
                        </div>
                        <div>
                          <h4 className="text-xs font-black text-slate-900 dark:text-white flex items-center gap-1.5 uppercase tracking-wide">
                            AI Agent Skill Active
                          </h4>
                          <p className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5 leading-relaxed">
                            Exposed as a custom AI Agent skill registry. Agents can fetch and parse this skill page dynamically.
                          </p>
                        </div>
                      </div>
                      <div className="flex items-center gap-2 self-stretch sm:self-auto justify-end shrink-0">
                        <a
                          href={`/api/skills/${currentArticle.slug}`}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="px-3 py-1.5 rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 text-[10px] font-bold hover:bg-slate-50 dark:hover:bg-slate-800 text-slate-655 dark:text-slate-400 transition-all shadow-inner cursor-pointer"
                        >
                          JSON Schema
                        </a>
                        <a
                          href={`/api/skills/${currentArticle.slug}/raw`}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="px-3 py-1.5 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white text-[10px] font-bold transition-all shadow-md shadow-indigo-500/15 cursor-pointer"
                        >
                          Raw SKILL.md
                        </a>
                      </div>
                    </div>
                  )}

                  {isPlan(currentArticle) && (
                    <div className="p-5 rounded-2xl bg-gradient-to-tr from-emerald-500/5 to-teal-500/5 border border-emerald-500/25 dark:border-emerald-500/15 text-slate-700 dark:text-slate-300 shadow-xs flex flex-col sm:flex-row gap-4 items-start sm:items-center justify-between no-print select-none backdrop-blur-sm animate-fade-in">
                      <div className="flex items-center gap-3">
                        <div className="p-2 rounded-xl bg-emerald-500/10 dark:bg-emerald-400/10 text-emerald-600 dark:text-emerald-400 animate-pulse shrink-0">
                          <ClipboardList size={18} />
                        </div>
                        <div>
                          <h4 className="text-xs font-black text-slate-900 dark:text-white flex items-center gap-1.5 uppercase tracking-wide">
                            Collaborative AI Plan Active
                          </h4>
                          <p className="text-[10px] text-slate-500 dark:text-slate-400 mt-0.5 leading-relaxed">
                            This page is registered as an active AI Plan. You and your connected AI agent can collaboratively manage and execute this roadmap.
                          </p>
                        </div>
                      </div>
                    </div>
                  )}

                  <Viewer
                    content={currentArticle.content || ''}
                    onNavigate={handleNavigate}
                    articles={articles}
                  />

                  <BacklinksPanel slug={currentArticle.slug} onNavigate={handleNavigate} />
                </div>
              </article>
            ) : (
              // Fallback Article Not Found
              <div className="max-w-2xl lg:max-w-3xl 2xl:max-w-5xl mx-auto py-12 text-center select-none">
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
            <div className="no-print">
              <TOC content={currentArticle.content} />
            </div>
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

      {themeModalOpen && (
        <ThemeManagerModal
          isOpen={themeModalOpen}
          onClose={() => setThemeModalOpen(false)}
          themes={themes}
          activeThemeName={activeThemeName}
          onSelectTheme={selectTheme}
          onSaveTheme={saveTheme}
          onDeleteTheme={deleteTheme}
        />
      )}

      <ActivityLogDrawer
        isOpen={isActivityOpen}
        onClose={() => {
          setIsActivityOpen(false);
          resetUnreadCount();
        }}
        onNavigate={handleNavigate}
      />
    </div>
  );
};



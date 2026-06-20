import { useEffect, useState } from 'react';
import type { Article } from '../types';
import { Link2 } from 'lucide-react';

interface BacklinksPanelProps {
  slug: string;
  onNavigate: (slug: string) => void;
}

// BacklinksPanel fetches and renders the list of articles linking to the current page.
// Hidden entirely when the article has no inbound WikiLinks.
export function BacklinksPanel({ slug, onNavigate }: BacklinksPanelProps) {
  const [backlinks, setBacklinks] = useState<Article[]>([]);

  useEffect(() => {
    let cancelled = false;
    setBacklinks([]);
    fetch(`/api/articles/${slug}/backlinks`)
      .then((res) => (res.ok ? res.json() : []))
      .then((data: Article[]) => {
        if (!cancelled) setBacklinks(Array.isArray(data) ? data : []);
      })
      .catch(() => {
        if (!cancelled) setBacklinks([]);
      });
    return () => {
      cancelled = true;
    };
  }, [slug]);

  if (backlinks.length === 0) return null;

  return (
    <div className="mt-10 pt-5 border-t border-slate-200/60 dark:border-slate-800/60 no-print select-none">
      <h4 className="flex items-center gap-1.5 text-[10px] font-bold uppercase tracking-widest text-slate-400 dark:text-slate-500 mb-3">
        <Link2 size={11} />
        Linked from
      </h4>
      <div className="flex flex-wrap gap-2">
        {backlinks.map((bl) => (
          <button
            key={bl.slug}
            onClick={() => onNavigate(bl.slug)}
            title={bl.description || bl.title}
            className="text-xs px-2.5 py-1 rounded-full border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 text-indigo-600 dark:text-indigo-400 hover:bg-indigo-50 dark:hover:bg-slate-800 font-medium transition-colors cursor-pointer"
          >
            {bl.title}
          </button>
        ))}
      </div>
    </div>
  );
}

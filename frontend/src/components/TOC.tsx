import React, { useState, useEffect } from 'react';
import { Slugify } from '../utils';
import { AlignLeft } from 'lucide-react';

interface HeadingItem {
  text: string;
  level: number;
  id: string;
}

interface TOCProps {
  content: string;
}

// Strip YAML front matter block
function stripFrontMatter(md: string): string {
  if (!md.startsWith('---')) return md;
  const parts = md.split('---');
  if (parts.length >= 3) {
    return parts.slice(2).join('---');
  }
  return md;
}

// Parse markdown string to extract headings
function parseHeadings(markdown: string): HeadingItem[] {
  const cleanMarkdown = stripFrontMatter(markdown);
  const lines = cleanMarkdown.split('\n');
  const headings: HeadingItem[] = [];
  
  let inCodeBlock = false;

  for (let line of lines) {
    line = line.trim();
    
    // Track code blocks to ignore comments/headers inside them
    if (line.startsWith('```')) {
      inCodeBlock = !inCodeBlock;
      continue;
    }
    
    if (inCodeBlock) continue;

    // Matches # Header, ## Header, ### Header
    const headingMatch = line.match(/^(#{1,3})\s+(.+)$/);
    if (headingMatch) {
      const hashes = headingMatch[1];
      const text = headingMatch[2].replace(/[#*`_\[\]]/g, '').trim(); // Remove formatting symbols
      const level = hashes.length;
      const id = Slugify(text);
      
      if (id) {
        headings.push({ text, level, id });
      }
    }
  }

  return headings;
}

export const TOC: React.FC<TOCProps> = ({ content }) => {
  const [headings, setHeadings] = useState<HeadingItem[]>([]);
  const [activeId, setActiveId] = useState<string>('');

  useEffect(() => {
    setHeadings(parseHeadings(content));
  }, [content]);

  // Track active heading on scroll using IntersectionObserver
  useEffect(() => {
    if (headings.length === 0) return;

    const observerOptions = {
      root: null,
      rootMargin: '-80px 0px -60% 0px', // Highlights elements near the top of the viewport
      threshold: 0
    };

    const handleIntersection = (entries: IntersectionObserverEntry[]) => {
      // Find the first heading that is intersecting in our viewport target range
      const visible = entries.find(entry => entry.isIntersecting);
      if (visible) {
        setActiveId(visible.target.id);
      }
    };

    const observer = new IntersectionObserver(handleIntersection, observerOptions);

    // Observe each heading element currently in the DOM
    headings.forEach(heading => {
      const el = document.getElementById(heading.id);
      if (el) observer.observe(el);
    });

    return () => {
      headings.forEach(heading => {
        const el = document.getElementById(heading.id);
        if (el) observer.unobserve(el);
      });
      observer.disconnect();
    };
  }, [headings]);

  if (headings.length === 0) return null;

  return (
    <div className="w-64 shrink-0 hidden xl:block select-none select-none relative">
      <div className="sticky top-24 p-6 rounded-2xl glass-panel">
        <h4 className="text-xs font-bold text-slate-400 dark:text-slate-500 uppercase tracking-widest flex items-center gap-2 mb-4">
          <AlignLeft size={12} className="text-indigo-500 dark:text-emerald-400" />
          On this page
        </h4>
        
        <nav className="space-y-2 max-h-[calc(100vh-280px)] overflow-y-auto pr-1">
          {headings.map((heading) => {
            const isActive = activeId === heading.id;
            
            // Margins based on header indentation level
            const pl = heading.level === 1 
              ? 'pl-0 font-semibold' 
              : heading.level === 2 
                ? 'pl-3 text-xs' 
                : 'pl-6 text-[11px]';

            return (
              <a
                key={heading.id}
                href={`#${heading.id}`}
                onClick={(e) => {
                  e.preventDefault();
                  document.getElementById(heading.id)?.scrollIntoView({ behavior: 'smooth' });
                  setActiveId(heading.id);
                }}
                className={`block transition-all duration-150 truncate hover:text-slate-900 dark:hover:text-white ${pl} ${
                  isActive
                    ? 'text-indigo-600 dark:text-emerald-400 border-l-2 border-indigo-500 dark:border-emerald-400 pl-2 font-medium translate-x-0.5'
                    : 'text-slate-500 dark:text-slate-400 border-l border-transparent hover:translate-x-0.5'
                }`}
              >
                {heading.text}
              </a>
            );
          })}
        </nav>
      </div>
    </div>
  );
};

import React from 'react';
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import 'highlight.js/styles/github-dark-dimmed.css';
import type { Article } from '../types';
import { Slugify } from '../utils';

interface ViewerProps {
  content: string;
  onNavigate: (slug: string) => void;
  articles: Article[];
}

/**
 * Preprocesses markdown string to transform double bracket [[WikiLinks]]
 * into standard Markdown links using a custom "wikilink:" protocol.
 * E.g., [[Learning Go]] -> [Learning Go](wikilink:learning-go)
 * E.g., [[learning-go|My Guide]] -> [My Guide](wikilink:learning-go)
 *
 * [[...]] inside fenced code blocks (``` / ~~~) and inline code spans (`...`) are left verbatim,
 * so code examples like C++ `[[nodiscard]]` or Lua `[[long strings]]` don't render as links.
 */
function preprocessWikiLinks(markdown: string): string {
  if (!markdown) return '';
  const convert = (text: string) =>
    text.replace(/\[\[([^\]|]+)(?:\|([^\]]+))?]]/g, (_, target, display) => {
      const label = display ? display.trim() : target.trim();
      const slug = Slugify(target.trim());
      return `[${label}](wikilink:${slug})`;
    });

  // Keep fenced code blocks untouched; within prose, keep inline code spans untouched.
  return markdown
    .split(/(```[\s\S]*?```|~~~[\s\S]*?~~~)/g)
    .map((segment) => {
      if (segment.startsWith('```') || segment.startsWith('~~~')) return segment;
      return segment
        .split(/(`[^`]*`)/g)
        .map((part) => (part.startsWith('`') && part.endsWith('`') ? part : convert(part)))
        .join('');
    })
    .join('');
}

/**
 * Resolves an href to the wiki slug it targets, or null when the link is external.
 *
 * NexWiki has two internal link forms and they must behave identically: the `wikilink:` protocol
 * that preprocessWikiLinks emits for [[WikiLinks]], and absolute `/articles/<slug>` Markdown links,
 * which the agent guidelines tell authors to prefer and which make up the majority of real links.
 * Any #fragment or ?query is dropped — it addresses a position within the article, not a different
 * article.
 */
function internalTargetSlug(href: string | undefined): string | null {
  if (!href) return null;
  if (href.startsWith('wikilink:')) return href.substring('wikilink:'.length) || null;
  if (href.startsWith('/articles/')) {
    const raw = href.substring('/articles/'.length).split(/[#?]/)[0].replace(/\/$/, '');
    try {
      return decodeURIComponent(raw) || null;
    } catch {
      return raw || null; // malformed percent-encoding: use it verbatim rather than throwing
    }
  }
  return null;
}

export const Viewer: React.FC<ViewerProps> = ({ content, onNavigate, articles }) => {
  const processedContent = preprocessWikiLinks(content);

  return (
    <div className="wiki-content">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeHighlight]}
        // The default transform sanitizes away the custom wikilink: protocol
        urlTransform={(url) => (url.startsWith('wikilink:') ? url : defaultUrlTransform(url))}
        components={{
        h1: ({ children }) => {
          const id = Slugify(String(children));
          return <h1 id={id}>{children}</h1>;
        },
          h2: ({ children }) => {
            const id = Slugify(String(children));
            return <h2 id={id}>{children}</h2>;
          },
          h3: ({ children }) => {
            const id = Slugify(String(children));
            return <h3 id={id}>{children}</h3>;
          },
          // Override default link rendering for internal links and SPA standard links
          a: ({ href, children, ...props }) => {
            // Both internal link forms resolve to a slug and must look and behave identically.
            // [[WikiLinks]] arrive as the custom wikilink: protocol from preprocessWikiLinks;
            // absolute Markdown links are the form the agent guidelines tell authors to prefer,
            // and they fell through to the external-link branch below — opening the wiki's own
            // pages in a new tab with a full page reload.
            const slug = internalTargetSlug(href);

            if (slug) {
              // Verify if the referenced page exists in our wiki list
              const exists = articles.some(art => art.slug === slug);

              if (exists || slug === 'home') {
                return (
                  <a
                    href={`/articles/${slug}`}
                    onClick={(e) => {
                      e.preventDefault();
                      onNavigate(slug);
                    }}
                    className="font-semibold text-indigo-600 dark:text-indigo-400 hover:text-indigo-800 dark:hover:text-indigo-300 underline underline-offset-4 decoration-2 transition-colors cursor-pointer"
                  >
                    {children}
                  </a>
                );
              } else {
                // Render dotted red broken link for non-existent pages (wiki style!).
                // A real <button> rather than a clickable <span>: this is an interactive control,
                // so it must be focusable, activatable with Enter/Space, and announced to screen
                // readers with what it does.
                const linkTitle = String(children);
                // Create the page the link actually points at. The display text is the nicer
                // title and is used whenever it resolves to the same slug — always true for
                // [[WikiLinks]] — but a Markdown link's text can differ from its destination
                // ([Rust](/articles/rust-lang)), and a "create" affordance that produces a page
                // the link still cannot reach is worse than none.
                const createTitle = Slugify(linkTitle) === slug ? linkTitle : slug;
                return (
                  <button
                    type="button"
                    onClick={() => onNavigate(`new?title=${encodeURIComponent(createTitle)}`)}
                    className="wikilink-broken"
                    title={`"${linkTitle}" does not exist yet. Click to create!`}
                    aria-label={`Create missing page "${linkTitle}"`}
                  >
                    {children}
                  </button>
                );
              }
            }

            // Standard HTTP / HTTPS external links
            return (
              <a
                href={href}
                target="_blank"
                rel="noopener noreferrer"
                className="font-semibold text-indigo-600 dark:text-indigo-400 hover:text-indigo-800 dark:hover:text-indigo-300 underline underline-offset-4 decoration-2 transition-colors"
                {...props}
              >
                {children}
              </a>
            );
          },

          // Add clean wrapper around code blocks
          code: ({ className, children, ...props }) => {
            return (
              <code className={className} {...props}>
                {children}
              </code>
            );
          },

          // Custom styles for checklist items
          li: ({ children, ...props }) => {
            return (
              <li {...props}>
                {children}
              </li>
            );
          }
        }}
      >
        {processedContent}
      </ReactMarkdown>
    </div>
  );
};

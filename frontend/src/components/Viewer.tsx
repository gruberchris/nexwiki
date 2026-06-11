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
 */
function preprocessWikiLinks(markdown: string): string {
  if (!markdown) return '';
  return markdown.replace(/\[\[([^\]|]+)(?:\|([^\]]+))?]]/g, (_, target, display) => {
    const text = display ? display.trim() : target.trim();
    const slug = Slugify(target.trim());
    return `[${text}](wikilink:${slug})`;
  });
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
          // Override default link rendering for WikiLinks and SPA standard links
          a: ({ href, children, ...props }) => {
            if (href && href.startsWith('wikilink:')) {
              const slug = href.substring('wikilink:'.length);
              
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
                // Render dotted red broken link for non-existent pages (wiki style!)
                const linkTitle = String(children);
                return (
                  <span
                    onClick={() => onNavigate(`new?title=${encodeURIComponent(linkTitle)}`)}
                    className="wikilink-broken"
                    title={`"${linkTitle}" does not exist yet. Click to create!`}
                  >
                    {children}
                  </span>
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

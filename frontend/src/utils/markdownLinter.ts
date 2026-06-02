import { Slugify } from '../utils';

export interface LintDiagnostic {
  line: number;
  from: number;
  to: number;
  severity: 'error' | 'warning' | 'info';
  message: string;
  suggestion?: string;
  code: string;
}

export function lintMarkdown(content: string, articles: { slug: string; title: string }[]): LintDiagnostic[] {
  const diagnostics: LintDiagnostic[] = [];
  const lines = content.split('\n');
  
  let currentOffset = 0;
  let prevHeadingLevel = 0;
  let h1Count = 0;
  
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const lineLen = line.length;
    
    // MD001 & MD025: Heading checks
    const headingMatch = line.match(/^(#{1,6})\s+(.*)$/);
    if (headingMatch) {
      const headingText = headingMatch[1];
      const headingLevel = headingText.length;
      
      // MD001: Heading hierarchy sequence
      if (prevHeadingLevel > 0 && headingLevel > prevHeadingLevel + 1) {
        diagnostics.push({
          line: i + 1,
          from: currentOffset,
          to: currentOffset + headingText.length,
          severity: 'warning',
          message: `Heading level should only increase by one level at a time. Expected H${prevHeadingLevel + 1} but got H${headingLevel}.`,
          suggestion: `${'#'.repeat(prevHeadingLevel + 1)} ${headingMatch[2]}`,
          code: 'MD001'
        });
      }
      
      // MD025: Multiple top-level H1 headers
      if (headingLevel === 1) {
        h1Count++;
        if (h1Count > 1) {
          diagnostics.push({
            line: i + 1,
            from: currentOffset,
            to: currentOffset + headingText.length,
            severity: 'error',
            message: 'Multiple top-level H1 headers found. Only one H1 is recommended per document.',
            suggestion: `## ${headingMatch[2]}`,
            code: 'MD025'
          });
        }
      }
      
      prevHeadingLevel = headingLevel;
    }
    
    // MD037: Spaces around emphasis indicators (* text * or ** text **)
    const emphasisRegex = /(?:^|[^\\])(\*{1,2}|_{1,2})(\s+[^*_]+?\s+)\1/g;
    let empMatch;
    while ((empMatch = emphasisRegex.exec(line)) !== null) {
      const fullMatch = empMatch[0];
      const matchIndex = empMatch.index;
      const indicator = empMatch[1];
      const spacesAndText = empMatch[2];
      
      diagnostics.push({
        line: i + 1,
        from: currentOffset + matchIndex + (fullMatch.indexOf(indicator)),
        to: currentOffset + matchIndex + fullMatch.indexOf(indicator) + fullMatch.trim().length,
        severity: 'warning',
        message: 'Emphasis indicators should not be surrounded by spaces.',
        suggestion: `${indicator}${spacesAndText.trim()}${indicator}`,
        code: 'MD037'
      });
    }
    
    // MD034: Bare URLs (not in standard brackets `<url>` or standard links `[text](url)` or images `![alt](url)`)
    const urlRegex = /(https?:\/\/[^\s()[\]{}<>]+)/g;
    let urlMatch;
    while ((urlMatch = urlRegex.exec(line)) !== null) {
      const url = urlMatch[1];
      const matchIndex = urlMatch.index;
      
      // Check if it is wrapped in angle brackets `<url>`
      const isAngleWrapped = matchIndex > 0 && line[matchIndex - 1] === '<' && line[matchIndex + url.length] === '>';
      // Check if it is inside Markdown link `[text](url)` or `![alt](url)`
      const isParenthesisWrapped = matchIndex > 0 && line[matchIndex - 1] === '(' && line.substring(0, matchIndex).includes(']');
      
      if (!isAngleWrapped && !isParenthesisWrapped) {
        diagnostics.push({
          line: i + 1,
          from: currentOffset + matchIndex,
          to: currentOffset + matchIndex + url.length,
          severity: 'info',
          message: 'Bare URLs should be wrapped in angle brackets or properly formatted.',
          suggestion: `<${url}>`,
          code: 'MD034'
        });
      }
    }
    
    // WikiLinks check: [[Page Title]] or [[page-slug|Custom text]]
    const wikiLinkRegex = /\[\[([^\]|]+)(?:\|([^\]]+))?]]/g;
    let wlMatch;
    while ((wlMatch = wikiLinkRegex.exec(line)) !== null) {
      const fullWikiLink = wlMatch[0];
      const matchIndex = wlMatch.index;
      const target = wlMatch[1].trim();
      
      const slug = Slugify(target);
      const exists = articles.some(art => art.slug === slug) || slug === 'home' || slug === 'new';
      
      if (!exists) {
        diagnostics.push({
          line: i + 1,
          from: currentOffset + matchIndex,
          to: currentOffset + matchIndex + fullWikiLink.length,
          severity: 'warning',
          message: `WikiLink target "${target}" does not exist yet.`,
          suggestion: `Click to create this page.`,
          code: 'WIKILINK_BROKEN'
        });
      }
    }
    
    // Add length of current line + newline char
    currentOffset += lineLen + 1; // +1 for the \n
  }
  
  return diagnostics;
}

import type { Article } from './types';
import type { LogEvent } from './context/SSEContextObject';

export function getTagPriority(tag: string, statusTags: Set<string>): number {
  const lower = tag.toLowerCase();
  if (statusTags.has(lower)) return 0;
  if (lower.startsWith('aiagent-')) return 2;
  return 1;
}

export function sortCardTags(tags: string[], statusTags: Set<string>): string[] {
  return [...tags].sort((a, b) => getTagPriority(a, statusTags) - getTagPriority(b, statusTags));
}

/**
 * Strip surrounding quote characters from a raw term string.
 * Handles complete pairs ("foo", 'foo') and open-quoted partials ("foo, 'foo).
 */
function extractTerm(raw: string): string {
  if (!raw) return '';
  if (raw[0] === '"' || raw[0] === "'") {
    const openQuote = raw[0];
    let inner = raw.slice(1);
    if (inner.endsWith(openQuote)) inner = inner.slice(0, -1);
    return inner;
  }
  return raw;
}

/**
 * Tokenize a boolean query string into raw string tokens.
 *
 * Rules:
 *   - Whitespace separates tokens (except inside quotes)
 *   - && and || are operator tokens
 *   - Single/double-quoted strings are kept as one token (including their quotes)
 *   - '!' immediately followed by a quote starts a single quoted-exclusion token (e.g. !'foo bar')
 *   - A bare '!' that is not followed by a quote is kept as its own token prefix
 *     (e.g. "!pro" → token "!pro"; "!pro grm" → tokens "!pro", "grm")
 */
function tokenizeQuery(query: string): string[] {
  const tokens: string[] = [];
  let i = 0;
  let current = '';
  let quoteChar: string | null = null;

  while (i < query.length) {
    const ch = query[i];

    if (quoteChar !== null) {
      if (ch === quoteChar) {
        current += ch;
        quoteChar = null;
        tokens.push(current);
        current = '';
      } else {
        current += ch;
      }
      i++;
    } else if (ch === '"' || ch === "'") {
      // Opening quote — if current token is just '!' keep it attached (quoted exclusion)
      if (current && current !== '!') {
        tokens.push(current);
        current = '';
      }
      current += ch;
      quoteChar = ch;
      i++;
    } else if (ch === ' ' || ch === '\t') {
      if (current) {
        tokens.push(current);
        current = '';
      }
      i++;
    } else if (ch === '&' && i + 1 < query.length && query[i + 1] === '&') {
      if (current) { tokens.push(current); current = ''; }
      tokens.push('&&');
      i += 2;
    } else if (ch === '|' && i + 1 < query.length && query[i + 1] === '|') {
      if (current) { tokens.push(current); current = ''; }
      tokens.push('||');
      i += 2;
    } else {
      current += ch;
      i++;
    }
  }

  // Flush any remaining token (includes open-quoted partial strings like !'foo bar')
  if (current) tokens.push(current);
  return tokens;
}

/**
 * Generic boolean query evaluator.
 *
 * Syntax:
 *   space / OR / || → implicit OR between positive terms
 *   AND / && → AND within a group (higher precedence than OR)
 *   !term → global exclusion of a single-word term
 *   !'multi-word' or !"multi-word" → quoted multi-word exclusion
 *   'multi-word' or "multi-word" → quoted multi-word positive term
 *
 * Note: "!word1 word2" means exclude word1 AND separately match word2 (OR).
 * Multi-word exclusions require quotes:'word1 word2'.
 */
export function evaluateBooleanQuery(fields: string[], query: string): boolean {
  if (!query.trim()) return true;

  const lowerFields = fields.map(f => f.toLowerCase());
  const contains = (term: string) => lowerFields.some(f => f.includes(term));

  const rawTokens = tokenizeQuery(query);
  const mustNot: string[] = [];
  const orGroups: string[][] = [[]];
  let nextIsAnd = false;

  for (const token of rawTokens) {
    const lower = token.toLowerCase();

    if (lower === '&&' || lower === 'and') {
      nextIsAnd = true;
    } else if (lower === '||' || lower === 'or') {
      orGroups.push([]);
      nextIsAnd = false;
    } else if (lower.startsWith('!')) {
      const term = extractTerm(lower.slice(1));
      if (term.length > 0) mustNot.push(term);
    } else {
      const term = extractTerm(lower);
      if (term.length > 0) {
        if (!nextIsAnd && orGroups[orGroups.length - 1].length > 0) {
          orGroups.push([]);
        }
        orGroups[orGroups.length - 1].push(term);
        nextIsAnd = false;
      }
    }
  }

  if (mustNot.some(contains)) return false;

  const positiveGroups = orGroups.filter(g => g.length > 0);
  if (positiveGroups.length === 0) return true;

  return positiveGroups.some(group => group.every(contains));
}

/**
 * Evaluates a filter query against an article's title and tags.
 */
export function matchesFilter(art: Article, query: string): boolean {
  const fields = [art.title, ...(art.tags ?? [])];
  return evaluateBooleanQuery(fields, query);
}

/**
 * Evaluates a filter query against an article's title, slug, and tags.
 */
export function matchesSidebarFilter(art: Article, query: string): boolean {
  const fields = [art.title, art.slug, ...(art.tags ?? [])];
  return evaluateBooleanQuery(fields, query);
}

/**
 * Evaluates a filter query against a log event's fields.
 */
export function matchesLogEvent(event: LogEvent, query: string): boolean {
  const fields = [
    event.action,
    event.source,
    event.tool,
    event.slug,
    event.title,
    event.agent,
  ].filter(Boolean);
  return evaluateBooleanQuery(fields, query);
}

/** Scans left-to-right and returns the position of the last unclosed opening quote, or -1. */
function findOpenQuotePos(query: string): number {
  let openQuoteChar: string | null = null;
  let openQuotePos = -1;
  for (let j = 0; j < query.length; j++) {
    const ch = query[j];
    if (openQuoteChar === null && (ch === '"' || ch === "'")) {
      openQuoteChar = ch;
      openQuotePos = j;
    } else if (openQuoteChar !== null && ch === openQuoteChar) {
      openQuoteChar = null;
      openQuotePos = -1;
    }
  }
  return openQuotePos;
}

/** Walks back from the end of a (trimmed) string to find where the active token begins. */
function findActiveTokenStart(trimmed: string): number {
  for (let i = trimmed.length - 1; i >= 0; i--) {
    const ch = trimmed[i];
    if (ch === ' ' || ch === '\t' || ch === '&' || ch === '|') {
      return i + 1;
    }
  }
  return 0;
}

/**
 * Returns the raw search string for the autocomplete dropdown based on what the user
 * is currently typing (the "active" rightmost token), with all prefixes and quotes stripped.
 *
 * Returns '' when there is no active token (trailing space, complete quoted term, etc.).
 */
export function getAutocompleteSearchTerm(query: string): string {
  if (!query) return '';

  const openQuotePos = findOpenQuotePos(query);
  if (openQuotePos !== -1) {
    // Inside an open-quoted string — return content after the opening quote.
    return query.slice(openQuotePos + 1);
  }

  // Not in an open quote. Active token is the rightmost whitespace/operator-delimited segment.
  const trimmed = query.trimEnd();
  if (trimmed.length < query.length) return ''; // trailing whitespace → no active token

  // If the trimmed query ends with a closing quote, the last token is complete.
  if (trimmed.endsWith('"') || trimmed.endsWith("'")) return '';

  const tokenStart = findActiveTokenStart(trimmed);
  const activeToken = trimmed.slice(tokenStart);
  if (!activeToken) return '';

  // Strip the leading exclamation character
  const withoutBang = activeToken.startsWith('!') ? activeToken.slice(1) : activeToken;

  // Complete quoted string — shouldn't normally reach here, but guard anyway.
  if (
    (withoutBang.startsWith('"') && withoutBang.endsWith('"')) ||
    (withoutBang.startsWith("'") && withoutBang.endsWith("'"))
  ) {
    return '';
  }

  // Strip a leading partial quote (edge case).
  if (withoutBang.startsWith('"') || withoutBang.startsWith("'")) {
    return withoutBang.slice(1);
  }

  return withoutBang;
}

/**
 * Replaces the active typed token in the query with the autocomplete selection.
 *
 * - If the selection contains spaces, it is wrapped in single quotes: 'the selection'
 * - A leading '!' on the active token is preserved like !'the selection'
 * - The rest of the query (the prefix before the active token) is kept verbatim.
 */
export function applyAutocompleteSelection(query: string, selection: string): string {
  const quotedSelection = selection.includes(' ') ? `'${selection}'` : selection;

  const openQuotePos = findOpenQuotePos(query);
  if (openQuotePos !== -1) {
    // Replace from the character before the open quote (could be !) to end.
    const hasBang = openQuotePos > 0 && query[openQuotePos - 1] === '!';
    const tokenStart = hasBang ? openQuotePos - 1 : openQuotePos;
    const rawPrefix = query.slice(0, tokenStart).trimEnd();
    const sep = rawPrefix.length > 0 ? ' ' : '';
    const bang = hasBang ? '!' : '';
    return rawPrefix + sep + bang + quotedSelection;
  }

  // Not in open quote — find start of active token by walking back.
  const trimmed = query.trimEnd();
  const tokenStart = findActiveTokenStart(trimmed);
  const activeToken = trimmed.slice(tokenStart);
  const hasBang = activeToken.startsWith('!');
  const rawPrefix = trimmed.slice(0, tokenStart).trimEnd();
  const endsWithOp = rawPrefix.endsWith('&&') || rawPrefix.endsWith('||');
  const sep = rawPrefix.length > 0 && !endsWithOp ? ' ' : '';
  const bang = hasBang ? '!' : '';

  return rawPrefix + sep + bang + quotedSelection;
}

/**
 * Builds autocomplete suggestions from article objects.
 */
export function buildSuggestionsFromArticles(articles: Article[], searchTerm: string): Array<{ type: 'tag' | 'title', value: string }> {
  if (!searchTerm.trim()) return [];

  const lowerSearch = searchTerm.toLowerCase();
  const results: Array<{ type: 'tag' | 'title', value: string }> = [];
  const seenValues = new Set<string>();

  articles.forEach(art => {
    if (art.title.toLowerCase().includes(lowerSearch) && !seenValues.has(art.title)) {
      seenValues.add(art.title);
      results.push({ type: 'title', value: art.title });
    }

    if (art.tags) {
      art.tags.forEach(tag => {
        const lowerTag = tag.toLowerCase();
        if (!lowerTag.startsWith('aiagent-') &&
            lowerTag.includes(lowerSearch) &&
            !seenValues.has(tag)) {
          seenValues.add(tag);
          results.push({ type: 'tag', value: tag });
        }
      });
    }
  });

  return results.slice(0, 8);
}

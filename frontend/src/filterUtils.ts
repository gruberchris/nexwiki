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
 * Generic boolean query evaluator.
 *
 * Syntax:
 *   space / OR / || → OR between positive terms (bare space is implicit OR)
 *   AND / && → AND within a group (higher precedence than OR)
 *   !term → global exclusion — always ANDed regardless of position
 *   bare!           → ignored (user is mid-typing)
 */
export function evaluateBooleanQuery(fields: string[], query: string): boolean {
  if (!query.trim()) return true;

  const lowerFields = fields.map(f => f.toLowerCase());
  const contains = (term: string) => lowerFields.some(f => f.includes(term));

  const normalized = query.trim().replace(/&&/g, ' AND ').replace(/\|\|/g, ' OR ');
  const tokens = normalized.split(/\s+/).filter(Boolean);

  const mustNot: string[] = [];
  const positiveTokens: string[] = [];
  for (const token of tokens) {
    const lower = token.toLowerCase();
    if (lower.startsWith('!')) {
      if (lower.length > 1) mustNot.push(lower.slice(1));
    } else {
      positiveTokens.push(lower);
    }
  }

  if (mustNot.some(contains)) return false;
  if (positiveTokens.length === 0) return true;

  const orGroups: string[][] = [[]];
  let nextIsAnd = false;
  for (const token of positiveTokens) {
    if (token === 'or') {
      orGroups.push([]);
      nextIsAnd = false;
    } else if (token === 'and') {
      nextIsAnd = true;
    } else {
      if (!nextIsAnd && orGroups[orGroups.length - 1].length > 0) orGroups.push([]);
      orGroups[orGroups.length - 1].push(token);
      nextIsAnd = false;
    }
  }

  return orGroups.filter(g => g.length > 0).some(group => group.every(contains));
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

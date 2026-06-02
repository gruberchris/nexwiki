import type { Article } from './types';

export function getTagPriority(tag: string, statusTags: Set<string>): number {
  const lower = tag.toLowerCase();
  if (statusTags.has(lower)) return 0;
  if (lower.startsWith('aiagent-')) return 2;
  return 1;
}

export function sortCardTags(tags: string[], statusTags: Set<string>): string[] {
  return [...tags].sort((a, b) => getTagPriority(a, statusTags) - getTagPriority(b, statusTags));
}

// Evaluates a filter query against an article's title and tags.
//
// Syntax:
//   space / OR / || → OR between positive terms (bare space is implicit OR)
//   AND / && → AND within a group (higher precedence than OR)
//   !term → global exclusion — always ANDed regardless of position
//   bare!           → ignored (user is mid-typing)
export function matchesFilter(art: Article, query: string): boolean {
  if (!query.trim()) return true;

  const fields = [art.title.toLowerCase(), ...(art.tags?.map(t => t.toLowerCase()) ?? [])];
  const contains = (term: string) => fields.some(f => f.includes(term));

  const normalized = query.trim().replace(/&&/g, ' AND ').replace(/\|\|/g, ' OR ');
  const tokens = normalized.split(/\s+/).filter(Boolean);

  const mustNot: string[] = [];
  const positiveTokens: string[] = [];
  for (const token of tokens) {
    const lower = token.toLowerCase();
    if (lower.startsWith('!')) {
      if (lower.length > 1) mustNot.push(lower.slice(1));
      // bare '!' ignored — incomplete negation while typing
    } else {
      positiveTokens.push(lower);
    }
  }

  if (mustNot.some(contains)) return false;
  if (positiveTokens.length === 0) return true;

  // Build OR-groups: space or explicit OR starts a new group; AND appends to current group.
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

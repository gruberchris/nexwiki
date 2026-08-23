// OKF document-class discriminator. Every article carries exactly one of these in `type`.
export type ContentType =
  | 'Wiki'
  | 'AI-Agent-Memory'
  | 'AI-Agent-Plan'
  | 'AI-Agent-Skill';

export const ContentTypes = {
  Wiki: 'Wiki',
  Memory: 'AI-Agent-Memory',
  Plan: 'AI-Agent-Plan',
  Skill: 'AI-Agent-Skill',
} as const;

export interface Article {
  type?: ContentType;
  title: string;
  slug: string;
  created_at: string;
  timestamp: string;
  content?: string;
  description?: string;
  source?: string;
  resource?: string;
  version?: number;
  edit_summary?: string;
  tags?: string[];
  archived_at?: string;
  /** Lifecycle state. Plans and skills use a closed vocabulary; other types may use anything. */
  status?: string;
  /** When a plan last changed lifecycle status; drives the auto-archive/auto-delete timers. */
  status_changed_at?: string;
}

/**
 * A timestamp that represents a real moment, rather than Go's zero value serialized as a string.
 * Declared as a type predicate so callers still narrow away `undefined`, which a plain boolean
 * return would silently stop doing.
 */
export function isRealTimestamp(value: string | undefined): value is string {
  return !!value && !value.startsWith('0001-01-01');
}

/**
 * Whether a document counts as archived — mirroring the server's IsArchived. All three forms are
 * checked because plans and skills archive through the status field while wiki articles and
 * memories archive through the tag, and a caller that inspects only one silently misses half.
 */
export function isArchivedDoc(art: Pick<Article, 'archived_at' | 'tags' | 'status'>): boolean {
  // Not `if (art.archived_at)`. A Go zero time serializes as "0001-01-01T00:00:00Z", which is a
  // truthy string — reading it as a boolean marked every document archived and emptied the
  // dashboard and sidebar while the counts, taken from the unfiltered list, still showed.
  if (isRealTimestamp(art.archived_at)) return true;
  if (art.status?.toLowerCase() === 'archived') return true;
  return !!art.tags?.some((t) => t.toLowerCase() === 'archived');
}

// Classification helpers keyed off the OKF `type` (replacing the old aiagent-* tag scan).
export function isAgentDoc(art: Pick<Article, 'type'>): boolean {
  return !!art.type && art.type !== ContentTypes.Wiki;
}
export function isMemory(art: Pick<Article, 'type'>): boolean {
  return art.type === ContentTypes.Memory;
}
export function isPlan(art: Pick<Article, 'type'>): boolean {
  return art.type === ContentTypes.Plan;
}
export function isSkill(art: Pick<Article, 'type'>): boolean {
  return art.type === ContentTypes.Skill;
}

// Short, human-friendly label for a document type (used in read-only badges).
export function typeLabel(type?: ContentType): string {
  switch (type) {
    case ContentTypes.Memory:
      return 'Agent Memory';
    case ContentTypes.Plan:
      return 'Agent Plan';
    case ContentTypes.Skill:
      return 'Agent Skill';
    default:
      return 'Wiki';
  }
}

// Light/dark variant selection mode: explicit choice or follow the browser.
export type ThemeMode = 'light' | 'dark' | 'auto';

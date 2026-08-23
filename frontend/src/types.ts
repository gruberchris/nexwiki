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
  /** When a plan last changed lifecycle status; drives the auto-archive/auto-delete timers. */
  status_changed_at?: string;
}

/**
 * Whether a document counts as archived, by timestamp or tag — mirroring the server's IsArchived,
 * which checks both because the browser archives by tag while storage records a timestamp.
 */
export function isArchivedDoc(art: Pick<Article, 'archived_at' | 'tags'>): boolean {
  if (art.archived_at) return true;
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

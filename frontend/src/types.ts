export interface Article {
  title: string;
  slug: string;
  created_at: string;
  updated_at: string;
  content?: string;
  version?: number;
  edit_summary?: string;
  tags?: string[];
}

// Light/dark variant selection mode: explicit choice or follow the browser.
export type ThemeMode = 'light' | 'dark' | 'auto';

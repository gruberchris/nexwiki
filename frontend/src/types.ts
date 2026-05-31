export interface Article {
  title: string;
  slug: string;
  created_at: string;
  updated_at: string;
  content?: string;
  version?: number;
  edit_summary?: string;
}

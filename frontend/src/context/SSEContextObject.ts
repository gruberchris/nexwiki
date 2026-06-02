import { createContext } from 'react';

export interface LogEvent {
  id: string;
  timestamp: string;
  source: 'mcp' | 'api';
  action: 'create' | 'edit' | 'delete' | 'read' | 'revert';
  tool: string;
  slug: string;
  title: string;
  agent: string;
}

export interface WikiUpdate {
  type: 'article-added' | 'article-edited' | 'article-removed';
  slug: string;
  title: string;
  tags: string[];
  directory: 'wiki' | 'aimemories' | 'aiplans' | 'aiskills';
  total_count: number;
  directory_count: number;
}

export interface SSEContextType {
  activityLog: LogEvent[];
  unreadCount: number;
  resetUnreadCount: () => void;
  isConnected: boolean;
}

export const SSEContext = createContext<SSEContextType | undefined>(undefined);

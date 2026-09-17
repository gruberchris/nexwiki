import { createContext } from 'react';

export interface LogEvent {
  id: string;
  timestamp: string;
  // 'lifecycle' is the server's plan lifecycle worker, acting unattended.
  source: 'mcp' | 'api' | 'lifecycle';
  // Mirrors the server's activityLogActions. 'verify' is a human verifying a document in the web UI;
  // 'delete-refused' is the lifecycle worker keeping a plan that may still be linked.
  action: 'create' | 'edit' | 'delete' | 'read' | 'revert' | 'verify' | 'delete-refused';
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

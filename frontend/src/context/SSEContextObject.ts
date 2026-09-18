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
  // The document revision a write event acted on; absent when the event is not tied to one.
  version?: number;
}

export interface WikiUpdate {
  // 'updates-missed' is not a document change: it marks that the live stream dropped updates
  // (a bulk write outpaced the buffer) and the client should reload from durable state.
  type: 'article-added' | 'article-edited' | 'article-removed' | 'updates-missed';
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
  // True once the server has reported that live events were missed and views were reloaded,
  // until acknowledged — the activity drawer shows its "incomplete, reloaded" notice meanwhile.
  missedEvents: boolean;
  acknowledgeMissedEvents: () => void;
}

export const SSEContext = createContext<SSEContextType | undefined>(undefined);

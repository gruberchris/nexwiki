import React from 'react';
import { SSEContext } from './context/SSEContextObject';
import type { SSEContextType, LogEvent } from './context/SSEContextObject';

export const mockSSEContext: SSEContextType = {
  activityLog: [],
  unreadCount: 0,
  resetUnreadCount: () => {},
  isConnected: true,
};

export function withSSEContext(ui: React.ReactElement, ctx: Partial<SSEContextType> = {}): React.ReactElement {
  return (
    <SSEContext.Provider value={{ ...mockSSEContext, ...ctx }}>
      {ui}
    </SSEContext.Provider>
  );
}

export const sampleLogEvents: LogEvent[] = [
  {
    id: 'evt_1',
    timestamp: '2024-01-15T12:00:00Z',
    source: 'mcp',
    action: 'create',
    tool: 'create_wiki_article',
    slug: 'go-guide',
    title: 'Go Guide',
    agent: 'Claude',
  },
  {
    id: 'evt_2',
    timestamp: '2024-01-15T12:01:00Z',
    source: 'api',
    action: 'edit',
    tool: '',
    slug: 'my-article',
    title: 'My Article',
    agent: 'User',
  },
];

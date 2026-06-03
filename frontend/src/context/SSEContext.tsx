import React, { useEffect, useState, useRef } from 'react';
import { SSEContext } from './SSEContextObject';
import type { LogEvent, WikiUpdate } from './SSEContextObject';
export type { LogEvent, WikiUpdate } from './SSEContextObject';

export const SSEProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [activityLog, setActivityLog] = useState<LogEvent[]>([]);
  const [unreadCount, setUnreadCount] = useState(0);
  const [isConnected, setIsConnected] = useState(false);

  // Keep a ref to unread buffering
  const unreadBufferRef = useRef<number>(0);
  const bufferTimeoutRef = useRef<NodeJS.Timeout | null>(null);

  // Buffer rapid updates in 500ms cooldown window to prevent rapid visual noise
  const triggerCumulativeUnreadBadge = (count: number) => {
    unreadBufferRef.current += count;
    
    if (bufferTimeoutRef.current) {
      clearTimeout(bufferTimeoutRef.current);
    }
    
    bufferTimeoutRef.current = setTimeout(() => {
      setUnreadCount(prev => prev + unreadBufferRef.current);
      unreadBufferRef.current = 0;
      bufferTimeoutRef.current = null;
    }, 500);
  };

  useEffect(() => {
    let eventSource: EventSource | null = null;
    let reconnectTimeout: NodeJS.Timeout | null = null;
    let lastConnectTime = 0;

    const connect = () => {
      const now = Date.now();
      if (now - lastConnectTime < 3000) {
        return; // Prevent rapid connection spam
      }
      lastConnectTime = now;

      if (eventSource) {
        eventSource.close();
      }

      eventSource = new EventSource('/api/activity/stream');

      eventSource.onopen = () => {
        setIsConnected(true);
        console.log('SSE Stream established successfully.');
      };

      eventSource.onerror = () => {
        setIsConnected(false);
        console.warn('SSE Stream closed. Attempting reconnect in 5s...');
        eventSource?.close();
        
        reconnectTimeout = setTimeout(() => {
          connect();
        }, 5000);
      };

      // Listen to historical activity log streams
      eventSource.addEventListener('history', (event: MessageEvent) => {
        try {
          const ev = JSON.parse(event.data) as LogEvent;
          setActivityLog((prev) => {
            // Deduplicate logs by ID to prevent duplicates on reconnections
            if (prev.some(p => p.id === ev.id)) {
              return prev;
            }
            const next = [ev, ...prev];
            return next.slice(0, 200); // cap circular buffer at 200 log entries
          });
        } catch (err) {
          console.error('Failed to parse SSE history:', err);
        }
      });

      // Listen to live activity log streams
      eventSource.addEventListener('activity', (event: MessageEvent) => {
        try {
          const ev = JSON.parse(event.data) as LogEvent;
          setActivityLog((prev) => {
            // Deduplicate logs by ID to prevent duplicates on reconnections
            if (prev.some(p => p.id === ev.id)) {
              return prev;
            }
            const next = [ev, ...prev];
            return next.slice(0, 200); // cap circular buffer at 200 log entries
          });
          
          // Increment unread operations (e.g., AI agent tool uses)
          if (ev.agent !== 'User' && ev.source !== 'api') {
            triggerCumulativeUnreadBadge(1);
          }
        } catch (err) {
          console.error('Failed to parse SSE activity:', err);
        }
      });

      // Listen to count-synchronization events and propagate to window callbacks
      eventSource.addEventListener('wiki-update', (event: MessageEvent) => {
        try {
          const update = JSON.parse(event.data) as WikiUpdate;
          const customEvent = new CustomEvent('nexwiki-update', { detail: update });
          window.dispatchEvent(customEvent);
        } catch (err) {
          console.error('Failed to parse SSE wiki-update:', err);
        }
      });
    };

    const handleRefresh = () => {
      if (document.visibilityState === 'visible') {
        console.log('NexWiki window focused or tab became visible, refreshing SSE stream to catch up on updates...');
        connect();
      }
    };

    document.addEventListener('visibilitychange', handleRefresh);
    window.addEventListener('focus', handleRefresh);
    connect();

    return () => {
      document.removeEventListener('visibilitychange', handleRefresh);
      window.removeEventListener('focus', handleRefresh);
      if (eventSource) {
        eventSource.close();
      }
      if (reconnectTimeout) {
        clearTimeout(reconnectTimeout);
      }
      if (bufferTimeoutRef.current) {
        clearTimeout(bufferTimeoutRef.current);
      }
    };
  }, []);

  const resetUnreadCount = () => {
    setUnreadCount(0);
  };

  return (
    <SSEContext.Provider value={{ activityLog, unreadCount, resetUnreadCount, isConnected }}>
      {children}
    </SSEContext.Provider>
  );
};

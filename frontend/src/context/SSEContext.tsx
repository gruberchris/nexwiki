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

    const connect = () => {
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

      // Listen to live activity log streams
      eventSource.addEventListener('activity', (event: MessageEvent) => {
        try {
          const ev = JSON.parse(event.data) as LogEvent;
          setActivityLog((prev) => {
            const next = [ev, ...prev];
            return next.slice(0, 200); // cap circular buffer at 200 log entries
          });
          
          // Increment unread operations (e.g., AI agent tool uses)
          triggerCumulativeUnreadBadge(1);
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

    connect();

    return () => {
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

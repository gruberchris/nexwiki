import { useEffect } from 'react';
import type { WikiUpdate } from '../context/SSEContext';

// useWikiUpdates coordinates real-time synchronization updates received from the EventSource.
export function useWikiUpdates(onUpdate: (update: WikiUpdate) => void) {
  useEffect(() => {
    const handleUpdate = (event: Event) => {
      const customEvent = event as CustomEvent<WikiUpdate>;
      if (customEvent.detail) {
        onUpdate(customEvent.detail);
      }
    };

    window.addEventListener('nexwiki-update', handleUpdate);
    return () => {
      window.removeEventListener('nexwiki-update', handleUpdate);
    };
  }, [onUpdate]);
}

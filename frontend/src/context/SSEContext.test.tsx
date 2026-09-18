import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { SSEProvider } from './SSEContext';
import { useSSE } from '../hooks/useSSE';

afterEach(() => {
  vi.restoreAllMocks();
});

// Helper component to consume the context
function Consumer() {
  const ctx = useSSE();
  return (
    <div>
      <span data-testid="connected">{ctx.isConnected ? 'yes' : 'no'}</span>
      <span data-testid="unread">{ctx.unreadCount}</span>
      <span data-testid="missed">{ctx.missedEvents ? 'yes' : 'no'}</span>
      <button onClick={ctx.resetUnreadCount}>reset</button>
      <button onClick={ctx.acknowledgeMissedEvents}>acknowledge</button>
    </div>
  );
}

describe('SSEProvider', () => {
  it('provides default context values', async () => {
    class MockEventSource {
      onopen: (() => void) | null = null;
      onerror: (() => void) | null = null;
      addEventListener = vi.fn();
      close = vi.fn();
      constructor(_url: string) {
        void _url;
        void this.onopen;
        void this.onerror;
      }
    }
    vi.stubGlobal('EventSource', MockEventSource);

    await act(async () => {
      render(
        <SSEProvider>
          <Consumer />
        </SSEProvider>
      );
    });

    expect(screen.getByTestId('connected').textContent).toBe('no');
    expect(screen.getByTestId('unread').textContent).toBe('0');
  });

  it('renders children', async () => {
    class MockEventSource {
      onopen: (() => void) | null = null;
      onerror: (() => void) | null = null;
      addEventListener = vi.fn();
      close = vi.fn();
      constructor(_url: string) {
        void _url;
        void this.onopen;
        void this.onerror;
      }
    }
    vi.stubGlobal('EventSource', MockEventSource);

    await act(async () => {
      render(
        <SSEProvider>
          <span>child content</span>
        </SSEProvider>
      );
    });

    expect(screen.getByText('child content')).toBeInTheDocument();
  });

  it('calls setIsConnected on EventSource open', async () => {
    let capturedInstance: { onopen: (() => void) | null } | null = null;

    class MockEventSource {
      onopen: (() => void) | null = null;
      onerror: (() => void) | null = null;
      addEventListener = vi.fn();
      close = vi.fn();
      constructor(_url: string) {
        void _url;
        void this.onopen;
        void this.onerror;
        // eslint-disable-next-line @typescript-eslint/no-this-alias
        capturedInstance = this;
      }
    }
    vi.stubGlobal('EventSource', MockEventSource);

    await act(async () => {
      render(
        <SSEProvider>
          <Consumer />
        </SSEProvider>
      );
    });

    await act(async () => {
      if (capturedInstance?.onopen) capturedInstance.onopen();
    });

    expect(screen.getByTestId('connected').textContent).toBe('yes');
  });

  // The server replaces a backlog it could not deliver with one "missed events" marker. The
  // provider must route it onto the nexwiki-update window event — the path App's coalesced
  // reload listens on — and expose it so the activity drawer can show its notice (#172).
  it('reloads through the wiki-update path and flags on a missed-events marker', async () => {
    const listeners: Record<string, ((event: { data: string }) => void)[]> = {};

    class MockEventSource {
      onopen: (() => void) | null = null;
      onerror: (() => void) | null = null;
      close = vi.fn();
      addEventListener = (name: string, handler: (event: { data: string }) => void) => {
        (listeners[name] ||= []).push(handler);
        void this.onopen;
        void this.onerror;
      };
      constructor(_url: string) {
        void _url;
      }
    }
    vi.stubGlobal('EventSource', MockEventSource);

    const dispatched: unknown[] = [];
    const onNexwikiUpdate = (event: Event) => {
      dispatched.push((event as CustomEvent).detail);
    };
    window.addEventListener('nexwiki-update', onNexwikiUpdate);
    const restoreDispatch = vi.spyOn(window, 'dispatchEvent');

    await act(async () => {
      render(
        <SSEProvider>
          <Consumer />
        </SSEProvider>
      );
    });

    await act(async () => {
      for (const handler of listeners['missed-events']) {
        handler({ data: '{"type":"missed-events"}' });
      }
    });

    // The reload path: a nexwiki-update carrying the updates-missed marker.
    const markers = dispatched.filter(
      (detail) => (detail as { type?: string })?.type === 'updates-missed'
    );
    expect(markers.length).toBeGreaterThan(0);
    expect(restoreDispatch).toHaveBeenCalled();

    // And the drawer's indication has its flag.
    expect(screen.getByTestId('missed').textContent).toBe('yes');

    window.removeEventListener('nexwiki-update', onNexwikiUpdate);
  });

  it('clears the missed-events flag on acknowledge', async () => {
    const listeners: Record<string, ((event: { data: string }) => void)[]> = {};

    class MockEventSource {
      onopen: (() => void) | null = null;
      onerror: (() => void) | null = null;
      close = vi.fn();
      addEventListener = (name: string, handler: (event: { data: string }) => void) => {
        (listeners[name] ||= []).push(handler);
        void this.onopen;
        void this.onerror;
      };
      constructor(_url: string) {
        void _url;
      }
    }
    vi.stubGlobal('EventSource', MockEventSource);

    await act(async () => {
      render(
        <SSEProvider>
          <Consumer />
        </SSEProvider>
      );
    });

    await act(async () => {
      for (const handler of listeners['missed-events']) {
        handler({ data: '{"type":"missed-events"}' });
      }
    });
    expect(screen.getByTestId('missed').textContent).toBe('yes');

    await act(async () => {
      screen.getByText('acknowledge').click();
    });
    expect(screen.getByTestId('missed').textContent).toBe('no');
  });
});

describe('useSSE', () => {
  it('throws when used outside SSEProvider', () => {
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    expect(() => {
      render(<Consumer />);
    }).toThrow('useSSE must be used within an SSEProvider');
    consoleSpy.mockRestore();
  });
});

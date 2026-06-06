import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';
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
      <button onClick={ctx.resetUnreadCount}>reset</button>
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
      constructor(_url: string) {}
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
      constructor(_url: string) {}
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

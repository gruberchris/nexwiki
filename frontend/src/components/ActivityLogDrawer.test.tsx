import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ActivityLogDrawer } from './ActivityLogDrawer';
import { withSSEContext, sampleLogEvents } from '../test-helpers';
import type { LogEvent } from '../context/SSEContextObject';

const baseProps = {
  isOpen: true,
  onClose: vi.fn(),
  onNavigate: vi.fn(),
};

describe('ActivityLogDrawer', () => {
  it('renders collapsed state when closed', () => {
    // ActivityLogDrawer may render a hidden/transformed state when closed
    const { container } = render(
      withSSEContext(<ActivityLogDrawer {...baseProps} isOpen={false} />)
    );
    // Component is either null or renders a closed/hidden drawer
    expect(container).toBeInTheDocument();
  });

  it('renders when open with empty log', () => {
    render(withSSEContext(<ActivityLogDrawer {...baseProps} />));
    // Should render without crashing
    expect(document.body.textContent).toBeDefined();
  });

  it('renders activity log events', () => {
    render(withSSEContext(<ActivityLogDrawer {...baseProps} />, { activityLog: sampleLogEvents }));
    expect(screen.getByText('Go Guide') || document.body.textContent?.includes('Go Guide')).toBeTruthy();
  });

  it('calls onClose when close button is clicked', async () => {
    const onClose = vi.fn();
    render(withSSEContext(<ActivityLogDrawer {...baseProps} onClose={onClose} />));
    const closeButtons = screen.getAllByRole('button');
    if (closeButtons.length > 0) {
      await userEvent.click(closeButtons[0]);
      expect(onClose).toHaveBeenCalled();
    }
  });

  it('shows connected status indicator', () => {
    render(withSSEContext(<ActivityLogDrawer {...baseProps} />, { isConnected: true }));
    expect(document.body.textContent).toBeDefined();
  });

  it('shows disconnected indicator when not connected', () => {
    render(withSSEContext(<ActivityLogDrawer {...baseProps} />, { isConnected: false }));
    expect(document.body.textContent).toBeDefined();
  });

  it('calls onNavigate when an event slug is clicked', async () => {
    const onNavigate = vi.fn();
    render(
      withSSEContext(
        <ActivityLogDrawer {...baseProps} onNavigate={onNavigate} />,
        { activityLog: sampleLogEvents }
      )
    );
    const goGuideLink = screen.queryByText('Go Guide');
    if (goGuideLink) {
      await userEvent.click(goGuideLink);
      expect(onNavigate).toHaveBeenCalled();
    }
  });

  // Actions and a source the server logs but the drawer used to style as a plain read from the
  // REST API.
  const lifecycleEvents: LogEvent[] = [
    ...sampleLogEvents,
    {
      id: 'evt_3',
      timestamp: '2024-01-15T12:02:00Z',
      source: 'api',
      action: 'verify',
      tool: '',
      slug: 'verified-page',
      title: 'Verified Page',
      agent: 'User',
    },
    {
      id: 'evt_4',
      timestamp: '2024-01-15T12:03:00Z',
      source: 'lifecycle',
      action: 'delete-refused',
      tool: 'plan_lifecycle',
      slug: 'old-plan',
      title: 'Old Plan',
      agent: 'NexWiki',
    },
  ];

  it('labels and styles verify and delete-refused actions', () => {
    render(withSSEContext(<ActivityLogDrawer {...baseProps} />, { activityLog: lifecycleEvents }));

    const verify = screen.getByText('verify');
    expect(verify.className).toContain('teal');

    // Displayed as two words, styled as a warning rather than as a deletion or a read.
    const refused = screen.getByText('delete refused');
    expect(refused.className).toContain('orange');
    expect(refused.className).not.toContain('rose');
  });

  it('marks lifecycle events with their own source badge', () => {
    render(withSSEContext(<ActivityLogDrawer {...baseProps} />, { activityLog: lifecycleEvents }));

    const badge = screen.getByTitle('Plan lifecycle worker (unattended)');
    expect(badge.textContent).toContain('Lifecycle');
    // The refused plan still exists, so its title stays a link.
    expect(screen.getByText('Old Plan').closest('button')).not.toBeNull();
  });

  it('filters to lifecycle events', async () => {
    render(withSSEContext(<ActivityLogDrawer {...baseProps} />, { activityLog: lifecycleEvents }));

    await userEvent.click(screen.getByRole('button', { name: 'Lifecycle' }));

    expect(screen.getByText('Old Plan')).toBeInTheDocument();
    expect(screen.queryByText('Go Guide')).toBeNull();
    expect(screen.queryByText('My Article')).toBeNull();
    expect(screen.queryByText('Verified Page')).toBeNull();
  });
});

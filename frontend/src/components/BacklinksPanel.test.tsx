import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { BacklinksPanel } from './BacklinksPanel';

const mockBacklinks = [
  {
    title: 'Linking Article',
    slug: 'linking-article',
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-01-15T12:00:00Z',
    description: 'links to the target',
  },
];

describe('BacklinksPanel', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('renders backlinks returned by the API', async () => {
    (fetch as ReturnType<typeof vi.fn>).mockResolvedValue({
      ok: true,
      json: async () => mockBacklinks,
    });

    render(<BacklinksPanel slug="target" onNavigate={vi.fn()} />);
    await waitFor(() => {
      expect(screen.getByText('Linked from')).toBeInTheDocument();
      expect(screen.getByText('Linking Article')).toBeInTheDocument();
    });
    expect(fetch).toHaveBeenCalledWith('/api/articles/target/backlinks');
  });

  it('renders nothing when there are no backlinks', async () => {
    (fetch as ReturnType<typeof vi.fn>).mockResolvedValue({
      ok: true,
      json: async () => [],
    });

    const { container } = render(<BacklinksPanel slug="lonely" onNavigate={vi.fn()} />);
    await waitFor(() => expect(fetch).toHaveBeenCalled());
    expect(container.firstChild).toBeNull();
  });

  it('renders nothing when the request fails', async () => {
    (fetch as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('network down'));

    const { container } = render(<BacklinksPanel slug="target" onNavigate={vi.fn()} />);
    await waitFor(() => expect(fetch).toHaveBeenCalled());
    expect(container.firstChild).toBeNull();
  });

  it('navigates when a backlink is clicked', async () => {
    (fetch as ReturnType<typeof vi.fn>).mockResolvedValue({
      ok: true,
      json: async () => mockBacklinks,
    });

    const onNavigate = vi.fn();
    render(<BacklinksPanel slug="target" onNavigate={onNavigate} />);
    await waitFor(() => expect(screen.getByText('Linking Article')).toBeInTheDocument());
    await userEvent.click(screen.getByText('Linking Article'));
    expect(onNavigate).toHaveBeenCalledWith('linking-article');
  });
});

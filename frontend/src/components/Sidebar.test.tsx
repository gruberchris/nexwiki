import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Sidebar } from './Sidebar';
import { withSSEContext } from '../test-helpers';

afterEach(() => {
  vi.restoreAllMocks();
});

const defaultProps = {
  articles: [],
  currentSlug: '',
  themeMode: 'auto' as const,
  onCycleThemeMode: vi.fn(),
  onOpenThemeManager: vi.fn(),
  onNavigate: vi.fn(),
  onCreateNew: vi.fn(),
  wikiName: 'Test Wiki',
  onExportAll: vi.fn(),
  onImport: vi.fn(),
  onOpenActivityLog: vi.fn(),
  version: '0.0.1',
};

function renderSidebar(overrides = {}) {
  return render(withSSEContext(<Sidebar {...defaultProps} {...overrides} />));
}

describe('Sidebar backup & restore buttons', () => {
  it('renders the Backup Content button', () => {
    renderSidebar();
    expect(screen.getByText('Backup Content (.zip)')).toBeInTheDocument();
  });

  it('renders the Restore from Backup button', () => {
    renderSidebar();
    expect(screen.getByText('Restore from Backup (.zip)')).toBeInTheDocument();
  });

  it('calls onExportAll when Backup Content is clicked', async () => {
    const onExportAll = vi.fn();
    renderSidebar({ onExportAll });
    await userEvent.click(screen.getByText('Backup Content (.zip)'));
    expect(onExportAll).toHaveBeenCalledOnce();
  });

  it('calls onImport when Restore from Backup is clicked', async () => {
    const onImport = vi.fn();
    renderSidebar({ onImport });
    await userEvent.click(screen.getByText('Restore from Backup (.zip)'));
    expect(onImport).toHaveBeenCalledOnce();
  });
});

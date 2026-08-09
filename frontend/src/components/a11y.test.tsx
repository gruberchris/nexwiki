import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { FilterHelpModal } from './FilterHelpModal';
import { SidebarFilterHelpModal } from './SidebarFilterHelpModal';
import { ActivityFilterHelpModal } from './ActivityFilterHelpModal';
import { MarkdownSyntaxModal } from './MarkdownSyntaxModal';

// Icon-only controls carry no text, so without an aria-label a screen reader announces them as
// just "button". These modals are all dismissed by a bare <X/> button; this pins that each one
// says what it closes.
describe('icon-only close buttons have accessible names', () => {
  it('FilterHelpModal names its close button', () => {
    render(<FilterHelpModal onClose={vi.fn()} />);
    expect(screen.getByRole('button', { name: /close filter syntax help/i })).toBeInTheDocument();
  });

  it('SidebarFilterHelpModal names its close button', () => {
    render(<SidebarFilterHelpModal onClose={vi.fn()} />);
    expect(screen.getByRole('button', { name: /close sidebar filter syntax help/i })).toBeInTheDocument();
  });

  it('ActivityFilterHelpModal names its close button', () => {
    render(<ActivityFilterHelpModal onClose={vi.fn()} />);
    expect(screen.getByRole('button', { name: /close activity filter syntax help/i })).toBeInTheDocument();
  });

  it('MarkdownSyntaxModal names its close button', () => {
    render(<MarkdownSyntaxModal isOpen onClose={vi.fn()} />);
    expect(screen.getByRole('button', { name: /close markdown syntax guide/i })).toBeInTheDocument();
  });
});

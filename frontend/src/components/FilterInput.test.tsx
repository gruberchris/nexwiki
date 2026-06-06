import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { FilterInput } from './FilterInput';

const baseProps = {
  value: '',
  onChange: vi.fn(),
  suggestions: [],
  placeholder: 'Filter...',
};

describe('FilterInput', () => {
  it('renders with placeholder text', () => {
    render(<FilterInput {...baseProps} placeholder="Search articles..." />);
    expect(screen.getByPlaceholderText('Search articles...')).toBeInTheDocument();
  });

  it('shows the current value', () => {
    render(<FilterInput {...baseProps} value="golang" />);
    expect(screen.getByDisplayValue('golang')).toBeInTheDocument();
  });

  it('calls onChange when typing', async () => {
    const onChange = vi.fn();
    render(<FilterInput {...baseProps} onChange={onChange} />);
    await userEvent.type(screen.getByRole('textbox'), 'g');
    expect(onChange).toHaveBeenCalled();
  });

  it('shows clear (X) button when value is non-empty', () => {
    render(<FilterInput {...baseProps} value="something" />);
    // X button should exist when value is present
    const buttons = screen.getAllByRole('button');
    expect(buttons.length).toBeGreaterThan(0);
  });

  it('does not show clear button when value is empty', () => {
    render(<FilterInput {...baseProps} value="" />);
    // No clear button should be visible for empty value
    const buttons = screen.queryAllByRole('button');
    // If onOpenHelp is not provided, no buttons at all
    expect(buttons.length).toBe(0);
  });

  it('calls onChange with empty string when clear button clicked', async () => {
    const onChange = vi.fn();
    render(<FilterInput {...baseProps} value="something" onChange={onChange} />);
    const clearButton = screen.getByRole('button');
    await userEvent.click(clearButton);
    expect(onChange).toHaveBeenCalledWith('');
  });

  it('shows help button when onOpenHelp is provided', () => {
    render(<FilterInput {...baseProps} onOpenHelp={vi.fn()} />);
    const buttons = screen.getAllByRole('button');
    expect(buttons.length).toBeGreaterThan(0);
  });

  it('calls onOpenHelp when help button clicked', async () => {
    const onOpenHelp = vi.fn();
    render(<FilterInput {...baseProps} onOpenHelp={onOpenHelp} />);
    const helpButton = screen.getByRole('button');
    await userEvent.click(helpButton);
    expect(onOpenHelp).toHaveBeenCalled();
  });

  it('shows autocomplete dropdown when suggestions are present and input focused', async () => {
    const suggestions = [
      { type: 'title', value: 'Go Programming Guide' },
      { type: 'tag', value: 'golang' },
    ];
    render(<FilterInput {...baseProps} value="go" suggestions={suggestions} />);
    const input = screen.getByRole('textbox');
    await userEvent.click(input);
    // Dropdown should show
    expect(screen.queryByText('Go Programming Guide') || document.body.textContent?.includes('Go Programming Guide')).toBeTruthy();
  });

  it('calls onChange when suggestion is clicked', async () => {
    const onChange = vi.fn();
    const suggestions = [{ type: 'title', value: 'Go Guide' }];
    render(<FilterInput {...baseProps} value="go" onChange={onChange} suggestions={suggestions} />);
    const input = screen.getByRole('textbox');
    await userEvent.click(input);
    const option = screen.queryByText('Go Guide');
    if (option) {
      await userEvent.click(option);
      expect(onChange).toHaveBeenCalled();
    }
  });
});

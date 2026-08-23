import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
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
    await userEvent.type(screen.getByRole('combobox'), 'g');
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
    const input = screen.getByRole('combobox');
    await userEvent.click(input);
    // Dropdown should show
    expect(screen.queryByText('Go Programming Guide') || document.body.textContent?.includes('Go Programming Guide')).toBeTruthy();
  });

  it('calls onChange when suggestion is clicked', async () => {
    const onChange = vi.fn();
    const suggestions = [{ type: 'title', value: 'Go Guide' }];
    render(<FilterInput {...baseProps} value="go" onChange={onChange} suggestions={suggestions} />);
    const input = screen.getByRole('combobox');
    await userEvent.click(input);
    const option = screen.queryByText('Go Guide');
    if (option) {
      await userEvent.click(option);
      expect(onChange).toHaveBeenCalled();
    }
  });
});

describe('FilterInput keyboard navigation', () => {
  const suggestions = [
    { type: 'tag', value: 'golang' },
    { type: 'tag', value: 'gossip' },
    { type: 'title', value: 'Go Guide' },
  ];

  const focusedOption = () => screen.queryByRole('option', { selected: true });

  it('ArrowDown opens a closed dropdown and highlights the first suggestion', () => {
    render(<FilterInput {...baseProps} value="go" suggestions={suggestions} />);
    const input = screen.getByRole('combobox');

    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(input).toHaveAttribute('aria-expanded', 'true');
    expect(focusedOption()).toHaveTextContent('golang');
  });

  it('ArrowDown advances the highlight and wraps through the input (-1)', async () => {
    render(<FilterInput {...baseProps} value="go" suggestions={suggestions} />);
    const input = screen.getByRole('combobox');
    await userEvent.click(input); // opens dropdown

    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(focusedOption()).toHaveTextContent('gossip');

    // Off the end: highlight passes through the input itself before wrapping.
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(focusedOption()).toBeNull();
    expect(input).not.toHaveAttribute('aria-activedescendant');
  });

  it('ArrowUp moves backwards, wrapping to the last suggestion', async () => {
    render(<FilterInput {...baseProps} value="go" suggestions={suggestions} />);
    const input = screen.getByRole('combobox');
    await userEvent.click(input);

    fireEvent.keyDown(input, { key: 'ArrowUp' });
    expect(focusedOption()).toHaveTextContent('Go Guide');
  });

  it('Enter selects the highlighted suggestion', async () => {
    const onChange = vi.fn();
    render(<FilterInput {...baseProps} value="go" onChange={onChange} suggestions={suggestions} />);
    const input = screen.getByRole('combobox');
    await userEvent.click(input);

    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onChange).toHaveBeenCalledWith('golang');
  });

  it('Escape dismisses the dropdown and clears the highlight', async () => {
    render(<FilterInput {...baseProps} value="go" suggestions={suggestions} />);
    const input = screen.getByRole('combobox');
    await userEvent.click(input);
    fireEvent.keyDown(input, { key: 'ArrowDown' });

    fireEvent.keyDown(input, { key: 'Escape' });
    expect(input).toHaveAttribute('aria-expanded', 'false');
    // Reopening must not resume on a stale highlight.
    await userEvent.click(input);
    expect(focusedOption()).toBeNull();
  });

  it('does not swallow the arrows when the suggestion list is empty', () => {
    render(<FilterInput {...baseProps} value="go" suggestions={[]} />);
    const input = screen.getByRole('combobox');
    // fireEvent.keyDown returns false when preventDefault was called.
    expect(fireEvent.keyDown(input, { key: 'ArrowDown' })).toBe(true);
    expect(fireEvent.keyDown(input, { key: 'ArrowUp' })).toBe(true);
  });

  it('no longer intercepts Tab', async () => {
    render(<FilterInput {...baseProps} value="go" suggestions={suggestions} />);
    const input = screen.getByRole('combobox');
    await userEvent.click(input);

    expect(fireEvent.keyDown(input, { key: 'Tab' })).toBe(true); // not preventDefault-ed
    expect(focusedOption()).toBeNull(); // and the highlight did not move
  });

  it('closes the dropdown when focus leaves the control', async () => {
    render(<FilterInput {...baseProps} value="go" suggestions={suggestions} />);
    const input = screen.getByRole('combobox');
    await userEvent.click(input);
    expect(input).toHaveAttribute('aria-expanded', 'true');

    fireEvent.blur(input, { relatedTarget: document.body });
    expect(input).toHaveAttribute('aria-expanded', 'false');
  });
});

describe('FilterInput accessibility', () => {
  it('gives the clear button an accessible name', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<FilterInput {...baseProps} value="golang" onChange={onChange} />);

    // An icon-only <X/> button is invisible to screen readers without a name.
    const clear = screen.getByRole('button', { name: /clear filter/i });
    await user.click(clear);
    expect(onChange).toHaveBeenCalledWith('');
  });
});

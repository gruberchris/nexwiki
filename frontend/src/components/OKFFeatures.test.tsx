import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Editor } from './Editor';
import { ContentTypes, typeLabel, isComputation } from '../types';

describe('OKF v0.2 types and helpers', () => {
  it('includes Attested Computation in ContentTypes and labels', () => {
    expect(ContentTypes.Computation).toBe('Attested Computation');
    expect(typeLabel('Attested Computation')).toBe('Attested Computation');
    expect(isComputation({ type: 'Attested Computation' })).toBe(true);
    expect(isComputation({ type: 'Wiki' })).toBe(false);
  });
});

describe('Editor OKF v0.2 fields', () => {
  it('renders stale_after input field with initialStaleAfter and passes it on save', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    const onCancel = vi.fn();

    render(
      <Editor
        initialTitle="Test Article"
        initialContent="Hello world"
        initialStaleAfter="2026-12-31"
        slug="test-article"
        onSave={onSave}
        onCancel={onCancel}
        articles={[]}
      />
    );

    const staleInput = screen.getByPlaceholderText('Stale after (YYYY-MM-DD)...') as HTMLInputElement;
    expect(staleInput).toBeInTheDocument();
    expect(staleInput.value).toBe('2026-12-31');

    // Change value
    fireEvent.change(staleInput, { target: { value: '2027-01-15' } });
    expect(staleInput.value).toBe('2027-01-15');

    // Submit form
    const saveButton = screen.getByRole('button', { name: /save page/i });
    await userEvent.click(saveButton);

    await waitFor(() => {
      expect(onSave).toHaveBeenCalled();
    });

    // onSave arguments: title, content, editSummary, tags, description, source, resource, status, memoryKind, staleAfter
    expect(onSave).toHaveBeenCalledWith(
      'Test Article',
      'Hello world',
      expect.any(String),
      expect.any(Array),
      expect.any(String),
      expect.any(String),
      expect.any(String),
      expect.any(String),
      expect.any(String),
      '2027-01-15'
    );
  });
});

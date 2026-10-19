import { fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { ConfirmDialog, Dialog, PromptDialog } from './Dialog';

describe('Dialog focus', () => {
  it('focuses the first field, not the close button', () => {
    render(
      <PromptDialog
        open
        title="Rename"
        label="Name"
        initialValue="a.txt"
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(screen.getByLabelText('Name')).toHaveFocus();
  });

  it('falls back to the first control when there is no field', () => {
    render(
      <ConfirmDialog open title="Delete" message="Sure?" onConfirm={vi.fn()} onCancel={vi.fn()} />,
    );
    expect(screen.getByRole('button', { name: 'Close Delete' })).toHaveFocus();
  });

  it('restores focus to the invoker on close', () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>
            Open
          </button>
          <Dialog open={open} title="Add" onClose={() => setOpen(false)}>
            <input aria-label="First" />
          </Dialog>
        </>
      );
    }
    render(<Harness />);
    const opener = screen.getByRole('button', { name: 'Open' });
    opener.focus();
    fireEvent.click(opener);
    expect(screen.getByLabelText('First')).toHaveFocus();
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(opener).toHaveFocus();
  });
});

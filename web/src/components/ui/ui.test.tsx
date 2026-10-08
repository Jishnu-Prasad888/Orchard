import { act, fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { Menu, withDangerLast } from './Menu';
import { ToastProvider, useToast } from './Toast';

function Opener({ items }: { items: Parameters<typeof Menu>[0]['items'] }) {
  return (
    <>
      <button type="button" id="anchor">
        Open
      </button>
      <Menu
        anchor={{ x: 10, y: 10 }}
        items={items}
        onClose={() => document.body.setAttribute('data-closed', 'yes')}
        label="Actions"
      />
    </>
  );
}

describe('Menu', () => {
  const onRename = vi.fn();
  const onDelete = vi.fn();
  const items = withDangerLast([
    { id: 'delete', label: 'Delete', danger: true, onSelect: onDelete },
    { id: 'rename', label: 'Rename', onSelect: onRename },
    { id: 'move', label: 'Move', onSelect: () => {} },
  ]);

  it('puts destructive items last, after a separator', () => {
    expect(items.map((i) => (i === 'separator' ? '—' : i.id))).toEqual([
      'rename',
      'move',
      '—',
      'delete',
    ]);
  });

  it('moves focus into the menu and walks it with the arrow keys', () => {
    render(<Opener items={items} />);
    const menu = screen.getByRole('menu', { name: 'Actions' });
    const entries = screen.getAllByRole('menuitem');
    expect(entries[0]).toHaveFocus();
    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    expect(entries[1]).toHaveFocus();
    fireEvent.keyDown(menu, { key: 'End' });
    expect(entries[2]).toHaveFocus();
    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    expect(entries[0]).toHaveFocus();
  });

  it('jumps by typing the first letters of an item', () => {
    render(<Opener items={items} />);
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'd' });
    expect(screen.getByRole('menuitem', { name: 'Delete' })).toHaveFocus();
  });

  it('runs the chosen item and closes', () => {
    document.body.removeAttribute('data-closed');
    render(<Opener items={items} />);
    fireEvent.click(screen.getByRole('menuitem', { name: 'Rename' }));
    expect(onRename).toHaveBeenCalled();
    expect(document.body.getAttribute('data-closed')).toBe('yes');
  });

  it('closes on Escape and on an outside press', () => {
    document.body.removeAttribute('data-closed');
    const { unmount } = render(<Opener items={items} />);
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });
    expect(document.body.getAttribute('data-closed')).toBe('yes');
    unmount();

    document.body.removeAttribute('data-closed');
    render(<Opener items={items} />);
    fireEvent.pointerDown(document.body);
    expect(document.body.getAttribute('data-closed')).toBe('yes');
  });

  it('skips disabled items when walking', () => {
    render(
      <Opener
        items={[
          { id: 'a', label: 'A' },
          { id: 'b', label: 'B', disabled: true },
          { id: 'c', label: 'C' },
        ]}
      />,
    );
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'ArrowDown' });
    expect(screen.getByRole('menuitem', { name: 'C' })).toHaveFocus();
  });
});

describe('Toast', () => {
  function Fire() {
    const toast = useToast();
    return (
      <button
        onClick={() => {
          toast({ message: 'Added to album', tone: 'success' });
          toast({ message: 'Second' });
          toast({ message: 'Third' });
          toast({ message: 'Fourth' });
        }}
      >
        fire
      </button>
    );
  }

  it('announces politely, keeps at most three, and dismisses itself', () => {
    vi.useFakeTimers();
    try {
      render(
        <ToastProvider>
          <Fire />
        </ToastProvider>,
      );
      fireEvent.click(screen.getByText('fire'));
      expect(screen.getAllByTestId('toast')).toHaveLength(3);
      expect(screen.queryByText('Added to album')).not.toBeInTheDocument();
      expect(document.querySelector('[aria-live="polite"]')).toBeInTheDocument();
      act(() => {
        vi.advanceTimersByTime(5000);
      });
      expect(screen.queryAllByTestId('toast')).toHaveLength(0);
    } finally {
      vi.useRealTimers();
    }
  });

  it('can be dismissed by hand', () => {
    render(
      <ToastProvider>
        <Fire />
      </ToastProvider>,
    );
    fireEvent.click(screen.getByText('fire'));
    fireEvent.click(screen.getAllByRole('button', { name: 'Dismiss notification' })[0]!);
    expect(screen.getAllByTestId('toast')).toHaveLength(2);
  });
});

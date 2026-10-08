import { act, fireEvent, render, renderHook, screen, within } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { fileFixture } from '../../test/harness';
import { MediaGrid } from './MediaGrid';
import { useSelection } from './useSelection';

const files = Array.from({ length: 12 }, (_, i) =>
  fileFixture({
    id: `f${i}`,
    name: `photo-${i}.jpg`,
    rel_path: `photo-${i}.jpg`,
    mod_time: i < 6 ? '2026-09-28T10:00:00' : '2026-09-21T10:00:00',
  }),
);

function Harness({ onOpen = () => {} }: { onOpen?: (f: unknown) => void }) {
  const selection = useSelection(files, 'k');
  return (
    <>
      <p data-testid="count">{selection.size}</p>
      <MediaGrid
        libraryId="lib1"
        files={files}
        grouping="day"
        onOpen={onOpen}
        selection={selection}
        label="Photos"
      />
    </>
  );
}

describe('MediaGrid', () => {
  it('renders tiles under date headings', () => {
    render(<Harness />);
    expect(screen.getAllByTestId('media-tile')).toHaveLength(12);
    expect(screen.getAllByRole('heading', { level: 2 })).toHaveLength(2);
  });

  it('mounts only the rows near the viewport for a huge list', () => {
    const many = Array.from({ length: 20_000 }, (_, i) =>
      fileFixture({ id: `x${i}`, name: `${i}.jpg` }),
    );
    render(<MediaGrid libraryId="lib1" files={many} onOpen={() => {}} label="Big" />);
    expect(screen.getAllByTestId('media-tile').length).toBeLessThan(400);
  });

  it('opens a photo on click', () => {
    const onOpen = vi.fn();
    render(<Harness onOpen={onOpen} />);
    fireEvent.click(screen.getByRole('button', { name: 'photo-3.jpg' }));
    expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: 'f3' }));
  });

  it('selects with the circle, then a plain click adds to the selection instead of opening', () => {
    const onOpen = vi.fn();
    render(<Harness onOpen={onOpen} />);
    fireEvent.click(screen.getByRole('button', { name: 'Select photo-1.jpg' }));
    expect(screen.getByTestId('count')).toHaveTextContent('1');
    fireEvent.click(screen.getByRole('button', { name: 'photo-2.jpg' }));
    expect(screen.getByTestId('count')).toHaveTextContent('2');
    expect(onOpen).not.toHaveBeenCalled();
  });

  it('extends a range with shift-click', () => {
    render(<Harness />);
    fireEvent.click(screen.getByRole('button', { name: 'Select photo-1.jpg' }));
    fireEvent.click(screen.getByRole('button', { name: 'photo-4.jpg' }), { shiftKey: true });
    expect(screen.getByTestId('count')).toHaveTextContent('4');
  });

  it('toggles with Ctrl-click and selects everything with Ctrl+A', () => {
    render(<Harness />);
    const tile = screen.getByRole('button', { name: 'photo-0.jpg' });
    fireEvent.click(tile, { ctrlKey: true });
    expect(screen.getByTestId('count')).toHaveTextContent('1');
    fireEvent.keyDown(tile, { key: 'a', ctrlKey: true });
    expect(screen.getByTestId('count')).toHaveTextContent('12');
  });

  it('selects the focused tile with Space and clears with Escape', () => {
    render(<Harness />);
    const tile = screen.getByRole('button', { name: 'photo-2.jpg' });
    fireEvent.keyDown(tile, { key: ' ' });
    expect(screen.getByTestId('count')).toHaveTextContent('1');
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.getByTestId('count')).toHaveTextContent('0');
  });

  it('keeps one tile in the tab order and moves focus with the arrow keys', () => {
    render(<Harness />);
    const grid = screen.getByTestId('media-grid');
    const tabbable = within(grid)
      .getAllByRole('button')
      .filter((b) => b.getAttribute('tabindex') === '0' && b.classList.contains('media-tile-main'));
    expect(tabbable).toHaveLength(1);

    const first = screen.getByRole('button', { name: 'photo-0.jpg' });
    first.focus();
    fireEvent.keyDown(first, { key: 'ArrowRight' });
    expect(screen.getByRole('button', { name: 'photo-1.jpg' })).toHaveFocus();
  });

  it('starts a selection with a long press on touch', () => {
    vi.useFakeTimers();
    try {
      render(<Harness />);
      const tile = screen.getByRole('button', { name: 'photo-5.jpg' });
      fireEvent.pointerDown(tile, { pointerType: 'touch', clientX: 10, clientY: 10 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(screen.getByTestId('count')).toHaveTextContent('1');
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('useSelection', () => {
  it('drops the selection when the list identity changes', () => {
    const { result, rerender } = renderHook(({ k }) => useSelection(files, k), {
      initialProps: { k: 'a' },
    });
    act(() => result.current.toggle(files[0]!));
    expect(result.current.size).toBe(1);
    rerender({ k: 'b' });
    expect(result.current.size).toBe(0);
  });

  it('reports the selected files in list order', () => {
    function Probe() {
      const s = useSelection(files, 'k');
      const [, force] = useState(0);
      return (
        <button
          onClick={() => {
            s.toggle(files[3]!);
            s.toggle(files[1]!);
            force(1);
          }}
        >
          {s.selectedFiles.map((f) => f.id).join(',')}
        </button>
      );
    }
    render(<Probe />);
    fireEvent.click(screen.getByRole('button'));
    expect(screen.getByRole('button')).toHaveTextContent('f1,f3');
  });
});

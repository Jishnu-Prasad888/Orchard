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

  it('shows a resume-progress bar only on the files given one', () => {
    const books = [
      fileFixture({ id: 'b1', name: 'reading.epub', media_type: 'document' }),
      fileFixture({ id: 'b2', name: 'untouched.epub', media_type: 'document' }),
    ];
    render(
      <MediaGrid
        libraryId="lib1"
        files={books}
        onOpen={() => {}}
        progress={new Map([['b1', 42]])}
        label="Books"
      />,
    );
    const reading = screen.getByRole('button', { name: 'reading.epub' }).closest('.media-tile');
    const untouched = screen.getByRole('button', { name: 'untouched.epub' }).closest('.media-tile');
    expect(within(reading as HTMLElement).getByRole('progressbar')).toHaveAttribute(
      'aria-valuenow',
      '42',
    );
    expect(within(untouched as HTMLElement).queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it("shows each book's own reading progress as a bar and a percentage", () => {
    const books = [
      fileFixture({
        id: 'b1',
        name: 'halfway.pdf',
        media_type: 'document',
        progress_percent: 37.4,
      }),
      fileFixture({
        id: 'b2',
        name: 'just-opened.pdf',
        media_type: 'document',
        progress_percent: 0.2,
      }),
      fileFixture({ id: 'b3', name: 'unread.pdf', media_type: 'document' }),
    ];
    render(<MediaGrid libraryId="lib1" files={books} onOpen={() => {}} label="Books" />);
    const tile = (name: string) =>
      screen.getByRole('button', { name }).closest('.media-tile') as HTMLElement;

    expect(within(tile('halfway.pdf')).getByText('37%')).toBeInTheDocument();
    expect(within(tile('halfway.pdf')).getByRole('progressbar')).toHaveAttribute(
      'aria-valuenow',
      '37',
    );
    // A book that has been opened at all never reads as 0%.
    expect(within(tile('just-opened.pdf')).getByText('1%')).toBeInTheDocument();
    expect(within(tile('unread.pdf')).queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('shows a book cover as its thumbnail instead of the document glyph', () => {
    const books = [
      fileFixture({
        id: 'b1',
        name: 'covered.epub',
        media_type: 'document',
        cover_url: '/api/v1/libraries/lib1/files/b1/cover',
      }),
      fileFixture({ id: 'b2', name: 'no-cover.epub', media_type: 'document' }),
    ];
    render(<MediaGrid libraryId="lib1" files={books} onOpen={() => {}} label="Books" />);
    const covered = screen.getByRole('button', { name: 'covered.epub' });
    const uncovered = screen.getByRole('button', { name: 'no-cover.epub' });
    expect(covered.querySelector('img')).toHaveAttribute(
      'src',
      '/api/v1/libraries/lib1/files/b1/cover',
    );
    expect(uncovered.querySelector('img')).not.toBeInTheDocument();
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

  it('puts a book title under its cover, and its tags in the caption', () => {
    const books = [
      fileFixture({
        id: 'b1',
        name: 'dune_v2.epub',
        rel_path: 'dune_v2.epub',
        media_type: 'document',
        title: 'Dune',
        tags: ['classics', 'sci-fi'],
      }),
      fileFixture({
        id: 'b2',
        name: 'untitled-notes.pdf',
        rel_path: 'untitled-notes.pdf',
        media_type: 'document',
      }),
    ];
    render(<MediaGrid libraryId="lib1" files={books} onOpen={() => {}} label="Books" />);

    // Under the cover, and — with no artwork — written on a plain cover too.
    expect(screen.getAllByText('Dune').map((el) => el.className)).toEqual([
      'media-tile-cover-title',
      'media-tile-title',
    ]);
    // No title in the metadata: the file name, without its extension.
    expect(
      screen.getAllByText('untitled-notes').some((el) => el.classList.contains('media-tile-title')),
    ).toBe(true);
    const tagList = screen.getByTestId('media-tile-tags');
    expect(within(tagList).getByText('classics')).toBeInTheDocument();
    expect(within(tagList).getByText('sci-fi')).toBeInTheDocument();
    // The name is below the cover already, so the caption does not repeat it.
    expect(screen.queryByText('dune_v2.epub')).not.toBeInTheDocument();
  });

  it('keeps a photo grid edge to edge, with no title band', () => {
    render(<Harness />);
    expect(document.querySelector('.media-tile-title')).toBeNull();
  });
});

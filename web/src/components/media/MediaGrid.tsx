/**
 * The media grid: Cairn's main surface.
 *
 * Square thumbnails edge to edge, optionally grouped under quiet date headings,
 * with selection, keyboard navigation, and infinite scroll. Only the rows near
 * the viewport are mounted (see `layout.ts`), so the cost of a page does not
 * grow with the size of the library — a hundred thousand loaded items still
 * render a few dozen tiles.
 *
 * The window is the scroll container. That keeps one natural scrollbar for the
 * whole page instead of a grid scrolling inside a page that also scrolls.
 */

import {
  type MouseEvent as ReactMouseEvent,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';

import type { FileSummary } from '../../api/types';
import { MediaTile } from './MediaTile';
import {
  type Grouping,
  groupFiles,
  gridMetrics,
  layoutGrid,
  moveFocus,
  visibleRows,
} from './layout';
import type { Selection } from './useSelection';
import './MediaGrid.css';

/** Width assumed before the container has been measured (and in jsdom). */
const FALLBACK_WIDTH = 1024;
/** Rows rendered beyond the viewport, in px, so fast scrolling stays filled. */
const OVERSCAN = 800;
/** Start fetching the next page this far before the end. */
const PREFETCH_MARGIN = '1200px';

export interface MediaGridProps {
  libraryId: string;
  files: FileSummary[];
  /** Group under date headings. Only meaningful for a date-sorted list. */
  grouping?: Grouping;
  onOpen: (file: FileSummary) => void;
  /** Omit to make the grid read-only (no selection circles). */
  selection?: Selection;
  favorites?: ReadonlySet<string>;
  /** Right-click or long-press menu for a single tile. */
  onContextMenu?: (file: FileSummary, x: number, y: number) => void;
  /** Infinite scroll: more pages exist and how to fetch the next. */
  hasMore?: boolean;
  loadingMore?: boolean;
  onLoadMore?: () => void;
  /** Accessible name for the grid region ("Photos", "Album: Summer"). */
  label: string;
  testId?: string;
}

export function MediaGrid({
  libraryId,
  files,
  grouping = 'none',
  onOpen,
  selection,
  favorites,
  onContextMenu,
  hasMore = false,
  loadingMore = false,
  onLoadMore,
  label,
  testId = 'media-grid',
}: MediaGridProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const sentinelRef = useRef<HTMLDivElement | null>(null);
  const [box, setBox] = useState({ width: 0, gap: 4 });
  const [range, setRange] = useState<[number, number]>([0, -1]);
  const [focusIndex, setFocusIndex] = useState(0);
  const pendingFocus = useRef<number | null>(null);

  const groups = useMemo(() => groupFiles(files, grouping), [files, grouping]);
  const layout = useMemo(
    () => layoutGrid(groups, gridMetrics(box.width || FALLBACK_WIDTH, box.gap)),
    [groups, box],
  );

  // Scroll handlers read the latest layout through a ref, synced before the
  // range is recomputed below.
  const layoutRef = useRef(layout);
  useLayoutEffect(() => {
    layoutRef.current = layout;
  }, [layout]);

  // Track the container's width; tiles resize to fill each row exactly.
  useLayoutEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const measure = () => {
      const width = Math.round(el.getBoundingClientRect().width);
      const gap = readGap(el);
      setBox((prev) => (prev.width === width && prev.gap === gap ? prev : { width, gap }));
    };
    measure();
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  // Work out which rows are on screen. Runs on scroll (throttled to frames)
  // and whenever the layout changes; state only changes when the visible
  // window actually moves to a different set of rows.
  const updateRange = useCallback(() => {
    const el = containerRef.current;
    if (!el) return;
    const top = -el.getBoundingClientRect().top;
    const next = visibleRows(layoutRef.current.rows, top, top + window.innerHeight, OVERSCAN);
    setRange((prev) => (prev[0] === next[0] && prev[1] === next[1] ? prev : next));
  }, []);

  useLayoutEffect(updateRange, [layout, updateRange]);

  useEffect(() => {
    let frame = 0;
    const onScroll = () => {
      if (frame) return;
      frame = requestAnimationFrame(() => {
        frame = 0;
        updateRange();
      });
    };
    window.addEventListener('scroll', onScroll, { passive: true });
    window.addEventListener('resize', onScroll);
    return () => {
      window.removeEventListener('scroll', onScroll);
      window.removeEventListener('resize', onScroll);
      if (frame) cancelAnimationFrame(frame);
    };
  }, [updateRange]);

  // Infinite scroll: ask for the next page as the end comes into view.
  useEffect(() => {
    const sentinel = sentinelRef.current;
    if (!sentinel || !hasMore || !onLoadMore || typeof IntersectionObserver === 'undefined') {
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) onLoadMore();
      },
      { rootMargin: PREFETCH_MARGIN },
    );
    observer.observe(sentinel);
    return () => observer.disconnect();
  }, [hasMore, onLoadMore, files.length]);

  // Keep the roving tab stop on a file that still exists.
  const safeFocus = Math.min(focusIndex, Math.max(0, files.length - 1));

  // Move keyboard focus to a tile, scrolling it into view first if its row is
  // not mounted yet; focus is applied once the row has rendered.
  const focusTile = useCallback((index: number) => {
    const current = layoutRef.current;
    const rowIndex = current.rowOfItem[index];
    const row = rowIndex === undefined ? undefined : current.rows[rowIndex];
    const el = containerRef.current;
    if (row && el) {
      const containerTop = el.getBoundingClientRect().top + window.scrollY;
      const rowTop = containerTop + row.top;
      const viewTop = window.scrollY + stickyOffset();
      const viewBottom = window.scrollY + window.innerHeight;
      if (rowTop < viewTop) {
        window.scrollTo({ top: rowTop - stickyOffset() - 8 });
      } else if (rowTop + row.height > viewBottom) {
        window.scrollTo({ top: rowTop + row.height - window.innerHeight + 8 });
      }
    }
    pendingFocus.current = index;
    setFocusIndex(index);
  }, []);

  useEffect(() => {
    const index = pendingFocus.current;
    if (index === null) return;
    const button = containerRef.current?.querySelector<HTMLElement>(`[data-index="${index}"]`);
    if (button) {
      pendingFocus.current = null;
      button.focus({ preventScroll: true });
    }
  });

  const selectable = selection !== undefined;
  const selecting = selection?.active ?? false;

  const onActivate = useCallback(
    (file: FileSummary, _index: number, event: ReactMouseEvent) => {
      if (selection) {
        if (event.shiftKey) return selection.toggle(file, true);
        if (event.metaKey || event.ctrlKey) return selection.toggle(file);
        // Once something is selected, a click adds to the selection instead of
        // opening — the way every photo library behaves.
        if (selection.active) return selection.toggle(file);
      }
      onOpen(file);
    },
    [onOpen, selection],
  );

  const onToggleSelect = useCallback(
    (file: FileSummary, _index: number, range: boolean) => selection?.toggle(file, range),
    [selection],
  );

  const onKeyDown = (event: React.KeyboardEvent) => {
    const target = event.target as HTMLElement;
    if (!target.matches('.media-tile-main')) return;
    const index = Number(target.dataset.index);
    switch (event.key) {
      case 'ArrowLeft':
      case 'ArrowRight':
      case 'ArrowUp':
      case 'ArrowDown':
      case 'Home':
      case 'End': {
        event.preventDefault();
        const next = moveFocus(layout, index, event.key);
        if (next >= 0) focusTile(next);
        if (next >= files.length - layout.metrics.columns && hasMore && !loadingMore)
          onLoadMore?.();
        return;
      }
      case ' ':
      case 'x':
        if (!selection) return;
        event.preventDefault();
        selection.toggle(files[index]!, event.shiftKey);
        return;
      case 'a':
        if (!selection || !(event.metaKey || event.ctrlKey)) return;
        event.preventDefault();
        selection.selectAll();
        return;
      default:
        return;
    }
  };

  const [first, last] = range;
  const visible = last >= first ? layout.rows.slice(first, last + 1) : [];
  const { tileSize, gap } = layout.metrics;

  return (
    <section className="media-grid-wrap" aria-label={label}>
      <div
        ref={containerRef}
        className={selecting ? 'media-grid is-selecting' : 'media-grid'}
        style={{ height: layout.totalHeight }}
        onKeyDown={onKeyDown}
        data-testid={testId}
      >
        {visible.map((row) =>
          row.kind === 'header' ? (
            <h2
              key={row.key}
              className="media-date"
              style={{ transform: `translateY(${row.top}px)`, height: row.height }}
            >
              {row.label}
            </h2>
          ) : (
            <div
              key={row.key}
              className="media-row"
              style={{ transform: `translateY(${row.top}px)`, height: tileSize }}
            >
              {row.items.map((file, j) => {
                const index = row.startIndex + j;
                return (
                  <MediaTile
                    key={file.id}
                    libraryId={libraryId}
                    file={file}
                    index={index}
                    style={{ width: tileSize, height: tileSize, left: j * (tileSize + gap) }}
                    selected={selection?.isSelected(file.id) ?? false}
                    selectable={selectable}
                    selecting={selecting}
                    favorite={favorites?.has(file.id) ?? false}
                    tabbable={index === safeFocus}
                    onActivate={onActivate}
                    onToggleSelect={onToggleSelect}
                    onFocusTile={setFocusIndex}
                    onContextMenu={onContextMenu}
                  />
                );
              })}
            </div>
          ),
        )}
      </div>
      <div ref={sentinelRef} className="media-grid-sentinel" aria-hidden="true" />
      {hasMore && onLoadMore && (
        <div className="media-grid-more">
          <button
            type="button"
            className="button ghost-button"
            onClick={onLoadMore}
            disabled={loadingMore}
            data-testid="load-more"
          >
            {loadingMore ? 'Loading more…' : 'Load more'}
          </button>
        </div>
      )}
    </section>
  );
}

/** The gap between tiles, read from the `--media-gap` token. */
function readGap(el: HTMLElement): number {
  if (typeof getComputedStyle === 'undefined') return 4;
  const value = parseFloat(getComputedStyle(el).getPropertyValue('--media-gap'));
  return Number.isFinite(value) ? value : 4;
}

/** Height of the sticky top bar, so keyboard scrolling keeps tiles out from under it. */
function stickyOffset(): number {
  const bar = document.querySelector<HTMLElement>('[data-sticky-top]');
  return bar ? bar.getBoundingClientRect().height : 0;
}

/** Placeholder squares in the grid's own geometry, while the first page loads. */
export function MediaGridSkeleton({ count = 18 }: { count?: number }) {
  return (
    <div className="media-grid-skeleton" aria-hidden="true" data-testid="grid-skeleton">
      <div className="skeleton media-skeleton-heading" />
      <div className="media-skeleton-tiles">
        {Array.from({ length: count }, (_, i) => (
          <div key={i} className="skeleton media-skeleton-tile" />
        ))}
      </div>
    </div>
  );
}

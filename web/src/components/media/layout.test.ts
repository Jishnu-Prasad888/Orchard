import { describe, expect, it } from 'vitest';

import { fileFixture } from '../../test/harness';
import { groupFiles, gridMetrics, layoutGrid, moveFocus, visibleRows } from './layout';

const day = (n: number, id: string) =>
  fileFixture({
    id,
    name: `${id}.jpg`,
    mod_time: `2026-09-${String(n).padStart(2, '0')}T12:00:00`,
  });

describe('groupFiles', () => {
  const now = new Date(2026, 8, 30);

  it('groups runs of the same day, keeping order', () => {
    const groups = groupFiles([day(28, 'a'), day(28, 'b'), day(21, 'c')], 'day', now);
    expect(groups.map((g) => g.items.map((f) => f.id))).toEqual([['a', 'b'], ['c']]);
    expect(groups.map((g) => g.startIndex)).toEqual([0, 2]);
  });

  it('puts everything in one unlabelled group when not grouping', () => {
    const groups = groupFiles([day(1, 'a'), day(2, 'b')], 'none', now);
    expect(groups).toHaveLength(1);
    expect(groups[0]!.label).toBe('');
  });

  it('returns no groups for no files', () => {
    expect(groupFiles([], 'day', now)).toEqual([]);
    expect(groupFiles([], 'none', now)).toEqual([]);
  });
});

describe('gridMetrics', () => {
  it('fills the row exactly', () => {
    const { columns, tileSize, gap } = gridMetrics(1000, 4);
    expect(columns * tileSize + (columns - 1) * gap).toBeCloseTo(1000);
  });

  it('never goes below three columns, even on a narrow phone', () => {
    expect(gridMetrics(320, 2).columns).toBeGreaterThanOrEqual(3);
  });

  it('uses more columns on a wider container', () => {
    expect(gridMetrics(1600, 4).columns).toBeGreaterThan(gridMetrics(800, 4).columns);
  });
});

describe('layoutGrid and visibleRows', () => {
  const files = Array.from({ length: 1000 }, (_, i) => day((i % 28) + 1, `f${i}`));
  const groups = groupFiles(files, 'none');
  const layout = layoutGrid(groups, gridMetrics(1000, 4));

  it('lays every file out exactly once', () => {
    const placed = layout.rows.flatMap((r) => (r.kind === 'tiles' ? r.items : []));
    expect(placed).toHaveLength(1000);
    expect(layout.rowOfItem).toHaveLength(1000);
  });

  it('computes rows tall enough for the content', () => {
    const last = layout.rows[layout.rows.length - 1]!;
    expect(layout.totalHeight).toBe(last.top + last.height);
  });

  it('selects only the rows near the viewport', () => {
    const [first, last] = visibleRows(layout.rows, 5000, 5800, 400);
    expect(last - first + 1).toBeLessThan(layout.rows.length / 4);
    expect(layout.rows[first]!.top + layout.rows[first]!.height).toBeGreaterThan(4600);
    expect(layout.rows[first]!.top).toBeLessThanOrEqual(5800);
  });

  it('copes with a huge library without rendering it all', () => {
    const big = Array.from({ length: 100_000 }, (_, i) => day((i % 28) + 1, `b${i}`));
    const bigLayout = layoutGrid(groupFiles(big, 'none'), gridMetrics(1200, 4));
    const [first, last] = visibleRows(bigLayout.rows, 200_000, 201_000, 800);
    expect(last - first + 1).toBeLessThan(20);
  });

  it('has no visible rows for an empty list', () => {
    expect(visibleRows([], 0, 100, 10)).toEqual([0, -1]);
  });
});

describe('moveFocus', () => {
  const files = Array.from({ length: 23 }, (_, i) => day(1, `m${i}`));
  const layout = layoutGrid(groupFiles(files, 'none'), { columns: 5, tileSize: 100, gap: 4 });

  it('steps through the list with left and right, and stops at the ends', () => {
    expect(moveFocus(layout, 0, 'ArrowLeft')).toBe(0);
    expect(moveFocus(layout, 3, 'ArrowRight')).toBe(4);
    expect(moveFocus(layout, 22, 'ArrowRight')).toBe(22);
  });

  it('moves a row at a time with up and down, keeping the column', () => {
    expect(moveFocus(layout, 7, 'ArrowDown')).toBe(12);
    expect(moveFocus(layout, 12, 'ArrowUp')).toBe(7);
    expect(moveFocus(layout, 2, 'ArrowUp')).toBe(2);
  });

  it('clamps to a short last row', () => {
    // Row 4 holds items 20-22; column 4 of row 3 goes to its last item.
    expect(moveFocus(layout, 19, 'ArrowDown')).toBe(22);
  });

  it('jumps to the ends with Home and End', () => {
    expect(moveFocus(layout, 9, 'Home')).toBe(0);
    expect(moveFocus(layout, 9, 'End')).toBe(22);
  });
});

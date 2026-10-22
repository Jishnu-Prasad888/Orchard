import { describe, expect, it } from 'vitest';

import { DEFAULT_READER_PREFERENCES } from '../../api/types';
import { ZOOM_MAX_FONT, ZOOM_MIN_FONT, zoomDirection, zoomed, zoomPercent } from './zoom';

describe('zoom', () => {
  it('reads Ctrl/⌘ keys, including + typed with shift', () => {
    expect(zoomDirection('=')).toBe(1);
    expect(zoomDirection('+')).toBe(1);
    expect(zoomDirection('-')).toBe(-1);
    expect(zoomDirection('0')).toBe(0);
    expect(zoomDirection('a')).toBeNull();
  });

  it('steps the font size within bounds, and 0 resets it', () => {
    const big = { ...DEFAULT_READER_PREFERENCES, font_size: ZOOM_MAX_FONT };
    expect(zoomed(big, 1).font_size).toBe(ZOOM_MAX_FONT);
    const small = { ...DEFAULT_READER_PREFERENCES, font_size: ZOOM_MIN_FONT };
    expect(zoomed(small, -1).font_size).toBe(ZOOM_MIN_FONT);
    expect(zoomed(big, 0).font_size).toBe(DEFAULT_READER_PREFERENCES.font_size);
    expect(zoomPercent(DEFAULT_READER_PREFERENCES)).toBe(100);
  });
});

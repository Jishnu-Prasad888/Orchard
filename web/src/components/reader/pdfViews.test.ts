import { describe, expect, it } from 'vitest';

import { buildViews, viewIndexOf } from './pdfViews';

describe('buildViews', () => {
  it('gives every page its own view when showing one page', () => {
    expect(buildViews(3, 1)).toEqual([[1], [2], [3]]);
  });

  it('keeps the cover alone and pairs the rest when showing two pages', () => {
    expect(buildViews(6, 2)).toEqual([[1], [2, 3], [4, 5], [6]]);
    expect(buildViews(5, 2)).toEqual([[1], [2, 3], [4, 5]]);
  });

  it('handles an empty and a one-page document', () => {
    expect(buildViews(0, 2)).toEqual([]);
    expect(buildViews(1, 2)).toEqual([[1]]);
  });
});

describe('viewIndexOf', () => {
  it('finds the view that shows each page', () => {
    for (const perView of [1, 2] as const) {
      const views = buildViews(9, perView);
      for (let page = 1; page <= 9; page++) {
        expect(views[viewIndexOf(page, perView)]).toContain(page);
      }
    }
  });
});

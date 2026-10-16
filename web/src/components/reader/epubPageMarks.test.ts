import type { Contents } from 'epubjs';
import { describe, expect, it, vi } from 'vitest';

import { PAGE_MARKS_CLASS, drawPageMarks, findPageMarks } from './epubPageMarks';

/** A stand-in for an epub.js chapter: its own document, plus range lookup. */
function chapter(html: string, ranges: Record<string, Range> = {}): Contents {
  const doc = document.implementation.createHTMLDocument('chapter');
  doc.body.innerHTML = html;
  return {
    document: doc,
    window: window,
    cfiBase: '/6/4',
    range: (cfi: string) => {
      const r = ranges[cfi];
      if (!r) throw new Error('no such cfi');
      return r;
    },
  } as unknown as Contents;
}

function at(el: Element, top: number) {
  vi.spyOn(el, 'getBoundingClientRect').mockReturnValue({
    top,
    left: 0,
    width: 10,
    height: 16,
    right: 10,
    bottom: top + 16,
    x: 0,
    y: top,
    toJSON: () => ({}),
  } as DOMRect);
}

describe('findPageMarks', () => {
  it("uses the publisher's print page markers when the book has them", () => {
    const contents = chapter(
      '<p>one</p><span role="doc-pagebreak" aria-label="12"></span><p>two</p>' +
        '<span epub:type="pagebreak" title="xiii"></span><p>three</p>',
    );
    const [first, second] = contents.document.querySelectorAll('span');
    at(first!, 100);
    at(second!, 300);

    expect(findPageMarks(contents, null)).toEqual([
      { top: 100, label: 'Page 12' },
      { top: 300, label: 'Page xiii' },
    ]);
  });

  it("otherwise marks the end of each of this chapter's reading pages", () => {
    const contents = chapter('<p>alpha beta</p><p>gamma delta</p>');
    const [, second] = contents.document.querySelectorAll('p');
    const range = contents.document.createRange();
    range.setStart(second!.firstChild!, 0);
    range.setEnd(second!.firstChild!, 5);
    // jsdom does no layout: give the range the line box a browser would.
    range.getClientRects = () =>
      [{ top: 240, left: 0, width: 8, height: 16 } as DOMRect] as unknown as DOMRectList;
    range.cloneRange = () => range;

    const locations = {
      length: () => 4,
      cfiFromLocation: (i: number) =>
        [
          'epubcfi(/6/2!/4/2/1:0)', // another chapter
          'epubcfi(/6/2!/4/2/1:9)', // another chapter
          'epubcfi(/6/4!/4/4/1:0)', // this one: page 2 ends here
          'epubcfi(/6/8!/4/2/1:0)', // a later chapter
        ][i]!,
    };
    const marks = findPageMarks(
      { ...contents, range: () => range } as unknown as Contents,
      locations,
    );
    expect(marks).toEqual([{ top: 240, label: 'End of page 2' }]);
    vi.restoreAllMocks();
  });
});

describe('drawPageMarks', () => {
  it('draws on one layer after the text, replacing the last drawing', () => {
    const contents = chapter('<p>one</p><span role="doc-pagebreak" aria-label="5"></span>');
    at(contents.document.querySelector('span')!, 80);

    drawPageMarks(contents, null);
    drawPageMarks(contents, null);

    const layers = contents.document.querySelectorAll(`.${PAGE_MARKS_CLASS}`);
    expect(layers).toHaveLength(1);
    // Appended last, so the book's own nodes keep their positions (CFIs).
    expect(contents.document.body.lastElementChild).toBe(layers[0]);
    expect(layers[0]!.querySelector('[data-label]')?.getAttribute('data-label')).toBe('Page 5');
    vi.restoreAllMocks();
  });
});

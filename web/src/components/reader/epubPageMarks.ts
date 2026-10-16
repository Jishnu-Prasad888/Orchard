/**
 * Page-break lines for an EPUB read as one continuous scroll.
 *
 * An EPUB has no fixed pages, so there are two sources of "where a page
 * ends", in order of preference:
 *
 *   print page markers  `epub:type="pagebreak"` / `role="doc-pagebreak"`
 *                       elements a publisher left where the printed
 *                       edition's pages begin — real page numbers
 *   reading locations   epub.js's location map (a page per ~1000
 *                       characters), the same one progress is measured
 *                       against — so "page 12" here is page 12 in the
 *                       page counter when reading page by page
 *
 * The lines are drawn on a layer appended to the end of the chapter's body,
 * never inserted into the text: inserting elements among the book's own would
 * shift the CFIs that saved positions, bookmarks, and highlights point at.
 */

import type { Contents } from 'epubjs';

export const PAGE_MARKS_CLASS = 'orchard-page-marks';
const STYLE_ID = 'orchard-page-marks-style';
const OPS_NS = 'http://www.idpf.org/2007/ops';

/** The part of epub.js's Locations the marks need. */
export interface PageLocations {
  length(): number;
  cfiFromLocation(location: number): string;
}

export interface PageMark {
  /** Document y coordinate of the break. */
  top: number;
  label: string;
}

function isPrintPageBreak(el: Element): boolean {
  if (el.getAttribute('role') === 'doc-pagebreak') return true;
  const type = el.getAttributeNS(OPS_NS, 'type') || el.getAttribute('epub:type') || '';
  return /(^|\s)pagebreak(\s|$)/.test(type);
}

function printLabel(el: Element): string {
  const raw = (
    el.getAttribute('aria-label') ||
    el.getAttribute('title') ||
    el.textContent ||
    ''
  ).trim();
  if (!raw) return 'Page break';
  return /^([0-9]+|[ivxlcdm]+)$/i.test(raw) ? `Page ${raw}` : raw;
}

/** The first line box at a range's start, or null if it has none. */
function startRect(range: Range): DOMRect | null {
  const probe = range.cloneRange();
  const node = probe.startContainer;
  if (node.nodeType === Node.TEXT_NODE) {
    const length = (node as Text).length;
    const at = Math.min(probe.startOffset, Math.max(length - 1, 0));
    probe.setStart(node, at);
    probe.setEnd(node, Math.min(at + 1, length));
  }
  const rect = probe.getClientRects()[0] ?? probe.getBoundingClientRect();
  return rect && (rect.width > 0 || rect.height > 0) ? rect : null;
}

/** Where this chapter's page breaks fall. */
export function findPageMarks(contents: Contents, locations: PageLocations | null): PageMark[] {
  const doc = contents.document;
  const scrollY = contents.window.scrollY || 0;

  const printed = Array.from(doc.body.querySelectorAll('*')).filter(isPrintPageBreak);
  if (printed.length > 0) {
    return printed.flatMap((el) => {
      const rect = el.getBoundingClientRect();
      // A marker is usually an empty inline span; its box sits on the line
      // the new page starts with.
      return rect.width === 0 && rect.height === 0 && rect.top === 0
        ? []
        : [{ top: rect.top + scrollY, label: printLabel(el) }];
    });
  }

  if (!locations) return [];
  const prefix = `epubcfi(${contents.cfiBase}!`;
  const marks: PageMark[] = [];
  const total = locations.length();
  // Location 0 starts the book; every later one starts a page, so the line
  // drawn there is where the page before it ends.
  for (let i = 1; i < total; i++) {
    const cfi = locations.cfiFromLocation(i);
    if (typeof cfi !== 'string' || !cfi.startsWith(prefix)) continue;
    let range: Range;
    try {
      range = contents.range(cfi);
    } catch {
      continue;
    }
    const rect = range ? startRect(range) : null;
    if (!rect) continue;
    marks.push({ top: rect.top + scrollY, label: `End of page ${i}` });
  }
  return marks;
}

function ensureStyle(doc: Document) {
  if (doc.getElementById(STYLE_ID)) return;
  const style = doc.createElement('style');
  style.id = STYLE_ID;
  style.textContent = `
    .${PAGE_MARKS_CLASS} { position: absolute; top: 0; left: 0; width: 0; height: 0;
      pointer-events: none; user-select: none; -webkit-user-select: none; }
    .${PAGE_MARKS_CLASS} > div { position: absolute; height: 0;
      border-top: 1px dashed currentColor; opacity: 0.35; }
    .${PAGE_MARKS_CLASS} > div::after { content: attr(data-label); position: absolute;
      right: 0; top: -0.7em; padding: 0 0.5em; font: 600 11px/1.4 system-ui, sans-serif;
      letter-spacing: 0.04em; text-transform: uppercase; background: var(--orchard-page-bg, Canvas); }
  `;
  // At the end of <head>: the CFIs of everything in <body> are unaffected.
  doc.head.appendChild(style);
}

/** (Re)draws the page-break lines on one rendered chapter. */
export function drawPageMarks(contents: Contents, locations: PageLocations | null) {
  const doc = contents.document;
  const body = doc?.body;
  if (!body) return;
  doc.querySelector(`.${PAGE_MARKS_CLASS}`)?.remove();
  const marks = findPageMarks(contents, locations);
  if (marks.length === 0) return;
  ensureStyle(doc);

  const win = contents.window;
  const bodyRect = body.getBoundingClientRect();
  const css = win.getComputedStyle(body);
  const left = bodyRect.left + win.scrollX + (parseFloat(css.paddingLeft) || 0);
  const width =
    bodyRect.width - (parseFloat(css.paddingLeft) || 0) - (parseFloat(css.paddingRight) || 0);
  // A little above the line that starts the next page: in the leading
  // between it and the line before.
  const lineHeight = parseFloat(css.lineHeight) || (parseFloat(css.fontSize) || 16) * 1.4;
  const lift = Math.max(2, lineHeight * 0.15);

  const layer = doc.createElement('div');
  layer.className = PAGE_MARKS_CLASS;
  layer.setAttribute('aria-hidden', 'true');
  layer.style.setProperty('--orchard-page-bg', css.backgroundColor || 'Canvas');
  for (const mark of marks) {
    const line = doc.createElement('div');
    line.dataset.label = mark.label;
    line.style.top = `${Math.round(mark.top - lift)}px`;
    line.style.left = `${Math.round(left)}px`;
    line.style.width = `${Math.max(0, Math.round(width))}px`;
    layer.appendChild(line);
  }
  // Last child of <body>, so no existing node's CFI changes.
  body.appendChild(layer);
}

const watched = new WeakSet<Contents>();

/** Draws a chapter's page marks now and again whenever it reflows (a font
 * size change, a resize). `locations` is read at each draw, so marks appear
 * once the location map is ready. */
export function watchPageMarks(contents: Contents, locations: () => PageLocations | null) {
  const redraw = () => drawPageMarks(contents, locations());
  redraw();
  if (watched.has(contents)) return;
  watched.add(contents);
  const Observer = (contents.window as Window & typeof globalThis).ResizeObserver;
  if (!Observer) return;
  let frame = 0;
  new Observer(() => {
    if (frame) return;
    frame = contents.window.requestAnimationFrame(() => {
      frame = 0;
      redraw();
    });
  }).observe(contents.document.body);
}

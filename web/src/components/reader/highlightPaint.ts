/**
 * Paints saved highlights onto the PDF and Markdown/text readers' DOM text.
 *
 * A saved highlight is a coarse anchor (a PDF page, a text block id) plus the
 * exact text that was selected. Painting finds that text again under the
 * anchor and marks it with the CSS Custom Highlight API — which colors text
 * ranges without touching the DOM, so neither pdf.js's text layer nor the
 * rendered Markdown has to be rewritten to show them.
 *
 * Matching ignores whitespace: a selection's toString() puts line breaks
 * between PDF text-layer spans and Markdown blocks that the DOM text doesn't
 * have in the same places, and vice versa.
 *
 * Several surfaces paint at once (each PDF page is its own), so ranges are
 * kept per caller `key` and the browser-wide registry is rebuilt from all of
 * them. Where the API isn't available (older browsers, jsdom) painting is a
 * no-op — highlights are still listed in the reader's panel.
 */

import { HIGHLIGHT_COLORS } from '../../api/types';

export interface PaintableHighlight {
  id: string;
  text: string;
  color: string;
}

interface HighlightRegistry {
  set: (name: string, value: unknown) => void;
  delete: (name: string) => void;
}

type HighlightCtor = new (...ranges: Range[]) => unknown;

function registry(): { highlights: HighlightRegistry; Highlight: HighlightCtor } | null {
  const css = (globalThis as { CSS?: { highlights?: HighlightRegistry } }).CSS;
  const Highlight = (globalThis as { Highlight?: HighlightCtor }).Highlight;
  if (!css?.highlights || !Highlight) return null;
  return { highlights: css.highlights, Highlight };
}

/** The CSS highlight name for a color (styled by `::highlight(...)` in
 * ReaderPage.css). Unknown colors fall back to the first one. */
export function highlightName(color: string): string {
  const known = (HIGHLIGHT_COLORS as readonly string[]).includes(color)
    ? color
    : HIGHLIGHT_COLORS[0];
  return `orchard-hl-${known}`;
}

const painted = new Map<string, Array<{ range: Range; color: string }>>();

function rebuild() {
  const api = registry();
  if (!api) return;
  const byName = new Map<string, Range[]>();
  for (const color of HIGHLIGHT_COLORS) byName.set(highlightName(color), []);
  for (const entries of painted.values()) {
    for (const { range, color } of entries) byName.get(highlightName(color))!.push(range);
  }
  for (const [name, ranges] of byName) {
    if (ranges.length) api.highlights.set(name, new api.Highlight(...ranges));
    else api.highlights.delete(name);
  }
}

/** Index of every non-whitespace character under `root`, in document order,
 * so a whitespace-free needle can be found and mapped back to DOM positions. */
interface TextIndex {
  condensed: string;
  /** For each node, where its first condensed character falls. */
  starts: number[];
  nodes: Text[];
}

function indexText(root: Node): TextIndex {
  const walker = root.ownerDocument!.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const nodes: Text[] = [];
  const starts: number[] = [];
  let condensed = '';
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const text = n as Text;
    nodes.push(text);
    starts.push(condensed.length);
    condensed += text.data.replace(/\s+/g, '');
  }
  return { condensed, starts, nodes };
}

/** The DOM position of condensed character `index` (or just past it, when
 * `after`). */
function position(idx: TextIndex, index: number, after: boolean): [Text, number] | null {
  // The last node starting at or before `index` is the one holding it: a
  // node with no condensed characters shares its start with the next one.
  let lo = 0;
  let hi = idx.nodes.length - 1;
  let found = -1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (idx.starts[mid]! <= index) {
      found = mid;
      lo = mid + 1;
    } else {
      hi = mid - 1;
    }
  }
  if (found < 0) return null;
  const node = idx.nodes[found]!;
  let remaining = index - idx.starts[found]!;
  for (let i = 0; i < node.data.length; i++) {
    if (/\s/.test(node.data[i]!)) continue;
    if (remaining === 0) return [node, after ? i + 1 : i];
    remaining--;
  }
  return null;
}

/** Finds `text` under `root`, ignoring whitespace and case, searching from
 * `from` onward when given (falling back to the whole of `root`). */
export function findTextRange(root: Node, text: string, from?: Node | null): Range | null {
  const needle = text.replace(/\s+/g, '').toLowerCase();
  if (!needle) return null;
  const idx = indexText(root);
  const hay = idx.condensed.toLowerCase();

  let startAt = 0;
  if (from) {
    // The first text node inside or after `from` (descendants count as
    // following it).
    const i = idx.nodes.findIndex(
      (n) => n === from || !!(from.compareDocumentPosition(n) & Node.DOCUMENT_POSITION_FOLLOWING),
    );
    if (i >= 0) startAt = idx.starts[i]!;
  }
  let at = hay.indexOf(needle, startAt);
  if (at === -1 && startAt > 0) at = hay.indexOf(needle);
  if (at === -1) return null;

  const start = position(idx, at, false);
  const end = position(idx, at + needle.length - 1, true);
  if (!start || !end) return null;
  const range = root.ownerDocument!.createRange();
  range.setStart(start[0], start[1]);
  range.setEnd(end[0], end[1]);
  return range;
}

/** Replaces the highlights painted under `key`. */
export function paintHighlights(key: string, entries: Array<{ range: Range; color: string }>) {
  if (!registry()) return;
  if (entries.length) painted.set(key, entries);
  else painted.delete(key);
  rebuild();
}

export function clearHighlights(key: string) {
  if (!painted.has(key)) return;
  painted.delete(key);
  rebuild();
}

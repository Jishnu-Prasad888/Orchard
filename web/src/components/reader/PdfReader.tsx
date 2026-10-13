/**
 * PDF reader, via pdf.js. One page at a time (not a continuous scroll of
 * every page) — PDF is a fixed layout format, so a paginated view avoids
 * virtualizing render-on-scroll for a potentially huge page count, and
 * matches how most dedicated PDF readers present a book-like document.
 *
 * A text layer (invisible, positioned spans over the canvas) sits on top of
 * every rendered page, so text is selectable and search results are real
 * matches rather than guesses.
 */

import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from 'react';
import * as pdfjsLib from 'pdfjs-dist';
import type { PDFDocumentProxy } from 'pdfjs-dist';
import { TextLayer } from 'pdfjs-dist';
import 'pdfjs-dist/web/pdf_viewer.css';

import { Icon } from '../ui/Icon';
import type { ReaderHandle, ReaderProps, SearchMatch, TocEntry } from './types';

pdfjsLib.GlobalWorkerOptions.workerSrc = new URL(
  'pdfjs-dist/build/pdf.worker.mjs',
  import.meta.url,
).toString();

/** Caps how many pages a single search walks, so opening a 2,000-page PDF
 * and searching it cannot hang the tab. */
const MAX_SEARCH_PAGES = 500;

/** Structurally matches pdf.js's internal (unexported) RefProxy type. */
interface PageRef {
  num: number;
  gen: number;
}

function parsePageLocation(location: string): number | null {
  const m = /^page:(\d+)$/.exec(location);
  return m ? Number(m[1]) : null;
}

/** Resolves an outline entry's destination to a 1-based page number. */
async function destPageNumber(doc: PDFDocumentProxy, dest: string | unknown[] | null): Promise<number | null> {
  try {
    let explicit = dest;
    if (typeof dest === 'string') {
      explicit = await doc.getDestination(dest);
    }
    if (!Array.isArray(explicit) || explicit.length === 0) return null;
    const ref = explicit[0] as PageRef;
    const index = await doc.getPageIndex(ref);
    return index + 1;
  } catch {
    return null;
  }
}

interface OutlineNode {
  title: string;
  dest: string | unknown[] | null;
  items?: OutlineNode[];
}

async function flattenOutline(
  doc: PDFDocumentProxy,
  nodes: OutlineNode[] | null,
  depth: number,
): Promise<TocEntry[]> {
  if (!nodes) return [];
  const out: TocEntry[] = [];
  for (const node of nodes) {
    const page = await destPageNumber(doc, node.dest);
    if (page !== null) out.push({ label: node.title, location: `page:${page}`, depth });
    out.push(...(await flattenOutline(doc, node.items ?? null, depth + 1)));
  }
  return out;
}

export const PdfReader = forwardRef<ReaderHandle, ReaderProps>(function PdfReader(
  { contentUrl, initialLocation, onProgress, onToc, onError, onSelectionChange },
  ref,
) {
  const [doc, setDoc] = useState<PDFDocumentProxy | null>(null);
  const [pageNum, setPageNum] = useState(() => parsePageLocation(initialLocation) ?? 1);
  const [pageInput, setPageInput] = useState(String(pageNum));
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const textLayerRef = useRef<HTMLDivElement | null>(null);
  const pageWrapRef = useRef<HTMLDivElement | null>(null);
  const renderGeneration = useRef(0);

  useEffect(() => {
    let cancelled = false;
    const task = pdfjsLib.getDocument({ url: contentUrl, withCredentials: true });
    task.promise
      .then(async (loaded) => {
        if (cancelled) return;
        setDoc(loaded);
        setPageNum((p) => Math.min(Math.max(1, p), loaded.numPages));
        const outline = await loaded.getOutline().catch(() => null);
        if (cancelled) return;
        onToc(await flattenOutline(loaded, outline, 0));
      })
      .catch((e: unknown) => {
        if (!cancelled) onError(e instanceof Error ? e.message : 'Could not open this PDF.');
      });
    return () => {
      cancelled = true;
      void task.destroy();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [contentUrl]);

  const renderPage = useCallback(async () => {
    if (!doc) return;
    const generation = ++renderGeneration.current;
    const page = await doc.getPage(pageNum);
    if (generation !== renderGeneration.current) return;

    const container = pageWrapRef.current;
    const canvas = canvasRef.current;
    const textLayerEl = textLayerRef.current;
    if (!container || !canvas || !textLayerEl) return;

    const natural = page.getViewport({ scale: 1 });
    const availableWidth = container.clientWidth - 2; // border
    const scale = availableWidth > 0 ? availableWidth / natural.width : 1;
    const viewport = page.getViewport({ scale });

    canvas.width = viewport.width;
    canvas.height = viewport.height;
    canvas.style.width = `${viewport.width}px`;
    canvas.style.height = `${viewport.height}px`;

    const renderTask = page.render({ canvas, viewport });
    await renderTask.promise;
    if (generation !== renderGeneration.current) return;

    textLayerEl.style.width = `${viewport.width}px`;
    textLayerEl.style.height = `${viewport.height}px`;
    textLayerEl.style.setProperty('--scale-factor', String(scale));
    textLayerEl.replaceChildren();
    const textContent = await page.getTextContent();
    if (generation !== renderGeneration.current) return;
    await new TextLayer({ textContentSource: textContent, container: textLayerEl, viewport }).render();
  }, [doc, pageNum]);

  useEffect(() => {
    void renderPage();
  }, [renderPage]);

  // Re-fit on container resize (the toolbar/settings panel can change width).
  useEffect(() => {
    const container = pageWrapRef.current;
    if (!container || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(() => void renderPage());
    observer.observe(container);
    return () => observer.disconnect();
  }, [renderPage]);

  useEffect(() => {
    setPageInput(String(pageNum));
    onSelectionChange(null);
    if (!doc) return;
    onProgress(`page:${pageNum}`, (pageNum / doc.numPages) * 100);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pageNum, doc, onProgress]);

  // The text layer is DOM, so native selection works; report it whenever it
  // changes so ReaderPage can offer "highlight"/"save as quote".
  useEffect(() => {
    const onSelectionEvent = () => {
      const sel = window.getSelection();
      const text = sel?.toString().trim();
      const anchor = sel?.anchorNode;
      if (!text || !anchor || !textLayerRef.current?.contains(anchor)) {
        onSelectionChange(null);
        return;
      }
      onSelectionChange({ text, location: `page:${pageNum}` });
    };
    document.addEventListener('selectionchange', onSelectionEvent);
    return () => document.removeEventListener('selectionchange', onSelectionEvent);
  }, [pageNum, onSelectionChange]);

  useImperativeHandle(ref, () => ({
    goTo: (location: string) => {
      const page = parsePageLocation(location);
      if (page !== null && doc) setPageNum(Math.min(Math.max(1, page), doc.numPages));
    },
    search: async (query: string): Promise<SearchMatch[]> => {
      const needle = query.trim().toLowerCase();
      if (!needle || !doc) return [];
      const matches: SearchMatch[] = [];
      const pageCount = Math.min(doc.numPages, MAX_SEARCH_PAGES);
      for (let p = 1; p <= pageCount; p++) {
        const page = await doc.getPage(p);
        const content = await page.getTextContent();
        const text = content.items.map((it) => ('str' in it ? it.str : '')).join(' ');
        const at = text.toLowerCase().indexOf(needle);
        if (at === -1) continue;
        const start = Math.max(0, at - 30);
        const excerpt = (start > 0 ? '…' : '') + text.slice(start, at + needle.length + 30) + '…';
        matches.push({ location: `page:${p}`, excerpt });
        if (matches.length >= 50) break;
      }
      return matches;
    },
  }));

  if (!doc) {
    return <p className="reader-loading muted">Opening PDF…</p>;
  }

  return (
    <div className="reader-pdf">
      <div className="reader-pdf-pagebar">
        <button
          type="button"
          className="button ghost-button"
          onClick={() => setPageNum((p) => Math.max(1, p - 1))}
          disabled={pageNum <= 1}
          aria-label="Previous page"
        >
          <Icon name="chevron-left" />
        </button>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const n = Number(pageInput);
            if (Number.isFinite(n)) setPageNum(Math.min(Math.max(1, Math.trunc(n)), doc.numPages));
          }}
        >
          <input
            className="reader-pdf-page-input"
            value={pageInput}
            onChange={(e) => setPageInput(e.target.value)}
            aria-label="Page number"
            inputMode="numeric"
          />
          <span className="muted"> / {doc.numPages}</span>
        </form>
        <button
          type="button"
          className="button ghost-button"
          onClick={() => setPageNum((p) => Math.min(doc.numPages, p + 1))}
          disabled={pageNum >= doc.numPages}
          aria-label="Next page"
        >
          <Icon name="chevron-right" />
        </button>
      </div>
      <div className="reader-pdf-scroll">
        <div className="reader-pdf-page" ref={pageWrapRef}>
          <canvas ref={canvasRef} />
          <div className="textLayer" ref={textLayerRef} />
        </div>
      </div>
    </div>
  );
});

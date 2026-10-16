/**
 * PDF reader, via pdf.js.
 *
 * Two reading preferences shape the layout, and combine freely:
 *
 *   reading direction  horizontal — one view at a time, turned side to side
 *                      vertical   — every page in one continuous scroll
 *   pages per view     1 — a single page
 *                      2 — a facing-pages spread, the cover on its own as
 *                          in a printed book
 *
 * A "view" is the unit either direction advances by: one page, or one
 * spread. Scrolling a long document stays cheap because only the views near
 * the viewport hold a canvas; the rest are empty boxes of the right size.
 *
 * A text layer (invisible, positioned spans over the canvas) sits on top of
 * every rendered page, so text is selectable, search results are real
 * matches rather than guesses, and saved highlights can be painted onto it.
 *
 * Pages are sized to fit the window ("fit page": a whole page or spread in
 * view when turning pages) or, on request, to its width ("fit width"); zoom
 * scales whichever fit is chosen.
 */

import {
  forwardRef,
  memo,
  useCallback,
  useEffect,
  useImperativeHandle,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { MoveHorizontal, ZoomIn, ZoomOut } from 'lucide-react';
import * as pdfjsLib from 'pdfjs-dist';
import type { PDFDocumentProxy, RenderTask } from 'pdfjs-dist';
import { RenderingCancelledException, TextLayer } from 'pdfjs-dist';

import { Icon } from '../ui/Icon';
import { clearHighlights, findTextRange, paintHighlights } from './highlightPaint';
import { buildViews, viewIndexOf } from './pdfViews';
import type { ReaderHandle, ReaderHighlight, ReaderProps, SearchMatch, TocEntry } from './types';

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

/** Space around and between pages, in CSS px. Mirrored in ReaderPage.css. */
const PAGE_GAP = 16;
/** A US Letter page, used to size a page's box before its own size is known. */
const FALLBACK_ASPECT = 11 / 8.5;
/** In a vertical scroll a page is as wide as the window allows, up to this:
 * past it, a single page on a wide monitor is taller than the screen and
 * each line is too long to read comfortably. */
const MAX_FIT_WIDTH = 1000;
/** Below this width a two-page spread makes both pages too small to read,
 * so a narrow window shows one page whatever the preference says. */
const MIN_SPREAD_WIDTH = 700;
const ZOOM_STEPS = [0.5, 0.67, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3];
/** Canvas backing-store density is capped: beyond 2x the extra pixels cost
 * memory and render time without looking any sharper. */
const MAX_PIXEL_RATIO = 2;

function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  return !!el && (/^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName) || el.isContentEditable);
}

function parsePageLocation(location: string): number | null {
  const m = /^page:(\d+)$/.exec(location);
  return m ? Number(m[1]) : null;
}

/** Resolves an outline entry's destination to a 1-based page number. */
async function destPageNumber(
  doc: PDFDocumentProxy,
  dest: string | unknown[] | null,
): Promise<number | null> {
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

interface PdfPageProps {
  doc: PDFDocumentProxy;
  pageNumber: number;
  /** CSS width to draw the page at. */
  width: number;
  /** Height / width, as far as it is known yet. */
  aspect: number;
  /** Whether to hold a rendered canvas, or just the page's footprint. */
  active: boolean;
  /** Saved highlights anchored to this page. */
  highlights: ReaderHighlight[];
  onAspect: (pageNumber: number, aspect: number) => void;
}

const NO_HIGHLIGHTS: ReaderHighlight[] = [];

/** One page: a box of the page's size, holding its canvas and text layer
 * while `active`. */
const PdfPage = memo(function PdfPage({
  doc,
  pageNumber,
  width,
  aspect,
  active,
  highlights,
  onAspect,
}: PdfPageProps) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const textLayerRef = useRef<HTMLDivElement | null>(null);
  // The render in flight on this page's canvas, if any. pdf.js throws
  // rather than queueing when render() is called on a canvas that is still
  // rendering, so a re-render (a resize, a zoom) must cancel its
  // predecessor and wait for that cancellation to land first.
  const renderTaskRef = useRef<RenderTask | null>(null);
  // Bumped each time the text layer is rebuilt, so highlights repaint onto
  // its new text nodes.
  const [textVersion, setTextVersion] = useState(0);

  useEffect(() => {
    if (!active || width <= 0) return;
    let cancelled = false;

    const draw = async () => {
      const page = await doc.getPage(pageNumber);
      if (cancelled) return;
      const natural = page.getViewport({ scale: 1 });
      onAspect(pageNumber, natural.height / natural.width);

      const previous = renderTaskRef.current;
      if (previous) {
        previous.cancel();
        await previous.promise.catch(() => {});
        if (cancelled) return;
      }
      const canvas = canvasRef.current;
      const textLayerEl = textLayerRef.current;
      if (!canvas || !textLayerEl) return;

      const scale = width / natural.width;
      const viewport = page.getViewport({ scale });
      const ratio = Math.min(window.devicePixelRatio || 1, MAX_PIXEL_RATIO);
      canvas.width = Math.floor(viewport.width * ratio);
      canvas.height = Math.floor(viewport.height * ratio);

      const task = page.render({
        canvas,
        viewport,
        ...(ratio !== 1 ? { transform: [ratio, 0, 0, ratio, 0, 0] } : {}),
      });
      renderTaskRef.current = task;
      try {
        await task.promise;
      } finally {
        if (renderTaskRef.current === task) renderTaskRef.current = null;
      }
      if (cancelled) return;

      textLayerEl.style.setProperty('--scale-factor', String(scale));
      textLayerEl.replaceChildren();
      const textContent = await page.getTextContent();
      if (cancelled) return;
      await new TextLayer({
        textContentSource: textContent,
        container: textLayerEl,
        viewport,
      }).render();
      if (!cancelled) setTextVersion((v) => v + 1);
    };

    draw().catch((err: unknown) => {
      // Superseded by a newer draw, which owns the canvas now.
      if (!(err instanceof RenderingCancelledException) && !cancelled) console.error(err);
    });
    return () => {
      cancelled = true;
      renderTaskRef.current?.cancel();
    };
  }, [doc, pageNumber, width, active, onAspect]);

  useEffect(() => {
    const key = `pdf:${pageNumber}`;
    const textLayerEl = textLayerRef.current;
    if (!active || !textLayerEl || textVersion === 0) return;
    const entries = [];
    for (const h of highlights) {
      const range = findTextRange(textLayerEl, h.text);
      if (range) entries.push({ range, color: h.color });
    }
    paintHighlights(key, entries);
    return () => clearHighlights(key);
  }, [active, highlights, pageNumber, textVersion]);

  return (
    <div
      className="reader-pdf-page"
      data-page={pageNumber}
      style={{ width, height: Math.round(width * aspect) }}
    >
      {active && (
        <>
          <canvas ref={canvasRef} />
          <div className="textLayer" ref={textLayerRef} />
        </>
      )}
    </div>
  );
});

export const PdfReader = forwardRef<ReaderHandle, ReaderProps>(function PdfReader(
  {
    contentUrl,
    initialLocation,
    preferences,
    onProgress,
    onToc,
    onError,
    onSelectionChange,
    highlights,
  },
  ref,
) {
  const [doc, setDoc] = useState<PDFDocumentProxy | null>(null);
  const [pageNum, setPageNum] = useState(() => parsePageLocation(initialLocation) ?? 1);
  const [pageInput, setPageInput] = useState(String(pageNum));
  const [zoom, setZoom] = useState(1);
  const [fit, setFit] = useState<'page' | 'width'>('page');
  // The scroll container's inner size. It has a stable size from the flex
  // layout around it, unlike the pages inside it, which are sized from it —
  // measuring those instead would feed their own size back into itself.
  const [box, setBox] = useState({ width: 0, height: 0 });
  const [aspects, setAspects] = useState<Record<number, number>>({});
  /** Views near the viewport, in a vertical scroll. */
  const [nearby, setNearby] = useState<ReadonlySet<number>>(() => new Set());

  const scrollRef = useRef<HTMLDivElement | null>(null);
  const viewEls = useRef<Array<HTMLDivElement | null>>([]);
  // Layout effects below read the current page without re-running for it.
  const pageNumRef = useRef(pageNum);
  useLayoutEffect(() => {
    pageNumRef.current = pageNum;
  }, [pageNum]);

  const vertical = preferences.reading_direction === 'vertical';
  const perView: 1 | 2 = preferences.pages_per_view === 2 && box.width >= MIN_SPREAD_WIDTH ? 2 : 1;
  const numPages = doc?.numPages ?? 0;
  const views = useMemo(() => buildViews(numPages, perView), [numPages, perView]);
  const viewIndex = Math.min(viewIndexOf(pageNum, perView), Math.max(views.length - 1, 0));

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

  const hasDoc = doc !== null;
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const measure = () => {
      const next = { width: el.clientWidth, height: el.clientHeight };
      setBox((prev) => (prev.width === next.width && prev.height === next.height ? prev : next));
    };
    measure();
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [hasDoc]);

  const onAspect = useCallback((page: number, aspect: number) => {
    setAspects((prev) => (prev[page] === aspect ? prev : { ...prev, [page]: aspect }));
  }, []);

  // Until a page has been loaded its size is a guess: the first page's,
  // which is right for nearly every page of nearly every book.
  const defaultAspect = aspects[1] ?? FALLBACK_ASPECT;
  const aspectOf = (page: number) => aspects[page] ?? defaultAspect;

  // How wide one page is drawn. Turning pages, the whole view fits the
  // window like an open book; scrolling, pages fit its width.
  const usableWidth = Math.max(box.width - PAGE_GAP * 2 - PAGE_GAP * (perView - 1), 0);
  const fitWidth = usableWidth / perView;
  let basePageWidth = fit === 'width' ? fitWidth : Math.min(fitWidth, MAX_FIT_WIDTH);
  if (!vertical && fit === 'page') {
    const shown = views[viewIndex] ?? [];
    const tallest = Math.max(defaultAspect, ...shown.map(aspectOf));
    const usableHeight = Math.max(box.height - PAGE_GAP * 2 - 2, 0); // 2px of slack against rounding
    basePageWidth = Math.min(fitWidth, usableHeight / tallest);
  }
  const pageWidth = Math.max(Math.floor(basePageWidth * zoom), 0);

  // Highlights grouped by page, each page's list stable across renders so
  // pages whose highlights didn't change don't repaint.
  const highlightsByPage = useMemo(() => {
    const map = new Map<number, ReaderHighlight[]>();
    for (const h of highlights) {
      const page = parsePageLocation(h.location);
      if (page === null) continue;
      const list = map.get(page);
      if (list) list.push(h);
      else map.set(page, [h]);
    }
    return map;
  }, [highlights]);

  const scrollToPage = useCallback(
    (page: number) => {
      const scrollEl = scrollRef.current;
      const el = viewEls.current[viewIndexOf(page, perView)];
      if (scrollEl && el) scrollEl.scrollTop = el.offsetTop - PAGE_GAP;
    },
    [perView],
  );

  const goToPage = useCallback(
    (page: number) => {
      if (!doc) return;
      const target = Math.min(Math.max(1, Math.trunc(page)), doc.numPages);
      setPageNum(target);
      if (vertical) scrollToPage(target);
    },
    [doc, vertical, scrollToPage],
  );

  const turn = useCallback(
    (delta: number) => {
      const next = views[Math.min(Math.max(viewIndex + delta, 0), views.length - 1)];
      if (next) goToPage(next[0]!);
    },
    [views, viewIndex, goToPage],
  );

  // Keep the reader's place when the layout under it changes: on opening at
  // a saved page, and whenever the direction, pages per view, zoom, or
  // window size moves every page somewhere else.
  useLayoutEffect(() => {
    const scrollEl = scrollRef.current;
    if (!hasDoc || !scrollEl || pageWidth <= 0) return;
    if (vertical) scrollToPage(pageNumRef.current);
    else scrollEl.scrollTop = 0;
  }, [hasDoc, vertical, perView, pageWidth, scrollToPage]);

  // Turning to another view starts it from its top.
  useLayoutEffect(() => {
    if (!vertical && scrollRef.current) scrollRef.current.scrollTop = 0;
  }, [vertical, viewIndex]);

  // In a vertical scroll, the page being read is whichever view crosses the
  // top of the window (the last one, once scrolled to the very end).
  useEffect(() => {
    const scrollEl = scrollRef.current;
    if (!hasDoc || !vertical || !scrollEl) return;
    let frame = 0;
    const sync = () => {
      frame = 0;
      const els = viewEls.current;
      const count = views.length;
      if (count === 0) return;
      let found = 0;
      if (scrollEl.scrollTop + scrollEl.clientHeight >= scrollEl.scrollHeight - 2) {
        found = count - 1;
      } else {
        const line = scrollEl.scrollTop + PAGE_GAP + 1;
        let lo = 0;
        let hi = count - 1;
        while (lo <= hi) {
          const mid = (lo + hi) >> 1;
          const el = els[mid];
          if (el && el.offsetTop <= line) {
            found = mid;
            lo = mid + 1;
          } else {
            hi = mid - 1;
          }
        }
      }
      const page = views[found]?.[0];
      if (page !== undefined && viewIndexOf(pageNumRef.current, perView) !== found)
        setPageNum(page);
    };
    const onScroll = () => {
      if (!frame) frame = requestAnimationFrame(sync);
    };
    scrollEl.addEventListener('scroll', onScroll, { passive: true });
    return () => {
      scrollEl.removeEventListener('scroll', onScroll);
      if (frame) cancelAnimationFrame(frame);
    };
  }, [hasDoc, vertical, views, perView]);

  // In a vertical scroll, only views within a screen or so of the viewport
  // keep a canvas.
  useEffect(() => {
    const scrollEl = scrollRef.current;
    if (!hasDoc || !vertical || !scrollEl || typeof IntersectionObserver === 'undefined') return;
    const visible = new Set<number>();
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          const index = Number((entry.target as HTMLElement).dataset.view);
          if (entry.isIntersecting) visible.add(index);
          else visible.delete(index);
        }
        setNearby(new Set(visible));
      },
      { root: scrollEl, rootMargin: '100% 0px' },
    );
    for (const el of viewEls.current.slice(0, views.length)) {
      if (el) observer.observe(el);
    }
    return () => observer.disconnect();
  }, [hasDoc, vertical, views]);

  useEffect(() => {
    setPageInput(String(pageNum));
    if (!doc) return;
    // A spread counts as read through its last page, so the final view of
    // the book reports 100%.
    const shown = views[viewIndexOf(pageNum, perView)];
    const lastShown = shown?.[shown.length - 1] ?? pageNum;
    onProgress(`page:${pageNum}`, (lastShown / doc.numPages) * 100);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pageNum, doc, perView, onProgress]);

  // Turning the page leaves the selection behind. (Scrolling does not: the
  // text is still on screen.)
  useEffect(() => {
    if (!vertical) onSelectionChange(null);
  }, [vertical, viewIndex, onSelectionChange]);

  // The text layer is DOM, so native selection works; report it whenever it
  // changes so ReaderPage can offer "highlight"/"save as quote".
  useEffect(() => {
    const onSelectionEvent = () => {
      const sel = window.getSelection();
      const text = sel?.toString().trim();
      const anchor = sel?.anchorNode;
      const anchorEl = anchor instanceof Element ? anchor : anchor?.parentElement;
      const pageEl = anchorEl?.closest<HTMLElement>('.reader-pdf-page');
      if (!text || !pageEl || !scrollRef.current?.contains(pageEl)) {
        onSelectionChange(null);
        return;
      }
      onSelectionChange({ text, location: `page:${pageEl.dataset.page}` });
    };
    document.addEventListener('selectionchange', onSelectionEvent);
    return () => document.removeEventListener('selectionchange', onSelectionEvent);
  }, [onSelectionChange]);

  // Arrow keys turn pages; in a vertical scroll the up/down keys already
  // scroll, so only the sideways and paging keys are claimed there.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey) return;
      if (isTyping(event.target) || document.querySelector('[aria-modal="true"]')) return;
      const forward = event.key === 'ArrowRight' || (!vertical && event.key === 'PageDown');
      const back = event.key === 'ArrowLeft' || (!vertical && event.key === 'PageUp');
      if (!forward && !back) return;
      event.preventDefault();
      turn(forward ? 1 : -1);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [vertical, turn]);

  useImperativeHandle(ref, () => ({
    goTo: (location: string) => {
      const page = parsePageLocation(location);
      if (page !== null) goToPage(page);
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

  const stepZoom = (direction: 1 | -1) => {
    setZoom((current) => {
      const steps = direction === 1 ? ZOOM_STEPS : [...ZOOM_STEPS].reverse();
      return (
        steps.find((z) => (direction === 1 ? z > current + 0.001 : z < current - 0.001)) ?? current
      );
    });
  };

  const renderView = (pages: number[], index: number) => (
    <div
      key={pages[0]}
      className="reader-pdf-view"
      data-view={index}
      ref={(el) => {
        viewEls.current[index] = el;
      }}
    >
      {pages.map((page) => (
        <PdfPage
          key={page}
          doc={doc}
          pageNumber={page}
          width={pageWidth}
          aspect={aspectOf(page)}
          active={!vertical || nearby.has(index) || index === viewIndex}
          highlights={highlightsByPage.get(page) ?? NO_HIGHLIGHTS}
          onAspect={onAspect}
        />
      ))}
    </div>
  );

  const shownPages = views[viewIndex] ?? [pageNum];
  const pageLabel =
    shownPages.length > 1
      ? `${shownPages[0]}–${shownPages[shownPages.length - 1]}`
      : String(pageNum);

  return (
    <div className="reader-pdf" data-direction={vertical ? 'vertical' : 'horizontal'}>
      <div className="reader-pdf-pagebar">
        <button
          type="button"
          className="button ghost-button"
          onClick={() => turn(-1)}
          disabled={viewIndex <= 0}
          aria-label="Previous page"
        >
          <Icon name="chevron-left" />
        </button>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const n = Number(pageInput);
            if (Number.isFinite(n)) goToPage(n);
            else setPageInput(String(pageNum));
          }}
        >
          <input
            className="reader-pdf-page-input"
            value={pageInput}
            onChange={(e) => setPageInput(e.target.value)}
            onFocus={(e) => e.target.select()}
            onBlur={() => setPageInput(String(pageNum))}
            aria-label="Page number"
            inputMode="numeric"
            title={shownPages.length > 1 ? `Showing pages ${pageLabel}` : undefined}
          />
          <span className="muted"> / {doc.numPages}</span>
        </form>
        <button
          type="button"
          className="button ghost-button"
          onClick={() => turn(1)}
          disabled={viewIndex >= views.length - 1}
          aria-label="Next page"
        >
          <Icon name="chevron-right" />
        </button>

        <span className="reader-pdf-pagebar-divider" aria-hidden="true" />

        <button
          type="button"
          className="button ghost-button"
          onClick={() => stepZoom(-1)}
          disabled={zoom <= ZOOM_STEPS[0]!}
          aria-label="Zoom out"
        >
          <ZoomOut size={18} />
        </button>
        <button
          type="button"
          className="button ghost-button reader-pdf-zoom-level"
          onClick={() => setZoom(1)}
          title="Reset zoom"
          aria-label={`Zoom ${Math.round(zoom * 100)}%. Reset zoom`}
        >
          {Math.round(zoom * 100)}%
        </button>
        <button
          type="button"
          className="button ghost-button"
          onClick={() => stepZoom(1)}
          disabled={zoom >= ZOOM_STEPS[ZOOM_STEPS.length - 1]!}
          aria-label="Zoom in"
        >
          <ZoomIn size={18} />
        </button>
        <button
          type="button"
          className={fit === 'width' ? 'button ghost-button active' : 'button ghost-button'}
          onClick={() => {
            setFit((f) => (f === 'width' ? 'page' : 'width'));
            setZoom(1);
          }}
          aria-pressed={fit === 'width'}
          aria-label="Fit to width"
          title={fit === 'width' ? 'Fit page' : 'Fit to width'}
        >
          <MoveHorizontal size={18} />
        </button>
      </div>
      <div className="reader-pdf-scroll" ref={scrollRef}>
        <div className="reader-pdf-views">
          {vertical
            ? views.map(renderView)
            : views[viewIndex] && renderView(views[viewIndex], viewIndex)}
        </div>
      </div>
    </div>
  );
});

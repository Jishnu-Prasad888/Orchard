/**
 * EPUB reader, via epub.js. Paginated or continuously scrolled, one page
 * or a two-page spread, per the reading preferences — with the
 * reading-width preference applied as the content column's max width
 * inside each chapter's iframe, so "reading width" means the same thing
 * here as it does in the Markdown/text reader.
 *
 * epub.js's shipped type definitions are known to lag its runtime API in a
 * few places (Section.find's real return shape, Spine.spineItems); this
 * file casts through small local interfaces at exactly those points rather
 * than trusting the stale declaration, with a comment at each.
 */

import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from 'react';
import ePub from 'epubjs';
import type { Book, Contents, NavItem, Rendition } from 'epubjs';

import { Icon } from '../ui/Icon';
import { drawPageMarks, watchPageMarks, type PageLocations } from './epubPageMarks';
import type { ReaderHandle, ReaderHighlight, ReaderProps, SearchMatch, TocEntry } from './types';
import { zoomDirection } from './zoom';

/** Fill colors for saved highlights, painted by epub.js as an SVG overlay
 * (the PDF/text readers use `::highlight()` rules for the same palette). */
const HIGHLIGHT_FILLS: Record<string, string> = {
  yellow: '#ffd43b',
  green: '#69db7c',
  blue: '#74c0fc',
  pink: '#f783ac',
  orange: '#ffa94d',
};

/** The real runtime shape of Section.find()/search() results — the shipped
 * .d.ts says `Array<Element>`, but epub.js actually returns {cfi, excerpt}. */
interface EpubFindMatch {
  cfi: string;
  excerpt: string;
}

/** Section.load/find/unload and the request-binding `load` exist on epub.js
 * sections at runtime but are only partially declared. */
interface EpubSection {
  load: (request: (url: string) => Promise<Document>) => Promise<Document>;
  find: (query: string) => EpubFindMatch[];
  unload: () => void;
}

/** Spine.spineItems is the real backing array; the shipped .d.ts omits it. */
interface EpubSpineWithItems {
  spineItems: EpubSection[];
}

function flattenToc(items: NavItem[], depth: number): TocEntry[] {
  const out: TocEntry[] = [];
  for (const item of items) {
    out.push({ label: item.label.trim(), location: item.href, depth });
    if (item.subitems?.length) out.push(...flattenToc(item.subitems, depth + 1));
  }
  return out;
}

/** "Page 12 of 340" from the location map once it is built; until then,
 * the page within the current chapter. */
function pageLabelFor(
  book: Book,
  cfi: string,
  exact: boolean,
  displayed?: { page: number; total: number },
): string | null {
  if (exact) {
    const index = Number(book.locations.locationFromCfi(cfi));
    const total = book.locations.length();
    if (Number.isFinite(index) && index >= 0 && total > 0) {
      return `Page ${index + 1} of ${total}`;
    }
  }
  if (displayed && displayed.total > 0) {
    return `Page ${displayed.page} of ${displayed.total} in this chapter`;
  }
  return null;
}

/** A reading theme that wins over the book's own text colours. */
function themeRules(text: string, background: string, link: string) {
  return {
    body: { color: `${text} !important`, background: `${background} !important` },
    'body *': {
      color: `${text} !important`,
      'background-color': 'transparent !important',
      'border-color': `${text} !important`,
    },
    'a, a *': { color: `${link} !important` },
  };
}

function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  return !!el && (/^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName) || el.isContentEditable);
}

export const EpubReader = forwardRef<ReaderHandle, ReaderProps>(function EpubReader(
  {
    contentUrl,
    initialLocation,
    preferences,
    onProgress,
    onToc,
    onError,
    onSelectionChange,
    highlights,
    onSelectionContextMenu,
    onZoomKey,
  },
  ref,
) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [book, setBook] = useState<Book | null>(null);
  const bookRef = useRef<Book | null>(null);
  const renditionRef = useRef<Rendition | null>(null);
  const locationsReady = useRef(false);
  // Where the reader is, so a change of layout reopens the book at the
  // same place instead of at the saved resume point.
  const lastLocation = useRef(initialLocation);
  const [ready, setReady] = useState(false);
  // Bumped once the location map is built: page numbers become exact.
  const [locationsVersion, setLocationsVersion] = useState(0);
  // "Page 12 of 340" under a book read page by page.
  const [pageLabel, setPageLabel] = useState<string | null>(null);
  // The highlights currently drawn on the rendition, by CFI range.
  const drawn = useRef(new Map<string, ReaderHighlight>());
  // Read from inside epub.js callbacks registered once per rendition.
  const contextMenuRef = useRef(onSelectionContextMenu);
  useEffect(() => {
    contextMenuRef.current = onSelectionContextMenu;
  }, [onSelectionContextMenu]);
  const onZoomKeyRef = useRef(onZoomKey);
  useEffect(() => {
    onZoomKeyRef.current = onZoomKey;
  }, [onZoomKey]);

  // The two layout preferences decide how epub.js lays the book out, which
  // it only takes at creation — so they rebuild the rendition (not the
  // book: its parsed contents and location map are kept).
  //
  //   horizontal  paginated, turned side to side; two pages per view shows
  //               a two-column spread when the window is wide enough
  //   vertical    one continuous scroll through the chapters
  const vertical = preferences.reading_direction === 'vertical';
  const spread = !vertical && preferences.pages_per_view === 2;

  useEffect(() => {
    const container = containerRef.current;
    if (!container || !book) return;

    const drawnMap = drawn.current;
    const rendition = book.renderTo(container, {
      width: '100%',
      height: '100%',
      ...(vertical
        ? { manager: 'continuous', flow: 'scrolled' }
        : { flow: 'paginated', spread: spread ? 'auto' : 'none' }),
    });
    renditionRef.current = rendition;

    // The light theme keeps a book's own colours. Dark and sepia replace
    // them: most books colour their paragraphs black in their own CSS, which
    // beats a colour set on <body> and left dark mode as black text on a
    // dark page. Links stay distinguishable.
    rendition.themes.register('light', { body: { color: '#1a1a1a', background: '#ffffff' } });
    rendition.themes.register('dark', themeRules('#e4e4e4', '#1a1a1a', '#9cc0ff'));
    rendition.themes.register('sepia', themeRules('#3a2e1f', '#f4ecd8', '#6b4a1f'));

    rendition.on(
      'relocated',
      (location: {
        start: { cfi: string; index: number; displayed?: { page: number; total: number } };
      }) => {
        const cfi = location.start.cfi;
        lastLocation.current = cfi;
        setPageLabel(pageLabelFor(book, cfi, locationsReady.current, location.start.displayed));
        // Exact once the location map is built; until then, how far through
        // the chapters the reader is — close enough for a progress bar, and
        // far better than reporting a book in progress as 0% read.
        const chapters = (book.spine as unknown as EpubSpineWithItems).spineItems.length;
        const percent = locationsReady.current
          ? book.locations.percentageFromCfi(cfi) * 100
          : chapters > 0
            ? (location.start.index / chapters) * 100
            : 0;
        onProgress(cfi, percent);
        onSelectionChange(null);
      },
    );

    // In a continuous scroll there is no page turn to show where a page
    // ends, so each chapter gets a line at every page break.
    if (vertical) {
      rendition.hooks.content.register((contents: Contents) => {
        watchPageMarks(contents, () =>
          locationsReady.current ? (book.locations as unknown as PageLocations) : null,
        );
      });
    }

    // epub.js renders each chapter in its own iframe, so the main
    // document's selectionchange never fires for it — epub.js emits its
    // own 'selected' event with the cfiRange and the iframe's Contents
    // instead, which is where the native Selection actually lives.
    rendition.on('selected', (cfiRange: string, contents: Contents) => {
      const text = contents.window.getSelection?.()?.toString().trim();
      onSelectionChange(text ? { text, location: cfiRange } : null);
    });

    // A right-click over selected text inside the chapter's iframe opens the
    // page's own menu (quote / highlight) instead of the browser's. The
    // iframe's coordinates are offset into the page's.
    rendition.hooks.content.register((contents: Contents) => {
      contents.document.addEventListener('contextmenu', (event: MouseEvent) => {
        const text = contents.window.getSelection?.()?.toString().trim();
        const relay = contextMenuRef.current;
        if (!text || !relay) return;
        event.preventDefault();
        const frame = contents.window.frameElement?.getBoundingClientRect();
        relay({ x: event.clientX + (frame?.left ?? 0), y: event.clientY + (frame?.top ?? 0) });
      });
    });

    // Arrow keys turn pages. Keys pressed while the book itself has focus
    // land in its iframe, which epub.js relays; keys pressed elsewhere on
    // the page arrive on the window.
    const onKey = (event: KeyboardEvent) => {
      if (vertical || event.metaKey || event.ctrlKey || event.altKey) return;
      if (isTyping(event.target) || document.querySelector('[aria-modal="true"]')) return;
      if (event.key === 'ArrowRight' || event.key === 'PageDown') void rendition.next();
      else if (event.key === 'ArrowLeft' || event.key === 'PageUp') void rendition.prev();
    };
    rendition.on('keydown', onKey);
    window.addEventListener('keydown', onKey);
    // Ctrl/⌘ +, − and 0 pressed in the book's frame never reach the page,
    // which zooms the text; hand them up (and keep the browser's own zoom,
    // which would scale the whole reader instead, out of it).
    rendition.on('keydown', (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey) || event.altKey) return;
      const direction = zoomDirection(event.key);
      if (direction === null) return;
      event.preventDefault();
      onZoomKeyRef.current?.(direction);
    });

    let cancelled = false;
    book.ready
      .then(() => rendition.display(lastLocation.current || undefined))
      .then(() => {
        if (!cancelled) setReady(true);
      })
      .catch((e: unknown) => {
        if (!cancelled) onError(e instanceof Error ? e.message : 'Could not open this EPUB.');
      });

    return () => {
      cancelled = true;
      window.removeEventListener('keydown', onKey);
      renditionRef.current = null;
      // A new rendition starts with no annotations of its own.
      drawnMap.clear();
      setReady(false);
      try {
        rendition.destroy();
      } catch {
        // The book it belonged to is already gone.
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [book, vertical, spread]);

  useEffect(() => {
    // requestCredentials is declared as `object` in epub.js's shipped types,
    // but the runtime only checks truthiness before setting
    // `xhr.withCredentials = true` — a boolean is the documented usage.
    //
    // openAs is not optional here: epub.js decides what a URL is from its
    // extension, and the download endpoint has none, so left to guess it
    // takes the book for an unpacked folder and asks the server for
    // "<url>/META-INF/container.xml".
    const opened = ePub(contentUrl, {
      openAs: 'epub',
      requestCredentials: true as unknown as object,
    });
    bookRef.current = opened;
    locationsReady.current = false;
    setBook(opened);

    let cancelled = false;
    opened.loaded.navigation
      .then((nav) => {
        if (!cancelled) onToc(flattenToc(nav.toc, 0));
      })
      .catch(() => undefined);

    // Percentage needs a location map; this can take a while on a large
    // book, so it runs in the background rather than blocking the first page.
    opened.ready
      .then(() => opened.locations.generate(1000))
      .then(() => {
        if (cancelled) return;
        locationsReady.current = true;
        setLocationsVersion((v) => v + 1);
      })
      .catch(() => undefined);

    return () => {
      cancelled = true;
      bookRef.current = null;
      setBook(null);
      opened.destroy();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [contentUrl]);

  // Apply reading preferences whenever they change, without re-opening the book.
  useEffect(() => {
    const rendition = renditionRef.current;
    if (!rendition) return;
    rendition.themes.select(preferences.theme);
    rendition.themes.fontSize(`${preferences.font_size}px`);
    rendition.themes.override('line-height', String(preferences.line_spacing), true);
    // A spread splits the window into two columns itself; holding the text
    // to the reading width as well would squeeze both into half of it.
    rendition.themes.override(
      'max-width',
      spread ? 'none' : `${preferences.reading_width}px`,
      true,
    );
    rendition.themes.override('margin', '0 auto', true);
    rendition.themes.override('padding', `${preferences.margin}px`, true);
  }, [preferences, ready, spread]);

  // Once the location map exists, redraw page marks on the chapters already
  // on screen and make the page counter exact.
  useEffect(() => {
    const rendition = renditionRef.current;
    const current = bookRef.current;
    if (!rendition || !current || !ready || locationsVersion === 0) return;
    const locations = current.locations as unknown as PageLocations;
    if (vertical) {
      for (const contents of rendition.getContents() as unknown as Contents[]) {
        drawPageMarks(contents, locations);
      }
    }
    if (lastLocation.current) setPageLabel(pageLabelFor(current, lastLocation.current, true));
  }, [locationsVersion, ready, vertical]);

  // Keep the rendition's highlight annotations in step with the saved ones.
  // epub.js redraws them itself as chapters come and go.
  useEffect(() => {
    const rendition = renditionRef.current;
    if (!rendition || !ready) return;
    const wanted = new Map(highlights.map((h) => [h.location, h]));
    for (const [cfi, h] of drawn.current) {
      const next = wanted.get(cfi);
      if (!next || next.color !== h.color) {
        rendition.annotations.remove(cfi, 'highlight');
        drawn.current.delete(cfi);
      }
    }
    for (const [cfi, h] of wanted) {
      if (drawn.current.has(cfi) || !cfi.startsWith('epubcfi(')) continue;
      try {
        rendition.annotations.highlight(cfi, { id: h.id }, undefined, 'orchard-highlight', {
          fill: HIGHLIGHT_FILLS[h.color] ?? HIGHLIGHT_FILLS.yellow,
          'fill-opacity': '0.35',
        });
        drawn.current.set(cfi, h);
      } catch {
        // A CFI from an older edition of the file that no longer resolves.
      }
    }
  }, [highlights, ready]);

  useImperativeHandle(ref, () => ({
    goTo: (location: string) => {
      void renditionRef.current?.display(location);
    },
    search: async (query: string): Promise<SearchMatch[]> => {
      const book = bookRef.current;
      const needle = query.trim();
      if (!book || !needle) return [];
      const spine = book.spine as unknown as EpubSpineWithItems;
      const matches: SearchMatch[] = [];
      for (const section of spine.spineItems) {
        await section.load(book.load.bind(book) as (url: string) => Promise<Document>);
        const found = section.find(needle);
        section.unload();
        for (const m of found) matches.push({ location: m.cfi, excerpt: m.excerpt });
        if (matches.length >= 50) break;
      }
      return matches;
    },
  }));

  return (
    <div className="reader-epub" data-direction={vertical ? 'vertical' : 'horizontal'}>
      {!ready && <p className="reader-loading muted">Opening EPUB…</p>}
      <div className="reader-epub-viewport" ref={containerRef} />
      {!vertical && pageLabel && (
        <p className="reader-epub-pagenum" aria-live="polite" data-testid="epub-page-number">
          {pageLabel}
        </p>
      )}
      {!vertical && (
        <>
          <button
            type="button"
            className="reader-epub-turn reader-epub-turn-prev"
            onClick={() => void renditionRef.current?.prev()}
            aria-label="Previous page"
          >
            <Icon name="chevron-left" />
          </button>
          <button
            type="button"
            className="reader-epub-turn reader-epub-turn-next"
            onClick={() => void renditionRef.current?.next()}
            aria-label="Next page"
          >
            <Icon name="chevron-right" />
          </button>
        </>
      )}
    </div>
  );
});

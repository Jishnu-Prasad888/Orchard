/**
 * EPUB reader, via epub.js. Paginated (flow: "paginated"), with the
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
import type { Book, NavItem, Rendition } from 'epubjs';

import type { ReaderHandle, ReaderProps, SearchMatch, TocEntry } from './types';

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

export const EpubReader = forwardRef<ReaderHandle, ReaderProps>(function EpubReader(
  { contentUrl, initialLocation, preferences, onProgress, onToc, onError },
  ref,
) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const bookRef = useRef<Book | null>(null);
  const renditionRef = useRef<Rendition | null>(null);
  const locationsReady = useRef(false);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    // requestCredentials is declared as `object` in epub.js's shipped types,
    // but the runtime only checks truthiness before setting
    // `xhr.withCredentials = true` — a boolean is the documented usage.
    const book = ePub(contentUrl, { requestCredentials: true as unknown as object });
    bookRef.current = book;
    const rendition = book.renderTo(container, {
      width: '100%',
      height: '100%',
      flow: 'paginated',
      spread: 'none',
    });
    renditionRef.current = rendition;

    rendition.themes.register('light', { body: { color: '#1a1a1a', background: '#ffffff' } });
    rendition.themes.register('dark', { body: { color: '#e4e4e4', background: '#1a1a1a' } });
    rendition.themes.register('sepia', { body: { color: '#3a2e1f', background: '#f4ecd8' } });

    rendition.on('relocated', (location: { start: { cfi: string } }) => {
      const cfi = location.start.cfi;
      const percent = locationsReady.current ? book.locations.percentageFromCfi(cfi) * 100 : 0;
      onProgress(cfi, percent);
    });

    let cancelled = false;
    book.ready
      .then(() => rendition.display(initialLocation || undefined))
      .then(() => {
        if (cancelled) return;
        setReady(true);
      })
      .catch((e: unknown) => {
        if (!cancelled) onError(e instanceof Error ? e.message : 'Could not open this EPUB.');
      });

    book.loaded.navigation
      .then((nav) => {
        if (!cancelled) onToc(flattenToc(nav.toc, 0));
      })
      .catch(() => undefined);

    // Percentage needs a location map; this can take a while on a large
    // book, so it runs in the background rather than blocking the first page.
    book.locations
      .generate(1000)
      .then(() => {
        locationsReady.current = true;
      })
      .catch(() => undefined);

    return () => {
      cancelled = true;
      rendition.destroy();
      book.destroy();
      bookRef.current = null;
      renditionRef.current = null;
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
    rendition.themes.override('max-width', `${preferences.reading_width}px`, true);
    rendition.themes.override('margin', '0 auto', true);
    rendition.themes.override('padding', `${preferences.margin}px`, true);
  }, [preferences, ready]);

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
    <div className="reader-epub">
      {!ready && <p className="reader-loading muted">Opening EPUB…</p>}
      <div className="reader-epub-viewport" ref={containerRef} />
    </div>
  );
});

/**
 * The reader — a full-page, immersive view for a book, outside the app
 * shell's sidebar. Reading is a sustained activity, not a quick glance, so
 * it gets its own route (`/read/:libraryId/:fileId`) rather than the
 * photo/video viewer's overlay.
 *
 * Three reader implementations (PDF, EPUB, Markdown/text) share one
 * contract (see `components/reader/types.ts`): a toolbar drives TOC,
 * search, book details, reader preferences, and full-screen the same way
 * regardless of which one is mounted. Reading position is opaque to this
 * page — it only round-trips `location` between the active reader and the
 * progress API — and is saved debounced so turning pages doesn't fire a
 * request per page.
 *
 * Selected text can be highlighted or saved as a quote from the bar that
 * appears over the book, or from a right-click menu. Either takes a snapshot
 * of the selection first: typing a note moves focus out of the book, which
 * clears the live selection, and the note must still know what it is about.
 */

import { Maximize, Minimize } from 'lucide-react';
import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';

import {
  createBookmark,
  createHighlight,
  createQuote,
  deleteBookmark,
  deleteHighlight,
  deleteQuote,
  getBookMetadata,
  getFile,
  getReaderPreferences,
  getReadingProgress,
  listFileBookmarks,
  listFileHighlights,
  listFileQuotes,
  markFileOpened,
  searchFileContent,
  setReaderPreferences as saveReaderPreferences,
  setReadingProgress,
  updateBookmark,
  updateQuote,
} from '../api/queries';
import type {
  BookMetadata,
  Bookmark,
  ContentHit,
  FileSummary,
  Highlight,
  Quote,
  ReaderPreferences,
} from '../api/types';
import { DEFAULT_READER_PREFERENCES, HIGHLIGHT_COLORS } from '../api/types';
import { ContextMenu, type MenuEntry, type MenuPosition } from '../components/ContextMenu';
import { downloadUrl, fileExtension } from '../components/media';
import { BookmarksPanel, QuotesPanel } from '../components/reader/AnnotationPanels';
import { ReaderSettingsPanel } from '../components/reader/ReaderSettingsPanel';
import { TextReader } from '../components/reader/TextReader';
import type {
  ReaderHandle,
  ReaderHighlight,
  ReaderPoint,
  ReaderSelection,
  SearchMatch,
  TocEntry,
} from '../components/reader/types';
import { BookPanel } from '../components/viewer/BookPanel';
import { ErrorState, LoadingState } from '../components/States';
import { Icon } from '../components/ui/Icon';
import { lazyPage } from '../lib/lazyPage';
import { useAppTheme } from '../lib/useAppTheme';
import './ReaderPage.css';

// pdf.js and epub.js are by far the heaviest code in the app; load only the
// engine the open book needs, so an EPUB never downloads pdf.js.
const PdfReader = lazyPage(() =>
  import('../components/reader/PdfReader').then((m) => ({ default: m.PdfReader })),
);
const EpubReader = lazyPage(() =>
  import('../components/reader/EpubReader').then((m) => ({ default: m.EpubReader })),
);

const PROGRESS_SAVE_DEBOUNCE_MS = 2500;
/** How long a "Quoted!" / "Bookmarked!" confirmation stays up. */
const FLASH_MS = 1200;

type PanelTab = 'toc' | 'search' | 'book' | 'settings' | 'annotations' | 'quotes';

function readerKind(fileName: string): 'pdf' | 'epub' | 'text' {
  switch (fileExtension(fileName).toLowerCase()) {
    case 'pdf':
      return 'pdf';
    case 'epub':
      return 'epub';
    default:
      return 'text';
  }
}

/** Reads the route params and keys the actual page by file id, so opening
 * a different book (without ever unmounting the /read/... route) gets a
 * fully fresh component instance instead of stale state from the last one. */
export default function ReaderPage() {
  const { libraryId = '', fileId = '' } = useParams();
  return <ReaderPageContent key={fileId} libraryId={libraryId} fileId={fileId} />;
}

function ReaderPageContent({ libraryId, fileId }: { libraryId: string; fileId: string }) {
  const [params] = useSearchParams();
  const navigate = useNavigate();

  const [file, setFile] = useState<FileSummary | null>(null);
  const [book, setBook] = useState<BookMetadata | null>(null);
  const [preferences, setPreferences] = useState<ReaderPreferences>(DEFAULT_READER_PREFERENCES);
  const [initialLocation, setInitialLocation] = useState<string | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [toc, setToc] = useState<TocEntry[]>([]);
  const [panel, setPanel] = useState<PanelTab | null>(null);
  const [searchQuery, setSearchQuery] = useState('');
  const [searchResults, setSearchResults] = useState<SearchMatch[] | null>(null);
  const [contentHits, setContentHits] = useState<ContentHit[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [readerError, setReaderError] = useState<string | null>(null);
  const [isFullscreen, setIsFullscreen] = useState(false);

  const [bookmarks, setBookmarks] = useState<Bookmark[]>([]);
  const [highlights, setHighlights] = useState<Highlight[]>([]);
  const [quotes, setQuotes] = useState<Quote[]>([]);
  // The live selection in the book, and the snapshot an action works on.
  const [selection, setSelection] = useState<ReaderSelection | null>(null);
  const [pending, setPending] = useState<ReaderSelection | null>(null);
  const [selectionAction, setSelectionAction] = useState<'highlight' | 'quote' | null>(null);
  const [menuAt, setMenuAt] = useState<MenuPosition | null>(null);
  const [flash, setFlash] = useState<{ id: number; message: string } | null>(null);
  const [highlightColor, setHighlightColor] = useState<string>(HIGHLIGHT_COLORS[0]);
  const [highlightNote, setHighlightNote] = useState('');
  const [quoteCommentary, setQuoteCommentary] = useState('');
  const [savingAnnotation, setSavingAnnotation] = useState(false);
  const [annotationError, setAnnotationError] = useState<string | null>(null);

  // "Match app" resolves to the app's light/dark palette, live; the readers
  // only ever see a concrete theme.
  const appTheme = useAppTheme();
  const readerPreferences = useMemo<ReaderPreferences>(
    () => (preferences.theme === 'auto' ? { ...preferences, theme: appTheme } : preferences),
    [preferences, appTheme],
  );

  const readerRef = useRef<ReaderHandle | null>(null);
  const pageRootRef = useRef<HTMLDivElement | null>(null);
  // The same element as state, for rendering the selection menu inside it.
  const [shellEl, setShellEl] = useState<HTMLDivElement | null>(null);
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const latestProgress = useRef<{ location: string; percent: number } | null>(null);
  const selectionRef = useRef<ReaderSelection | null>(null);
  useEffect(() => {
    selectionRef.current = selection;
  }, [selection]);
  const flashTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  /** A small confirmation that disappears on its own almost at once. */
  const showFlash = useCallback((message: string) => {
    if (flashTimer.current) clearTimeout(flashTimer.current);
    setFlash({ id: Date.now(), message });
    flashTimer.current = setTimeout(() => setFlash(null), FLASH_MS);
  }, []);
  useEffect(
    () => () => {
      if (flashTimer.current) clearTimeout(flashTimer.current);
    },
    [],
  );

  // Load the file, its book metadata, saved progress, and reader
  // preferences once. A `?loc=` in the URL (a future bookmark/search deep
  // link) wins over the saved resume point.
  useEffect(() => {
    let cancelled = false;
    Promise.all([
      getFile(libraryId, fileId),
      getBookMetadata(libraryId, fileId),
      getReadingProgress(libraryId, fileId),
      getReaderPreferences(libraryId),
    ])
      .then(([fileResp, bookResp, progressResp, prefsResp]) => {
        if (cancelled) return;
        setFile(fileResp.file);
        setBook(bookResp.book);
        // Older saved preferences predate some fields; defaults fill them in.
        setPreferences({ ...DEFAULT_READER_PREFERENCES, ...prefsResp.preferences });
        const fromUrl = params.get('loc');
        setInitialLocation(fromUrl ?? progressResp.progress?.location ?? '');
        markFileOpened(libraryId, fileId).catch(() => {});
      })
      .catch((e: unknown) => {
        if (!cancelled) setLoadError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
    // Only the identity of the file being read should reopen everything;
    // `params` changing (e.g. the reader's own navigation) must not.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [libraryId, fileId]);

  const flushProgress = useCallback(() => {
    if (saveTimer.current) {
      clearTimeout(saveTimer.current);
      saveTimer.current = null;
    }
    const pending = latestProgress.current;
    if (pending) {
      setReadingProgress(libraryId, fileId, pending.location, pending.percent).catch(() => {});
    }
  }, [libraryId, fileId]);

  const onProgress = useCallback(
    (location: string, percent: number) => {
      latestProgress.current = { location, percent };
      if (saveTimer.current) clearTimeout(saveTimer.current);
      saveTimer.current = setTimeout(flushProgress, PROGRESS_SAVE_DEBOUNCE_MS);
    },
    [flushProgress],
  );

  // Save whatever is pending when leaving the page.
  useEffect(() => () => flushProgress(), [flushProgress]);

  // This file's bookmarks, highlights, and quotes, independent of the main
  // load — a slow fetch here shouldn't block opening the book.
  useEffect(() => {
    let cancelled = false;
    // Each list stands alone: one failing to load mustn't hide the others.
    listFileBookmarks(libraryId, fileId)
      .then((r) => !cancelled && setBookmarks(r.bookmarks ?? []))
      .catch(() => {});
    listFileHighlights(libraryId, fileId)
      .then((r) => !cancelled && setHighlights(r.highlights ?? []))
      .catch(() => {});
    listFileQuotes(libraryId, fileId)
      .then((r) => !cancelled && setQuotes(r.quotes ?? []))
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [libraryId, fileId]);

  const addBookmarkHere = useCallback(async () => {
    const location = latestProgress.current?.location || initialLocation || '';
    if (!location) return;
    try {
      const { bookmark } = await createBookmark(libraryId, fileId, location);
      setBookmarks((prev) => [bookmark, ...prev]);
      showFlash('Bookmarked!');
    } catch (e) {
      setAnnotationError(e instanceof Error ? e.message : String(e));
    }
  }, [libraryId, fileId, initialLocation, showFlash]);

  const removeBookmark = useCallback(
    async (id: string) => {
      setBookmarks((prev) => prev.filter((b) => b.id !== id));
      try {
        await deleteBookmark(libraryId, id);
      } catch (e) {
        setAnnotationError(e instanceof Error ? e.message : String(e));
      }
    },
    [libraryId],
  );

  const removeHighlight = useCallback(
    async (id: string) => {
      setHighlights((prev) => prev.filter((h) => h.id !== id));
      try {
        await deleteHighlight(libraryId, id);
      } catch (e) {
        setAnnotationError(e instanceof Error ? e.message : String(e));
      }
    },
    [libraryId],
  );

  const removeQuote = useCallback(
    async (id: string) => {
      setQuotes((prev) => prev.filter((q) => q.id !== id));
      try {
        await deleteQuote(libraryId, id);
      } catch (e) {
        setAnnotationError(e instanceof Error ? e.message : String(e));
      }
    },
    [libraryId],
  );

  const renameBookmark = useCallback(
    async (id: string, label: string) => {
      setBookmarks((prev) => prev.map((b) => (b.id === id ? { ...b, label } : b)));
      try {
        const { bookmark } = await updateBookmark(libraryId, id, label);
        setBookmarks((prev) => prev.map((b) => (b.id === id ? bookmark : b)));
      } catch (e) {
        setAnnotationError(e instanceof Error ? e.message : String(e));
      }
    },
    [libraryId],
  );

  const toggleQuoteFavorite = useCallback(
    async (quote: Quote) => {
      const favorite = !quote.favorite;
      setQuotes((prev) => prev.map((q) => (q.id === quote.id ? { ...q, favorite } : q)));
      try {
        await updateQuote(libraryId, quote.id, { favorite });
      } catch (e) {
        setQuotes((prev) =>
          prev.map((q) => (q.id === quote.id ? { ...q, favorite: !favorite } : q)),
        );
        setAnnotationError(e instanceof Error ? e.message : String(e));
      }
    },
    [libraryId],
  );

  /** Ends whatever selection action is under way, and the selection itself:
   * once saved, the passage shows as a highlight, not as selected text. */
  const finishSelection = useCallback(() => {
    setSelectionAction(null);
    setPending(null);
    setSelection(null);
    setHighlightNote('');
    setQuoteCommentary('');
    window.getSelection()?.removeAllRanges();
  }, []);

  /** Opens the note/thought form for the current selection. */
  const beginSelectionAction = useCallback(
    (action: 'highlight' | 'quote', target: ReaderSelection) => {
      setPending(target);
      setSelectionAction(action);
    },
    [],
  );

  const saveHighlight = useCallback(
    async (target: ReaderSelection, color: string, note = '') => {
      setSavingAnnotation(true);
      setAnnotationError(null);
      try {
        const { highlight } = await createHighlight(
          libraryId,
          fileId,
          target.location,
          target.text,
          color,
          note,
        );
        setHighlights((prev) => [highlight, ...prev]);
        finishSelection();
        showFlash('Highlighted!');
      } catch (e) {
        setAnnotationError(e instanceof Error ? e.message : String(e));
      } finally {
        setSavingAnnotation(false);
      }
    },
    [libraryId, fileId, finishSelection, showFlash],
  );

  const saveQuote = useCallback(
    async (target: ReaderSelection, commentary = '') => {
      setSavingAnnotation(true);
      setAnnotationError(null);
      try {
        const { quote } = await createQuote(libraryId, fileId, {
          text: target.text,
          location: target.location,
          commentary,
        });
        setQuotes((prev) => [quote, ...prev]);
        finishSelection();
        showFlash('Quoted!');
      } catch (e) {
        setAnnotationError(e instanceof Error ? e.message : String(e));
      } finally {
        setSavingAnnotation(false);
      }
    },
    [libraryId, fileId, finishSelection, showFlash],
  );

  /** Right-click over selected text: the page's own menu, on a snapshot of
   * the selection (opening the menu moves focus out of the book). */
  const openSelectionMenu = useCallback((point: ReaderPoint) => {
    const current = selectionRef.current;
    if (!current) return;
    setPending(current);
    setSelectionAction(null);
    setMenuAt(point);
  }, []);

  const onSurfaceContextMenu = (event: React.MouseEvent) => {
    // Only over the book's own text, and only with something selected; the
    // browser's menu stays everywhere else.
    if (!selectionRef.current) return;
    const target = event.target as HTMLElement;
    if (target.closest('.reader-selection-bar, .reader-pdf-pagebar')) return;
    event.preventDefault();
    openSelectionMenu({ x: event.clientX, y: event.clientY });
  };

  const menuEntries: MenuEntry[] = pending
    ? [
        { id: 'quote', label: 'Save quote', onSelect: () => void saveQuote(pending) },
        {
          id: 'quote-thought',
          label: 'Save quote with a thought…',
          onSelect: () => beginSelectionAction('quote', pending),
        },
        'separator',
        {
          id: 'highlight',
          label: 'Highlight',
          onSelect: () => void saveHighlight(pending, highlightColor),
        },
        {
          id: 'highlight-note',
          label: 'Highlight with a note…',
          onSelect: () => beginSelectionAction('highlight', pending),
        },
        'separator',
        {
          id: 'copy',
          label: 'Copy',
          onSelect: () => {
            void navigator.clipboard?.writeText(pending.text).then(
              () => showFlash('Copied!'),
              () => undefined,
            );
          },
        },
      ]
    : [];

  /** Where a saved location is, in words: a page, the chapter (a heading
   * in a Markdown book) it falls under, or how far through a text it is.
   * EPUB positions have no cheap human form, so they go undescribed. */
  const describeLocation = useCallback(
    (location: string): string | null => {
      const page = /^page:(\d+)$/.exec(location);
      if (page) return `Page ${page[1]}`;
      const block = /^block-(\d+)$/.exec(location);
      if (block) {
        const at = Number(block[1]);
        let chapter: string | null = null;
        for (const entry of toc) {
          const m = /^block-(\d+)$/.exec(entry.location);
          if (m && Number(m[1]) <= at) chapter = entry.label;
        }
        return chapter;
      }
      const fraction = Number(location);
      if (location && Number.isFinite(fraction)) return `${Math.round(fraction * 100)}% through`;
      return null;
    },
    [toc],
  );

  const jumpTo = useCallback((location: string) => readerRef.current?.goTo(location), []);

  // What the reader paints: the saved highlights, in the shape it takes.
  const readerHighlights = useMemo<ReaderHighlight[]>(
    () => highlights.map((h) => ({ id: h.id, location: h.location, text: h.text, color: h.color })),
    [highlights],
  );

  const onSavePreferences = useCallback(
    (next: ReaderPreferences) => {
      setPreferences(next);
      saveReaderPreferences(next, libraryId).catch(() => {});
    },
    [libraryId],
  );

  const runSearch = useCallback(
    async (query: string) => {
      setSearchQuery(query);
      if (!query.trim()) {
        setSearchResults(null);
        setContentHits(null);
        return;
      }
      setSearching(true);
      // The reader's own client-side search (open-book text only) and the
      // server-side content search (this book's extracted text plus your
      // quotes/highlights in it) run side by side — one has no
      // bearing on the other's availability.
      searchFileContent(libraryId, fileId, query)
        .then((resp) => setContentHits(resp.hits ?? []))
        .catch(() => setContentHits([]));
      try {
        const results = (await readerRef.current?.search(query)) ?? [];
        setSearchResults(results);
      } finally {
        setSearching(false);
      }
    },
    [libraryId, fileId],
  );

  const togglePanel = (tab: PanelTab) => {
    setPanel((p) => (p === tab ? null : tab));
  };

  const toggleFullscreen = () => {
    const el = pageRootRef.current;
    if (!el) return;
    if (document.fullscreenElement) {
      void document.exitFullscreen().catch(() => undefined);
    } else {
      void el.requestFullscreen?.().catch(() => undefined);
    }
  };

  useEffect(() => {
    const onChange = () => setIsFullscreen(!!document.fullscreenElement);
    document.addEventListener('fullscreenchange', onChange);
    return () => document.removeEventListener('fullscreenchange', onChange);
  }, []);

  const contentUrl = useMemo(() => (file ? downloadUrl(libraryId, file) : ''), [libraryId, file]);
  const kind = file ? readerKind(file.name) : 'text';

  if (loadError) {
    return (
      <main className="page reader-page">
        <ErrorState message={loadError} title="Couldn't open this book" />
        <Link className="button" to="/files">
          Back to files
        </Link>
      </main>
    );
  }
  if (!file || initialLocation === null) {
    return (
      <main className="page reader-page">
        <LoadingState label="Opening…" />
      </main>
    );
  }

  const title = book?.title || file.name;
  const author = book?.author;

  return (
    <div
      className="reader-shell"
      ref={(el) => {
        pageRootRef.current = el;
        setShellEl(el);
      }}
      data-theme={readerPreferences.theme}
    >
      <header className="reader-topbar">
        <button
          type="button"
          className="icon-button"
          onClick={() => navigate(-1)}
          aria-label="Back"
        >
          <Icon name="chevron-left" />
        </button>
        <div className="reader-topbar-title">
          <span className="reader-topbar-book">{title}</span>
          {author && <span className="reader-topbar-author"> · {author}</span>}
        </div>
        <div className="reader-topbar-actions">
          <button
            type="button"
            className={panel === 'toc' ? 'icon-button active' : 'icon-button'}
            onClick={() => togglePanel('toc')}
            aria-label="Table of contents"
            disabled={toc.length === 0}
          >
            <Icon name="list" />
          </button>
          <button
            type="button"
            className={panel === 'search' ? 'icon-button active' : 'icon-button'}
            onClick={() => togglePanel('search')}
            aria-label="Search this book"
          >
            <Icon name="search" />
          </button>
          <button
            type="button"
            className={panel === 'book' ? 'icon-button active' : 'icon-button'}
            onClick={() => togglePanel('book')}
            aria-label="Book details"
          >
            <Icon name="document" />
          </button>
          <button
            type="button"
            className={panel === 'annotations' ? 'icon-button active' : 'icon-button'}
            onClick={() => togglePanel('annotations')}
            aria-label="Bookmarks and highlights"
          >
            <Icon name="bookmark" />
          </button>
          <button
            type="button"
            className={panel === 'quotes' ? 'icon-button active' : 'icon-button'}
            onClick={() => togglePanel('quotes')}
            aria-label="Quotes from this book"
          >
            <Icon name="quote" />
          </button>
          <button
            type="button"
            className={panel === 'settings' ? 'icon-button active' : 'icon-button'}
            onClick={() => togglePanel('settings')}
            aria-label="Reading settings"
          >
            <Icon name="settings" />
          </button>
          <button
            type="button"
            className="icon-button"
            onClick={toggleFullscreen}
            aria-label={isFullscreen ? 'Exit full screen' : 'Full screen'}
          >
            {isFullscreen ? <Minimize /> : <Maximize />}
          </button>
        </div>
      </header>

      <div className="reader-body">
        <div className="reader-surface" onContextMenu={onSurfaceContextMenu}>
          {readerError && <ErrorState message={readerError} title="Couldn't display this page" />}
          <Suspense fallback={<LoadingState label="Opening the book…" />}>
            {!readerError && kind === 'pdf' && (
              <PdfReader
                ref={readerRef}
                libraryId={libraryId}
                fileId={fileId}
                fileName={file.name}
                contentUrl={contentUrl}
                initialLocation={initialLocation}
                preferences={readerPreferences}
                onProgress={onProgress}
                onToc={setToc}
                onError={setReaderError}
                onSelectionChange={setSelection}
                highlights={readerHighlights}
                onSelectionContextMenu={openSelectionMenu}
              />
            )}
            {!readerError && kind === 'epub' && (
              <EpubReader
                ref={readerRef}
                libraryId={libraryId}
                fileId={fileId}
                fileName={file.name}
                contentUrl={contentUrl}
                initialLocation={initialLocation}
                preferences={readerPreferences}
                onProgress={onProgress}
                onToc={setToc}
                onError={setReaderError}
                onSelectionChange={setSelection}
                highlights={readerHighlights}
                onSelectionContextMenu={openSelectionMenu}
              />
            )}
          </Suspense>
          {!readerError && kind === 'text' && (
            <TextReader
              ref={readerRef}
              libraryId={libraryId}
              fileId={fileId}
              fileName={file.name}
              contentUrl={contentUrl}
              initialLocation={initialLocation}
              preferences={readerPreferences}
              onProgress={onProgress}
              onToc={setToc}
              onError={setReaderError}
              onSelectionChange={setSelection}
              highlights={readerHighlights}
              onSelectionContextMenu={openSelectionMenu}
            />
          )}

          {(selection || selectionAction) && (
            <div
              className="reader-selection-bar"
              // Pressing a button here mustn't clear the selection it acts on.
              onMouseDown={(e) => {
                if (!(e.target as HTMLElement).closest('input')) e.preventDefault();
              }}
            >
              {!selectionAction && selection && (
                <>
                  <span className="reader-selection-preview">
                    “
                    {selection.text.length > 80
                      ? `${selection.text.slice(0, 80)}…`
                      : selection.text}
                    ”
                  </span>
                  <div className="reader-highlight-colors" role="group" aria-label="Highlight">
                    {HIGHLIGHT_COLORS.map((c) => (
                      <button
                        key={c}
                        type="button"
                        aria-label={`Highlight ${c}`}
                        title={`Highlight ${c}`}
                        className={`reader-color-swatch reader-color-${c}`}
                        disabled={savingAnnotation}
                        onClick={() => {
                          setHighlightColor(c);
                          void saveHighlight(selection, c);
                        }}
                      />
                    ))}
                  </div>
                  <button
                    type="button"
                    className="button ghost-button"
                    disabled={savingAnnotation}
                    onClick={() => void saveQuote(selection)}
                  >
                    <Icon name="quote" /> Quote
                  </button>
                  <button
                    type="button"
                    className="button ghost-button"
                    onClick={() => beginSelectionAction('highlight', selection)}
                  >
                    <Icon name="highlighter" /> Note…
                  </button>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label="Dismiss selection"
                    onClick={finishSelection}
                  >
                    <Icon name="close" />
                  </button>
                </>
              )}
              {selectionAction === 'highlight' && pending && (
                <form
                  className="reader-selection-form"
                  onSubmit={(e) => {
                    e.preventDefault();
                    void saveHighlight(pending, highlightColor, highlightNote);
                  }}
                >
                  <div className="reader-highlight-colors">
                    {HIGHLIGHT_COLORS.map((c) => (
                      <button
                        key={c}
                        type="button"
                        aria-label={`Color ${c}`}
                        aria-pressed={highlightColor === c}
                        className={`reader-color-swatch reader-color-${c}${highlightColor === c ? ' active' : ''}`}
                        onClick={() => setHighlightColor(c)}
                      />
                    ))}
                  </div>
                  <input
                    className="reader-selection-input"
                    placeholder="Add a note (optional)"
                    value={highlightNote}
                    onChange={(e) => setHighlightNote(e.target.value)}
                    autoFocus
                  />
                  <button type="submit" className="button" disabled={savingAnnotation}>
                    Save
                  </button>
                  <button type="button" className="button ghost-button" onClick={finishSelection}>
                    Cancel
                  </button>
                </form>
              )}
              {selectionAction === 'quote' && pending && (
                <form
                  className="reader-selection-form"
                  onSubmit={(e) => {
                    e.preventDefault();
                    void saveQuote(pending, quoteCommentary);
                  }}
                >
                  <input
                    className="reader-selection-input"
                    placeholder="Add a thought (optional)"
                    value={quoteCommentary}
                    onChange={(e) => setQuoteCommentary(e.target.value)}
                    autoFocus
                  />
                  <button type="submit" className="button" disabled={savingAnnotation}>
                    Save quote
                  </button>
                  <button type="button" className="button ghost-button" onClick={finishSelection}>
                    Cancel
                  </button>
                </form>
              )}
              {annotationError && !panel && (
                <p className="error-text reader-selection-error" role="alert">
                  {annotationError}
                </p>
              )}
            </div>
          )}

          {flash && (
            <div key={flash.id} className="reader-flash" role="status" data-testid="reader-flash">
              {flash.message}
            </div>
          )}
        </div>

        {panel && (
          <aside className="reader-panel" aria-label="Reader panel">
            {panel === 'toc' && (
              <nav aria-label="Table of contents" data-testid="reader-toc">
                <ul className="reader-toc-list">
                  {toc.map((entry) => (
                    <li key={entry.location} style={{ paddingLeft: `${entry.depth * 16}px` }}>
                      <button
                        type="button"
                        className="reader-toc-item"
                        onClick={() => readerRef.current?.goTo(entry.location)}
                      >
                        {entry.label}
                      </button>
                    </li>
                  ))}
                </ul>
              </nav>
            )}

            {panel === 'search' && (
              <div data-testid="reader-search">
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    void runSearch(searchQuery);
                  }}
                >
                  <label className="visually-hidden" htmlFor="reader-search-input">
                    Search this book
                  </label>
                  <input
                    id="reader-search-input"
                    className="reader-search-input"
                    value={searchQuery}
                    onChange={(e) => setSearchQuery(e.target.value)}
                    placeholder="Search…"
                  />
                </form>
                {searching && <p className="muted">Searching…</p>}
                {!searching && searchResults !== null && searchResults.length === 0 && (
                  <p className="muted">No matches.</p>
                )}
                {!searching && searchResults && searchResults.length > 0 && (
                  <ul className="reader-search-results">
                    {searchResults.map((m, i) => (
                      <li key={`${m.location}-${i}`}>
                        <button
                          type="button"
                          className="reader-search-result"
                          onClick={() => readerRef.current?.goTo(m.location)}
                        >
                          {m.excerpt}
                        </button>
                      </li>
                    ))}
                  </ul>
                )}

                {contentHits !== null && contentHits.length > 0 && (
                  <>
                    <h3 className="reader-search-section-heading">
                      Your quotes &amp; highlights in this book
                    </h3>
                    <ul className="reader-search-results" data-testid="reader-content-hits">
                      {contentHits.map((h, i) =>
                        h.location ? (
                          <li key={`${h.source_type}-${h.source_id}-${i}`}>
                            <button
                              type="button"
                              className="reader-search-result"
                              onClick={() => readerRef.current?.goTo(h.location!)}
                            >
                              {h.excerpt}
                            </button>
                          </li>
                        ) : (
                          <li
                            key={`${h.source_type}-${h.source_id}-${i}`}
                            className="reader-search-result-static"
                          >
                            {h.excerpt}
                          </li>
                        ),
                      )}
                    </ul>
                  </>
                )}
              </div>
            )}

            {panel === 'annotations' && (
              <BookmarksPanel
                bookmarks={bookmarks}
                highlights={highlights}
                describe={describeLocation}
                onJump={jumpTo}
                error={annotationError}
                onAddBookmark={() => void addBookmarkHere()}
                onRenameBookmark={(id, label) => void renameBookmark(id, label)}
                onRemoveBookmark={(id) => void removeBookmark(id)}
                onRemoveHighlight={(id) => void removeHighlight(id)}
              />
            )}

            {panel === 'quotes' && (
              <QuotesPanel
                quotes={quotes}
                describe={describeLocation}
                onJump={jumpTo}
                error={annotationError}
                onRemove={(id) => void removeQuote(id)}
                onToggleFavorite={(q) => void toggleQuoteFavorite(q)}
                onCopy={(q) => {
                  void navigator.clipboard?.writeText(q.text).then(
                    () => showFlash('Copied!'),
                    () => undefined,
                  );
                }}
              />
            )}

            {panel === 'book' && <BookPanel libraryId={libraryId} file={file} />}

            {panel === 'settings' && (
              <ReaderSettingsPanel
                preferences={preferences}
                onChange={onSavePreferences}
                typography={kind !== 'pdf'}
                layout={kind !== 'text'}
              />
            )}
          </aside>
        )}
      </div>

      <ContextMenu
        open={menuAt !== null && pending !== null}
        position={menuAt}
        entries={menuEntries}
        label="Selected text"
        onClose={() => setMenuAt(null)}
        container={shellEl}
      />
    </div>
  );
}

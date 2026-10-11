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
 */

import { Maximize, Minimize } from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';

import {
  getBookMetadata,
  getFile,
  getReaderPreferences,
  getReadingProgress,
  markFileOpened,
  setReaderPreferences as saveReaderPreferences,
  setReadingProgress,
} from '../api/queries';
import type { BookMetadata, FileSummary, ReaderPreferences } from '../api/types';
import { DEFAULT_READER_PREFERENCES } from '../api/types';
import { downloadUrl, fileExtension } from '../components/media';
import { EpubReader } from '../components/reader/EpubReader';
import { PdfReader } from '../components/reader/PdfReader';
import { ReaderSettingsPanel } from '../components/reader/ReaderSettingsPanel';
import { TextReader } from '../components/reader/TextReader';
import type { ReaderHandle, SearchMatch, TocEntry } from '../components/reader/types';
import { BookPanel } from '../components/viewer/BookPanel';
import { ErrorState, LoadingState } from '../components/States';
import { Icon } from '../components/ui/Icon';
import './ReaderPage.css';

const PROGRESS_SAVE_DEBOUNCE_MS = 2500;

type PanelTab = 'toc' | 'search' | 'book' | 'settings';

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
  const [searching, setSearching] = useState(false);
  const [readerError, setReaderError] = useState<string | null>(null);
  const [isFullscreen, setIsFullscreen] = useState(false);

  const readerRef = useRef<ReaderHandle | null>(null);
  const pageRootRef = useRef<HTMLDivElement | null>(null);
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const latestProgress = useRef<{ location: string; percent: number } | null>(null);

  // Load the file, its book metadata, saved progress, and reader
  // preferences once. A `?loc=` in the URL (a future bookmark/search deep
  // link) wins over the saved resume point.
  useEffect(() => {
    let cancelled = false;
    Promise.all([
      getFile(libraryId, fileId),
      getBookMetadata(libraryId, fileId),
      getReadingProgress(libraryId, fileId),
      getReaderPreferences(),
    ])
      .then(([fileResp, bookResp, progressResp, prefsResp]) => {
        if (cancelled) return;
        setFile(fileResp.file);
        setBook(bookResp.book);
        setPreferences(prefsResp.preferences);
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

  const onSavePreferences = useCallback(
    (next: ReaderPreferences) => {
      setPreferences(next);
      saveReaderPreferences(next).catch(() => {});
    },
    [],
  );

  const runSearch = useCallback(
    async (query: string) => {
      setSearchQuery(query);
      if (!query.trim()) {
        setSearchResults(null);
        return;
      }
      setSearching(true);
      try {
        const results = (await readerRef.current?.search(query)) ?? [];
        setSearchResults(results);
      } finally {
        setSearching(false);
      }
    },
    [],
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
    <div className="reader-shell" ref={pageRootRef} data-theme={preferences.theme}>
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
        <div className="reader-surface">
          {readerError && <ErrorState message={readerError} title="Couldn't display this page" />}
          {!readerError && kind === 'pdf' && (
            <PdfReader
              ref={readerRef}
              libraryId={libraryId}
              fileId={fileId}
              fileName={file.name}
              contentUrl={contentUrl}
              initialLocation={initialLocation}
              preferences={preferences}
              onProgress={onProgress}
              onToc={setToc}
              onError={setReaderError}
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
              preferences={preferences}
              onProgress={onProgress}
              onToc={setToc}
              onError={setReaderError}
            />
          )}
          {!readerError && kind === 'text' && (
            <TextReader
              ref={readerRef}
              libraryId={libraryId}
              fileId={fileId}
              fileName={file.name}
              contentUrl={contentUrl}
              initialLocation={initialLocation}
              preferences={preferences}
              onProgress={onProgress}
              onToc={setToc}
              onError={setReaderError}
            />
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
              </div>
            )}

            {panel === 'book' && <BookPanel libraryId={libraryId} file={file} />}

            {panel === 'settings' && (
              <ReaderSettingsPanel preferences={preferences} onChange={onSavePreferences} />
            )}
          </aside>
        )}
      </div>
    </div>
  );
}

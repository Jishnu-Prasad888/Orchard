/**
 * Home — a living view of the library, not a dashboard.
 *
 * It leads with the most recently added files. There are no counters or
 * status tiles — the library's status lives in the sidebar and Settings.
 *
 * It is one small request, so the page stays quick on a Raspberry Pi with a
 * hundred thousand files.
 */

import { useCallback, useEffect, useLayoutEffect, useState } from 'react';
import { Link } from 'react-router-dom';

import { useAuth } from '../auth/authContext';
import { notifyLibraryChanged } from '../api/libraryEvents';
import { useLibraryResource } from '../api/resources';
import { listFiles, listRecentlyOpened } from '../api/queries';
import type { FileSummary, Library } from '../api/types';
import { useFileOperations } from '../components/FileOperations';
import { LibraryGatePage } from '../components/LibraryGatePage';
import { MediaGrid, MediaGridSkeleton } from '../components/media/MediaGrid';
import { gridMetrics } from '../components/media/layout';
import { EmptyState, ErrorState, LibraryOfflineNotice } from '../components/States';
import { Icon } from '../components/ui/Icon';
import { ViewerModal } from '../components/ViewerModal';
import { greeting } from '../lib/dates';
import { isIndexing, useIndexStatus } from '../components/app-shell/useIndexStatus';
import './HomePage.css';

const RECENT_LIMIT = 40;
/** Home shows a taste, not the library: two rows. */
const HOME_ROWS = 2;
const PHOTO_GAP = 4;
/** How often Home re-reads itself: fast while empty (a scan may be filling it). */
const HOME_EMPTY_REFRESH_MS = 3000;
const HOME_REFRESH_MS = 15000;

/** Width assumed before the first measurement (and in jsdom). */
const FALLBACK_WIDTH = 1024;

/**
 * Track an element's width so the grid can know how many columns it has. A
 * callback ref, because the section mounts only once its data has loaded.
 */
function useWidth(): [(el: HTMLElement | null) => void, number] {
  const [el, setEl] = useState<HTMLElement | null>(null);
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    if (!el) return;
    const measure = () => setWidth(Math.round(el.getBoundingClientRect().width));
    measure();
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [el]);
  return [setEl, width || FALLBACK_WIDTH];
}

/**
 * How many of `total` items to show in `rows` rows of `columns`: whole rows
 * only, so the last row is never ragged. With fewer than one row's worth, all
 * of them.
 */
function cleanRows(total: number, columns: number, rows = HOME_ROWS): number {
  if (total < columns) return total;
  return Math.min(rows * columns, Math.floor(total / columns) * columns);
}

interface HomeData {
  recent: FileSummary[];
  recentlyOpened: FileSummary[];
}

/** "Good evening, name" in the brand face, the same in every state. */
function Greeting({ name }: { name: string | undefined }) {
  return (
    <header className="home-hello">
      <h1 className="home-greeting">
        {greeting()}
        {name ? `, ${name}` : ''}
      </h1>
    </header>
  );
}

export default function HomePage() {
  const { user } = useAuth();
  // The greeting is there even while there is no library to greet you with.
  const title = `${greeting()}${user?.username ? `, ${user.username}` : ''}`;
  return (
    <LibraryGatePage title={title} className="home" header={<Greeting name={user?.username} />}>
      {(library) => <Home library={library} />}
    </LibraryGatePage>
  );
}

function Home({ library }: { library: Library }) {
  const { user } = useAuth();
  const libraryId = library.id;
  const offline = library.status === 'offline';

  const home = useLibraryResource<HomeData>(
    useCallback(async (id: string) => {
      const [recent, recentlyOpened] = await Promise.all([
        listFiles(id, {
          recursive: true,
          sort: 'mod_time',
          order: 'desc',
          limit: RECENT_LIMIT,
        })
          .then((r) => r.files ?? [])
          .catch(() => [] as FileSummary[]),
        listRecentlyOpened(id, RECENT_LIMIT)
          .then((r) => r.files ?? [])
          .catch(() => [] as FileSummary[]),
      ]);
      return { recent, recentlyOpened };
    }, []),
  );

  const ops = useFileOperations(libraryId, home.reload);
  const data = home.data;

  // A freshly added library is empty until its first scan has run. The shell
  // already watches the index and makes this page re-read as files arrive;
  // here it only decides whether to say "scanning" rather than "empty".
  const empty = data !== null && data.recent.length === 0;
  const indexStatus = useIndexStatus(libraryId, empty, false);
  const scanning = empty && isIndexing(indexStatus);

  // Keep the page current without a manual refresh. The shell only announces a
  // change when the index counts move between two of its polls, which a small
  // library that finishes scanning in between never does — so Home also asks to
  // be re-read on a timer: quickly while it has nothing to show, slowly after.
  // The re-read happens behind the data on screen, so nothing flashes.
  const hasLoaded = data !== null;
  useEffect(() => {
    if (!hasLoaded) return;
    const timer = setInterval(
      () => {
        if (!document.hidden) notifyLibraryChanged(libraryId);
      },
      empty ? HOME_EMPTY_REFRESH_MS : HOME_REFRESH_MS,
    );
    return () => clearInterval(timer);
  }, [hasLoaded, empty, libraryId]);

  const [recentRef, recentWidth] = useWidth();
  const photoColumns = gridMetrics(recentWidth, PHOTO_GAP).columns;
  const recent = data ? data.recent.slice(0, cleanRows(data.recent.length, photoColumns)) : [];

  const [openedRef, openedWidth] = useWidth();
  const openedColumns = gridMetrics(openedWidth, PHOTO_GAP).columns;
  const recentlyOpened = data
    ? data.recentlyOpened.slice(0, cleanRows(data.recentlyOpened.length, openedColumns))
    : [];

  const name = user?.username;

  const nothingYet = data !== null && !scanning && data.recent.length === 0;

  return (
    <main className="page home">
      <Greeting name={name} />

      {offline && <LibraryOfflineNotice library={library} />}
      {home.error && (
        <ErrorState message={home.error} onRetry={home.reload} title="Couldn't load your library" />
      )}

      {home.loading && (
        <section className="section" aria-label="Loading recent files">
          <div className="skeleton home-skeleton-title" />
          <MediaGridSkeleton count={12} />
        </section>
      )}

      {scanning && !home.loading && (
        <section className="section home-scanning" aria-label="Scanning library" aria-live="polite">
          <div className="home-scanning-inner">
            <span className="home-scanning-spinner" aria-hidden="true" />
            <p>Scanning your library… files will appear here shortly.</p>
          </div>
          <MediaGridSkeleton count={12} />
        </section>
      )}

      {nothingYet && !offline && (
        <EmptyState
          title="Your library is empty"
          testId="home-no-files"
          icon="folder"
          action={
            <Link className="button primary-button" to="/files">
              Browse files
            </Link>
          }
        >
          <p>
            Once Orchard has looked through {library.name}, what it finds will gather here. You can
            keep using Orchard while it works.
          </p>
        </EmptyState>
      )}

      {data && data.recentlyOpened.length > 0 && (
        <section className="section" aria-labelledby="home-opened-title">
          <div className="section-head">
            <h2 id="home-opened-title" className="section-title">
              Recently opened
            </h2>
          </div>
          <div data-testid="home-recently-opened" ref={openedRef}>
            <MediaGrid
              libraryId={libraryId}
              files={recentlyOpened}
              onOpen={ops.openViewer}
              label="Recently opened files"
              testId="home-recently-opened-grid"
            />
          </div>
        </section>
      )}

      {data && data.recent.length > 0 && (
        <section className="section" aria-labelledby="home-recent-title">
          <div className="section-head">
            <h2 id="home-recent-title" className="section-title">
              Recently added
            </h2>
            <Link to="/files" className="link-button">
              All files
              <Icon name="chevron-right" size={16} />
            </Link>
          </div>
          <div data-testid="home-recent" ref={recentRef}>
            <MediaGrid
              libraryId={libraryId}
              files={recent}
              onOpen={ops.openViewer}
              label="Recently added files"
              testId="home-recent-grid"
            />
          </div>
        </section>
      )}

      {ops.viewer && data && (
        <ViewerModal
          libraryId={libraryId}
          file={ops.viewer}
          siblings={recent}
          onNavigate={ops.openViewer}
          onChanged={home.reload}
          onRequestAction={ops.requestAction}
          onClose={ops.closeViewer}
        />
      )}
      {ops.dialogs}
    </main>
  );
}

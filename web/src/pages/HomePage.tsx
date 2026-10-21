/**
 * Home — a living view of the library, not a dashboard.
 *
 * It leads with the most recently added files, then continue-reading,
 * favorites, pinned collections, and recent writing — each a "taste, not the
 * library" (a row or two, with a link to the full page). Section order and
 * visibility are per-user (see Customize), so this list is the default, not
 * the only shape Home can take.
 *
 * It is one small request per section, so the page stays quick on a
 * Raspberry Pi with a hundred thousand files.
 */

import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';

import { useAuth } from '../auth/authContext';
import { useFileLabels } from '../api/fileLabels';
import { useLibraryResource } from '../api/resources';
import {
  clearReadingStatus,
  getHomeLayout,
  listCollections,
  listFavorites,
  listFiles,
  listInProgress,
  listLibraryQuotes,
  listRecentlyOpened,
  setHomeLayout,
  setReadingStatus,
} from '../api/queries';
import type {
  Collection,
  FileSummary,
  HomeLayoutSection,
  HomeSectionId,
  InProgressFile,
  Library,
  Quote,
} from '../api/types';
import { Dialog } from '../components/Dialog';
import { useFileOperations } from '../components/FileOperations';
import { LibraryGatePage } from '../components/LibraryGatePage';
import { MediaGrid, MediaGridSkeleton } from '../components/media/MediaGrid';
import { gridMetrics } from '../components/media/layout';
import { useVisibleFiles } from '../components/media/useHideCompleted';
import { EmptyState, ErrorState, LibraryOfflineNotice } from '../components/States';
import { downloadUrl } from '../components/media';
import { Icon } from '../components/ui/Icon';
import { Menu, type MenuAnchor, type MenuEntry } from '../components/ui/Menu';
import { useToast } from '../components/ui/Toast';
import { useUploadActions } from '../components/upload/UploadProvider';
import { ViewerModal } from '../components/ViewerModal';
import { greeting } from '../lib/dates';
import { isIndexing, useIndexStatus } from '../components/app-shell/useIndexStatus';
import './HomePage.css';

const RECENT_LIMIT = 40;
const NO_FILES: FileSummary[] = [];
const NO_IN_PROGRESS: InProgressFile[] = [];
/** Home shows a taste, not the library: two rows. */
const HOME_ROWS = 2;
const PHOTO_GAP = 4;
/** How often Home re-reads itself: fast while empty (a scan may be filling it). */

/** How many quotes/collections to fetch for their Home sections. */
const WRITING_LIMIT = 6;
const PINNED_COLLECTIONS_LIMIT = 8;

/** Width assumed before the first measurement (and in jsdom). */
const FALLBACK_WIDTH = 1024;

const SECTION_LABEL: Record<HomeSectionId, string> = {
  'continue-reading': 'Continue reading',
  'recently-opened': 'Recently opened',
  'recently-added': 'Recently added',
  favorites: 'Favorites',
  'pinned-collections': 'Pinned collections',
  'recent-quotes': 'Recent quotes',
};

const DEFAULT_LAYOUT: HomeLayoutSection[] = (Object.keys(SECTION_LABEL) as HomeSectionId[]).map(
  (id) => ({ id, visible: true }),
);

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
  inProgress: InProgressFile[];
  favorites: FileSummary[];
  pinnedCollections: Collection[];
  recentQuotes: Quote[];
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
      const [recent, recentlyOpened, inProgress, favorites, collections, recentQuotes] =
        await Promise.all([
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
          listInProgress(id)
            .then((r) => r.files ?? [])
            .catch(() => [] as InProgressFile[]),
          listFavorites(id)
            .then((r) => r.files ?? [])
            .catch(() => [] as FileSummary[]),
          listCollections(id)
            .then((r) => r.collections ?? [])
            .catch(() => [] as Collection[]),
          listLibraryQuotes(id, { limit: WRITING_LIMIT })
            .then((r) => r.quotes ?? [])
            .catch(() => [] as Quote[]),
        ]);
      return {
        recent,
        recentlyOpened,
        inProgress,
        favorites,
        pinnedCollections: collections.filter((c) => c.pinned).slice(0, PINNED_COLLECTIONS_LIMIT),
        recentQuotes,
      };
    }, []),
  );

  const layout = useLibraryResource<HomeLayoutSection[]>(
    useCallback(async (id: string) => (await getHomeLayout(id)).sections ?? DEFAULT_LAYOUT, []),
  );
  const [customizing, setCustomizing] = useState(false);

  const ops = useFileOperations(libraryId, home.reload);
  // Right-click on any tile: the same short menu in every section.
  const [menu, setMenu] = useState<{ anchor: MenuAnchor; items: MenuEntry[] } | null>(null);
  const { openViewer, openProperties, openTags } = ops;
  const toast = useToast();
  const reloadHome = home.reload;
  const setCompleted = useCallback(
    (file: FileSummary, value: boolean) => {
      (value
        ? setReadingStatus(libraryId, file.id, 'completed')
        : clearReadingStatus(libraryId, file.id)
      )
        .then(() => {
          toast({
            message: value ? 'Marked as completed' : 'Marked as not completed',
            tone: 'success',
          });
          reloadHome();
        })
        .catch(() => toast({ message: "Couldn't update the file", tone: 'error' }));
    },
    [libraryId, reloadHome, toast],
  );
  const openMenu = useCallback(
    (file: FileSummary, x: number, y: number) =>
      setMenu({
        anchor: { x, y },
        items: [
          { id: 'open', label: 'Open', icon: 'file', onSelect: () => openViewer(file) },
          {
            id: 'download',
            label: 'Download',
            icon: 'download',
            href: downloadUrl(libraryId, file),
          },
          {
            id: 'tags',
            label: 'Tags…',
            icon: 'tag',
            onSelect: () => openTags(file),
            testId: 'menu-tags',
          },
          {
            id: 'complete',
            label: file.completed ? 'Mark as not completed' : 'Mark as completed',
            icon: 'check',
            onSelect: () => setCompleted(file, !file.completed),
            testId: 'menu-complete',
          },
          {
            id: 'properties',
            label: 'Properties',
            icon: 'settings',
            onSelect: () => openProperties(file),
            testId: 'menu-properties',
          },
        ],
      }),
    [libraryId, openViewer, openProperties, openTags, setCompleted],
  );
  const data = home.data;
  // With "Hide completed" on, every section leaves out what the user has
  // finished. Emptiness below still goes by the unfiltered list: a library
  // whose recent files are all completed is not "empty".
  const visibleRecent = useVisibleFiles(data?.recent ?? NO_FILES);
  const visibleOpened = useVisibleFiles(data?.recentlyOpened ?? NO_FILES);
  const visibleInProgress = useVisibleFiles(data?.inProgress ?? NO_IN_PROGRESS);
  const visibleFavorites = useVisibleFiles(data?.favorites ?? NO_FILES);

  // A freshly added library is empty until its first scan has run. The shell
  // already watches the index and makes this page re-read as files arrive;
  // here it only decides whether to say "scanning" rather than "empty".
  const empty = data !== null && data.recent.length === 0;
  const indexStatus = useIndexStatus(libraryId, empty, false);
  const scanning = empty && isIndexing(indexStatus);

  // Home stays current without a timer of its own: the shell polls the
  // library's index status revision and announces a change whenever it moves
  // (a scan, a new cover, an edit from another device), which re-reads this
  // page behind the data on screen. It used to re-fetch all six sections every
  // 15 seconds whether or not anything had changed.

  const [recentRef, recentWidth] = useWidth();
  const photoColumns = gridMetrics(recentWidth, PHOTO_GAP).columns;
  const recent = visibleRecent.slice(0, cleanRows(visibleRecent.length, photoColumns));

  const [openedRef, openedWidth] = useWidth();
  const openedColumns = gridMetrics(openedWidth, PHOTO_GAP).columns;
  const recentlyOpened = visibleOpened.slice(0, cleanRows(visibleOpened.length, openedColumns));

  const [inProgressRef, inProgressWidth] = useWidth();
  const inProgressColumns = gridMetrics(inProgressWidth, PHOTO_GAP).columns;
  const inProgressFiles = visibleInProgress.slice(
    0,
    cleanRows(visibleInProgress.length, inProgressColumns),
  );
  const inProgressMap = new Map(inProgressFiles.map((f) => [f.id, f.percent]));

  const [favoritesRef, favoritesWidth] = useWidth();
  const favoritesColumns = gridMetrics(favoritesWidth, PHOTO_GAP).columns;
  const favoriteFiles = visibleFavorites.slice(
    0,
    cleanRows(visibleFavorites.length, favoritesColumns),
  );

  const quoteFileIds = (data?.recentQuotes ?? []).map((q) => q.file_id);
  const quoteLabels = useFileLabels(libraryId, quoteFileIds);

  const name = user?.username;

  const nothingYet = data !== null && !scanning && data.recent.length === 0;

  const sectionOrder = layout.data ?? DEFAULT_LAYOUT;
  const visible = (id: HomeSectionId) => sectionOrder.find((s) => s.id === id)?.visible ?? true;

  const sectionNode = (id: HomeSectionId) => {
    if (!data || !visible(id)) return null;
    switch (id) {
      case 'continue-reading':
        return inProgressFiles.length > 0 ? (
          <section key={id} className="section" aria-labelledby="home-progress-title">
            <div className="section-head">
              <h2 id="home-progress-title" className="section-title">
                Continue reading
              </h2>
            </div>
            <div data-testid="home-in-progress" ref={inProgressRef}>
              <MediaGrid
                libraryId={libraryId}
                files={inProgressFiles}
                progress={inProgressMap}
                onOpen={ops.openViewer}
                onContextMenu={openMenu}
                label="Books in progress"
                testId="home-in-progress-grid"
              />
            </div>
          </section>
        ) : null;
      case 'recently-opened':
        return recentlyOpened.length > 0 ? (
          <section key={id} className="section" aria-labelledby="home-opened-title">
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
                onContextMenu={openMenu}
                label="Recently opened files"
                testId="home-recently-opened-grid"
              />
            </div>
          </section>
        ) : null;
      case 'recently-added':
        return recent.length > 0 ? (
          <section key={id} className="section" aria-labelledby="home-recent-title">
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
                onContextMenu={openMenu}
                label="Recently added files"
                testId="home-recent-grid"
              />
            </div>
          </section>
        ) : null;
      case 'favorites':
        return favoriteFiles.length > 0 ? (
          <section key={id} className="section" aria-labelledby="home-favorites-title">
            <div className="section-head">
              <h2 id="home-favorites-title" className="section-title">
                Favorites
              </h2>
              <Link to="/favorites" className="link-button">
                All favorites
                <Icon name="chevron-right" size={16} />
              </Link>
            </div>
            <div data-testid="home-favorites" ref={favoritesRef}>
              <MediaGrid
                libraryId={libraryId}
                files={favoriteFiles}
                onOpen={ops.openViewer}
                onContextMenu={openMenu}
                label="Favorite files"
                testId="home-favorites-grid"
              />
            </div>
          </section>
        ) : null;
      case 'pinned-collections':
        return data.pinnedCollections.length > 0 ? (
          <section key={id} className="section" aria-labelledby="home-collections-title">
            <div className="section-head">
              <h2 id="home-collections-title" className="section-title">
                Pinned collections
              </h2>
              <Link to="/collections" className="link-button">
                All collections
                <Icon name="chevron-right" size={16} />
              </Link>
            </div>
            <ul className="home-collections" data-testid="home-pinned-collections">
              {data.pinnedCollections.map((c) => (
                <li key={c.id}>
                  <Link to={`/collections?open=${c.id}`} className="home-collection-card">
                    <Icon name="pin" size={16} />
                    <span className="home-collection-name">{c.name}</span>
                    <span className="home-collection-count">
                      {c.item_count} {c.item_count === 1 ? 'item' : 'items'}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        ) : null;
      case 'recent-quotes':
        return data.recentQuotes.length > 0 ? (
          <section key={id} className="section" aria-labelledby="home-quotes-title">
            <div className="section-head">
              <h2 id="home-quotes-title" className="section-title">
                Recent quotes
              </h2>
              <Link to="/quotes" className="link-button">
                All quotes
                <Icon name="chevron-right" size={16} />
              </Link>
            </div>
            <ul className="home-writing-grid" data-testid="home-recent-quotes">
              {data.recentQuotes.map((q) => (
                <li key={q.id}>
                  <Link to="/quotes" className="home-quote-card">
                    <Icon name="quote" size={16} />
                    <span className="home-quote-text">{q.text}</span>
                    <span className="home-quote-source">{quoteLabels[q.file_id]?.title}</span>
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        ) : null;
      default:
        return null;
    }
  };

  return (
    <main className="page home">
      <div className="home-hello-row">
        <Greeting name={name} />
        <div className="home-hello-actions">
          {!offline && <QuickImport library={library} />}
          <button
            type="button"
            className="button ghost-button"
            onClick={() => setCustomizing(true)}
            data-testid="home-customize-button"
          >
            <Icon name="settings" size={16} />
            Customize
          </button>
        </div>
      </div>

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

      {data && sectionOrder.map((s) => sectionNode(s.id))}

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
      {menu && (
        <Menu
          anchor={menu.anchor}
          items={menu.items}
          onClose={() => setMenu(null)}
          label="File actions"
        />
      )}
      {ops.dialogs}

      {customizing && (
        <CustomizeHomeDialog
          sections={sectionOrder}
          onCancel={() => setCustomizing(false)}
          onSave={async (next) => {
            await setHomeLayout(next, libraryId);
            layout.reload();
            setCustomizing(false);
          }}
        />
      )}
    </main>
  );
}

/** A button that opens the OS file picker and hands the result to the
 * existing upload provider — the same mechanism the top bar's Upload button
 * uses, surfaced here for a quick add-books-and-go without leaving Home. */
function QuickImport({ library }: { library: Library }) {
  const { enqueue } = useUploadActions();
  const fileRef = useRef<HTMLInputElement | null>(null);
  return (
    <>
      <button
        type="button"
        className="button ghost-button"
        onClick={() => fileRef.current?.click()}
        title={`Add books to ${library.name}`}
        data-testid="home-quick-import"
      >
        <Icon name="upload" size={16} />
        Add books
      </button>
      <input
        ref={fileRef}
        type="file"
        multiple
        hidden
        aria-label={`Add books to ${library.name}`}
        onChange={(event) => {
          const files = Array.from(event.target.files ?? []);
          if (files.length) enqueue(library.id, files, '');
          event.target.value = '';
        }}
      />
    </>
  );
}

/** Reorder and show/hide Home's sections. Order changes by swapping a
 * section with its neighbor — plain buttons, not drag-and-drop, so it works
 * the same with a mouse, a keyboard, or a finger. */
function CustomizeHomeDialog({
  sections,
  onCancel,
  onSave,
}: {
  sections: HomeLayoutSection[];
  onCancel: () => void;
  onSave: (sections: HomeLayoutSection[]) => void | Promise<void>;
}) {
  const [draft, setDraft] = useState(sections);
  const [saving, setSaving] = useState(false);

  const move = (index: number, dir: -1 | 1) => {
    setDraft((prev) => {
      const next = prev.slice();
      const target = index + dir;
      if (target < 0 || target >= next.length) return prev;
      [next[index], next[target]] = [
        next[target] as HomeLayoutSection,
        next[index] as HomeLayoutSection,
      ];
      return next;
    });
  };

  const toggle = (index: number) => {
    setDraft((prev) => prev.map((s, i) => (i === index ? { ...s, visible: !s.visible } : s)));
  };

  return (
    <Dialog
      open
      size="medium"
      title="Customize Home"
      onClose={onCancel}
      dismissible={!saving}
      testId="home-customize-dialog"
      footer={
        <>
          <button type="button" className="button" onClick={onCancel} disabled={saving}>
            Cancel
          </button>
          <button
            type="button"
            className="button primary-button"
            disabled={saving}
            onClick={() => {
              setSaving(true);
              Promise.resolve(onSave(draft)).finally(() => setSaving(false));
            }}
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
        </>
      }
    >
      <p className="muted">Choose which sections show, and in what order.</p>
      <ul className="home-customize-list">
        {draft.map((s, i) => (
          <li key={s.id} className="home-customize-row">
            <label className="home-customize-label">
              <input
                type="checkbox"
                checked={s.visible}
                onChange={() => toggle(i)}
                data-testid={`home-customize-toggle-${s.id}`}
              />
              {SECTION_LABEL[s.id]}
            </label>
            <span className="home-customize-move">
              <button
                type="button"
                className="icon-button"
                aria-label={`Move ${SECTION_LABEL[s.id]} up`}
                disabled={i === 0}
                onClick={() => move(i, -1)}
              >
                <Icon name="chevron-left" size={16} />
              </button>
              <button
                type="button"
                className="icon-button"
                aria-label={`Move ${SECTION_LABEL[s.id]} down`}
                disabled={i === draft.length - 1}
                onClick={() => move(i, 1)}
              >
                <Icon name="chevron-right" size={16} />
              </button>
            </span>
          </li>
        ))}
      </ul>
    </Dialog>
  );
}

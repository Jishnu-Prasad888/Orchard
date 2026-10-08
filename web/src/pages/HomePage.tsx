/**
 * Home — a living view of the library, not a dashboard.
 *
 * What a person opens Cairn for is their photos, so the page leads with the
 * most recent ones and then the things they have made from them: albums,
 * memories, the people in them. Sections only appear when they have something
 * to show; there are no counters or status tiles — the library's status lives
 * in the sidebar and Settings.
 *
 * Every section is one small request, so the page stays quick on a Raspberry
 * Pi with a hundred thousand files.
 */

import { useCallback, useEffect, useLayoutEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';

import { useAuth } from '../auth/authContext';
import { mlWorthAsking } from '../api/mlSwitch';
import { notifyLibraryChanged } from '../api/libraryEvents';
import { useLibraryResource } from '../api/resources';
import { faceImageUrl, listAlbums, listFiles, listMemories, listPeople } from '../api/queries';
import type { Album, FileSummary, Library, Memory, Person } from '../api/types';
import { AlbumCard } from '../components/albums/AlbumCard';
import { useFileOperations } from '../components/FileOperations';
import { LibraryGatePage } from '../components/LibraryGatePage';
import { MediaGrid, MediaGridSkeleton } from '../components/media/MediaGrid';
import { gridMetrics } from '../components/media/layout';
import { EmptyState, ErrorState, LibraryOfflineNotice } from '../components/States';
import { Icon } from '../components/ui/Icon';
import { ViewerModal } from '../components/ViewerModal';
import { formatDate, greeting } from '../lib/dates';
import { extractExcerpt } from '../lib/markdown';
import { thumbnailUrl } from '../components/media';
import { isIndexing, useIndexStatus } from '../components/app-shell/useIndexStatus';
import './HomePage.css';

/** Fetched per kind; only the rows that fit are shown (see `cleanRows`). */
const RECENT_LIMIT = 40;
const MEMORY_LIMIT = 40;
/** Home shows a taste, not the library: two rows of each. */
const HOME_ROWS = 2;
const MEMORY_MIN_WIDTH = 240;
const MEMORY_GAP = 12;
const PHOTO_GAP = 4;
/** How often Home re-reads itself: fast while empty (a scan may be filling it). */
const HOME_EMPTY_REFRESH_MS = 3000;
const HOME_REFRESH_MS = 15000;

/** Width assumed before the first measurement (and in jsdom). */
const FALLBACK_WIDTH = 1024;

/**
 * Track an element's width so a section can know how many columns it has. A
 * callback ref, because the sections mount only once their data has loaded.
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
  albums: Album[];
  memories: Memory[];
  /** Null when the server has no face support. */
  people: Person[] | null;
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
  const navigate = useNavigate();
  const libraryId = library.id;
  const offline = library.status === 'offline';

  const home = useLibraryResource<HomeData>(
    useCallback(async (id: string) => {
      const [recent, albums, memories, people] = await Promise.all([
        // The API filters by one type, so photos and videos are fetched
        // separately and merged newest first.
        Promise.all(
          (['photo', 'video'] as const).map((type) =>
            listFiles(id, {
              type,
              recursive: true,
              sort: 'mod_time',
              order: 'desc',
              limit: RECENT_LIMIT,
            })
              .then((r) => r.files ?? [])
              .catch(() => [] as FileSummary[]),
          ),
        ).then((lists) =>
          lists
            .flat()
            .filter((f, i, all) => all.findIndex((x) => x.id === f.id) === i)
            .sort((a, b) => b.mod_time.localeCompare(a.mod_time))
            .slice(0, RECENT_LIMIT),
        ),
        listAlbums(id)
          .then((r) => r.albums ?? [])
          .catch(() => [] as Album[]),
        listMemories(id, { limit: MEMORY_LIMIT })
          .then((r) => r.memories ?? [])
          .catch(() => [] as Memory[]),
        // A server without face support has no people route; that is "not
        // available", not "nobody".
        // While ML is off there are no people to ask about (the route is 503).
        mlWorthAsking().then((ask) =>
          ask
            ? listPeople(id)
                .then((r) => r.people ?? [])
                .catch(() => null)
            : null,
        ),
      ]);
      return {
        recent,
        albums: [...albums].sort((a, b) => b.updated_at.localeCompare(a.updated_at)).slice(0, 6),
        memories: [...memories].sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
        people: people
          ? [...people]
              .filter((p) => p.name)
              .sort((a, b) => b.face_count - a.face_count)
              .slice(0, 8)
          : null,
      };
    }, []),
  );

  const ops = useFileOperations(libraryId, home.reload);
  const data = home.data;

  // A freshly added library is empty until its first scan has run. The shell
  // already watches the index and makes this page re-read as files arrive;
  // here it only decides whether to say "scanning" rather than "empty".
  const empty = data !== null && data.recent.length === 0 && data.albums.length === 0;
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
  const [memoriesRef, memoriesWidth] = useWidth();
  const photoColumns = gridMetrics(recentWidth, PHOTO_GAP).columns;
  const memoryColumns = Math.max(
    1,
    Math.floor((memoriesWidth + MEMORY_GAP) / (MEMORY_MIN_WIDTH + MEMORY_GAP)),
  );
  const recent = data ? data.recent.slice(0, cleanRows(data.recent.length, photoColumns)) : [];
  const memoryList = data
    ? data.memories.slice(0, cleanRows(data.memories.length, memoryColumns))
    : [];
  const name = user?.username;

  const nothingYet =
    data !== null &&
    !scanning &&
    data.recent.length === 0 &&
    data.albums.length === 0 &&
    data.memories.length === 0;

  return (
    <main className="page home">
      <Greeting name={name} />

      {offline && <LibraryOfflineNotice library={library} />}
      {home.error && (
        <ErrorState message={home.error} onRetry={home.reload} title="Couldn't load your library" />
      )}

      {home.loading && (
        <section className="section" aria-label="Loading recent photos">
          <div className="skeleton home-skeleton-title" />
          <MediaGridSkeleton count={12} />
        </section>
      )}

      {scanning && !home.loading && (
        <section className="section home-scanning" aria-label="Scanning library" aria-live="polite">
          <div className="home-scanning-inner">
            <span className="home-scanning-spinner" aria-hidden="true" />
            <p>Scanning your library… photos will appear here shortly.</p>
          </div>
          <MediaGridSkeleton count={12} />
        </section>
      )}

      {nothingYet && !offline && (
        <EmptyState
          title="Your library is waking up"
          testId="home-no-photos"
          icon="photo"
          action={
            <Link className="button primary-button" to="/files">
              Browse files
            </Link>
          }
        >
          <p>
            Once Cairn has looked through {library.name}, your newest photos will gather here. You
            can keep using Cairn while it works.
          </p>
        </EmptyState>
      )}

      {data && data.recent.length > 0 && (
        <section className="section" aria-labelledby="home-recent-title">
          <div className="section-head">
            <h2 id="home-recent-title" className="section-title">
              Recently added
            </h2>
            <Link to="/photos" className="link-button">
              All photos
              <Icon name="chevron-right" size={16} />
            </Link>
          </div>
          <div data-testid="home-recent" ref={recentRef}>
            <MediaGrid
              libraryId={libraryId}
              files={recent}
              onOpen={ops.openViewer}
              label="Recently added photos"
              testId="home-recent-grid"
            />
          </div>
        </section>
      )}

      {data && data.people && data.people.length > 0 && (
        <section className="section" aria-labelledby="home-people-title">
          <div className="section-head">
            <h2 id="home-people-title" className="section-title">
              People
            </h2>
            <Link to="/people" className="link-button">
              All people
              <Icon name="chevron-right" size={16} />
            </Link>
          </div>
          <ul className="home-people" data-testid="home-people">
            {data.people.map((person) => (
              <li key={person.id}>
                <Link to={`/search?person=${person.id}`} className="home-person">
                  <span className="home-person-face">
                    {person.cover_face_id ? (
                      <img
                        src={faceImageUrl(libraryId, person.cover_face_id)}
                        alt=""
                        loading="lazy"
                      />
                    ) : person.cover_file_id ? (
                      <img
                        src={thumbnailUrl(libraryId, { id: person.cover_file_id })}
                        alt=""
                        loading="lazy"
                      />
                    ) : (
                      <Icon name="person" size={28} />
                    )}
                  </span>
                  <span className="home-person-name">{person.name}</span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}

      {data && data.albums.length > 0 && (
        <section className="section" aria-labelledby="home-albums-title">
          <div className="section-head">
            <h2 id="home-albums-title" className="section-title">
              Albums
            </h2>
            <Link to="/albums" className="link-button">
              All albums
              <Icon name="chevron-right" size={16} />
            </Link>
          </div>
          <ul className="home-albums" data-testid="home-albums">
            {data.albums.map((album) => (
              <li key={album.id}>
                <AlbumCard
                  album={album}
                  libraryId={libraryId}
                  onOpen={(a) => navigate(`/albums/${a.id}`)}
                />
              </li>
            ))}
          </ul>
        </section>
      )}

      {data && (data.memories.length > 0 || data.recent.length > 0) && (
        <section className="section" aria-labelledby="home-memories-title">
          <div className="section-head">
            <h2 id="home-memories-title" className="section-title">
              Memories
            </h2>
            <Link to="/memories" className="link-button">
              All memories
              <Icon name="chevron-right" size={16} />
            </Link>
          </div>
          {data.memories.length === 0 ? (
            <div className="home-memory-invite" data-testid="home-no-memories">
              <p>Write down a day, a trip, or a story behind a photo.</p>
              <Link className="button" to="/memories">
                <Icon name="edit" />
                Write a memory
              </Link>
            </div>
          ) : (
            <ul
              className="home-memories"
              data-testid="home-memories"
              ref={memoriesRef}
              style={{ gridTemplateColumns: `repeat(${memoryColumns}, minmax(0, 1fr))` }}
            >
              {memoryList.map((memory) => (
                <li key={memory.id}>
                  <Link to={`/memories/${memory.id}`} className="home-memory">
                    <span className="home-memory-date">
                      {formatDate(memory.memory_date ?? memory.updated_at)}
                    </span>
                    <span className="home-memory-title">{memory.title || 'Untitled'}</span>
                    <span className="home-memory-excerpt">{extractExcerpt(memory.body, 120)}</span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
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

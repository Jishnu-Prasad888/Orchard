/**
 * Albums — curated sets of photos from anywhere in the library.
 *
 * Two views on one route: the grid of albums (`/albums`), where the covers do
 * the talking, and one album (`/albums/:id`), which is a photo grid with the
 * album's own actions. An album never owns its files: deleting it, or removing
 * a photo from it, leaves the files exactly where they are on disk.
 */

import { useCallback, useState } from 'react';
import { matchPath, useLocation, useNavigate } from 'react-router-dom';

import { useAuth } from '../auth/authContext';
import { useLibraryGate } from '../api/libraries';
import { useLibraryResource } from '../api/resources';
import {
  createAlbum,
  deleteAlbum,
  listAlbumFiles,
  listAlbums,
  removeAlbumFile,
  updateAlbum,
} from '../api/queries';
import type { Album, FileSummary, Library } from '../api/types';
import { AddFilesDialog } from '../components/albums/AddFilesDialog';
import { AlbumCard } from '../components/albums/AlbumCard';
import { ConfirmDialog, PromptDialog } from '../components/Dialog';
import { useFileOperations } from '../components/FileOperations';
import { MediaGrid, MediaGridSkeleton } from '../components/media/MediaGrid';
import { SelectionToolbar } from '../components/media/SelectionToolbar';
import { Timeline } from '../components/media/Timeline';
import { useFavorites } from '../components/media/useFavorites';
import { useMediaActions } from '../components/media/useMediaActions';
import { useSelection } from '../components/media/useSelection';
import {
  EmptyState,
  ErrorState,
  LibraryOfflineNotice,
  NoLibrariesState,
  PageHeader,
} from '../components/States';
import { Icon } from '../components/ui/Icon';
import { Menu, type MenuAnchor, type MenuEntry, useMenuButton } from '../components/ui/Menu';
import { useToast } from '../components/ui/Toast';
import { ViewerModal } from '../components/ViewerModal';
import { formatDate } from '../lib/dates';
import { readTimelineOpen, storeTimelineOpen } from '../lib/timelinePreference';
import './AlbumsPage.css';

/** The album id in `/albums/:id`, read from the path so the page works in or out of `<Routes>`. */
function useAlbumIdFromPath(): string | null {
  const { pathname } = useLocation();
  return matchPath('/albums/:albumId', pathname)?.params.albumId ?? null;
}

export default function AlbumsPage() {
  const gate = useLibraryGate();
  const { user } = useAuth();
  const albumId = useAlbumIdFromPath();

  if (gate.kind === 'loading') {
    return (
      <main className="page albums-page">
        <PageHeader title="Albums" />
        <div className="album-grid-skeleton" aria-hidden="true">
          {Array.from({ length: 8 }, (_, i) => (
            <span key={i} className="skeleton" />
          ))}
        </div>
      </main>
    );
  }
  if (gate.kind === 'error') {
    return (
      <main className="page albums-page">
        <PageHeader title="Albums" />
        <ErrorState message={gate.message} title="Couldn't load your libraries" />
      </main>
    );
  }
  if (gate.kind === 'empty') {
    return (
      <main className="page albums-page">
        <PageHeader title="Albums" />
        <NoLibrariesState isAdmin={user?.role === 'admin'} />
      </main>
    );
  }
  return <Albums library={gate.library} albumId={albumId} />;
}

function Albums({ library, albumId }: { library: Library; albumId: string | null }) {
  const libraryId = library.id;
  const navigate = useNavigate();
  const toast = useToast();
  const [creating, setCreating] = useState(false);
  const [busy, setBusy] = useState(false);
  const [promptError, setPromptError] = useState<string | null>(null);

  const albums = useLibraryResource(
    useCallback(async (id: string) => (await listAlbums(id)).albums ?? [], []),
  );

  const offline = library.status === 'offline';
  const active = albumId ? (albums.data?.find((a) => a.id === albumId) ?? null) : null;

  if (albumId) {
    if (albums.data && !active) {
      return (
        <main className="page albums-page">
          <EmptyState title="Album not found" icon="album" testId="album-missing">
            <p>It may have been deleted, or it belongs to another library.</p>
          </EmptyState>
          <div className="albums-center">
            <button type="button" className="button" onClick={() => navigate('/albums')}>
              Back to albums
            </button>
          </div>
        </main>
      );
    }
    if (active) {
      return (
        <AlbumDetail
          key={active.id}
          library={library}
          album={active}
          onBack={() => navigate('/albums')}
          onChanged={albums.reload}
        />
      );
    }
  }

  return (
    <main className="page albums-page">
      <PageHeader
        title="Albums"
        subtitle={
          albums.data && albums.data.length > 0
            ? `${albums.data.length} ${albums.data.length === 1 ? 'album' : 'albums'}`
            : undefined
        }
        controls={
          !offline && (
            <button
              type="button"
              className="button primary-button"
              onClick={() => {
                setPromptError(null);
                setCreating(true);
              }}
              data-testid="new-album-button"
            >
              <Icon name="plus" />
              New album
            </button>
          )
        }
      />

      {offline && <LibraryOfflineNotice library={library} />}
      {albums.error && (
        <ErrorState message={albums.error} onRetry={albums.reload} title="Couldn't load albums" />
      )}

      {albums.loading && (
        <div className="album-grid-skeleton" aria-hidden="true">
          {Array.from({ length: 8 }, (_, i) => (
            <span key={i} className="skeleton" />
          ))}
        </div>
      )}

      {albums.data !== null && albums.data.length === 0 && (
        <EmptyState
          title="No albums yet"
          testId="albums-empty"
          icon="album"
          action={
            !offline && (
              <button
                type="button"
                className="button primary-button"
                onClick={() => setCreating(true)}
              >
                Create an album
              </button>
            )
          }
        >
          <p>Gather photos from any folder into an album — a trip, a year, a person.</p>
        </EmptyState>
      )}

      {albums.data !== null && albums.data.length > 0 && (
        <ul className="album-grid" data-testid="albums-grid" aria-label="Albums">
          {albums.data.map((album) => (
            <li key={album.id}>
              <AlbumCard
                album={album}
                libraryId={libraryId}
                onOpen={(a) => navigate(`/albums/${a.id}`)}
              />
            </li>
          ))}
        </ul>
      )}

      <PromptDialog
        open={creating}
        title="New album"
        label="Album name"
        placeholder="Summer 2026"
        busy={busy}
        error={promptError}
        onCancel={() => setCreating(false)}
        onConfirm={(name) => {
          setBusy(true);
          setPromptError(null);
          createAlbum(libraryId, name)
            .then((resp) => {
              setCreating(false);
              albums.reload();
              toast({ message: `Created ${name}`, tone: 'success' });
              navigate(`/albums/${resp.album.id}`);
            })
            .catch((e: unknown) => setPromptError(e instanceof Error ? e.message : String(e)))
            .finally(() => setBusy(false));
        }}
        testId="new-album-dialog"
      />
    </main>
  );
}

type Editing = 'rename' | 'describe' | 'delete' | null;

function AlbumDetail({
  library,
  album,
  onBack,
  onChanged,
}: {
  library: Library;
  album: Album;
  onBack: () => void;
  onChanged: () => void;
}) {
  const libraryId = library.id;
  const toast = useToast();
  const more = useMenuButton();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<Editing>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [menu, setMenu] = useState<{ anchor: MenuAnchor; items: MenuEntry[] } | null>(null);
  const [timelineOpen, setTimelineOpenState] = useState(readTimelineOpen);
  const setTimelineOpen = (open: boolean) => {
    setTimelineOpenState(open);
    storeTimelineOpen(open);
  };

  const files = useLibraryResource(
    useCallback(
      (id: string) => listAlbumFiles(id, album.id).then((r) => r.files ?? []),
      [album.id],
    ),
    [album.id],
  );
  const list = files.data ?? EMPTY;

  const reloadAll = useCallback(() => {
    files.reload();
    onChanged();
  }, [files, onChanged]);

  const favorites = useFavorites(libraryId);
  const selection = useSelection(list, album.id);
  const ops = useFileOperations(libraryId, reloadAll);

  const remove = async (targets: FileSummary[]) => {
    const results = await Promise.allSettled(
      targets.map((f) => removeAlbumFile(libraryId, album.id, f.id)),
    );
    const failed = results.filter((r) => r.status === 'rejected').length;
    if (failed) {
      toast({ message: `Couldn't remove ${failed} from the album`, tone: 'error' });
    } else {
      toast({
        message:
          targets.length === 1 ? 'Removed from album' : `${targets.length} removed from album`,
        tone: 'success',
      });
    }
    selection.clear();
    reloadAll();
  };

  const setCover = async (file: FileSummary) => {
    try {
      await updateAlbum(libraryId, album.id, { cover_file_id: file.id });
      toast({ message: 'Album cover updated', tone: 'success' });
      selection.clear();
      onChanged();
    } catch (e: unknown) {
      toast({ message: e instanceof Error ? e.message : "Couldn't set the cover", tone: 'error' });
    }
  };

  const actions = useMediaActions({
    libraryId,
    selection,
    favorites,
    ops,
    onChanged: reloadAll,
    allow: { album: false },
    extra: [
      ...(selection.size === 1
        ? [
            {
              id: 'cover',
              label: 'Use as album cover',
              icon: 'photo' as const,
              onClick: () => void setCover(selection.selectedFiles[0]!),
            },
          ]
        : []),
      {
        id: 'remove',
        label: 'Remove from album',
        icon: 'minus' as const,
        onClick: () => void remove(selection.selectedFiles),
        testId: 'selection-remove',
      },
    ],
    extraMenu: (file) => [
      {
        id: 'cover',
        label: 'Use as album cover',
        icon: 'photo',
        onSelect: () => void setCover(file),
      },
      {
        id: 'remove',
        label: 'Remove from album',
        icon: 'minus',
        onSelect: () => void remove([file]),
        testId: `remove-${file.id}`,
      },
    ],
  });

  const runEdit = async (action: () => Promise<unknown>, after: () => void) => {
    setBusy(true);
    setError(null);
    try {
      await action();
      after();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const offline = library.status === 'offline';
  const count = files.data?.length ?? album.file_count;

  return (
    <main className="page albums-page" data-testid="album-detail">
      {selection.active && (
        <SelectionToolbar
          count={selection.size}
          total={list.length}
          actions={actions.selectionActions}
          onClear={selection.clear}
          onSelectAll={selection.selectAll}
          onDeleteKey={actions.onDeleteKey}
        />
      )}

      <header className="album-head">
        <button type="button" className="icon-button" onClick={onBack} aria-label="Back to albums">
          <Icon name="arrow-left" />
        </button>
        <div className="album-head-titles">
          <h1>{album.name}</h1>
          <p className="album-head-meta">
            {count !== undefined && (
              <span>
                {count} {count === 1 ? 'item' : 'items'}
              </span>
            )}
            <span>Updated {formatDate(album.updated_at)}</span>
          </p>
          {album.description && <p className="album-head-description">{album.description}</p>}
        </div>
        {!offline && (
          <div className="header-controls">
            <button
              type="button"
              className="button"
              onClick={() => setAdding(true)}
              data-testid="add-files-button"
            >
              <Icon name="plus" />
              Add media
            </button>
            <button
              type="button"
              className="icon-button"
              aria-label={timelineOpen ? 'Hide timeline' : 'Show timeline'}
              aria-pressed={timelineOpen}
              title={timelineOpen ? 'Hide timeline' : 'Show timeline'}
              onClick={() => setTimelineOpen(!timelineOpen)}
              data-testid="toggle-timeline"
            >
              <Icon name="ruler" />
            </button>
            <button
              type="button"
              className="icon-button"
              aria-label="Album options"
              aria-haspopup="menu"
              aria-expanded={more.open}
              onClick={more.toggle}
            >
              <Icon name="more" />
            </button>
          </div>
        )}
      </header>

      {offline && <LibraryOfflineNotice library={library} />}
      {files.error && (
        <ErrorState message={files.error} onRetry={files.reload} title="Couldn't load this album" />
      )}
      {files.loading && <MediaGridSkeleton count={12} />}

      {files.data !== null && files.data.length === 0 && (
        <EmptyState
          title="This album is empty"
          testId="album-empty"
          icon="album"
          action={
            !offline && (
              <button
                type="button"
                className="button primary-button"
                onClick={() => setAdding(true)}
              >
                Add media
              </button>
            )
          }
        >
          <p>Bring in photos and files from anywhere in the library.</p>
        </EmptyState>
      )}

      {list.length > 0 && (
        <MediaGrid
          libraryId={libraryId}
          files={list}
          onOpen={ops.openViewer}
          selection={selection}
          favorites={favorites.ids}
          onContextMenu={(file, x, y) =>
            setMenu({ anchor: { x, y }, items: actions.menuFor(file) })
          }
          label={`Album: ${album.name}`}
          testId="file-grid"
        />
      )}

      {ops.viewer && (
        <ViewerModal
          libraryId={libraryId}
          file={ops.viewer}
          siblings={list}
          onNavigate={ops.openViewer}
          onChanged={reloadAll}
          onRequestAction={ops.requestAction}
          onClose={ops.closeViewer}
        />
      )}
      {ops.dialogs}
      {actions.dialogs}

      {menu && (
        <Menu
          anchor={menu.anchor}
          items={menu.items}
          onClose={() => setMenu(null)}
          label="Photo actions"
        />
      )}

      {more.anchor && (
        <Menu
          anchor={more.anchor}
          align="end"
          label="Album options"
          onClose={more.close}
          items={[
            {
              id: 'rename',
              label: 'Rename album',
              icon: 'edit',
              onSelect: () => setEditing('rename'),
            },
            {
              id: 'describe',
              label: album.description ? 'Edit description' : 'Add a description',
              icon: 'document',
              onSelect: () => setEditing('describe'),
            },
            ...(album.cover_file_id
              ? [
                  {
                    id: 'cover',
                    label: 'Use the first photo as cover',
                    icon: 'photo' as const,
                    onSelect: () =>
                      void updateAlbum(libraryId, album.id, { cover_file_id: '' }).then(onChanged),
                  },
                ]
              : []),
            'separator',
            {
              id: 'delete',
              label: 'Delete album',
              icon: 'trash',
              danger: true,
              onSelect: () => setEditing('delete'),
              testId: 'delete-album',
            },
          ]}
        />
      )}

      {adding && (
        <AddFilesDialog
          libraryId={libraryId}
          album={album}
          alreadyIn={list.map((f) => f.id)}
          onClose={() => setAdding(false)}
          onAdded={reloadAll}
        />
      )}

      <PromptDialog
        open={editing === 'rename'}
        title="Rename album"
        label="Album name"
        initialValue={album.name}
        busy={busy}
        error={error}
        onCancel={() => setEditing(null)}
        onConfirm={(name) =>
          void runEdit(
            () => updateAlbum(libraryId, album.id, { name }),
            () => {
              setEditing(null);
              onChanged();
            },
          )
        }
        testId="rename-album-dialog"
      />

      <PromptDialog
        open={editing === 'describe'}
        title="Album description"
        label="Description"
        initialValue={album.description ?? ''}
        multiline
        busy={busy}
        error={error}
        onCancel={() => setEditing(null)}
        onConfirm={(description) =>
          void runEdit(
            () => updateAlbum(libraryId, album.id, { description }),
            () => {
              setEditing(null);
              onChanged();
            },
          )
        }
        testId="describe-album-dialog"
      />

      <ConfirmDialog
        open={editing === 'delete'}
        title={`Delete album “${album.name}”?`}
        destructive
        confirmLabel="Delete album"
        busy={busy}
        error={error}
        message={
          <p>
            Only the album is deleted. The photos and files in it stay exactly where they are in
            your library.
          </p>
        }
        onCancel={() => setEditing(null)}
        onConfirm={() =>
          void runEdit(
            () => deleteAlbum(libraryId, album.id),
            () => {
              setEditing(null);
              toast({ message: `Deleted ${album.name}`, tone: 'success' });
              onChanged();
              onBack();
            },
          )
        }
        testId="delete-album-dialog"
      />

      {!offline && timelineOpen && <Timeline files={list} label={`Album: ${album.name}`} />}
    </main>
  );
}

const EMPTY: FileSummary[] = [];

/**
 * Favorites — everything the signed-in user has starred. Per person: nobody
 * else sees your favorites.
 */

import { useCallback, useState } from 'react';

import { useLibraryResource } from '../api/resources';
import { listFavorites } from '../api/queries';
import type { Library } from '../api/types';
import { useFileOperations } from '../components/FileOperations';
import { LibraryGatePage } from '../components/LibraryGatePage';
import { MediaGrid, MediaGridSkeleton } from '../components/media/MediaGrid';
import { SelectionToolbar } from '../components/media/SelectionToolbar';
import { useFavorites } from '../components/media/useFavorites';
import { useMediaActions } from '../components/media/useMediaActions';
import { useSelection } from '../components/media/useSelection';
import { EmptyState, ErrorState, LibraryOfflineNotice, PageHeader } from '../components/States';
import { Menu, type MenuAnchor, type MenuEntry } from '../components/ui/Menu';
import { ViewerModal } from '../components/ViewerModal';

export default function FavoritesPage() {
  return (
    <LibraryGatePage title="Favorites">
      {(library) => <Favorites library={library} />}
    </LibraryGatePage>
  );
}

function Favorites({ library }: { library: Library }) {
  const libraryId = library.id;
  const [menu, setMenu] = useState<{ anchor: MenuAnchor; items: MenuEntry[] } | null>(null);
  const favorites = useLibraryResource(
    useCallback(async (id: string) => (await listFavorites(id)).files ?? [], []),
  );
  const files = favorites.data ?? [];

  const starred = useFavorites(libraryId, files.length);
  const selection = useSelection(files, libraryId);
  const ops = useFileOperations(libraryId, favorites.reload);
  const actions = useMediaActions({
    libraryId,
    selection,
    favorites: starred,
    ops,
    onChanged: favorites.reload,
  });

  const count = favorites.data?.length ?? 0;

  return (
    <main className="page media-page">
      {selection.active && (
        <SelectionToolbar
          count={selection.size}
          total={files.length}
          actions={actions.selectionActions}
          onClear={selection.clear}
          onSelectAll={selection.selectAll}
          onDeleteKey={actions.onDeleteKey}
        />
      )}
      <PageHeader
        title="Favorites"
        subtitle={count > 0 ? `${count} ${count === 1 ? 'favorite' : 'favorites'}` : undefined}
      />

      {library.status === 'offline' && <LibraryOfflineNotice library={library} />}
      {favorites.error && (
        <ErrorState
          message={favorites.error}
          onRetry={favorites.reload}
          title="Couldn't load your favorites"
        />
      )}
      {favorites.loading && <MediaGridSkeleton />}

      {favorites.data !== null && favorites.data.length === 0 && (
        <EmptyState title="No favorites yet" testId="favorites-empty" icon="star">
          <p>Tap the star on any photo to keep it here. Only you see your favorites.</p>
        </EmptyState>
      )}

      {files.length > 0 && (
        <MediaGrid
          libraryId={libraryId}
          files={files}
          onOpen={ops.openViewer}
          selection={selection}
          favorites={starred.ids}
          onContextMenu={(file, x, y) =>
            setMenu({ anchor: { x, y }, items: actions.menuFor(file) })
          }
          label="Favorites"
          testId="file-grid"
        />
      )}

      {ops.viewer && (
        <ViewerModal
          libraryId={libraryId}
          file={ops.viewer}
          siblings={files}
          onNavigate={ops.openViewer}
          onChanged={favorites.reload}
          onRequestAction={ops.requestAction}
          onClose={ops.closeViewer}
        />
      )}
      {menu && (
        <Menu
          anchor={menu.anchor}
          items={menu.items}
          onClose={() => setMenu(null)}
          label="Photo actions"
        />
      )}
      {ops.dialogs}
      {actions.dialogs}
    </main>
  );
}

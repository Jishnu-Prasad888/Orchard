/**
 * What can be done to media, from a selection or a single tile's menu.
 *
 * Every grid offers the same core actions — download, add to an album,
 * favorite, rename/move/copy, move to trash — and pages add their own (Trash
 * adds Restore, an album adds Remove). Keeping them here means the wording,
 * the confirmations, and the toasts are identical everywhere.
 *
 * Harmless actions run immediately and confirm with a toast. Moving to the
 * trash asks first, because it changes what is on disk, and says where the
 * files went.
 */

import { type ReactElement, useCallback, useState } from 'react';

import { softDeleteFile } from '../../api/queries';
import type { FileSummary } from '../../api/types';
import { AlbumPickerDialog } from '../albums/AlbumPickerDialog';
import { ConfirmDialog } from '../Dialog';
import type { FileOperations } from '../FileOperations';
import { downloadUrl } from '../media';
import type { MenuEntry, MenuItem } from '../ui/Menu';
import { withDangerLast } from '../ui/Menu';
import { useToast } from '../ui/Toast';
import type { SelectionAction } from './SelectionToolbar';
import type { Favorites } from './useFavorites';
import type { Selection } from './useSelection';

export interface MediaActionOptions {
  libraryId: string;
  selection: Selection;
  favorites?: Favorites | undefined;
  /** The page's file operations (viewer and rename/move/copy dialogs). */
  ops?: FileOperations | undefined;
  /** Something changed on the server; reload the listing. */
  onChanged: () => void;
  /** Which of the standard actions this page offers. Defaults to all. */
  allow?: Partial<Record<'download' | 'album' | 'favorite' | 'trash' | 'edit', boolean>>;
  /** Page-specific selection actions, shown before the standard ones. */
  extra?: SelectionAction[];
  /** Page-specific items for a single file's menu. */
  extraMenu?: (file: FileSummary) => MenuItem[];
  /** "Show in folder" target, when the page is not already the folder view. */
  onShowInFolder?: ((file: FileSummary) => void) | undefined;
}

export interface MediaActions {
  selectionActions: SelectionAction[];
  menuFor: (file: FileSummary) => MenuEntry[];
  /** Run by the Delete key while a selection exists. */
  onDeleteKey: (() => void) | undefined;
  dialogs: ReactElement;
}

/**
 * Save several files one after another. Browsers allow a burst of downloads
 * only when they are spaced out, and may ask the person once to allow them.
 */
function downloadAll(libraryId: string, files: FileSummary[]) {
  files.forEach((file, i) => {
    setTimeout(() => {
      const a = document.createElement('a');
      a.href = downloadUrl(libraryId, file);
      a.download = file.name;
      a.rel = 'noreferrer';
      document.body.appendChild(a);
      a.click();
      a.remove();
    }, i * 350);
  });
}

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

export function useMediaActions({
  libraryId,
  selection,
  favorites,
  ops,
  onChanged,
  allow = {},
  extra = [],
  extraMenu,
  onShowInFolder,
}: MediaActionOptions): MediaActions {
  const toast = useToast();
  const [trashing, setTrashing] = useState<FileSummary[] | null>(null);
  const [albumTargets, setAlbumTargets] = useState<FileSummary[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const can = {
    download: allow.download ?? true,
    album: allow.album ?? true,
    favorite: (allow.favorite ?? true) && favorites !== undefined,
    trash: allow.trash ?? true,
    edit: (allow.edit ?? true) && ops !== undefined,
  };

  const setFavorite = useCallback(
    async (files: FileSummary[], value: boolean) => {
      if (!favorites) return;
      const failed = await favorites.set(
        files.map((f) => f.id),
        value,
      );
      if (failed) {
        toast({
          message: `Couldn't update ${plural(failed, 'favorite', 'favorites')}`,
          tone: 'error',
        });
      } else {
        toast({
          message: value
            ? files.length === 1
              ? 'Added to favorites'
              : `${files.length} added to favorites`
            : files.length === 1
              ? 'Removed from favorites'
              : `${files.length} removed from favorites`,
          tone: 'success',
        });
      }
    },
    [favorites, toast],
  );

  const confirmTrash = async () => {
    if (!trashing) return;
    setBusy(true);
    setError(null);
    const results = await Promise.allSettled(
      trashing.map((f) => softDeleteFile(libraryId, f.rel_path, f.id)),
    );
    const failed = results.filter((r) => r.status === 'rejected').length;
    setBusy(false);
    if (failed > 0) {
      setError(
        `Moved ${results.length - failed} of ${trashing.length} to the trash. The rest could not be moved — they may have changed on disk.`,
      );
      onChanged();
      return;
    }
    toast({
      message: trashing.length === 1 ? 'Moved to trash' : `${trashing.length} items moved to trash`,
      tone: 'success',
    });
    setTrashing(null);
    selection.clear();
    onChanged();
  };

  const files = selection.selectedFiles;
  const allFavorite =
    favorites !== undefined && files.length > 0 && files.every((f) => favorites.has(f.id));

  const selectionActions: SelectionAction[] = [
    ...extra,
    ...(can.download
      ? [
          files.length === 1
            ? {
                id: 'download',
                label: 'Download',
                icon: 'download' as const,
                href: downloadUrl(libraryId, files[0]!),
              }
            : {
                id: 'download',
                label: 'Download',
                icon: 'download' as const,
                onClick: () => downloadAll(libraryId, files),
              },
        ]
      : []),
    ...(can.album
      ? [
          {
            id: 'album',
            label: 'Add to album',
            icon: 'album' as const,
            onClick: () => setAlbumTargets(files),
            testId: 'selection-album',
          },
        ]
      : []),
    ...(can.favorite
      ? [
          {
            id: 'favorite',
            label: allFavorite ? 'Remove from favorites' : 'Add to favorites',
            icon: 'star' as const,
            onClick: () => void setFavorite(files, !allFavorite),
            testId: 'selection-favorite',
          },
        ]
      : []),
    ...(can.trash
      ? [
          {
            id: 'trash',
            label: 'Move to trash',
            icon: 'trash' as const,
            danger: true,
            onClick: () => setTrashing(files),
            testId: 'selection-trash',
          },
        ]
      : []),
  ];

  const menuFor = (file: FileSummary): MenuEntry[] => {
    const favorite = favorites?.has(file.id) ?? false;
    const items: MenuItem[] = [];
    if (ops)
      items.push({
        id: 'open',
        label: 'Open',
        icon: 'photo',
        onSelect: () => ops.openViewer(file),
      });
    if (can.download) {
      items.push({
        id: 'download',
        label: 'Download',
        icon: 'download',
        href: downloadUrl(libraryId, file),
      });
    }
    if (can.album) {
      items.push({
        id: 'album',
        label: 'Add to album',
        icon: 'album',
        onSelect: () => setAlbumTargets([file]),
      });
    }
    if (can.favorite) {
      items.push({
        id: 'favorite',
        label: favorite ? 'Remove from favorites' : 'Add to favorites',
        icon: 'star',
        onSelect: () => void setFavorite([file], !favorite),
      });
    }
    if (onShowInFolder) {
      items.push({
        id: 'folder',
        label: 'Show in folder',
        icon: 'folder',
        onSelect: () => onShowInFolder(file),
      });
    }
    if (can.edit && ops) {
      items.push(
        {
          id: 'rename',
          label: 'Rename',
          icon: 'edit',
          onSelect: () => ops.requestAction('rename', file),
        },
        {
          id: 'move',
          label: 'Move',
          icon: 'folder-move',
          onSelect: () => ops.requestAction('move', file),
        },
        {
          id: 'copy',
          label: 'Copy',
          icon: 'copy',
          onSelect: () => ops.requestAction('copy', file),
        },
      );
    }
    items.push(...(extraMenu?.(file) ?? []));
    if (can.trash) {
      items.push({
        id: 'trash',
        label: 'Move to trash',
        icon: 'trash',
        danger: true,
        onSelect: () => setTrashing([file]),
      });
    }
    return withDangerLast(items);
  };

  const dialogs = (
    <>
      <ConfirmDialog
        open={trashing !== null}
        title={
          trashing && trashing.length === 1
            ? `Move “${trashing[0]!.name}” to trash?`
            : `Move ${trashing?.length ?? 0} items to trash?`
        }
        destructive
        confirmLabel="Move to trash"
        busy={busy}
        error={error}
        message={
          <p>
            {trashing && trashing.length === 1 ? 'It' : 'They'} will be moved to the trash, where{' '}
            {trashing && trashing.length === 1 ? 'it stays' : 'they stay'} until you restore or
            delete {trashing && trashing.length === 1 ? 'it' : 'them'}.
          </p>
        }
        onCancel={() => {
          setTrashing(null);
          setError(null);
        }}
        onConfirm={() => void confirmTrash()}
        testId="trash-many-dialog"
      />
      <AlbumPickerDialog
        open={albumTargets !== null}
        libraryId={libraryId}
        fileIds={(albumTargets ?? []).map((f) => f.id)}
        onClose={() => setAlbumTargets(null)}
        onDone={({ album, added, failed }) => {
          setAlbumTargets(null);
          if (failed) {
            toast({
              message: `Added ${added} to ${album.name}; ${failed} couldn't be added`,
              tone: 'error',
            });
          } else {
            toast({
              message: added === 1 ? `Added to ${album.name}` : `${added} added to ${album.name}`,
              tone: 'success',
            });
            selection.clear();
          }
          onChanged();
        }}
      />
    </>
  );

  return {
    selectionActions,
    menuFor,
    onDeleteKey:
      can.trash && selection.active ? () => setTrashing(selection.selectedFiles) : undefined,
    dialogs,
  };
}

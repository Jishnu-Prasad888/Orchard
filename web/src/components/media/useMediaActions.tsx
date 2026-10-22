/**
 * What can be done to media, from a selection or a single tile's menu.
 *
 * Every grid offers the same core actions — download, favorite, mark as
 * completed, rename/move/copy, move to trash — and pages add their own (Trash adds Restore). Keeping
 * them here means the wording, the confirmations, and the toasts are
 * identical everywhere.
 *
 * Harmless actions run immediately and confirm with a toast. Moving to the
 * trash asks first, because it changes what is on disk, and says where the
 * files went.
 */

import { type ReactElement, useCallback, useState } from 'react';

import { useLibraries } from '../../api/libraries';
import { softDeleteFile } from '../../api/queries';
import type { FileSummary } from '../../api/types';
import { ConfirmDialog } from '../Dialog';
import type { FileOperations } from '../FileOperations';
import { downloadUrl } from '../media';
import type { MenuEntry, MenuItem } from '../ui/Menu';
import { menuGroups } from '../ui/Menu';
import { useToast } from '../ui/Toast';
import { FileMenuHeader } from './FileMenuHeader';
import type { SelectionAction } from './SelectionToolbar';
import type { Favorites } from './useFavorites';
import { useHideCompleted } from './useHideCompleted';
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
  allow?: Partial<Record<'download' | 'favorite' | 'complete' | 'trash' | 'edit', boolean>>;
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
  /** The file's name and kind, for the menu's header. */
  headerFor: (file: FileSummary) => ReactElement;
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
  const { libraries } = useLibraries();
  // Only someone who may create links is offered "Share…".
  const canShare = libraries.find((l) => l.id === libraryId)?.can_share ?? false;
  const [trashing, setTrashing] = useState<FileSummary[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const can = {
    download: allow.download ?? true,
    favorite: (allow.favorite ?? true) && favorites !== undefined,
    complete: allow.complete ?? true,
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

  const { markCompleted } = useHideCompleted();
  const setCompleted = useCallback(
    async (files: FileSummary[], value: boolean) => {
      // No reload: the mark is laid over the listing (see useHideCompleted),
      // so only this tile changes.
      const failed = await markCompleted(libraryId, files, value);
      if (failed) {
        toast({ message: `Couldn't update ${plural(failed, 'file', 'files')}`, tone: 'error' });
      } else {
        const what = files.length === 1 ? 'Marked' : `${files.length} marked`;
        toast({
          message: value ? `${what} as completed` : `${what} as not completed`,
          tone: 'success',
        });
      }
    },
    [libraryId, markCompleted, toast],
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
  const allCompleted = files.length > 0 && files.every((f) => f.completed);

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
    ...(can.complete
      ? [
          {
            id: 'complete',
            label: allCompleted ? 'Mark as not completed' : 'Mark as completed',
            icon: 'check-circle' as const,
            onClick: () => {
              void setCompleted(files, !allCompleted);
              selection.clear();
            },
            testId: 'selection-complete',
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

  /**
   * A file's right-click menu, in small groups so it reads at a glance:
   * open; the two toggles (shown with a check mark rather than as "Add
   * to…"/"Remove from…" pairs); labels; organizing; details; and, last and
   * apart, the destructive action.
   */
  const menuFor = (file: FileSummary): MenuEntry[] => {
    const open: MenuItem[] = ops
      ? [{ id: 'open', label: 'Open', icon: 'file', onSelect: () => ops.openViewer(file) }]
      : [];

    const toggles: MenuItem[] = [];
    if (can.favorite) {
      const favorite = favorites?.has(file.id) ?? false;
      toggles.push({
        id: 'favorite',
        label: 'Favorite',
        icon: 'star',
        checked: favorite,
        onSelect: () => void setFavorite([file], !favorite),
        testId: 'menu-favorite',
      });
    }
    if (can.complete) {
      toggles.push({
        id: 'complete',
        label: 'Completed',
        icon: 'check-circle',
        checked: Boolean(file.completed),
        onSelect: () => void setCompleted([file], !file.completed),
        testId: 'menu-complete',
      });
    }

    const labels: MenuItem[] =
      can.edit && ops
        ? [
            {
              id: 'tags',
              label: 'Tags…',
              icon: 'tag',
              onSelect: () => ops.openTags(file),
              testId: 'menu-tags',
            },
            {
              id: 'collections',
              label: 'Collections…',
              icon: 'archive',
              onSelect: () => ops.openCollections(file),
              testId: 'menu-collections',
            },
          ]
        : [];

    const organize: MenuItem[] = [];
    if (can.edit && ops) {
      organize.push(
        {
          id: 'rename',
          label: 'Rename…',
          icon: 'edit',
          onSelect: () => ops.requestAction('rename', file),
        },
        {
          id: 'move',
          label: 'Move to…',
          icon: 'folder-move',
          onSelect: () => ops.requestAction('move', file),
        },
        {
          id: 'copy',
          label: 'Copy to…',
          icon: 'copy',
          onSelect: () => ops.requestAction('copy', file),
        },
      );
    }
    if (onShowInFolder) {
      organize.push({
        id: 'folder',
        label: 'Show in folder',
        icon: 'folder',
        onSelect: () => onShowInFolder(file),
      });
    }

    const extras = extraMenu?.(file) ?? [];

    const details: MenuItem[] = [];
    if (can.download) {
      details.push({
        id: 'download',
        label: 'Download',
        icon: 'download',
        href: downloadUrl(libraryId, file),
      });
    }
    if (ops && canShare) {
      details.push({
        id: 'share',
        label: 'Share…',
        icon: 'share',
        onSelect: () => ops.openShare(file),
        testId: 'menu-share',
      });
    }
    if (ops) {
      details.push({
        id: 'properties',
        label: 'Properties',
        icon: 'info',
        onSelect: () => ops.openProperties(file),
        testId: 'menu-properties',
      });
    }

    const danger: MenuItem[] = [
      ...extras.filter((item) => item.danger),
      ...(can.trash
        ? [
            {
              id: 'trash',
              label: 'Move to trash',
              icon: 'trash' as const,
              danger: true,
              onSelect: () => setTrashing([file]),
            },
          ]
        : []),
    ];

    return menuGroups(
      open,
      toggles,
      labels,
      organize,
      extras.filter((item) => !item.danger),
      details,
      danger,
    );
  };

  /** What a file's menu says above its actions: which file this is. */
  const headerFor = (file: FileSummary): ReactElement => <FileMenuHeader file={file} />;

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
    </>
  );

  return {
    selectionActions,
    menuFor,
    headerFor,
    onDeleteKey:
      can.trash && selection.active ? () => setTrashing(selection.selectedFiles) : undefined,
    dialogs,
  };
}

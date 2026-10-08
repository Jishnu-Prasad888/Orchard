/**
 * Trash — what was removed, waiting to be restored or erased.
 *
 * A file in the trash is still on disk, so restoring is always safe and puts
 * it back exactly where it was. Deleting forever erases the bytes; it is the
 * one irreversible action in Cairn, so it is the only one that asks twice and
 * says so plainly.
 */

import { useCallback, useState } from 'react';

import { useLibraryResource } from '../api/resources';
import { deleteForever, listTrash, restoreFile } from '../api/queries';
import type { FileSummary, Library } from '../api/types';
import { ConfirmDialog } from '../components/Dialog';
import { useFileOperations } from '../components/FileOperations';
import { LibraryGatePage } from '../components/LibraryGatePage';
import { MediaGrid, MediaGridSkeleton } from '../components/media/MediaGrid';
import { SelectionToolbar } from '../components/media/SelectionToolbar';
import { useSelection } from '../components/media/useSelection';
import { EmptyState, ErrorState, LibraryOfflineNotice, PageHeader } from '../components/States';
import { Icon } from '../components/ui/Icon';
import { Menu, type MenuAnchor } from '../components/ui/Menu';
import { useToast } from '../components/ui/Toast';
import { ViewerModal } from '../components/ViewerModal';
import './TrashPage.css';

export default function TrashPage() {
  return (
    <LibraryGatePage title="Trash">{(library) => <Trash library={library} />}</LibraryGatePage>
  );
}

const plural = (n: number) => `${n} ${n === 1 ? 'item' : 'items'}`;

function Trash({ library }: { library: Library }) {
  const libraryId = library.id;
  const toast = useToast();
  const [erasing, setErasing] = useState<FileSummary[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [menu, setMenu] = useState<{ anchor: MenuAnchor; file: FileSummary } | null>(null);

  const trash = useLibraryResource(
    useCallback(async (id: string) => (await listTrash(id)).files ?? [], []),
  );
  const files = trash.data ?? EMPTY;
  const selection = useSelection(files, libraryId);

  // The viewer here offers "Delete forever" in place of a second soft delete.
  const ops = useFileOperations(libraryId, trash.reload, { permanent: true });

  const restore = async (targets: FileSummary[]) => {
    setBusy(true);
    const results = await Promise.allSettled(targets.map((f) => restoreFile(libraryId, f.id)));
    setBusy(false);
    const failed = results.filter((r) => r.status === 'rejected').length;
    if (failed) {
      toast({
        message: `Couldn't restore ${plural(failed)}. Something may already be at the original location.`,
        tone: 'error',
      });
    } else {
      toast({
        message: targets.length === 1 ? 'Restored' : `${plural(targets.length)} restored`,
        tone: 'success',
      });
    }
    selection.clear();
    trash.reload();
  };

  const erase = async () => {
    if (!erasing) return;
    setBusy(true);
    setError(null);
    const results = await Promise.allSettled(erasing.map((f) => deleteForever(libraryId, f.id)));
    setBusy(false);
    const failed = results.filter((r) => r.status === 'rejected').length;
    if (failed) {
      const reason = results.find(
        (r): r is PromiseRejectedResult => r.status === 'rejected',
      )?.reason;
      setError(
        reason instanceof Error ? reason.message : `${plural(failed)} could not be deleted.`,
      );
      trash.reload();
      return;
    }
    toast({
      message:
        erasing.length === 1 ? 'Deleted forever' : `${plural(erasing.length)} deleted forever`,
      tone: 'success',
    });
    setErasing(null);
    selection.clear();
    trash.reload();
  };

  return (
    <main className="page media-page trash-page">
      {selection.active && (
        <SelectionToolbar
          count={selection.size}
          total={files.length}
          onClear={selection.clear}
          onSelectAll={selection.selectAll}
          onDeleteKey={() => setErasing(selection.selectedFiles)}
          actions={[
            {
              id: 'restore',
              label: 'Restore',
              icon: 'restore',
              onClick: () => void restore(selection.selectedFiles),
              disabled: busy,
              testId: 'selection-restore',
            },
            {
              id: 'erase',
              label: 'Delete forever',
              icon: 'trash',
              danger: true,
              onClick: () => setErasing(selection.selectedFiles),
              testId: 'selection-erase',
            },
          ]}
        />
      )}

      <PageHeader
        title="Trash"
        subtitle={files.length > 0 ? plural(files.length) : undefined}
        controls={
          files.length > 0 && (
            <>
              <button
                type="button"
                className="button"
                onClick={() => void restore(files)}
                disabled={busy}
              >
                <Icon name="restore" />
                Restore all
              </button>
              <button
                type="button"
                className="button danger-button"
                onClick={() => setErasing(files)}
                disabled={busy}
                data-testid="empty-trash"
              >
                Empty trash
              </button>
            </>
          )
        }
      />

      {library.status === 'offline' && <LibraryOfflineNotice library={library} />}

      {files.length > 0 && (
        <p className="trash-note">
          <Icon name="info" size={18} />
          Items here are still on disk. Restore puts them back where they were; Delete forever
          erases them.
        </p>
      )}

      {trash.error && (
        <ErrorState message={trash.error} onRetry={trash.reload} title="Couldn't load the trash" />
      )}
      {trash.loading && <MediaGridSkeleton />}

      {trash.data !== null && trash.data.length === 0 && (
        <EmptyState title="Trash is empty" testId="trash-empty" icon="trash">
          <p>
            When you remove something, it waits here until you restore it or delete it for good.
          </p>
        </EmptyState>
      )}

      {files.length > 0 && (
        <MediaGrid
          libraryId={libraryId}
          files={files}
          onOpen={ops.openViewer}
          selection={selection}
          onContextMenu={(file, x, y) => setMenu({ anchor: { x, y }, file })}
          label="Trash"
          testId="trash-list"
        />
      )}

      {menu && (
        <Menu
          anchor={menu.anchor}
          label="Trash actions"
          onClose={() => setMenu(null)}
          items={[
            {
              id: 'restore',
              label: 'Restore',
              icon: 'restore',
              onSelect: () => void restore([menu.file]),
              testId: `restore-${menu.file.id}`,
            },
            'separator',
            {
              id: 'erase',
              label: 'Delete forever',
              icon: 'trash',
              danger: true,
              onSelect: () => setErasing([menu.file]),
              testId: `erase-${menu.file.id}`,
            },
          ]}
        />
      )}

      {ops.viewer && (
        <ViewerModal
          libraryId={libraryId}
          file={ops.viewer}
          siblings={files}
          onNavigate={ops.openViewer}
          onChanged={trash.reload}
          onRequestAction={ops.requestAction}
          onClose={ops.closeViewer}
        />
      )}
      {ops.dialogs}

      <ConfirmDialog
        open={erasing !== null}
        title={
          erasing && erasing.length === 1
            ? `Delete “${erasing[0]!.name}” forever?`
            : `Delete ${plural(erasing?.length ?? 0)} forever?`
        }
        destructive
        confirmLabel="Delete forever"
        busy={busy}
        error={error}
        message={
          <p>
            {erasing && erasing.length === 1 ? 'It' : 'They'} will be erased from disk.{' '}
            <strong>This cannot be undone.</strong>
          </p>
        }
        onCancel={() => {
          setErasing(null);
          setError(null);
        }}
        onConfirm={() => void erase()}
        testId="erase-dialog"
      />
    </main>
  );
}

const EMPTY: FileSummary[] = [];

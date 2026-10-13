/**
 * Collections — personal, user-curated groupings of files.
 *
 * Unlike Tags (a shared library vocabulary), a collection belongs to the
 * person who made it; nobody else sees it. The flat list and the drill-in
 * detail view intentionally mirror TagsPage, since the two features share
 * almost the same shape (a named group, and everything carrying it).
 */

import { useCallback, useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';

import { useAuth } from '../auth/authContext';
import { useLibraryGate } from '../api/libraries';
import { useLibraryResource } from '../api/resources';
import {
  createCollection,
  deleteCollection,
  listCollectionFiles,
  listCollections,
  removeCollectionFile,
  updateCollection,
} from '../api/queries';
import type { Collection, FileSummary } from '../api/types';
import { ConfirmDialog, PromptDialog } from '../components/Dialog';
import { MediaGrid } from '../components/media/MediaGrid';
import { SelectionToolbar } from '../components/media/SelectionToolbar';
import { useMediaActions } from '../components/media/useMediaActions';
import { useSelection } from '../components/media/useSelection';
import { useFileOperations } from '../components/FileOperations';
import {
  EmptyState,
  ErrorState,
  LibraryOfflineNotice,
  LoadingState,
  NoLibrariesState,
  PageHeader,
} from '../components/States';
import { Icon } from '../components/ui/Icon';
import { ViewerModal } from '../components/ViewerModal';
import './CollectionsPage.css';

export default function CollectionsPage() {
  const gate = useLibraryGate();
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  const [params, setParams] = useSearchParams();
  const openID = params.get('open');
  const [active, setActive] = useState<Collection | null>(null);
  const [creating, setCreating] = useState(false);
  const [renaming, setRenaming] = useState<Collection | null>(null);
  const [deleting, setDeleting] = useState<Collection | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [promptError, setPromptError] = useState<string | null>(null);

  const collections = useLibraryResource(
    useCallback(async (libraryId: string) => (await listCollections(libraryId)).collections ?? [], []),
  );

  // A deep link from elsewhere (e.g. a pinned collection on Home) opens a
  // specific collection once the list has loaded.
  useEffect(() => {
    if (!openID || active || !collections.data) return;
    const match = collections.data.find((c) => c.id === openID);
    if (match) setActive(match);
  }, [openID, active, collections.data]);

  const clearOpenParam = useCallback(() => {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete('open');
        return next;
      },
      { replace: true },
    );
  }, [setParams]);

  const activeId = active?.id;
  const files = useLibraryResource(
    useCallback(
      (libraryId) =>
        activeId ? listCollectionFiles(libraryId, activeId).then((r) => r.files ?? []) : Promise.resolve([]),
      [activeId],
    ),
    [activeId],
    active !== null,
  );

  const ops = useFileOperations(gate.kind === 'ready' ? gate.libraryId : '', () => {
    files.reload();
    collections.reload();
  });
  const collectionFiles = files.data ?? NO_FILES;
  const selection = useSelection(collectionFiles, active?.id ?? '');
  const libraryIdOrEmpty = gate.kind === 'ready' ? gate.libraryId : '';
  const removeFromCollection = async (targets: FileSummary[]) => {
    if (!active) return;
    await run(() =>
      Promise.all(targets.map((f) => removeCollectionFile(libraryIdOrEmpty, active.id, f.id))),
    );
    selection.clear();
  };
  const actions = useMediaActions({
    libraryId: libraryIdOrEmpty,
    selection,
    ops,
    onChanged: files.reload,
    extra: active
      ? [
          {
            id: 'remove-from-collection',
            label: `Remove from “${active.name}”`,
            icon: 'close',
            onClick: () => void removeFromCollection(selection.selectedFiles),
            testId: 'selection-remove-from-collection',
          },
        ]
      : [],
  });

  const run = useCallback(
    async (action: () => Promise<unknown>, after?: () => void) => {
      setBusy(true);
      setActionError(null);
      try {
        await action();
        after?.();
        collections.reload();
        files.reload();
      } catch (e: unknown) {
        setActionError(e instanceof Error ? e.message : String(e));
      } finally {
        setBusy(false);
      }
    },
    [files, collections],
  );

  const togglePin = (c: Collection) => {
    void run(() => updateCollection(gate.kind === 'ready' ? gate.libraryId : '', c.id, { pinned: !c.pinned }));
  };

  if (gate.kind === 'loading') {
    return (
      <main className="page collections-page">
        <LoadingState label="Loading libraries…" />
      </main>
    );
  }

  const header = (
    <PageHeader
      title="Collections"
      subtitle="Your own curated shelves. Nobody else using this library sees them."
      controls={
        <>
          <button
            type="button"
            className="button primary-button"
            onClick={() => {
              setPromptError(null);
              setCreating(true);
            }}
            disabled={gate.kind !== 'ready'}
            data-testid="new-collection-button"
          >
            New collection
          </button>
        </>
      }
    />
  );

  if (gate.kind === 'error') {
    return (
      <main className="page collections-page">
        {header}
        <ErrorState message={gate.message} onRetry={collections.reload} />
      </main>
    );
  }

  if (gate.kind === 'empty') {
    return (
      <main className="page collections-page">
        {header}
        <NoLibrariesState isAdmin={isAdmin} />
      </main>
    );
  }

  const library = gate.library;
  const offline = library.status === 'offline';

  return (
    <main className="page collections-page">
      {header}

      {offline && <LibraryOfflineNotice library={library} />}
      {actionError && (
        <p className="error-text" role="alert">
          {actionError}
        </p>
      )}

      {collections.error && <ErrorState message={collections.error} onRetry={collections.reload} />}

      {active ? (
        <section className="collection-detail" data-testid="collection-detail">
          <div className="collection-detail-header">
            <button
              type="button"
              className="button"
              onClick={() => {
                setActive(null);
                clearOpenParam();
              }}
            >
              ← Collections
            </button>
            <h2>{active.name}</h2>
            <button
              type="button"
              className="button"
              onClick={() => {
                setPromptError(null);
                setRenaming(active);
              }}
            >
              Rename
            </button>
            <button type="button" className="button danger-button" onClick={() => setDeleting(active)}>
              Delete collection
            </button>
          </div>

          {files.loading && <LoadingState />}
          {files.error && <ErrorState message={files.error} onRetry={files.reload} />}

          {files.data !== null && files.data.length === 0 && (
            <EmptyState title="Nothing in this collection yet" testId="collection-files-empty">
              <p className="muted">
                Add files to this collection from the viewer's Collections tab.
              </p>
            </EmptyState>
          )}

          {files.data !== null && files.data.length > 0 && (
            <MediaGrid
              libraryId={gate.libraryId}
              files={files.data}
              onOpen={ops.openViewer}
              selection={selection}
              label={active.name}
              testId="file-grid"
            />
          )}

          {ops.viewer && (
            <ViewerModal
              libraryId={gate.libraryId}
              file={ops.viewer}
              siblings={files.data ?? []}
              onNavigate={ops.openViewer}
              onChanged={() => {
                files.reload();
                collections.reload();
              }}
              onRequestAction={ops.requestAction}
              onClose={ops.closeViewer}
            />
          )}
          {ops.dialogs}
          {actions.dialogs}
          {selection.active && (
            <SelectionToolbar
              count={selection.size}
              total={collectionFiles.length}
              actions={actions.selectionActions}
              onClear={selection.clear}
              onSelectAll={selection.selectAll}
              onDeleteKey={actions.onDeleteKey}
            />
          )}
        </section>
      ) : (
        <>
          {collections.loading && <LoadingState />}
          {collections.data !== null && collections.data.length === 0 && (
            <EmptyState title="No collections yet" testId="collections-empty">
              <p className="muted">
                Collections are personal shelves you curate yourself — a book can be in many at
                once. Create one, then add books to it from the viewer.
              </p>
            </EmptyState>
          )}
          {collections.data !== null && collections.data.length > 0 && (
            <ul className="collection-grid" data-testid="collections-grid" aria-label="Collections">
              {collections.data.map((c) => (
                <li key={c.id}>
                  <div className="collection-card">
                    <button
                      type="button"
                      className="collection-card-open"
                      onClick={() => setActive(c)}
                      data-testid={`open-collection-${c.name}`}
                    >
                      <span className="collection-card-name">{c.name}</span>
                      <span className="collection-card-count">
                        {c.item_count} {c.item_count === 1 ? 'item' : 'items'}
                      </span>
                    </button>
                    <button
                      type="button"
                      className={c.pinned ? 'icon-button active' : 'icon-button'}
                      aria-label={c.pinned ? `Unpin ${c.name}` : `Pin ${c.name}`}
                      onClick={() => togglePin(c)}
                      data-testid={`pin-collection-${c.name}`}
                    >
                      <Icon name="pin" size={16} />
                    </button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </>
      )}

      <PromptDialog
        open={creating}
        title="New collection"
        label="Collection name"
        placeholder="Summer reading"
        busy={busy}
        error={promptError}
        onCancel={() => setCreating(false)}
        onConfirm={(name) => {
          setBusy(true);
          setPromptError(null);
          createCollection(gate.libraryId, name)
            .then(() => {
              setCreating(false);
              collections.reload();
            })
            .catch((e: unknown) => setPromptError(e instanceof Error ? e.message : String(e)))
            .finally(() => setBusy(false));
        }}
        testId="new-collection-dialog"
      />

      <PromptDialog
        open={renaming !== null}
        title="Rename collection"
        label="Collection name"
        initialValue={renaming?.name ?? ''}
        busy={busy}
        error={promptError}
        onCancel={() => setRenaming(null)}
        onConfirm={(name) => {
          if (!renaming) return;
          const target = renaming;
          setBusy(true);
          setPromptError(null);
          updateCollection(gate.libraryId, target.id, { name })
            .then((resp) => {
              setRenaming(null);
              setActive(resp.collection);
              collections.reload();
            })
            .catch((e: unknown) => setPromptError(e instanceof Error ? e.message : String(e)))
            .finally(() => setBusy(false));
        }}
        testId="rename-collection-dialog"
      />

      <ConfirmDialog
        open={deleting !== null}
        title={`Delete “${deleting?.name ?? ''}”?`}
        destructive
        confirmLabel="Delete collection"
        busy={busy}
        error={actionError}
        message={
          <p>
            The collection is removed. The files themselves are not affected — they stay in your
            library and in any other collection they belong to.
          </p>
        }
        onCancel={() => setDeleting(null)}
        onConfirm={() => {
          if (!deleting) return;
          const target = deleting;
          void run(
            () => deleteCollection(gate.libraryId, target.id),
            () => {
              setActive(null);
              clearOpenParam();
              setDeleting(null);
            },
          );
        }}
        testId="delete-collection-dialog"
      />
    </main>
  );
}

const NO_FILES: FileSummary[] = [];

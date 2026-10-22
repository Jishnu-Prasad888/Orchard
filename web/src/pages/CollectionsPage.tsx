/**
 * Collections — personal, user-curated groupings of files.
 *
 * Unlike Tags (a shared library vocabulary), a collection belongs to the
 * person who made it; nobody else sees it. The flat list and the drill-in
 * detail view intentionally mirror TagsPage, since the two features share
 * almost the same shape (a named group, and everything carrying it).
 */

import { useCallback, useState, type ReactNode } from 'react';
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
import { ShareDialog } from '../components/ShareDialog';
import { MediaGrid } from '../components/media/MediaGrid';
import { SelectionToolbar } from '../components/media/SelectionToolbar';
import { useMediaActions } from '../components/media/useMediaActions';
import { useVisibleFiles } from '../components/media/useHideCompleted';
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
import { matchesQuery, usePageQuery } from '../components/app-shell/pageSearch';
import { Icon } from '../components/ui/Icon';
import { Menu, menuGroups, type MenuAnchor, type MenuEntry } from '../components/ui/Menu';
import { ViewerModal } from '../components/ViewerModal';
import './CollectionsPage.css';

export default function CollectionsPage() {
  const gate = useLibraryGate();
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  const [params, setParams] = useSearchParams();
  const openID = params.get('open');
  // The collection the user opened here; a deep link opens one too (below).
  const [picked, setActive] = useState<Collection | null>(null);
  const [creating, setCreating] = useState(false);
  const [renaming, setRenaming] = useState<Collection | null>(null);
  const [deleting, setDeleting] = useState<Collection | null>(null);
  const [sharing, setSharing] = useState<Collection | null>(null);
  const [menu, setMenu] = useState<{
    anchor: MenuAnchor;
    items: MenuEntry[];
    header?: ReactNode;
  } | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [promptError, setPromptError] = useState<string | null>(null);

  const collections = useLibraryResource(
    useCallback(
      async (libraryId: string) => (await listCollections(libraryId)).collections ?? [],
      [],
    ),
  );

  // The top-bar search filters the collection grid in place (see pageSearch.ts).
  const pageQuery = usePageQuery();
  const shownCollections =
    collections.data?.filter((c) => matchesQuery(pageQuery, c.name, c.description)) ?? null;

  // A deep link from elsewhere (e.g. a pinned collection on Home) opens a
  // specific collection once the list has loaded. Closing one clears the
  // link as well, so this never reopens a collection the user just closed.
  const deepLinked =
    !picked && openID ? (collections.data?.find((c) => c.id === openID) ?? null) : null;
  const active = picked ?? deepLinked;

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
        activeId
          ? listCollectionFiles(libraryId, activeId).then((r) => r.files ?? [])
          : Promise.resolve([]),
      [activeId],
    ),
    [activeId],
    active !== null,
  );

  const ops = useFileOperations(gate.kind === 'ready' ? gate.libraryId : '', () => {
    files.reload();
    collections.reload();
  });
  const collectionFiles = useVisibleFiles(files.data ?? NO_FILES);
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
    extraMenu: (file) =>
      active
        ? [
            {
              id: 'remove-from-collection',
              label: `Remove from “${active.name}”`,
              icon: 'close',
              onSelect: () => void removeFromCollection([file]),
              testId: 'menu-remove-from-collection',
            },
          ]
        : [],
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
    void run(() =>
      updateCollection(gate.kind === 'ready' ? gate.libraryId : '', c.id, { pinned: !c.pinned }),
    );
  };

  const openRename = (c: Collection) => {
    setPromptError(null);
    setRenaming(c);
  };
  const canShare = gate.kind === 'ready' && (gate.library.can_share ?? false);
  const collectionMenu = (c: Collection): MenuEntry[] =>
    menuGroups(
      [
        {
          id: 'open',
          label: 'Open',
          icon: 'archive',
          onSelect: () => setActive(c),
        },
      ],
      [
        {
          id: 'rename',
          label: 'Rename…',
          icon: 'edit',
          onSelect: () => openRename(c),
          testId: 'menu-rename-collection',
        },
        {
          id: 'pin',
          label: 'Pinned to Home',
          icon: 'pin',
          checked: c.pinned,
          onSelect: () => togglePin(c),
        },
        ...(canShare
          ? [
              {
                id: 'share',
                label: 'Share…',
                icon: 'share' as const,
                onSelect: () => setSharing(c),
                testId: 'menu-share-collection',
              },
            ]
          : []),
      ],
      [
        {
          id: 'delete',
          label: 'Delete collection…',
          icon: 'trash',
          danger: true,
          onSelect: () => {
            setActionError(null);
            setDeleting(c);
          },
          testId: 'menu-delete-collection',
        },
      ],
    );

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
            {canShare && (
              <button
                type="button"
                className="button"
                onClick={() => setSharing(active)}
                data-testid="share-collection-button"
              >
                Share
              </button>
            )}
            <button
              type="button"
              className="button"
              onClick={() => openRename(active)}
              data-testid="rename-collection-button"
            >
              Rename
            </button>
            <button
              type="button"
              className="button danger-button"
              onClick={() => setDeleting(active)}
            >
              Delete collection
            </button>
          </div>

          {files.loading && <LoadingState />}
          {files.error && <ErrorState message={files.error} onRetry={files.reload} />}

          {files.data !== null && files.data.length === 0 && (
            <EmptyState title="Nothing in this collection yet" testId="collection-files-empty">
              <p className="muted">Right-click any file and choose Collections… to add it here.</p>
            </EmptyState>
          )}

          {files.data !== null && collectionFiles.length > 0 && (
            <MediaGrid
              libraryId={gate.libraryId}
              files={collectionFiles}
              onOpen={ops.openViewer}
              selection={selection}
              onContextMenu={(file, x, y) =>
                setMenu({
                  anchor: { x, y },
                  items: actions.menuFor(file),
                  header: actions.headerFor(file),
                })
              }
              label={active.name}
              testId="file-grid"
            />
          )}

          {ops.viewer && (
            <ViewerModal
              libraryId={gate.libraryId}
              file={ops.viewer}
              siblings={collectionFiles}
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
          {shownCollections !== null &&
            collections.data!.length > 0 &&
            shownCollections.length === 0 && (
              <p className="muted">No collections match “{pageQuery}”.</p>
            )}
          {shownCollections !== null && shownCollections.length > 0 && (
            <ul className="collection-grid" data-testid="collections-grid" aria-label="Collections">
              {shownCollections.map((c) => (
                <li key={c.id}>
                  <div
                    className="collection-card"
                    onContextMenu={(event) => {
                      event.preventDefault();
                      setMenu({
                        anchor: { x: event.clientX, y: event.clientY },
                        items: collectionMenu(c),
                      });
                    }}
                  >
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
                    <button
                      type="button"
                      className="icon-button"
                      aria-label={`More actions for ${c.name}`}
                      aria-haspopup="menu"
                      onClick={(event) =>
                        setMenu({
                          anchor: { element: event.currentTarget },
                          items: collectionMenu(c),
                        })
                      }
                      data-testid={`more-collection-${c.name}`}
                    >
                      <Icon name="more" size={16} />
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
              // Renamed from its card: stay on the list; from inside: stay in it.
              if (active?.id === resp.collection.id) setActive(resp.collection);
              collections.reload();
            })
            .catch((e: unknown) => setPromptError(e instanceof Error ? e.message : String(e)))
            .finally(() => setBusy(false));
        }}
        testId="rename-collection-dialog"
      />

      {menu && (
        <Menu
          anchor={menu.anchor}
          items={menu.items}
          header={menu.header}
          onClose={() => setMenu(null)}
          label="Collection actions"
        />
      )}

      <ShareDialog
        libraryId={gate.libraryId}
        open={sharing !== null}
        fixed={
          sharing
            ? { target: { kind: 'collection', id: sharing.id }, name: sharing.name }
            : undefined
        }
        onClose={() => setSharing(null)}
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

/**
 * Collections — personal, user-curated groupings of files.
 *
 * Unlike Tags (a shared library vocabulary), a collection belongs to the
 * person who made it; nobody else sees it. The flat list and the drill-in
 * detail view intentionally mirror TagsPage, since the two features share
 * almost the same shape (a named group, and everything carrying it).
 */

import { ArrowLeft, Check, LayoutGrid, Route } from 'lucide-react';
import { useCallback, useMemo, useRef, useState, type ReactNode } from 'react';
import { useSearchParams } from 'react-router-dom';

import { useAuth } from '../auth/authContext';
import { useLibraryGate } from '../api/libraries';
import { useLibraryResource } from '../api/resources';
import {
  addCollectionFile,
  createCollection,
  deleteCollection,
  listCollectionFiles,
  listCollections,
  removeCollectionFile,
  setCollectionTimeline,
  updateCollection,
} from '../api/queries';
import type { Collection, FileSummary } from '../api/types';
import { AddFilesDialog } from '../components/AddFilesDialog';
import { CollectionTimeline } from '../components/collections/CollectionTimeline';
import { ColorSwatches } from '../components/collections/ColorSwatches';
import { HiddenCollectionsPicker } from '../components/collections/HiddenCollectionsPicker';
import { useHiddenLibraries } from '../components/collections/useHiddenLibraries';
import { COLLECTION_COLORS, colorLabel, isCollectionColor } from '../components/collections/colors';
import { ConfirmDialog, PromptDialog } from '../components/Dialog';
import { ShareDialog } from '../components/ShareDialog';
import { MediaGrid } from '../components/media/MediaGrid';
import { SelectionToolbar } from '../components/media/SelectionToolbar';
import { useMediaActions } from '../components/media/useMediaActions';
import { useHideCompleted, useVisibleFiles } from '../components/media/useHideCompleted';
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
import { useToast } from '../components/ui/Toast';
import { ViewerModal } from '../components/ViewerModal';
import { useFlip } from '../lib/useFlip';
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
  // Pins and colours are shown at once and saved behind (see
  // patchCollection). Pinned collections come first, the most recently
  // pinned at the front, then the rest by name; a card that changes place
  // glides there (useFlip) so it's plain where it went.
  const [overrides, setOverrides] = useState<
    ReadonlyMap<string, Partial<Pick<Collection, 'pinned' | 'color' | 'pinned_at' | 'hidden'>>>
  >(() => new Map());
  const shown = useCallback(
    (c: Collection): Collection => ({ ...c, ...overrides.get(c.id) }),
    [overrides],
  );
  const isPinned = useCallback((c: Collection) => shown(c).pinned, [shown]);
  const colorOf = useCallback((c: Collection) => shown(c).color ?? '', [shown]);
  const isHidden = useCallback((c: Collection) => Boolean(shown(c).hidden), [shown]);
  // A whole library's collections can be hidden too (an account setting).
  const hiddenLibraries = useHiddenLibraries();
  const libraryHidden = gate.kind === 'ready' && hiddenLibraries.isLibraryHidden(gate.libraryId);
  /** Out of sight: hidden itself, or in a hidden library. */
  const isOff = useCallback(
    (c: Collection) => libraryHidden || isHidden(c),
    [libraryHidden, isHidden],
  );

  // Name or newest first, remembered on this device. Pinned ones lead either way.
  const [sortBy, setSortByState] = useState<CollectionSort>(readCollectionSort);
  const setSortBy = (next: CollectionSort) => {
    setSortByState(next);
    try {
      localStorage.setItem(SORT_KEY, next);
    } catch {
      // Private mode: the choice lasts for this visit.
    }
  };

  // The colour circles under the header show only collections of that colour.
  const colorParam = params.get('color');
  const colorFilter = isCollectionColor(colorParam) ? colorParam : null;
  const setColorFilter = (color: string | null) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (color) next.set('color', color);
        else next.delete('color');
        return next;
      },
      { replace: true },
    );
  const colorCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const c of collections.data ?? []) {
      if (isOff(c)) continue;
      const color = colorOf(c);
      if (color) counts.set(color, (counts.get(color) ?? 0) + 1);
    }
    return counts;
  }, [collections.data, colorOf, isOff]);

  // What the search and colour ask for, then split: the grid shows the
  // collections not hidden; a search also offers the hidden ones it found,
  // folded away below.
  const matching = useMemo(
    () =>
      collections.data
        ?.filter((c) => matchesQuery(pageQuery, c.name, c.description))
        .filter((c) => !colorFilter || colorOf(c) === colorFilter)
        .map(shown)
        .sort(collectionOrder(sortBy)) ?? null,
    [collections.data, pageQuery, colorFilter, colorOf, shown, sortBy],
  );
  const shownCollections = useMemo(
    () => matching?.filter((c) => !libraryHidden && !c.hidden) ?? null,
    [matching, libraryHidden],
  );
  const hiddenMatches = useMemo(
    () => (pageQuery.trim() ? (matching?.filter((c) => libraryHidden || c.hidden) ?? []) : []),
    [matching, pageQuery, libraryHidden],
  );
  const pinnedCount = (collections.data ?? []).filter(isPinned).length;
  const flipRef = useFlip(shownCollections?.map((c) => c.id).join('|') ?? '');

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
  const contents = useLibraryResource(
    useCallback(
      (libraryId) =>
        activeId
          ? listCollectionFiles(libraryId, activeId).then((r) => ({
              files: r.files ?? [],
              timeline: r.timeline ?? [],
            }))
          : Promise.resolve({ files: [] as FileSummary[], timeline: [] as string[] }),
      [activeId],
    ),
    [activeId],
    active !== null,
  );
  const files = useMemo(
    () => ({
      data: contents.data?.files ?? null,
      loading: contents.loading,
      error: contents.error,
      reload: contents.reload,
    }),
    [contents.data, contents.loading, contents.error, contents.reload],
  );
  const [view, setView] = useState<'books' | 'timeline'>('books');
  const { marked } = useHideCompleted();
  const isCompleted = useCallback(
    (f: FileSummary) => marked.get(f.id) ?? Boolean(f.completed),
    [marked],
  );

  const ops = useFileOperations(gate.kind === 'ready' ? gate.libraryId : '', () => {
    files.reload();
    collections.reload();
  });
  const collectionFiles = useVisibleFiles(files.data ?? NO_FILES);
  const collectedIds = useMemo(
    () => new Set((files.data ?? NO_FILES).map((f) => f.id)),
    [files.data],
  );
  const [adding, setAdding] = useState(false);
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

  // Each collection's pin/colour requests go one after another, so quick
  // clicks land in the order they were made and the last one wins.
  const patchQueue = useRef(new Map<string, Promise<unknown>>());
  const patchCollection = (
    c: Collection,
    patch: Partial<Pick<Collection, 'pinned' | 'color' | 'hidden'>>,
    // Shown at once but not sent: what the server works out for itself.
    local: Partial<Pick<Collection, 'pinned' | 'pinned_at'>> = {},
  ) => {
    const libraryId = gate.kind === 'ready' ? gate.libraryId : '';
    const before = shown(c);
    setOverrides((prev) => new Map(prev).set(c.id, { ...prev.get(c.id), ...patch, ...local }));
    setActionError(null);
    const previous = patchQueue.current.get(c.id) ?? Promise.resolve();
    const request = previous
      .catch(() => undefined)
      .then(() => updateCollection(libraryId, c.id, patch))
      .then(() => collections.reload())
      .catch((e: unknown) => {
        const undo = Object.fromEntries(
          [...Object.keys(patch), ...Object.keys(local)].map((k) => [
            k,
            before[k as keyof Collection],
          ]),
        );
        setOverrides((prev) => new Map(prev).set(c.id, { ...prev.get(c.id), ...undo }));
        setActionError(e instanceof Error ? e.message : String(e));
      });
    patchQueue.current.set(c.id, request);
  };
  const toast = useToast();
  const togglePin = (c: Collection) => {
    const pinned = !isPinned(c);
    if (pinned && pinnedCount >= MAX_PINNED) {
      toast({
        message: `You can pin up to ${MAX_PINNED} collections. Unpin one first.`,
        tone: 'error',
      });
      return;
    }
    if (pinned && isHidden(c)) {
      // Pinning something hidden brings it back into view.
      patchCollection(c, { hidden: false, pinned }, { pinned_at: new Date().toISOString() });
      return;
    }
    patchCollection(c, { pinned }, { pinned_at: pinned ? new Date().toISOString() : undefined });
  };
  /** Hiding unpins too: the server does the same. */
  const setHidden = (c: Collection, hidden: boolean) =>
    patchCollection(c, { hidden }, hidden ? { pinned: false, pinned_at: undefined } : {});
  const setColor = (c: Collection, color: string) => patchCollection(c, { color });

  /** The colour palette at the top of a collection's menu. */
  const colorMenuHeader = (c: Collection) => (
    <div className="collection-menu-colors">
      <span className="collection-menu-colors-label">Colour</span>
      <ColorSwatches
        value={colorOf(c)}
        label={`Colour of ${c.name}`}
        onPick={(color) => {
          setColor(c, color);
          setMenu(null);
        }}
      />
    </div>
  );

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
          label: 'Pinned',
          icon: 'pin',
          checked: isPinned(c),
          onSelect: () => togglePin(c),
        },
        {
          id: 'hide',
          label: 'Hidden',
          icon: 'eye-off',
          checked: isHidden(c),
          onSelect: () => setHidden(c, !isHidden(c)),
          testId: 'menu-hide-collection',
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

  const renderCard = (c: Collection) => (
    <div
      className={colorOf(c) ? 'collection-card has-color' : 'collection-card'}
      data-collection-color={colorOf(c) || undefined}
      data-testid={`collection-card-${c.name}`}
      onContextMenu={(event) => {
        event.preventDefault();
        setMenu({
          anchor: { x: event.clientX, y: event.clientY },
          items: collectionMenu(c),
          header: colorMenuHeader(c),
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
        className={isPinned(c) ? 'icon-button active' : 'icon-button'}
        aria-label={isPinned(c) ? `Unpin ${c.name}` : `Pin ${c.name}`}
        title={
          isPinned(c)
            ? 'Unpin'
            : pinnedCount >= MAX_PINNED
              ? `Up to ${MAX_PINNED} can be pinned`
              : 'Pin to the front'
        }
        aria-pressed={isPinned(c)}
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
            header: colorMenuHeader(c),
          })
        }
        data-testid={`more-collection-${c.name}`}
      >
        <Icon name="more" size={16} />
      </button>
    </div>
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
          {!active && collections.data && collections.data.length > 0 && (
            <>
              <div className="collection-sort" role="radiogroup" aria-label="Sort collections">
                {(
                  [
                    ['name', 'A–Z', 'By name'],
                    ['created', 'Newest', 'Newest created first'],
                  ] as const
                ).map(([value, label, title]) => (
                  <button
                    key={value}
                    type="button"
                    role="radio"
                    aria-checked={sortBy === value}
                    className={sortBy === value ? 'is-active' : undefined}
                    title={`${title} (pinned collections stay first)`}
                    onClick={() => setSortBy(value)}
                    data-testid={`collection-sort-${value}`}
                  >
                    {label}
                  </button>
                ))}
              </div>
              <HiddenCollectionsPicker
                collections={collections.data}
                isHidden={isHidden}
                onToggle={setHidden}
                libraries={gate.kind === 'ready' ? gate.libraries : []}
                {...(gate.kind === 'ready' ? { currentLibraryId: gate.libraryId } : {})}
                isLibraryHidden={hiddenLibraries.isLibraryHidden}
                onToggleLibrary={hiddenLibraries.setLibraryHidden}
              />
            </>
          )}
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
      {(actionError ?? hiddenLibraries.error) && (
        <p className="error-text" role="alert">
          {actionError ?? hiddenLibraries.error}
        </p>
      )}

      {collections.error && <ErrorState message={collections.error} onRetry={collections.reload} />}

      {!active && collections.data !== null && collections.data.length > 0 && (
        <div
          className="collection-color-filter"
          role="group"
          aria-label="Show collections of one colour"
        >
          {COLLECTION_COLORS.map(({ id, label }) => {
            const count = colorCounts.get(id) ?? 0;
            const on = colorFilter === id;
            return (
              <button
                key={id}
                type="button"
                className={count === 0 && !on ? 'color-swatch is-unused' : 'color-swatch'}
                data-collection-color={id}
                aria-pressed={on}
                aria-label={`${label}: ${count} ${count === 1 ? 'collection' : 'collections'}`}
                title={on ? `Showing ${label} — click to show all` : `${label} (${count})`}
                onClick={() => setColorFilter(on ? null : id)}
                data-testid={`color-filter-${id}`}
              >
                {on && <Check size={14} strokeWidth={3} aria-hidden="true" />}
              </button>
            );
          })}
          {colorFilter && (
            <button
              type="button"
              className="link-button collection-color-filter-clear"
              onClick={() => setColorFilter(null)}
            >
              Show all
            </button>
          )}
        </div>
      )}

      {active ? (
        <section className="collection-detail" data-testid="collection-detail">
          <div className="collection-detail-header">
            <button
              type="button"
              className="button"
              onClick={() => {
                setActive(null);
                setView('books');
                clearOpenParam();
              }}
            >
              <ArrowLeft aria-hidden="true" />
              Collections
            </button>
            <h2>{active.name}</h2>
            <button
              type="button"
              className="button primary-button"
              onClick={() => setAdding(true)}
              disabled={offline}
              data-testid="add-collection-files-button"
            >
              Add books
            </button>
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

          <div className="collection-view-switch" role="tablist" aria-label="Show">
            <button
              type="button"
              role="tab"
              aria-selected={view === 'books'}
              onClick={() => setView('books')}
              data-testid="collection-view-books"
            >
              <LayoutGrid aria-hidden="true" />
              Books
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={view === 'timeline'}
              onClick={() => setView('timeline')}
              data-testid="collection-view-timeline"
            >
              <Route aria-hidden="true" />
              Timeline
            </button>
          </div>

          {files.loading && <LoadingState />}
          {files.error && <ErrorState message={files.error} onRetry={files.reload} />}

          {view === 'timeline' && contents.data && (
            <CollectionTimeline
              // A fresh timeline per collection: its order is local state.
              key={active.id}
              libraryId={gate.libraryId}
              files={contents.data.files}
              timeline={contents.data.timeline}
              completed={isCompleted}
              onOpen={ops.openViewer}
              onSave={(ids) =>
                setCollectionTimeline(gate.libraryId, active.id, ids).then((r) => r.timeline)
              }
            />
          )}

          {view === 'books' && files.data !== null && files.data.length === 0 && (
            <EmptyState title="Nothing in this collection yet" testId="collection-files-empty">
              <p className="muted">
                Use Add books, or right-click any file and choose Collections… to add it here.
              </p>
            </EmptyState>
          )}

          {view === 'books' && files.data !== null && collectionFiles.length > 0 && (
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
            shownCollections.length === 0 &&
            hiddenMatches.length === 0 && (
              <p className="muted">
                {!pageQuery && libraryHidden
                  ? "This library's collections are hidden. Untick it under Hidden to see them, or search for one."
                  : !pageQuery && !colorFilter
                    ? 'Every collection is hidden. Untick some under Hidden to see them here.'
                    : colorFilter && !pageQuery
                      ? `No ${colorLabel(colorFilter)} collections yet. Right-click a collection to give it a colour.`
                      : `No collections match${pageQuery ? ` “${pageQuery}”` : ''}${colorFilter ? ` in ${colorLabel(colorFilter)}` : ''}.`}
              </p>
            )}
          {shownCollections !== null && shownCollections.length > 0 && (
            <ul className="collection-grid" data-testid="collections-grid" aria-label="Collections">
              {shownCollections.map((c) => (
                <li key={c.id} ref={flipRef(c.id)}>
                  {renderCard(c)}
                </li>
              ))}
            </ul>
          )}
          {hiddenMatches.length > 0 && (
            <details className="search-hidden" data-testid="collections-hidden-matches">
              <summary>
                <Icon name="chevron-right" size={16} />
                {hiddenMatches.length === 1
                  ? '1 hidden collection matches'
                  : `${hiddenMatches.length} hidden collections match`}
                <span className="search-hidden-why">
                  Hidden by you — untick it under Hidden to show it again
                </span>
              </summary>
              <ul className="collection-grid" aria-label="Hidden collections">
                {hiddenMatches.map((c) => (
                  <li key={c.id}>{renderCard(c)}</li>
                ))}
              </ul>
            </details>
          )}
        </>
      )}

      {active && (
        <AddFilesDialog
          open={adding}
          libraryId={gate.libraryId}
          title={`Add to “${active.name}”`}
          existingIds={collectedIds}
          onAdd={async (picked) => {
            await Promise.all(
              picked.map((f) => addCollectionFile(gate.libraryId, active.id, f.id)),
            );
            files.reload();
            collections.reload();
          }}
          onClose={() => setAdding(false)}
          testId="add-collection-files-dialog"
        />
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

/** Matches the server's collections.MaxPinned. */
const MAX_PINNED = 5;

type CollectionSort = 'name' | 'created';
const SORT_KEY = 'orchard.collections.sort';

function readCollectionSort(): CollectionSort {
  try {
    return localStorage.getItem(SORT_KEY) === 'created' ? 'created' : 'name';
  } catch {
    return 'name';
  }
}

/** Pinned first, the most recently pinned at the front; then the rest by
 * name or newest created. */
function collectionOrder(sort: CollectionSort) {
  const byName = (a: Collection, b: Collection) =>
    a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true });
  return (a: Collection, b: Collection): number => {
    if (a.pinned !== b.pinned) return a.pinned ? -1 : 1;
    if (a.pinned) {
      const at = (c: Collection) => (c.pinned_at ? Date.parse(c.pinned_at) : 0);
      const diff = at(b) - at(a);
      if (diff) return diff;
    }
    if (sort === 'created') {
      const diff = Date.parse(b.created_at) - Date.parse(a.created_at);
      if (diff) return diff;
    }
    return byName(a, b);
  };
}

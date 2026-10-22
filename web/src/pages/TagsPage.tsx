/**
 * Tags — the library's tag vocabulary and the files that carry each tag.
 *
 * Tags are a flat, colourable vocabulary; the interesting part is the second
 * screen, where you see everything carrying a tag and can strip it from
 * individual files. Both screens use the shared library gate, the shared
 * dialogs, and the shared viewer, so nothing here reimplements them.
 */

import { useCallback, useState, type ReactNode } from 'react';

import { useAuth } from '../auth/authContext';
import { useLibraryGate } from '../api/libraries';
import { useLibraryResource } from '../api/resources';
import {
  createTag,
  deleteTag,
  listTags,
  removeFileTag,
  renameTag,
  searchFiles,
} from '../api/queries';
import type { FileSummary, Tag } from '../api/types';
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
import { Menu, type MenuAnchor, type MenuEntry } from '../components/ui/Menu';
import { ViewerModal } from '../components/ViewerModal';
import './TagsPage.css';

export default function TagsPage() {
  const gate = useLibraryGate();
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  const [activeTag, setActiveTag] = useState<Tag | null>(null);
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState<Tag | null>(null);
  const [renaming, setRenaming] = useState<Tag | null>(null);
  const [sharing, setSharing] = useState<Tag | null>(null);
  const [menu, setMenu] = useState<{
    anchor: MenuAnchor;
    items: MenuEntry[];
    header?: ReactNode;
  } | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [promptError, setPromptError] = useState<string | null>(null);

  const tags = useLibraryResource(
    useCallback(async (libraryId: string) => (await listTags(libraryId)).tags ?? [], []),
  );

  // The top-bar search filters the tag grid in place (see pageSearch.ts).
  const pageQuery = usePageQuery();
  const shownTags = tags.data?.filter((t) => matchesQuery(pageQuery, t.name)) ?? null;

  const files = useLibraryResource(
    useCallback(
      (libraryId) =>
        searchFiles(libraryId, { tag: activeTag?.name, limit: 200 }).then((r) => r.files ?? []),
      [activeTag?.name],
    ),
    [activeTag?.name],
    activeTag !== null,
  );

  const ops = useFileOperations(gate.kind === 'ready' ? gate.libraryId : '', () => {
    files.reload();
    tags.reload();
  });
  const tagFiles = useVisibleFiles(files.data ?? NO_FILES);
  const selection = useSelection(tagFiles, activeTag?.id ?? '');
  const libraryIdOrEmpty = gate.kind === 'ready' ? gate.libraryId : '';
  const untag = async (targets: FileSummary[]) => {
    if (!activeTag) return;
    await run(() =>
      Promise.all(targets.map((f) => removeFileTag(libraryIdOrEmpty, f.id, activeTag.id))),
    );
    selection.clear();
  };
  const actions = useMediaActions({
    libraryId: libraryIdOrEmpty,
    selection,
    ops,
    onChanged: files.reload,
    extraMenu: (file) =>
      activeTag
        ? [
            {
              id: 'untag',
              label: `Remove tag “${activeTag.name}”`,
              icon: 'tag',
              onSelect: () => void untag([file]),
            },
          ]
        : [],
    extra: activeTag
      ? [
          {
            id: 'untag',
            label: `Remove tag “${activeTag.name}”`,
            icon: 'tag',
            onClick: () => void untag(selection.selectedFiles),
            testId: 'selection-untag',
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
        tags.reload();
        files.reload();
      } catch (e: unknown) {
        setActionError(e instanceof Error ? e.message : String(e));
      } finally {
        setBusy(false);
      }
    },
    [files, tags],
  );

  const openRename = (tag: Tag) => {
    setPromptError(null);
    setRenaming(tag);
  };
  const canShare = gate.kind === 'ready' && (gate.library.can_share ?? false);
  const tagMenu = (tag: Tag): MenuEntry[] => [
    { id: 'open', label: 'Open', icon: 'tag', onSelect: () => setActiveTag(tag) },
    ...(canShare
      ? [
          {
            id: 'share',
            label: 'Share…',
            icon: 'share' as const,
            onSelect: () => setSharing(tag),
            testId: 'menu-share-tag',
          },
        ]
      : []),
    {
      id: 'rename',
      label: 'Rename…',
      icon: 'edit',
      onSelect: () => openRename(tag),
      testId: 'menu-rename-tag',
    },
    {
      id: 'delete',
      label: 'Delete tag…',
      icon: 'trash',
      danger: true,
      onSelect: () => {
        setActionError(null);
        setDeleting(tag);
      },
      testId: 'menu-delete-tag',
    },
  ];

  if (gate.kind === 'loading') {
    return (
      <main className="page tags-page">
        <LoadingState label="Loading libraries…" />
      </main>
    );
  }

  const header = (
    <PageHeader
      title="Tags"
      subtitle="A flat vocabulary you apply to any file, from the viewer or in bulk."
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
            data-testid="new-tag-button"
          >
            New tag
          </button>
        </>
      }
    />
  );

  if (gate.kind === 'error') {
    return (
      <main className="page tags-page">
        {header}
        <ErrorState message={gate.message} onRetry={tags.reload} />
      </main>
    );
  }

  if (gate.kind === 'empty') {
    return (
      <main className="page tags-page">
        {header}
        <NoLibrariesState isAdmin={isAdmin} />
      </main>
    );
  }

  const library = gate.library;
  const offline = library.status === 'offline';

  return (
    <main className="page tags-page">
      {header}

      {offline && <LibraryOfflineNotice library={library} />}
      {actionError && (
        <p className="error-text" role="alert">
          {actionError}
        </p>
      )}

      {tags.error && <ErrorState message={tags.error} onRetry={tags.reload} />}

      {activeTag ? (
        <section className="tag-detail" data-testid="tag-detail">
          <div className="tag-detail-header">
            <button type="button" className="button" onClick={() => setActiveTag(null)}>
              ← Tags
            </button>
            <h2>
              <span className="tag-chip">{activeTag.name}</span>
            </h2>
            {canShare && (
              <button
                type="button"
                className="button"
                onClick={() => setSharing(activeTag)}
                data-testid="share-tag-button"
              >
                Share
              </button>
            )}
            <button
              type="button"
              className="button"
              onClick={() => openRename(activeTag)}
              data-testid="rename-tag-button"
            >
              Rename
            </button>
            <button
              type="button"
              className="button danger-button"
              onClick={() => setDeleting(activeTag)}
            >
              Delete tag
            </button>
          </div>

          {files.loading && <LoadingState />}
          {files.error && <ErrorState message={files.error} onRetry={files.reload} />}

          {files.data !== null && tagFiles.length === 0 && (
            <EmptyState title="Nothing tagged yet" testId="tag-files-empty">
              <p className="muted">No files carry this tag. Add it from the file viewer.</p>
            </EmptyState>
          )}

          {files.data !== null && tagFiles.length > 0 && (
            <MediaGrid
              libraryId={gate.libraryId}
              files={tagFiles}
              onOpen={ops.openViewer}
              selection={selection}
              onContextMenu={(file, x, y) =>
                setMenu({
                  anchor: { x, y },
                  items: actions.menuFor(file),
                  header: actions.headerFor(file),
                })
              }
              label={`Tagged ${activeTag.name}`}
              testId="file-grid"
            />
          )}

          {ops.viewer && (
            <ViewerModal
              libraryId={gate.libraryId}
              file={ops.viewer}
              siblings={tagFiles}
              onNavigate={ops.openViewer}
              onChanged={() => {
                files.reload();
                tags.reload();
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
              total={tagFiles.length}
              actions={actions.selectionActions}
              onClear={selection.clear}
              onSelectAll={selection.selectAll}
              onDeleteKey={actions.onDeleteKey}
            />
          )}
        </section>
      ) : (
        <>
          {tags.loading && <LoadingState />}
          {tags.data !== null && tags.data.length === 0 && (
            <EmptyState title="No tags yet" testId="tags-empty">
              <p className="muted">
                Tags are how you label media across folders. Create one, then apply it from the
                viewer.
              </p>
            </EmptyState>
          )}
          {shownTags !== null && tags.data!.length > 0 && shownTags.length === 0 && (
            <p className="muted">No tags match “{pageQuery}”.</p>
          )}
          {shownTags !== null && shownTags.length > 0 && (
            <ul className="tag-grid" data-testid="tags-grid" aria-label="Tags">
              {shownTags.map((tag) => (
                <li key={tag.id}>
                  <button
                    type="button"
                    className="tag-card"
                    onClick={() => setActiveTag(tag)}
                    onContextMenu={(event) => {
                      event.preventDefault();
                      setMenu({
                        anchor: { x: event.clientX, y: event.clientY },
                        items: tagMenu(tag),
                      });
                    }}
                  >
                    <span
                      className="tag-chip"
                      style={tag.color ? { background: tag.color } : undefined}
                    >
                      {tag.name}
                    </span>
                    <span className="tag-date">
                      Created {new Date(tag.created_at).toLocaleDateString()}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </>
      )}

      <PromptDialog
        open={creating}
        title="New tag"
        label="Tag name"
        placeholder="family"
        hint="Tags are per-library. Typing an existing name in the viewer reuses it."
        busy={busy}
        error={promptError}
        onCancel={() => setCreating(false)}
        onConfirm={(name) => {
          setBusy(true);
          setPromptError(null);
          createTag(gate.libraryId, name)
            .then(() => {
              setCreating(false);
              tags.reload();
            })
            .catch((e: unknown) => setPromptError(e instanceof Error ? e.message : String(e)))
            .finally(() => setBusy(false));
        }}
        testId="new-tag-dialog"
      />

      <PromptDialog
        open={renaming !== null}
        title="Rename tag"
        label="Tag name"
        initialValue={renaming?.name ?? ''}
        hint="Every file with this tag keeps it under the new name."
        busy={busy}
        error={promptError}
        onCancel={() => setRenaming(null)}
        onConfirm={(name) => {
          if (!renaming) return;
          const target = renaming;
          setBusy(true);
          setPromptError(null);
          renameTag(gate.libraryId, target.id, name)
            .then(({ tag }) => {
              setRenaming(null);
              // Still looking at it: follow it to its new name.
              setActiveTag((current) => (current?.id === tag.id ? tag : current));
              tags.reload();
            })
            .catch((e: unknown) => setPromptError(e instanceof Error ? e.message : String(e)))
            .finally(() => setBusy(false));
        }}
        testId="rename-tag-dialog"
      />

      {menu && (
        <Menu
          anchor={menu.anchor}
          items={menu.items}
          header={menu.header}
          onClose={() => setMenu(null)}
          label="Tag actions"
        />
      )}

      <ShareDialog
        libraryId={gate.libraryId}
        open={sharing !== null}
        fixed={
          sharing ? { target: { kind: 'tag', id: sharing.id }, name: sharing.name } : undefined
        }
        onClose={() => setSharing(null)}
      />

      <ConfirmDialog
        open={deleting !== null}
        title={`Delete tag “${deleting?.name ?? ''}”?`}
        destructive
        confirmLabel="Delete tag"
        busy={busy}
        error={actionError}
        message={
          <p>
            The tag is removed from every file that carries it. The files themselves are not
            affected.
          </p>
        }
        onCancel={() => setDeleting(null)}
        onConfirm={() => {
          if (!deleting) return;
          const target = deleting;
          void run(
            () => deleteTag(gate.libraryId, target.id),
            () => {
              setDeleting(null);
              setActiveTag(null);
            },
          );
        }}
        testId="delete-tag-dialog"
      />
    </main>
  );
}

const NO_FILES: FileSummary[] = [];

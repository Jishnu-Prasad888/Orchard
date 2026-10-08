/**
 * Similar photos — visual near-duplicates found by Cairn's local similarity
 * pass, grouped so the ones worth trimming can be reviewed together instead
 * of one file at a time.
 *
 * Each group is shown collapsed, as a small fanned stack of its thumbnails;
 * opening it lists every member with a checkbox. Exactly one member starts
 * unchecked — the largest file in the group, the best guess at which copy is
 * worth keeping — and the rest start checked, ready to move to the trash.
 * Nothing is deleted until "Move to trash" is pressed.
 *
 * Finding the matches is a separate, admin-only step (Settings › Machine
 * learning): a similarity pass has to run before there is anything to review
 * here, and this page says so rather than just looking empty.
 */

import { useCallback, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';

import { useAuth } from '../auth/authContext';
import { ApiError } from '../api/client';
import { useLibraryGate } from '../api/libraries';
import { useLibraryResource } from '../api/resources';
import { getMLStatus, listSimilarityGroups, softDeleteFile } from '../api/queries';
import type { FileSummary, Library, MLStatus, SimilarityGroup } from '../api/types';
import { formatBytes } from '../api/types';
import { Dialog } from '../components/Dialog';
import LibraryPicker from '../components/LibraryPicker';
import { thumbnailUrl } from '../components/media';
import {
  EmptyState,
  ErrorState,
  LibraryOfflineNotice,
  LoadingState,
  NoLibrariesState,
  PageHeader,
} from '../components/States';
import { useToast } from '../components/ui/Toast';
import { formatDate } from '../lib/dates';
import './SimilarPage.css';

/** Where a library stands with the similarity pass, for the page to explain. */
type Phase =
  | { kind: 'unavailable' }
  | { kind: 'disabled' }
  | { kind: 'no-signatures' }
  | { kind: 'ready'; groups: SimilarityGroup[]; pending: number };

async function loadPhase(libraryId: string): Promise<Phase> {
  let status: MLStatus;
  try {
    status = await getMLStatus(libraryId);
  } catch (e: unknown) {
    if (e instanceof ApiError && (e.status === 404 || e.status === 503)) {
      return { kind: 'unavailable' };
    }
    throw e;
  }
  if (!status.enabled) return { kind: 'disabled' };
  if (!status.signatured) return { kind: 'no-signatures' };
  const resp = await listSimilarityGroups(libraryId);
  return { kind: 'ready', groups: resp.groups ?? [], pending: status.pending ?? 0 };
}

/** The member to leave unchecked by default: the largest file, ties kept in
 * place — the best guess at which copy is the original worth keeping. */
function bestToKeep(files: FileSummary[]): string {
  return files.reduce((best, f) => (f.size_bytes > best.size_bytes ? f : best), files[0]!).id;
}

const EMPTY_GROUPS: SimilarityGroup[] = [];

const STACK_OFFSETS = [
  { x: 0, y: 0, r: 0 },
  { x: 7, y: -5, r: -6 },
  { x: -7, y: 4, r: 5 },
  { x: 11, y: 7, r: -10 },
];

function GroupStack({ group, onOpen }: { group: SimilarityGroup; onOpen: () => void }) {
  const cards = group.files.slice(0, STACK_OFFSETS.length);
  const redundant = group.files.length - 1;
  return (
    <button
      type="button"
      className="similar-stack"
      onClick={onOpen}
      aria-label={`${group.files.length} similar photos — review`}
      data-testid="similar-stack"
    >
      <span className="similar-stack-cards" aria-hidden="true">
        {cards.map((file, i) => {
          const offset = STACK_OFFSETS[i]!;
          return (
            <img
              key={file.id}
              className="similar-stack-card"
              style={{
                transform: `translate(${offset.x}px, ${offset.y}px) rotate(${offset.r}deg)`,
                zIndex: cards.length - i,
              }}
              src={thumbnailUrl(file.library_id, file)}
              alt=""
              loading="lazy"
            />
          );
        })}
      </span>
      <span className="similar-stack-count">{group.files.length}</span>
      <span className="similar-stack-hint">
        {redundant} {redundant === 1 ? 'copy' : 'copies'} to review
      </span>
    </button>
  );
}

function GroupModal({
  libraryId,
  group,
  onClose,
  onTrashed,
}: {
  libraryId: string;
  group: SimilarityGroup;
  onClose: () => void;
  onTrashed: () => void;
}) {
  const toast = useToast();
  const [selected, setSelected] = useState<ReadonlySet<string>>(() => {
    const keep = bestToKeep(group.files);
    return new Set(group.files.filter((f) => f.id !== keep).map((f) => f.id));
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const toggle = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const confirm = async () => {
    const targets = group.files.filter((f) => selected.has(f.id));
    if (targets.length === 0) return;
    setBusy(true);
    setError(null);
    const results = await Promise.allSettled(
      targets.map((f) => softDeleteFile(libraryId, f.rel_path, f.id)),
    );
    const failed = results.filter((r) => r.status === 'rejected').length;
    setBusy(false);
    if (failed > 0) {
      setError(
        `Moved ${targets.length - failed} of ${targets.length} to the trash. The rest could not be moved — they may have changed on disk.`,
      );
      onTrashed();
      return;
    }
    toast({
      message: targets.length === 1 ? 'Moved to trash' : `${targets.length} photos moved to trash`,
      tone: 'success',
    });
    onTrashed();
  };

  return (
    <Dialog
      open
      size="large"
      title={`${group.files.length} similar photos`}
      onClose={onClose}
      dismissible={!busy}
      testId="similar-group-dialog"
      footer={
        <>
          <button type="button" className="button" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="button danger-button"
            onClick={() => void confirm()}
            disabled={busy || selected.size === 0}
            data-testid="similar-confirm-trash"
          >
            {busy ? 'Moving…' : `Move ${selected.size > 0 ? selected.size : ''} to trash`.trim()}
          </button>
        </>
      }
    >
      <div className="similar-modal-toolbar">
        <span className="similar-modal-count" aria-live="polite">
          {selected.size} of {group.files.length} selected
        </span>
        <span className="similar-modal-actions">
          <button
            type="button"
            className="button ghost-button"
            onClick={() => setSelected(new Set(group.files.map((f) => f.id)))}
            data-testid="similar-select-all"
          >
            Select all
          </button>
          <button
            type="button"
            className="button ghost-button"
            onClick={() => setSelected(new Set())}
            data-testid="similar-deselect-all"
          >
            Deselect all
          </button>
        </span>
      </div>

      <ul className="similar-member-list">
        {group.files.map((file) => {
          const checked = selected.has(file.id);
          const kept = file.id === bestToKeep(group.files);
          return (
            <li key={file.id} className="similar-member">
              <label className="similar-member-label">
                <input
                  type="checkbox"
                  checked={checked}
                  onChange={() => toggle(file.id)}
                  aria-label={`Select ${file.name}`}
                />
                <img className="similar-member-thumb" src={thumbnailUrl(libraryId, file)} alt="" />
                <span className="similar-member-info">
                  <span className="similar-member-name">
                    {file.name}
                    {kept && <span className="similar-member-badge">Largest copy</span>}
                  </span>
                  <span className="similar-member-meta">
                    {file.folder_path || 'Library root'} · {formatBytes(file.size_bytes)} ·{' '}
                    {formatDate(file.mod_time)}
                  </span>
                </span>
              </label>
            </li>
          );
        })}
      </ul>

      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}
    </Dialog>
  );
}

export default function SimilarPage() {
  const gate = useLibraryGate();
  const { user } = useAuth();

  if (gate.kind === 'loading') {
    return (
      <main className="page similar-page">
        <LoadingState label="Loading libraries…" />
      </main>
    );
  }

  const header = (
    <PageHeader
      title="Similar photos"
      subtitle="Visual near-duplicates found by Cairn's local similarity pass."
      controls={<LibraryPicker />}
    />
  );

  if (gate.kind === 'error') {
    return (
      <main className="page similar-page">
        {header}
        <ErrorState message={gate.message} />
      </main>
    );
  }

  if (gate.kind === 'empty') {
    return (
      <main className="page similar-page">
        {header}
        <NoLibrariesState isAdmin={user?.role === 'admin'} />
      </main>
    );
  }

  return <Similar library={gate.library} header={header} isAdmin={user?.role === 'admin'} />;
}

function Similar({
  library,
  header,
  isAdmin,
}: {
  library: Library;
  header: ReactNode;
  isAdmin: boolean;
}) {
  const libraryId = library.id;
  const offline = library.status === 'offline';
  const [openGroupIndex, setOpenGroupIndex] = useState<number | null>(null);

  const resource = useLibraryResource<Phase>(useCallback((id: string) => loadPhase(id), []));

  const phase = resource.data;
  const groups = useMemo(() => (phase?.kind === 'ready' ? phase.groups : EMPTY_GROUPS), [phase]);
  const openGroup = openGroupIndex !== null ? (groups[openGroupIndex] ?? null) : null;

  const totalRedundant = useMemo(
    () => groups.reduce((sum, g) => sum + (g.files.length - 1), 0),
    [groups],
  );

  return (
    <main className="page similar-page">
      {header}
      {offline && <LibraryOfflineNotice library={library} />}

      {resource.error && <ErrorState message={resource.error} onRetry={resource.reload} />}
      {resource.loading && <LoadingState label="Looking for similar photos…" />}

      {phase?.kind === 'unavailable' && (
        <EmptyState title="Not available on this server" icon="spark" testId="similar-unavailable">
          <p className="muted">
            This Cairn server was built without local image similarity, so visual near-duplicates
            cannot be found.
          </p>
        </EmptyState>
      )}

      {phase?.kind === 'disabled' && (
        <EmptyState title="Local ML is off" icon="spark" testId="similar-disabled">
          <p className="muted">
            {isAdmin ? (
              <>
                Turn it on in <Link to="/ml">Machine learning</Link> to find photos that look alike.
              </>
            ) : (
              'An admin needs to turn it on in Machine learning settings before Cairn can find photos that look alike.'
            )}
          </p>
        </EmptyState>
      )}

      {phase?.kind === 'no-signatures' && (
        <EmptyState title="No similarity pass has run yet" icon="spark" testId="similar-no-pass">
          <p className="muted">
            {isAdmin ? (
              <>
                Run one from <Link to="/ml">Machine learning</Link> and come back — it only looks at
                photos once.
              </>
            ) : (
              'Ask an admin to run a similarity pass from Machine learning settings.'
            )}
          </p>
        </EmptyState>
      )}

      {phase?.kind === 'ready' && groups.length === 0 && (
        <EmptyState title="Nothing looks alike" testId="similar-empty" icon="spark">
          <p className="muted">Every photo Cairn has checked looks distinct from every other.</p>
        </EmptyState>
      )}

      {phase?.kind === 'ready' && groups.length > 0 && (
        <>
          <p className="similar-summary" data-testid="similar-summary">
            {groups.length} {groups.length === 1 ? 'group' : 'groups'} of similar photos ·{' '}
            {totalRedundant} {totalRedundant === 1 ? 'copy' : 'copies'} could be trimmed
          </p>
          <div className="similar-groups">
            {groups.map((group, i) => (
              <GroupStack
                key={group.files.map((f) => f.id).join(',')}
                group={group}
                onOpen={() => setOpenGroupIndex(i)}
              />
            ))}
          </div>
        </>
      )}

      {phase?.kind === 'ready' && phase.pending > 0 && (
        <p className="muted similar-footnote">
          {phase.pending} more {phase.pending === 1 ? 'photo has' : 'photos have'} not been checked
          yet.{' '}
          {isAdmin ? (
            <>
              Run another pass from <Link to="/ml">Machine learning</Link> to catch them.
            </>
          ) : (
            'Ask an admin to run another pass to catch them.'
          )}
        </p>
      )}

      {openGroup && (
        <GroupModal
          libraryId={libraryId}
          group={openGroup}
          onClose={() => setOpenGroupIndex(null)}
          onTrashed={() => {
            setOpenGroupIndex(null);
            resource.reload();
          }}
        />
      )}
    </main>
  );
}

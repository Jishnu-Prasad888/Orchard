/**
 * Duplicates — identical files found by content hash.
 *
 * The point of the page is deciding what to do about them, so each group names
 * the file it considers redundant rather than making the user count. Cleanup
 * itself happens in the viewer and the file grid (delete, trash, move); this
 * page shows the evidence and links to it.
 *
 * The old version fetched its own library list and defaulted to
 * `libraries[0]`, which meant a user with two libraries silently saw the wrong
 * one. It now uses the shared selection.
 */

import { useCallback, useState } from 'react';
import { Link } from 'react-router-dom';

import { useAuth } from '../auth/authContext';
import { API_BASE } from '../api/client';
import { useLibraryGate } from '../api/libraries';
import { useLibraryResource } from '../api/resources';
import { listDuplicates } from '../api/queries';
import type { DuplicateGroup, FileSummary } from '../api/types';
import { formatBytes } from '../api/types';
import LibraryPicker from '../components/LibraryPicker';
import {
  EmptyState,
  ErrorState,
  LibraryOfflineNotice,
  LoadingState,
  NoLibrariesState,
  PageHeader,
} from '../components/States';
import './DuplicatesPage.css';

/** Bytes wasted by keeping every copy except the first in a group. */
function redundantBytes(groups: DuplicateGroup[]): number {
  return groups.reduce((sum, g) => sum + g.size_bytes * (g.files.length - 1), 0);
}

function DuplicateMember({ file }: { file: FileSummary }) {
  return (
    <li className="dup-member">
      {file.media_type === 'photo' ? (
        <img
          className="dup-thumb"
          src={`${API_BASE}/libraries/${file.library_id}/files/${file.id}/thumbnail`}
          alt=""
          loading="lazy"
        />
      ) : (
        <span className="dup-thumb dup-thumb-placeholder" aria-hidden="true">
          {file.media_type.charAt(0).toUpperCase()}
        </span>
      )}
      <span className="dup-member-info">
        <span className="dup-member-name">{file.name}</span>
        <span className="dup-member-path">{file.folder_path || 'Library root'}</span>
      </span>
      <span className="dup-member-size">{formatBytes(file.size_bytes)}</span>
      <a
        className="dup-member-action"
        href={`${API_BASE}/libraries/${file.library_id}/files/${file.id}/download`}
      >
        Download
      </a>
    </li>
  );
}

function DuplicateGroupCard({ group }: { group: DuplicateGroup }) {
  const redundant = group.size_bytes * (group.files.length - 1);
  // The first copy is the one to keep; the rest are the candidates to remove.
  const [keep, ...removable] = group.files;
  return (
    <section
      className="dup-group"
      aria-label={`Duplicate group ${group.content_hash.slice(0, 12)}`}
    >
      <header className="dup-group-header">
        <h3>
          {group.files.length} identical files · {formatBytes(group.size_bytes)} each ·{' '}
          {formatBytes(redundant)} redundant
        </h3>
        <code title={group.content_hash}>{group.content_hash.slice(0, 16)}…</code>
      </header>
      <ul className="dup-members">{keep && <DuplicateMember file={keep} />}</ul>
      {removable.length > 0 && (
        <>
          <p className="muted dup-group-hint">
            Removable copies — pick one to open it, then delete or move it in the viewer.
          </p>
          <ul className="dup-members dup-members-removable">
            {removable.map((file) => (
              <DuplicateMember key={file.id} file={file} />
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

export default function DuplicatesPage() {
  const gate = useLibraryGate();
  const { user } = useAuth();
  const [limit, setLimit] = useState(50);

  const duplicates = useLibraryResource<{ total: number; groups: DuplicateGroup[] }>(
    useCallback(
      async (libraryId: string) => {
        const resp = await listDuplicates(libraryId, undefined, limit);
        return { total: resp.total ?? 0, groups: resp.groups ?? [] };
      },
      [limit],
    ),
    [limit],
  );

  if (gate.kind === 'loading') {
    return (
      <main className="page duplicates-page">
        <LoadingState label="Loading libraries…" />
      </main>
    );
  }

  const header = (
    <PageHeader
      title="Duplicates"
      subtitle="Files whose contents are byte-for-byte identical, grouped by hash."
      controls={
        <>
          <LibraryPicker />
          <button
            type="button"
            className="button"
            onClick={duplicates.reload}
            disabled={duplicates.loading}
          >
            Rescan
          </button>
        </>
      }
    />
  );

  if (gate.kind === 'error') {
    return (
      <main className="page duplicates-page">
        {header}
        <ErrorState message={gate.message} onRetry={duplicates.reload} />
      </main>
    );
  }

  if (gate.kind === 'empty') {
    return (
      <main className="page duplicates-page">
        {header}
        <NoLibrariesState isAdmin={user?.role === 'admin'} />
      </main>
    );
  }

  const groups = duplicates.data?.groups ?? [];
  const total = duplicates.data?.total ?? 0;

  return (
    <main className="page duplicates-page">
      {header}
      {gate.library.status === 'offline' && <LibraryOfflineNotice library={gate.library} />}

      {duplicates.error && <ErrorState message={duplicates.error} onRetry={duplicates.reload} />}
      {duplicates.loading && <LoadingState label="Scanning for duplicates…" />}

      {!duplicates.loading && !duplicates.error && groups.length === 0 && (
        <EmptyState title="No duplicates" testId="duplicates-empty">
          <p className="muted">Every file in this library has unique content.</p>
        </EmptyState>
      )}

      {groups.length > 0 && (
        <>
          <p className="duplicates-summary" data-testid="duplicates-summary">
            {total} duplicate {total === 1 ? 'group' : 'groups'} across this library ·{' '}
            {formatBytes(redundantBytes(groups))} redundant
          </p>
          <div className="duplicate-groups">
            {groups.map((group) => (
              <DuplicateGroupCard key={group.content_hash} group={group} />
            ))}
          </div>
          {total > groups.length && (
            <div className="duplicates-more">
              <button type="button" className="button" onClick={() => setLimit((n) => n + 50)}>
                Show more groups
              </button>
            </div>
          )}
        </>
      )}

      <p className="muted duplicates-footnote">
        Duplicates are computed from the index. Re-run an <Link to="/libraries">index</Link> after
        adding files to pick up new matches.
      </p>
    </main>
  );
}

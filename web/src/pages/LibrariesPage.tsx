/**
 * Library management.
 *
 * This is the adoption workflow from the product brief: point Cairn at a
 * directory that already holds years of media, see what is already there, and
 * open it — without copying or modifying a single original. It is also where a
 * user reconnects a drive that came back at a different mount point, which is
 * the case the whole volume-identity design exists for.
 *
 * Everything except the read-only status list is admin-only, so for a member
 * this page degrades to a read-only list rather than a wall of dead buttons.
 */

import { useCallback, useEffect, useState } from 'react';

import { useLibraries } from '../api/libraries';
import {
  browseServerDirs,
  getIndexStatus,
  probeLibrary,
  refreshLibrary,
  registerLibrary,
  triggerIndex,
  unregisterLibrary,
} from '../api/queries';
import type { DirListing } from '../api/queries';
import type { IndexStatus, Library, LibraryProbe } from '../api/types';
import { useAuth } from '../auth/authContext';
import { ConfirmDialog, Dialog } from '../components/Dialog';
import LibraryPicker from '../components/LibraryPicker';
import {
  EmptyState,
  ErrorState,
  LibraryStatusBadge,
  LoadingState,
  PageHeader,
} from '../components/States';
import './LibrariesPage.css';

/** What a probe found, phrased for a person choosing whether to open a path. */
function describeProbe(probe: LibraryProbe): { tone: 'ok' | 'warn' | 'error'; lines: string[] } {
  if (!probe.path_exists) {
    // Registering creates the folder (and any missing parents), so a path that
    // does not exist yet is a valid choice, not an error.
    return {
      tone: 'ok',
      lines: ['That folder does not exist yet. Adding the library will create it.'],
    };
  }
  if (!probe.is_directory) {
    return { tone: 'error', lines: ['That path is a file, not a directory.'] };
  }
  if (!probe.is_writable) {
    return {
      tone: 'warn',
      lines: [
        'Cairn cannot write to that directory, so it can index and browse it but not accept uploads.',
      ],
    };
  }
  if (probe.registered) {
    return { tone: 'warn', lines: ['This path is already registered as a library.'] };
  }
  if (probe.has_metadata) {
    return {
      tone: 'ok',
      lines: [
        `Found an existing Cairn library${probe.existing_name ? ` (“${probe.existing_name}”)` : ''}. Opening it reuses its metadata, tags, albums, and index instead of reprocessing every file.`,
      ],
    };
  }
  return {
    tone: 'ok',
    lines: ['No Cairn metadata here yet. Adding it will index the media in place.'],
  };
}

/** The last segment of a path, used as the default library name. */
function folderName(path: string): string {
  const parts = path.trim().split('/').filter(Boolean);
  return parts[parts.length - 1] ?? '';
}

const iconFolderOpen = (
  <svg
    viewBox="0 0 24 24"
    width="18"
    height="18"
    aria-hidden="true"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.8"
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    <path d="M3 8V6a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v1" />
    <path d="M3.5 19h14a2 2 0 0 0 1.9-1.4l1.6-6A1 1 0 0 0 20 10.4H7a2 2 0 0 0-1.9 1.4L3 19z" />
  </svg>
);

/** Walks the server's folders one level at a time and reports the chosen one. */
function ServerFolderBrowser({
  start,
  onChoose,
  onCancel,
}: {
  start: string;
  onChoose: (path: string) => void;
  onCancel: () => void;
}) {
  const [target, setTarget] = useState(start);
  const [listing, setListing] = useState<DirListing | null>(null);
  const [failure, setFailure] = useState<{ target: string; message: string } | null>(null);

  useEffect(() => {
    let cancelled = false;
    browseServerDirs(target || undefined)
      .then((resp) => {
        if (cancelled) return;
        setListing(resp);
        setFailure(null);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setFailure({ target, message: e instanceof Error ? e.message : String(e) });
      });
    return () => {
      cancelled = true;
    };
  }, [target]);

  return (
    <div className="folder-browser" data-testid="server-folder-browser">
      <div className="folder-browser-bar">
        <button
          type="button"
          className="button"
          onClick={() => listing && setTarget(listing.parent)}
          disabled={!listing || listing.roots}
        >
          ↑ Up
        </button>
        <button
          type="button"
          className="button"
          onClick={() => setTarget('')}
          disabled={!listing || listing.roots}
        >
          Drives
        </button>
        <code className="folder-browser-path" title={listing?.path}>
          {listing ? (listing.roots ? 'Choose a drive or folder' : listing.path) : '…'}
        </code>
      </div>
      <ul className="folder-browser-list">
        {failure && failure.target === target && <li className="error-text">{failure.message}</li>}
        {listing && listing.dirs.length === 0 && <li className="muted">No sub-folders here.</li>}
        {listing?.dirs.map((dir) => (
          <li key={dir.path}>
            <button
              type="button"
              className="folder-browser-item"
              onClick={() => setTarget(dir.path)}
            >
              <span aria-hidden="true">{listing?.roots ? '💽' : '📁'}</span> {dir.name}
            </button>
          </li>
        ))}
      </ul>
      <div className="folder-browser-actions">
        <button type="button" className="button" onClick={onCancel}>
          Cancel
        </button>
        <button
          type="button"
          className="button primary-button"
          onClick={() => listing && onChoose(listing.path)}
          disabled={!listing || listing.roots}
          data-testid="choose-folder"
        >
          Use this folder
        </button>
      </div>
    </div>
  );
}

function AddLibraryDialog({ onClose }: { onClose: () => void }) {
  const { refresh, selectLibrary } = useLibraries();
  const [path, setPath] = useState('');
  const [name, setName] = useState('');
  // Until the person types a name of their own, the library is named after its
  // folder.
  const [nameTouched, setNameTouched] = useState(false);
  const [browsing, setBrowsing] = useState(false);
  const [probe, setProbe] = useState<LibraryProbe | null>(null);
  const [probing, setProbing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const choosePath = (next: string) => {
    setPath(next);
    setProbe(null);
    if (!nameTouched) setName(folderName(next));
  };

  const runProbe = async () => {
    const target = path.trim();
    if (!target) return;
    setProbing(true);
    setError(null);
    setProbe(null);
    try {
      const resp = await probeLibrary(target);
      setProbe(resp.probe);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setProbing(false);
    }
  };

  const submit = async () => {
    const target = path.trim();
    if (!target) return;
    setBusy(true);
    setError(null);
    try {
      const resp = await registerLibrary(target, name.trim() || undefined);
      refresh();
      selectLibrary(resp.library.id);
      onClose();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const described = probe ? describeProbe(probe) : null;
  const blocked =
    probe !== null && ((probe.path_exists && !probe.is_directory) || probe.registered);

  return (
    <Dialog
      open
      size="medium"
      title="Add a library"
      onClose={onClose}
      dismissible={!busy}
      testId="add-library-dialog"
      footer={
        <>
          <button type="button" className="button" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="button"
            onClick={() => void runProbe()}
            disabled={busy || probing || path.trim() === ''}
          >
            {probing ? 'Checking…' : 'Check path'}
          </button>
          <button
            type="button"
            className="button primary-button"
            onClick={() => void submit()}
            disabled={busy || path.trim() === '' || blocked}
            data-testid="confirm-add-library"
          >
            {busy ? 'Adding…' : probe?.has_metadata ? 'Open existing library' : 'Add library'}
          </button>
        </>
      }
    >
      <div className="library-form">
        <div className="library-field">
          <label htmlFor="library-path">Folder on this server</label>
          <div className="library-path-row">
            <input
              id="library-path"
              type="text"
              className="library-input"
              value={path}
              onChange={(e) => choosePath(e.target.value)}
              placeholder="/mnt/photos"
              autoFocus
              data-testid="library-path-input"
            />
            <button
              type="button"
              className="button library-browse"
              onClick={() => setBrowsing((open) => !open)}
              aria-expanded={browsing}
              title="Browse folders on the server"
              data-testid="browse-folders"
            >
              {iconFolderOpen}
              <span>Browse</span>
            </button>
          </div>
          <p className="library-hint">
            An absolute path on the machine running Cairn. Cairn indexes the media where it already
            lives.
          </p>
        </div>

        {browsing && (
          <ServerFolderBrowser
            start={path.trim().startsWith('/') ? path.trim() : ''}
            onChoose={(chosen) => {
              choosePath(chosen);
              setBrowsing(false);
            }}
            onCancel={() => setBrowsing(false)}
          />
        )}

        <div className="library-field">
          <label htmlFor="library-name">Name</label>
          <input
            id="library-name"
            type="text"
            className="library-input"
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              setNameTouched(true);
            }}
            placeholder="Photos"
          />
          <p className="library-hint">Filled in from the folder name — change it if you like.</p>
        </div>

        {described && (
          <div
            className={`library-probe library-probe-${described.tone}`}
            role="status"
            data-testid="library-probe"
          >
            {described.lines.map((line) => (
              <p key={line}>{line}</p>
            ))}
            {probe?.has_metadata && (
              <p className="muted">
                Its tags, albums, faces, and memories travel with the library, so it can be
                unplugged and reopened on another machine.
              </p>
            )}
          </div>
        )}

        {error && (
          <p className="error-text" role="alert">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  );
}

/** Reconnect an offline library that is back at a new mount point. */
function ReconnectDialog({ library, onClose }: { library: Library; onClose: () => void }) {
  const { refresh } = useLibraries();
  const [path, setPath] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      await refreshLibrary(library.id, path.trim() || undefined);
      refresh();
      onClose();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open
      title={`Reconnect ${library.name}`}
      onClose={onClose}
      dismissible={!busy}
      testId="reconnect-dialog"
      footer={
        <>
          <button type="button" className="button" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="button primary-button"
            onClick={() => void submit()}
            disabled={busy}
            data-testid="confirm-reconnect"
          >
            {busy ? 'Reconnecting…' : path.trim() ? 'Reconnect here' : 'Check again'}
          </button>
        </>
      }
    >
      <div className="library-form">
        <p>
          If the drive came back at a different path, give Cairn the new location. It will recognize
          the same library by its stored identity, so nothing is reindexed.
        </p>
        <p className="muted">
          Last known path: <code>{library.root}</code>
        </p>
        <label className="library-field">
          <span>New path (optional)</span>
          <input
            type="text"
            className="library-input"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            placeholder="/media/backup-drive/Photos"
            data-testid="reconnect-path-input"
          />
        </label>
        {error && (
          <p className="error-text" role="alert">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  );
}

function LibraryRow({ library, isAdmin }: { library: Library; isAdmin: boolean }) {
  const { refresh, selectLibrary, libraryId } = useLibraries();
  const [status, setStatus] = useState<IndexStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [reconnecting, setReconnecting] = useState(false);
  const [confirmUnregister, setConfirmUnregister] = useState(false);

  const loadStatus = useCallback(() => {
    getIndexStatus(library.id)
      .then((resp) => setStatus(resp.status ?? null))
      .catch(() => setStatus(null));
  }, [library.id]);

  useEffect(() => {
    loadStatus();
  }, [loadStatus]);

  const offline = library.status === 'offline';

  const run = async (action: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await action();
      loadStatus();
      refresh();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <li className="library-row" data-testid="library-row" data-library-id={library.id}>
      <div className="library-row-main">
        <div className="library-row-titles">
          <h2>
            {library.name} <LibraryStatusBadge status={library.status} />
          </h2>
          <p className="muted library-row-path" title={library.root}>
            {library.root}
          </p>
          {status && (status.present ?? 0) + (status.missing ?? 0) > 0 && (
            <p className="muted library-row-index">
              {status.present} files indexed
              {status.missing ? ` · ${status.missing} unavailable` : ''}
              {status.active_job ? ' · scan in progress' : ''}
            </p>
          )}
        </div>

        <div className="library-row-actions">
          {!offline && (
            <button
              type="button"
              className="button"
              onClick={() => selectLibrary(library.id)}
              disabled={libraryId === library.id}
            >
              {libraryId === library.id ? 'Selected' : 'Open'}
            </button>
          )}
          {offline && isAdmin && (
            <button
              type="button"
              className="button primary-button"
              onClick={() => setReconnecting(true)}
              data-testid="reconnect-button"
            >
              Reconnect
            </button>
          )}
          {isAdmin && !offline && (
            <button
              type="button"
              className="button"
              onClick={() => void run(() => triggerIndex(library.id))}
              disabled={busy}
              data-testid="index-button"
            >
              {busy ? 'Working…' : 'Scan for changes'}
            </button>
          )}
          {isAdmin && (
            <button
              type="button"
              className="button"
              onClick={() => void run(() => refreshLibrary(library.id))}
              disabled={busy}
            >
              Re-check
            </button>
          )}
          {isAdmin && (
            <button
              type="button"
              className="button danger-button"
              onClick={() => setConfirmUnregister(true)}
              disabled={busy}
            >
              Unregister
            </button>
          )}
        </div>
      </div>

      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}

      {reconnecting && <ReconnectDialog library={library} onClose={() => setReconnecting(false)} />}

      <ConfirmDialog
        open={confirmUnregister}
        title={`Unregister ${library.name}?`}
        destructive
        confirmLabel="Unregister"
        message={
          <>
            <p>
              Cairn will stop listing this library. <strong>Your files are not touched</strong> —
              everything stays exactly where it is, including the <code>.cairn</code> metadata
              directory.
            </p>
            <p>You can add the same folder again later and it will pick up where it left off.</p>
          </>
        }
        onCancel={() => setConfirmUnregister(false)}
        onConfirm={() => {
          setConfirmUnregister(false);
          void run(() => unregisterLibrary(library.id));
        }}
        testId="unregister-dialog"
      />
    </li>
  );
}

export default function LibrariesPage() {
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  const { libraries, loading, error, refresh } = useLibraries();
  const [adding, setAdding] = useState(false);

  return (
    <main className="page libraries-page">
      <PageHeader
        title="Libraries"
        subtitle="Folders of media that Cairn indexes in place."
        controls={
          isAdmin ? (
            <>
              <LibraryPicker />
              <button
                type="button"
                className="button primary-button"
                onClick={() => setAdding(true)}
                data-testid="add-library-button"
              >
                Add a library
              </button>
            </>
          ) : (
            <LibraryPicker />
          )
        }
      />

      {loading && <LoadingState label="Loading libraries…" />}

      {error && <ErrorState message={error} onRetry={refresh} />}

      {!loading && !error && libraries.length === 0 && (
        <EmptyState title="No libraries yet" testId="libraries-empty">
          <p className="muted">
            {isAdmin
              ? 'Add a folder of photos, videos, or files and Cairn will index it in place.'
              : 'You have not been given access to a library yet. Ask an administrator to grant you one.'}
          </p>
        </EmptyState>
      )}

      {libraries.length > 0 && (
        <ul className="library-list" data-testid="library-list">
          {libraries.map((lib) => (
            <LibraryRow key={lib.id} library={lib} isAdmin={isAdmin} />
          ))}
        </ul>
      )}

      {!isAdmin && libraries.length > 0 && (
        <p className="muted libraries-note">
          Adding, reconnecting, and scanning libraries is an administrator task. You can browse
          everything you have been given access to.
        </p>
      )}

      {adding && <AddLibraryDialog onClose={() => setAdding(false)} />}
    </main>
  );
}

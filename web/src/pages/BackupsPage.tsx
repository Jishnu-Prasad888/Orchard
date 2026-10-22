/**
 * Backups — run one, verify one, restore one.
 *
 * Backup policy is server-wide rather than per library, so this page is not
 * library-scoped and has no library picker. It is also admin-only: writing a
 * copy of every library is not something a member should be able to trigger.
 *
 * A backup is metadata, not a download — the bytes live in the destination
 * directory the server chose. The page therefore reports *where* and *how much*
 * rather than pretending to be a file browser, and a backup on the same device
 * as the original is called out, because it is not protection against losing the
 * machine.
 */

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/authContext';
import { useResource } from '../api/resources';
import { BACKUP_TRIGGER_LABEL } from '../api/types';
import { BackupRemote } from '../components/backups/BackupRemote';
import { BackupWhen } from '../components/backups/BackupWhen';
import {
  exportBookMetadataUrl,
  getBackup,
  getBackupConfig,
  listBackups,
  restoreBackup,
  runBackup,
  setBackupDir,
  verifyBackup,
  getBackupPolicy,
  listBackupDrives,
} from '../api/queries';
import type {
  BackupConfig,
  BackupRecord,
  BackupPolicy,
  BackupDrive,
  BackupDriveStatus,
} from '../api/types';
import { formatBytes } from '../api/types';
import { ConfirmDialog, Dialog, PromptDialog } from '../components/Dialog';
import { EmptyState, ErrorState, LoadingState, PageHeader } from '../components/States';
import './BackupsPage.css';

const STATUS_LABEL: Record<string, string> = {
  running: 'Running',
  completed: 'Completed',
  failed: 'Failed',
  pruned: 'Removed (old)',
  restoring: 'Restoring',
};

function StatusBadge({ record }: { record: BackupRecord }) {
  const ok = record.status === 'completed';
  const failed = record.status === 'failed';
  return (
    <span
      className={
        failed
          ? 'status-badge status-badge-offline'
          : ok
            ? 'status-badge status-badge-online'
            : 'status-badge'
      }
      data-testid={`backup-status-${record.id}`}
    >
      {STATUS_LABEL[record.status] ?? record.status}
    </span>
  );
}

const DIR_SOURCE_NOTE: Record<string, string> = {
  setting: 'Chosen here.',
  env: 'Set by the ORCHARD_BACKUP_DIR environment variable. Choosing a folder here overrides it.',
  default:
    "Orchard's default, inside its data directory. Pick a folder on another drive to protect against losing this one.",
};

/** Which drive the folder is on, and whether it is there. */
function DriveLine({ drive }: { drive: BackupDriveStatus }) {
  if (!drive.connected) {
    return (
      <p
        className="error-text backup-drive-missing"
        role="alert"
        data-testid="backup-drive-problem"
      >
        {drive.problem}
      </p>
    );
  }
  return (
    <p className="muted backup-location-note" data-testid="backup-drive">
      {drive.system
        ? 'On the system disk — a separate drive protects against losing this one.'
        : `On ${drive.mount} (${drive.device}, ${drive.fs_type})`}
      {drive.total > 0 && ` · ${formatBytes(drive.free)} free of ${formatBytes(drive.total)}`}
    </p>
  );
}

/** Where backups are written, with a way to change it. */
function BackupLocation({
  config,
  onChanged,
}: {
  config: BackupConfig | null;
  onChanged: (next: BackupConfig) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [drives, setDrives] = useState<BackupDrive[] | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = () => {
    setSaving(true);
    setError(null);
    setBackupDir(draft.trim())
      .then((next) => {
        onChanged(next);
        setEditing(false);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setSaving(false));
  };

  return (
    <section className="backup-location" aria-labelledby="backup-location-heading">
      <div className="backup-location-text">
        <h2 id="backup-location-heading">Backup location</h2>
        {!editing && (
          <>
            <p className="backup-location-path" data-testid="backup-dir">
              {config?.dir || <span className="muted">Not set</span>}
            </p>
            {config?.dir_source && (
              <p className="muted backup-location-note">{DIR_SOURCE_NOTE[config.dir_source]}</p>
            )}
            {config?.dir && config.drive && <DriveLine drive={config.drive} />}
            {config?.destination === 'remote' && (
              <p className="muted backup-location-note" data-testid="backup-dir-unused">
                Not in use: backups go to the network destination below.
              </p>
            )}
          </>
        )}
        {editing && (
          <form
            className="backup-location-form"
            onSubmit={(e) => {
              e.preventDefault();
              save();
            }}
          >
            <label className="field">
              <span>Folder on the server (absolute path)</span>
              <input
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                placeholder="/mnt/backup-drive/orchard"
                spellCheck={false}
                autoFocus
                data-testid="backup-dir-input"
              />
            </label>
            <p className="muted backup-location-note">
              Created if it doesn't exist. Earlier backups stay where they are.
            </p>
            {drives && drives.length > 0 && (
              <div className="backup-drives">
                <p className="backup-drives-title">Mounted drives</p>
                <ul data-testid="backup-drives">
                  {drives.map((d) => (
                    <li key={d.path}>
                      <button
                        type="button"
                        className="backup-drive"
                        onClick={() =>
                          setDraft(
                            `${d.path === '/' ? '/srv' : d.path.replace(/\/$/, '')}/orchard-backups`,
                          )
                        }
                      >
                        <span className="backup-drive-path">{d.path}</span>
                        <span className="muted">
                          {d.system ? 'System disk' : `${d.device} · ${d.fs_type}`}
                          {d.total > 0 &&
                            ` · ${formatBytes(d.free)} free of ${formatBytes(d.total)}`}
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
                <p className="muted backup-location-note">
                  Orchard remembers which drive you pick, and won't write backups to the system disk
                  in its place while it is unplugged.
                </p>
              </div>
            )}
            {error && (
              <p className="error-text" role="alert">
                {error}
              </p>
            )}
            <div className="backup-location-actions">
              <button
                type="submit"
                className="button primary-button"
                disabled={saving || !draft.trim()}
              >
                {saving ? 'Saving…' : 'Save'}
              </button>
              <button
                type="button"
                className="button"
                onClick={() => setEditing(false)}
                disabled={saving}
              >
                Cancel
              </button>
            </div>
          </form>
        )}
      </div>
      {!editing && (
        <button
          type="button"
          className="button"
          onClick={() => {
            setDraft(config?.dir ?? '');
            setError(null);
            setEditing(true);
            listBackupDrives()
              .then(setDrives)
              .catch(() => setDrives([]));
          }}
          data-testid="change-backup-dir"
        >
          {config?.dir ? 'Change' : 'Choose folder'}
        </button>
      )}
    </section>
  );
}

function BackupDetailDialog({
  record,
  onClose,
  onRestored,
}: {
  record: BackupRecord;
  onClose: () => void;
  onRestored: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [verified, setVerified] = useState<BackupRecord | null>(null);
  const [restoring, setRestoring] = useState(false);
  const shown = verified ?? record;

  const verify = () => {
    setBusy(true);
    setError(null);
    verifyBackup(record.id)
      .then(setVerified)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };

  return (
    <>
      <Dialog
        open
        size="medium"
        title={`Backup of ${new Date(shown.started_at).toLocaleString()}`}
        onClose={onClose}
        dismissible={!busy}
        testId="backup-detail-dialog"
        footer={
          <>
            <button
              type="button"
              className="button"
              onClick={verify}
              disabled={busy || shown.status !== 'completed'}
              data-testid="verify-backup"
            >
              {busy ? 'Verifying…' : 'Verify'}
            </button>
            <button
              type="button"
              className="button primary-button"
              onClick={() => setRestoring(true)}
              disabled={busy || shown.status !== 'completed'}
              data-testid="open-restore-backup"
            >
              Restore…
            </button>
            <button type="button" className="button" onClick={onClose} disabled={busy}>
              Close
            </button>
          </>
        }
      >
        <dl className="backup-detail">
          <div>
            <dt>Destination</dt>
            <dd>
              <code>{shown.destination}</code>
            </dd>
          </div>
          <div>
            <dt>Started</dt>
            <dd>{new Date(shown.started_at).toLocaleString()}</dd>
          </div>
          {shown.finished_at && (
            <div>
              <dt>Finished</dt>
              <dd>{new Date(shown.finished_at).toLocaleString()}</dd>
            </div>
          )}
          <div>
            <dt>Libraries</dt>
            <dd>{shown.libraries}</dd>
          </div>
          <div>
            <dt>Files</dt>
            <dd>
              {shown.files} copied
              {shown.files_skipped > 0 ? `, ${shown.files_skipped} already present` : ''}
            </dd>
          </div>
          <div>
            <dt>Size</dt>
            <dd>
              {formatBytes(shown.bytes)} of media → {formatBytes(shown.stored_bytes)} stored
            </dd>
          </div>
          <div>
            <dt>Encryption</dt>
            <dd>{shown.encrypted ? 'Encrypted at rest' : 'Not encrypted'}</dd>
          </div>
          <div>
            <dt>On the same device</dt>
            <dd>
              {shown.same_device
                ? 'Yes — this would not survive losing the machine'
                : 'No — this is on different storage'}
            </dd>
          </div>
          {shown.verify_status && (
            <div>
              <dt>Verification</dt>
              <dd>
                {shown.verify_status}: {shown.verify_checked} checked, {shown.verify_errors} errors
              </dd>
            </div>
          )}
          {shown.error_msg && (
            <div>
              <dt>Error</dt>
              <dd className="error-text">{shown.error_msg}</dd>
            </div>
          )}
        </dl>

        {error && (
          <p className="error-text" role="alert">
            {error}
          </p>
        )}
      </Dialog>

      {restoring && (
        <PromptDialog
          open
          title="Restore this backup"
          label="Destination directory"
          placeholder="/srv/orchard-restore"
          confirmLabel="Restore"
          hint="Files are written here. Orchard's own libraries are not modified — restore to a new directory and point a library at it."
          busy={busy}
          onCancel={() => setRestoring(false)}
          onConfirm={(destination) => {
            setBusy(true);
            setError(null);
            restoreBackup(record.id, destination)
              .then(() => {
                setRestoring(false);
                onRestored();
              })
              .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
              .finally(() => setBusy(false));
          }}
          testId="restore-backup-dialog"
        />
      )}
    </>
  );
}

export default function BackupsPage() {
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';

  // `isAdmin` gates the load, not just the render: a member must not fire a
  // request that can only come back 403.
  const backups = useResource(
    useCallback(() => listBackups(), []),
    [],
    isAdmin,
  );
  const config = useResource(
    useCallback(() => getBackupConfig(), []),
    [],
    isAdmin,
  );
  const [savedConfig, setSavedConfig] = useState<BackupConfig | null>(null);
  const policy = useResource(
    useCallback(() => getBackupPolicy(), []),
    [],
    isAdmin,
  );
  const [savedPolicy, setSavedPolicy] = useState<BackupPolicy | null>(null);
  const shownPolicy = savedPolicy ?? policy.data;
  const [detailId, setDetailId] = useState<string | null>(null);
  const [running, setRunning] = useState(false);
  const [confirmRun, setConfirmRun] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  // Fetch the single record when a row is opened, so the detail is the
  // server's current view rather than whatever the list was holding.
  const detail = useResource(
    useCallback(() => (detailId ? getBackup(detailId) : Promise.resolve(null)), [detailId]),
    [detailId],
    detailId !== null,
  );

  if (!isAdmin) {
    return (
      <main className="page backups-page">
        <PageHeader title="Backups" subtitle="Copies of every library, written by the server." />
        <EmptyState title="Administrators only" testId="backups-forbidden">
          <p className="muted">
            Backups cover every library on this server and are written to a destination the server
            owns, so running them is an administrator action. Ask an administrator if you need one.
          </p>
        </EmptyState>
      </main>
    );
  }

  const run = () => {
    setBusy(true);
    setError(null);
    setNotice(null);
    runBackup()
      .then((rec) => {
        setConfirmRun(false);
        setRunning(false);
        setNotice(
          rec.status === 'completed'
            ? `Backed up ${rec.files} files (${formatBytes(rec.stored_bytes)}) to ${rec.destination}.`
            : `Backup finished with status "${rec.status}".`,
        );
        backups.reload();
        // What is pending, and when the schedule next runs, both moved.
        setSavedPolicy(null);
        policy.reload();
      })
      .catch((e: unknown) => {
        // The dialog stays open on failure, with the reason inside it, so the
        // run can be retried without re-confirming.
        setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => setBusy(false));
  };

  const detailRecord = detail.data;
  const latest = backups.data?.[0] ?? null;

  return (
    <main className="page backups-page">
      <PageHeader
        title="Backups"
        subtitle="Copies of every library, written to a destination the server owns. Nothing leaves this machine unless you configure it to."
        controls={
          <>
            <a
              className="button ghost-button"
              href={exportBookMetadataUrl()}
              download
              data-testid="export-book-metadata"
            >
              Export book metadata
            </a>
            <button
              type="button"
              className="button primary-button"
              onClick={() => {
                setRunning(true);
                setConfirmRun(true);
              }}
              disabled={running}
              data-testid="run-backup"
            >
              Back up now
            </button>
          </>
        }
      />

      <BackupLocation
        config={savedConfig ?? config.data}
        onChanged={(next) => {
          setSavedConfig(next);
          setNotice(`Backups will now be written to ${next.dir}.`);
        }}
      />

      {(savedConfig ?? config.data) && (
        <BackupRemote
          config={(savedConfig ?? config.data)!}
          onChanged={(next, message) => {
            setSavedConfig(next);
            setNotice(message);
          }}
        />
      )}

      {shownPolicy && (
        <BackupWhen
          // A fresh policy from the server starts the form over.
          key={JSON.stringify(shownPolicy)}
          policy={shownPolicy}
          onSaved={setSavedPolicy}
        />
      )}

      {/* While the confirm dialog is open it owns the error, so a screen reader
          is not told about the same failure twice. */}
      {error && !confirmRun && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}
      {notice && (
        <p className="muted" role="status" data-testid="backup-notice">
          {notice}
        </p>
      )}

      {latest?.same_device && (
        <p className="muted backups-warning" data-testid="backup-same-device">
          The most recent backup is on the same device as your libraries. It protects against
          accidental deletion, not against losing the machine — point the backup destination at
          separate storage for that.
        </p>
      )}

      {backups.error && <ErrorState message={backups.error} onRetry={backups.reload} />}
      {backups.loading && <LoadingState label="Loading backups…" />}

      {backups.data !== null && backups.data.length === 0 && (
        <EmptyState title="No backups yet" testId="backups-empty">
          <p className="muted">
            Nothing has been backed up. A first run copies every indexed file; later runs skip what
            is already present, so they are quick.
          </p>
        </EmptyState>
      )}

      {backups.data !== null && backups.data.length > 0 && (
        <table className="backup-table" data-testid="backups-table">
          <caption className="visually-hidden">
            Backups with their status, destination, size, and when they ran
          </caption>
          <thead>
            <tr>
              <th scope="col">Started</th>
              <th scope="col">Status</th>
              <th scope="col">Why</th>
              <th scope="col">Destination</th>
              <th scope="col">Files</th>
              <th scope="col">Stored</th>
              <th scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {backups.data.map((record) => (
              <tr key={record.id} className="backup-row">
                <th scope="row">
                  <time dateTime={record.started_at}>
                    {new Date(record.started_at).toLocaleString()}
                  </time>
                </th>
                <td>
                  <StatusBadge record={record} />
                </td>
                <td className="backup-trigger">
                  {BACKUP_TRIGGER_LABEL[record.trigger ?? 'manual'] ?? 'By hand'}
                </td>
                <td className="backup-destination">
                  <code>{record.destination}</code>
                  {record.encrypted && <span className="tag-chip">encrypted</span>}
                  {record.same_device && <span className="tag-chip">same device</span>}
                </td>
                <td>{record.files}</td>
                <td>{formatBytes(record.stored_bytes)}</td>
                <td>
                  <button
                    type="button"
                    className="button"
                    onClick={() => setDetailId(record.id)}
                    data-testid={`backup-open-${record.id}`}
                  >
                    Details
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {detailRecord && (
        <BackupDetailDialog
          record={detailRecord}
          onClose={() => setDetailId(null)}
          onRestored={() => {
            setNotice('Restore started.');
            backups.reload();
            setDetailId(null);
          }}
        />
      )}

      <ConfirmDialog
        open={confirmRun}
        title="Back up every library now?"
        confirmLabel="Back up now"
        busy={busy}
        error={error}
        message={
          <p>
            This copies every indexed file from every library into the configured destination. It
            runs synchronously, so a large library can take a while. Files already present at the
            destination are skipped.
          </p>
        }
        onCancel={() => {
          setConfirmRun(false);
          setRunning(false);
        }}
        onConfirm={run}
        testId="run-backup-dialog"
      />
    </main>
  );
}

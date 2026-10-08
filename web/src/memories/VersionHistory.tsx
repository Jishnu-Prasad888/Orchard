/**
 * VersionHistory — browse earlier saves of a memory and restore one.
 *
 * Restoring replaces the current blocks with the version's blocks as a
 * normal, undoable edit; nothing in history is ever deleted.
 */

import { useEffect, useState } from 'react';

import { listMemoryVersions } from '../api/queries';
import type { MemoryVersion } from '../api/types';
import { Dialog } from '../components/Dialog';
import { getMemoryVersionDetail } from './api';
import { normalizeBlocks } from './model';
import type { MemoryBlock } from './types';

interface Props {
  open: boolean;
  libraryId: string;
  memoryId: string;
  onClose: () => void;
  onRestore: (version: { title: string; blocks: MemoryBlock[] }) => void;
}

export function VersionHistory(props: Props) {
  if (!props.open) return null;
  return <VersionHistoryPanel {...props} />;
}

function VersionHistoryPanel({ open, libraryId, memoryId, onClose, onRestore }: Props) {
  const [versions, setVersions] = useState<MemoryVersion[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<number | null>(null);

  useEffect(() => {
    let cancelled = false;
    listMemoryVersions(libraryId, memoryId)
      .then((r) => !cancelled && setVersions(r.versions ?? []))
      .catch((e: unknown) => !cancelled && setError(e instanceof Error ? e.message : String(e)));
    return () => {
      cancelled = true;
    };
  }, [libraryId, memoryId]);

  const restore = (version: number) => {
    setBusy(version);
    getMemoryVersionDetail(libraryId, memoryId, version)
      .then((r) => {
        onRestore({ title: r.version.title, blocks: normalizeBlocks(r.version.blocks) });
        onClose();
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(null));
  };

  return (
    <Dialog
      open={open}
      title="Version history"
      onClose={onClose}
      size="medium"
      testId="version-history"
    >
      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}
      {versions === null && !error && <p className="muted">Loading…</p>}
      {versions && (
        <ol className="versions">
          {versions.map((v) => (
            <li key={v.version} className="version-row">
              <div>
                <strong>Version {v.version}</strong>
                <span className="muted"> · {new Date(v.saved_at).toLocaleString()}</span>
                <p className="version-excerpt">{v.body.slice(0, 160) || v.title}</p>
              </div>
              <button
                type="button"
                className="button"
                disabled={busy !== null}
                onClick={() => restore(v.version)}
              >
                {busy === v.version ? 'Restoring…' : 'Restore'}
              </button>
            </li>
          ))}
        </ol>
      )}
      <p className="muted">
        Restoring is an ordinary edit — you can undo it, and no version is ever removed.
      </p>
    </Dialog>
  );
}

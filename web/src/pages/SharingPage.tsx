/**
 * Sharing — the links you have handed out.
 *
 * A share is a capability grant on a resource key delivered as an opaque token
 * instead of a session. That makes it easy to leak: the token *is* the
 * credential, and the API only returns it once, at creation. So this page shows
 * the link immediately after it is made, offers a copy button, and is otherwise
 * a list you can revoke.
 *
 * What a link exposes is chosen, never typed (see ShareDialog): a folder, a
 * tag, a collection or the whole library here, and a single file from its
 * right-click menu. The list says what each link shares in words.
 */

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/authContext';
import { useLibraryGate } from '../api/libraries';
import { useLibraryResource } from '../api/resources';
import { listShares, revokeShare } from '../api/queries';
import { CAPABILITY_LABEL, SHARE_KIND_LABEL } from '../api/types';
import type { Share } from '../api/types';
import { ConfirmDialog } from '../components/Dialog';
import { ShareDialog } from '../components/ShareDialog';
import LibraryPicker from '../components/LibraryPicker';
import {
  EmptyState,
  ErrorState,
  LoadingState,
  NoLibrariesState,
  PageHeader,
} from '../components/States';
import './SharingPage.css';

function expired(share: Share): boolean {
  if (share.revoked_at) return true;
  if (!share.expires_at) return false;
  return Date.parse(share.expires_at) < Date.now();
}

export default function SharingPage() {
  const gate = useLibraryGate();
  const { user } = useAuth();

  const [creating, setCreating] = useState(false);
  const [revoking, setRevoking] = useState<Share | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const shares = useLibraryResource(
    useCallback(async (libraryId: string) => (await listShares(libraryId)).shares ?? [], []),
  );

  const revoke = () => {
    if (!revoking || gate.kind !== 'ready') return;
    setBusy(true);
    setError(null);
    revokeShare(gate.libraryId, revoking.id)
      .then(() => {
        setRevoking(null);
        shares.reload();
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };

  if (gate.kind === 'loading') {
    return (
      <main className="page sharing-page">
        <LoadingState label="Loading libraries…" />
      </main>
    );
  }

  const header = (
    <PageHeader
      title="Sharing"
      subtitle="Read-mostly links that let someone without a Orchard account see a resource."
      controls={
        <>
          <LibraryPicker />
          <button
            type="button"
            className="button primary-button"
            onClick={() => setCreating(true)}
            disabled={gate.kind !== 'ready'}
            data-testid="new-share-button"
          >
            New share link
          </button>
        </>
      }
    />
  );

  if (gate.kind === 'error') {
    return (
      <main className="page sharing-page">
        {header}
        <ErrorState message={gate.message} onRetry={shares.reload} />
      </main>
    );
  }

  if (gate.kind === 'empty') {
    return (
      <main className="page sharing-page">
        {header}
        <NoLibrariesState isAdmin={user?.role === 'admin'} />
      </main>
    );
  }

  return (
    <main className="page sharing-page">
      {header}

      {shares.error && <ErrorState message={shares.error} onRetry={shares.reload} />}
      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}

      {shares.loading && <LoadingState />}

      {shares.data !== null && shares.data.length === 0 && (
        <EmptyState title="No share links" testId="shares-empty">
          <p className="muted">
            A share link is a capability grant delivered as a URL instead of a session. Anyone with
            the link — and the password, if you set one — can open it without an account.
          </p>
        </EmptyState>
      )}

      {shares.data !== null && shares.data.length > 0 && (
        <table className="share-table" data-testid="shares-table">
          <caption className="visually-hidden">
            Share links for this library, with their resource, allowed actions, and status
          </caption>
          <thead>
            <tr>
              <th scope="col">Resource</th>
              <th scope="col">Allowed</th>
              <th scope="col">Protection</th>
              <th scope="col">Expires</th>
              <th scope="col">Status</th>
              <th scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {shares.data.map((share) => {
              const isExpired = expired(share);
              return (
                <tr key={share.id} className="share-row">
                  <th scope="row" className="share-key">
                    <span className="share-target-kind">
                      {SHARE_KIND_LABEL[share.target_kind] ?? 'Link'}
                    </span>
                    <span className="share-target-name">{share.target_name}</span>
                  </th>
                  <td>
                    <span className="share-caps-list">
                      {share.capabilities.map((c) => (
                        <span key={c} className="tag-chip">
                          {CAPABILITY_LABEL[c] ?? c}
                        </span>
                      ))}
                    </span>
                  </td>
                  <td>{share.has_password ? 'Password' : '—'}</td>
                  <td>
                    {share.expires_at ? (
                      <time dateTime={share.expires_at}>
                        {new Date(share.expires_at).toLocaleDateString()}
                      </time>
                    ) : (
                      'Never'
                    )}
                  </td>
                  <td>
                    {share.revoked_at ? (
                      <span className="status-badge status-badge-offline">Revoked</span>
                    ) : isExpired ? (
                      <span className="status-badge status-badge-offline">Expired</span>
                    ) : (
                      <span className="status-badge status-badge-online">Active</span>
                    )}
                  </td>
                  <td>
                    <button
                      type="button"
                      className="button danger-button"
                      onClick={() => setRevoking(share)}
                      disabled={Boolean(share.revoked_at)}
                    >
                      Revoke
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}

      {shares.data !== null && shares.data.some((s) => !expired(s)) && (
        <p className="muted share-note">
          The link itself is not shown here on purpose — Orchard only ever returns the token once,
          at creation. If you lose it, revoke the share and make a new one.
        </p>
      )}

      {gate.kind === 'ready' && (
        <ShareDialog
          libraryId={gate.libraryId}
          open={creating}
          onClose={() => setCreating(false)}
          onCreated={() => shares.reload()}
        />
      )}

      <ConfirmDialog
        open={revoking !== null}
        title="Revoke this share link?"
        destructive
        confirmLabel="Revoke"
        busy={busy}
        error={error}
        message={
          <p>
            Anyone holding the link loses access immediately. The files themselves are not affected,
            and you can create a new link at any time.
          </p>
        }
        onCancel={() => setRevoking(null)}
        onConfirm={revoke}
        testId="revoke-share-dialog"
      />
    </main>
  );
}

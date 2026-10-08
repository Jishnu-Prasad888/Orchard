/**
 * Sharing — the links you have handed out.
 *
 * A share is a capability grant on a resource key delivered as an opaque token
 * instead of a session. That makes it easy to leak: the token *is* the
 * credential, and the API only returns it once, at creation. So this page shows
 * the link immediately after it is made, offers a copy button, and is otherwise
 * a list you can revoke.
 *
 * The resource key is the part people get wrong, so it is editable with the
 * scope in a hint rather than hidden behind a dropdown of the whole tree.
 */

import { useCallback, useState } from 'react';

import { useAuth } from '../auth/authContext';
import { useLibraryGate } from '../api/libraries';
import { useLibraryResource } from '../api/resources';
import { createShare, listShares, revokeShare } from '../api/queries';
import { ALL_CAPABILITIES, CAPABILITY_LABEL } from '../api/types';
import type { Capability, Share } from '../api/types';
import { ConfirmDialog, Dialog } from '../components/Dialog';
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

function NewShareDialog({
  libraryId,
  onClose,
  onCreated,
}: {
  libraryId: string;
  onClose: () => void;
  onCreated: (share: Share, url: string) => void;
}) {
  const [resourceKey, setResourceKey] = useState(`library:${libraryId}`);
  const [caps, setCaps] = useState<Capability[]>(['read']);
  const [password, setPassword] = useState('');
  const [expires, setExpires] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const toggleCap = (cap: Capability) =>
    setCaps((prev) => (prev.includes(cap) ? prev.filter((c) => c !== cap) : [...prev, cap]));

  const submit = () => {
    setBusy(true);
    setError(null);
    createShare(libraryId, {
      key: resourceKey.trim(),
      caps,
      ...(password ? { password } : {}),
      ...(expires ? { expires_at: new Date(expires).toISOString() } : {}),
    })
      .then((resp) => {
        onCreated(resp.share, `${window.location.origin}/s/${resp.token}`);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };

  return (
    <Dialog
      open
      size="medium"
      title="Create a share link"
      onClose={onClose}
      dismissible={!busy}
      testId="new-share-dialog"
      footer={
        <>
          <button type="button" className="button" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="button primary-button"
            onClick={submit}
            disabled={busy || resourceKey.trim() === '' || caps.length === 0}
            data-testid="confirm-new-share"
          >
            {busy ? 'Creating…' : 'Create link'}
          </button>
        </>
      }
    >
      <div className="share-form">
        <label className="share-field">
          <span>What the link exposes</span>
          <input
            type="text"
            className="share-input"
            value={resourceKey}
            onChange={(e) => setResourceKey(e.target.value)}
            data-testid="share-resource-key"
          />
        </label>
        <p className="share-hint">
          A whole library is <code>library:&lt;id&gt;</code>. One file is{' '}
          <code>file:&lt;library-id&gt;/&lt;relative-path&gt;</code> — the viewer’s Share tab
          creates those for you.
        </p>

        <fieldset className="share-caps">
          <legend>Allowed actions</legend>
          {ALL_CAPABILITIES.map((cap) => (
            <label key={cap} className="share-cap">
              <input type="checkbox" checked={caps.includes(cap)} onChange={() => toggleCap(cap)} />
              <span>{CAPABILITY_LABEL[cap]}</span>
            </label>
          ))}
        </fieldset>
        <p className="share-hint">
          Grant only what the visitor needs. <strong>Read</strong> alone is the safe default;
          anything that writes is a decision to make on purpose.
        </p>

        <div className="share-form-row">
          <label className="share-field">
            <span>Password (optional)</span>
            <input
              type="password"
              className="share-input"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="new-password"
              data-testid="share-password"
            />
          </label>
          <label className="share-field">
            <span>Expires (optional)</span>
            <input
              type="datetime-local"
              className="share-input"
              value={expires}
              onChange={(e) => setExpires(e.target.value)}
            />
          </label>
        </div>

        {error && (
          <p className="error-text" role="alert">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  );
}

export default function SharingPage() {
  const gate = useLibraryGate();
  const { user } = useAuth();

  const [creating, setCreating] = useState(false);
  const [revoking, setRevoking] = useState<Share | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fresh, setFresh] = useState<{ share: Share; url: string } | null>(null);
  const [copied, setCopied] = useState(false);

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

  const copyFresh = async () => {
    if (!fresh) return;
    try {
      await navigator.clipboard.writeText(fresh.url);
      setCopied(true);
    } catch {
      setCopied(false);
    }
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
      subtitle="Read-mostly links that let someone without a Cairn account see a resource."
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

      {fresh && (
        <div className="share-fresh" role="status" data-testid="fresh-share">
          <h2>Your link is ready</h2>
          <p className="muted">
            This is the only time Cairn shows the token. Copy it now — it cannot be recovered later,
            only replaced.
          </p>
          <div className="share-fresh-row">
            <label className="visually-hidden" htmlFor="fresh-share-url">
              Share link
            </label>
            <input id="fresh-share-url" className="share-input" readOnly value={fresh.url} />
            <button type="button" className="button" onClick={() => void copyFresh()}>
              {copied ? 'Copied' : 'Copy link'}
            </button>
            <button type="button" className="button" onClick={() => setFresh(null)}>
              Done
            </button>
          </div>
        </div>
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
                  <th scope="row" className="share-key" title={share.resource_key}>
                    <code>{share.resource_key}</code>
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
          The link itself is not shown here on purpose — Cairn only ever returns the token once, at
          creation. If you lose it, revoke the share and make a new one.
        </p>
      )}

      {creating && gate.kind === 'ready' && (
        <NewShareDialog
          libraryId={gate.libraryId}
          onClose={() => setCreating(false)}
          onCreated={(share, url) => {
            setCreating(false);
            setFresh({ share, url });
            setCopied(false);
            shares.reload();
          }}
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

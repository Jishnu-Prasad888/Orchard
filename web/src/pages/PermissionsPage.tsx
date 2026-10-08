/**
 * Permissions — who can do what in this library, and where.
 *
 * A grant is `(user, resource key, capabilities, effect)` and the resource key
 * is hierarchical: `library:<id>` covers everything beneath it, and a grant on
 * `library:<id>/2024` covers only that subtree. That inheritance is the part
 * worth making visible, so every row shows the full key and the page explains
 * that a narrower key is what you want almost every time.
 *
 * Listing and writing grants both require `manage` on the library. Choosing a
 * user needs the admin user list, which a non-admin manager cannot read — so
 * the form falls back to typing a user id rather than pretending the picker is
 * empty.
 */

import { useCallback, useMemo, useState } from 'react';

import { useAuth } from '../auth/authContext';
import { useLibraryGate } from '../api/libraries';
import { useLibraryResource, useResource } from '../api/resources';
import { createGrant, listGrants, listUsers, revokeGrant } from '../api/queries';
import { ALL_CAPABILITIES, CAPABILITY_LABEL } from '../api/types';
import type { Capability, PermissionGrant, User } from '../api/types';
import { ConfirmDialog, Dialog } from '../components/Dialog';
import LibraryPicker from '../components/LibraryPicker';
import {
  EmptyState,
  ErrorState,
  LoadingState,
  NoLibrariesState,
  PageHeader,
} from '../components/States';
import './PermissionsPage.css';

interface GrantFormState {
  userId: string;
  scope: 'library' | 'folder' | 'file';
  path: string;
  effect: 'allow' | 'deny';
  caps: Capability[];
}

const EMPTY_FORM: GrantFormState = {
  userId: '',
  scope: 'library',
  path: '',
  effect: 'allow',
  caps: ['read'],
};

/**
 * Turn the form into a resource key.
 *
 * A bare path is interpreted against the scope the user picked, because
 * `2024/summer` means a folder here and a file in the file scope — the server
 * would read it the same way but showing the resulting key is what makes the
 * inheritance legible.
 */
function resourceKey(libraryId: string, form: GrantFormState): string {
  const path = form.path.trim().replace(/^\/+|\/+$/g, '');
  if (form.scope === 'library' || path === '') return `library:${libraryId}`;
  if (form.scope === 'folder') return `library:${libraryId}/${path}`;
  return `file:${libraryId}/${path}`;
}

function GrantDialog({
  libraryId,
  users,
  canListUsers,
  onClose,
  onCreated,
}: {
  libraryId: string;
  users: User[];
  canListUsers: boolean;
  onClose: () => void;
  onCreated: () => void;
}) {
  const [form, setForm] = useState<GrantFormState>(EMPTY_FORM);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const toggleCap = (cap: Capability) =>
    setForm((prev) => ({
      ...prev,
      caps: prev.caps.includes(cap) ? prev.caps.filter((c) => c !== cap) : [...prev.caps, cap],
    }));

  const key = resourceKey(libraryId, form);

  const submit = () => {
    setBusy(true);
    setError(null);
    createGrant(libraryId, {
      user_id: form.userId.trim(),
      key,
      caps: form.caps,
      effect: form.effect,
    })
      .then(() => {
        onCreated();
        onClose();
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };

  return (
    <Dialog
      open
      size="medium"
      title="Add a permission grant"
      onClose={onClose}
      dismissible={!busy}
      testId="new-grant-dialog"
      footer={
        <>
          <button type="button" className="button" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="button primary-button"
            onClick={submit}
            disabled={busy || form.userId.trim() === '' || form.caps.length === 0}
            data-testid="confirm-new-grant"
          >
            {busy ? 'Saving…' : 'Add grant'}
          </button>
        </>
      }
    >
      <div className="grant-form">
        {canListUsers && users.length > 0 ? (
          <label className="grant-field">
            <span>Who</span>
            <select
              className="grant-input"
              value={form.userId}
              onChange={(e) => setForm({ ...form, userId: e.target.value })}
              data-testid="grant-user"
            >
              <option value="">Choose a person…</option>
              {users.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.username} ({u.role})
                </option>
              ))}
            </select>
          </label>
        ) : (
          <label className="grant-field">
            <span>User ID</span>
            <input
              className="grant-input"
              value={form.userId}
              onChange={(e) => setForm({ ...form, userId: e.target.value })}
              placeholder="The id from the user list"
              data-testid="grant-user-id"
            />
          </label>
        )}

        <div className="grant-scope">
          <label className="grant-field">
            <span>Applies to</span>
            <select
              className="grant-input"
              value={form.scope}
              onChange={(e) =>
                setForm({ ...form, scope: e.target.value as GrantFormState['scope'] })
              }
              data-testid="grant-scope"
            >
              <option value="library">The whole library</option>
              <option value="folder">A folder</option>
              <option value="file">A single file</option>
            </select>
          </label>
          {form.scope !== 'library' && (
            <label className="grant-field">
              <span>Path (relative to the library root)</span>
              <input
                className="grant-input"
                value={form.path}
                onChange={(e) => setForm({ ...form, path: e.target.value })}
                placeholder="2024/summer"
                data-testid="grant-path"
              />
            </label>
          )}
        </div>

        <p className="grant-key">
          Grants on <code data-testid="grant-key">{key}</code> apply to everything beneath it.
        </p>

        <fieldset className="grant-caps">
          <legend>Capabilities</legend>
          {ALL_CAPABILITIES.map((cap) => (
            <label key={cap} className="grant-cap">
              <input
                type="checkbox"
                checked={form.caps.includes(cap)}
                onChange={() => toggleCap(cap)}
                data-testid={`grant-cap-${cap}`}
              />
              <span>{CAPABILITY_LABEL[cap]}</span>
            </label>
          ))}
        </fieldset>

        <fieldset className="grant-caps">
          <legend>Effect</legend>
          {(['allow', 'deny'] as const).map((effect) => (
            <label key={effect} className="grant-cap">
              <input
                type="radio"
                name="grant-effect"
                checked={form.effect === effect}
                onChange={() => setForm({ ...form, effect })}
              />
              <span>{effect === 'allow' ? 'Allow' : 'Deny'}</span>
            </label>
          ))}
        </fieldset>
        <p className="grant-hint">
          A <strong>deny</strong> wins over an allow at the same or a deeper key, which is how you
          carve one private folder out of a library everyone else can see.
        </p>

        {error && (
          <p className="error-text" role="alert">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  );
}

export default function PermissionsPage() {
  const gate = useLibraryGate();
  const { user } = useAuth();

  const [creating, setCreating] = useState(false);
  const [revoking, setRevoking] = useState<PermissionGrant | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const grants = useLibraryResource(
    useCallback(async (libraryId: string) => (await listGrants(libraryId)).grants ?? [], []),
  );

  // Listing users is admin-only, so a non-admin manager cannot read it. The
  // grant form falls back to a typed user id rather than an empty picker.
  const isAdmin = user?.role === 'admin';
  const users = useResource(
    useCallback(() => listUsers(), []),
    [],
    isAdmin,
  );
  const userNames = useMemo(() => {
    const map = new Map<string, string>();
    for (const u of users.data?.users ?? []) map.set(u.id, u.username);
    return map;
  }, [users.data]);

  const revoke = () => {
    if (!revoking || gate.kind !== 'ready') return;
    setBusy(true);
    setError(null);
    revokeGrant(gate.libraryId, revoking.id)
      .then(() => {
        setRevoking(null);
        grants.reload();
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };

  if (gate.kind === 'loading') {
    return (
      <main className="page permissions-page">
        <LoadingState label="Loading libraries…" />
      </main>
    );
  }

  const header = (
    <PageHeader
      title="Permissions"
      subtitle="Who can do what in this library, and how far down the tree it reaches."
      controls={
        <>
          <LibraryPicker />
          <button
            type="button"
            className="button primary-button"
            onClick={() => setCreating(true)}
            disabled={gate.kind !== 'ready'}
            data-testid="new-grant-button"
          >
            Add grant
          </button>
        </>
      }
    />
  );

  if (gate.kind === 'error') {
    return (
      <main className="page permissions-page">
        {header}
        <ErrorState message={gate.message} onRetry={grants.reload} />
      </main>
    );
  }

  if (gate.kind === 'empty') {
    return (
      <main className="page permissions-page">
        {header}
        <NoLibrariesState isAdmin={user?.role === 'admin'} />
      </main>
    );
  }

  return (
    <main className="page permissions-page">
      {header}

      {grants.error && <ErrorState message={grants.error} onRetry={grants.reload} />}
      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}
      {grants.loading && <LoadingState />}

      {grants.data !== null && grants.data.length === 0 && (
        <EmptyState title="No permission grants" testId="grants-empty">
          <p className="muted">
            Without a grant, only administrators can see this library. A grant gives one person a
            capability on one branch of the tree.
          </p>
        </EmptyState>
      )}

      {grants.data !== null && grants.data.length > 0 && (
        <table className="grant-table" data-testid="grants-table">
          <caption className="visually-hidden">
            Permission grants in this library, with the person, the resource key, the capabilities,
            and whether they are allowed or denied
          </caption>
          <thead>
            <tr>
              <th scope="col">Person</th>
              <th scope="col">Applies to</th>
              <th scope="col">Capabilities</th>
              <th scope="col">Effect</th>
              <th scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {grants.data.map((grant) => (
              <tr key={grant.id} className="grant-row">
                <th scope="row" className="grant-person">
                  {userNames.get(grant.user_id) ?? grant.user_id}
                  {!userNames.has(grant.user_id) && isAdmin && (
                    <span className="muted grant-user-id"> {grant.user_id}</span>
                  )}
                </th>
                <td className="grant-key">
                  <code>{grant.resource_key}</code>
                </td>
                <td>
                  <span className="grant-caps-list">
                    {grant.capabilities.map((cap) => (
                      <span key={cap} className="tag-chip">
                        {CAPABILITY_LABEL[cap] ?? cap}
                      </span>
                    ))}
                  </span>
                </td>
                <td>
                  <span
                    className={
                      grant.effect === 'deny'
                        ? 'status-badge status-badge-offline'
                        : 'status-badge status-badge-online'
                    }
                  >
                    {grant.effect === 'deny' ? 'Deny' : 'Allow'}
                  </span>
                </td>
                <td>
                  <button
                    type="button"
                    className="button danger-button"
                    onClick={() => setRevoking(grant)}
                    data-testid={`revoke-grant-${grant.id}`}
                  >
                    Revoke
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {creating && (
        <GrantDialog
          libraryId={gate.libraryId}
          users={users.data?.users ?? []}
          canListUsers={isAdmin}
          onClose={() => setCreating(false)}
          onCreated={() => grants.reload()}
        />
      )}

      <ConfirmDialog
        open={revoking !== null}
        title="Revoke this grant?"
        destructive
        confirmLabel="Revoke"
        busy={busy}
        error={error}
        message={
          <p>
            {revoking && (
              <>
                <strong>{userNames.get(revoking.user_id) ?? revoking.user_id}</strong> will lose{' '}
                {revoking.capabilities.join(', ')} on <code>{revoking.resource_key}</code>. Anything
                they could already do there becomes inaccessible immediately.
              </>
            )}
          </p>
        }
        onCancel={() => setRevoking(null)}
        onConfirm={revoke}
        testId="revoke-grant-dialog"
      />
    </main>
  );
}

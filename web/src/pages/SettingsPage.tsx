/**
 * Settings — one page of quiet rows, grouped into sections.
 *
 * Every setting is a row: what it is, a sentence on what it does, and the
 * control. Sections are anchored (`/settings#appearance`) so a link can land
 * on one. Technical detail — versions, health, server tasks — lives at the end
 * under Advanced and About, available to the people who want it and out of
 * everyone else's way.
 */

import { type FormEvent, type ReactNode, useCallback, useEffect, useState } from 'react';
import { Link, useLocation } from 'react-router-dom';

import { useAuth } from '../auth/authContext';
import { apiGet } from '../api/client';
import { useResource } from '../api/resources';
import {
  approveSignup,
  createUser,
  deleteUser,
  denySignup,
  listPendingSignups,
  listUsers,
  revokeUserSessions,
  setSignupRequestsEnabled,
  updateUser,
} from '../api/queries';
import type { HealthResponse, PendingSignup, User, UserRole, VersionResponse } from '../api/types';
import Brand from '../components/Brand';
import { ConfirmDialog } from '../components/Dialog';
import { DictionarySettings } from '../components/settings/DictionarySettings';
import { PageHeader } from '../components/States';
import { Icon, type IconName } from '../components/ui/Icon';
import { formatDate } from '../lib/dates';
import { type ThemePreference, readStoredTheme, storeTheme } from '../lib/theme';
import './SettingsPage.css';

const THEME_OPTIONS: Array<{ value: ThemePreference; label: string; icon: IconName }> = [
  { value: 'system', label: 'Match system', icon: 'monitor' },
  { value: 'light', label: 'Light', icon: 'sun' },
  { value: 'dark', label: 'Dark', icon: 'moon' },
];

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function Section({
  id,
  title,
  description,
  children,
}: {
  id: string;
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <section className="settings-section" id={id} aria-labelledby={`settings-${id}`}>
      <div className="settings-section-head">
        <h2 id={`settings-${id}`}>{title}</h2>
        {description && <p className="settings-section-description">{description}</p>}
      </div>
      <div className="settings-rows">{children}</div>
    </section>
  );
}

function Row({
  title,
  description,
  children,
}: {
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <div className="settings-row">
      <div className="settings-row-text">
        <span className="settings-row-title">{title}</span>
        {description && <span className="settings-row-description">{description}</span>}
      </div>
      {children && <div className="settings-row-control">{children}</div>}
    </div>
  );
}

/** A row that goes somewhere else: the whole row is the link. */
function LinkRow({
  to,
  icon,
  title,
  description,
}: {
  to: string;
  icon: IconName;
  title: string;
  description: string;
}) {
  return (
    <Link to={to} className="settings-row settings-link-row">
      <Icon name={icon} className="settings-row-icon" />
      <div className="settings-row-text">
        <span className="settings-row-title">{title}</span>
        <span className="settings-row-description">{description}</span>
      </div>
      <Icon name="chevron-right" className="settings-row-chevron" />
    </Link>
  );
}

/** One pending "request an account" row, with its own role choice for approval. */
function PendingSignupRow({
  request,
  onApprove,
  onDeny,
  busy,
}: {
  request: PendingSignup;
  onApprove: (id: string, role: UserRole) => void;
  onDeny: (id: string) => void;
  busy: boolean;
}) {
  const [role, setRole] = useState<UserRole>('user');
  return (
    <li className="settings-row">
      <span className="settings-avatar settings-avatar-sm" aria-hidden="true">
        {request.username.slice(0, 1).toUpperCase()}
      </span>
      <div className="settings-row-text">
        <span className="settings-row-title">{request.username}</span>
        <span className="settings-row-description">
          Requested {formatDate(request.requested_at)}
        </span>
      </div>
      <div className="settings-row-control settings-signup-controls">
        <select
          aria-label={`Role for ${request.username}`}
          value={role}
          onChange={(e) => setRole(e.target.value as UserRole)}
          disabled={busy}
        >
          <option value="user">Member</option>
          <option value="admin">Administrator</option>
        </select>
        <button
          type="button"
          className="button primary-button"
          onClick={() => onApprove(request.id, role)}
          disabled={busy}
        >
          Approve
        </button>
        <button
          type="button"
          className="button ghost-button"
          onClick={() => onDeny(request.id)}
          disabled={busy}
        >
          Deny
        </button>
      </div>
    </li>
  );
}

export default function SettingsPage() {
  const { user, logout, signupRequestsEnabled, refresh: refreshAuth } = useAuth();
  const isAdmin = user?.role === 'admin';
  const { hash } = useLocation();

  // Arriving from the settings search (or a link) at /settings#section.
  useEffect(() => {
    if (!hash) return;
    document.getElementById(hash.slice(1))?.scrollIntoView({ block: 'start' });
  }, [hash]);

  const [theme, setTheme] = useState<ThemePreference>(() => readStoredTheme());

  const users = useResource<User[]>(
    useCallback(async () => (await listUsers()).users ?? [], []),
    [],
    isAdmin,
  );

  const server = useResource(
    useCallback(async () => {
      const [health, version] = await Promise.all([
        apiGet<HealthResponse>('/health'),
        apiGet<VersionResponse>('/version'),
      ]);
      return { health, version };
    }, []),
  );

  const [revoking, setRevoking] = useState<User | null>(null);
  const [revokeBusy, setRevokeBusy] = useState(false);
  const [revokeError, setRevokeError] = useState<string | null>(null);

  const [newUsername, setNewUsername] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [newRole, setNewRole] = useState<UserRole>('user');
  const [createBusy, setCreateBusy] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [created, setCreated] = useState<string | null>(null);

  const onThemeChange = (next: ThemePreference) => {
    setTheme(next);
    storeTheme(next);
  };

  const confirmRevoke = () => {
    if (!revoking) return;
    setRevokeBusy(true);
    setRevokeError(null);
    revokeUserSessions(revoking.id)
      .then(() => {
        setRevoking(null);
        users.reload();
      })
      .catch((e: unknown) => setRevokeError(message(e)))
      .finally(() => setRevokeBusy(false));
  };

  const create = (event: FormEvent) => {
    event.preventDefault();
    const name = newUsername.trim();
    if (name === '' || newPassword === '') {
      setCreateError('Enter a username and a password.');
      return;
    }
    setCreateBusy(true);
    setCreateError(null);
    setCreated(null);
    createUser({ username: name, password: newPassword, role: newRole })
      .then((resp) => {
        setCreated(`Created ${resp.user.username}.`);
        setNewUsername('');
        setNewPassword('');
        setNewRole('user');
        users.reload();
      })
      .catch((e: unknown) => setCreateError(message(e)))
      .finally(() => setCreateBusy(false));
  };

  const [roleBusyId, setRoleBusyId] = useState<string | null>(null);
  const [roleError, setRoleError] = useState<string | null>(null);
  const changeRole = (target: User, role: UserRole) => {
    setRoleBusyId(target.id);
    setRoleError(null);
    updateUser(target.id, { role })
      .then(() => users.reload())
      .catch((e: unknown) => setRoleError(message(e)))
      .finally(() => setRoleBusyId(null));
  };

  const [enabledBusyId, setEnabledBusyId] = useState<string | null>(null);
  const [enabledError, setEnabledError] = useState<string | null>(null);
  const toggleEnabled = (target: User) => {
    setEnabledBusyId(target.id);
    setEnabledError(null);
    updateUser(target.id, { enabled: !target.enabled })
      .then(() => users.reload())
      .catch((e: unknown) => setEnabledError(message(e)))
      .finally(() => setEnabledBusyId(null));
  };

  const [deleting, setDeleting] = useState<User | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const confirmDelete = () => {
    if (!deleting) return;
    setDeleteBusy(true);
    setDeleteError(null);
    deleteUser(deleting.id)
      .then(() => {
        setDeleting(null);
        users.reload();
      })
      .catch((e: unknown) => setDeleteError(message(e)))
      .finally(() => setDeleteBusy(false));
  };

  const pending = useResource<PendingSignup[]>(
    useCallback(async () => (await listPendingSignups()).requests ?? [], []),
    [],
    isAdmin,
  );
  const [signupActionId, setSignupActionId] = useState<string | null>(null);
  const [signupActionError, setSignupActionError] = useState<string | null>(null);
  const approveRequest = (id: string, role: UserRole) => {
    setSignupActionId(id);
    setSignupActionError(null);
    approveSignup(id, role)
      .then(() => {
        pending.reload();
        users.reload();
      })
      .catch((e: unknown) => setSignupActionError(message(e)))
      .finally(() => setSignupActionId(null));
  };
  const denyRequest = (id: string) => {
    setSignupActionId(id);
    setSignupActionError(null);
    denySignup(id)
      .then(() => pending.reload())
      .catch((e: unknown) => setSignupActionError(message(e)))
      .finally(() => setSignupActionId(null));
  };

  const [signupToggleBusy, setSignupToggleBusy] = useState(false);
  const [signupToggleError, setSignupToggleError] = useState<string | null>(null);
  const toggleSignupRequests = (enabled: boolean) => {
    setSignupToggleBusy(true);
    setSignupToggleError(null);
    setSignupRequestsEnabled(enabled)
      .then(() => refreshAuth())
      .catch((e: unknown) => setSignupToggleError(message(e)))
      .finally(() => setSignupToggleBusy(false));
  };

  const sections: Array<{ id: string; label: string }> = [
    { id: 'account', label: 'Account' },
    { id: 'appearance', label: 'Appearance' },
    { id: 'library', label: 'Library' },
    { id: 'dictionary', label: 'Dictionary' },
    ...(isAdmin
      ? [
          { id: 'accounts', label: 'Accounts' },
          { id: 'advanced', label: 'Advanced' },
        ]
      : [{ id: 'access', label: 'Access' }]),
    { id: 'about', label: 'About' },
  ];

  return (
    <main className="page page-narrow settings-page">
      <PageHeader title="Settings" />

      <div className="settings-layout">
        <nav className="settings-nav" aria-label="Settings sections">
          {sections.map((s) => (
            <a key={s.id} href={`#${s.id}`}>
              {s.label}
            </a>
          ))}
        </nav>

        <div className="settings-content">
          <Section id="account" title="Account">
            <div className="settings-row settings-account">
              <span className="settings-avatar" aria-hidden="true">
                {(user?.username ?? '?').slice(0, 1).toUpperCase()}
              </span>
              <div className="settings-row-text">
                <span className="settings-row-title">{user?.username}</span>
                <span className="settings-row-description">
                  <span className="settings-role" data-role={user?.role}>
                    {user?.role === 'admin' ? 'Administrator' : 'Member'}
                  </span>
                  {user && <> · Joined {formatDate(user.created_at)}</>}
                </span>
              </div>
              <div className="settings-row-control">
                <button type="button" className="button" onClick={() => void logout()}>
                  <Icon name="logout" />
                  Sign out
                </button>
              </div>
            </div>
          </Section>

          <Section id="appearance" title="Appearance">
            <Row
              title="Theme"
              description="Match your device, or always use the light or dark look."
            >
              <div className="segmented settings-themes" role="radiogroup" aria-label="Theme">
                {THEME_OPTIONS.map((option) => (
                  <label
                    key={option.value}
                    className={theme === option.value ? 'segmented-item active' : 'segmented-item'}
                  >
                    <input
                      type="radio"
                      name="theme"
                      className="visually-hidden"
                      value={option.value}
                      checked={theme === option.value}
                      onChange={() => onThemeChange(option.value)}
                    />
                    <Icon name={option.icon} />
                    <span>{option.label}</span>
                  </label>
                ))}
              </div>
            </Row>
          </Section>

          <Section id="library" title="Library" description="These act on your current library.">
            <LinkRow
              to="/tags"
              icon="tag"
              title="Tags"
              description="Labels you apply to files, and browsing by them."
            />
            <LinkRow
              to="/shared"
              icon="share"
              title="Sharing"
              description="Links you've shared, and who can open them."
            />
            <LinkRow
              to="/trash"
              icon="trash"
              title="Trash"
              description="Removed files waiting to be restored or deleted."
            />
            <LinkRow
              to="/duplicates"
              icon="check"
              title="Duplicates"
              description="Files that share exact content with another file."
            />
          </Section>

          <Section
            id="dictionary"
            title="Dictionary"
            description="Look words up while you read: select a word, then right-click it or press and hold."
          >
            <DictionarySettings isAdmin={isAdmin} />
          </Section>

          {isAdmin ? (
            <>
              <Section id="accounts" title="Accounts" description="Who can sign in to Orchard.">
                {users.error && (
                  <p className="error-text" role="alert">
                    {users.error}
                  </p>
                )}
                {users.loading && !users.error && <p className="muted">Loading accounts…</p>}
                {(roleError || enabledError || deleteError) && (
                  <p className="error-text" role="alert">
                    {roleError ?? enabledError ?? deleteError}
                  </p>
                )}
                {users.data !== null && users.data.length > 0 && (
                  <ul className="settings-user-list" data-testid="settings-user-list">
                    {users.data.map((row) => (
                      <li key={row.id} className="settings-row">
                        <span className="settings-avatar settings-avatar-sm" aria-hidden="true">
                          {row.username.slice(0, 1).toUpperCase()}
                        </span>
                        <div className="settings-row-text">
                          <span className="settings-row-title">
                            {row.username}
                            {row.id === user?.id && <span className="muted"> (you)</span>}
                          </span>
                          <span className="settings-row-description">
                            <span className="settings-role" data-role={row.role}>
                              {row.role === 'admin' ? 'Administrator' : 'Member'}
                            </span>{' '}
                            · Joined {formatDate(row.created_at)}
                            {!row.enabled && (
                              <>
                                {' · '}
                                <span className="status-badge status-badge-offline">Disabled</span>
                              </>
                            )}
                          </span>
                        </div>
                        <div className="settings-row-control settings-account-controls">
                          <select
                            aria-label={`Role for ${row.username}`}
                            value={row.role}
                            onChange={(e) => changeRole(row, e.target.value as UserRole)}
                            disabled={roleBusyId === row.id}
                          >
                            <option value="user">Member</option>
                            <option value="admin">Administrator</option>
                          </select>
                          <button
                            type="button"
                            className="button ghost-button"
                            onClick={() => toggleEnabled(row)}
                            disabled={enabledBusyId === row.id}
                          >
                            {row.enabled ? 'Disable' : 'Enable'}
                          </button>
                          <button
                            type="button"
                            className="button ghost-button"
                            onClick={() => {
                              setRevokeError(null);
                              setRevoking(row);
                            }}
                          >
                            Revoke sessions
                          </button>
                          <button
                            type="button"
                            className="button danger-button"
                            onClick={() => {
                              setDeleteError(null);
                              setDeleting(row);
                            }}
                          >
                            Delete
                          </button>
                        </div>
                      </li>
                    ))}
                  </ul>
                )}

                {(pending.data?.length ?? 0) > 0 && (
                  <div className="settings-pending">
                    <h3>Pending requests</h3>
                    {signupActionError && (
                      <p className="error-text" role="alert">
                        {signupActionError}
                      </p>
                    )}
                    <ul className="settings-user-list" data-testid="settings-pending-list">
                      {pending.data?.map((req) => (
                        <PendingSignupRow
                          key={req.id}
                          request={req}
                          onApprove={approveRequest}
                          onDeny={denyRequest}
                          busy={signupActionId === req.id}
                        />
                      ))}
                    </ul>
                  </div>
                )}

                <Row
                  title="Allow account requests"
                  description="Let visitors without an account ask for one from the sign-in page. You approve or deny each request."
                >
                  <label className="settings-toggle">
                    <input
                      type="checkbox"
                      checked={signupRequestsEnabled}
                      onChange={(e) => toggleSignupRequests(e.target.checked)}
                      disabled={signupToggleBusy}
                    />
                    <span>{signupRequestsEnabled ? 'On' : 'Off'}</span>
                  </label>
                </Row>
                {signupToggleError && (
                  <p className="error-text" role="alert">
                    {signupToggleError}
                  </p>
                )}

                <form className="settings-create" onSubmit={(e) => void create(e)}>
                  <h3>Add an account</h3>
                  <div className="settings-create-fields">
                    <label className="field">
                      <span className="field-label">Username</span>
                      <input
                        type="text"
                        aria-label="New username"
                        value={newUsername}
                        onChange={(e) => setNewUsername(e.target.value)}
                        disabled={createBusy}
                      />
                    </label>
                    <label className="field">
                      <span className="field-label">Password</span>
                      <input
                        type="password"
                        aria-label="New password"
                        autoComplete="new-password"
                        value={newPassword}
                        onChange={(e) => setNewPassword(e.target.value)}
                        disabled={createBusy}
                      />
                    </label>
                    <label className="field">
                      <span className="field-label">Role</span>
                      <select
                        aria-label="New account role"
                        value={newRole}
                        onChange={(e) => setNewRole(e.target.value as UserRole)}
                        disabled={createBusy}
                      >
                        <option value="user">Member</option>
                        <option value="admin">Administrator</option>
                      </select>
                    </label>
                    <button type="submit" className="button primary-button" disabled={createBusy}>
                      {createBusy ? 'Creating…' : 'Create account'}
                    </button>
                  </div>
                  {createError && (
                    <p className="error-text" role="alert">
                      {createError}
                    </p>
                  )}
                  {created && (
                    <p className="settings-created" role="status">
                      {created}
                    </p>
                  )}
                </form>
              </Section>

              <Section
                id="advanced"
                title="Advanced"
                description="Server-wide tasks for administrators."
              >
                <LinkRow
                  to="/libraries"
                  icon="drive"
                  title="Libraries"
                  description="Add, reconnect, re-index, and remove storage."
                />
                <LinkRow
                  to="/permissions"
                  icon="lock"
                  title="Permissions"
                  description="Who can see, change, and share each library."
                />
                <LinkRow
                  to="/backups"
                  icon="archive"
                  title="Backups"
                  description="Take, verify, and restore snapshots of Orchard's data."
                />
              </Section>
            </>
          ) : (
            <Section id="access" title="Access">
              <Row
                title="Your access"
                description="You can browse and organize the libraries shared with you. Managing libraries is for administrators."
              />
            </Section>
          )}

          <Section id="about" title="About">
            <Row title={<Brand>Orchard</Brand>} description="Your personal library." />
            {server.loading && <Row title="Server" description="Checking…" />}
            {server.error && (
              <Row title="Server" description={<span className="error-text">{server.error}</span>}>
                <button type="button" className="button" onClick={server.reload}>
                  Retry
                </button>
              </Row>
            )}
            {server.data && (
              <>
                <Row
                  title="Server"
                  description={
                    server.data.health.status === 'ok'
                      ? 'Running normally'
                      : 'Running, with problems'
                  }
                >
                  <span
                    className={
                      server.data.health.status === 'ok'
                        ? 'status-badge status-badge-online'
                        : 'status-badge status-badge-warning'
                    }
                  >
                    {server.data.health.status === 'ok' ? 'Healthy' : 'Degraded'}
                  </span>
                </Row>
                <dl className="settings-facts">
                  <div>
                    <dt>Version</dt>
                    <dd>{server.data.version.version}</dd>
                  </div>
                  <div>
                    <dt>Commit</dt>
                    <dd>{server.data.version.commit}</dd>
                  </div>
                  <div>
                    <dt>Database</dt>
                    <dd>{server.data.health.database}</dd>
                  </div>
                  {server.data.version.platform && (
                    <div>
                      <dt>Platform</dt>
                      <dd>{server.data.version.platform}</dd>
                    </div>
                  )}
                </dl>
              </>
            )}
          </Section>
        </div>
      </div>

      <ConfirmDialog
        open={revoking !== null}
        title="Sign this account out everywhere?"
        confirmLabel="Revoke sessions"
        busy={revokeBusy}
        error={revokeError}
        message={
          <p>
            <strong>{revoking?.username}</strong> will be signed out of every device and browser.
            They will need to sign in again.
          </p>
        }
        onCancel={() => setRevoking(null)}
        onConfirm={confirmRevoke}
        testId="revoke-sessions-dialog"
      />

      <ConfirmDialog
        open={deleting !== null}
        title="Delete this account?"
        destructive
        confirmLabel="Delete"
        busy={deleteBusy}
        error={deleteError}
        message={
          <p>
            <strong>{deleting?.username}</strong> and everything tied only to their account will be
            removed. This cannot be undone.
          </p>
        }
        onCancel={() => setDeleting(null)}
        onConfirm={confirmDelete}
        testId="delete-account-dialog"
      />
    </main>
  );
}

/**
 * Backing up to another machine: an SFTP, FTP(S) or SMB server.
 *
 * Saving always connects first, writes a small file and removes it, so a
 * destination that is saved is one that worked. The first time Orchard meets
 * a server it cannot vouch for (any SSH server; an FTPS server with a
 * self-signed certificate) it shows the server's fingerprint and waits for a
 * person to confirm it; after that, a different identity is refused. The
 * password or key is sealed with a key file on the server and never comes
 * back to the page.
 */

import { useState } from 'react';

import { ApiError } from '../../api/client';
import { setBackupRemote, testBackupRemote, useLocalBackups } from '../../api/queries';
import type { BackupConfig, BackupRemoteConfig, BackupRemoteSecret } from '../../api/types';

const KIND_LABEL: Record<BackupRemoteConfig['kind'], string> = {
  sftp: 'SFTP (SSH)',
  ftp: 'FTP / FTPS',
  smb: 'SMB (Windows share, NAS)',
};

const DEFAULT_PORT: Record<string, number> = { sftp: 22, smb: 445, ftp: 21, ftps_implicit: 990 };

interface Identity {
  fingerprint: string;
  changed: boolean;
}

function emptyConfig(): BackupRemoteConfig {
  return { kind: 'sftp', host: '', user: '', path: 'orchard-backups', ftp_security: 'explicit' };
}

export function BackupRemote({
  config,
  onChanged,
}: {
  config: BackupConfig;
  onChanged: (next: BackupConfig, message: string) => void;
}) {
  const saved = config.remote;
  const active = config.destination === 'remote';
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<BackupRemoteConfig>(saved?.config ?? emptyConfig());
  const [password, setPassword] = useState('');
  const [useKey, setUseKey] = useState(false);
  const [privateKey, setPrivateKey] = useState('');
  const [keyPassphrase, setKeyPassphrase] = useState('');
  const [busy, setBusy] = useState<'test' | 'save' | 'local' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tested, setTested] = useState(false);
  const [identity, setIdentity] = useState<Identity | null>(null);

  // A saved password only belongs to the same server and account.
  const sameServer =
    saved !== undefined &&
    saved.has_secret &&
    saved.config.kind === draft.kind &&
    saved.config.host.toLowerCase() === draft.host.trim().toLowerCase() &&
    (saved.config.user ?? '') === (draft.user ?? '') &&
    (saved.config.port ?? 0) === (draft.port ?? 0);
  const typedSecret = useKey ? privateKey.trim() !== '' : password !== '';

  const secret = (): BackupRemoteSecret | undefined => {
    if (!typedSecret) return undefined; // keep the saved one
    return useKey
      ? { private_key: privateKey, ...(keyPassphrase ? { key_passphrase: keyPassphrase } : {}) }
      : { password };
  };

  const set = <K extends keyof BackupRemoteConfig>(key: K, value: BackupRemoteConfig[K]) => {
    setTested(false);
    setIdentity(null);
    setDraft((d) => {
      const next = { ...d, [key]: value };
      // A different server is a different identity to confirm.
      if (key === 'host' || key === 'port' || key === 'kind') delete next.fingerprint;
      return next;
    });
  };

  const handle = (e: unknown) => {
    if (e instanceof ApiError && e.code === 'SERVER_IDENTITY' && e.details) {
      setIdentity({
        fingerprint: String(e.details.fingerprint ?? ''),
        changed: Boolean(e.details.changed),
      });
      setError(null);
      return;
    }
    setError(e instanceof Error ? e.message : String(e));
  };

  const run = (kind: 'test' | 'save', cfg: BackupRemoteConfig = draft) => {
    setBusy(kind);
    setError(null);
    setIdentity(null);
    const clean = { ...cfg, host: cfg.host.trim() };
    const call =
      kind === 'test' ? testBackupRemote(clean, secret()) : setBackupRemote(clean, secret());
    call
      .then((result) => {
        if (kind === 'test') {
          setTested(true);
          return;
        }
        setEditing(false);
        setPassword('');
        setPrivateKey('');
        setKeyPassphrase('');
        onChanged(result as BackupConfig, `Backups will now go to ${describe(clean)}.`);
      })
      .catch(handle)
      .finally(() => setBusy(null));
  };

  const trust = () => {
    if (!identity) return;
    const next = { ...draft, fingerprint: identity.fingerprint };
    setDraft(next);
    setIdentity(null);
    run('test', next);
  };

  const useLocal = () => {
    setBusy('local');
    setError(null);
    useLocalBackups()
      .then((next) => onChanged(next, `Backups will now be written to ${next.dir}.`))
      .catch(handle)
      .finally(() => setBusy(null));
  };

  return (
    <section className="backup-remote" aria-labelledby="backup-remote-heading">
      <div className="backup-remote-head">
        <div>
          <h2 id="backup-remote-heading">Network destination</h2>
          {saved && !editing && (
            <p className="backup-location-path" data-testid="backup-remote-current">
              {describe(saved.config)}
              {active ? (
                <span className="status-badge status-badge-online">In use</span>
              ) : (
                <span className="status-badge">Not in use</span>
              )}
            </p>
          )}
          {!saved && !editing && (
            <p className="muted backup-location-note">
              Send backups to another machine — a NAS, a server, another computer — over SFTP,
              FTP(S) or SMB.
            </p>
          )}
        </div>
        {!editing && (
          <div className="backup-remote-actions">
            {saved && active && (
              <button type="button" className="button" onClick={useLocal} disabled={busy !== null}>
                Use the folder instead
              </button>
            )}
            {saved && !active && (
              <button
                type="button"
                className="button"
                onClick={() => run('save', saved.config)}
                disabled={busy !== null}
                data-testid="backup-remote-use"
              >
                {busy === 'save' ? 'Connecting…' : 'Use it again'}
              </button>
            )}
            <button
              type="button"
              className="button"
              onClick={() => {
                setDraft(saved?.config ?? emptyConfig());
                setEditing(true);
                setTested(false);
                setError(null);
              }}
              data-testid="backup-remote-edit"
            >
              {saved ? 'Change' : 'Set up'}
            </button>
          </div>
        )}
      </div>

      {!config.encrypted && (saved || editing) && (
        <p className="muted backups-warning" data-testid="backup-remote-unencrypted">
          These backups leave this machine unencrypted. Set ORCHARD_BACKUP_PASSPHRASE on the server
          (before the first network backup) to encrypt them.
        </p>
      )}

      {editing && (
        <form
          className="backup-remote-form"
          onSubmit={(e) => {
            e.preventDefault();
            run('save');
          }}
        >
          <div className="backup-remote-grid">
            <label className="field">
              <span>Protocol</span>
              <select
                value={draft.kind}
                onChange={(e) => set('kind', e.target.value as BackupRemoteConfig['kind'])}
                data-testid="remote-kind"
              >
                {(Object.keys(KIND_LABEL) as BackupRemoteConfig['kind'][]).map((k) => (
                  <option key={k} value={k}>
                    {KIND_LABEL[k]}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>Server</span>
              <input
                value={draft.host}
                onChange={(e) => set('host', e.target.value)}
                placeholder="nas.local or 192.168.1.20"
                spellCheck={false}
                autoCapitalize="off"
                data-testid="remote-host"
              />
            </label>
            <label className="field backup-remote-port">
              <span>Port</span>
              <input
                type="number"
                min={1}
                max={65535}
                value={draft.port ?? ''}
                onChange={(e) => set('port', e.target.value ? Number(e.target.value) : undefined)}
                placeholder={String(
                  draft.kind === 'ftp' && draft.ftp_security === 'implicit'
                    ? DEFAULT_PORT.ftps_implicit
                    : DEFAULT_PORT[draft.kind],
                )}
              />
            </label>
          </div>

          {draft.kind === 'ftp' && (
            <label className="field">
              <span>Security</span>
              <select
                value={draft.ftp_security ?? 'explicit'}
                onChange={(e) =>
                  set('ftp_security', e.target.value as BackupRemoteConfig['ftp_security'])
                }
                data-testid="remote-ftp-security"
              >
                <option value="explicit">FTPS — TLS on the usual port (recommended)</option>
                <option value="implicit">FTPS — implicit TLS (port 990)</option>
                <option value="none">Plain FTP — not encrypted</option>
              </select>
            </label>
          )}
          {draft.kind === 'ftp' && draft.ftp_security === 'none' && (
            <p className="error-text" role="note">
              Plain FTP sends the password and every file unencrypted over the network. Use it only
              on a network you trust.
            </p>
          )}

          {draft.kind === 'smb' && (
            <div className="backup-remote-grid">
              <label className="field">
                <span>Share</span>
                <input
                  value={draft.share ?? ''}
                  onChange={(e) => set('share', e.target.value)}
                  placeholder="backups"
                  data-testid="remote-share"
                />
              </label>
              <label className="field">
                <span>Domain (optional)</span>
                <input value={draft.domain ?? ''} onChange={(e) => set('domain', e.target.value)} />
              </label>
            </div>
          )}

          <div className="backup-remote-grid">
            <label className="field">
              <span>User name</span>
              <input
                value={draft.user ?? ''}
                onChange={(e) => set('user', e.target.value)}
                autoComplete="off"
                autoCapitalize="off"
                spellCheck={false}
                data-testid="remote-user"
              />
            </label>
            {!useKey && (
              <label className="field">
                <span>Password</span>
                <input
                  type="password"
                  value={password}
                  onChange={(e) => {
                    setTested(false);
                    setPassword(e.target.value);
                  }}
                  autoComplete="new-password"
                  placeholder={sameServer ? 'Saved — leave empty to keep it' : ''}
                  data-testid="remote-password"
                />
              </label>
            )}
          </div>

          {draft.kind === 'sftp' && (
            <label className="backup-when-check">
              <input
                type="checkbox"
                checked={useKey}
                onChange={(e) => {
                  setTested(false);
                  setUseKey(e.target.checked);
                }}
                data-testid="remote-use-key"
              />
              <span>Sign in with an SSH key instead</span>
            </label>
          )}
          {draft.kind === 'sftp' && useKey && (
            <>
              <label className="field">
                <span>Private key</span>
                <textarea
                  rows={5}
                  value={privateKey}
                  onChange={(e) => setPrivateKey(e.target.value)}
                  placeholder={
                    sameServer
                      ? 'Saved — leave empty to keep it'
                      : '-----BEGIN OPENSSH PRIVATE KEY-----'
                  }
                  spellCheck={false}
                  className="backup-remote-key"
                  data-testid="remote-key"
                />
              </label>
              <label className="field">
                <span>Key passphrase (if the key has one)</span>
                <input
                  type="password"
                  value={keyPassphrase}
                  onChange={(e) => setKeyPassphrase(e.target.value)}
                  autoComplete="off"
                />
              </label>
            </>
          )}

          <label className="field">
            <span>{draft.kind === 'smb' ? 'Folder inside the share' : 'Folder on the server'}</span>
            <input
              value={draft.path}
              onChange={(e) => set('path', e.target.value)}
              placeholder="orchard-backups"
              spellCheck={false}
              data-testid="remote-path"
            />
          </label>
          <p className="muted backup-location-note">
            {draft.kind === 'smb'
              ? 'Many NAS shares keep a recycle bin: old backups Orchard removes may stay there, using space, until it is emptied.'
              : 'Without a leading "/", the folder is inside the account\'s home folder. It is created if missing, and must be empty or hold earlier Orchard backups.'}
          </p>

          {identity && (
            <div
              className={identity.changed ? 'backup-identity is-changed' : 'backup-identity'}
              role="alert"
              data-testid="remote-identity"
            >
              {identity.changed ? (
                <p>
                  <strong>This server's identity has changed</strong> since you confirmed it. That
                  happens when the server is reinstalled — and when someone is intercepting the
                  connection. Only trust it if you know why it changed.
                </p>
              ) : (
                <p>
                  <strong>First connection to {draft.host}.</strong> Check that this is the server's
                  identity before trusting it
                  {draft.kind === 'sftp' &&
                    ' (on the server: ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub)'}
                  :
                </p>
              )}
              <code className="backup-identity-print">{identity.fingerprint}</code>
              <div className="backup-location-actions">
                <button
                  type="button"
                  className={identity.changed ? 'button danger-button' : 'button primary-button'}
                  onClick={trust}
                  data-testid="remote-trust"
                >
                  {identity.changed ? 'Trust the new identity' : 'Trust this server'}
                </button>
              </div>
            </div>
          )}

          {tested && !identity && (
            <p className="backup-when-next" role="status" data-testid="remote-tested">
              Connected, and able to write there.
            </p>
          )}
          {error && (
            <p className="error-text" role="alert">
              {error}
            </p>
          )}

          <div className="backup-location-actions">
            <button
              type="button"
              className="button"
              onClick={() => run('test')}
              disabled={busy !== null || !draft.host.trim()}
              data-testid="remote-test"
            >
              {busy === 'test' ? 'Connecting…' : 'Test connection'}
            </button>
            <button
              type="submit"
              className="button primary-button"
              disabled={busy !== null || !draft.host.trim() || (!typedSecret && !sameServer)}
              data-testid="remote-save"
            >
              {busy === 'save' ? 'Connecting…' : 'Save and use'}
            </button>
            <button
              type="button"
              className="button"
              onClick={() => setEditing(false)}
              disabled={busy !== null}
            >
              Cancel
            </button>
          </div>
        </form>
      )}
    </section>
  );
}

function describe(c: BackupRemoteConfig): string {
  if (c.kind === 'smb') {
    const sub = c.path ? `\\${c.path.replace(/\//g, '\\').replace(/^\\+|\\+$/g, '')}` : '';
    return `\\\\${c.host}\\${c.share ?? ''}${sub}`;
  }
  const port = c.port ? `:${c.port}` : '';
  const path = c.path ? `/${c.path.replace(/^\/+/, '')}` : '';
  return `${c.kind}://${c.user ?? ''}@${c.host}${port}${path}`;
}

import { type FormEvent, useState } from 'react';
import { Link, Navigate, useNavigate } from 'react-router-dom';

import { ApiError } from '../api/client';
import AuthSplash from '../auth/AuthSplash';
import { useAuth } from '../auth/authContext';
import Brand from '../components/Brand';
import './SetupPage.css';

/** Mirrors internal/auth.ValidateCredentials so the form can explain the rules
 * before the request is sent; the server remains the authority. */
const USERNAME_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$/;
const MIN_PASSWORD_LENGTH = 8;
const MAX_PASSWORD_LENGTH = 1024;

/**
 * First-run account creation. Cairn has no accounts until somebody creates
 * one, and the first account is always the administrator. The form is only
 * offered while the server reports `bootstrap_required`; on a server that
 * already has accounts, the same page explains that an administrator creates
 * accounts instead — so the "Create an account" button on the sign-in page
 * always leads somewhere useful and never bypasses that rule.
 */
export default function SetupPage() {
  const { loading, bootstrapRequired, user, bootstrap, refresh } = useAuth();
  const navigate = useNavigate();

  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  if (loading) return <AuthSplash />;
  // Someone is already signed in — nothing to set up.
  if (user) return <Navigate to="/" replace />;
  // An account already exists: bootstrap is closed and only an administrator
  // can add accounts, so say that rather than showing a form that would fail.
  if (!bootstrapRequired) {
    return (
      <main className="auth-page" data-testid="setup-page">
        <section className="auth-card" aria-labelledby="setup-title">
          <header className="auth-header">
            <h1 className="auth-brand">
              <Brand>Cairn</Brand>
            </h1>
            <h2 id="setup-title" className="auth-title">
              This server is already set up
            </h2>
            <p className="auth-subtitle">
              Accounts on this server are created by its administrator. Ask them to add you, then
              come back and sign in.
            </p>
          </header>
          <Link className="auth-secondary" to="/login">
            Back to sign in
          </Link>
        </section>
      </main>
    );
  }

  const validate = (): string | null => {
    const name = username.trim();
    if (name === '') return 'Choose a username.';
    if (!USERNAME_PATTERN.test(name)) {
      return 'Use 1–64 letters, digits, ".", "_" or "-", starting with a letter or digit.';
    }
    if (password.length < MIN_PASSWORD_LENGTH) {
      return `Use a password of at least ${MIN_PASSWORD_LENGTH} characters.`;
    }
    if (password.length > MAX_PASSWORD_LENGTH) {
      return 'That password is too long.';
    }
    if (password !== confirm) return 'The passwords do not match.';
    return null;
  };

  const onSubmit = async (event: FormEvent) => {
    event.preventDefault();
    const problem = validate();
    if (problem) {
      setError(problem);
      return;
    }

    setBusy(true);
    setError(null);
    try {
      await bootstrap(username.trim(), password);
      // The new administrator is signed in by the server response, so go
      // straight to the app.
      navigate('/', { replace: true });
    } catch (e: unknown) {
      // A conflict means somebody else created the first account in the
      // meantime (a second browser tab, say). Re-read the state so the
      // redirects above send the visitor to sign in instead.
      if (e instanceof ApiError && e.status === 409) {
        await refresh();
        return;
      }
      setError(
        e instanceof ApiError
          ? e.message
          : 'Could not reach the Cairn server. Check your connection and try again.',
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="auth-page" data-testid="setup-page">
      <section className="auth-card" aria-labelledby="setup-title">
        <header className="auth-header">
          <h1 className="auth-brand">
            <Brand>Cairn</Brand>
          </h1>
          <h2 id="setup-title" className="auth-title">
            Create your admin account
          </h2>
          <p className="auth-subtitle">
            This is the first account on your Cairn server. As the administrator you can manage
            libraries, add other accounts, and run backups.
          </p>
        </header>

        <form className="auth-form" onSubmit={onSubmit}>
          {/* Fields use explicit label/input association: the password hint
              must not become part of the accessible name. */}
          <div className="auth-field">
            <label className="auth-label" htmlFor="setup-username">
              Username
            </label>
            <input
              className="auth-input"
              id="setup-username"
              type="text"
              name="username"
              autoComplete="username"
              autoFocus
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              disabled={busy}
            />
          </div>

          <div className="auth-field">
            <label className="auth-label" htmlFor="setup-password">
              Password
            </label>
            <input
              className="auth-input"
              id="setup-password"
              type="password"
              name="password"
              autoComplete="new-password"
              aria-describedby="setup-password-hint"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={busy}
            />
            <span className="auth-hint" id="setup-password-hint">
              At least {MIN_PASSWORD_LENGTH} characters.
            </span>
          </div>

          <div className="auth-field">
            <label className="auth-label" htmlFor="setup-confirm">
              Confirm password
            </label>
            <input
              className="auth-input"
              id="setup-confirm"
              type="password"
              name="confirm-password"
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              disabled={busy}
            />
          </div>

          {error && (
            <p className="auth-error" role="alert">
              {error}
            </p>
          )}

          <button type="submit" className="auth-submit" disabled={busy}>
            {busy ? 'Creating your account…' : 'Create account'}
          </button>
        </form>
      </section>
    </main>
  );
}

import { type FormEvent, useState } from 'react';
import { Link, Navigate, useLocation, useNavigate } from 'react-router-dom';

import { ApiError } from '../api/client';
import { useAuth } from '../auth/authContext';
import Brand from '../components/Brand';
import './LoginPage.css';

interface LoginLocationState {
  from?: string;
}

/**
 * Sign-in page for existing accounts. First-run account creation has its own
 * page (/setup), reached from the "Create an account" button below the form.
 * The button is always present so the route is discoverable; on a server that
 * already has accounts, /setup says so instead of offering a form that the
 * server would reject.
 */
export default function LoginPage() {
  const { bootstrapRequired, user, login } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const state = location.state as LoginLocationState | null;
  const from = state?.from && state.from !== '/login' ? state.from : '/';

  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Already signed in (e.g. visited /login directly): skip the form.
  if (user) {
    return <Navigate to={from} replace />;
  }

  const onSubmit = async (event: FormEvent) => {
    event.preventDefault();
    const name = username.trim();
    if (name === '' || password === '') {
      setError('Enter your username and password.');
      return;
    }

    setBusy(true);
    setError(null);
    try {
      await login(name, password);
      navigate(from, { replace: true });
    } catch (e: unknown) {
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
    <main className="auth-page" data-testid="login-page">
      <section className="auth-card" aria-labelledby="login-title">
        <header className="auth-header">
          <h1 className="auth-brand">
            <Brand>Cairn</Brand>
          </h1>
          <h2 id="login-title" className="auth-title">
            Sign in to Cairn
          </h2>
          <p className="auth-subtitle">
            {bootstrapRequired
              ? 'This server has no accounts yet — create the first one to get started.'
              : 'Your photos, files, and memories — kept quietly in your own hands.'}
          </p>
        </header>

        <form className="auth-form" onSubmit={onSubmit}>
          <label className="auth-field">
            <span className="auth-label">Username</span>
            <input
              className="auth-input"
              type="text"
              name="username"
              autoComplete="username"
              autoFocus
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              disabled={busy}
            />
          </label>

          <label className="auth-field">
            <span className="auth-label">Password</span>
            <input
              className="auth-input"
              type="password"
              name="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={busy}
            />
          </label>

          {error && (
            <p className="auth-error" role="alert">
              {error}
            </p>
          )}

          <button type="submit" className="auth-submit" disabled={busy}>
            {busy ? 'Please wait…' : 'Sign in'}
          </button>

          {/* Always shown: on a configured server /setup explains that an
              administrator creates accounts, so the link is never a dead end. */}
          <div className="auth-switch">
            <span className="auth-switch-note">
              {bootstrapRequired ? 'First time on this server?' : 'Need an account?'}
            </span>
            <Link className="auth-secondary" to="/setup">
              Create an account
            </Link>
          </div>
        </form>
      </section>
    </main>
  );
}

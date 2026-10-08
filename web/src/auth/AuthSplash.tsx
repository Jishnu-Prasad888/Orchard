import Brand from '../components/Brand';
import './AuthSplash.css';

/**
 * Full-screen placeholder shown while the session state is being resolved.
 * Used by the route gate and by the first-run setup page, so the app never
 * flashes the wrong screen before the auth status is known.
 */
export default function AuthSplash() {
  return (
    <div className="auth-splash" data-testid="auth-splash">
      <h1 className="auth-splash-brand">
        <Brand>Cairn</Brand>
      </h1>
      <p className="auth-splash-note">Checking your session…</p>
    </div>
  );
}

import { Link, useLocation, useNavigate } from 'react-router-dom';

import Brand from '../components/Brand';
import './ForbiddenPage.css';

interface ForbiddenState {
  message?: string;
}

/**
 * Access-denied page (HTTP 403). It deliberately renders on its own, without
 * the sidebar: several endpoints (library listing in particular) are
 * administrator-only, so a member account can meet 403 on most pages, and a
 * navigation rail full of dead links would be worse than a focused screen.
 *
 * Account settings is the primary action because it is the one destination
 * that always works (and where the user can sign out); "Go back" is offered
 * second, since returning to the page that failed just re-runs the request that
 * was denied.
 */
export default function ForbiddenPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const state = location.state as ForbiddenState | null;

  return (
    <main className="forbidden-page" data-testid="forbidden-page">
      <p className="forbidden-code" aria-hidden="true">
        403
      </p>
      <h1 className="forbidden-title">
        <Brand>Cairn</Brand> can’t open this
      </h1>
      <p className="forbidden-message">
        {state?.message ??
          'You do not have permission to view this page. Ask an administrator for access, or sign in with a different account.'}
      </p>
      <div className="forbidden-actions">
        <Link className="forbidden-primary" to="/settings">
          Account settings
        </Link>
        <button type="button" className="forbidden-secondary" onClick={() => navigate(-1)}>
          Go back
        </button>
      </div>
    </main>
  );
}

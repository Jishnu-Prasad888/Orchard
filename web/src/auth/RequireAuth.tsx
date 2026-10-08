import { useEffect } from 'react';
import { Navigate, Outlet, useLocation, useNavigate } from 'react-router-dom';

import { FORBIDDEN_EVENT, UNAUTHORIZED_EVENT } from '../api/client';
import AuthSplash from './AuthSplash';
import { useAuth } from './authContext';

/** Public pages the gate must never redirect to (it would loop). */
const PUBLIC_PATHS = new Set(['/login', '/setup']);

/**
 * Gate for every authenticated page. While the auth state is loading it shows
 * a splash, then sends anonymous visitors to the right place — first-run setup
 * when the server has no accounts at all, sign-in otherwise (remembering where
 * they were headed) — and renders the app for signed-in users.
 *
 * It also listens for the API layer's auth broadcasts, so neither a permission
 * failure nor an expired session has to be handled by each page:
 * - 403 routes to the access-denied page with the server's own message;
 * - 401 re-reads the auth state, which drops the principal and sends the
 *   visitor back to the sign-in page via the `!user` branch below.
 */
export default function RequireAuth() {
  const { loading, bootstrapRequired, user, refresh } = useAuth();
  const location = useLocation();
  const navigate = useNavigate();

  useEffect(() => {
    const onForbidden = (event: Event) => {
      // Already showing the access-denied page: stay put instead of looping.
      if (location.pathname === '/403') return;
      const detail = (event as CustomEvent<{ message?: string }>).detail;
      navigate('/403', { replace: true, state: { message: detail?.message } });
    };
    const onUnauthorized = () => {
      // A rejected sign-in attempt is reported as 401 too; the sign-in page
      // shows the failure itself, so only re-check when inside the app.
      if (location.pathname === '/login') return;
      void refresh();
    };
    window.addEventListener(FORBIDDEN_EVENT, onForbidden);
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    return () => {
      window.removeEventListener(FORBIDDEN_EVENT, onForbidden);
      window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    };
  }, [location.pathname, navigate, refresh]);

  if (loading) {
    return <AuthSplash />;
  }

  if (!user) {
    // If a public route is nested inside this gate, let it render instead of
    // redirecting to itself (which would loop forever).
    if (PUBLIC_PATHS.has(location.pathname)) {
      return <Outlet />;
    }
    const from = `${location.pathname}${location.search}`;
    if (bootstrapRequired) {
      return <Navigate to="/setup" replace state={{ from }} />;
    }
    return <Navigate to="/login" replace state={{ from }} />;
  }

  return <Outlet />;
}

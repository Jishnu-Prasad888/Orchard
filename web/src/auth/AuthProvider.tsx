import { useCallback, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';

import { apiGet, apiPost } from '../api/client';
import type { AuthStatusResponse, PendingSignup, User } from '../api/types';
import { type AuthState, AuthContext } from './authContext';

/**
 * Holds the session state for the whole app. The public /auth/status endpoint
 * is loaded on mount, so the app can decide between the app itself, the
 * sign-in page, and first-run setup before showing anything.
 */
export default function AuthProvider({ children }: { children: ReactNode }) {
  const [loading, setLoading] = useState(true);
  const [bootstrapRequired, setBootstrapRequired] = useState(false);
  const [signupRequestsEnabled, setSignupRequestsEnabled] = useState(false);
  const [user, setUser] = useState<User | null>(null);

  useEffect(() => {
    let cancelled = false;
    apiGet<AuthStatusResponse>('/auth/status')
      .then((resp) => {
        if (cancelled) return;
        setBootstrapRequired(Boolean(resp.bootstrap_required));
        setSignupRequestsEnabled(Boolean(resp.signup_requests_enabled));
        setUser(resp.authenticated ? (resp.user ?? null) : null);
      })
      .catch(() => {
        // A server that cannot answer is treated as signed out; the sign-in
        // page surfaces the real connection problem.
        if (cancelled) return;
        setBootstrapRequired(false);
        setSignupRequestsEnabled(false);
        setUser(null);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const login = useCallback(async (username: string, password: string) => {
    const resp = await apiPost<{ user: User }>('/auth/login', { username, password });
    setUser(resp.user);
    setBootstrapRequired(false);
    return resp.user;
  }, []);

  const bootstrap = useCallback(async (username: string, password: string) => {
    const resp = await apiPost<{ user: User }>('/auth/bootstrap', { username, password });
    setUser(resp.user);
    setBootstrapRequired(false);
    return resp.user;
  }, []);

  const requestSignup = useCallback(async (username: string, password: string) => {
    const resp = await apiPost<{ request: PendingSignup }>('/auth/signup-requests', {
      username,
      password,
    });
    return resp.request;
  }, []);

  const logout = useCallback(async () => {
    try {
      await apiPost('/auth/logout');
    } finally {
      // The session is gone either way — drop the principal so the shell
      // immediately falls back to the sign-in page.
      setUser(null);
    }
  }, []);

  const refresh = useCallback(async () => {
    try {
      const resp = await apiGet<AuthStatusResponse>('/auth/status');
      setBootstrapRequired(Boolean(resp.bootstrap_required));
      setSignupRequestsEnabled(Boolean(resp.signup_requests_enabled));
      setUser(resp.authenticated ? (resp.user ?? null) : null);
    } catch {
      setUser(null);
    }
  }, []);

  const value = useMemo<AuthState>(
    () => ({
      loading,
      bootstrapRequired,
      signupRequestsEnabled,
      user,
      login,
      bootstrap,
      requestSignup,
      logout,
      refresh,
    }),
    [
      loading,
      bootstrapRequired,
      signupRequestsEnabled,
      user,
      login,
      bootstrap,
      requestSignup,
      logout,
      refresh,
    ],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

import { createContext, useContext } from 'react';

import type { User } from '../api/types';

export interface AuthState {
  /** True until the first /auth/status response has been handled. */
  loading: boolean;
  /** True when the server has no accounts yet and needs the first admin. */
  bootstrapRequired: boolean;
  /** The signed-in principal, or null when anonymous. */
  user: User | null;
  /** Sign in with an existing account. Resolves to the user. */
  login: (username: string, password: string) => Promise<User>;
  /** Create the very first administrator account. Resolves to the user. */
  bootstrap: (username: string, password: string) => Promise<User>;
  /** Revoke the current session and clear the session cookie. */
  logout: () => Promise<void>;
  /** Re-read the authentication state (after external changes). */
  refresh: () => Promise<void>;
}

export const AuthContext = createContext<AuthState | null>(null);

/** Access the authentication state. Must be used inside the AuthProvider. */
export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error('useAuth must be used inside an AuthProvider');
  }
  return ctx;
}

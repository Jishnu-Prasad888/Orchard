import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { FORBIDDEN_EVENT, UNAUTHORIZED_EVENT } from '../api/client';
import AuthProvider from './AuthProvider';
import RequireAuth from './RequireAuth';

const admin = {
  id: 'u1',
  username: 'jishnu',
  role: 'admin' as const,
  created_at: '2026-01-01T00:00:00Z',
};

const originalFetch = globalThis.fetch;

function mockFetch(authenticated: boolean, bootstrapRequired = false) {
  const fn = vi.fn(async (input: URL | RequestInfo) => {
    const url = String(input);
    if (url.endsWith('/api/v1/auth/status')) {
      return new Response(
        JSON.stringify({
          bootstrap_required: bootstrapRequired,
          authenticated,
          ...(authenticated ? { user: admin } : {}),
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      );
    }
    return new Response(
      JSON.stringify({ error: { code: 'NOT_FOUND', message: 'missing', request_id: '1' } }),
      { status: 404, headers: { 'Content-Type': 'application/json' } },
    );
  });
  globalThis.fetch = fn as unknown as typeof fetch;
}

function setup() {
  return render(
    <MemoryRouter initialEntries={['/browse']}>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<div>login page</div>} />
          <Route path="/setup" element={<div>first run setup</div>} />
          <Route element={<RequireAuth />}>
            <Route path="/browse" element={<div>browse page</div>} />
            <Route path="/403" element={<div>access denied page</div>} />
          </Route>
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe('RequireAuth', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('sends anonymous visitors to the sign-in page', async () => {
    mockFetch(false);
    setup();

    expect(await screen.findByText('login page')).toBeInTheDocument();
  });

  it('sends a server with no accounts to the first-run setup page', async () => {
    mockFetch(false, true);
    setup();

    expect(await screen.findByText('first run setup')).toBeInTheDocument();
  });

  it('renders the app for a signed-in user', async () => {
    mockFetch(true);
    setup();

    expect(await screen.findByText('browse page')).toBeInTheDocument();
  });

  it('routes to the access-denied page when the API reports 403', async () => {
    mockFetch(true);
    setup();

    await screen.findByText('browse page');
    window.dispatchEvent(
      new CustomEvent(FORBIDDEN_EVENT, { detail: { message: 'Not allowed here.' } }),
    );

    expect(await screen.findByText('access denied page')).toBeInTheDocument();
  });

  it('returns to the sign-in page when the session expires', async () => {
    // The first status call signs the user in; later ones report a dead
    // session, which is what the 401 re-check finds.
    let signedIn = true;
    const fn = vi.fn(async (input: URL | RequestInfo) => {
      const url = String(input);
      if (url.endsWith('/api/v1/auth/status')) {
        return new Response(
          JSON.stringify({
            bootstrap_required: false,
            authenticated: signedIn,
            ...(signedIn ? { user: admin } : {}),
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }
      return new Response(
        JSON.stringify({ error: { code: 'NOT_FOUND', message: 'missing', request_id: '1' } }),
        { status: 404, headers: { 'Content-Type': 'application/json' } },
      );
    });
    globalThis.fetch = fn as unknown as typeof fetch;
    setup();

    await screen.findByText('browse page');
    signedIn = false;
    window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT));

    expect(await screen.findByText('login page')).toBeInTheDocument();
  });
});

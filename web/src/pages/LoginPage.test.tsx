import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';

import AuthProvider from '../auth/AuthProvider';
import LoginPage from './LoginPage';

const admin = {
  id: 'u1',
  username: 'jishnu',
  role: 'admin' as const,
  created_at: '2026-01-01T00:00:00Z',
};

const originalFetch = globalThis.fetch;

interface FetchOptions {
  /** Server has no accounts yet → the page offers first-run setup. */
  bootstrapRequired?: boolean;
  /** A valid session cookie already exists. */
  authenticated?: boolean;
  user?: typeof admin;
  loginError?: number;
}

function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function mockFetch(options: FetchOptions = {}) {
  const authenticated = options.authenticated ?? false;
  const fn = vi.fn(async (input: URL | RequestInfo, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/api/v1/auth/status')) {
      return json({
        bootstrap_required: options.bootstrapRequired ?? false,
        authenticated,
        ...(authenticated ? { user: options.user ?? admin } : {}),
      });
    }
    if (url.endsWith('/api/v1/auth/login')) {
      if (options.loginError) {
        return json(
          {
            error: {
              code: 'UNAUTHORIZED',
              message: 'Invalid username or password.',
              request_id: 'r1',
            },
          },
          options.loginError,
        );
      }
      return json({ user: options.user ?? admin });
    }
    if (url.endsWith('/api/v1/auth/bootstrap')) {
      return json({ user: admin }, 201);
    }
    void init;
    return json({ error: { code: 'NOT_FOUND', message: 'missing', request_id: '1' } }, 404);
  });
  globalThis.fetch = fn as unknown as typeof fetch;
  return fn;
}

function setup() {
  return render(
    <MemoryRouter initialEntries={['/login']}>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/setup" element={<div>first run setup</div>} />
          <Route path="/" element={<div>app home</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

function signIn() {
  fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'jishnu' } });
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 's3cret' } });
  fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
}

describe('LoginPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('signs in with valid credentials and lands in the app', async () => {
    const fn = mockFetch();
    setup();

    await screen.findByRole('heading', { name: 'Sign in to Cairn' });
    signIn();

    expect(await screen.findByText('app home')).toBeInTheDocument();
    const loginCall = fn.mock.calls.find(([url]) => String(url).endsWith('/auth/login'));
    expect(loginCall).toBeDefined();
    expect(loginCall?.[1]?.body).toBe(JSON.stringify({ username: 'jishnu', password: 's3cret' }));
  });

  it('shows the server error message for bad credentials', async () => {
    mockFetch({ loginError: 401 });
    setup();

    await screen.findByRole('heading', { name: 'Sign in to Cairn' });
    signIn();

    expect(await screen.findByRole('alert')).toHaveTextContent('Invalid username or password.');
    expect(screen.queryByText('app home')).not.toBeInTheDocument();
  });

  it('requires both fields before calling the API', async () => {
    const fn = mockFetch();
    setup();

    await screen.findByRole('heading', { name: 'Sign in to Cairn' });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Enter your username and password.');
    expect(fn.mock.calls.some(([url]) => String(url).endsWith('/auth/login'))).toBe(false);
  });

  it('offers account creation when the server has no accounts', async () => {
    mockFetch({ bootstrapRequired: true });
    setup();

    expect(await screen.findByRole('heading', { name: 'Sign in to Cairn' })).toBeInTheDocument();
    expect(screen.getByText(/no accounts yet/)).toBeInTheDocument();
    expect(screen.getByText('First time on this server?')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Create an account' })).toBeInTheDocument();
  });

  it('sends the visitor to first-run setup from the signup button', async () => {
    mockFetch({ bootstrapRequired: true });
    setup();

    const signup = await screen.findByRole('link', { name: 'Create an account' });
    fireEvent.click(signup);

    expect(await screen.findByText('first run setup')).toBeInTheDocument();
  });

  it('keeps the signup button available once an account exists', async () => {
    const fn = mockFetch();
    setup();

    await screen.findByRole('heading', { name: 'Sign in to Cairn' });
    // The button stays, so the route is discoverable: /setup explains that an
    // administrator creates accounts. Only the first-run wording changes.
    expect(screen.getByText('Need an account?')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Create an account' })).toBeInTheDocument();
    expect(screen.queryByText('First time on this server?')).not.toBeInTheDocument();
    expect(
      screen.queryByText('Your photos, files, and memories — kept quietly in your own hands.'),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText('Confirm password')).not.toBeInTheDocument();
    expect(fn.mock.calls.some(([url]) => String(url).endsWith('/auth/bootstrap'))).toBe(false);
  });

  it('skips the form when a session already exists', async () => {
    mockFetch({ authenticated: true, user: admin });
    setup();

    await waitFor(() => {
      expect(screen.getByText('app home')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('login-page')).not.toBeInTheDocument();
  });
});

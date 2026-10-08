import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';

import AuthProvider from '../auth/AuthProvider';
import SetupPage from './SetupPage';

const admin = {
  id: 'u1',
  username: 'jishnu',
  role: 'admin' as const,
  created_at: '2026-01-01T00:00:00Z',
};

const originalFetch = globalThis.fetch;

function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

interface FetchOptions {
  /** Server still has no accounts → the setup form is offered. */
  bootstrapRequired?: boolean;
  /** A session already exists. */
  authenticated?: boolean;
  /** Server-side rejection of the bootstrap attempt. */
  bootstrapError?: { status: number; message: string };
}

function mockFetch(options: FetchOptions = {}) {
  // A refused bootstrap means another browser created the first account, so
  // the server stops reporting `bootstrap_required` from then on.
  let bootstrapRequired = options.bootstrapRequired ?? false;
  const fn = vi.fn(async (input: URL | RequestInfo, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/api/v1/auth/status')) {
      return json({
        bootstrap_required: bootstrapRequired,
        authenticated: options.authenticated ?? false,
        ...(options.authenticated ? { user: admin } : {}),
      });
    }
    if (url.endsWith('/api/v1/auth/bootstrap')) {
      if (options.bootstrapError) {
        bootstrapRequired = false;
        return json(
          {
            error: {
              code: 'CONFLICT',
              message: options.bootstrapError.message,
              request_id: 'r1',
            },
          },
          options.bootstrapError.status,
        );
      }
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
    <MemoryRouter initialEntries={['/setup']}>
      <AuthProvider>
        <Routes>
          <Route path="/setup" element={<SetupPage />} />
          <Route path="/login" element={<div>sign in page</div>} />
          <Route path="/" element={<div>app home</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

function fillForm(username: string, password: string, confirm = password) {
  fireEvent.change(screen.getByLabelText('Username'), { target: { value: username } });
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: password } });
  fireEvent.change(screen.getByLabelText('Confirm password'), { target: { value: confirm } });
  fireEvent.click(screen.getByRole('button', { name: 'Create account' }));
}

describe('SetupPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('offers first-run account creation when the server has no accounts', async () => {
    mockFetch({ bootstrapRequired: true });
    setup();

    expect(
      await screen.findByRole('heading', { name: 'Create your admin account' }),
    ).toBeInTheDocument();
    expect(screen.getByText(/first account on your Cairn server/)).toBeInTheDocument();
    expect(screen.getByText('At least 8 characters.')).toBeInTheDocument();
  });

  it('creates the first administrator and lands in the app', async () => {
    const fn = mockFetch({ bootstrapRequired: true });
    setup();

    await screen.findByRole('heading', { name: 'Create your admin account' });
    fillForm('jishnu', 'correct-horse');

    expect(await screen.findByText('app home')).toBeInTheDocument();
    const call = fn.mock.calls.find(([url]) => String(url).endsWith('/auth/bootstrap'));
    expect(call?.[1]?.body).toBe(JSON.stringify({ username: 'jishnu', password: 'correct-horse' }));
  });

  it('requires a matching confirmation before calling the API', async () => {
    const fn = mockFetch({ bootstrapRequired: true });
    setup();

    await screen.findByRole('heading', { name: 'Create your admin account' });
    fillForm('jishnu', 'correct-horse', 'different-one');

    expect(await screen.findByRole('alert')).toHaveTextContent('The passwords do not match.');
    expect(fn.mock.calls.some(([url]) => String(url).endsWith('/auth/bootstrap'))).toBe(false);
  });

  it('enforces the username and password rules the server uses', async () => {
    const fn = mockFetch({ bootstrapRequired: true });
    setup();

    await screen.findByRole('heading', { name: 'Create your admin account' });
    fillForm('bad user!', 'correct-horse');
    expect(await screen.findByRole('alert')).toHaveTextContent('Use 1–64 letters, digits');

    fillForm('jishnu', 'short');
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Use a password of at least 8 characters.',
    );
    expect(fn.mock.calls.some(([url]) => String(url).endsWith('/auth/bootstrap'))).toBe(false);
  });

  it('shows the server error when bootstrap fails for another reason', async () => {
    mockFetch({
      bootstrapRequired: true,
      bootstrapError: { status: 500, message: 'The account store is unavailable.' },
    });
    setup();

    await screen.findByRole('heading', { name: 'Create your admin account' });
    fillForm('jishnu', 'correct-horse');

    expect(await screen.findByRole('alert')).toHaveTextContent('The account store is unavailable.');
    expect(screen.queryByText('app home')).not.toBeInTheDocument();
  });

  it('explains that an administrator creates accounts once one exists', async () => {
    mockFetch({});
    setup();

    // The create-account button on the sign-in page must not dead-end here, so
    // the page says who can create accounts instead of bouncing back.
    expect(
      await screen.findByRole('heading', { name: 'This server is already set up' }),
    ).toBeInTheDocument();
    expect(screen.getByText(/created by its administrator/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Create account' })).not.toBeInTheDocument();
  });

  it('offers a way back to sign-in from the already-set-up notice', async () => {
    mockFetch({});
    setup();

    fireEvent.click(await screen.findByRole('link', { name: 'Back to sign in' }));

    expect(await screen.findByText('sign in page')).toBeInTheDocument();
  });

  it('hands over to the already-set-up notice when another browser created the first account', async () => {
    mockFetch({
      bootstrapRequired: true,
      bootstrapError: { status: 409, message: 'The first account already exists.' },
    });
    setup();

    await screen.findByRole('heading', { name: 'Create your admin account' });
    fillForm('jishnu', 'correct-horse');

    // A 409 means bootstrap is closed, so the page must not sit on a dead form.
    expect(
      await screen.findByRole('heading', { name: 'This server is already set up' }),
    ).toBeInTheDocument();
  });

  it('sends an already signed-in visitor straight into the app', async () => {
    mockFetch({ authenticated: true });
    setup();

    await waitFor(() => {
      expect(screen.getByText('app home')).toBeInTheDocument();
    });
  });
});

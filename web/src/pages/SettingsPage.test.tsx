import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  admin,
  bodyOf,
  called,
  json,
  member,
  mockApi,
  noContent,
  originalFetch,
  renderPage,
} from '../test/harness';
import type { RouteHandler } from '../test/harness';
import SettingsPage from './SettingsPage';

const accounts = [admin, member];

function setup(overrides: RouteHandler[] = [], options = {}) {
  const fn = mockApi(
    [
      ...overrides,
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/auth/logout') ? noContent() : undefined,
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/users')
          ? json(
              {
                user: {
                  id: 'u3',
                  username: 'bob',
                  role: 'user',
                  created_at: '2026-03-03T00:00:00Z',
                },
              },
              201,
            )
          : undefined,
      (url) => (url.includes('/sessions/revoke') ? noContent() : undefined),
      (url) => (url.endsWith('/api/v1/users') ? json({ users: accounts }) : undefined),
    ],
    options,
  );
  renderPage(<SettingsPage />, options);
  return fn;
}

describe('SettingsPage', () => {
  beforeEach(() => {
    localStorage.clear();
    delete document.documentElement.dataset.theme;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('shows the signed-in account and lets it sign out', async () => {
    const fn = setup();

    expect(await screen.findByRole('heading', { name: 'Settings' })).toBeInTheDocument();
    expect(screen.getAllByText('jishnu').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Administrator').length).toBeGreaterThan(0);

    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));

    await waitFor(() => {
      expect(called(fn, 'POST', '/api/v1/auth/logout')).toBe(true);
    });
  });

  it('switches between the existing light and dark appearances', async () => {
    setup();
    await screen.findByRole('heading', { name: 'Settings' });

    fireEvent.click(screen.getByRole('radio', { name: 'Dark' }));
    await waitFor(() => {
      expect(document.documentElement.dataset.theme).toBe('dark');
    });
    expect(localStorage.getItem('cairn.theme')).toBe('dark');

    fireEvent.click(screen.getByRole('radio', { name: 'Match system' }));
    await waitFor(() => {
      expect(document.documentElement.dataset.theme).toBeUndefined();
    });
  });

  it('links the organizing surfaces from one place', async () => {
    setup();
    await screen.findByRole('heading', { name: 'Library' });

    // Each of these has its own page; settings is where the less-used ones land.
    for (const label of ['Tags', 'Sharing', 'Duplicates', 'Trash']) {
      expect(screen.getByRole('link', { name: new RegExp(`^${label}`) })).toBeInTheDocument();
    }
    // Sharing goes to the real route.
    expect(screen.getByRole('link', { name: /^Sharing/ })).toHaveAttribute('href', '/shared');
  });

  it('links the server upkeep surfaces to administrators', async () => {
    setup();
    await screen.findByRole('heading', { name: 'Advanced' });

    for (const label of ['Libraries', 'Permissions', 'Machine learning', 'Backups']) {
      expect(screen.getByRole('link', { name: new RegExp(`^${label}`) })).toBeInTheDocument();
    }
  });

  it('keeps the upkeep surfaces away from members', async () => {
    setup([], { user: member });

    expect(await screen.findByRole('heading', { name: 'Library' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Advanced' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /^Backups/ })).not.toBeInTheDocument();
  });

  it('lists accounts for administrators and can revoke sessions', async () => {
    const fn = setup();

    const list = await screen.findByTestId('settings-user-list');
    expect(list).toBeInTheDocument();

    const aliceRow = screen.getByText('alice').closest('li');
    expect(aliceRow).not.toBeNull();
    fireEvent.click(
      within(aliceRow as HTMLElement).getByRole('button', { name: 'Revoke sessions' }),
    );

    // The click only opens the confirmation — the request waits for it, so a
    // stray click cannot sign somebody out.
    const dialog = await screen.findByTestId('revoke-sessions-dialog');
    expect(called(fn, 'POST', '/api/v1/users/u2/sessions/revoke')).toBe(false);
    expect(within(dialog as HTMLElement).getByText('alice')).toBeInTheDocument();

    fireEvent.click(within(dialog as HTMLElement).getByRole('button', { name: 'Revoke sessions' }));

    await waitFor(() => {
      expect(called(fn, 'POST', '/api/v1/users/u2/sessions/revoke')).toBe(true);
    });
  });

  it('leaves the account signed in when revoking is cancelled', async () => {
    const fn = setup();

    await screen.findByTestId('settings-user-list');
    const aliceRow = screen.getByText('alice').closest('li') as HTMLElement;
    fireEvent.click(within(aliceRow).getByRole('button', { name: 'Revoke sessions' }));

    const dialog = await screen.findByTestId('revoke-sessions-dialog');
    fireEvent.click(within(dialog as HTMLElement).getByRole('button', { name: 'Cancel' }));

    await waitFor(() => {
      expect(screen.queryByTestId('revoke-sessions-dialog')).not.toBeInTheDocument();
    });
    expect(called(fn, 'POST', '/sessions/revoke')).toBe(false);
  });

  it('creates a new account for administrators', async () => {
    const fn = setup();

    await screen.findByTestId('settings-user-list');
    fireEvent.change(screen.getByLabelText('New username'), { target: { value: 'bob' } });
    fireEvent.change(screen.getByLabelText('New password'), { target: { value: 's3cret' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create account' }));

    expect(await screen.findByText('Created bob.')).toBeInTheDocument();
    expect(bodyOf(fn, 'POST', '/api/v1/users')).toEqual({
      username: 'bob',
      password: 's3cret',
      role: 'user',
    });
  });

  it('hides account administration from non-administrators', async () => {
    const fn = setup([], { user: member });

    expect(await screen.findByRole('heading', { name: 'Access' })).toBeInTheDocument();
    expect(screen.queryByTestId('settings-user-list')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Create account' })).not.toBeInTheDocument();
    // A member must not fire a request that can only come back 403.
    expect(called(fn, 'GET', '/api/v1/users')).toBe(false);
  });
});

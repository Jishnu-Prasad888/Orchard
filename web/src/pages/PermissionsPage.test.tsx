import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import {
  apiError,
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
import PermissionsPage from './PermissionsPage';

const grants = {
  grants: [
    {
      id: 'g1',
      user_id: 'u2',
      resource_key: 'library:lib1/2024',
      capabilities: ['read' as const, 'download' as const],
      effect: 'allow' as const,
      created_at: '2026-01-01T00:00:00Z',
    },
    {
      id: 'g2',
      user_id: 'u3',
      resource_key: 'library:lib1/2024/private',
      capabilities: ['read' as const],
      effect: 'deny' as const,
      created_at: '2026-01-01T00:00:00Z',
    },
  ],
};

const users = { users: [member, { id: 'u3', username: 'sam', role: 'user', created_at: '' }] };

function setup(overrides: RouteHandler[] = [], options = {}) {
  const fn = mockApi(
    [
      ...overrides,
      (url) => (url.endsWith('/api/v1/libraries/lib1/permissions') ? json(grants) : undefined),
      (url) => (url.endsWith('/api/v1/users') ? json(users) : undefined),
    ],
    options,
  );
  renderPage(<PermissionsPage />, options);
  return fn;
}

describe('PermissionsPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('lists the grants with the full resource key and its effect', async () => {
    setup();

    expect(await screen.findByRole('heading', { name: 'Permissions' })).toBeInTheDocument();
    const table = await screen.findByTestId('grants-table');
    // Usernames are resolved from the admin user list, not shown as raw ids.
    expect(within(table).getByText('alice')).toBeInTheDocument();
    expect(within(table).getByText('sam')).toBeInTheDocument();
    expect(within(table).getByText('library:lib1/2024')).toBeInTheDocument();
    expect(within(table).getByText('library:lib1/2024/private')).toBeInTheDocument();
    expect(within(table).getByText('Allow')).toBeInTheDocument();
    expect(within(table).getByText('Deny')).toBeInTheDocument();
  });

  it('shows an empty state when nothing has been granted', async () => {
    setup([
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/permissions') ? json({ grants: [] }) : undefined,
    ]);

    expect(await screen.findByTestId('grants-empty')).toBeInTheDocument();
  });

  it('adds a grant on a folder and shows the key it will produce', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/permissions')
          ? json({ grant: grants.grants[0]! }, 201)
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('new-grant-button'));
    const dialog = await screen.findByTestId('new-grant-dialog');
    // The whole library is the default, and the key is shown before committing.
    expect(screen.getByTestId('grant-key')).toHaveTextContent('library:lib1');

    fireEvent.change(screen.getByTestId('grant-scope'), { target: { value: 'folder' } });
    fireEvent.change(screen.getByTestId('grant-path'), { target: { value: '/2024/summer/' } });
    expect(screen.getByTestId('grant-key')).toHaveTextContent('library:lib1/2024/summer');

    // The picker only exists once the user list has arrived.
    fireEvent.change(await screen.findByTestId('grant-user'), { target: { value: 'u2' } });
    fireEvent.click(screen.getByTestId('grant-cap-download'));
    fireEvent.click(within(dialog).getByTestId('confirm-new-grant'));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/permissions')).toBe(true);
    });
    expect(bodyOf(fetchMock, 'POST', '/api/v1/libraries/lib1/permissions')).toEqual({
      user_id: 'u2',
      key: 'library:lib1/2024/summer',
      caps: ['read', 'download'],
      effect: 'allow',
    });
  });

  it('builds a file key when the scope is a single file', async () => {
    setup();

    fireEvent.click(await screen.findByTestId('new-grant-button'));
    fireEvent.change(screen.getByTestId('grant-scope'), { target: { value: 'file' } });
    fireEvent.change(screen.getByTestId('grant-path'), { target: { value: 'IMG_0001.png' } });

    expect(screen.getByTestId('grant-key')).toHaveTextContent('file:lib1/IMG_0001.png');
  });

  it('refuses a grant with nobody or no capabilities', async () => {
    const fetchMock = setup();

    fireEvent.click(await screen.findByTestId('new-grant-button'));
    expect(screen.getByTestId('confirm-new-grant')).toBeDisabled();

    // The picker only exists once the user list has arrived.
    fireEvent.change(await screen.findByTestId('grant-user'), { target: { value: 'u2' } });
    expect(screen.getByTestId('confirm-new-grant')).toBeEnabled();

    fireEvent.click(screen.getByTestId('grant-cap-read'));
    expect(screen.getByTestId('confirm-new-grant')).toBeDisabled();
    expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/permissions')).toBe(false);
  });

  it('falls back to a typed user id when the user list is not readable', async () => {
    mockApi(
      [
        (url, init) =>
          init?.method === 'GET' && url.endsWith('/api/v1/users')
            ? apiError(403, 'FORBIDDEN', 'Administrator only.')
            : undefined,
        (url) => (url.endsWith('/api/v1/libraries/lib1/permissions') ? json(grants) : undefined),
      ],
      { user: member },
    );
    renderPage(<PermissionsPage />, { user: member });

    const table = await screen.findByTestId('grants-table');
    // Without the user list the raw id is the honest thing to show.
    expect(within(table).getByText('u2')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('new-grant-button'));
    expect(await screen.findByTestId('grant-user-id')).toBeInTheDocument();
    expect(screen.queryByTestId('grant-user')).not.toBeInTheDocument();
  });

  it('revokes a grant after confirming, and keeps it on cancel', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'DELETE' && url.endsWith('/api/v1/libraries/lib1/permissions/g1')
          ? noContent()
          : undefined,
    ]);

    await screen.findByTestId('grants-table');
    fireEvent.click(screen.getByTestId('revoke-grant-g1'));

    const dialog = await screen.findByTestId('revoke-grant-dialog');
    expect(within(dialog).getByText(/library:lib1\/2024/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    await waitFor(() => {
      expect(screen.queryByTestId('revoke-grant-dialog')).not.toBeInTheDocument();
    });
    expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/permissions/g1')).toBe(false);

    fireEvent.click(screen.getByTestId('revoke-grant-g1'));
    const again = await screen.findByTestId('revoke-grant-dialog');
    fireEvent.click(within(again).getByRole('button', { name: 'Revoke' }));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/permissions/g1')).toBe(true);
    });
  });

  it('reports a failed revoke inside the dialog', async () => {
    setup([
      (url, init) =>
        init?.method === 'DELETE' && url.endsWith('/api/v1/libraries/lib1/permissions/g1')
          ? json(
              {
                error: {
                  code: 'FORBIDDEN',
                  message: 'You need the manage capability.',
                  request_id: '1',
                },
              },
              403,
            )
          : undefined,
    ]);

    await screen.findByTestId('grants-table');
    fireEvent.click(screen.getByTestId('revoke-grant-g1'));
    const dialog = await screen.findByTestId('revoke-grant-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'You need the manage capability.',
    );
  });

  it('says a member with no libraries to ask an administrator', async () => {
    mockApi([], { user: member, libraries: [] });
    renderPage(<PermissionsPage />, { user: member });

    expect(await screen.findByTestId('no-libraries')).toBeInTheDocument();
    expect(screen.getByTestId('new-grant-button')).toBeDisabled();
  });
});

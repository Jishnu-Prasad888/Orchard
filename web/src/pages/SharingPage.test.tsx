import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  apiError,
  bodyOf,
  called,
  json,
  mockApi,
  noContent,
  originalFetch,
  renderPage,
} from '../test/harness';
import type { RouteHandler } from '../test/harness';
import SharingPage from './SharingPage';

const shares = {
  shares: [
    {
      id: 'sh1',
      resource_key: 'file:lib1/IMG_0001.png',
      capabilities: ['read' as const],
      has_password: false,
      created_at: '2026-01-01T00:00:00Z',
    },
    {
      id: 'sh2',
      resource_key: 'library:lib1',
      capabilities: ['read' as const, 'download' as const],
      has_password: true,
      expires_at: '2026-12-01T00:00:00Z',
      created_at: '2026-01-01T00:00:00Z',
    },
  ],
};

function setup(overrides: RouteHandler[] = []) {
  const fn = mockApi([
    ...overrides,
    (url) => (url.endsWith('/api/v1/libraries/lib1/shares') ? json(shares) : undefined),
  ]);
  renderPage(<SharingPage />);
  return fn;
}

describe('SharingPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('lists the shares with what each one allows and how it is protected', async () => {
    setup();

    expect(await screen.findByRole('heading', { name: 'Sharing' })).toBeInTheDocument();
    const table = await screen.findByTestId('shares-table');

    expect(within(table).getByText('file:lib1/IMG_0001.png')).toBeInTheDocument();
    expect(within(table).getByText('library:lib1')).toBeInTheDocument();
    // A share with no password and no expiry says so, rather than showing a blank.
    expect(within(table).getAllByText('—')).toHaveLength(1);
    expect(within(table).getByText('Never')).toBeInTheDocument();
    expect(within(table).getByText('Password')).toBeInTheDocument();
    expect(within(table).getAllByText('Active')).toHaveLength(2);
  });

  it('marks a revoked or expired share as such', async () => {
    setup([
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/shares')
          ? json({
              shares: [
                { ...shares.shares[0]!, revoked_at: '2026-02-01T00:00:00Z' },
                { ...shares.shares[1]!, expires_at: '2020-01-01T00:00:00Z' },
              ],
            })
          : undefined,
    ]);

    const table = await screen.findByTestId('shares-table');
    expect(within(table).getByText('Revoked')).toBeInTheDocument();
    expect(within(table).getByText('Expired')).toBeInTheDocument();
    // Nothing left to do about them, so the buttons are off.
    expect(within(table).getAllByRole('button', { name: 'Revoke' })[0]).toBeDisabled();
  });

  it('shows an empty state when nothing has been shared', async () => {
    setup([
      (url) => (url.endsWith('/api/v1/libraries/lib1/shares') ? json({ shares: [] }) : undefined),
    ]);

    expect(await screen.findByTestId('shares-empty')).toBeInTheDocument();
  });

  it('shows the link once when a share is created', async () => {
    // jsdom has no clipboard, and the page's copy button is worth covering.
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/shares')
          ? json(
              {
                share: { ...shares.shares[0]!, id: 'sh9' },
                token: 'tok9',
              },
              201,
            )
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('new-share-button'));
    const dialog = await screen.findByTestId('new-share-dialog');
    // The default is the whole library, read-only, no password, no expiry.
    expect(screen.getByTestId('share-resource-key')).toHaveValue('library:lib1');
    fireEvent.change(screen.getByTestId('share-resource-key'), {
      target: { value: 'file:lib1/beach.jpg' },
    });
    fireEvent.click(within(dialog).getByTestId('confirm-new-share'));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/shares')).toBe(true);
    });
    expect(bodyOf(fetchMock, 'POST', '/api/v1/libraries/lib1/shares')).toEqual({
      key: 'file:lib1/beach.jpg',
      caps: ['read'],
    });

    const fresh = await screen.findByTestId('fresh-share');
    const link = screen.getByLabelText('Share link') as HTMLInputElement;
    expect(link.value).toMatch(/\/s\/tok9$/);
    expect(fresh).toHaveTextContent('the only time Cairn shows the token');

    fireEvent.click(within(fresh).getByRole('button', { name: 'Copy link' }));
    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith(link.value);
    });
    await within(fresh).findByRole('button', { name: 'Copied' });
  });

  it('sends a password and an expiry when they are set', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/shares')
          ? json({ share: shares.shares[0]!, token: 'tok9' }, 201)
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('new-share-button'));
    fireEvent.change(screen.getByTestId('share-password'), { target: { value: 'hunter2' } });
    fireEvent.change(screen.getByLabelText('Expires (optional)'), {
      target: { value: '2026-12-01T09:00' },
    });
    // Write access is a decision to make on purpose, so it is unticked by default.
    fireEvent.click(screen.getByRole('checkbox', { name: 'Edit' }));
    fireEvent.click(screen.getByTestId('confirm-new-share'));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/shares')).toBe(true);
    });
    expect(bodyOf(fetchMock, 'POST', '/api/v1/libraries/lib1/shares')).toMatchObject({
      caps: ['read', 'edit'],
      password: 'hunter2',
    });
  });

  it('will not create a share with no capability at all', async () => {
    const fetchMock = setup();

    fireEvent.click(await screen.findByTestId('new-share-button'));
    fireEvent.click(screen.getByRole('checkbox', { name: 'View' }));

    expect(screen.getByTestId('confirm-new-share')).toBeDisabled();
    expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/shares')).toBe(false);
  });

  it('revokes a share after confirming, and keeps it on cancel', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'DELETE' && url.includes('/api/v1/libraries/lib1/shares/sh1')
          ? noContent()
          : undefined,
    ]);

    const table = await screen.findByTestId('shares-table');
    const row = within(table).getAllByRole('row')[1]!;
    fireEvent.click(within(row).getByRole('button', { name: 'Revoke' }));

    const dialog = await screen.findByTestId('revoke-share-dialog');
    expect(within(dialog).getByText(/loses access immediately/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    await waitFor(() => {
      expect(screen.queryByTestId('revoke-share-dialog')).not.toBeInTheDocument();
    });
    expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/shares/sh1')).toBe(false);

    fireEvent.click(within(row).getByRole('button', { name: 'Revoke' }));
    const again = await screen.findByTestId('revoke-share-dialog');
    fireEvent.click(within(again).getByRole('button', { name: 'Revoke' }));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/shares/sh1')).toBe(true);
    });
  });

  it('reports a failed revoke instead of dropping the row silently', async () => {
    setup([
      (url, init) =>
        init?.method === 'DELETE' && url.includes('/api/v1/libraries/lib1/shares/sh1')
          ? apiError(403, 'FORBIDDEN', 'You need the manage capability for this.')
          : undefined,
    ]);

    const table = await screen.findByTestId('shares-table');
    fireEvent.click(
      within(within(table).getAllByRole('row')[1]!).getByRole('button', { name: 'Revoke' }),
    );
    const dialog = await screen.findByTestId('revoke-share-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'You need the manage capability for this.',
    );
  });

  it('says a member with no libraries to ask an administrator', async () => {
    mockApi([], { libraries: [] });
    renderPage(<SharingPage />);

    expect(await screen.findByTestId('no-libraries')).toBeInTheDocument();
    // There is no library to share, so the button is disabled rather than broken.
    expect(screen.getByTestId('new-share-button')).toBeDisabled();
  });
});

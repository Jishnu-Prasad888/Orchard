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
      resource_key: 'lib1/x:IMG_0001.png',
      target_kind: 'file' as const,
      target_name: 'IMG_0001.png',
      capabilities: ['read' as const],
      has_password: false,
      created_at: '2026-01-01T00:00:00Z',
    },
    {
      id: 'sh2',
      resource_key: 'lib1/t:t1',
      target_kind: 'tag' as const,
      target_name: 'beach',
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
    (url) =>
      url.endsWith('/api/v1/libraries/lib1/tags')
        ? json({ tags: [{ id: 't1', name: 'beach', created_at: '2026-01-01T00:00:00Z' }] })
        : undefined,
    (url) => (url.includes('/api/v1/libraries/lib1/folders') ? json({ folders: [] }) : undefined),
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

    // Each link says what it shares in words, not as an internal key.
    expect(within(table).getByText('IMG_0001.png')).toBeInTheDocument();
    expect(within(table).getByText('beach')).toBeInTheDocument();
    expect(within(table).getByText('File')).toBeInTheDocument();
    expect(within(table).getByText('Tag')).toBeInTheDocument();
    expect(within(table).queryByText(/lib1/)).not.toBeInTheDocument();
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

  it('will not create a link until something is chosen to share', async () => {
    const fetchMock = setup();

    fireEvent.click(await screen.findByTestId('new-share-button'));
    const dialog = await screen.findByTestId('share-dialog');
    // A folder is the default kind, and no folder is chosen yet.
    expect(within(dialog).getByTestId('confirm-new-share')).toBeDisabled();
    fireEvent.click(within(dialog).getByTestId('share-kind-tag'));
    expect(within(dialog).getByTestId('confirm-new-share')).toBeDisabled();
    fireEvent.click(within(dialog).getByTestId('share-kind-collection'));
    expect(within(dialog).getByTestId('confirm-new-share')).toBeDisabled();
    expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/shares')).toBe(false);
  });

  it('shares a tag, and shows the link once in the dialog', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/shares')
          ? json({ share: { ...shares.shares[1]!, id: 'sh9' }, token: 'tok9' }, 201)
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('new-share-button'));
    const dialog = await screen.findByTestId('share-dialog');
    fireEvent.click(within(dialog).getByTestId('share-kind-tag'));
    const select = within(dialog).getByTestId('share-tag');
    await within(select).findByRole('option', { name: 'beach' });
    fireEvent.change(select, { target: { value: 't1' } });
    fireEvent.click(within(dialog).getByTestId('share-allow-download'));
    fireEvent.click(within(dialog).getByTestId('confirm-new-share'));

    await waitFor(() =>
      expect(bodyOf(fetchMock, 'POST', '/api/v1/libraries/lib1/shares')).toEqual({
        target: { kind: 'tag', id: 't1' },
        caps: ['read', 'download'],
      }),
    );
    const link = (await within(dialog).findByTestId('share-link')) as HTMLInputElement;
    expect(link.value).toMatch(/\/s\/tok9$/);
    expect(within(dialog).getByText(/shows it only once/)).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Copy link' }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(link.value));
    await within(dialog).findByRole('button', { name: 'Copied' });
  });

  it('shares a folder by its path', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/shares')
          ? json({ share: shares.shares[0]!, token: 'tok9' }, 201)
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('new-share-button'));
    const dialog = await screen.findByTestId('share-dialog');
    fireEvent.change(within(dialog).getByLabelText('Folder'), {
      target: { value: '/2024/Japan/' },
    });
    fireEvent.click(within(dialog).getByTestId('confirm-new-share'));

    await waitFor(() =>
      expect(bodyOf(fetchMock, 'POST', '/api/v1/libraries/lib1/shares')).toEqual({
        target: { kind: 'folder', path: '2024/Japan' },
        caps: ['read'],
      }),
    );
  });

  it('warns before sharing the whole library', async () => {
    setup();
    fireEvent.click(await screen.findByTestId('new-share-button'));
    const dialog = await screen.findByTestId('share-dialog');
    fireEvent.click(within(dialog).getByTestId('share-kind-library'));
    expect(within(dialog).getByRole('note')).toHaveTextContent('every file in this library');
    expect(within(dialog).getByTestId('confirm-new-share')).toBeEnabled();
  });

  it('sends a password and an expiry, and refuses a password that is too short', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/shares')
          ? json({ share: shares.shares[0]!, token: 'tok9' }, 201)
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('new-share-button'));
    const dialog = await screen.findByTestId('share-dialog');
    fireEvent.click(within(dialog).getByTestId('share-kind-library'));
    fireEvent.change(within(dialog).getByTestId('share-password'), { target: { value: 'short' } });
    expect(within(dialog).getByText('At least 8 characters.')).toBeInTheDocument();
    expect(within(dialog).getByTestId('confirm-new-share')).toBeDisabled();

    fireEvent.change(within(dialog).getByTestId('share-password'), {
      target: { value: 'long-enough' },
    });
    fireEvent.change(within(dialog).getByTestId('share-expires'), {
      target: { value: '2099-12-01T09:00' },
    });
    fireEvent.click(within(dialog).getByTestId('confirm-new-share'));

    await waitFor(() =>
      expect(bodyOf(fetchMock, 'POST', '/api/v1/libraries/lib1/shares')).toMatchObject({
        target: { kind: 'library' },
        caps: ['read'],
        password: 'long-enough',
        expires_at: new Date('2099-12-01T09:00').toISOString(),
      }),
    );
  });

  it('keeps a refused link inside the dialog with the reason', async () => {
    setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/shares')
          ? apiError(404, 'NOT_FOUND', 'That folder does not exist.')
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('new-share-button'));
    const dialog = await screen.findByTestId('share-dialog');
    fireEvent.change(within(dialog).getByLabelText('Folder'), { target: { value: 'nope' } });
    fireEvent.click(within(dialog).getByTestId('confirm-new-share'));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'That folder does not exist.',
    );
    expect(within(dialog).queryByTestId('share-link')).not.toBeInTheDocument();
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

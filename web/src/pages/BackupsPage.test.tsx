import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import {
  apiError,
  bodyOf,
  called,
  json,
  member,
  mockApi,
  originalFetch,
  renderPage,
} from '../test/harness';
import type { RouteHandler } from '../test/harness';
import BackupsPage from './BackupsPage';

const okBackup = {
  id: 'b1',
  status: 'ok',
  destination: '/mnt/backup/cairn',
  started_at: '2026-09-01T02:00:00Z',
  finished_at: '2026-09-01T02:14:00Z',
  libraries: 2,
  files: 1204,
  files_skipped: 1180,
  bytes: 8_589_934_592,
  stored_bytes: 4_294_967_296,
  encrypted: true,
  same_device: false,
  created_at: '2026-09-01T02:00:00Z',
};

const failedBackup = {
  ...okBackup,
  id: 'b2',
  status: 'failed',
  destination: '/mnt/backup/cairn',
  started_at: '2026-08-31T02:00:00Z',
  finished_at: '',
  // A half-finished run is neither encrypted nor off this machine.
  encrypted: false,
  same_device: true,
  error_msg: 'the destination is full',
};

// GET /backups answers with a bare array, not an envelope.
const backups = [okBackup, failedBackup];

function setup(overrides: RouteHandler[] = [], options = {}) {
  const fn = mockApi(
    [
      ...overrides,
      (url) => (url.endsWith('/api/v1/backups/b1') ? json(okBackup) : undefined),
      (url) => (url.endsWith('/api/v1/backups') ? json(backups) : undefined),
    ],
    options,
  );
  renderPage(<BackupsPage />, options);
  return fn;
}

describe('BackupsPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('lists the backups with their status and destination', async () => {
    setup();

    expect(await screen.findByRole('heading', { name: 'Backups' })).toBeInTheDocument();
    const table = await screen.findByTestId('backups-table');
    expect(within(table).getByTestId('backup-status-b1')).toHaveTextContent('Completed');
    expect(within(table).getByTestId('backup-status-b2')).toHaveTextContent('Failed');
    // The destination's properties are called out, not left to be discovered.
    expect(within(table).getAllByText('/mnt/backup/cairn')).toHaveLength(2);
    expect(within(table).getByText('encrypted')).toBeInTheDocument();
    expect(within(table).getByText('same device')).toBeInTheDocument();
  });

  it('warns that a backup on the same disk is not protection', async () => {
    setup([(url) => (url.endsWith('/api/v1/backups') ? json([failedBackup]) : undefined)]);

    expect(await screen.findByTestId('backup-same-device')).toHaveTextContent(
      'not against losing the machine',
    );
  });

  it('shows an empty state before the first run', async () => {
    setup([(url) => (url.endsWith('/api/v1/backups') ? json([]) : undefined)]);

    expect(await screen.findByTestId('backups-empty')).toBeInTheDocument();
  });

  it('runs a backup after confirming, and does not on cancel', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/backups')
          ? json({ ...okBackup, id: 'b3' }, 201)
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('run-backup'));
    let dialog = await screen.findByTestId('run-backup-dialog');
    // It is synchronous and touches every library, so the cost is stated first.
    expect(within(dialog).getByText(/copies every indexed file/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    await waitFor(() => {
      expect(screen.queryByTestId('run-backup-dialog')).not.toBeInTheDocument();
    });
    expect(called(fetchMock, 'POST', '/api/v1/backups')).toBe(false);

    fireEvent.click(screen.getByTestId('run-backup'));
    dialog = await screen.findByTestId('run-backup-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Back up now' }));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/backups')).toBe(true);
    });
    expect(await screen.findByTestId('backup-notice')).toHaveTextContent('Backed up 1204 files');
  });

  it('reports a finished run that did not succeed', async () => {
    setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/backups')
          ? json({ ...failedBackup, id: 'b3' }, 201)
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('run-backup'));
    const dialog = await screen.findByTestId('run-backup-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Back up now' }));

    expect(await screen.findByTestId('backup-notice')).toHaveTextContent(
      'Backup finished with status "failed".',
    );
  });

  it('reports a failed run inside the dialog and leaves it open to retry', async () => {
    let attempts = 0;
    const fetchMock = setup([
      (url, init) => {
        if (init?.method !== 'POST' || !url.endsWith('/api/v1/backups')) return undefined;
        attempts += 1;
        return attempts === 1
          ? apiError(500, 'INTERNAL', 'The destination is not writable.')
          : json({ ...okBackup, id: 'b3' }, 201);
      },
    ]);

    fireEvent.click(await screen.findByTestId('run-backup'));
    const dialog = await screen.findByTestId('run-backup-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Back up now' }));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'The destination is not writable.',
    );
    // The dialog is still open, so the run can be retried without re-confirming.
    expect(screen.getByTestId('run-backup-dialog')).toBeInTheDocument();
    expect(screen.queryByTestId('backup-notice')).not.toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Back up now' }));
    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/backups')).toBe(true);
    });
    expect(await screen.findByTestId('backup-notice')).toHaveTextContent('Backed up 1204 files');
    expect(screen.queryByTestId('run-backup-dialog')).not.toBeInTheDocument();
  });

  it('opens a backup and verifies it', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/backups/b1/verify')
          ? json({
              ...okBackup,
              verify_status: 'ok',
              verify_checked: 1204,
              verify_errors: 0,
            })
          : undefined,
    ]);

    const table = await screen.findByTestId('backups-table');
    fireEvent.click(within(table).getByTestId('backup-open-b1'));

    const dialog = await screen.findByTestId('backup-detail-dialog');
    expect(within(dialog).getByText('/mnt/backup/cairn')).toBeInTheDocument();
    // A backup is metadata, not a download, so the page reports where it is.
    expect(within(dialog).getByText('1204 copied, 1180 already present')).toBeInTheDocument();
    expect(within(dialog).getByText('8.0 GB of media → 4.0 GB stored')).toBeInTheDocument();
    expect(within(dialog).getByText('No — this is on different storage')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('verify-backup'));
    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/backups/b1/verify')).toBe(true);
    });
    expect(await within(dialog).findByText(/ok: 1204 checked, 0 errors/)).toBeInTheDocument();
  });

  it('will not verify or restore a backup that did not finish', async () => {
    setup([(url) => (url.endsWith('/api/v1/backups/b2') ? json(failedBackup) : undefined)]);

    const table = await screen.findByTestId('backups-table');
    fireEvent.click(within(table).getByTestId('backup-open-b2'));

    const dialog = await screen.findByTestId('backup-detail-dialog');
    expect(within(dialog).getByText('the destination is full')).toBeInTheDocument();
    expect(screen.getByTestId('verify-backup')).toBeDisabled();
    expect(screen.getByTestId('open-restore-backup')).toBeDisabled();
  });

  it('restores to a directory the user names, not over the libraries', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/backups/b1/restore')
          ? json({ ...okBackup, status: 'running' }, 202)
          : undefined,
    ]);

    const table = await screen.findByTestId('backups-table');
    fireEvent.click(within(table).getByTestId('backup-open-b1'));
    fireEvent.click(await screen.findByTestId('open-restore-backup'));

    const dialog = await screen.findByTestId('restore-backup-dialog');
    // Restoring writes a copy; it never overwrites the libraries in place.
    expect(within(dialog).getByText(/not modified/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Destination directory'), {
      target: { value: '/srv/cairn-restore' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Restore' }));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/backups/b1/restore')).toBe(true);
    });
    expect(bodyOf(fetchMock, 'POST', '/api/v1/backups/b1/restore')).toEqual({
      destination: '/srv/cairn-restore',
    });
    expect(await screen.findByTestId('backup-notice')).toHaveTextContent('Restore started.');
  });

  it('tells a member backups are an administrator action', async () => {
    const fetchMock = mockApi([], { user: member });
    renderPage(<BackupsPage />, { user: member });

    expect(await screen.findByTestId('backups-forbidden')).toBeInTheDocument();
    expect(screen.queryByTestId('run-backup')).not.toBeInTheDocument();
    expect(called(fetchMock, 'GET', '/api/v1/backups')).toBe(false);
  });

  it('retries a failed listing', async () => {
    let attempts = 0;
    const fetchMock = mockApi([
      (url) => {
        if (!url.endsWith('/api/v1/backups')) return undefined;
        attempts += 1;
        return attempts === 1
          ? apiError(500, 'INTERNAL', 'The backup directory is missing.')
          : json(backups);
      },
    ]);
    renderPage(<BackupsPage />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('The backup directory is missing.');
    fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));

    expect(await screen.findByTestId('backups-table')).toBeInTheDocument();
    expect(called(fetchMock, 'GET', '/api/v1/backups')).toBe(true);
  });
});

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

const defaultPolicy = {
  schedule: { kind: 'off' },
  size_trigger: { enabled: false, bytes: 0, quiet_minutes: 0 },
  timezone: 'IST',
  pending: { files: 12, bytes: 3 * 1024 * 1024 },
};

const okBackup = {
  id: 'b1',
  status: 'completed',
  destination: '/mnt/backup/orchard',
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
  destination: '/mnt/backup/orchard',
  started_at: '2026-08-31T02:00:00Z',
  finished_at: '',
  // A half-finished run is neither encrypted nor off this machine.
  encrypted: false,
  same_device: true,
  error_msg: 'the destination is full',
};

// GET /backups answers with a bare array, not an envelope.
const backups = [okBackup, failedBackup];

const defaultConfig = {
  dir: '/data/orchard/backups',
  dir_source: 'default',
  keep: 4,
  interval_minutes: 0,
  encrypted: false,
};

function setup(overrides: RouteHandler[] = [], options = {}) {
  const fn = mockApi(
    [
      ...overrides,
      (url) => (url.endsWith('/api/v1/backups/b1') ? json(okBackup) : undefined),
      (url, init) =>
        (init?.method ?? 'GET') === 'GET' && url.endsWith('/api/v1/backups/policy')
          ? json(defaultPolicy)
          : undefined,
      (url, init) =>
        (init?.method ?? 'GET') === 'GET' && url.endsWith('/api/v1/backups/config')
          ? json(defaultConfig)
          : undefined,
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

  it('offers a book metadata CSV export link, for any administrator', async () => {
    setup();
    await screen.findByRole('heading', { name: 'Backups' });

    const link = screen.getByTestId('export-book-metadata');
    expect(link).toHaveAttribute('href', '/api/v1/backups/export/book-metadata.csv');
    expect(link).toHaveAttribute('download');
  });

  it('lists the backups with their status and destination', async () => {
    setup();

    expect(await screen.findByRole('heading', { name: 'Backups' })).toBeInTheDocument();
    const table = await screen.findByTestId('backups-table');
    expect(within(table).getByTestId('backup-status-b1')).toHaveTextContent('Completed');
    expect(within(table).getByTestId('backup-status-b2')).toHaveTextContent('Failed');
    // The destination's properties are called out, not left to be discovered.
    expect(within(table).getAllByText('/mnt/backup/orchard')).toHaveLength(2);
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
    expect(within(dialog).getByText('/mnt/backup/orchard')).toBeInTheDocument();
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
      target: { value: '/srv/orchard-restore' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Restore' }));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/backups/b1/restore')).toBe(true);
    });
    expect(bodyOf(fetchMock, 'POST', '/api/v1/backups/b1/restore')).toEqual({
      destination: '/srv/orchard-restore',
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

  it('shows where backups go and lets an administrator change it', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'PUT' && url.endsWith('/api/v1/backups/config')
          ? json({ ...defaultConfig, dir: '/mnt/usb/orchard', dir_source: 'setting' })
          : undefined,
    ]);

    expect(await screen.findByText('/data/orchard/backups')).toBeInTheDocument();
    expect(screen.getByText(/Orchard's default/)).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('change-backup-dir'));
    fireEvent.change(screen.getByTestId('backup-dir-input'), {
      target: { value: '/mnt/usb/orchard' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(screen.getByTestId('backup-dir')).toHaveTextContent('/mnt/usb/orchard'),
    );
    expect(called(fetchMock, 'PUT', '/api/v1/backups/config')).toBe(true);
  });

  it("keeps the form open with the server's reason when a folder is refused", async () => {
    setup([
      (url, init) =>
        init?.method === 'PUT' && url.endsWith('/api/v1/backups/config')
          ? new Response(
              JSON.stringify({
                error: {
                  code: 'BAD_REQUEST',
                  message: 'backup directory is not writable',
                  request_id: 'r1',
                },
              }),
              { status: 400, headers: { 'Content-Type': 'application/json' } },
            )
          : undefined,
    ]);
    fireEvent.click(await screen.findByTestId('change-backup-dir'));
    fireEvent.change(screen.getByTestId('backup-dir-input'), { target: { value: '/root/nope' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(await screen.findByText(/not writable/)).toBeInTheDocument();
    expect(screen.getByTestId('backup-dir-input')).toBeInTheDocument();
  });

  it('can verify and restore a completed backup', async () => {
    setup();
    const table = await screen.findByTestId('backups-table');
    fireEvent.click(within(table).getByTestId('backup-open-b1'));
    // The server says "completed"; the page once looked for "ok", which kept
    // both of these disabled for every real backup.
    expect(await screen.findByTestId('verify-backup')).toBeEnabled();
    expect(screen.getByTestId('open-restore-backup')).toBeEnabled();
  });

  it('says why each backup ran', async () => {
    setup([
      (url) =>
        url.endsWith('/api/v1/backups')
          ? json([
              { ...okBackup, trigger: 'catch-up' },
              { ...failedBackup, trigger: 'size' },
            ])
          : undefined,
    ]);
    const table = await screen.findByTestId('backups-table');
    expect(within(table).getByText('Missed schedule')).toBeInTheDocument();
    expect(within(table).getByText('New data')).toBeInTheDocument();
  });

  it('saves a weekly schedule and a size trigger, and shows the next run', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'PUT' && url.endsWith('/api/v1/backups/policy')
          ? json({
              ...defaultPolicy,
              schedule: { kind: 'weekly', at: '02:30', weekday: 1 },
              size_trigger: { enabled: true, bytes: 2 * 1024 ** 3, quiet_minutes: 15 },
              next_run: '2099-01-05T21:00:00Z',
            })
          : undefined,
    ]);

    expect(await screen.findByTestId('size-pending')).toHaveTextContent(
      'Not yet backed up: 12 files, 3.0 MB.',
    );
    expect(screen.getByTestId('save-backup-policy')).toBeDisabled();
    // The starting limit reads as 5 GB, not 5120 MB.
    expect(screen.getByTestId('size-amount')).toHaveValue(5);

    fireEvent.change(screen.getByTestId('schedule-kind'), { target: { value: 'weekly' } });
    fireEvent.change(screen.getByTestId('schedule-weekday'), { target: { value: '1' } });
    fireEvent.change(screen.getByTestId('schedule-at'), { target: { value: '02:30' } });
    expect(screen.getByText(/runs once as soon as the server is back/)).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('size-enabled'));
    fireEvent.change(screen.getByTestId('size-amount'), { target: { value: '2' } });
    fireEvent.change(screen.getByTestId('size-quiet'), { target: { value: '15' } });
    fireEvent.click(screen.getByTestId('save-backup-policy'));

    await waitFor(() =>
      expect(bodyOf(fetchMock, 'PUT', '/api/v1/backups/policy')).toEqual({
        schedule: { kind: 'weekly', at: '02:30', weekday: 1 },
        size_trigger: { enabled: true, bytes: 2 * 1024 ** 3, quiet_minutes: 15 },
      }),
    );
    expect(await screen.findByTestId('schedule-next')).toHaveTextContent('Next backup:');
  });

  it('shows a refused schedule inside the form', async () => {
    setup([
      (url, init) =>
        init?.method === 'PUT' && url.endsWith('/api/v1/backups/policy')
          ? apiError(400, 'BAD_REQUEST', 'the interval must be between 15 minutes and 31 days')
          : undefined,
    ]);
    fireEvent.change(await screen.findByTestId('schedule-kind'), {
      target: { value: 'interval' },
    });
    fireEvent.change(screen.getByTestId('schedule-hours'), { target: { value: '6' } });
    fireEvent.click(screen.getByTestId('save-backup-policy'));
    expect(await screen.findByRole('alert')).toHaveTextContent('between 15 minutes and 31 days');
    // Still unsaved, so it can be corrected and tried again.
    expect(screen.getByTestId('save-backup-policy')).toBeEnabled();
  });

  it('warns, in words, when the backup drive is unplugged', async () => {
    setup([
      (url, init) =>
        (init?.method ?? 'GET') === 'GET' && url.endsWith('/api/v1/backups/config')
          ? json({
              ...defaultConfig,
              drive: {
                system: true,
                free: 0,
                total: 0,
                connected: false,
                problem:
                  'The backup drive for /media/pi/backup/orchard (/media/pi/backup) is not connected.',
              },
            })
          : undefined,
    ]);
    expect(await screen.findByTestId('backup-drive-problem')).toHaveTextContent('is not connected');
  });

  it('offers the mounted drives when choosing a folder', async () => {
    setup([
      (url) =>
        url.endsWith('/api/v1/backups/drives')
          ? json({
              drives: [
                {
                  path: '/',
                  device: '/dev/mmcblk0p2',
                  fs_type: 'ext4',
                  total: 32e9,
                  free: 20e9,
                  system: true,
                },
                {
                  path: '/media/pi/backup',
                  device: '/dev/sda1',
                  fs_type: 'exfat',
                  total: 1e12,
                  free: 6e11,
                  system: false,
                },
              ],
            })
          : undefined,
    ]);
    fireEvent.click(await screen.findByTestId('change-backup-dir'));
    const list = await screen.findByTestId('backup-drives');
    expect(within(list).getByText('System disk', { exact: false })).toBeInTheDocument();
    fireEvent.click(within(list).getByText('/media/pi/backup'));
    expect(screen.getByTestId('backup-dir-input')).toHaveValue('/media/pi/backup/orchard-backups');
  });

  it('sets up SFTP, asking to confirm the server on first contact', async () => {
    let tests = 0;
    const fetchMock = setup([
      (url, init) => {
        if (init?.method !== 'POST' || !url.endsWith('/api/v1/backups/remote/test'))
          return undefined;
        tests += 1;
        const body = JSON.parse(String(init.body)) as { config: { fingerprint?: string } };
        return body.config.fingerprint
          ? json({ ok: true })
          : new Response(
              JSON.stringify({
                error: {
                  code: 'SERVER_IDENTITY',
                  message: "confirm the server's identity",
                  request_id: 'r',
                  details: { fingerprint: 'SHA256:abc123', changed: false },
                },
              }),
              { status: 409, headers: { 'Content-Type': 'application/json' } },
            );
      },
      (url, init) =>
        init?.method === 'PUT' && url.endsWith('/api/v1/backups/remote')
          ? json({
              ...defaultConfig,
              destination: 'remote',
              remote: {
                config: { kind: 'sftp', host: 'nas.local', user: 'pi', path: 'orchard-backups' },
                has_secret: true,
              },
            })
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('backup-remote-edit'));
    // Not encrypted: said before anything leaves the machine.
    expect(screen.getByTestId('backup-remote-unencrypted')).toBeInTheDocument();
    fireEvent.change(screen.getByTestId('remote-host'), { target: { value: 'nas.local' } });
    fireEvent.change(screen.getByTestId('remote-user'), { target: { value: 'pi' } });
    fireEvent.change(screen.getByTestId('remote-password'), { target: { value: 'pw' } });
    fireEvent.click(screen.getByTestId('remote-test'));

    const prompt = await screen.findByTestId('remote-identity');
    expect(prompt).toHaveTextContent('First connection to nas.local');
    expect(prompt).toHaveTextContent('SHA256:abc123');
    fireEvent.click(within(prompt).getByTestId('remote-trust'));
    expect(await screen.findByTestId('remote-tested')).toBeInTheDocument();
    expect(tests).toBe(2);

    fireEvent.click(screen.getByTestId('remote-save'));
    await waitFor(() =>
      expect(bodyOf(fetchMock, 'PUT', '/api/v1/backups/remote')).toEqual({
        config: {
          kind: 'sftp',
          host: 'nas.local',
          user: 'pi',
          path: 'orchard-backups',
          ftp_security: 'explicit',
          fingerprint: 'SHA256:abc123',
        },
        secret: { password: 'pw' },
      }),
    );
    expect(await screen.findByTestId('backup-notice')).toHaveTextContent(
      'Backups will now go to sftp://pi@nas.local/orchard-backups.',
    );
    expect(screen.getByTestId('backup-dir-unused')).toBeInTheDocument();
  });

  it('warns loudly when a confirmed server presents a different identity', async () => {
    setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/backups/remote/test')
          ? new Response(
              JSON.stringify({
                error: {
                  code: 'SERVER_IDENTITY',
                  message: 'changed',
                  request_id: 'r',
                  details: { fingerprint: 'SHA256:new', changed: true },
                },
              }),
              { status: 409, headers: { 'Content-Type': 'application/json' } },
            )
          : undefined,
    ]);
    fireEvent.click(await screen.findByTestId('backup-remote-edit'));
    fireEvent.change(screen.getByTestId('remote-host'), { target: { value: 'nas.local' } });
    fireEvent.change(screen.getByTestId('remote-password'), { target: { value: 'pw' } });
    fireEvent.click(screen.getByTestId('remote-test'));
    const prompt = await screen.findByTestId('remote-identity');
    expect(prompt).toHaveTextContent("This server's identity has changed");
    expect(within(prompt).getByTestId('remote-trust')).toHaveTextContent('Trust the new identity');
  });

  it('says plain FTP is unencrypted, and keeps a saved password without resending it', async () => {
    const fetchMock = setup([
      (url, init) =>
        (init?.method ?? 'GET') === 'GET' && url.endsWith('/api/v1/backups/config')
          ? json({
              ...defaultConfig,
              encrypted: true,
              destination: 'remote',
              remote: {
                config: {
                  kind: 'ftp',
                  host: 'nas',
                  user: 'pi',
                  path: 'b',
                  ftp_security: 'explicit',
                },
                has_secret: true,
              },
            })
          : undefined,
      (url, init) =>
        init?.method === 'PUT' && url.endsWith('/api/v1/backups/remote')
          ? json({ ...defaultConfig, destination: 'remote' })
          : undefined,
    ]);
    expect(await screen.findByTestId('backup-remote-current')).toHaveTextContent('ftp://pi@nas/b');
    expect(screen.queryByTestId('backup-remote-unencrypted')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('backup-remote-edit'));
    expect(screen.getByTestId('remote-password')).toHaveAttribute(
      'placeholder',
      'Saved — leave empty to keep it',
    );
    fireEvent.change(screen.getByTestId('remote-ftp-security'), { target: { value: 'none' } });
    expect(screen.getByRole('note')).toHaveTextContent('unencrypted');
    fireEvent.click(screen.getByTestId('remote-save'));
    await waitFor(() =>
      expect(bodyOf(fetchMock, 'PUT', '/api/v1/backups/remote')).not.toHaveProperty('secret'),
    );
  });
});

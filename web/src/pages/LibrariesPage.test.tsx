import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import {
  bodyOf,
  called,
  json,
  libraryFixture,
  member,
  mockApi,
  noContent,
  originalFetch,
  renderPage,
} from '../test/harness';
import type { RouteHandler } from '../test/harness';
import LibrariesPage from './LibrariesPage';

const indexStatus = {
  status: { present: 1204, missing: 0, active_job: null, last_finished_at: '2026-09-01T00:00:00Z' },
};

const probeClean = {
  probe: {
    path: '/mnt/photos',
    path_exists: true,
    is_directory: true,
    is_writable: true,
    has_metadata: false,
    registered: false,
    existing_name: '',
  },
};

function setup(overrides: RouteHandler[] = []) {
  const fn = mockApi([
    ...overrides,
    (url) => (url.includes('/index/status') ? json(indexStatus) : undefined),
  ]);
  renderPage(<LibrariesPage />);
  return fn;
}

describe('LibrariesPage', () => {
  // The selected library is remembered across visits, so a leftover id from a
  // previous test would decide which row the page opens on.
  beforeEach(() => localStorage.clear());
  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('lists the libraries with their status and index count', async () => {
    setup();

    expect(await screen.findByRole('heading', { name: 'Libraries' })).toBeInTheDocument();
    const row = await screen.findByTestId('library-row');
    expect(within(row).getByText('/srv/photos')).toBeInTheDocument();
    expect(await within(row).findByText(/1204 files indexed/)).toBeInTheDocument();
    expect(within(row).getByTestId('library-status')).toHaveAttribute('data-status', 'online');
  });

  it('shows an empty state when there are no libraries yet', async () => {
    mockApi([], { libraries: [] });
    renderPage(<LibrariesPage />);

    expect(await screen.findByTestId('libraries-empty')).toBeInTheDocument();
  });

  it('probes a path before offering to register it', async () => {
    const fetchMock = setup([
      (url, init) =>
        url.endsWith('/api/v1/libraries/probe') && init?.method === 'POST'
          ? json(probeClean)
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('add-library-button'));
    const dialog = await screen.findByTestId('add-library-dialog');
    fireEvent.change(screen.getByTestId('library-path-input'), {
      target: { value: '/mnt/photos' },
    });

    fireEvent.click(within(dialog).getByRole('button', { name: 'Check path' }));
    const probe = await screen.findByTestId('library-probe');
    expect(probe).toHaveTextContent('No Cairn metadata here yet.');
    expect(bodyOf(fetchMock, 'POST', '/api/v1/libraries/probe')).toEqual({ path: '/mnt/photos' });

    fireEvent.change(screen.getByTestId('library-path-input'), { target: { value: '/mnt' } });
    // Editing the path invalidates the probe rather than leaving a stale answer.
    expect(screen.queryByTestId('library-probe')).not.toBeInTheDocument();
  });

  it('names the library after the folder until the name is edited', async () => {
    setup();
    fireEvent.click(await screen.findByTestId('add-library-button'));
    fireEvent.change(screen.getByTestId('library-path-input'), {
      target: { value: '/mnt/Family Photos' },
    });
    const name = screen.getByLabelText('Name');
    expect(name).toHaveValue('Family Photos');

    fireEvent.change(name, { target: { value: 'Mine' } });
    fireEvent.change(screen.getByTestId('library-path-input'), { target: { value: '/mnt/other' } });
    expect(name).toHaveValue('Mine');
  });

  it('fills the path from the server folder browser', async () => {
    setup([
      (url) => {
        if (!url.includes('/api/v1/fs/dirs')) return undefined;
        return url.includes(encodeURIComponent('/mnt/Pictures'))
          ? json({ path: '/mnt/Pictures', parent: '/mnt', dirs: [] })
          : json({
              path: '/mnt',
              parent: '/',
              dirs: [{ name: 'Pictures', path: '/mnt/Pictures' }],
            });
      },
    ]);
    fireEvent.click(await screen.findByTestId('add-library-button'));
    fireEvent.click(screen.getByTestId('browse-folders'));
    fireEvent.click(await screen.findByRole('button', { name: /Pictures/ }));
    expect(await screen.findByText('/mnt/Pictures')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('choose-folder'));

    await waitFor(() => {
      expect(screen.getByTestId('library-path-input')).toHaveValue('/mnt/Pictures');
    });
    expect(screen.getByLabelText('Name')).toHaveValue('Pictures');
    expect(screen.queryByTestId('server-folder-browser')).not.toBeInTheDocument();
  });

  it('starts the folder browser at the list of drives', async () => {
    setup([
      (url) =>
        url.includes('/api/v1/fs/dirs')
          ? json({
              path: '',
              parent: '',
              roots: true,
              dirs: [
                { name: 'Home', path: '/home/me' },
                { name: '/mnt/Data', path: '/mnt/Data' },
              ],
            })
          : undefined,
    ]);
    fireEvent.click(await screen.findByTestId('add-library-button'));
    fireEvent.click(screen.getByTestId('browse-folders'));

    expect(await screen.findByRole('button', { name: /\/mnt\/Data/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Home/ })).toBeInTheDocument();
    // There is nothing to choose until a drive has been opened.
    expect(screen.getByTestId('choose-folder')).toBeDisabled();
  });

  it('refuses to register a path that is not a directory', async () => {
    setup([
      (url, init) =>
        url.endsWith('/api/v1/libraries/probe') && init?.method === 'POST'
          ? json({
              probe: {
                path: '/mnt/photos.jpg',
                path_exists: true,
                is_directory: false,
                is_writable: true,
                has_metadata: false,
                registered: false,
                existing_name: '',
              },
            })
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('add-library-button'));
    fireEvent.change(screen.getByTestId('library-path-input'), {
      target: { value: '/mnt/photos.jpg' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Check path' }));

    expect(await screen.findByTestId('library-probe')).toHaveTextContent(
      'That path is a file, not a directory.',
    );
    expect(screen.getByTestId('confirm-add-library')).toBeDisabled();
  });

  it('says a read-only directory can still be indexed', async () => {
    setup([
      (url, init) =>
        url.endsWith('/api/v1/libraries/probe') && init?.method === 'POST'
          ? json({
              probe: {
                path: '/mnt/archive',
                path_exists: true,
                is_directory: true,
                is_writable: false,
                has_metadata: false,
                registered: false,
                existing_name: '',
              },
            })
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('add-library-button'));
    fireEvent.change(screen.getByTestId('library-path-input'), {
      target: { value: '/mnt/archive' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Check path' }));

    expect(await screen.findByTestId('library-probe')).toHaveTextContent(
      'Cairn cannot write to that directory',
    );
    // A warning, not a block: browsing a read-only archive is still useful.
    expect(screen.getByTestId('confirm-add-library')).toBeEnabled();
  });

  it('registers the library and selects it', async () => {
    const fetchMock = setup([
      (url, init) =>
        url.endsWith('/api/v1/libraries') && init?.method === 'POST'
          ? json(
              { library: libraryFixture({ id: 'lib9', name: 'Archive', root: '/mnt/archive' }) },
              201,
            )
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('add-library-button'));
    fireEvent.change(screen.getByTestId('library-path-input'), {
      target: { value: '/mnt/archive' },
    });
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Archive' } });
    fireEvent.click(screen.getByTestId('confirm-add-library'));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries')).toBe(true);
    });
    expect(bodyOf(fetchMock, 'POST', '/api/v1/libraries')).toEqual({
      path: '/mnt/archive',
      name: 'Archive',
    });
    await waitFor(() => {
      expect(screen.queryByTestId('add-library-dialog')).not.toBeInTheDocument();
    });
    expect(localStorage.getItem('cairn.library')).toBe('lib9');
  });

  it('triggers a scan and refreshes the index status afterwards', async () => {
    const fetchMock = setup([
      (url, init) =>
        url.endsWith('/api/v1/libraries/lib1/index') && init?.method === 'POST'
          ? json({ library_id: 'lib1', status: 'started' }, 202)
          : undefined,
    ]);

    const row = await screen.findByTestId('library-row');
    const before = fetchMock.mock.calls.filter(([input]) =>
      String(input).includes('index/status'),
    ).length;
    fireEvent.click(within(row).getByTestId('index-button'));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/index')).toBe(true);
    });
    await waitFor(() => {
      const after = fetchMock.mock.calls.filter(([input]) =>
        String(input).includes('index/status'),
      ).length;
      expect(after).toBeGreaterThan(before);
    });
  });

  it('unregisters after confirming, and keeps the library on cancel', async () => {
    const fetchMock = setup([
      (url, init) =>
        url.endsWith('/api/v1/libraries/lib1') && init?.method === 'DELETE'
          ? noContent()
          : undefined,
    ]);

    const row = await screen.findByTestId('library-row');
    fireEvent.click(within(row).getByRole('button', { name: 'Unregister' }));
    const dialog = await screen.findByTestId('unregister-dialog');
    expect(within(dialog).getByText(/Your files are not touched/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    await waitFor(() => {
      expect(screen.queryByTestId('unregister-dialog')).not.toBeInTheDocument();
    });
    expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1')).toBe(false);

    fireEvent.click(within(row).getByRole('button', { name: 'Unregister' }));
    const again = await screen.findByTestId('unregister-dialog');
    fireEvent.click(within(again).getByRole('button', { name: 'Unregister' }));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1')).toBe(true);
    });
  });

  it('reconnects an offline library at a new path', async () => {
    const fetchMock = mockApi(
      [
        (url, init) =>
          url.endsWith('/api/v1/libraries/lib1/refresh') && init?.method === 'POST'
            ? json({ library: libraryFixture() })
            : undefined,
        (url) => (url.includes('/index/status') ? json(indexStatus) : undefined),
      ],
      { libraries: [libraryFixture({ status: 'offline' })] },
    );
    renderPage(<LibrariesPage />);

    const row = await screen.findByTestId('library-row');
    // A disconnected disk cannot be scanned, only reconnected.
    expect(within(row).queryByTestId('index-button')).not.toBeInTheDocument();

    fireEvent.click(within(row).getByTestId('reconnect-button'));
    const dialog = await screen.findByTestId('reconnect-dialog');
    expect(dialog).toHaveTextContent('/srv/photos');
    fireEvent.change(screen.getByTestId('reconnect-path-input'), {
      target: { value: '/media/backup/photos' },
    });
    fireEvent.click(screen.getByTestId('confirm-reconnect'));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/refresh')).toBe(true);
    });
    expect(bodyOf(fetchMock, 'POST', '/api/v1/libraries/lib1/refresh')).toEqual({
      path: '/media/backup/photos',
    });
  });

  it('shows a member a read-only list instead of admin buttons', async () => {
    mockApi([(url) => (url.includes('/index/status') ? json(indexStatus) : undefined)], {
      user: member,
    });
    renderPage(<LibrariesPage />, { user: member });

    const row = await screen.findByTestId('library-row');
    expect(screen.queryByTestId('add-library-button')).not.toBeInTheDocument();
    expect(within(row).queryByTestId('index-button')).not.toBeInTheDocument();
    expect(within(row).queryByRole('button', { name: 'Unregister' })).not.toBeInTheDocument();
    expect(screen.getByText(/Adding, reconnecting, and scanning libraries/)).toBeInTheDocument();
  });
});

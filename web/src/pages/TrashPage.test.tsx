import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  apiError,
  bodyOf,
  called,
  fileFixture,
  json,
  libraryFixture,
  mockApi,
  noContent,
  originalFetch,
  renderPage,
} from '../test/harness';
import type { RouteHandler } from '../test/harness';
import TrashPage from './TrashPage';

const photo = fileFixture();
const video = fileFixture({
  id: 'f2',
  rel_path: 'clips/clip.mp4',
  name: 'clip.mp4',
  folder_path: 'clips',
  media_type: 'video',
  mime_type: 'video/mp4',
  status: 'trashed',
  size_bytes: 4194304,
});

const trashed = { files: [photo, video], next_cursor: '', total: 2 };

/**
 * `/trash` plus the two best-effort calls the viewer makes when it opens. The
 * metadata and favorites failures are swallowed by the viewer, but answering
 * them keeps the log readable.
 */
function apiRules(overrides: RouteHandler[] = []): RouteHandler[] {
  return [
    ...overrides,
    (url) => (url.includes('/api/v1/libraries/lib1/trash') ? json(trashed) : undefined),
    (url) => (url.includes('/api/v1/libraries/lib1/favorites') ? json({ files: [] }) : undefined),
    (url) => (url.includes('/files/f1/metadata') ? json({ metadata: null }) : undefined),
    (url) => (url.includes('/files/f2/metadata') ? json({ metadata: null }) : undefined),
  ];
}

function setup(overrides: RouteHandler[] = [], options = {}) {
  const fn = mockApi(apiRules(overrides), options);
  renderPage(<TrashPage />, options);
  return fn;
}

describe('TrashPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('shows what was removed as a grid, with a note that it is recoverable', async () => {
    setup();

    expect(await screen.findByRole('heading', { name: 'Trash' })).toBeInTheDocument();
    const grid = await screen.findByTestId('trash-list');
    expect(within(grid).getByRole('button', { name: 'IMG_0001.png' })).toBeInTheDocument();
    expect(within(grid).getByRole('button', { name: 'clip.mp4' })).toBeInTheDocument();
    expect(screen.getByText('2 items')).toBeInTheDocument();
    expect(screen.getByText(/still on disk/)).toBeInTheDocument();
  });

  it('says the trash is empty rather than showing a bare list', async () => {
    setup([
      (url) => (url.includes('/api/v1/libraries/lib1/trash') ? json({ files: [] }) : undefined),
    ]);

    expect(await screen.findByTestId('trash-empty')).toBeInTheDocument();
  });

  it('restores a file back to where it was', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/files/f1/restore')
          ? json({ file: photo })
          : undefined,
    ]);

    const list = await screen.findByTestId('trash-list');
    fireEvent.click(within(list).getByRole('button', { name: 'Select IMG_0001.png' }));
    fireEvent.click(await screen.findByTestId('selection-restore'));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/files/f1/restore')).toBe(true);
    });
    // The list is re-read, because the file is no longer in the trash.
    await waitFor(() => {
      expect(called(fetchMock, 'GET', '/api/v1/libraries/lib1/trash')).toBe(true);
    });
  });

  it('reports a failed restore without losing the row', async () => {
    setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/files/f1/restore')
          ? apiError(409, 'CONFLICT', 'The file is no longer on disk.')
          : undefined,
    ]);

    const list = await screen.findByTestId('trash-list');
    fireEvent.click(within(list).getByRole('button', { name: 'Select IMG_0001.png' }));
    fireEvent.click(await screen.findByTestId('selection-restore'));

    expect(await screen.findByText(/Couldn't restore/)).toBeInTheDocument();
    // The row is still there once the list has been re-read.
    expect(await screen.findByTestId('trash-list')).toBeInTheDocument();
  });

  it('asks before erasing for good, and names the file', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'DELETE' && url.endsWith('/api/v1/libraries/lib1/files/f2/permanent')
          ? noContent()
          : undefined,
    ]);

    const list = await screen.findByTestId('trash-list');
    fireEvent.click(within(list).getByRole('button', { name: 'Select clip.mp4' }));
    fireEvent.click(await screen.findByTestId('selection-erase'));

    const dialog = await screen.findByTestId('erase-dialog');
    // The one irreversible action in the product says so, and names its target.
    expect(within(dialog).getByRole('heading', { name: /clip\.mp4/ })).toBeInTheDocument();
    expect(within(dialog).getByText(/cannot be undone/)).toBeInTheDocument();
    expect(called(fetchMock, 'DELETE', '/permanent')).toBe(false);

    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => {
      expect(screen.queryByTestId('erase-dialog')).not.toBeInTheDocument();
    });
    expect(called(fetchMock, 'DELETE', '/permanent')).toBe(false);
  });

  it('erases with the real file id, not the path', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'DELETE' && url.endsWith('/api/v1/libraries/lib1/files/f2/permanent')
          ? noContent()
          : undefined,
    ]);

    const list = await screen.findByTestId('trash-list');
    fireEvent.click(within(list).getByRole('button', { name: 'Select clip.mp4' }));
    fireEvent.click(await screen.findByTestId('selection-erase'));
    const dialog = await screen.findByTestId('erase-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete forever' }));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/files/f2/permanent')).toBe(true);
    });
    expect(bodyOf(fetchMock, 'DELETE', '/permanent')).toBeUndefined();
  });

  it('keeps the dialog open when the erase fails', async () => {
    setup([
      (url, init) =>
        init?.method === 'DELETE' && url.endsWith('/api/v1/libraries/lib1/files/f2/permanent')
          ? apiError(500, 'INTERNAL', 'The file could not be removed from disk.')
          : undefined,
    ]);

    const list = await screen.findByTestId('trash-list');
    fireEvent.click(within(list).getByRole('button', { name: 'Select clip.mp4' }));
    fireEvent.click(await screen.findByTestId('selection-erase'));
    const dialog = await screen.findByTestId('erase-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete forever' }));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'The file could not be removed from disk.',
    );
    // Still open, so the erase can be retried.
    expect(screen.getByTestId('erase-dialog')).toBeInTheDocument();
  });

  it('offers a permanent delete in the viewer rather than a second soft delete', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'DELETE' && url.endsWith('/api/v1/libraries/lib1/files/f1/permanent')
          ? noContent()
          : undefined,
    ]);

    const list = await screen.findByTestId('trash-list');
    fireEvent.click(within(list).getByRole('button', { name: 'IMG_0001.png' }));

    const viewer = await screen.findByTestId('viewer');
    fireEvent.click(within(viewer).getByTestId('viewer-trash'));

    const dialog = await screen.findByTestId('trash-dialog');
    expect(within(dialog).getByText(/Delete permanently/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete forever' }));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/files/f1/permanent')).toBe(true);
    });
  });

  it('closes the viewer with Escape', async () => {
    setup();

    const list = await screen.findByTestId('trash-list');
    fireEvent.click(within(list).getByRole('button', { name: 'IMG_0001.png' }));
    await screen.findByTestId('viewer');

    fireEvent.keyDown(document.body, { key: 'Escape' });
    await waitFor(() => {
      expect(screen.queryByTestId('viewer')).not.toBeInTheDocument();
    });
  });

  it('retries a failed listing', async () => {
    let attempts = 0;
    const fetchMock = mockApi([
      (url) => {
        if (!url.includes('/api/v1/libraries/lib1/trash')) return undefined;
        attempts += 1;
        return attempts === 1
          ? apiError(500, 'INTERNAL', 'The index is rebuilding.')
          : json(trashed);
      },
    ]);
    renderPage(<TrashPage />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('The index is rebuilding.');
    fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));

    expect(await screen.findByTestId('trash-list')).toBeInTheDocument();
    expect(called(fetchMock, 'GET', '/api/v1/libraries/lib1/trash')).toBe(true);
  });

  it('sends a member with no libraries to create one', async () => {
    mockApi([], { libraries: [] });
    renderPage(<TrashPage />);

    expect(await screen.findByTestId('no-libraries')).toBeInTheDocument();
  });

  it('warns when the library is offline', async () => {
    setup([], { libraries: [libraryFixture({ status: 'offline' })] });

    expect(await screen.findByTestId('library-offline')).toBeInTheDocument();
  });
});

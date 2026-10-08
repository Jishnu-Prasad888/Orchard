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
import FavoritesPage from './FavoritesPage';

const photo = fileFixture();
const video = fileFixture({
  id: 'f2',
  rel_path: 'clip.mp4',
  name: 'clip.mp4',
  media_type: 'video',
  mime_type: 'video/mp4',
  content_hash: 'def',
});

const favorites = { files: [photo, video], next_cursor: '', total: 2 };

/** `/favorites` plus the best-effort calls the viewer makes when it opens. */
function apiRules(overrides: RouteHandler[] = []): RouteHandler[] {
  return [
    ...overrides,
    (url, init) =>
      init?.method !== 'GET' && url.includes('/api/v1/libraries/lib1/files/f1/favorite')
        ? noContent()
        : undefined,
    (url, init) =>
      init?.method !== 'GET' && url.includes('/api/v1/libraries/lib1/files/f2/favorite')
        ? noContent()
        : undefined,
    (url) => (url.includes('/api/v1/libraries/lib1/favorites') ? json(favorites) : undefined),
    (url) => (url.includes('/files/f1/metadata') ? json({ metadata: null }) : undefined),
    (url) => (url.includes('/files/f2/metadata') ? json({ metadata: null }) : undefined),
  ];
}

function setup(overrides: RouteHandler[] = [], options = {}) {
  const fn = mockApi(apiRules(overrides), options);
  renderPage(<FavoritesPage />, options);
  return fn;
}

describe('FavoritesPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('grids the starred files and counts them', async () => {
    setup();

    expect(await screen.findByRole('heading', { name: 'Favorites' })).toBeInTheDocument();
    expect(await screen.findByTestId('file-grid')).toBeInTheDocument();
    expect(screen.getByText('IMG_0001.png')).toBeInTheDocument();
    expect(screen.getByText('clip.mp4')).toBeInTheDocument();
    expect(screen.getByText('2 favorites')).toBeInTheDocument();
  });

  it('says favorites are private, so an empty list is not a bug', async () => {
    setup([
      (url) => (url.includes('/api/v1/libraries/lib1/favorites') ? json({ files: [] }) : undefined),
    ]);

    const empty = await screen.findByTestId('favorites-empty');
    expect(within(empty).getByText(/Only you see your favorites/)).toBeInTheDocument();
  });

  it('uses the singular for a single favorite', async () => {
    setup([
      (url) =>
        url.includes('/api/v1/libraries/lib1/favorites') ? json({ files: [photo] }) : undefined,
    ]);

    expect(await screen.findByText('1 favorite')).toBeInTheDocument();
  });

  it('opens a favorite in the viewer and can page to the next one', async () => {
    setup();

    await screen.findByTestId('file-grid');
    fireEvent.click(screen.getByText('IMG_0001.png'));

    const viewer = await screen.findByTestId('viewer');
    expect(within(viewer).getByTestId('viewer-stage')).toBeInTheDocument();

    // Siblings are the favorites list, so the arrow keys walk them.
    fireEvent.keyDown(document.body, { key: 'ArrowRight' });
    await waitFor(() => {
      expect(screen.getByTestId('viewer')).toHaveTextContent('clip.mp4');
    });
  });

  it('un-stars from the viewer and re-reads the list', async () => {
    const fetchMock = setup();

    await screen.findByTestId('file-grid');
    fireEvent.click(screen.getByText('IMG_0001.png'));
    const viewer = await screen.findByTestId('viewer');

    const star = within(viewer).getByRole('button', { name: 'Remove from favorites' });
    expect(star).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(star);

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/files/f1/favorite')).toBe(true);
    });
    await waitFor(() => {
      expect(called(fetchMock, 'GET', '/api/v1/libraries/lib1/favorites')).toBe(true);
    });
  });

  it('trash dialogs come from the shared operations, not window.confirm', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'DELETE' && url.endsWith('/api/v1/libraries/lib1/files/f1')
          ? noContent()
          : undefined,
    ]);

    await screen.findByTestId('file-grid');
    fireEvent.click(screen.getByText('IMG_0001.png'));
    const viewer = await screen.findByTestId('viewer');
    fireEvent.click(within(viewer).getByTestId('viewer-trash'));

    const dialog = await screen.findByTestId('trash-dialog');
    // The page-level trash dialog, not the permanent one — this is not the Trash.
    expect(within(dialog).getByText(/moved to the trash/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Move to trash' }));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/files/f1')).toBe(true);
    });
    // The backend acts on the body path, so the path is what has to be right.
    expect(bodyOf(fetchMock, 'DELETE', '/api/v1/libraries/lib1/files/f1')).toEqual({
      path: 'IMG_0001.png',
    });
  });

  it('renames through a labelled prompt, not window.prompt', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.includes('/api/v1/libraries/lib1/files/f1/rename')
          ? json({ file: { ...photo, name: 'beach.png' } })
          : undefined,
    ]);

    await screen.findByTestId('file-grid');
    fireEvent.click(screen.getByText('IMG_0001.png'));
    const viewer = await screen.findByTestId('viewer');
    fireEvent.click(within(viewer).getByTestId('viewer-more'));
    fireEvent.click(await within(viewer).findByRole('menuitem', { name: 'Rename' }));

    const dialog = await screen.findByTestId('rename-dialog');
    fireEvent.change(screen.getByLabelText('New name'), { target: { value: 'beach.png' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/files/f1/rename')).toBe(true);
    });
    expect(bodyOf(fetchMock, 'POST', '/rename')).toEqual({
      path: 'IMG_0001.png',
      new_name: 'beach.png',
    });
  });

  it('retries a failed listing', async () => {
    let attempts = 0;
    mockApi([
      (url) => {
        if (!url.includes('/api/v1/libraries/lib1/favorites')) return undefined;
        attempts += 1;
        return attempts === 1
          ? apiError(500, 'INTERNAL', 'Favorites are unavailable right now.')
          : json(favorites);
      },
    ]);
    renderPage(<FavoritesPage />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Favorites are unavailable right now.');
    fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));

    expect(await screen.findByTestId('file-grid')).toBeInTheDocument();
    // The listing was asked for again, not served from a cached rejection.
    expect(attempts).toBeGreaterThanOrEqual(2);
  });

  it('warns when the library is offline', async () => {
    setup([], { libraries: [libraryFixture({ status: 'offline' })] });

    expect(await screen.findByTestId('library-offline')).toBeInTheDocument();
  });

  it('tells a user with no libraries how to get one', async () => {
    mockApi([], { libraries: [] });
    renderPage(<FavoritesPage />);

    expect(await screen.findByTestId('no-libraries')).toBeInTheDocument();
  });
});

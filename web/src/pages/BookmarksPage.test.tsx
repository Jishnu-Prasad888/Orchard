import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  apiError,
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
import BookmarksPage from './BookmarksPage';

const book = fileFixture({
  id: 'f1',
  rel_path: 'dune.epub',
  name: 'dune.epub',
  media_type: 'document',
});

const bookmark = {
  id: 'bm1',
  file_id: 'f1',
  location: 'epubcfi(/6/14!/4/2)',
  label: 'Start of chapter 3',
  created_at: '2026-10-01T00:00:00Z',
};

const highlight = {
  id: 'hl1',
  file_id: 'f1',
  location: 'epubcfi(/6/14!/4/4)',
  text: 'The spice must flow.',
  color: 'yellow',
  note: 'key theme',
  created_at: '2026-10-02T00:00:00Z',
  updated_at: '2026-10-02T00:00:00Z',
};

function apiRules(overrides: RouteHandler[] = []): RouteHandler[] {
  return [
    ...overrides,
    (url) =>
      url.includes('/api/v1/libraries/lib1/bookmarks')
        ? json({ bookmarks: [bookmark] })
        : undefined,
    (url) =>
      url.includes('/api/v1/libraries/lib1/highlights')
        ? json({ highlights: [highlight] })
        : undefined,
    (url) =>
      url.includes('/api/v1/libraries/lib1/files/f1/book-metadata')
        ? json({ book: null })
        : undefined,
    (url) => (url.endsWith('/api/v1/libraries/lib1/files/f1') ? json({ file: book }) : undefined),
  ];
}

function setup(overrides: RouteHandler[] = [], options = {}) {
  const fn = mockApi(apiRules(overrides), options);
  renderPage(<BookmarksPage />, options);
  return fn;
}

describe('BookmarksPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('lists bookmarks and highlights for the library', async () => {
    setup();

    expect(await screen.findByTestId('bookmarks-list')).toBeInTheDocument();
    expect(screen.getByText('Start of chapter 3')).toBeInTheDocument();
    const highlights = await screen.findByTestId('highlights-list');
    expect(within(highlights).getByText(/The spice must flow/)).toBeInTheDocument();
    expect(within(highlights).getByText('key theme')).toBeInTheDocument();
  });

  it('shows empty states when there is nothing yet', async () => {
    setup([
      (url) =>
        url.includes('/api/v1/libraries/lib1/bookmarks') ? json({ bookmarks: [] }) : undefined,
      (url) =>
        url.includes('/api/v1/libraries/lib1/highlights') ? json({ highlights: [] }) : undefined,
    ]);

    expect(await screen.findByTestId('bookmarks-empty')).toBeInTheDocument();
    expect(await screen.findByTestId('highlights-empty')).toBeInTheDocument();
  });

  it('deletes a bookmark and re-reads the list', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'DELETE' && url.includes('/api/v1/libraries/lib1/bookmarks/bm1')
          ? noContent()
          : undefined,
    ]);

    await screen.findByTestId('bookmarks-list');
    fireEvent.click(screen.getByRole('button', { name: 'Delete bookmark' }));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/bookmarks/bm1')).toBe(true);
    });
  });

  it('deletes a highlight and re-reads the list', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'DELETE' && url.includes('/api/v1/libraries/lib1/highlights/hl1')
          ? noContent()
          : undefined,
    ]);

    await screen.findByTestId('highlights-list');
    fireEvent.click(screen.getByRole('button', { name: 'Delete highlight' }));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/highlights/hl1')).toBe(true);
    });
  });

  it('links a bookmark into the reader at its saved location', async () => {
    setup();

    const list = await screen.findByTestId('bookmarks-list');
    const link = within(list).getByRole('link');
    expect(link).toHaveAttribute(
      'href',
      `/read/lib1/f1?loc=${encodeURIComponent(bookmark.location)}`,
    );
  });

  it('retries a failed listing', async () => {
    let attempts = 0;
    mockApi(
      apiRules([
        (url) => {
          if (!url.includes('/api/v1/libraries/lib1/bookmarks')) return undefined;
          attempts += 1;
          return attempts === 1
            ? apiError(500, 'INTERNAL', 'Bookmarks are unavailable right now.')
            : json({ bookmarks: [bookmark] });
        },
      ]),
    );
    renderPage(<BookmarksPage />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Bookmarks are unavailable right now.');
    fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));

    expect(await screen.findByTestId('bookmarks-list')).toBeInTheDocument();
    expect(attempts).toBeGreaterThanOrEqual(2);
  });

  it('warns when the library is offline', async () => {
    setup([], { libraries: [libraryFixture({ status: 'offline' })] });

    expect(await screen.findByTestId('library-offline')).toBeInTheDocument();
  });

  it('tells a user with no libraries how to get one', async () => {
    mockApi([], { libraries: [] });
    renderPage(<BookmarksPage />);

    expect(await screen.findByTestId('no-libraries')).toBeInTheDocument();
  });
});

import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { apiError, called, fileFixture, json, mockApi, originalFetch, renderPage } from '../test/harness';
import type { Collection, InProgressFile, Note, Quote } from '../api/types';
import HomePage from './HomePage';

interface Options {
  recent?: ReturnType<typeof fileFixture>[];
  recentlyOpened?: ReturnType<typeof fileFixture>[];
  inProgress?: InProgressFile[];
  favorites?: ReturnType<typeof fileFixture>[];
  collections?: Collection[];
  quotes?: Quote[];
  notes?: Note[];
}

function setup(options: Options = {}) {
  const recent = options.recent ?? [
    fileFixture({ id: 'f1' }),
    fileFixture({ id: 'f2', name: 'IMG_0002.png' }),
  ];
  const recentlyOpened = options.recentlyOpened ?? [];
  const inProgress = options.inProgress ?? [];
  const favorites = options.favorites ?? [];
  const collections = options.collections ?? [];
  const quotes = options.quotes ?? [];
  const notes = options.notes ?? [];
  const fn = mockApi([
    (url) =>
      url.includes('/api/v1/libraries/lib1/files/recently-opened')
        ? json({ files: recentlyOpened, total: recentlyOpened.length })
        : undefined,
    (url) =>
      url.includes('/api/v1/libraries/lib1/reading/in-progress')
        ? json({ files: inProgress })
        : undefined,
    (url) =>
      url.includes('/api/v1/libraries/lib1/favorites')
        ? json({ files: favorites })
        : undefined,
    (url) =>
      url.includes('/api/v1/libraries/lib1/collections')
        ? json({ collections })
        : undefined,
    (url) =>
      url.includes('/api/v1/libraries/lib1/quotes')
        ? json({ quotes })
        : undefined,
    (url) =>
      url.includes('/api/v1/libraries/lib1/notes')
        ? json({ notes })
        : undefined,
    (url) =>
      url.includes('/api/v1/settings/home-layout')
        ? json({
            sections: [
              { id: 'continue-reading', visible: true },
              { id: 'recently-opened', visible: true },
              { id: 'recently-added', visible: true },
              { id: 'favorites', visible: true },
              { id: 'pinned-collections', visible: true },
              { id: 'recent-quotes', visible: true },
              { id: 'recent-notes', visible: true },
            ],
          })
        : undefined,
    (url) =>
      url.includes('/api/v1/libraries/lib1/files')
        ? json({ files: recent, total: recent.length })
        : undefined,
  ]);
  renderPage(<HomePage />);
  return fn;
}

describe('HomePage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('greets the signed-in account, in the time of day', async () => {
    setup();

    // The gate page and then Home each render the greeting, so the first
    // match can be replaced mid-assertion: retry the whole check.
    await waitFor(() =>
      expect(
        screen.getByRole('heading', { name: /^Good (morning|afternoon|evening), jishnu$/ }),
      ).toBeInTheDocument(),
    );
  });

  it('leads with the newest files, and nothing like a counter dashboard', async () => {
    setup();

    const recent = await screen.findByTestId('home-recent');
    expect(within(recent).getAllByRole('button', { name: /IMG_000/ })).toHaveLength(2);
    expect(screen.getByRole('link', { name: /All files/ })).toHaveAttribute('href', '/files');
  });

  it('shows a recently-opened section when the user has opened a book', async () => {
    setup({ recentlyOpened: [fileFixture({ id: 'f3', name: 'novel.epub', media_type: 'document' })] });

    const opened = await screen.findByTestId('home-recently-opened');
    expect(within(opened).getByRole('button', { name: 'novel.epub' })).toBeInTheDocument();
  });

  it('omits the recently-opened section when nothing has been opened', async () => {
    setup();

    await screen.findByTestId('home-recent');
    expect(screen.queryByTestId('home-recently-opened')).not.toBeInTheDocument();
  });

  it('shows an inviting empty state when nothing has been indexed yet', async () => {
    setup({ recent: [] });

    expect(await screen.findByTestId('home-no-files')).toBeInTheDocument();
  });

  it('reports a library-list failure instead of rendering an empty home', async () => {
    mockApi([
      (url) =>
        url.endsWith('/api/v1/libraries')
          ? apiError(500, 'INTERNAL', 'Libraries are unavailable.')
          : undefined,
    ]);
    renderPage(<HomePage />);

    expect(await screen.findByText('Libraries are unavailable.')).toBeInTheDocument();
    expect(screen.queryByTestId('home-recent')).not.toBeInTheDocument();
  });

  it('offers a way forward when there are no libraries', async () => {
    mockApi([], { libraries: [] });
    renderPage(<HomePage />, { libraries: [] });

    expect(await screen.findByTestId('no-libraries')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Add a library' })).toHaveAttribute(
      'href',
      '/libraries',
    );
  });

  it('shows a continue-reading section with a resume progress bar', async () => {
    setup({
      inProgress: [
        {
          ...fileFixture({ id: 'f9', name: 'novel.epub', media_type: 'document' }),
          percent: 42,
          location: 'epubcfi(/6/4)',
        },
      ],
    });

    const section = await screen.findByTestId('home-in-progress');
    expect(within(section).getByRole('button', { name: 'novel.epub' })).toBeInTheDocument();
    expect(within(section).getByRole('progressbar')).toHaveAttribute('aria-valuenow', '42');
  });

  it('omits the continue-reading section when nothing is in progress', async () => {
    setup();
    await screen.findByTestId('home-recent');
    expect(screen.queryByTestId('home-in-progress')).not.toBeInTheDocument();
  });

  it('shows a favorites section with a link to the full page', async () => {
    setup({ favorites: [fileFixture({ id: 'f8', name: 'starred.png' })] });

    const section = await screen.findByTestId('home-favorites');
    expect(within(section).getByRole('button', { name: 'starred.png' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /All favorites/ })).toHaveAttribute(
      'href',
      '/favorites',
    );
  });

  it('shows only pinned collections, deep-linked to the collection', async () => {
    setup({
      collections: [
        {
          id: 'c1',
          name: 'Summer reading',
          pinned: true,
          item_count: 3,
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z',
        },
        {
          id: 'c2',
          name: 'Unpinned shelf',
          pinned: false,
          item_count: 1,
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z',
        },
      ],
    });

    const section = await screen.findByTestId('home-pinned-collections');
    expect(within(section).getByText('Summer reading')).toBeInTheDocument();
    expect(within(section).queryByText('Unpinned shelf')).not.toBeInTheDocument();
    expect(within(section).getByRole('link', { name: /Summer reading/ })).toHaveAttribute(
      'href',
      '/collections?open=c1',
    );
  });

  it('shows recent quotes and recent notes, the note deep-linked', async () => {
    setup({
      quotes: [
        {
          id: 'q1',
          file_id: 'f1',
          text: 'A line worth keeping.',
          favorite: false,
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z',
        },
      ],
      notes: [
        {
          id: 'n1',
          title: 'Reading notes',
          body: 'Some thoughts.',
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z',
        },
      ],
    });

    const quoteSection = await screen.findByTestId('home-recent-quotes');
    expect(within(quoteSection).getByText('A line worth keeping.')).toBeInTheDocument();

    const noteSection = await screen.findByTestId('home-recent-notes');
    expect(within(noteSection).getByRole('link', { name: /Reading notes/ })).toHaveAttribute(
      'href',
      '/notes?open=n1',
    );
  });

  it('lets a user hide a section and saves the change', async () => {
    const fn = setup({ favorites: [fileFixture({ id: 'f8', name: 'starred.png' })] });
    await screen.findByTestId('home-favorites');

    fireEvent.click(screen.getByTestId('home-customize-button'));
    const dialog = await screen.findByRole('dialog', { name: 'Customize Home' });
    fireEvent.click(within(dialog).getByTestId('home-customize-toggle-favorites'));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(called(fn, 'PUT', '/settings/home-layout')).toBe(true));
  });
});

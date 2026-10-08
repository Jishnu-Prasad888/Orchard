import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { apiError, fileFixture, json, mockApi, originalFetch, renderPage } from '../test/harness';
import HomePage from './HomePage';

interface Options {
  recent?: ReturnType<typeof fileFixture>[];
  albums?: unknown[];
  people?: unknown[] | 'unsupported';
  memories?: unknown[];
}

const memory = (id: string, title: string, updated_at: string, body = '') => ({
  id,
  title,
  body,
  deleted: false,
  created_at: updated_at,
  updated_at,
});

function setup(options: Options = {}) {
  const recent = options.recent ?? [
    fileFixture({ id: 'f1' }),
    fileFixture({ id: 'f2', name: 'IMG_0002.png' }),
  ];
  const fn = mockApi([
    (url) =>
      url.includes('/api/v1/libraries/lib1/files')
        ? json({ files: recent, total: recent.length })
        : undefined,
    (url) =>
      url.endsWith('/api/v1/libraries/lib1/albums')
        ? json({ albums: options.albums ?? [] })
        : undefined,
    (url) =>
      url.includes('/api/v1/libraries/lib1/memories')
        ? json({ memories: options.memories ?? [] })
        : undefined,
    (url) => {
      if (!url.endsWith('/api/v1/libraries/lib1/people')) return undefined;
      if (options.people === 'unsupported') {
        return json({ error: { code: 'NOT_FOUND', message: 'no faces', request_id: '1' } }, 404);
      }
      return json({ people: options.people ?? [] });
    },
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

  it('leads with the newest photos, and nothing like a counter dashboard', async () => {
    setup();

    const recent = await screen.findByTestId('home-recent');
    expect(within(recent).getAllByRole('button', { name: /IMG_000/ })).toHaveLength(2);
    expect(screen.getByRole('link', { name: /All photos/ })).toHaveAttribute('href', '/photos');
    expect(screen.queryByTestId('home-tile-/media?type=photo')).not.toBeInTheDocument();
  });

  it('shows an inviting empty state when nothing has been indexed yet', async () => {
    setup({ recent: [] });

    expect(await screen.findByTestId('home-no-photos')).toBeInTheDocument();
  });

  it('shows albums with their covers when there are any', async () => {
    setup({
      albums: [
        {
          id: 'a1',
          name: 'Vacation',
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-02T00:00:00Z',
          file_count: 3,
          preview_file_id: 'f1',
        },
      ],
    });

    const albums = await screen.findByTestId('home-albums');
    expect(within(albums).getByText('Vacation')).toBeInTheDocument();
    expect(within(albums).getByText('3 items')).toBeInTheDocument();
  });

  it('shows named people', async () => {
    setup({ people: [{ id: 'p1', name: 'Mom', face_count: 4 }] });

    const people = await screen.findByTestId('home-people');
    expect(within(people).getByText('Mom')).toBeInTheDocument();
  });

  it('omits people when the build has no face support', async () => {
    setup({ people: 'unsupported' });

    await screen.findByTestId('home-recent');
    expect(screen.queryByTestId('home-people')).not.toBeInTheDocument();
  });

  it('lists the most recently edited memories, newest first, with an excerpt', async () => {
    setup({
      memories: [
        memory('m1', 'Old trip', '2026-01-01T00:00:00Z'),
        memory('m2', 'Birthday', '2026-09-01T00:00:00Z', '# Birthday\n\nWe had **cake**.'),
      ],
    });

    const list = await screen.findByTestId('home-memories');
    const links = within(list).getAllByRole('link');
    expect(links[0]).toHaveTextContent('Birthday');
    expect(links[0]).toHaveTextContent('We had cake.');
    expect(links[0]).toHaveAttribute('href', '/memories/m2');
    expect(links[1]).toHaveTextContent('Old trip');
  });

  it('invites the first memory when there are none', async () => {
    setup();

    expect(await screen.findByTestId('home-no-memories')).toBeInTheDocument();
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
});

import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { apiError, fileFixture, json, mockApi, originalFetch, renderPage } from '../test/harness';
import HomePage from './HomePage';

interface Options {
  recent?: ReturnType<typeof fileFixture>[];
}

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
});

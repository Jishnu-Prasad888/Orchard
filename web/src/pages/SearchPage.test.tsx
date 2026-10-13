import { screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { json, mockApi, originalFetch, renderPage } from '../test/harness';
import type { RouteHandler } from '../test/harness';
import SearchPage from './SearchPage';

function apiRules(overrides: RouteHandler[] = []): RouteHandler[] {
  return [
    ...overrides,
    (url) => (url.includes('/api/v1/libraries/lib1/collections') ? json({ collections: [] }) : undefined),
    (url) =>
      url.includes('/api/v1/libraries/lib1/search?') || url.endsWith('/api/v1/libraries/lib1/search')
        ? json({ files: [], next_cursor: '', total: 0 })
        : undefined,
    (url) =>
      url.includes('/api/v1/libraries/lib1/search/content')
        ? json({ hits: [] })
        : undefined,
  ];
}

function setup(overrides: RouteHandler[] = [], route = '/search?q=dune') {
  mockApi(apiRules(overrides));
  renderPage(<SearchPage />, { route });
}

describe('SearchPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('shows content hits below the (empty) file results', async () => {
    setup([
      (url) =>
        url.includes('/api/v1/libraries/lib1/search/content')
          ? json({
              hits: [
                {
                  source_type: 'quote',
                  source_id: 'q1',
                  file_id: 'f1',
                  excerpt: 'fear is the mindkiller',
                  location: 'page:42',
                },
                { source_type: 'note', source_id: 'n1', excerpt: 'a standalone note about dunes' },
              ],
            })
          : undefined,
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/files/f1')
          ? json({
              file: {
                id: 'f1',
                library_id: 'lib1',
                rel_path: 'dune.epub',
                name: 'dune.epub',
                folder_path: '',
                size_bytes: 10,
                mod_time: '2026-01-01T00:00:00Z',
                media_type: 'document',
                mime_type: 'application/epub+zip',
                status: 'present',
              },
            })
          : undefined,
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/files/f1/book-metadata') ? json({ book: null }) : undefined,
    ]);

    const section = await screen.findByTestId('content-search-results');
    expect(within(section).getByText(/mindkiller/)).toBeInTheDocument();
    expect(within(section).getByText(/standalone note about dunes/)).toBeInTheDocument();
    expect(within(section).getByText('Your quote')).toBeInTheDocument();
    expect(within(section).getByText('Your note')).toBeInTheDocument();

    const noteLink = within(section).getByText(/standalone note/).closest('a');
    expect(noteLink).toHaveAttribute('href', '/notes?open=n1');

    const quoteLink = within(section).getByText(/mindkiller/).closest('a');
    expect(quoteLink).toHaveAttribute('href', `/read/lib1/f1?loc=${encodeURIComponent('page:42')}`);
  });

  it('shows nothing extra when there are no content hits', async () => {
    setup();

    await screen.findByText('Results for “dune”');
    expect(screen.queryByTestId('content-search-results')).not.toBeInTheDocument();
  });
});

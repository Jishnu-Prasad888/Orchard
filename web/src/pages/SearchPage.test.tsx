import { fireEvent, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { fileFixture, json, mockApi, originalFetch, renderPage } from '../test/harness';
import type { RouteHandler } from '../test/harness';
import SearchPage from './SearchPage';

function apiRules(overrides: RouteHandler[] = []): RouteHandler[] {
  return [
    ...overrides,
    (url) =>
      url.includes('/api/v1/libraries/lib1/collections') ? json({ collections: [] }) : undefined,
    (url) =>
      url.includes('/api/v1/libraries/lib1/search?') ||
      url.endsWith('/api/v1/libraries/lib1/search')
        ? json({ files: [], next_cursor: '', total: 0 })
        : undefined,
    (url) =>
      url.includes('/api/v1/libraries/lib1/search/content') ? json({ hits: [] }) : undefined,
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
                {
                  source_type: 'highlight',
                  source_id: 'h1',
                  file_id: 'f1',
                  excerpt: 'the spice must flow',
                },
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
        url.endsWith('/api/v1/libraries/lib1/files/f1/book-metadata')
          ? json({ book: null })
          : undefined,
    ]);

    const section = await screen.findByTestId('content-search-results');
    expect(within(section).getByText(/mindkiller/)).toBeInTheDocument();
    expect(within(section).getByText(/spice must flow/)).toBeInTheDocument();
    expect(within(section).getByText('Your quote')).toBeInTheDocument();
    expect(within(section).getByText('Your highlight')).toBeInTheDocument();

    // A highlight with no saved location falls back to its list page.
    const highlightLink = within(section)
      .getByText(/spice must flow/)
      .closest('a');
    expect(highlightLink).toHaveAttribute('href', '/bookmarks');

    const quoteLink = within(section)
      .getByText(/mindkiller/)
      .closest('a');
    expect(quoteLink).toHaveAttribute('href', `/read/lib1/f1?loc=${encodeURIComponent('page:42')}`);
  });

  it('shows nothing extra when there are no content hits', async () => {
    setup();

    await screen.findByText('Results for “dune”');
    expect(screen.queryByTestId('content-search-results')).not.toBeInTheDocument();
  });

  it('folds matches in hidden (completed) books away below the results', async () => {
    setup([
      (url) =>
        url.includes('/api/v1/settings/hide-completed')
          ? json({ hide_completed: true })
          : undefined,
      (url) =>
        url.includes('/api/v1/libraries/lib1/search?')
          ? json({
              files: [
                fileFixture({ id: 'f1', name: 'Dune.epub' }),
                fileFixture({ id: 'f2', name: 'Dune Messiah.epub', completed: true }),
              ],
              next_cursor: '',
              total: 2,
            })
          : undefined,
    ]);

    const hidden = await screen.findByTestId('search-hidden');
    expect(within(hidden).getByText('1 more match in a hidden book')).toBeInTheDocument();
    expect(hidden).not.toHaveAttribute('open');
    expect(within(screen.getByTestId('file-grid')).queryByText('Dune Messiah.epub')).toBeNull();

    fireEvent.click(within(hidden).getByText('1 more match in a hidden book'));
    expect(hidden).toHaveAttribute('open');
    expect(within(hidden).getByText('Dune Messiah.epub')).toBeInTheDocument();
  });

  it('has no hidden section while completed books are shown', async () => {
    setup([
      (url) =>
        url.includes('/api/v1/libraries/lib1/search?')
          ? json({
              files: [fileFixture({ id: 'f2', name: 'Dune Messiah.epub', completed: true })],
              next_cursor: '',
              total: 1,
            })
          : undefined,
    ]);

    expect(await screen.findByText('Dune Messiah.epub')).toBeInTheDocument();
    expect(screen.queryByTestId('search-hidden')).not.toBeInTheDocument();
  });
});

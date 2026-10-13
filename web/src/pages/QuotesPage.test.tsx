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
import QuotesPage from './QuotesPage';

const book = fileFixture({ id: 'f1', rel_path: 'dune.epub', name: 'dune.epub', media_type: 'document' });

const quote = {
  id: 'q1',
  file_id: 'f1',
  text: 'Fear is the mind-killer.',
  location: 'epubcfi(/6/14!/4/2)',
  author: 'Frank Herbert',
  chapter: 'Litany Against Fear',
  page: '8',
  commentary: 'Read this every morning.',
  favorite: false,
  created_at: '2026-10-01T00:00:00Z',
  updated_at: '2026-10-01T00:00:00Z',
};

function apiRules(overrides: RouteHandler[] = []): RouteHandler[] {
  return [
    ...overrides,
    (url) => (url.includes('/api/v1/libraries/lib1/quotes') ? json({ quotes: [quote] }) : undefined),
    (url) => (url.includes('/api/v1/libraries/lib1/files/f1/book-metadata') ? json({ book: null }) : undefined),
    (url) => (url.endsWith('/api/v1/libraries/lib1/files/f1') ? json({ file: book }) : undefined),
  ];
}

function setup(overrides: RouteHandler[] = [], options = {}) {
  const fn = mockApi(apiRules(overrides), options);
  renderPage(<QuotesPage />, options);
  return fn;
}

describe('QuotesPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('lists quotes with their citation details', async () => {
    setup();

    const list = await screen.findByTestId('quotes-list');
    expect(within(list).getByText(/Fear is the mind-killer/)).toBeInTheDocument();
    expect(within(list).getByText(/Frank Herbert/)).toBeInTheDocument();
    expect(within(list).getByText('Read this every morning.')).toBeInTheDocument();
  });

  it('shows an empty state when there are no quotes', async () => {
    setup([
      (url) => (url.includes('/api/v1/libraries/lib1/quotes') ? json({ quotes: [] }) : undefined),
    ]);

    expect(await screen.findByTestId('quotes-empty')).toBeInTheDocument();
  });

  it('re-reads with the favorites filter applied', async () => {
    const fetchMock = setup();

    await screen.findByTestId('quotes-list');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Favorites only' }));

    await waitFor(() => {
      expect(called(fetchMock, 'GET', '/api/v1/libraries/lib1/quotes?favorite=1')).toBe(true);
    });
  });

  it('toggles favorite from the list', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'PATCH' && url.includes('/api/v1/libraries/lib1/quotes/q1')
          ? json({ quote: { ...quote, favorite: true } })
          : undefined,
    ]);

    await screen.findByTestId('quotes-list');
    fireEvent.click(screen.getByRole('button', { name: 'Favorite' }));

    await waitFor(() => {
      expect(called(fetchMock, 'PATCH', '/api/v1/libraries/lib1/quotes/q1')).toBe(true);
    });
    expect(bodyOf(fetchMock, 'PATCH', '/api/v1/libraries/lib1/quotes/q1')).toEqual({ favorite: true });
  });

  it('deletes a quote through a confirm dialog, not window.confirm', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'DELETE' && url.includes('/api/v1/libraries/lib1/quotes/q1')
          ? noContent()
          : undefined,
    ]);

    await screen.findByTestId('quotes-list');
    fireEvent.click(screen.getByRole('button', { name: 'Delete quote' }));

    const dialog = await screen.findByTestId('delete-quote-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete quote' }));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/quotes/q1')).toBe(true);
    });
  });

  it('adds a quote by hand through a labelled form', async () => {
    const fetchMock = setup([
      (url, init) =>
        (init?.method ?? 'GET') === 'GET' && url.includes('/api/v1/libraries/lib1/files?')
          ? json({ files: [book], next_cursor: '', total: 1 })
          : undefined,
      (url, init) =>
        init?.method === 'POST' && url.includes('/api/v1/libraries/lib1/files/f1/quotes')
          ? json({ quote })
          : undefined,
    ]);

    await screen.findByTestId('quotes-list');
    fireEvent.click(screen.getByRole('button', { name: 'Save a quote' }));

    const dialog = await screen.findByTestId('add-quote-dialog');
    await within(dialog).findByText('dune.epub');
    fireEvent.change(within(dialog).getByTestId('quote-book-select'), { target: { value: 'f1' } });
    fireEvent.change(within(dialog).getByTestId('quote-text-input'), {
      target: { value: 'A new quote.' },
    });
    fireEvent.click(within(dialog).getByTestId('confirm-add-quote'));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/files/f1/quotes')).toBe(true);
    });
    expect(bodyOf(fetchMock, 'POST', '/api/v1/libraries/lib1/files/f1/quotes')).toMatchObject({
      text: 'A new quote.',
    });
  });

  it('exports quotes as a Markdown file', async () => {
    setup();
    await screen.findByTestId('quotes-list');

    const createObjectURL = vi.fn(() => 'blob:mock');
    const revokeObjectURL = vi.fn();
    vi.stubGlobal('URL', { ...URL, createObjectURL, revokeObjectURL });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});

    fireEvent.click(screen.getByTestId('export-quotes-button'));

    expect(createObjectURL).toHaveBeenCalled();
    expect(clickSpy).toHaveBeenCalled();
    clickSpy.mockRestore();
  });

  it('retries a failed listing', async () => {
    let attempts = 0;
    mockApi(
      apiRules([
        (url) => {
          if (!url.includes('/api/v1/libraries/lib1/quotes')) return undefined;
          attempts += 1;
          return attempts === 1
            ? apiError(500, 'INTERNAL', 'Quotes are unavailable right now.')
            : json({ quotes: [quote] });
        },
      ]),
    );
    renderPage(<QuotesPage />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Quotes are unavailable right now.');
    fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));

    expect(await screen.findByTestId('quotes-list')).toBeInTheDocument();
    expect(attempts).toBeGreaterThanOrEqual(2);
  });

  it('warns when the library is offline', async () => {
    setup([], { libraries: [libraryFixture({ status: 'offline' })] });

    expect(await screen.findByTestId('library-offline')).toBeInTheDocument();
  });

  it('tells a user with no libraries how to get one', async () => {
    mockApi([], { libraries: [] });
    renderPage(<QuotesPage />);

    expect(await screen.findByTestId('no-libraries')).toBeInTheDocument();
  });
});

import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { called, json, mockApi, noContent, originalFetch } from '../test/harness';
import type { RouteHandler } from '../test/harness';
import Providers from '../test/Providers';
import ReaderPage from './ReaderPage';

const DEFAULT_PREFS = {
  font_size: 18,
  line_spacing: 1.5,
  margin: 24,
  reading_width: 720,
  theme: 'light',
};

const mdFile = {
  id: 'f1',
  library_id: 'lib1',
  rel_path: 'notes/essay.md',
  name: 'essay.md',
  folder_path: 'notes',
  size_bytes: 128,
  mod_time: '2026-09-01T00:00:00Z',
  media_type: 'document',
  mime_type: 'text/markdown',
  status: 'present',
};

const emptyBookMetadata = {
  book: {
    file_id: 'f1',
    title: 'The Great Essay',
    author: 'Jane Author',
    publisher: '',
    year: '',
    isbn: '',
    description: '',
    user_edited: false,
  },
};

const MARKDOWN_BODY = '# Introduction\n\nFirst section text.\n\n# Conclusion\n\nClosing thoughts.';

function apiRules(overrides: RouteHandler[] = []): RouteHandler[] {
  return [
    ...overrides,
    (url) => (url.endsWith('/api/v1/libraries/lib1/files/f1') ? json({ file: mdFile }) : undefined),
    (url) =>
      url.endsWith('/api/v1/libraries/lib1/files/f1/book-metadata')
        ? json(emptyBookMetadata)
        : undefined,
    (url) =>
      url.endsWith('/api/v1/libraries/lib1/files/f1/progress')
        ? json({ progress: null })
        : undefined,
    (url) =>
      url.endsWith('/api/v1/libraries/lib1/files/f1/reading-status')
        ? json({ status: null })
        : undefined,
    (url) =>
      url.endsWith('/api/v1/libraries/lib1/files/f1/bookmarks') ? json({ bookmarks: [] }) : undefined,
    (url) =>
      url.endsWith('/api/v1/libraries/lib1/files/f1/highlights') ? json({ highlights: [] }) : undefined,
    (url) => (url.endsWith('/api/v1/settings/reader') ? json({ preferences: DEFAULT_PREFS }) : undefined),
    (url, init) =>
      init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/files/f1/opened')
        ? noContent()
        : undefined,
    (url) =>
      url.endsWith('/api/v1/libraries/lib1/files/f1/download')
        ? new Response(MARKDOWN_BODY, { status: 200, headers: { 'Content-Type': 'text/markdown' } })
        : undefined,
  ];
}

function setup(overrides: RouteHandler[] = []) {
  const fn = mockApi(apiRules(overrides));
  render(
    <MemoryRouter initialEntries={['/read/lib1/f1']}>
      <Providers>
        <Routes>
          <Route path="/read/:libraryId/:fileId" element={<ReaderPage />} />
          <Route path="/files" element={<div>files page</div>} />
        </Routes>
      </Providers>
    </MemoryRouter>,
  );
  return fn;
}

describe('ReaderPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('shows the book title and author once loaded', async () => {
    setup();
    expect(await screen.findByText('The Great Essay')).toBeInTheDocument();
    expect(screen.getByText(/Jane Author/)).toBeInTheDocument();
  });

  it('renders the Markdown content', async () => {
    setup();
    expect(await screen.findByRole('heading', { name: 'Introduction' })).toBeInTheDocument();
    expect(screen.getByText('First section text.')).toBeInTheDocument();
  });

  it('records the file as opened on mount', async () => {
    const fetchMock = setup();
    await screen.findByText('The Great Essay');
    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/files/f1/opened')).toBe(true);
    });
  });

  it('builds a table of contents from the headings and jumps to one', async () => {
    setup();
    await screen.findByRole('heading', { name: 'Introduction' });

    // The TOC button stays disabled until TextReader reports it has one.
    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Table of contents' })).toBeEnabled();
    });
    fireEvent.click(screen.getByRole('button', { name: 'Table of contents' }));
    const toc = await screen.findByTestId('reader-toc');
    expect(within(toc).getByRole('button', { name: 'Introduction' })).toBeInTheDocument();
    expect(within(toc).getByRole('button', { name: 'Conclusion' })).toBeInTheDocument();

    fireEvent.click(within(toc).getByRole('button', { name: 'Conclusion' }));
    // No crash / no error state is the main thing; scrollIntoView is a jsdom no-op.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('searches the book and lists matching excerpts', async () => {
    setup();
    await screen.findByRole('heading', { name: 'Introduction' });

    fireEvent.click(screen.getByRole('button', { name: 'Search this book' }));
    const panel = await screen.findByTestId('reader-search');
    fireEvent.change(within(panel).getByLabelText('Search this book'), {
      target: { value: 'Closing' },
    });
    fireEvent.submit(within(panel).getByLabelText('Search this book').closest('form')!);

    expect(await within(panel).findByText(/Closing thoughts/)).toBeInTheDocument();
  });

  it('saves a reader preference change', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'PUT' && url.endsWith('/api/v1/settings/reader')
          ? json({ preferences: { ...DEFAULT_PREFS, theme: 'dark' } })
          : undefined,
    ]);
    await screen.findByText('The Great Essay');

    fireEvent.click(screen.getByRole('button', { name: 'Reading settings' }));
    const panel = await screen.findByTestId('reader-settings');
    fireEvent.click(within(panel).getByRole('radio', { name: 'Dark' }));

    await waitFor(() => {
      expect(called(fetchMock, 'PUT', '/api/v1/settings/reader')).toBe(true);
    });
  });

  it('opens the book details panel for editing metadata', async () => {
    setup();
    await screen.findByText('The Great Essay');

    fireEvent.click(screen.getByRole('button', { name: 'Book details' }));
    const panel = await screen.findByTestId('viewer-book');
    expect(within(panel).getByDisplayValue('The Great Essay')).toBeInTheDocument();
  });

  it('shows an error state when the file fails to load', async () => {
    mockApi([
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/files/f1')
          ? new Response(
              JSON.stringify({ error: { code: 'NOT_FOUND', message: 'File not found.', request_id: 'r1' } }),
              { status: 404, headers: { 'Content-Type': 'application/json' } },
            )
          : undefined,
    ]);
    render(
      <MemoryRouter initialEntries={['/read/lib1/f1']}>
        <Providers>
          <Routes>
            <Route path="/read/:libraryId/:fileId" element={<ReaderPage />} />
          </Routes>
        </Providers>
      </MemoryRouter>,
    );

    expect(await screen.findByText('File not found.')).toBeInTheDocument();
  });

  it('goes back when the back button is pressed', async () => {
    mockApi(apiRules());
    render(
      <MemoryRouter initialEntries={['/files', '/read/lib1/f1']} initialIndex={1}>
        <Providers>
          <Routes>
            <Route path="/read/:libraryId/:fileId" element={<ReaderPage />} />
            <Route path="/files" element={<div>files page</div>} />
          </Routes>
        </Providers>
      </MemoryRouter>,
    );
    await screen.findByText('The Great Essay');
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    expect(await screen.findByText('files page')).toBeInTheDocument();
  });

  it('adds a bookmark at the current location', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/files/f1/bookmarks')
          ? json({ bookmark: { id: 'bm1', file_id: 'f1', location: '', label: '', created_at: '2026-10-01T00:00:00Z' } })
          : undefined,
    ]);
    await screen.findByText('The Great Essay');

    fireEvent.click(screen.getByRole('button', { name: 'Bookmarks and highlights' }));
    const panel = await screen.findByTestId('reader-annotations');
    fireEvent.click(within(panel).getByRole('button', { name: /Add here/ }));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/files/f1/bookmarks')).toBe(true);
    });
    expect(await within(panel).findByRole('button', { name: 'Remove bookmark' })).toBeInTheDocument();
  });

  it('selects text and saves a highlight', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/files/f1/highlights')
          ? json({
              highlight: {
                id: 'hl1',
                file_id: 'f1',
                location: 'block-2',
                text: 'First section text.',
                color: 'yellow',
                note: '',
                created_at: '2026-10-01T00:00:00Z',
                updated_at: '2026-10-01T00:00:00Z',
              },
            })
          : undefined,
    ]);
    const paragraph = await screen.findByText('First section text.');

    const range = document.createRange();
    range.selectNodeContents(paragraph);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    document.dispatchEvent(new Event('selectionchange'));

    const bar = await screen.findByText('“First section text.”');
    fireEvent.click(within(bar.closest('.reader-selection-bar')!).getByRole('button', { name: /Highlight/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/files/f1/highlights')).toBe(true);
    });
  });
});

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
    (url) => (url.endsWith('/api/v1/libraries/lib1/files/f1/quotes') ? json({ quotes: [] }) : undefined),
    (url) =>
      url.includes('/api/v1/libraries/lib1/files/f1/search/content') ? json({ hits: [] }) : undefined,
    (url) => (url.includes('/api/v1/settings/reader') ? json({ preferences: DEFAULT_PREFS }) : undefined),
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

  it('also shows content-search hits about this book, jumping to a located one', async () => {
    setup([
      (url, init) =>
        (init?.method ?? 'GET') === 'GET' && url.includes('/api/v1/libraries/lib1/files/f1/search/content')
          ? json({
              hits: [
                {
                  source_type: 'quote',
                  source_id: 'q1',
                  file_id: 'f1',
                  excerpt: 'Closing thoughts on everything.',
                  location: 'block-3',
                },
                { source_type: 'book_text', source_id: 'f1', file_id: 'f1', excerpt: 'no location for this one' },
              ],
            })
          : undefined,
    ]);
    await screen.findByRole('heading', { name: 'Introduction' });

    fireEvent.click(screen.getByRole('button', { name: 'Search this book' }));
    const panel = await screen.findByTestId('reader-search');
    fireEvent.change(within(panel).getByLabelText('Search this book'), {
      target: { value: 'Closing' },
    });
    fireEvent.submit(within(panel).getByLabelText('Search this book').closest('form')!);

    const hits = await within(panel).findByTestId('reader-content-hits');
    expect(within(hits).getByText('Closing thoughts on everything.')).toBeInTheDocument();
    expect(within(hits).getByText('no location for this one')).toBeInTheDocument();

    // block-3 is the "Closing thoughts." paragraph (see MARKDOWN_BODY);
    // goTo flashes it — scrollIntoView itself is a jsdom no-op, so the
    // flash class is what proves the jump actually targeted it.
    fireEvent.click(within(hits).getByText('Closing thoughts on everything.'));
    const target = screen.getByText('Closing thoughts.');
    expect(target).toHaveClass('reader-text-highlight');
  });

  it('saves a reader preference change', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'PUT' && url.includes('/api/v1/settings/reader')
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

  it('follows the app between light and dark when set to match it', async () => {
    document.documentElement.dataset.theme = 'dark';
    try {
      setup([
        (url) =>
          url.includes('/api/v1/settings/reader')
            ? json({ preferences: { ...DEFAULT_PREFS, theme: 'auto' } })
            : undefined,
      ]);
      await screen.findByText('The Great Essay');
      const shell = document.querySelector('.reader-shell')!;
      expect(shell).toHaveAttribute('data-theme', 'dark');

      // Switching the app's theme while reading switches the book too.
      document.documentElement.dataset.theme = 'light';
      await waitFor(() => expect(shell).toHaveAttribute('data-theme', 'light'));

      fireEvent.click(screen.getByRole('button', { name: 'Reading settings' }));
      const panel = await screen.findByTestId('reader-settings');
      expect(within(panel).getByRole('radio', { name: 'Match app' })).toBeChecked();
    } finally {
      delete document.documentElement.dataset.theme;
    }
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
    fireEvent.click(within(panel).getByRole('button', { name: /Bookmark this page/ }));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/files/f1/bookmarks')).toBe(true);
    });
    expect(await within(panel).findByRole('button', { name: 'Remove bookmark' })).toBeInTheDocument();
  });

  const savedHighlight = {
    id: 'hl1',
    file_id: 'f1',
    location: 'block-2',
    text: 'First section text.',
    color: 'yellow',
    note: '',
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
  };

  /** Selects the paragraph the way a reader would, and returns the bar. */
  async function selectParagraph() {
    const paragraph = await screen.findByText('First section text.');
    const range = document.createRange();
    range.selectNodeContents(paragraph);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    document.dispatchEvent(new Event('selectionchange'));
    const preview = await screen.findByText('“First section text.”');
    return { paragraph, bar: preview.closest<HTMLElement>('.reader-selection-bar')! };
  }

  function postBody(fetchMock: ReturnType<typeof setup>, path: string) {
    const call = fetchMock.mock.calls.find(
      ([url, init]) => String(url).endsWith(path) && init?.method === 'POST',
    );
    return call ? JSON.parse(String(call[1]?.body)) : undefined;
  }

  it('highlights a selection in one click and confirms it', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/files/f1/highlights')
          ? json({ highlight: { ...savedHighlight, color: 'green' } })
          : undefined,
    ]);
    const { bar } = await selectParagraph();

    fireEvent.click(within(bar).getByRole('button', { name: 'Highlight green' }));

    expect(await screen.findByTestId('reader-flash')).toHaveTextContent('Highlighted!');
    expect(postBody(fetchMock, '/api/v1/libraries/lib1/files/f1/highlights')).toMatchObject({
      location: 'block-1',
      text: 'First section text.',
      color: 'green',
    });
  });

  it('keeps the selection for a note even after typing clears it', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/files/f1/highlights')
          ? json({ highlight: { ...savedHighlight, note: 'remember this' } })
          : undefined,
    ]);
    const { bar } = await selectParagraph();
    fireEvent.click(within(bar).getByRole('button', { name: /Note/ }));

    // Focusing the note field collapses the selection in the book.
    window.getSelection()?.removeAllRanges();
    document.dispatchEvent(new Event('selectionchange'));

    fireEvent.change(await screen.findByPlaceholderText('Add a note (optional)'), {
      target: { value: 'remember this' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(postBody(fetchMock, '/api/v1/libraries/lib1/files/f1/highlights')).toMatchObject({
        text: 'First section text.',
        note: 'remember this',
      });
    });
  });

  it('saves a quote from the right-click menu and lists it under Quotes', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/files/f1/quotes')
          ? json({
              quote: {
                id: 'q1',
                file_id: 'f1',
                text: 'First section text.',
                location: 'block-2',
                favorite: false,
                created_at: '2026-10-01T00:00:00Z',
                updated_at: '2026-10-01T00:00:00Z',
              },
            })
          : undefined,
    ]);
    const { paragraph } = await selectParagraph();

    fireEvent.contextMenu(paragraph);
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Save quote' }));

    expect(await screen.findByTestId('reader-flash')).toHaveTextContent('Quoted!');
    expect(postBody(fetchMock, '/api/v1/libraries/lib1/files/f1/quotes')).toMatchObject({
      text: 'First section text.',
      location: 'block-1',
    });

    fireEvent.click(screen.getByRole('button', { name: 'Quotes from this book' }));
    expect(
      within(screen.getByTestId('reader-quotes')).getByText(/First section text\./),
    ).toBeInTheDocument();
  });

  it('leaves the browser menu alone when nothing is selected', async () => {
    setup();
    const paragraph = await screen.findByText('First section text.');
    const event = fireEvent.contextMenu(paragraph);
    expect(event).toBe(true); // not default-prevented
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });

  it('confirms a new bookmark with a quick flash', async () => {
    setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/files/f1/bookmarks')
          ? json({
              bookmark: {
                id: 'b1',
                file_id: 'f1',
                location: '0.0000',
                created_at: '2026-10-01T00:00:00Z',
              },
            })
          : undefined,
    ]);
    await screen.findByText('First section text.');
    fireEvent.click(screen.getByRole('button', { name: 'Bookmarks and highlights' }));
    fireEvent.click(await screen.findByRole('button', { name: /Bookmark this page/ }));

    expect(await screen.findByTestId('reader-flash')).toHaveTextContent('Bookmarked!');
  });

  it('lists marks as cards that say where they are, and renames a bookmark', async () => {
    const fetchMock = setup([
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/files/f1/bookmarks')
          ? json({
              bookmarks: [
                { id: 'b1', file_id: 'f1', location: 'block-1', created_at: '2026-10-01T00:00:00Z' },
              ],
            })
          : undefined,
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/files/f1/highlights')
          ? json({ highlights: [{ ...savedHighlight, location: 'block-3', note: 'a note' }] })
          : undefined,
      (url, init) =>
        init?.method === 'PATCH' && url.endsWith('/api/v1/libraries/lib1/bookmarks/b1')
          ? json({
              bookmark: {
                id: 'b1',
                file_id: 'f1',
                location: 'block-1',
                label: 'Start here',
                created_at: '2026-10-01T00:00:00Z',
              },
            })
          : undefined,
    ]);
    await screen.findByText('First section text.');
    fireEvent.click(screen.getByRole('button', { name: 'Bookmarks and highlights' }));
    const panel = await screen.findByTestId('reader-annotations');

    // Named after the chapter it falls in until it has a name of its own.
    const bookmarks = within(panel).getByRole('list', { name: 'Bookmarks' });
    expect(await within(bookmarks).findByText('Introduction')).toBeInTheDocument();

    fireEvent.click(within(panel).getByRole('button', { name: 'Rename bookmark' }));
    const input = within(panel).getByLabelText('Bookmark name');
    fireEvent.change(input, { target: { value: 'Start here' } });
    fireEvent.submit(input.closest('form')!);
    expect(await within(panel).findByText('Start here')).toBeInTheDocument();
    expect(called(fetchMock, 'PATCH', '/api/v1/libraries/lib1/bookmarks/b1')).toBe(true);

    fireEvent.click(within(panel).getByRole('tab', { name: /Highlights/ }));
    const highlights = within(panel).getByRole('list', { name: 'Highlights' });
    expect(within(highlights).getByText('First section text.')).toBeInTheDocument();
    expect(within(highlights).getByText('a note')).toBeInTheDocument();
    expect(within(highlights).getByText('Conclusion')).toBeInTheDocument();
  });
});

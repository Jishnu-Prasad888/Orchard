import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  apiError,
  called,
  json,
  libraryFixture,
  mockApi,
  noContent,
  originalFetch,
  renderPage,
} from '../test/harness';
import type { RouteHandler } from '../test/harness';
import NotesPage from './NotesPage';

const note = {
  id: 'n1',
  title: 'Reading plan',
  body: 'Finish **Dune** by Friday.',
  created_at: '2026-10-01T00:00:00Z',
  updated_at: '2026-10-01T00:00:00Z',
};

const tag = { id: 't1', name: 'books', color: '', created_at: '2026-01-01T00:00:00Z' };

function apiRules(overrides: RouteHandler[] = []): RouteHandler[] {
  return [
    ...overrides,
    (url) => (url.includes('/api/v1/libraries/lib1/notes/n1/tags') ? json({ tags: [] }) : undefined),
    (url) => (url.includes('/api/v1/libraries/lib1/notes/n1/links') ? json({ notes: [] }) : undefined),
    (url) => (url.includes('/api/v1/libraries/lib1/tags') ? json({ tags: [tag] }) : undefined),
    (url) => (url.includes('/api/v1/libraries/lib1/notes') ? json({ notes: [note] }) : undefined),
  ];
}

function setup(overrides: RouteHandler[] = [], options = {}) {
  const fn = mockApi(apiRules(overrides), options);
  renderPage(<NotesPage />, options);
  return fn;
}

describe('NotesPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('lists notes with a title and excerpt', async () => {
    setup();

    const list = await screen.findByTestId('notes-list');
    expect(within(list).getByText('Reading plan')).toBeInTheDocument();
    expect(within(list).getByText(/Finish Dune by Friday/)).toBeInTheDocument();
  });

  it('shows an empty state when there are no notes', async () => {
    setup([
      (url) => (url.endsWith('/api/v1/libraries/lib1/notes') ? json({ notes: [] }) : undefined),
    ]);

    expect(await screen.findByTestId('notes-empty')).toBeInTheDocument();
  });

  it('opens a note into the detail editor', async () => {
    setup();

    await screen.findByTestId('notes-list');
    fireEvent.click(screen.getByTestId('open-note-n1'));

    const detail = await screen.findByTestId('note-detail');
    expect(within(detail).getByDisplayValue('Reading plan')).toBeInTheDocument();
    expect(within(detail).getByLabelText('Note body (Markdown)')).toHaveValue(note.body);
  });

  it('autosaves edits to the title and body', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'PATCH' && url.includes('/api/v1/libraries/lib1/notes/n1')
          ? json({ note: { ...note, title: 'Updated plan', body: note.body } })
          : undefined,
    ]);

    await screen.findByTestId('notes-list');
    fireEvent.click(screen.getByTestId('open-note-n1'));
    const detail = await screen.findByTestId('note-detail');

    fireEvent.change(within(detail).getByLabelText('Note title'), {
      target: { value: 'Updated plan' },
    });

    await waitFor(
      () => {
        expect(called(fetchMock, 'PATCH', '/api/v1/libraries/lib1/notes/n1')).toBe(true);
      },
      { timeout: 2000 },
    );
  });

  it('creates a new note and opens it', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/notes')
          ? json({ note: { id: 'n2', title: '', body: '', created_at: note.created_at, updated_at: note.updated_at } })
          : undefined,
    ]);

    await screen.findByTestId('notes-list');
    fireEvent.click(screen.getByTestId('new-note-button'));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/notes')).toBe(true);
    });
    expect(await screen.findByTestId('note-detail')).toBeInTheDocument();
  });

  it('deletes a note through a confirm dialog, not window.confirm', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'DELETE' && url.endsWith('/api/v1/libraries/lib1/notes/n1')
          ? noContent()
          : undefined,
    ]);

    await screen.findByTestId('notes-list');
    fireEvent.click(screen.getByTestId('open-note-n1'));
    const detail = await screen.findByTestId('note-detail');
    fireEvent.click(within(detail).getByRole('button', { name: 'Delete note' }));

    const dialog = await screen.findByTestId('delete-note-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete note' }));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/notes/n1')).toBe(true);
    });
    expect(await screen.findByTestId('notes-list')).toBeInTheDocument();
  });

  it('filters by tag', async () => {
    const fetchMock = setup();

    await screen.findByTestId('notes-list');
    fireEvent.click(screen.getByRole('button', { name: 'books' }));

    await waitFor(() => {
      expect(called(fetchMock, 'GET', 'tag_id=t1')).toBe(true);
    });
  });

  it('filters to standalone notes', async () => {
    const fetchMock = setup();

    await screen.findByTestId('notes-list');
    fireEvent.click(screen.getByRole('button', { name: 'Standalone' }));

    await waitFor(() => {
      expect(called(fetchMock, 'GET', 'standalone=1')).toBe(true);
    });
  });

  it('retries a failed listing', async () => {
    let attempts = 0;
    mockApi(
      apiRules([
        (url) => {
          if (!url.endsWith('/api/v1/libraries/lib1/notes')) return undefined;
          attempts += 1;
          return attempts === 1
            ? apiError(500, 'INTERNAL', 'Notes are unavailable right now.')
            : json({ notes: [note] });
        },
      ]),
    );
    renderPage(<NotesPage />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Notes are unavailable right now.');
    fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));

    expect(await screen.findByTestId('notes-list')).toBeInTheDocument();
    expect(attempts).toBeGreaterThanOrEqual(2);
  });

  it('warns when the library is offline', async () => {
    setup([], { libraries: [libraryFixture({ status: 'offline' })] });

    expect(await screen.findByTestId('library-offline')).toBeInTheDocument();
  });

  it('tells a user with no libraries how to get one', async () => {
    mockApi([], { libraries: [] });
    renderPage(<NotesPage />);

    expect(await screen.findByTestId('no-libraries')).toBeInTheDocument();
  });
});

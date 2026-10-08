import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { fileFixture, json, mockApi, originalFetch, renderPage } from '../test/harness';
import DuplicatesPage from './DuplicatesPage';

const group = {
  content_hash: 'sha256-abcdef1234567890',
  size_bytes: 2048,
  files: [
    fileFixture({ content_hash: 'sha256-abcdef1234567890' }),
    fileFixture({
      id: 'f2',
      rel_path: '2024/IMG_0001_copy.png',
      name: 'IMG_0001_copy.png',
      folder_path: '2024',
      mod_time: '2026-09-02T00:00:00Z',
      content_hash: 'sha256-abcdef1234567890',
    }),
  ],
};

const duplicatesResponse = { groups: [group], next_cursor: '', total: 1 };

function setup(overrides: { duplicates?: unknown } = {}) {
  const payload = overrides.duplicates ?? duplicatesResponse;
  const fn = mockApi([(url) => (url.includes('/files/duplicates') ? json(payload) : undefined)]);
  renderPage(<DuplicatesPage />);
  return fn;
}

describe('DuplicatesPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('shows a heading and the library selector', async () => {
    setup();

    expect(await screen.findByRole('heading', { name: 'Duplicates' })).toBeInTheDocument();
    expect(await screen.findByTestId('library-picker')).toHaveValue('lib1');
  });

  it('renders duplicate groups with member files and sizes', async () => {
    setup();

    expect(await screen.findByText('IMG_0001.png')).toBeInTheDocument();
    expect(screen.getByText('IMG_0001_copy.png')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /2 identical files/ })).toBeInTheDocument();
    expect(screen.getByTestId('duplicates-summary')).toHaveTextContent('1 duplicate group');
  });

  it('separates the copy to keep from the removable ones', async () => {
    setup();

    await screen.findByText('IMG_0001.png');
    // The first copy is the one Cairn suggests keeping, so it is not listed as
    // a removal candidate.
    expect(
      screen.getByText(
        'Removable copies — pick one to open it, then delete or move it in the viewer.',
      ),
    ).toBeInTheDocument();
    const keep = document.querySelector('.dup-members:not(.dup-members-removable)') as HTMLElement;
    expect(within(keep).getByText('IMG_0001.png')).toBeInTheDocument();
    expect(within(keep).queryByText('IMG_0001_copy.png')).not.toBeInTheDocument();
  });

  it('links every member back to its download endpoint', async () => {
    setup();

    await screen.findByText('IMG_0001.png');
    const links = screen.getAllByRole('link', { name: 'Download' });
    expect(links).toHaveLength(2);
    expect(links[0]).toHaveAttribute('href', '/api/v1/libraries/lib1/files/f1/download');
  });

  it('shows an empty state when no duplicates exist', async () => {
    setup({ duplicates: { groups: [], next_cursor: '', total: 0 } });

    expect(await screen.findByTestId('duplicates-empty')).toBeInTheDocument();
    expect(screen.getByText('No duplicates')).toBeInTheDocument();
  });

  it('offers a rescan that re-requests the listing', async () => {
    const fetchMock = setup();

    await screen.findByText('IMG_0001.png');
    const before = fetchMock.mock.calls.filter(([url]) =>
      String(url).includes('/files/duplicates'),
    ).length;

    fireEvent.click(screen.getByRole('button', { name: 'Rescan' }));

    await waitFor(() => {
      const after = fetchMock.mock.calls.filter(([url]) =>
        String(url).includes('/files/duplicates'),
      ).length;
      expect(after).toBeGreaterThan(before);
    });
  });

  it('asks for more groups only when the scan reports more than it returned', async () => {
    // The scan says there are 2 groups but the first page returned 1, so there
    // is more to load.
    const fn = mockApi([
      (url) =>
        url.includes('/files/duplicates') && url.includes('limit=100')
          ? json({
              groups: [group, { ...group, content_hash: 'sha256-second' }],
              next_cursor: '',
              total: 2,
            })
          : undefined,
      (url) =>
        url.includes('/files/duplicates')
          ? json({ groups: [group], next_cursor: 'c1', total: 2 })
          : undefined,
    ]);
    const view = renderPage(<DuplicatesPage />);

    const more = await within(view.container).findByRole('button', { name: 'Show more groups' });
    fireEvent.click(more);

    await waitFor(() => {
      expect(fn.mock.calls.some(([url]) => String(url).includes('limit=100'))).toBe(true);
    });
    await waitFor(() => {
      expect(view.container.querySelectorAll('.dup-group')).toHaveLength(2);
    });
  });

  it('explains when there are no libraries yet', async () => {
    mockApi([], { libraries: [] });
    renderPage(<DuplicatesPage />, { libraries: [] });

    expect(await screen.findByTestId('no-libraries')).toBeInTheDocument();
    expect(screen.queryByTestId('duplicates-summary')).not.toBeInTheDocument();
  });

  it('explains a failed scan instead of claiming there are none', async () => {
    mockApi([
      (url) =>
        url.includes('/files/duplicates')
          ? json({ error: { code: 'INTERNAL', message: 'Index is locked.', request_id: '1' } }, 500)
          : undefined,
    ]);
    renderPage(<DuplicatesPage />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Index is locked.');
    expect(screen.queryByTestId('duplicates-empty')).not.toBeInTheDocument();
  });

  it('warns when the library storage is offline', async () => {
    mockApi([(url) => (url.includes('/files/duplicates') ? json(duplicatesResponse) : undefined)], {
      libraries: [
        {
          id: 'lib1',
          name: 'Photos',
          root: '/srv/photos',
          status: 'offline',
          schema_version: 1,
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-02T00:00:00Z',
        },
      ],
    });
    renderPage(<DuplicatesPage />, {
      libraries: [
        {
          id: 'lib1',
          name: 'Photos',
          root: '/srv/photos',
          status: 'offline',
          schema_version: 1,
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-02T00:00:00Z',
        },
      ],
    });

    expect(await screen.findByTestId('library-offline')).toBeInTheDocument();
  });
});

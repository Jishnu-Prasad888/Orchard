import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
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
import DuplicatesPage from './DuplicatesPage';

const copyA = fileFixture({ id: 'f1', rel_path: 'a.epub', name: 'a.epub', content_hash: 'same' });
const copyB = fileFixture({
  id: 'f2',
  rel_path: 'copies/a-copy.epub',
  name: 'a-copy.epub',
  folder_path: 'copies',
  content_hash: 'same',
});

const oneGroup = { groups: [{ content_hash: 'same', files: [copyA, copyB] }] };

function setup(overrides: RouteHandler[] = [], options = {}) {
  const fn = mockApi(
    [
      ...overrides,
      (url) =>
        url.includes('/api/v1/libraries/lib1/files/duplicates') ? json(oneGroup) : undefined,
    ],
    options,
  );
  renderPage(<DuplicatesPage />, options);
  return fn;
}

describe('DuplicatesPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('lists duplicate groups with their file paths', async () => {
    setup();

    const groups = await screen.findByTestId('duplicate-groups');
    expect(within(groups).getByText('a.epub')).toBeInTheDocument();
    expect(within(groups).getByText('copies/a-copy.epub')).toBeInTheDocument();
    expect(within(groups).getByText(/2 copies/)).toBeInTheDocument();
  });

  it('says there are no duplicates when the library has none', async () => {
    setup([
      (url) =>
        url.includes('/api/v1/libraries/lib1/files/duplicates') ? json({ groups: [] }) : undefined,
    ]);

    expect(await screen.findByTestId('duplicates-empty')).toBeInTheDocument();
  });

  it('moves a duplicate to trash through the shared confirmation dialog', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'DELETE' && url.endsWith('/api/v1/libraries/lib1/files/f2')
          ? noContent()
          : undefined,
    ]);

    await screen.findByTestId('duplicate-groups');
    fireEvent.click(screen.getByTestId('duplicate-trash-f2'));

    const dialog = await screen.findByTestId('trash-dialog');
    expect(within(dialog).getByText(/moved to the trash/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Move to trash' }));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/files/f2')).toBe(true);
    });
    expect(bodyOf(fetchMock, 'DELETE', '/api/v1/libraries/lib1/files/f2')).toEqual({
      path: 'copies/a-copy.epub',
    });
  });

  it('opens a duplicate file in the viewer', async () => {
    setup();

    await screen.findByTestId('duplicate-groups');
    fireEvent.click(screen.getByText('a.epub'));

    expect(await screen.findByTestId('viewer')).toBeInTheDocument();
  });

  it('warns when the library is offline', async () => {
    setup([], { libraries: [libraryFixture({ status: 'offline' })] });

    expect(await screen.findByTestId('library-offline')).toBeInTheDocument();
  });

  it('tells a user with no libraries how to get one', async () => {
    mockApi([], { libraries: [] });
    renderPage(<DuplicatesPage />, { libraries: [] });

    expect(await screen.findByTestId('no-libraries')).toBeInTheDocument();
  });
});

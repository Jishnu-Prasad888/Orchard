import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  bodyOf,
  called,
  fileFixture,
  json,
  mockApi,
  noContent,
  originalFetch,
  renderPage,
} from '../test/harness';
import CollectionsPage from './CollectionsPage';

const collectionsList = {
  collections: [
    {
      id: 'c1',
      name: 'Summer reading',
      description: '',
      pinned: false,
      item_count: 1,
      created_at: '2026-01-02T00:00:00Z',
      updated_at: '2026-01-02T00:00:00Z',
    },
  ],
};

const collectionFiles = { files: [fileFixture()] };

function setup(
  overrides: {
    collections?: typeof collectionsList;
    collectionFiles?: typeof collectionFiles;
  } = {},
) {
  const collectionsResp = overrides.collections ?? collectionsList;
  const filesResp = overrides.collectionFiles ?? collectionFiles;
  const fn = mockApi([
    (url, init) =>
      url.endsWith('/api/v1/libraries/lib1/collections/c1/files/f1') && init?.method === 'DELETE'
        ? noContent()
        : undefined, // remove file from collection
    (url, init) =>
      url.endsWith('/api/v1/libraries/lib1/collections/c1') && init?.method === 'DELETE'
        ? noContent()
        : undefined, // delete collection
    (url, init) =>
      url.endsWith('/api/v1/libraries/lib1/collections/c1') && init?.method === 'PATCH'
        ? json({
            collection: { ...collectionsResp.collections[0]!, name: 'Winter reading' },
          })
        : undefined, // rename collection
    (url, init) =>
      url.endsWith('/api/v1/libraries/lib1/collections') && init?.method === 'POST'
        ? json(
            {
              collection: {
                id: 'c9',
                name: 'Trip',
                description: '',
                pinned: false,
                item_count: 0,
                created_at: '2026-01-02T00:00:00Z',
                updated_at: '2026-01-02T00:00:00Z',
              },
            },
            201,
          )
        : undefined,
    (url) =>
      url.endsWith('/api/v1/libraries/lib1/collections/c1/files') ? json(filesResp) : undefined,
    (url) =>
      url.endsWith('/api/v1/libraries/lib1/collections') ? json(collectionsResp) : undefined,
    (url) => (url.includes('/favorites') ? json({ files: [] }) : undefined),
    (url) =>
      url.includes('/files/f1/metadata')
        ? json({
            metadata: {
              file_id: 'f1',
              media_type: 'photo',
              mime_type: 'image/jpeg',
              width: 4032,
              height: 3024,
              camera_make: 'Apple',
              camera_model: 'iPhone 15',
              taken_at: '2026-06-01T12:00:00Z',
              has_thumbnail: true,
            },
          })
        : undefined,
  ]);
  renderPage(<CollectionsPage />);
  return fn;
}

describe('CollectionsPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('renders the heading and collection cards', async () => {
    setup();

    expect(await screen.findByRole('heading', { name: 'Collections' })).toBeInTheDocument();
    expect(await screen.findByTestId('collections-grid')).toBeInTheDocument();
    expect(screen.getByText('Summer reading')).toBeInTheDocument();
    expect(screen.getByText('1 item')).toBeInTheDocument();
  });

  it('shows an empty state when there are no collections', async () => {
    setup({ collections: { collections: [] } });

    expect(await screen.findByTestId('collections-empty')).toBeInTheDocument();
  });

  it('prompts for a name in a dialog instead of window.prompt', async () => {
    const fetchMock = setup();
    const promptSpy = vi.spyOn(window, 'prompt');

    await screen.findByTestId('collections-grid');

    fireEvent.click(screen.getByRole('button', { name: 'New collection' }));
    const dialog = await screen.findByTestId('new-collection-dialog');
    expect(dialog).toHaveAttribute('role', 'dialog');

    fireEvent.change(screen.getByLabelText('Collection name'), { target: { value: 'Trip' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/collections')).toBe(true);
    });
    expect(bodyOf(fetchMock, 'POST', '/api/v1/libraries/lib1/collections')).toEqual({
      name: 'Trip',
      description: undefined,
    });
    expect(promptSpy).not.toHaveBeenCalled();
  });

  it('keeps a failed create inside the dialog instead of closing it', async () => {
    mockApi([
      (url, init) =>
        url.endsWith('/api/v1/libraries/lib1/collections') && init?.method === 'POST'
          ? json(
              {
                error: {
                  code: 'CONFLICT',
                  message: 'A collection with that name already exists.',
                  request_id: '1',
                },
              },
              409,
            )
          : undefined,
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/collections') ? json(collectionsList) : undefined,
    ]);
    renderPage(<CollectionsPage />);

    await screen.findByTestId('collections-grid');
    fireEvent.click(screen.getByRole('button', { name: 'New collection' }));
    const dialog = await screen.findByTestId('new-collection-dialog');
    fireEvent.change(screen.getByLabelText('Collection name'), { target: { value: 'Trip' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'A collection with that name already exists.',
    );
    expect(screen.getByTestId('new-collection-dialog')).toBeInTheDocument();
  });

  it('opens a collection and lists its files', async () => {
    setup();

    await screen.findByTestId('collections-grid');
    fireEvent.click(screen.getByTestId('open-collection-Summer reading'));

    expect(await screen.findByTestId('collection-detail')).toBeInTheDocument();
    expect(screen.getByTestId('file-grid')).toBeInTheDocument();
    expect(screen.getByText('IMG_0001.png')).toBeInTheDocument();
  });

  it('shows an empty state for a collection nothing is in yet', async () => {
    setup({ collectionFiles: { files: [] } });

    await screen.findByTestId('collections-grid');
    fireEvent.click(screen.getByTestId('open-collection-Summer reading'));

    expect(await screen.findByTestId('collection-files-empty')).toBeInTheDocument();
  });

  it('removes a file from the collection it is shown under', async () => {
    const fetchMock = setup();

    await screen.findByTestId('collections-grid');
    fireEvent.click(screen.getByTestId('open-collection-Summer reading'));
    await screen.findByTestId('file-grid');

    fireEvent.click(screen.getByRole('button', { name: 'Select IMG_0001.png' }));
    fireEvent.click(await screen.findByTestId('selection-remove-from-collection'));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/collections/c1/files/f1')).toBe(
        true,
      );
    });
  });

  it('renames a collection', async () => {
    const fetchMock = setup();

    await screen.findByTestId('collections-grid');
    fireEvent.click(screen.getByTestId('open-collection-Summer reading'));
    await screen.findByTestId('collection-detail');

    fireEvent.click(screen.getByRole('button', { name: 'Rename' }));
    const dialog = await screen.findByTestId('rename-collection-dialog');
    fireEvent.change(screen.getByLabelText('Collection name'), {
      target: { value: 'Winter reading' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(called(fetchMock, 'PATCH', '/api/v1/libraries/lib1/collections/c1')).toBe(true);
    });
    expect(await screen.findByRole('heading', { name: 'Winter reading' })).toBeInTheDocument();
  });

  it('deletes a collection after confirming the dialog', async () => {
    const fetchMock = setup();
    const confirmSpy = vi.spyOn(window, 'confirm');

    await screen.findByTestId('collections-grid');
    fireEvent.click(screen.getByTestId('open-collection-Summer reading'));
    await screen.findByTestId('collection-detail');

    fireEvent.click(screen.getByRole('button', { name: 'Delete collection' }));
    const dialog = await screen.findByTestId('delete-collection-dialog');
    expect(dialog).toHaveAttribute('role', 'dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete collection' }));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/collections/c1')).toBe(true);
    });
    // Back on the collection list after deletion.
    expect(await screen.findByTestId('collections-grid')).toBeInTheDocument();
    expect(confirmSpy).not.toHaveBeenCalled();
  });

  it('does nothing when the delete dialog is cancelled', async () => {
    const fetchMock = setup();

    await screen.findByTestId('collections-grid');
    fireEvent.click(screen.getByTestId('open-collection-Summer reading'));
    await screen.findByTestId('collection-detail');

    fireEvent.click(screen.getByRole('button', { name: 'Delete collection' }));
    const dialog = await screen.findByTestId('delete-collection-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    await waitFor(() => {
      expect(screen.queryByTestId('delete-collection-dialog')).not.toBeInTheDocument();
    });
    expect(called(fetchMock, 'DELETE', '/collections/c1')).toBe(false);
    expect(screen.getByTestId('collection-detail')).toBeInTheDocument();
  });

  it('pins a collection from the list', async () => {
    const fetchMock = setup();

    await screen.findByTestId('collections-grid');
    fireEvent.click(screen.getByTestId('pin-collection-Summer reading'));

    await waitFor(() => {
      expect(called(fetchMock, 'PATCH', '/api/v1/libraries/lib1/collections/c1')).toBe(true);
    });
    expect(bodyOf(fetchMock, 'PATCH', '/api/v1/libraries/lib1/collections/c1')).toEqual({
      pinned: true,
    });
  });
});

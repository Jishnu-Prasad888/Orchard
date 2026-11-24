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
  libraryFixture,
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
    route?: string;
    hiddenLibraries?: string[];
  } = {},
) {
  const collectionsResp = overrides.collections ?? collectionsList;
  const filesResp = overrides.collectionFiles ?? collectionFiles;
  const fn = mockApi([
    (url, init) =>
      url.endsWith('/api/v1/settings/hidden-collection-libraries')
        ? json({
            library_ids:
              init?.method === 'PUT'
                ? (JSON.parse(String(init.body)) as { library_ids: string[] }).library_ids
                : (overrides.hiddenLibraries ?? []),
          })
        : undefined,
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
  renderPage(<CollectionsPage />, overrides.route ? { route: overrides.route } : {});
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

  it('renames a collection from its card menu without opening it', async () => {
    const fn = setup();
    fireEvent.click(await screen.findByTestId('more-collection-Summer reading'));
    fireEvent.click(await screen.findByTestId('menu-rename-collection'));

    const dialog = await screen.findByTestId('rename-collection-dialog');
    fireEvent.change(within(dialog).getByLabelText('Collection name'), {
      target: { value: 'Winter reading' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(bodyOf(fn, 'PATCH', '/collections/c1')).toEqual({ name: 'Winter reading' }),
    );
    expect(screen.queryByTestId('collection-detail')).not.toBeInTheDocument();
  });

  it('deletes a collection from its right-click menu', async () => {
    const fn = setup();
    fireEvent.contextMenu(await screen.findByTestId('open-collection-Summer reading'));
    fireEvent.click(await screen.findByTestId('menu-delete-collection'));

    const dialog = await screen.findByTestId('delete-collection-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete collection' }));

    await waitFor(() =>
      expect(called(fn, 'DELETE', '/api/v1/libraries/lib1/collections/c1')).toBe(true),
    );
  });

  it('removes one file from the collection with its right-click menu', async () => {
    const fn = setup();
    fireEvent.click(await screen.findByTestId('open-collection-Summer reading'));
    fireEvent.contextMenu(await screen.findByRole('button', { name: 'IMG_0001.png' }));
    fireEvent.click(await screen.findByTestId('menu-remove-from-collection'));

    await waitFor(() =>
      expect(called(fn, 'DELETE', '/api/v1/libraries/lib1/collections/c1/files/f1')).toBe(true),
    );
  });

  it('shares a collection from its card menu', async () => {
    const fn = mockApi(
      [
        (url, init) =>
          init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/shares')
            ? json({ share: { id: 'sh1' }, token: 'tok1' }, 201)
            : undefined,
        (url) =>
          url.endsWith('/api/v1/libraries/lib1/collections') ? json(collectionsList) : undefined,
      ],
      { libraries: [libraryFixture({ can_share: true })] },
    );
    renderPage(<CollectionsPage />, { libraries: [libraryFixture({ can_share: true })] });

    fireEvent.click(await screen.findByTestId('more-collection-Summer reading'));
    fireEvent.click(await screen.findByTestId('menu-share-collection'));
    const dialog = await screen.findByTestId('share-dialog');
    fireEvent.click(within(dialog).getByTestId('confirm-new-share'));

    await waitFor(() =>
      expect(bodyOf(fn, 'POST', '/api/v1/libraries/lib1/shares')).toEqual({
        target: { kind: 'collection', id: 'c1' },
        caps: ['read'],
      }),
    );
  });

  it('pins only the collection clicked, and puts the latest pin first', async () => {
    const pinned = (id: string, name: string, at: string) => ({
      ...collectionsList.collections[0]!,
      id,
      name,
      pinned: true,
      pinned_at: at,
    });
    const fetchMock = setup({
      collections: {
        collections: [
          pinned('c2', 'Bedtime', '2026-03-02T00:00:00Z'),
          pinned('c3', 'Anthologies', '2026-03-01T00:00:00Z'),
          { ...collectionsList.collections[0]!, id: 'c4', name: 'Atlas' },
          { ...collectionsList.collections[0]! },
        ],
      } as typeof collectionsList,
    });

    await screen.findByTestId('collections-grid');
    const names = () =>
      screen.getAllByTestId(/^collection-card-/).map((c) => c.dataset.testid!.slice(16));
    // Pinned first, newest pin first; then the rest by name.
    expect(names()).toEqual(['Bedtime', 'Anthologies', 'Atlas', 'Summer reading']);

    const pin = screen.getByTestId('pin-collection-Summer reading');
    fireEvent.click(pin);
    // At once: it goes to the front, and nothing else changes.
    expect(pin).toHaveAttribute('aria-pressed', 'true');
    expect(names()).toEqual(['Summer reading', 'Bedtime', 'Anthologies', 'Atlas']);
    await waitFor(() =>
      expect(bodyOf(fetchMock, 'PATCH', '/collections/c1')).toEqual({ pinned: true }),
    );
    expect(called(fetchMock, 'PATCH', '/collections/c2')).toBe(false);

    // Unpinned, it goes back among the others by name.
    fireEvent.click(screen.getByTestId('pin-collection-Bedtime'));
    expect(names()).toEqual(['Summer reading', 'Anthologies', 'Atlas', 'Bedtime']);
  });

  it('puts books on the reading timeline and saves the order', async () => {
    const fetchMock = setup({
      collectionFiles: {
        files: [
          fileFixture({ id: 'f1', name: 'One.epub', media_type: 'document', completed: true }),
          fileFixture({ id: 'f2', name: 'Two.epub', media_type: 'document' }),
          fileFixture({ id: 'f3', name: 'Three.epub', media_type: 'document' }),
        ],
        timeline: ['f1', 'f2'],
      } as unknown as typeof collectionFiles,
    });
    mockTimelinePut(fetchMock);

    fireEvent.click(await screen.findByTestId('open-collection-Summer reading'));
    fireEvent.click(await screen.findByTestId('collection-view-timeline'));

    const timeline = await screen.findByTestId('collection-timeline');
    const order = () =>
      within(timeline)
        .getAllByTestId('timeline-item')
        .map((li) => within(li).getByRole('button', { name: /Open$/ }).getAttribute('aria-label'));
    expect(order()).toEqual(['1. One, read. Open', '2. Two, up next. Open']);
    expect(within(timeline).getByText('1 of 2 read')).toBeInTheDocument();
    expect(within(timeline).getAllByTestId('timeline-waiting')).toHaveLength(1);

    // Three goes on the end, then moves up past Two.
    fireEvent.click(
      within(timeline).getByRole('button', { name: 'Add Three to the end of the timeline' }),
    );
    await waitFor(() =>
      expect(lastBody(fetchMock, 'PUT', '/collections/c1/timeline')).toEqual({
        file_ids: ['f1', 'f2', 'f3'],
      }),
    );
    fireEvent.click(within(timeline).getByRole('button', { name: 'Move Three earlier' }));
    await waitFor(() =>
      expect(lastBody(fetchMock, 'PUT', '/collections/c1/timeline')).toEqual({
        file_ids: ['f1', 'f3', 'f2'],
      }),
    );
    expect(order()[1]).toBe('2. Three, up next. Open');

    fireEvent.click(within(timeline).getByRole('button', { name: 'Take One off the timeline' }));
    await waitFor(() =>
      expect(lastBody(fetchMock, 'PUT', '/collections/c1/timeline')).toEqual({
        file_ids: ['f3', 'f2'],
      }),
    );
    expect(await within(timeline).findByText('Saved')).toBeInTheDocument();
  });

  it('sorts the Add books list newest first or by name', async () => {
    const fetchMock = setup();
    fireEvent.click(await screen.findByTestId('open-collection-Summer reading'));
    fireEvent.click(await screen.findByTestId('add-collection-files-button'));
    await screen.findByTestId('add-collection-files-dialog');

    await waitFor(() => expect(called(fetchMock, 'GET', 'sort=mod_time&order=desc')).toBe(true));
    fireEvent.click(screen.getByTestId('add-files-sort-name'));
    await waitFor(() => expect(called(fetchMock, 'GET', 'sort=name&order=asc')).toBe(true));
    expect(screen.getByTestId('add-files-sort-name')).toHaveAttribute('aria-checked', 'true');
  });

  it('colours a collection from its right-click menu, at once', async () => {
    const fetchMock = setup();
    const card = await screen.findByTestId('collection-card-Summer reading');
    fireEvent.contextMenu(card);
    fireEvent.click(await screen.findByTestId('color-swatch-mint'));

    expect(card).toHaveAttribute('data-collection-color', 'mint');
    await waitFor(() =>
      expect(bodyOf(fetchMock, 'PATCH', '/collections/c1')).toEqual({ color: 'mint' }),
    );
    // Menu closed once a colour is picked.
    expect(screen.queryByTestId('color-swatch-mint')).not.toBeInTheDocument();
  });

  it('shows only the collections of a colour picked under the header', async () => {
    setup({
      collections: {
        collections: [
          { ...collectionsList.collections[0]!, color: 'sage' },
          { ...collectionsList.collections[0]!, id: 'c2', name: 'Bedtime', color: 'coral' },
          { ...collectionsList.collections[0]!, id: 'c3', name: 'Plain' },
        ],
      } as typeof collectionsList,
    });

    await screen.findByTestId('collections-grid');
    expect(screen.getAllByTestId(/^collection-card-/)).toHaveLength(3);
    const sage = screen.getByTestId('color-filter-sage');
    expect(sage).toHaveAccessibleName('Sage: 1 collection');

    fireEvent.click(sage);
    expect(sage).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getAllByTestId(/^collection-card-/).map((c) => c.dataset.testid)).toEqual([
      'collection-card-Summer reading',
    ]);

    // The same circle again shows everything.
    fireEvent.click(sage);
    expect(screen.getAllByTestId(/^collection-card-/)).toHaveLength(3);
  });

  it('reorders the timeline by dragging a card by its handle', async () => {
    const fetchMock = setup({
      collectionFiles: {
        files: ['f1', 'f2', 'f3'].map((id, i) =>
          fileFixture({ id, name: `${['One', 'Two', 'Three'][i]}.epub`, media_type: 'document' }),
        ),
        timeline: ['f1', 'f2', 'f3'],
      } as unknown as typeof collectionFiles,
    });
    mockTimelinePut(fetchMock);
    fireEvent.click(await screen.findByTestId('open-collection-Summer reading'));
    fireEvent.click(await screen.findByTestId('collection-view-timeline'));
    const items = await screen.findAllByTestId('timeline-item');
    // jsdom has no layout: give each card a 60px box, 12px apart.
    items.forEach((li, i) => {
      li.getBoundingClientRect = () =>
        ({
          top: i * 72,
          bottom: i * 72 + 60,
          height: 60,
          left: 0,
          right: 300,
          width: 300,
        }) as DOMRect;
    });

    const grip = items[0]!.querySelector('.timeline-grip')!;
    fireEvent.pointerDown(grip, { button: 0, clientY: 30 });
    expect(items[0]).toHaveClass('is-lifted');
    // Past the middle of the third card.
    fireEvent.pointerMove(window, { clientY: 30 + 150 });
    expect(items[0]!.style.transform).toContain('translateY(150px)');
    expect(items[1]!.style.transform).toBe('translateY(-72px)');
    fireEvent.pointerUp(window);

    await waitFor(() =>
      expect(lastBody(fetchMock, 'PUT', '/collections/c1/timeline')).toEqual({
        file_ids: ['f2', 'f3', 'f1'],
      }),
    );
  });

  it('sorts by name or newest created, with pinned ones always first', async () => {
    localStorage.removeItem('orchard.collections.sort');
    const c = (id: string, name: string, created: string, extra = {}) => ({
      ...collectionsList.collections[0]!,
      id,
      name,
      created_at: created,
      ...extra,
    });
    setup({
      collections: {
        collections: [
          c('c2', 'Zen', '2026-05-01T00:00:00Z', {
            pinned: true,
            pinned_at: '2026-05-02T00:00:00Z',
          }),
          c('c3', 'Alpha', '2026-01-01T00:00:00Z'),
          c('c4', 'Middle', '2026-04-01T00:00:00Z'),
        ],
      } as typeof collectionsList,
    });
    await screen.findByTestId('collections-grid');
    const names = () =>
      screen.getAllByTestId(/^collection-card-/).map((el) => el.dataset.testid!.slice(16));
    expect(names()).toEqual(['Zen', 'Alpha', 'Middle']);

    fireEvent.click(screen.getByTestId('collection-sort-created'));
    expect(names()).toEqual(['Zen', 'Middle', 'Alpha']);
    // Remembered for next time.
    expect(localStorage.getItem('orchard.collections.sort')).toBe('created');
    localStorage.removeItem('orchard.collections.sort');
  });

  it('pins at most five', async () => {
    const many = Array.from({ length: 6 }, (_, i) => ({
      ...collectionsList.collections[0]!,
      id: `p${i}`,
      name: `Shelf ${i}`,
      pinned: i < 5,
      pinned_at: i < 5 ? `2026-01-0${i + 1}T00:00:00Z` : undefined,
    }));
    const fetchMock = setup({ collections: { collections: many } as typeof collectionsList });
    await screen.findByTestId('collections-grid');

    fireEvent.click(screen.getByTestId('pin-collection-Shelf 5'));
    expect(
      await screen.findByText('You can pin up to 5 collections. Unpin one first.'),
    ).toBeInTheDocument();
    expect(screen.getByTestId('pin-collection-Shelf 5')).toHaveAttribute('aria-pressed', 'false');
    expect(called(fetchMock, 'PATCH', '/collections/p5')).toBe(false);
  });

  it('hides collections from the dropdown, and a search shows them folded away', async () => {
    const fetchMock = setup({
      collections: {
        collections: [
          { ...collectionsList.collections[0]! },
          { ...collectionsList.collections[0]!, id: 'c2', name: 'Summer drafts', hidden: true },
        ],
      } as typeof collectionsList,
    });
    await screen.findByTestId('collections-grid');
    expect(screen.queryByTestId('collection-card-Summer drafts')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('hidden-collections-button'));
    const panel = await screen.findByTestId('hidden-collections-panel');
    expect(within(panel).getByTestId('hide-collection-Summer drafts')).toBeChecked();
    fireEvent.click(within(panel).getByTestId('hide-collection-Summer reading'));
    await waitFor(() =>
      expect(bodyOf(fetchMock, 'PATCH', '/collections/c1')).toEqual({ hidden: true }),
    );
    expect(screen.queryByTestId('collection-card-Summer reading')).not.toBeInTheDocument();
    expect(screen.getByText(/Every collection is hidden/)).toBeInTheDocument();
  });

  it('lists hidden collections a search matches in a section closed until opened', async () => {
    setup({
      route: '/collections?q=summer',
      collections: {
        collections: [
          { ...collectionsList.collections[0]! },
          { ...collectionsList.collections[0]!, id: 'c2', name: 'Summer drafts', hidden: true },
          { ...collectionsList.collections[0]!, id: 'c3', name: 'Winter', hidden: true },
        ],
      } as typeof collectionsList,
    });

    const hidden = await screen.findByTestId('collections-hidden-matches');
    expect(hidden).not.toHaveAttribute('open');
    expect(within(hidden).getByText('1 hidden collection matches')).toBeInTheDocument();
    fireEvent.click(within(hidden).getByText('1 hidden collection matches'));
    expect(hidden).toHaveAttribute('open');
    expect(within(hidden).getByTestId('collection-card-Summer drafts')).toBeInTheDocument();
    expect(screen.queryByTestId('collection-card-Winter')).not.toBeInTheDocument();
  });

  it('hides every collection of a library ticked under Hidden', async () => {
    const fetchMock = setup();
    await screen.findByTestId('collection-card-Summer reading');

    fireEvent.click(screen.getByTestId('hidden-collections-button'));
    const panel = await screen.findByTestId('hidden-collections-panel');
    fireEvent.click(within(panel).getByTestId('hide-library-Photos'));

    expect(screen.queryByTestId('collection-card-Summer reading')).not.toBeInTheDocument();
    expect(screen.getByText(/This library's collections are hidden/)).toBeInTheDocument();
    expect(within(panel).getByTestId('hide-collection-Summer reading')).toBeDisabled();
    await waitFor(() =>
      expect(bodyOf(fetchMock, 'PUT', '/settings/hidden-collection-libraries')).toEqual({
        library_ids: ['lib1'],
      }),
    );
  });

  it('still finds collections of a hidden library in a search, folded away', async () => {
    setup({ route: '/collections?q=summer', hiddenLibraries: ['lib1'] });
    const hidden = await screen.findByTestId('collections-hidden-matches');
    expect(hidden).not.toHaveAttribute('open');
    expect(within(hidden).getByTestId('collection-card-Summer reading')).toBeInTheDocument();
    expect(screen.queryByTestId('collections-grid')).not.toBeInTheDocument();
  });
});

/** Answers PUT …/timeline with the order it was sent. */
function mockTimelinePut(fetchMock: ReturnType<typeof vi.fn>) {
  const fallback = fetchMock.getMockImplementation() as (
    input: RequestInfo,
    init?: RequestInit,
  ) => Promise<Response>;
  fetchMock.mockImplementation((input: RequestInfo, init?: RequestInit) => {
    if (String(input).endsWith('/collections/c1/timeline') && init?.method === 'PUT') {
      const sent = JSON.parse(String(init.body)) as { file_ids: string[] };
      return Promise.resolve(
        new Response(JSON.stringify({ timeline: sent.file_ids }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      );
    }
    return fallback(input, init);
  });
}

/** The body of the last request matching method and url. */
function lastBody(fn: ReturnType<typeof vi.fn>, method: string, urlPart: string): unknown {
  const calls = fn.mock.calls.filter(([input, init]) => {
    const verb = (init as RequestInit | undefined)?.method ?? 'GET';
    return verb === method && String(input).includes(urlPart);
  });
  const body = (calls.at(-1)?.[1] as RequestInit | undefined)?.body;
  return body === undefined ? undefined : JSON.parse(String(body));
}

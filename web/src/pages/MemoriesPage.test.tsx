import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  apiError,
  bodyOf,
  called,
  json,
  mockApi,
  noContent,
  originalFetch,
  renderPage,
} from '../test/harness';
import type { MemoryDocument } from '../memories/types';
import MemoriesPage from './MemoriesPage';

const photo = (id: string, caption = '') => ({
  id: `img-${id}`,
  position: 0,
  file_id: id,
  caption,
  crop: null,
  rotation: 0,
  filter: 'original',
  adjustments: { brightness: 0, contrast: 0, saturation: 0 },
  edited: false,
  media: {
    available: true,
    status: 'present',
    name: `${id}.jpg`,
    media_type: 'photo',
    thumbnail_url: `/api/v1/libraries/lib1/files/${id}/thumbnail`,
    original_url: `/api/v1/libraries/lib1/files/${id}/download`,
    width: 400,
    height: 300,
  },
  derived: null,
});

function memoryDoc(overrides: Partial<MemoryDocument> = {}): MemoryDocument {
  return {
    id: 'mem1',
    title: 'My Trip to Kerala',
    body: 'We left **early**.',
    description: '',
    location: 'Kochi',
    memory_date: '2026-09-01T00:00:00Z',
    tags: [],
    revision: 4,
    deleted: false,
    created_at: '2026-09-01T10:00:00Z',
    updated_at: '2026-09-02T10:00:00Z',
    blocks: [
      { id: 'blk-text-1', type: 'text', markdown: 'We left **early**.' },
      {
        id: 'blk-img-1',
        type: 'image',
        layout: 'grid',
        slideshow: { enabled: false, interval_seconds: null },
        images: [photo('p1', 'The road to Munnar'), photo('p2')],
      },
    ],
    ...overrides,
  } as MemoryDocument;
}

interface Setup {
  memory?: MemoryDocument;
  list?: unknown[];
  saveResponse?: (body: { base_revision?: number; blocks: unknown[] }) => Response;
}

function setup(options: Setup = {}) {
  let current = options.memory ?? memoryDoc();
  const list = options.list ?? [current];
  const fn = mockApi([
    (url) =>
      url.endsWith('/api/v1/settings/memories')
        ? json({
            settings: {
              slideshow_interval: 15,
              edited_copies: false,
              default_layout: 'grid',
              default_mode: 'edit',
              autosave: true,
            },
          })
        : undefined,
    (url, init) => {
      if (!/\/memories\/mem1\/document$/.test(url) || init?.method !== 'PUT') return undefined;
      const body = JSON.parse(String(init.body)) as { base_revision?: number; blocks: unknown[] };
      if (options.saveResponse) return options.saveResponse(body);
      current = {
        ...current,
        revision: current.revision + 1,
        blocks: body.blocks as MemoryDocument['blocks'],
      };
      return json({ memory: current });
    },
    (url, init) => {
      if (!/\/memories\/mem1$/.test(url) || init?.method !== 'PATCH') return undefined;
      const patch = JSON.parse(String(init.body)) as Partial<MemoryDocument>;
      current = { ...current, ...patch, revision: current.revision + 1 } as MemoryDocument;
      return json({ memory: current });
    },
    (url, init) =>
      /\/memories\/mem1$/.test(url) && init?.method === 'DELETE' ? noContent() : undefined,
    (url) => (/\/memories\/mem1$/.test(url) ? json({ memory: current }) : undefined),
    (url, init) =>
      /\/libraries\/lib1\/memories$/.test(url) && init?.method === 'POST'
        ? json({ memory: memoryDoc({ id: 'mem2', title: 'Untitled memory', blocks: [] }) }, 201)
        : undefined,
    (url) =>
      /\/libraries\/lib1\/memories(\?.*)?$/.test(url) ? json({ memories: list }) : undefined,
  ]);
  return fn;
}

function renderMemories(route = '/memories/mem1') {
  return renderPage(
    <Routes>
      <Route path="/memories" element={<MemoriesPage />} />
      <Route path="/memories/:memoryId" element={<MemoriesPage />} />
    </Routes>,
    { route },
  );
}

const documentPuts = (fn: ReturnType<typeof vi.fn>) =>
  fn.mock.calls.filter(([url, init]) => /\/document$/.test(String(url)) && init?.method === 'PUT');

describe('MemoriesPage (notebook)', () => {
  beforeEach(() => {
    localStorage.clear();
  });
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('lists memories and opens one as a notebook of blocks', async () => {
    setup();
    renderMemories();
    expect(await screen.findByTestId('memory-editor')).toBeInTheDocument();
    expect(screen.getByLabelText('Memory title')).toHaveValue('My Trip to Kerala');
    const text = screen.getByTestId('text-block-editor');
    expect(text.textContent).toBe('We left **early**.\n');
    expect(text.querySelector('strong')).not.toBeNull();
    expect(
      screen.getByRole('group', { name: /Media section 2 of 2, 2 items/ }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Add text' }).length).toBe(3);
  });

  it('shows an empty state and creates a memory', async () => {
    const fn = setup({ list: [] });
    renderMemories('/memories');
    expect(await screen.findByTestId('memories-empty')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('new-memory'));
    await waitFor(() => expect(called(fn, 'POST', '/api/v1/libraries/lib1/memories')).toBe(true));
    const body = bodyOf(fn, 'POST', '/api/v1/libraries/lib1/memories') as {
      blocks: Array<{ type: string }>;
    };
    expect(body.blocks[0]?.type).toBe('text');
  });

  it('shows the landing page with a New memory tile, and remembers the list view', async () => {
    setup();
    renderMemories('/memories');
    expect(await screen.findByText('My Trip to Kerala')).toBeInTheDocument();
    expect(screen.getByTestId('new-memory')).toBeInTheDocument();
    // No editor and no library dropdown on the landing page.
    expect(screen.queryByTestId('memory-editor')).toBeNull();
    expect(screen.queryByLabelText('Library')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'List view' }));
    expect(localStorage.getItem('cairn.memories.view')).toBe('list');
    expect(screen.getByText('My Trip to Kerala').closest('.mem-row')).not.toBeNull();
  });

  it('opens a memory on its own screen and returns with ← Memories', async () => {
    setup();
    renderMemories('/memories');
    fireEvent.click(await screen.findByText('My Trip to Kerala'));
    expect(await screen.findByTestId('memory-editor')).toBeInTheDocument();
    expect(screen.queryByTestId('new-memory')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Back to memories' }));
    expect(await screen.findByTestId('new-memory')).toBeInTheDocument();
  });

  it('autosaves edits once, debounced, with the base revision', async () => {
    const fn = setup();
    renderMemories();
    const text = await screen.findByTestId('text-block-editor');
    for (const value of ['We', 'We left', 'We left at dawn.']) {
      text.textContent = `${value}\n`;
      fireEvent.input(text);
    }
    await waitFor(() => expect(screen.getByTestId('save-status')).toHaveTextContent('Saved'), {
      timeout: 4000,
    });
    const puts = documentPuts(fn);
    expect(puts).toHaveLength(1);
    const sent = JSON.parse(String(puts[0]![1]!.body)) as {
      base_revision: number;
      blocks: Array<Record<string, unknown>>;
    };
    expect(sent.base_revision).toBe(4);
    expect(sent.blocks[0]).toEqual({
      id: 'blk-text-1',
      type: 'text',
      markdown: 'We left at dawn.',
    });
    // Image references are sent as references with their edits, never as media.
    expect(sent.blocks[1]).toMatchObject({ id: 'blk-img-1', type: 'image', layout: 'grid' });
    expect((sent.blocks[1]!.images as Array<Record<string, unknown>>)[0]).toEqual({
      id: 'img-p1',
      file_id: 'p1',
      caption: 'The road to Munnar',
      crop: null,
      rotation: 0,
      filter: 'original',
      adjustments: { brightness: 0, contrast: 0, saturation: 0 },
    });
  });

  it('keeps a local draft and recovers it after a refresh', async () => {
    setup({ saveResponse: () => apiError(500, 'INTERNAL', 'boom') });
    const { unmount } = renderMemories();
    const text = await screen.findByTestId('text-block-editor');
    text.textContent = 'Unsaved thought\n';
    fireEvent.input(text);
    expect(
      await screen.findByText('Save failed', undefined, { timeout: 4000 }),
    ).toBeInTheDocument();
    unmount();

    // Same revision on the server: the draft is restored silently.
    setup();
    renderMemories();
    const again = await screen.findByTestId('text-block-editor');
    await waitFor(() => expect(again.textContent).toBe('Unsaved thought\n'));
    expect(screen.getByText(/Restored changes that had not been saved/)).toBeInTheDocument();
  });

  it('never overwrites newer server content with an old draft without asking', async () => {
    localStorage.setItem(
      'cairn.memory.draft.lib1.mem1',
      JSON.stringify({
        base_revision: 2,
        blocks: [{ id: 'blk-text-1', type: 'text', markdown: 'Old draft' }],
        meta: {},
        saved_at: '2026-09-01T00:00:00Z',
      }),
    );
    const fn = setup();
    renderMemories();
    expect(await screen.findByText(/were found on this device/)).toBeInTheDocument();
    expect(screen.getByTestId('text-block-editor').textContent).toBe('We left **early**.\n');
    expect(documentPuts(fn)).toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: 'Restore my changes' }));
    await waitFor(() =>
      expect(screen.getByTestId('text-block-editor').textContent).toBe('Old draft\n'),
    );
  });

  it('shows a conflict instead of overwriting a newer revision', async () => {
    setup({
      saveResponse: () =>
        json(
          {
            error: {
              code: 'CONFLICT',
              message: 'changed',
              details: { current_revision: 9 },
              request_id: 't',
            },
          },
          409,
        ),
    });
    renderMemories();
    const text = await screen.findByTestId('text-block-editor');
    text.textContent = 'Mine\n';
    fireEvent.input(text);
    expect(
      await screen.findByText(/changed somewhere else/, undefined, { timeout: 4000 }),
    ).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Keep mine' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Load the other version' })).toBeInTheDocument();
  });

  it('inserts a text block between blocks and moves blocks from the menu', async () => {
    const fn = setup();
    renderMemories();
    await screen.findByTestId('memory-editor');
    fireEvent.click(screen.getAllByRole('button', { name: 'Add text' })[1]!);
    expect(screen.getAllByTestId('text-block-editor')).toHaveLength(2);

    fireEvent.click(screen.getByRole('button', { name: 'Media section actions' }));
    const menu = await screen.findByTestId('context-menu');
    fireEvent.click(within(menu).getByText('Move up'));
    await waitFor(() => expect(documentPuts(fn).length).toBeGreaterThan(0), { timeout: 4000 });
    const last = documentPuts(fn).at(-1)!;
    const ids = (
      JSON.parse(String(last[1]!.body)) as { blocks: Array<{ id: string; type: string }> }
    ).blocks.map((b) => b.type);
    expect(ids).toEqual(['text', 'image', 'text']);
  });

  it('removes a photo from the memory without touching the library file', async () => {
    const fn = setup();
    renderMemories();
    await screen.findByTestId('memory-editor');
    const figure = document.querySelector('[data-image-id="img-p1"]')!;
    fireEvent.contextMenu(figure);
    const menu = await screen.findByTestId('context-menu');
    expect(within(menu).getByText('Edit image')).toBeInTheDocument();
    expect(within(menu).getByText('Add media')).toBeInTheDocument();
    fireEvent.click(within(menu).getByText('Remove from memory'));
    expect(await screen.findByText(/original stays in your library/)).toBeInTheDocument();
    await waitFor(() => expect(documentPuts(fn).length).toBe(1), { timeout: 4000 });
    const sent = JSON.parse(String(documentPuts(fn)[0]![1]!.body)) as {
      blocks: Array<{ images?: Array<{ file_id: string }> }>;
    };
    expect(sent.blocks[1]!.images!.map((i) => i.file_id)).toEqual(['p2']);
    // No file endpoint was ever called with a destructive method.
    expect(
      fn.mock.calls.some(
        ([url, init]) => /\/files\//.test(String(url)) && init?.method === 'DELETE',
      ),
    ).toBe(false);
  });

  it('replaces a section holding an unavailable image with a red notice', async () => {
    const doc = memoryDoc();
    const block = doc.blocks[1] as unknown as { images: Array<ReturnType<typeof photo>> };
    block.images[0] = {
      ...block.images[0]!,
      media: { available: false, status: 'missing' } as never,
    };
    setup({ memory: doc });
    renderMemories();
    const notice = await screen.findByText('Image unavailable');
    expect(notice).toHaveClass('image-unavailable');
    expect(screen.queryByText('The road to Munnar')).toBeNull();
  });

  it('switches to Preview, which hides every editing control', async () => {
    setup();
    renderMemories();
    await screen.findByTestId('memory-editor');
    fireEvent.click(screen.getByRole('button', { name: 'Preview' }));
    const reader = await screen.findByTestId('memory-reader');
    expect(within(reader).getByRole('heading', { level: 1 })).toHaveTextContent(
      'My Trip to Kerala',
    );
    expect(reader.innerHTML).toContain('<strong>early</strong>');
    expect(screen.queryByRole('button', { name: 'Add text' })).toBeNull();
    expect(screen.queryByTestId('text-block-editor')).toBeNull();
    expect(screen.getByText('Kochi')).toBeInTheDocument();
  });

  it('deletes a memory after confirming', async () => {
    const fn = setup();
    renderMemories();
    await screen.findByTestId('memory-editor');
    fireEvent.click(screen.getByTestId('delete-memory'));
    const dialog = await screen.findByTestId('delete-memory-dialog');
    expect(dialog).toHaveTextContent('none of its photos are touched');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(called(fn, 'DELETE', '/memories/mem1')).toBe(true));
  });

  it('saves title changes through the metadata endpoint', async () => {
    const fn = setup();
    renderMemories();
    const title = await screen.findByLabelText('Memory title');
    fireEvent.change(title, { target: { value: 'Kerala, 2026' } });
    await waitFor(() => expect(called(fn, 'PATCH', '/memories/mem1')).toBe(true), {
      timeout: 4000,
    });
    expect(bodyOf(fn, 'PATCH', '/memories/mem1')).toEqual({
      title: 'Kerala, 2026',
      base_revision: 4,
    });
  });

  it('does not send a blank title', async () => {
    const fn = setup();
    renderMemories();
    const title = await screen.findByLabelText('Memory title');
    fireEvent.change(title, { target: { value: '   ' } });
    expect(await screen.findByText(/A memory needs a title/)).toBeInTheDocument();
    await act(async () => {
      await new Promise((r) => setTimeout(r, 900));
    });
    expect(called(fn, 'PATCH', '/memories/mem1')).toBe(false);
  });
});

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
import TagsPage from './TagsPage';

const tags = {
  tags: [{ id: 't1', name: 'vacation', created_at: '2026-01-02T00:00:00Z' }],
};

const tagFiles = { files: [fileFixture()] };

function setup(overrides: { tags?: typeof tags; tagFiles?: typeof tagFiles } = {}) {
  const tagList = overrides.tags ?? tags;
  const files = overrides.tagFiles ?? tagFiles;
  const fn = mockApi([
    (url, init) =>
      url.endsWith('/api/v1/libraries/lib1/files/f1/tags/t1') && init?.method === 'DELETE'
        ? noContent()
        : undefined, // remove tag from file
    (url, init) =>
      url.endsWith('/api/v1/libraries/lib1/tags/t1') && init?.method === 'DELETE'
        ? noContent()
        : undefined, // delete tag
    (url, init) =>
      url.endsWith('/api/v1/libraries/lib1/tags') && init?.method === 'POST'
        ? json({ tag: { id: 't9', name: 'Trip' } }, 201)
        : undefined,
    (url) => (url.endsWith('/api/v1/libraries/lib1/tags') ? json(tagList) : undefined),
    (url) => {
      if (!url.includes('/search?')) return undefined;
      const name = url.split('tag=')[1]?.split('&')[0] ?? '';
      return json(name === 'vacation' ? files : { files: [] });
    },
    (url) =>
      url.includes('/files/f1/note') ? json({ note: { file_id: 'f1', body: '' } }) : undefined,
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
  renderPage(<TagsPage />);
  return fn;
}

describe('TagsPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('renders the heading and tag cards', async () => {
    setup();

    expect(await screen.findByRole('heading', { name: 'Tags' })).toBeInTheDocument();
    expect(await screen.findByTestId('tags-grid')).toBeInTheDocument();
    expect(screen.getByText('vacation')).toBeInTheDocument();
  });

  it('shows an empty state when there are no tags', async () => {
    setup({ tags: { tags: [] } });

    expect(await screen.findByTestId('tags-empty')).toBeInTheDocument();
  });

  it('prompts for a name in a dialog instead of window.prompt', async () => {
    const fetchMock = setup();
    const promptSpy = vi.spyOn(window, 'prompt');

    await screen.findByTestId('tags-grid');

    fireEvent.click(screen.getByRole('button', { name: 'New tag' }));
    const dialog = await screen.findByTestId('new-tag-dialog');
    expect(dialog).toHaveAttribute('role', 'dialog');

    fireEvent.change(screen.getByLabelText('Tag name'), { target: { value: 'Trip' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(
        fetchMock.mock.calls.some(
          ([input, init]) =>
            String(input).endsWith('/api/v1/libraries/lib1/tags') && init?.method === 'POST',
        ),
      ).toBe(true);
    });
    expect(bodyOf(fetchMock, 'POST', '/api/v1/libraries/lib1/tags')).toEqual({ name: 'Trip' });
    expect(promptSpy).not.toHaveBeenCalled();
  });

  it('keeps a failed create inside the dialog instead of closing it', async () => {
    mockApi([
      (url, init) =>
        url.endsWith('/api/v1/libraries/lib1/tags') && init?.method === 'POST'
          ? json(
              { error: { code: 'CONFLICT', message: 'That tag already exists.', request_id: '1' } },
              409,
            )
          : undefined,
      (url) => (url.endsWith('/api/v1/libraries/lib1/tags') ? json(tags) : undefined),
    ]);
    renderPage(<TagsPage />);

    await screen.findByTestId('tags-grid');
    fireEvent.click(screen.getByRole('button', { name: 'New tag' }));
    const dialog = await screen.findByTestId('new-tag-dialog');
    fireEvent.change(screen.getByLabelText('Tag name'), { target: { value: 'Trip' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('That tag already exists.');
    expect(screen.getByTestId('new-tag-dialog')).toBeInTheDocument();
  });

  it('opens a tag and lists its files', async () => {
    setup();

    await screen.findByTestId('tags-grid');
    fireEvent.click(screen.getByRole('button', { name: /vacation/ }));

    expect(await screen.findByTestId('tag-detail')).toBeInTheDocument();
    expect(screen.getByTestId('file-grid')).toBeInTheDocument();
    expect(screen.getByText('IMG_0001.png')).toBeInTheDocument();
  });

  it('shows an empty state for a tag nothing carries yet', async () => {
    setup({ tags: { tags: [{ id: 't2', name: 'quiet', created_at: '2026-01-02T00:00:00Z' }] } });

    await screen.findByTestId('tags-grid');
    fireEvent.click(screen.getByRole('button', { name: /quiet/ }));

    expect(await screen.findByTestId('tag-files-empty')).toBeInTheDocument();
  });

  it('removes a tag from a file shown under that tag', async () => {
    const fetchMock = setup();

    await screen.findByTestId('tags-grid');
    fireEvent.click(screen.getByRole('button', { name: /vacation/ }));
    await screen.findByTestId('file-grid');

    fireEvent.click(screen.getByRole('button', { name: 'Select IMG_0001.png' }));
    fireEvent.click(await screen.findByTestId('selection-untag'));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/files/f1/tags/t1')).toBe(true);
    });
  });

  it('deletes a tag after confirming the dialog', async () => {
    const fetchMock = setup();
    const confirmSpy = vi.spyOn(window, 'confirm');

    await screen.findByTestId('tags-grid');
    fireEvent.click(screen.getByRole('button', { name: /vacation/ }));
    await screen.findByTestId('tag-detail');

    fireEvent.click(screen.getByRole('button', { name: 'Delete tag' }));
    const dialog = await screen.findByTestId('delete-tag-dialog');
    expect(dialog).toHaveAttribute('role', 'dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete tag' }));

    await waitFor(() => {
      expect(called(fetchMock, 'DELETE', '/api/v1/libraries/lib1/tags/t1')).toBe(true);
    });
    // Back on the tag list after deletion.
    expect(await screen.findByTestId('tags-grid')).toBeInTheDocument();
    expect(confirmSpy).not.toHaveBeenCalled();
  });

  it('does nothing when the delete dialog is cancelled', async () => {
    const fetchMock = setup();

    await screen.findByTestId('tags-grid');
    fireEvent.click(screen.getByRole('button', { name: /vacation/ }));
    await screen.findByTestId('tag-detail');

    fireEvent.click(screen.getByRole('button', { name: 'Delete tag' }));
    const dialog = await screen.findByTestId('delete-tag-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    await waitFor(() => {
      expect(screen.queryByTestId('delete-tag-dialog')).not.toBeInTheDocument();
    });
    expect(called(fetchMock, 'DELETE', '/tags/t1')).toBe(false);
    expect(screen.getByTestId('tag-detail')).toBeInTheDocument();
  });
});

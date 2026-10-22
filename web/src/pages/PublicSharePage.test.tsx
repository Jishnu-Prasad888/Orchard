import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Route, Routes } from 'react-router-dom';

import {
  apiError,
  called,
  fileFixture,
  json,
  mockApi,
  originalFetch,
  renderPage,
} from '../test/harness';
import type { RouteHandler } from '../test/harness';
import PublicSharePage from './PublicSharePage';

const shareInfo = {
  share: {
    kind: 'library' as const,
    name: 'Photos',
    library: 'Photos',
    has_password: false,
    can_download: true,
  },
};

const firstPage = { files: [fileFixture()], next_cursor: '' };

/**
 * The public route is mounted at `/s/:token` outside the app shell and outside
 * `RequireAuth`, so it is rendered bare with a route that actually supplies the
 * token.
 */
function renderShare(rules: RouteHandler[] = [], route = '/s/tok1') {
  const fn = mockApi(rules);
  renderPage(
    <Routes>
      <Route path="/s/:token" element={<PublicSharePage />} />
    </Routes>,
    { route, bare: true },
  );
  return fn;
}

describe('PublicSharePage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('opens a password-free share without a session', async () => {
    const fetchMock = renderShare([
      (url) => (url.includes('/api/v1/shares/tok1/files?') ? json(firstPage) : undefined),
      (url) => (url.endsWith('/api/v1/shares/tok1') ? json(shareInfo) : undefined),
    ]);

    expect(await screen.findByRole('heading', { name: 'Photos' })).toBeInTheDocument();
    expect(screen.getByText('Shared from Photos')).toBeInTheDocument();
    const list = await screen.findByTestId('share-file-list');
    expect(within(list).getByText('IMG_0001.png')).toBeInTheDocument();
    // No session means no session cookie is being relied on.
    expect(called(fetchMock, 'GET', '/api/v1/shares/tok1')).toBe(true);
  });

  it('asks for the password when the share is locked', async () => {
    renderShare([
      (url) =>
        url.endsWith('/api/v1/shares/tok1')
          ? apiError(401, 'UNAUTHORIZED', 'This share needs a password.')
          : undefined,
    ]);

    expect(
      await screen.findByRole('heading', { name: 'This share is locked' }),
    ).toBeInTheDocument();
    expect(screen.getByTestId('share-password-input')).toBeInTheDocument();
    expect(screen.queryByTestId('share-file-list')).not.toBeInTheDocument();
  });

  it('sends the password in the header, never in the URL', async () => {
    const fetchMock = renderShare([
      (url, init) => {
        if (!url.endsWith('/api/v1/shares/tok1')) return undefined;
        const headers = (init?.headers ?? {}) as Record<string, string>;
        if (headers['X-Orchard-Share-Password'] !== 'hunter2') {
          return apiError(401, 'UNAUTHORIZED', 'This share needs a password.');
        }
        return json(shareInfo);
      },
      (url) => (url.includes('/api/v1/shares/tok1/files?') ? json(firstPage) : undefined),
    ]);

    fireEvent.change(await screen.findByTestId('share-password-input'), {
      target: { value: 'hunter2' },
    });
    fireEvent.click(screen.getByTestId('share-unlock'));

    await waitFor(() =>
      expect(screen.getByRole('heading', { name: 'Photos' })).toBeInTheDocument(),
    );
    // The heading comes from the share info; the file list is requested right
    // after it, so wait for that request rather than assuming it already went.
    await waitFor(() => expect(called(fetchMock, 'GET', '/api/v1/shares/tok1/files?')).toBe(true));
    // The credential is a header, so a link stays safe to paste into a chat.
    expect(fetchMock.mock.calls.every(([input]) => !String(input).includes('hunter2'))).toBe(true);
  });

  it('explains a share that is gone rather than asking for a password', async () => {
    renderShare([
      (url) =>
        url.endsWith('/api/v1/shares/tok1')
          ? apiError(404, 'NOT_FOUND', 'This share link is no longer valid.')
          : undefined,
    ]);

    expect(
      await screen.findByRole('heading', { name: 'This share is locked' }),
    ).toBeInTheDocument();
    expect(screen.getByText('This share link is no longer valid.')).toBeInTheDocument();
  });

  it('downloads a file by fetching the bytes with the share password', async () => {
    const createObjectURL = vi.fn(() => 'blob:fake');
    const revokeObjectURL = vi.fn();
    Object.defineProperty(URL, 'createObjectURL', { value: createObjectURL, configurable: true });
    Object.defineProperty(URL, 'revokeObjectURL', { value: revokeObjectURL, configurable: true });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});

    const fetchMock = renderShare([
      (url) =>
        url.endsWith(`/api/v1/shares/tok1/files/${fileFixture().id}/download`)
          ? new Response('bytes', { status: 200 })
          : undefined,
      (url) => (url.includes('/api/v1/shares/tok1/files?') ? json(firstPage) : undefined),
      (url) => (url.endsWith('/api/v1/shares/tok1') ? json(shareInfo) : undefined),
    ]);

    fireEvent.click(await screen.findByTestId('download-f1'));

    await waitFor(() => {
      expect(called(fetchMock, 'GET', '/api/v1/shares/tok1/files/f1/download')).toBe(true);
    });
    await waitFor(() => {
      expect(clickSpy).toHaveBeenCalled();
    });
    // The blob is released rather than leaked for the life of the page.
    await waitFor(() => {
      expect(revokeObjectURL).toHaveBeenCalledWith('blob:fake');
    });
  });

  it('shows an empty share plainly', async () => {
    renderShare([
      (url) => (url.includes('/api/v1/shares/tok1/files?') ? json({ files: [] }) : undefined),
      (url) => (url.endsWith('/api/v1/shares/tok1') ? json(shareInfo) : undefined),
    ]);

    expect(await screen.findByTestId('share-empty')).toBeInTheDocument();
  });

  it('appends the next page of a large share', async () => {
    const second = fileFixture({ id: 'f2', rel_path: 'beach.jpg', name: 'beach.jpg' });
    renderShare([
      (url) =>
        url.includes('cursor=cur2') ? json({ files: [second], next_cursor: '' }) : undefined,
      (url) =>
        url.includes('/api/v1/shares/tok1/files?')
          ? json({ files: [fileFixture()], next_cursor: 'cur2' })
          : undefined,
      (url) => (url.endsWith('/api/v1/shares/tok1') ? json(shareInfo) : undefined),
    ]);

    const list = await screen.findByTestId('share-file-list');
    expect(within(list).getByText('IMG_0001.png')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('share-load-more'));
    await waitFor(() => {
      expect(within(list).getByText('beach.jpg')).toBeInTheDocument();
    });
  });

  it('opens a folder link at its folder, browses down, and goes back up no higher', async () => {
    const folderInfo = {
      share: { ...shareInfo.share, kind: 'folder' as const, name: 'Japan', folder: 'trips/japan' },
    };
    const fetchMock = renderShare([
      (url) =>
        url.includes('folder=trips%2Fjapan%2Fkyoto')
          ? json({ files: [], folders: [], folder: 'trips/japan/kyoto' })
          : undefined,
      (url) =>
        url.includes('/api/v1/shares/tok1/files?')
          ? json({
              ...firstPage,
              folder: 'trips/japan',
              folders: [
                {
                  id: 'k',
                  rel_path: 'trips/japan/kyoto',
                  name: 'kyoto',
                  file_count: 3,
                  library_id: '',
                },
              ],
            })
          : undefined,
      (url) => (url.endsWith('/api/v1/shares/tok1') ? json(folderInfo) : undefined),
    ]);

    expect(await screen.findByRole('heading', { name: 'Japan' })).toBeInTheDocument();
    // The link carries no folder: the server opens it at the shared one.
    await waitFor(() => expect(called(fetchMock, 'GET', '/api/v1/shares/tok1/files?')).toBe(true));
    expect(fetchMock.mock.calls.some(([u]) => String(u).includes('folder='))).toBe(false);
    // At the top of the share there is nowhere further up to go.
    expect(screen.queryByTestId('share-up')).not.toBeInTheDocument();

    fireEvent.click(within(await screen.findByTestId('share-folders')).getByText('kyoto'));
    await waitFor(() =>
      expect(called(fetchMock, 'GET', 'folder=trips%2Fjapan%2Fkyoto')).toBe(true),
    );
    fireEvent.click(await screen.findByTestId('share-up'));
    await waitFor(() => expect(screen.queryByTestId('share-up')).not.toBeInTheDocument());
  });

  it('offers no download on a view-only link', async () => {
    renderShare([
      (url) => (url.includes('/api/v1/shares/tok1/files?') ? json(firstPage) : undefined),
      (url) =>
        url.endsWith('/api/v1/shares/tok1')
          ? json({ share: { ...shareInfo.share, can_download: false } })
          : undefined,
    ]);

    const list = await screen.findByTestId('share-file-list');
    expect(within(list).getByText('IMG_0001.png')).toBeInTheDocument();
    expect(screen.queryByTestId('download-f1')).not.toBeInTheDocument();
  });

  it('says the link is missing its token', async () => {
    mockApi([]);
    renderPage(
      <Routes>
        <Route path="/s" element={<PublicSharePage />} />
      </Routes>,
      { route: '/s', bare: true },
    );

    expect(await screen.findByText('This link is missing its share token.')).toBeInTheDocument();
  });

  it('opens a file in the viewer even when the link does not allow downloads', async () => {
    const second = fileFixture({
      id: 'f2',
      rel_path: 'notes.docx',
      name: 'notes.docx',
      media_type: 'document',
    });
    renderShare([
      (url) =>
        url.includes('/api/v1/shares/tok1/files?')
          ? json({ files: [fileFixture(), second] })
          : undefined,
      (url) =>
        url.endsWith('/api/v1/shares/tok1')
          ? json({ share: { ...shareInfo.share, can_download: false } })
          : undefined,
    ]);

    fireEvent.click(await screen.findByTestId('open-f1'));
    const viewer = await screen.findByTestId('share-viewer');
    // The picture comes from the view route, not the download route.
    const img = within(viewer).getByRole('img', { name: 'IMG_0001.png' });
    expect(img.getAttribute('src')).toMatch(/\/api\/v1\/shares\/tok1\/files\/f1\/content$/);
    expect(within(viewer).queryByTestId('share-viewer-download')).not.toBeInTheDocument();
    expect(within(viewer).getByText(/1 of 2/)).toBeInTheDocument();

    // → steps to the next file; one with no preview says so.
    fireEvent.keyDown(window, { key: 'ArrowRight' });
    expect(await within(viewer).findByTestId('share-viewer-no-preview')).toHaveTextContent(
      'This link does not allow downloads.',
    );
    fireEvent.click(within(viewer).getByTestId('share-viewer-prev'));
    expect(within(viewer).getByRole('img', { name: 'IMG_0001.png' })).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByTestId('share-viewer')).not.toBeInTheDocument());
  });

  it('offers download from the viewer when the link allows it', async () => {
    renderShare([
      (url) => (url.includes('/api/v1/shares/tok1/files?') ? json(firstPage) : undefined),
      (url) => (url.endsWith('/api/v1/shares/tok1') ? json(shareInfo) : undefined),
    ]);

    fireEvent.click(await screen.findByTestId('open-f1'));
    const viewer = await screen.findByTestId('share-viewer');
    expect(within(viewer).getByTestId('share-viewer-download')).toBeInTheDocument();
  });
});

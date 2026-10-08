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
    resource_key: 'library:lib1',
    capabilities: ['read' as const, 'download' as const],
    library: 'Photos',
    requires_password: false,
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

    expect(await screen.findByRole('heading', { name: /library:lib1/ })).toBeInTheDocument();
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
        if (headers['X-Cairn-Share-Password'] !== 'hunter2') {
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
      expect(screen.getByRole('heading', { name: /library:lib1/ })).toBeInTheDocument(),
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

  it('scopes the listing to a folder carried in the link', async () => {
    const fetchMock = renderShare(
      [
        (url) => (url.includes('/api/v1/shares/tok1/files?') ? json(firstPage) : undefined),
        (url) => (url.endsWith('/api/v1/shares/tok1') ? json(shareInfo) : undefined),
      ],
      '/s/tok1?folder=2024',
    );

    expect(await screen.findByRole('heading', { name: /2024/ })).toBeInTheDocument();
    await waitFor(() => {
      expect(called(fetchMock, 'GET', 'folder=2024')).toBe(true);
    });
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
});

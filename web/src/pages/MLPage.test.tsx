import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import {
  apiError,
  bodyOf,
  called,
  json,
  libraryFixture,
  mockApi,
  originalFetch,
  renderPage,
} from '../test/harness';
import type { RouteHandler } from '../test/harness';
import MLPage from './MLPage';

const mlStatus = {
  enabled: true,
  provider: 'clip',
  provider_version: 1,
  signatured: 8421,
  pending: 12,
  last_pass_at: '2026-09-01T00:00:00Z',
};

const faceStatus = {
  enabled: true,
  provider: 'pigo',
  provider_version: 1,
  faces: 331,
  people: 24,
  unassigned: 57,
};

function setup(overrides: RouteHandler[] = [], options = {}) {
  const fn = mockApi(
    [
      ...overrides,
      (url) => (url.endsWith('/api/v1/libraries/lib1/ml/faces') ? json(faceStatus) : undefined),
      (url) => (url.endsWith('/api/v1/libraries/lib1/ml') ? json(mlStatus) : undefined),
    ],
    options,
  );
  renderPage(<MLPage />, options);
  return fn;
}

describe('MLPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('reports the similarity and face counters', async () => {
    setup();

    expect(await screen.findByRole('heading', { name: 'Machine learning' })).toBeInTheDocument();
    const stats = await screen.findByTestId('ml-stats');
    // Numbers are formatted with the locale's thousands separator (e.g. "8,421").
    expect(within(stats).getByText('Signatures').previousSibling).toHaveTextContent('8,421');
    // Pending work gets a hint saying what to do about it.
    expect(within(stats).getByText('A pass will pick these up')).toBeInTheDocument();

    const faces = screen.getByTestId('face-stats');
    expect(within(faces).getByText('Faces found').previousSibling).toHaveTextContent('331');
    expect(within(faces).getByText('Unassigned').previousSibling).toHaveTextContent('57');
    expect(within(faces).getByText('Name these on the People page')).toBeInTheDocument();

    expect(screen.getByTestId('ml-enabled')).toBeInTheDocument();
    expect(screen.getByTestId('faces-enabled')).toBeInTheDocument();
  });

  it('shows a similarity pass running even when this page did not start it', async () => {
    setup([
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/ml')
          ? json({ ...mlStatus, running: true })
          : undefined,
    ]);

    expect(await screen.findByTestId('ml-running')).toHaveTextContent('Scanning…');
    expect(screen.queryByTestId('ml-enabled')).not.toBeInTheDocument();
    expect(screen.getByTestId('ml-progress')).toHaveTextContent('photos analysed so far');
    const button = screen.getByTestId('run-similarity-pass');
    expect(button).toBeDisabled();
    expect(button).toHaveTextContent('Running…');
  });

  it('shows a face pass running even when this page did not start it', async () => {
    setup([
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/ml/faces')
          ? json({ ...faceStatus, running: true, pending: 4 })
          : undefined,
    ]);

    expect(await screen.findByTestId('faces-running')).toHaveTextContent('Working…');
    expect(screen.queryByTestId('faces-enabled')).not.toBeInTheDocument();
    expect(screen.getByTestId('faces-working')).toHaveTextContent('4 photos left');
    const detect = screen.getByTestId('run-face-pass');
    expect(detect).toBeDisabled();
    expect(detect).toHaveTextContent('Running…');
    expect(screen.getByTestId('run-face-cluster')).toBeDisabled();
  });

  it('starts a similarity pass and says it is background work', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/ml/similarity/pass')
          ? json({ library_id: 'lib1', status: 'started' }, 202)
          : undefined,
    ]);

    await screen.findByTestId('ml-stats');
    fireEvent.click(screen.getByTestId('run-similarity-pass'));

    expect(
      await screen.findByText('Similarity pass started in the background.'),
    ).toBeInTheDocument();
    expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/ml/similarity/pass')).toBe(true);
  });

  it('shows a live progress bar while a pass it started is running, then toasts when it finishes', async () => {
    let statusReads = 0;
    setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/ml/similarity/pass')
          ? json({ library_id: 'lib1', status: 'started' }, 202)
          : undefined,
      (url) => {
        if (!url.endsWith('/api/v1/libraries/lib1/ml')) return undefined;
        statusReads += 1;
        // The initial read (page load) is idle, so the button is clickable.
        // The read right after starting shows it running, with some progress
        // made; every read after that shows it finished.
        if (statusReads === 1) return json(mlStatus);
        return json(
          statusReads === 2
            ? { ...mlStatus, pending: 12, signatured: 8427, running: true }
            : { ...mlStatus, pending: 0, signatured: 8433, running: false },
        );
      },
    ]);

    await screen.findByTestId('ml-stats');
    fireEvent.click(screen.getByTestId('run-similarity-pass'));

    const progress = await screen.findByTestId('ml-progress');
    expect(progress).toHaveTextContent('8,427 of 8,439 photos analysed so far');

    expect(
      await screen.findByText(
        'Similarity pass finished — 8,433 photos analysed.',
        {},
        { timeout: 5000 },
      ),
    ).toBeInTheDocument();
  });

  it('starts a face pass and a clustering pass', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/ml/faces/pass')
          ? json({ library_id: 'lib1', status: 'started' }, 202)
          : undefined,
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/ml/faces/cluster')
          ? json({ library_id: 'lib1', status: 'started' }, 202)
          : undefined,
    ]);

    await screen.findByTestId('face-stats');
    fireEvent.click(screen.getByTestId('run-face-pass'));
    expect(await screen.findByText('Face detection pass started.')).toBeInTheDocument();
    expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/ml/faces/pass')).toBe(true);

    // Only one pass may be in flight, so the second button stays disabled for a
    // moment after the first settles.
    await waitFor(() => {
      expect(screen.getByTestId('run-face-cluster')).toBeEnabled();
    });
    fireEvent.click(screen.getByTestId('run-face-cluster'));
    expect(await screen.findByText('Face clustering pass started.')).toBeInTheDocument();
    expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/ml/faces/cluster')).toBe(true);
  });

  it('will not cluster before there is anything to cluster', async () => {
    setup([
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/ml/faces')
          ? json({ ...faceStatus, faces: 0, unassigned: 0 })
          : undefined,
    ]);

    await screen.findByTestId('face-stats');
    expect(screen.getByTestId('run-face-cluster')).toBeDisabled();
    expect(
      within(screen.getByTestId('face-stats')).getByText('Every face has a name'),
    ).toBeInTheDocument();
  });

  it('discards the derived data after confirming, and keeps it on cancel', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/ml/purge')
          ? json({ library_id: 'lib1', purged: 8421 })
          : undefined,
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/ml/faces/purge')
          ? json({ library_id: 'lib1', faces_removed: 331 })
          : undefined,
    ]);

    await screen.findByTestId('ml-stats');
    fireEvent.click(screen.getByTestId('purge-similarity'));
    let dialog = await screen.findByTestId('ml-purge-dialog');
    // Purging is safe, but it is still a decision, so it is confirmed.
    expect(within(dialog).getByText(/Your photos are not touched/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => {
      expect(screen.queryByTestId('ml-purge-dialog')).not.toBeInTheDocument();
    });
    expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/ml/purge')).toBe(false);

    fireEvent.click(screen.getByTestId('purge-faces'));
    dialog = await screen.findByTestId('ml-purge-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Discard' }));

    await waitFor(() => {
      expect(called(fetchMock, 'POST', '/api/v1/libraries/lib1/ml/faces/purge')).toBe(true);
    });
    expect(await screen.findByText('Face data discarded.')).toBeInTheDocument();
  });

  it('explains a server built without face recognition', async () => {
    setup([
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/ml/faces')
          ? apiError(404, 'NOT_FOUND', 'not found')
          : undefined,
    ]);

    expect(await screen.findByTestId('faces-unavailable')).toHaveTextContent(
      'built without face recognition',
    );
    // Similarity is a separate build flag, so it still works.
    expect(screen.getByTestId('ml-stats')).toBeInTheDocument();
    expect(screen.queryByTestId('run-face-pass')).not.toBeInTheDocument();
  });

  it('explains ML that is switched off', async () => {
    setup([
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/ml')
          ? apiError(503, 'UNAVAILABLE', 'ML is disabled')
          : undefined,
    ]);

    expect(await screen.findByTestId('ml-unavailable')).toHaveTextContent(
      'without local image embeddings',
    );
    // Duplicate detection compares bytes, so it is unaffected.
    expect(screen.getByTestId('ml-unavailable')).toHaveTextContent('duplicate detection');
    expect(screen.getByTestId('face-stats')).toBeInTheDocument();
  });

  it('offers nothing to run when both providers are switched off', async () => {
    setup([
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/ml/faces')
          ? json({ ...faceStatus, enabled: false })
          : undefined,
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/ml')
          ? json({ ...mlStatus, enabled: false, signatured: 0, pending: 0 })
          : undefined,
    ]);

    // The stats render as soon as the status arrives, which is when the
    // "Available" badges should have disappeared and the buttons gone off.
    const stats = await screen.findByTestId('ml-stats');
    expect(within(stats).getByText('Signatures').previousSibling).toHaveTextContent('0');
    expect(screen.queryByTestId('ml-enabled')).not.toBeInTheDocument();
    expect(screen.queryByTestId('faces-enabled')).not.toBeInTheDocument();
    expect(screen.getByTestId('run-similarity-pass')).toBeDisabled();
    expect(screen.getByTestId('purge-similarity')).toBeDisabled();
    expect(screen.getByTestId('run-face-pass')).toBeDisabled();
  });

  it('does not offer to run a pass against a disconnected library', async () => {
    setup([], { libraries: [libraryFixture({ status: 'offline' })] });

    expect(await screen.findByTestId('library-offline')).toBeInTheDocument();
    expect(screen.getByTestId('run-similarity-pass')).toBeDisabled();
    expect(screen.getByTestId('run-face-pass')).toBeDisabled();
  });

  it('reports a real failure rather than calling it unavailable', async () => {
    setup([
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/ml')
          ? apiError(500, 'INTERNAL', 'The status file could not be read.')
          : undefined,
    ]);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('The status file could not be read.');
    expect(screen.queryByTestId('ml-unavailable')).not.toBeInTheDocument();
  });

  it('surfaces the error the last pass reported', async () => {
    setup([
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/ml')
          ? json({ ...mlStatus, error: 'signature model did not converge' })
          : undefined,
    ]);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('signature model did not converge');
  });

  it('reports a failed purge instead of claiming it worked', async () => {
    setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/ml/purge')
          ? apiError(500, 'INTERNAL', 'The signature store is locked.')
          : undefined,
    ]);

    await screen.findByTestId('ml-stats');
    fireEvent.click(screen.getByTestId('purge-similarity'));
    const dialog = await screen.findByTestId('ml-purge-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Discard' }));

    expect(await screen.findByText('The signature store is locked.')).toBeInTheDocument();
  });

  it('posts nothing to the ML routes when the member has no libraries', async () => {
    const fetchMock = mockApi([], { libraries: [] });
    renderPage(<MLPage />);

    expect(await screen.findByTestId('no-libraries')).toBeInTheDocument();
    expect(called(fetchMock, 'GET', '/api/v1/libraries/lib1/ml')).toBe(false);
  });
});

describe('MLPage — unavailable payloads', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it('does not treat a disabled provider as an error', async () => {
    // The server answers 200 with enabled: false rather than 503 when the
    // provider is compiled in but switched off by configuration.
    setup([
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/ml')
          ? json({ enabled: false, signatured: 0, pending: 0 })
          : undefined,
    ]);

    await screen.findByTestId('ml-stats');
    expect(screen.queryByTestId('ml-unavailable')).not.toBeInTheDocument();
    expect(screen.getByTestId('run-similarity-pass')).toBeDisabled();
  });

  it('shows the pending count with no hint when there is none', async () => {
    setup([
      (url) =>
        url.endsWith('/api/v1/libraries/lib1/ml')
          ? json({ ...mlStatus, pending: 0, last_pass_at: '' })
          : undefined,
    ]);

    const stats = await screen.findByTestId('ml-stats');
    expect(within(stats).queryByText('A pass will pick these up')).not.toBeInTheDocument();
    expect(within(stats).getByText('Never')).toBeInTheDocument();
  });

  it('sends no body to the pass endpoints', async () => {
    const fetchMock = setup([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/libraries/lib1/ml/similarity/pass')
          ? json({ library_id: 'lib1', status: 'started' }, 202)
          : undefined,
    ]);

    await screen.findByTestId('ml-stats');
    fireEvent.click(screen.getByTestId('run-similarity-pass'));
    await waitFor(() => {
      expect(
        bodyOf(fetchMock, 'POST', '/api/v1/libraries/lib1/ml/similarity/pass'),
      ).toBeUndefined();
    });
  });
});

import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useEffect } from 'react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { uploadTiming } from '../../api/resumableUpload';
import {
  apiError,
  bodyOf,
  called,
  json,
  mockApi,
  noContent,
  originalFetch,
} from '../../test/harness';
import Providers from '../../test/Providers';
import { TopBar } from '../app-shell/TopBar';
import { useUploads } from './UploadProvider';
import { UploadTray } from './UploadTray';

type Handler = (() => void) | null;

/** An XMLHttpRequest whose requests (upload chunks) finish when the test says so. */
const requests: FakeXhr[] = [];
class FakeXhr {
  status = 200;
  responseText = '';
  upload: { onprogress: ((e: unknown) => void) | null } = { onprogress: null };
  onload: Handler = null;
  onerror: Handler = null;
  onabort: Handler = null;
  withCredentials = false;
  url = '';
  headers: Record<string, string> = {};
  constructor() {
    requests.push(this);
  }
  open(_m: string, url: string) {
    this.url = url;
  }
  body: unknown = null;
  setRequestHeader(name: string, value: string) {
    this.headers[name] = value;
  }
  send(body: unknown) {
    this.body = body;
  }
  abort() {
    this.onabort?.();
  }
  progress(loaded: number, total: number) {
    this.upload.onprogress?.({ lengthComputable: true, loaded, total });
  }
  /** The chunk arrived; by default the server now has the whole (1-byte) file. */
  finish(status = 200, body?: string) {
    const id = this.url.split('/').pop()!;
    this.status = status;
    this.responseText = body ?? JSON.stringify({ upload: { ...sessions.get(id), offset: 1 } });
    this.onload?.();
  }
  /** The connection dropped. */
  fail() {
    this.onerror?.();
  }
}

/** Server-side upload sessions, by id. */
const sessions = new Map<string, { id: string; path: string; size: number; offset: number }>();
/** What `complete` answers for a destination path (default: an empty file). */
let completions: Record<string, string> = {};

const uploadRoutes = (url: string, init: RequestInit | undefined): Response | undefined => {
  const method = init?.method ?? 'GET';
  const match = /\/uploads(?:\/([^/?]+))?(\/complete)?$/.exec(url);
  if (!match) return undefined;
  const [, id, complete] = match;
  if (method === 'POST' && !id) {
    const { path, size } = JSON.parse(String(init?.body)) as { path: string; size: number };
    const session = { id: `u${sessions.size + 1}`, path, size, offset: 0 };
    sessions.set(session.id, session);
    return json({ upload: { ...session, chunk_size: 8 << 20, complete: false } }, 201);
  }
  const session = id ? sessions.get(id) : undefined;
  if (!session) return apiError(404, 'UPLOAD_NOT_FOUND', 'gone');
  if (method === 'POST' && complete) {
    return new Response(completions[session.path] ?? '{"file":{}}', { status: 201 });
  }
  if (method === 'GET') return json({ upload: { ...session, chunk_size: 8 << 20 } });
  if (method === 'DELETE') return noContent();
  return undefined;
};

function Pick() {
  const { enqueue, completed } = useUploads();
  return (
    <>
      <p data-testid="completed">{completed}</p>
      <button
        onClick={() =>
          enqueue(
            'lib1',
            ['a', 'b', 'c'].map((n) => new File(['x'], `${n}.pdf`)),
            'trip',
          )
        }
      >
        add
      </button>
    </>
  );
}

/** Where the app has navigated to, for asserting the tray's links. */
function Location() {
  const { pathname, search } = useLocation();
  return <p data-testid="location">{pathname + search}</p>;
}

let fetchMock: ReturnType<typeof mockApi>;

function mount(ui: React.ReactNode) {
  requests.length = 0;
  sessions.clear();
  vi.stubGlobal('XMLHttpRequest', FakeXhr);
  fetchMock = mockApi([uploadRoutes]);
  render(
    <MemoryRouter initialEntries={['/']}>
      <Providers>
        <Routes>
          <Route path="*" element={<Location />} />
        </Routes>
        {ui}
      </Providers>
    </MemoryRouter>,
  );
}

function setup() {
  mount(
    <>
      <Pick />
      <UploadTray />
    </>,
  );
  fireEvent.click(screen.getByText('add'));
}

/** The server's answer for an uploaded file. */
function uploadedFile(id: string, name: string, mediaType: string) {
  return JSON.stringify({
    file: {
      id,
      library_id: 'lib1',
      rel_path: `trip/${name}`,
      name,
      folder_path: 'trip',
      size_bytes: 1,
      mod_time: '2026-10-19T00:00:00Z',
      media_type: mediaType,
      status: 'present',
    },
  });
}

describe('uploads', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    globalThis.fetch = originalFetch;
    completions = {};
    uploadTiming.baseDelayMs = 1000;
  });

  it('runs two at a time, into the chosen folder, and starts the next as one finishes', async () => {
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    expect(screen.getByText('Uploading 3 files')).toBeInTheDocument();
    expect(requests[0]!.headers['Upload-Offset']).toBe('0');

    act(() => requests[0]!.finish());
    await waitFor(() => expect(requests).toHaveLength(3));
    expect(screen.getByTestId('completed')).toHaveTextContent('1');
  });

  it('reports a failure in the server’s words and retries it', async () => {
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    act(() =>
      requests[0]!.finish(
        413,
        JSON.stringify({
          error: {
            code: 'PAYLOAD_TOO_LARGE',
            message: 'That file is too large.',
            request_id: '1',
          },
        }),
      ),
    );
    act(() => requests[1]!.finish());
    await waitFor(() => expect(requests).toHaveLength(3));
    act(() => requests[2]!.finish());

    expect(await screen.findByText('1 upload failed')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Show upload details' }));
    expect(screen.getByText('That file is too large.')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Retry a.pdf' }));
    await waitFor(() => expect(requests).toHaveLength(4));
  });

  it('rides out a dropped connection and resumes from what the server has', async () => {
    uploadTiming.baseDelayMs = 0;
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    // The byte reached the server, but the connection died before the reply.
    sessions.get('u1')!.offset = 1;
    act(() => requests[0]!.fail());

    // No re-send: it asks the server, sees the file is all there, finishes.
    await waitFor(() => expect(screen.getByTestId('completed')).toHaveTextContent('1'));
    expect(called(fetchMock, 'GET', '/uploads/u1')).toBe(true);
    expect(called(fetchMock, 'POST', '/uploads/u1/complete')).toBe(true);
    expect(requests.filter((r) => r.url.endsWith('/u1'))).toHaveLength(1);
    expect(screen.queryByText(/failed/)).not.toBeInTheDocument();
  });

  it('says why an upload paused while it waits to retry', async () => {
    uploadTiming.baseDelayMs = 20_000;
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    act(() => requests[0]!.fail());

    expect(
      await screen.findByText(/a\.pdf: The connection to the server was lost\. Retrying in 20 s/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/failed/)).not.toBeInTheDocument();
  });

  it('cancels a file in flight and frees the server’s copy', async () => {
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    fireEvent.click(screen.getByRole('button', { name: 'Show upload details' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel a.pdf' }));
    expect(await screen.findByText('Cancelled')).toBeInTheDocument();
    await waitFor(() => expect(called(fetchMock, 'DELETE', '/uploads/u1')).toBe(true));
  });

  it('shows overall progress', async () => {
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    act(() => requests[0]!.progress(1, 1));
    const bar = screen.getByRole('progressbar', { name: 'Upload progress' });
    await waitFor(() => expect(Number(bar.getAttribute('aria-valuenow'))).toBeGreaterThan(0));
  });

  it('opens a finished upload with View', async () => {
    completions = { 'trip/a.pdf': uploadedFile('f1', 'a.pdf', 'photo') };
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    act(() => requests[0]!.finish());

    fireEvent.click(await screen.findByRole('button', { name: 'View a.pdf' }));
    expect(screen.getByTestId('location')).toHaveTextContent('/files?folder=trip&view=f1');
  });

  it('opens an uploaded book in the reader', async () => {
    completions = { 'trip/a.pdf': uploadedFile('b1', 'a.epub', 'document') };
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    act(() => requests[0]!.finish());

    fireEvent.click(await screen.findByRole('button', { name: 'View a.epub' }));
    expect(screen.getByTestId('location')).toHaveTextContent('/read/lib1/b1');
  });

  it('shows the uploaded files in their folder when the tray is clicked', async () => {
    completions = {
      'trip/a.pdf': uploadedFile('f1', 'a.pdf', 'photo'),
      'trip/b.pdf': uploadedFile('f2', 'b.pdf', 'photo'),
    };
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    act(() => requests[0]!.finish());
    await waitFor(() => expect(screen.getByTestId('completed')).toHaveTextContent('1'));
    act(() => requests[1]!.finish());

    fireEvent.click(await screen.findByTestId('upload-reveal'));
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent('/files?folder=trip&reveal=f1,f2'),
    );
  });

  it('uploads from the top bar into the folder on screen', async () => {
    function OnFolder() {
      const { setTarget } = useUploads();
      useEffect(() => setTarget({ libraryId: 'lib1', folder: 'trip' }), [setTarget]);
      return null;
    }
    mount(
      <>
        <OnFolder />
        <TopBar searchRef={{ current: null }} onShowShortcuts={() => {}} />
      </>,
    );

    const input = await screen.findByLabelText('Upload files to Photos');
    fireEvent.change(input, { target: { files: [new File(['x'], 'beach.pdf')] } });

    await waitFor(() => expect(requests).toHaveLength(1));
    expect(bodyOf(fetchMock, 'POST', '/uploads')).toEqual({ path: 'trip/beach.pdf', size: 1 });
  });

  it('only offers books, and skips anything else with a word why', async () => {
    mount(<TopBar searchRef={{ current: null }} onShowShortcuts={() => {}} />);

    const input = await screen.findByLabelText('Upload files to Photos');
    expect(input).toHaveAttribute('accept', expect.stringContaining('.epub'));
    fireEvent.change(input, {
      target: {
        files: [
          new File(['x'], 'Tool-x86_64.AppImage'),
          new File(['x'], 'Novel.EPUB'),
          new File(['x'], 'photo.jpg'),
        ],
      },
    });

    expect(
      await screen.findByText('Skipped 2 files that aren’t EPUBs or PDFs.'),
    ).toBeInTheDocument();
    await waitFor(() => expect(requests).toHaveLength(1));
    expect(bodyOf(fetchMock, 'POST', '/uploads')).toEqual({ path: 'Novel.EPUB', size: 1 });
  });
});

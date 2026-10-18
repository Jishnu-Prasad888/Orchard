import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useEffect } from 'react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { mockApi, originalFetch } from '../../test/harness';
import Providers from '../../test/Providers';
import { TopBar } from '../app-shell/TopBar';
import { useUploads } from './UploadProvider';
import { UploadTray } from './UploadTray';

type Handler = (() => void) | null;

/** An XMLHttpRequest whose requests finish when the test says so. */
const requests: FakeXhr[] = [];
class FakeXhr {
  status = 201;
  responseText = '{"file":{}}';
  upload: { onprogress: ((e: unknown) => void) | null } = { onprogress: null };
  onload: Handler = null;
  onerror: Handler = null;
  onabort: Handler = null;
  withCredentials = false;
  url = '';
  constructor() {
    requests.push(this);
  }
  open(_m: string, url: string) {
    this.url = url;
  }
  body: unknown = null;
  setRequestHeader() {}
  send(body: unknown) {
    this.body = body;
  }
  abort() {
    this.onabort?.();
  }
  progress(loaded: number, total: number) {
    this.upload.onprogress?.({ lengthComputable: true, loaded, total });
  }
  finish(status = 201, body = '{"file":{}}') {
    this.status = status;
    this.responseText = body;
    this.onload?.();
  }
}

function Pick() {
  const { enqueue, completed } = useUploads();
  return (
    <>
      <p data-testid="completed">{completed}</p>
      <button
        onClick={() =>
          enqueue(
            'lib1',
            ['a', 'b', 'c'].map((n) => new File(['x'], `${n}.jpg`)),
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

function mount(ui: React.ReactNode) {
  requests.length = 0;
  vi.stubGlobal('XMLHttpRequest', FakeXhr);
  mockApi();
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
  });

  it('runs two at a time, into the chosen folder, and starts the next as one finishes', async () => {
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    expect(screen.getByText('Uploading 3 files')).toBeInTheDocument();

    act(() => requests[0]!.finish());
    await waitFor(() => expect(requests).toHaveLength(3));
    expect(screen.getByTestId('completed')).toHaveTextContent('1');
  });

  it('reports a failure and retries it', async () => {
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    act(() =>
      requests[0]!.finish(
        413,
        JSON.stringify({
          error: { code: 'TOO_LARGE', message: 'That file is too large.', request_id: '1' },
        }),
      ),
    );
    act(() => requests[1]!.finish());
    await waitFor(() => expect(requests).toHaveLength(3));
    act(() => requests[2]!.finish());

    expect(await screen.findByText('1 upload failed')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Show upload details' }));
    expect(screen.getByText('That file is too large.')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Retry a.jpg' }));
    await waitFor(() => expect(requests).toHaveLength(4));
  });

  it('cancels a file in flight', async () => {
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    fireEvent.click(screen.getByRole('button', { name: 'Show upload details' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel a.jpg' }));
    expect(await screen.findByText('Cancelled')).toBeInTheDocument();
  });

  it('shows overall progress', async () => {
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    act(() => requests[0]!.progress(1, 1));
    const bar = screen.getByRole('progressbar', { name: 'Upload progress' });
    await waitFor(() => expect(Number(bar.getAttribute('aria-valuenow'))).toBeGreaterThan(0));
  });

  it('opens a finished upload with View', async () => {
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    act(() => requests[0]!.finish(201, uploadedFile('f1', 'a.jpg', 'photo')));

    fireEvent.click(await screen.findByRole('button', { name: 'View a.jpg' }));
    expect(screen.getByTestId('location')).toHaveTextContent('/files?folder=trip&view=f1');
  });

  it('opens an uploaded book in the reader', async () => {
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    act(() => requests[0]!.finish(201, uploadedFile('b1', 'a.epub', 'document')));

    fireEvent.click(await screen.findByRole('button', { name: 'View a.epub' }));
    expect(screen.getByTestId('location')).toHaveTextContent('/read/lib1/b1');
  });

  it('shows the uploaded files in their folder when the tray is clicked', async () => {
    setup();
    await waitFor(() => expect(requests).toHaveLength(2));
    act(() => requests[0]!.finish(201, uploadedFile('f1', 'a.jpg', 'photo')));
    act(() => requests[1]!.finish(201, uploadedFile('f2', 'b.jpg', 'photo')));

    fireEvent.click(await screen.findByTestId('upload-reveal'));
    expect(screen.getByTestId('location')).toHaveTextContent('/files?folder=trip&reveal=f1,f2');
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
    fireEvent.change(input, { target: { files: [new File(['x'], 'beach.jpg')] } });

    await waitFor(() => expect(requests).toHaveLength(1));
    expect((requests[0]!.body as FormData).get('path')).toBe('trip/beach.jpg');
  });
});

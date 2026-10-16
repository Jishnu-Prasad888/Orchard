import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { FileSummary } from '../api/types';
import { called, json, mockApi, originalFetch } from '../test/harness';
import { PropertiesDialog } from './PropertiesDialog';

const book: FileSummary = {
  id: 'f1',
  library_id: 'lib1',
  rel_path: 'shelf/go.pdf',
  name: 'go.pdf',
  folder_path: 'shelf',
  size_bytes: 2048,
  mod_time: '2026-09-01T00:00:00Z',
  media_type: 'document',
  mime_type: 'application/pdf',
  status: 'present',
};

const metadata = (cover?: string) => ({
  book: {
    file_id: 'f1',
    title: 'Go in Practice',
    author: 'A. Gopher',
    publisher: '',
    year: '',
    isbn: '',
    description: '',
    user_edited: false,
    ...(cover ? { cover_url: cover } : {}),
  },
});

afterEach(() => {
  globalThis.fetch = originalFetch;
});

function setup(file: FileSummary, onChanged = vi.fn()) {
  const fetchMock = mockApi([
    (url, init) =>
      init?.method === 'PUT' && url.endsWith('/files/f1/cover')
        ? json(metadata('/api/v1/libraries/lib1/files/f1/cover?v=2'))
        : undefined,
    (url) => (url.endsWith('/files/f1/book-metadata') ? json(metadata()) : undefined),
    (url) => (url.endsWith('/files/f1/reading-status') ? json({ status: null }) : undefined),
  ]);
  render(
    <MemoryRouter>
      <PropertiesDialog libraryId="lib1" file={file} onClose={() => {}} onChanged={onChanged} />
    </MemoryRouter>,
  );
  return { fetchMock, onChanged };
}

describe('PropertiesDialog', () => {
  it('shows the file facts and, for a book, its details', async () => {
    setup(book);
    const facts = screen.getByTestId('properties-facts');
    expect(facts).toHaveTextContent('go.pdf');
    expect(facts).toHaveTextContent('Document (PDF)');
    expect(facts).toHaveTextContent('shelf');
    expect(await screen.findByDisplayValue('Go in Practice')).toBeInTheDocument();
  });

  it('replaces the cover image and tells the page to refresh', async () => {
    const { fetchMock, onChanged } = setup(book);
    await screen.findByDisplayValue('Go in Practice');

    const image = new File(['png-bytes'], 'cover.png', { type: 'image/png' });
    fireEvent.change(screen.getByLabelText('Choose a cover image'), { target: { files: [image] } });

    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(called(fetchMock, 'PUT', '/files/f1/cover')).toBe(true);
    expect(screen.getByRole('button', { name: 'Change cover image' })).toBeInTheDocument();
  });

  it('shows only the file facts for something that is not a book', () => {
    setup({ ...book, name: 'beach.jpg', media_type: 'photo', mime_type: 'image/jpeg' });
    expect(screen.getByTestId('properties-facts')).toHaveTextContent('Photo (JPG)');
    expect(screen.queryByTestId('viewer-book')).not.toBeInTheDocument();
  });
});

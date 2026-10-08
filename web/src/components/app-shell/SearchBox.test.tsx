import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { json, mockApi, originalFetch, renderPage } from '../../test/harness';
import { SearchBox } from './SearchBox';

function Where() {
  const { pathname, search } = useLocation();
  return <p data-testid="where">{pathname + search}</p>;
}

function setup() {
  mockApi([
    (url) =>
      url.endsWith('/libraries/lib1/albums')
        ? json({ albums: [{ id: 'a1', name: 'Mountains trip', created_at: '', updated_at: '' }] })
        : undefined,
    (url) =>
      url.endsWith('/libraries/lib1/tags')
        ? json({ tags: [{ id: 't1', name: 'mountain', created_at: '' }] })
        : undefined,
    (url) => (url.endsWith('/libraries/lib1/people') ? json({ people: [] }) : undefined),
  ]);
  renderPage(
    <>
      <SearchBox />
      <Routes>
        <Route path="*" element={<Where />} />
      </Routes>
    </>,
  );
}

describe('SearchBox', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('suggests searching everything, photos, and videos for what was typed', async () => {
    setup();
    const input = screen.getByRole('combobox', { name: 'Search Cairn' });
    fireEvent.change(input, { target: { value: 'mountains' } });

    const list = await screen.findByRole('listbox');
    expect(within(list).getByText('Search everything')).toBeInTheDocument();
    expect(within(list).getByText('in Photos')).toBeInTheDocument();
    expect(within(list).getByText('in Videos')).toBeInTheDocument();
  });

  it('suggests albums and tags whose names match, from the real library', async () => {
    setup();
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'mountain' } });

    expect(await screen.findByText('Mountains trip')).toBeInTheDocument();
    expect(screen.getByText('Tag')).toBeInTheDocument();
  });

  it('takes a suggestion with the keyboard', async () => {
    setup();
    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'beach' } });
    await screen.findByRole('listbox');
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.submit(screen.getByRole('search'));

    await waitFor(() =>
      expect(screen.getByTestId('where')).toHaveTextContent('/search?q=beach&type=photo'),
    );
  });

  it('remembers searches and offers them again, and forgets on request', async () => {
    setup();
    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'sunset' } });
    fireEvent.submit(screen.getByRole('search'));
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('q=sunset'));

    fireEvent.change(input, { target: { value: '' } });
    fireEvent.focus(input);
    expect(await screen.findByText('Recent searches')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Remove sunset from recent searches' }));
    expect(screen.queryByText('sunset')).not.toBeInTheDocument();
  });

  it('clears with its button and closes with Escape', async () => {
    setup();
    const input = screen.getByRole('combobox') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'x' } });
    fireEvent.click(screen.getByRole('button', { name: 'Clear search' }));
    expect(input.value).toBe('');

    fireEvent.change(input, { target: { value: 'y' } });
    await screen.findByRole('listbox');
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  });
});

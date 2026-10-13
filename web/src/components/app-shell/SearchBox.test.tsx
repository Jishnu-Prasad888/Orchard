import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { called, json, mockApi, noContent, originalFetch, renderPage } from '../../test/harness';
import type { RouteHandler } from '../../test/harness';
import { SearchBox } from './SearchBox';

function Where() {
  const { pathname, search } = useLocation();
  return <p data-testid="where">{pathname + search}</p>;
}

function setup(overrides: RouteHandler[] = []) {
  const fetchMock = mockApi([
    ...overrides,
    (url) =>
      url.endsWith('/libraries/lib1/tags')
        ? json({ tags: [{ id: 't1', name: 'mountain', created_at: '' }] })
        : undefined,
    (url, init) => {
      if (!url.includes('/libraries/lib1/search/recent')) return undefined;
      return (init?.method ?? 'GET') === 'GET' ? json({ searches: [] }) : noContent();
    },
  ]);
  renderPage(
    <>
      <SearchBox />
      <Routes>
        <Route path="*" element={<Where />} />
      </Routes>
    </>,
  );
  return fetchMock;
}

describe('SearchBox', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('suggests searching everything for what was typed', async () => {
    setup();
    const input = screen.getByRole('combobox', { name: 'Search Orchard' });
    fireEvent.change(input, { target: { value: 'mountains' } });

    const list = await screen.findByRole('listbox');
    expect(within(list).getByText('Search everything')).toBeInTheDocument();
  });

  it('suggests tags whose names match, from the real library', async () => {
    setup();
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'mountain' } });

    expect(await screen.findByText('Tag')).toBeInTheDocument();
    expect(screen.getAllByText('mountain').length).toBeGreaterThan(0);
  });

  it('takes a suggestion with the keyboard', async () => {
    setup();
    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'beach' } });
    await screen.findByRole('listbox');
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.submit(screen.getByRole('search'));

    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('/search?q=beach'));
  });

  it('remembers searches and offers them again, and forgets on request', async () => {
    const fetchMock = setup();
    const input = screen.getByRole('combobox');

    // The library list (and so libraryId, which remembering a search needs)
    // resolves from a background fetch on mount; wait for it, the way a
    // real person always takes longer to type than that fetch takes.
    fireEvent.focus(input);
    await waitFor(() => expect(called(fetchMock, 'GET', '/libraries/lib1/tags')).toBe(true));

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

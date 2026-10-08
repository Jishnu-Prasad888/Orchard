import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  apiError,
  called,
  fileFixture,
  json,
  mockApi,
  noContent,
  originalFetch,
  renderPage,
} from '../test/harness';
import SimilarPage from './SimilarPage';

const bigger = fileFixture({ id: 'f1', name: 'IMG_0001.png', size_bytes: 4096 });
const smaller = fileFixture({
  id: 'f2',
  rel_path: '2024/IMG_0001_edit.png',
  name: 'IMG_0001_edit.png',
  folder_path: '2024',
  size_bytes: 1024,
});

const readyStatus = {
  enabled: true,
  provider: 'average_hash',
  provider_version: 1,
  signatured: 2,
  pending: 0,
  running: false,
};

const groupsResponse = { groups: [{ files: [bigger, smaller] }], total: 1 };

function setup(overrides: { status?: unknown; groups?: unknown } = {}) {
  const status = overrides.status ?? readyStatus;
  const groups = overrides.groups ?? groupsResponse;
  const fn = mockApi([
    (url) => (url.includes('/ml/similarity/groups') ? json(groups) : undefined),
    (url) => (url.endsWith('/ml') ? json(status) : undefined),
    (url, init) =>
      url.includes('/files/f1') && init?.method === 'DELETE' ? noContent() : undefined,
    (url, init) =>
      url.includes('/files/f2') && init?.method === 'DELETE' ? noContent() : undefined,
  ]);
  renderPage(<SimilarPage />);
  return fn;
}

describe('SimilarPage', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('shows a heading and the library selector', async () => {
    setup();

    expect(await screen.findByRole('heading', { name: 'Similar photos' })).toBeInTheDocument();
    expect(await screen.findByTestId('library-picker')).toHaveValue('lib1');
  });

  it('explains when the server has no local similarity support', async () => {
    mockApi([(url) => (url.endsWith('/ml') ? apiError(404) : undefined)]);
    renderPage(<SimilarPage />);

    expect(await screen.findByTestId('similar-unavailable')).toBeInTheDocument();
  });

  it('explains when local ML is switched off', async () => {
    setup({ status: { ...readyStatus, enabled: false, signatured: 0 } });

    expect(await screen.findByTestId('similar-disabled')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Machine learning' })).toHaveAttribute('href', '/ml');
  });

  it('explains when no pass has run yet', async () => {
    setup({ status: { ...readyStatus, signatured: 0 } });

    expect(await screen.findByTestId('similar-no-pass')).toBeInTheDocument();
  });

  it('shows an empty state when a pass found nothing alike', async () => {
    setup({ groups: { groups: [], total: 0 } });

    expect(await screen.findByTestId('similar-empty')).toBeInTheDocument();
  });

  it('renders a stack per group with a summary', async () => {
    setup();

    expect(await screen.findByTestId('similar-summary')).toHaveTextContent(
      '1 group of similar photos',
    );
    expect(screen.getAllByTestId('similar-stack')).toHaveLength(1);
  });

  it('notes photos still waiting on a pass', async () => {
    setup({ status: { ...readyStatus, pending: 3 } });

    await screen.findByTestId('similar-summary');
    expect(screen.getByText(/3 more photos have not been checked yet/)).toBeInTheDocument();
  });

  it('opens a group with the largest copy unchecked and the rest checked', async () => {
    setup();

    fireEvent.click(await screen.findByTestId('similar-stack'));
    const dialog = await screen.findByTestId('similar-group-dialog');

    expect(within(dialog).getByText('1 of 2 selected')).toBeInTheDocument();
    expect(within(dialog).getByRole('checkbox', { name: 'Select IMG_0001.png' })).not.toBeChecked();
    expect(
      within(dialog).getByRole('checkbox', { name: 'Select IMG_0001_edit.png' }),
    ).toBeChecked();
    expect(within(dialog).getByText('Largest copy')).toBeInTheDocument();
  });

  it('selects and deselects everything', async () => {
    setup();

    fireEvent.click(await screen.findByTestId('similar-stack'));
    const dialog = await screen.findByTestId('similar-group-dialog');

    fireEvent.click(within(dialog).getByTestId('similar-deselect-all'));
    expect(within(dialog).getByText('0 of 2 selected')).toBeInTheDocument();
    expect(within(dialog).getByTestId('similar-confirm-trash')).toBeDisabled();

    fireEvent.click(within(dialog).getByTestId('similar-select-all'));
    expect(within(dialog).getByText('2 of 2 selected')).toBeInTheDocument();
  });

  it('moves only the checked files to trash and refreshes the groups', async () => {
    const fetchMock = setup();

    fireEvent.click(await screen.findByTestId('similar-stack'));
    const dialog = await screen.findByTestId('similar-group-dialog');
    fireEvent.click(within(dialog).getByTestId('similar-confirm-trash'));

    await waitFor(() => {
      expect(screen.queryByTestId('similar-group-dialog')).not.toBeInTheDocument();
    });
    expect(called(fetchMock, 'DELETE', '/files/f2')).toBe(true);
    expect(called(fetchMock, 'DELETE', '/files/f1')).toBe(false);
  });
});

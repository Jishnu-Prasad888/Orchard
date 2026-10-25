import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { Dictionary, DictionaryJob } from '../../api/types';
import {
  bodyOf,
  called,
  json,
  member,
  mockApi,
  noContent,
  originalFetch,
} from '../../test/harness';
import Providers from '../../test/Providers';
import { DictionarySettings } from './DictionarySettings';

const wordNet: Dictionary = {
  id: 'wordnet',
  kind: 'wordnet',
  name: 'WordNet',
  description: 'English definitions',
  version: '2024',
  license: 'CC BY 4.0',
  size_bytes: 29_458_397,
  installed_at: '2026-10-25T00:00:00Z',
  enabled: true,
};

const catalog = {
  sources: [
    {
      id: 'wordnet',
      kind: 'wordnet',
      name: 'WordNet',
      description: 'English definitions, examples and synonyms.',
      version: '2024',
      license: 'CC BY 4.0',
      download_bytes: 11_457_906,
    },
    {
      id: 'freedict-eng-spa',
      kind: 'freedict',
      name: 'FreeDict English → Spanish',
      description: 'Translations from English into Spanish.',
      version: '2024.10.10',
      license: 'GPL',
      download_bytes: 5_010_204,
      headwords: 64258,
    },
    {
      id: 'freedict-deu-eng',
      kind: 'freedict',
      name: 'FreeDict German → English',
      description: 'Translations from German into English.',
      version: '1.9',
      license: 'GPL',
      download_bytes: 20_000_000,
    },
  ],
};

function job(
  phase: DictionaryJob['phase'],
  done: number,
  extra: Partial<DictionaryJob> = {},
): DictionaryJob {
  return {
    id: 'j1',
    dictionary_id: 'wordnet',
    name: 'WordNet',
    phase,
    done,
    total: 11_457_906,
    started_at: '2026-10-25T00:00:00Z',
    ...extra,
  };
}

function renderSettings(isAdmin = true) {
  render(
    <Providers>
      <DictionarySettings isAdmin={isAdmin} />
    </Providers>,
  );
}

describe('DictionarySettings', () => {
  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('downloads WordNet with a progress dialog, then confirms with a toast', async () => {
    // The server's answers to polling as the install moves along.
    const states: Array<{ dictionaries: Dictionary[]; job: DictionaryJob | null }> = [
      { dictionaries: [], job: null },
    ];
    let installed = false;
    let polls = 0;
    const fetchMock = mockApi([
      (url, init) => {
        if (init?.method === 'POST' && url.endsWith('/api/v1/dictionaries/install')) {
          installed = true;
          return json({ job: job('downloading', 0) }, 202);
        }
        return undefined;
      },
      (url) => {
        if (!url.endsWith('/api/v1/dictionaries')) return undefined;
        if (!installed) return json({ ...states[0], available: false });
        polls++;
        if (polls === 1)
          return json({ dictionaries: [], available: false, job: job('downloading', 5_728_953) });
        return json({
          dictionaries: [wordNet],
          available: true,
          job: job('done', 0, { finished_at: '2026-10-25T00:00:05Z' }),
        });
      },
      (url) => (url.endsWith('/api/v1/dictionaries/catalog') ? json(catalog) : undefined),
    ]);
    renderSettings();

    expect(await screen.findByText('No dictionary installed')).toBeInTheDocument();
    fireEvent.click(await screen.findByTestId('dictionary-download-wordnet'));

    const dialog = await screen.findByTestId('dictionary-install-dialog');
    expect(within(dialog).getByText('Installing WordNet')).toBeInTheDocument();
    expect(within(dialog).getByTestId('dictionary-progress')).toBeInTheDocument();
    expect(
      await within(dialog).findByText(/5\.5 MB of 11 MB · 50%/, {}, { timeout: 3000 }),
    ).toBeInTheDocument();
    expect(bodyOf(fetchMock, 'POST', '/api/v1/dictionaries/install')).toEqual({ id: 'wordnet' });

    // Done: the dialog closes, a toast confirms, and WordNet is listed.
    expect(await screen.findByText(/WordNet is ready/, {}, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.queryByTestId('dictionary-install-dialog')).not.toBeInTheDocument();
    expect(screen.getByTestId('dictionary-toggle-wordnet')).toHaveAttribute('aria-checked', 'true');
    expect(screen.queryByTestId('dictionary-download-wordnet')).not.toBeInTheDocument();
  });

  it('cancels a running download from the dialog', async () => {
    let started = false;
    let cancelled = false;
    const fetchMock = mockApi([
      (url, init) =>
        init?.method === 'POST' && url.endsWith('/api/v1/dictionaries/install')
          ? ((started = true), json({ job: job('downloading', 0) }, 202))
          : undefined,
      (url, init) => {
        if (init?.method === 'DELETE' && url.endsWith('/api/v1/dictionaries/install')) {
          cancelled = true;
          return noContent();
        }
        return undefined;
      },
      (url) =>
        url.endsWith('/api/v1/dictionaries')
          ? json({
              dictionaries: [],
              available: false,
              job: !started
                ? null
                : cancelled
                  ? job('cancelled', 0, { finished_at: '2026-10-25T00:00:05Z' })
                  : job('downloading', 1000),
            })
          : undefined,
      (url) => (url.endsWith('/api/v1/dictionaries/catalog') ? json(catalog) : undefined),
    ]);
    renderSettings();
    fireEvent.click(await screen.findByTestId('dictionary-download-wordnet'));
    const dialog = await screen.findByTestId('dictionary-install-dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel download' }));

    await waitFor(() =>
      expect(called(fetchMock, 'DELETE', '/api/v1/dictionaries/install')).toBe(true),
    );
    expect(
      await screen.findByText('Stopped downloading WordNet.', {}, { timeout: 3000 }),
    ).toBeInTheDocument();
  });

  it('turns a dictionary off and offers FreeDict pairs grouped by direction', async () => {
    const fetchMock = mockApi([
      (url, init) =>
        init?.method === 'PATCH' && url.endsWith('/api/v1/dictionaries/wordnet')
          ? json({ dictionary: { ...wordNet, enabled: false } })
          : undefined,
      (url) =>
        url.endsWith('/api/v1/dictionaries')
          ? json({ dictionaries: [wordNet], available: true, job: null })
          : undefined,
      (url) => (url.endsWith('/api/v1/dictionaries/catalog') ? json(catalog) : undefined),
    ]);
    renderSettings();

    const toggle = await screen.findByTestId('dictionary-toggle-wordnet');
    fireEvent.click(toggle);
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'false'));
    expect(bodyOf(fetchMock, 'PATCH', '/api/v1/dictionaries/wordnet')).toEqual({ enabled: false });

    // WordNet is installed, so only FreeDict is offered.
    expect(screen.queryByTestId('dictionary-download-wordnet')).not.toBeInTheDocument();
    const select = screen.getByRole('combobox', { name: 'FreeDict language pair' });
    expect(within(select).getByRole('group', { name: 'From English' })).toHaveTextContent(
      'English → Spanish',
    );
    expect(within(select).getByRole('group', { name: 'Into English' })).toHaveTextContent(
      'German → English',
    );
    expect(screen.getByTestId('dictionary-download-freedict')).toBeDisabled();
    fireEvent.change(select, { target: { value: 'freedict-eng-spa' } });
    expect(screen.getByText(/64,258 words/)).toBeInTheDocument();
    expect(screen.getByTestId('dictionary-download-freedict')).toBeEnabled();
  });

  it('shows members what is installed without the controls', async () => {
    const fetchMock = mockApi(
      [
        (url) =>
          url.endsWith('/api/v1/dictionaries')
            ? json({ dictionaries: [wordNet], available: true, job: null })
            : undefined,
      ],
      { user: member },
    );
    renderSettings(false);
    expect(await screen.findByTestId('dictionary-toggle-wordnet')).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Remove' })).not.toBeInTheDocument();
    expect(called(fetchMock, 'GET', '/api/v1/dictionaries/catalog')).toBe(false);
  });
});

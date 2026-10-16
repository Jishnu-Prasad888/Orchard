import { describe, expect, it } from 'vitest';

import { matchesQuery, pageSearchMode } from './pageSearch';

describe('pageSearchMode', () => {
  it('searches settings on the settings page', () => {
    expect(pageSearchMode('/settings')).toEqual({ kind: 'settings' });
  });

  it('filters pages that hold their own lists', () => {
    expect(pageSearchMode('/quotes')).toEqual({ kind: 'filter', noun: 'quotes' });
    expect(pageSearchMode('/bookmarks')).toMatchObject({ kind: 'filter' });
    expect(pageSearchMode('/notes')).toMatchObject({ kind: 'filter' });
  });

  it('searches files everywhere else', () => {
    for (const path of ['/', '/files', '/favorites', '/search']) {
      expect(pageSearchMode(path)).toEqual({ kind: 'files' });
    }
  });
});

describe('matchesQuery', () => {
  it('matches every word anywhere across the fields, ignoring case', () => {
    expect(matchesQuery('stoic marcus', 'Meditations', 'Marcus Aurelius, a Stoic')).toBe(true);
    expect(matchesQuery('stoic seneca', 'Meditations', 'Marcus Aurelius, a Stoic')).toBe(false);
  });

  it('matches everything for an empty query, and skips missing fields', () => {
    expect(matchesQuery('', undefined)).toBe(true);
    expect(matchesQuery('x', undefined, null)).toBe(false);
  });
});

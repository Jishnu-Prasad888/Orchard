/**
 * Page-aware search: which kind of search the top bar offers depends on the
 * page underneath it.
 *
 *   settings                      finds a setting (SettingsSearch)
 *   quotes, bookmarks, tags,      filters the page's own list in place, via
 *   collections                   a `?q=` on the page's URL
 *   everything else               finds files (the global SearchBox)
 *
 * Filtering pages read the query back with `usePageQuery()` and narrow their
 * list with `matchesQuery()`, so the box and the page agree on what a match
 * is: every word of the query appears somewhere in the item's text.
 */

import { useSearchParams } from 'react-router-dom';

export type PageSearchMode =
  { kind: 'settings' } | { kind: 'files' } | { kind: 'filter'; noun: string };

const FILTER_PAGES: Record<string, string> = {
  '/quotes': 'quotes',
  '/bookmarks': 'bookmarks & highlights',
  '/tags': 'tags',
  '/collections': 'collections',
};

export function pageSearchMode(pathname: string): PageSearchMode {
  if (pathname === '/settings' || pathname.startsWith('/settings/')) return { kind: 'settings' };
  const noun = FILTER_PAGES[pathname];
  if (noun) return { kind: 'filter', noun };
  return { kind: 'files' };
}

/** The search param a filtering page reads its query from. */
export const PAGE_QUERY_PARAM = 'q';

/** The current page's in-place filter query, trimmed ('' when none). */
export function usePageQuery(): string {
  const [params] = useSearchParams();
  return (params.get(PAGE_QUERY_PARAM) ?? '').trim();
}

/** Whether every word of `query` appears in any of `fields`, ignoring case.
 * An empty query matches everything. */
export function matchesQuery(query: string, ...fields: Array<string | null | undefined>): boolean {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (terms.length === 0) return true;
  const hay = fields.filter(Boolean).join(' ').toLowerCase();
  return terms.every((t) => hay.includes(t));
}

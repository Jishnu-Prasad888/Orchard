/**
 * The timeline only knows about files already loaded into the grid, and a
 * busy library's very first page can be entirely one recent span — dozens of
 * phone photos from the last week, say — with nothing to mark a month against
 * until someone scrolls further. Opening the timeline pulls in a few more
 * pages on its own so there is something worth showing right away.
 *
 * It stops the moment there is a decent sample or the listing runs out, so a
 * library that genuinely is all from one week does not keep paging forever.
 */

import { useEffect } from 'react';

const TARGET_FILES = 300;

interface AutoloadTarget {
  files: readonly unknown[];
  hasMore: boolean;
  loadingMore: boolean;
  loadMore: () => void;
}

export function useTimelineAutoload(listing: AutoloadTarget, enabled: boolean): void {
  const { files, hasMore, loadingMore, loadMore } = listing;
  useEffect(() => {
    if (!enabled || loadingMore || !hasMore || files.length >= TARGET_FILES) return;
    loadMore();
  }, [enabled, files.length, hasMore, loadingMore, loadMore]);
}

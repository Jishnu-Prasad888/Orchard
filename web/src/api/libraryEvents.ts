/**
 * "The library changed on disk."
 *
 * Files can appear, move or vanish outside Cairn (a file manager, a sync
 * tool); the server notices on its next rescan. When the app learns of that —
 * the index counts moved, or the tab came back to the front — it announces it
 * here, and any open list quietly re-reads itself. Nothing flashes and no
 * scroll position is lost: pages keep showing what they have until the fresh
 * result arrives.
 */

import { useEffect, useState } from 'react';

const EVENT = 'cairn:library-changed';

export function notifyLibraryChanged(libraryId: string) {
  window.dispatchEvent(new CustomEvent(EVENT, { detail: { libraryId } }));
}

/** A counter that ticks each time `libraryId` is announced as changed. */
export function useLibraryChangeTick(libraryId: string | null): number {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (!libraryId) return;
    const onChange = (e: Event) => {
      const id = (e as CustomEvent<{ libraryId: string }>).detail?.libraryId;
      if (id === libraryId) setTick((n) => n + 1);
    };
    window.addEventListener(EVENT, onChange);
    return () => window.removeEventListener(EVENT, onChange);
  }, [libraryId]);
  return tick;
}

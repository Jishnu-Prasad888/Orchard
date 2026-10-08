/**
 * The signed-in user's favorites in a library, as a set of file ids, with
 * optimistic add and remove. Grids use it to draw the star badge; selection
 * and context menus use it to offer the right action.
 */

import { useCallback, useEffect, useState } from 'react';

import { addFavorite, listFavorites, removeFavorite } from '../../api/queries';

export interface Favorites {
  ids: ReadonlySet<string>;
  has: (id: string) => boolean;
  /** Add (or with `false`, remove) every id; resolves to how many failed. */
  set: (ids: string[], value: boolean) => Promise<number>;
}

export function useFavorites(libraryId: string | null, refreshKey = 0): Favorites {
  const [state, setState] = useState<{ libraryId: string; ids: ReadonlySet<string> } | null>(null);

  useEffect(() => {
    if (!libraryId) return;
    let cancelled = false;
    listFavorites(libraryId)
      .then((resp) => {
        if (!cancelled) {
          setState({ libraryId, ids: new Set((resp.files ?? []).map((f) => f.id)) });
        }
      })
      .catch(() => {
        // No favorites badge is better than an error over a photo grid.
        if (!cancelled) setState({ libraryId, ids: new Set() });
      });
    return () => {
      cancelled = true;
    };
  }, [libraryId, refreshKey]);

  const ids = state && state.libraryId === libraryId ? state.ids : EMPTY;

  const set = useCallback(
    async (targets: string[], value: boolean) => {
      if (!libraryId) return targets.length;
      // Optimistic: the star changes now, and is put back for any that fail.
      setState((prev) => {
        const next = new Set(prev?.libraryId === libraryId ? prev.ids : []);
        targets.forEach((id) => (value ? next.add(id) : next.delete(id)));
        return { libraryId, ids: next };
      });
      const results = await Promise.allSettled(
        targets.map((id) => (value ? addFavorite(libraryId, id) : removeFavorite(libraryId, id))),
      );
      const failed = targets.filter((_, i) => results[i]?.status === 'rejected');
      if (failed.length) {
        setState((prev) => {
          const next = new Set(prev?.ids ?? []);
          failed.forEach((id) => (value ? next.delete(id) : next.add(id)));
          return { libraryId, ids: next };
        });
      }
      return failed.length;
    },
    [libraryId],
  );

  const has = useCallback((id: string) => ids.has(id), [ids]);

  return { ids, has, set };
}

const EMPTY: ReadonlySet<string> = new Set();

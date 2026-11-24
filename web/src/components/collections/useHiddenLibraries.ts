/**
 * The libraries whose collections the user keeps out of sight (an account
 * setting). Toggling one shows at once and saves behind; a failed save is
 * put back. Until the setting has loaded, nothing counts as hidden.
 */

import { useCallback, useEffect, useRef, useState } from 'react';

import { getHiddenCollectionLibraries, setHiddenCollectionLibraries } from '../../api/queries';

export function useHiddenLibraries() {
  const [ids, setIds] = useState<readonly string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const idsRef = useRef(ids);
  useEffect(() => {
    idsRef.current = ids;
  });
  /** Saves go one after another, so the last toggle wins. */
  const queue = useRef<Promise<unknown>>(Promise.resolve());

  useEffect(() => {
    let cancelled = false;
    getHiddenCollectionLibraries()
      .then((r) => {
        if (!cancelled) setIds(r.library_ids ?? []);
      })
      // An older server, or settings unavailable: nothing is hidden.
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  const isLibraryHidden = useCallback((id: string) => ids.includes(id), [ids]);

  const setLibraryHidden = useCallback((id: string, hidden: boolean) => {
    const before = idsRef.current;
    const next = hidden ? [...before.filter((x) => x !== id), id] : before.filter((x) => x !== id);
    setIds(next);
    setError(null);
    queue.current = queue.current
      .catch(() => undefined)
      .then(() => setHiddenCollectionLibraries(next))
      .catch((e: unknown) => {
        setIds(before);
        setError(e instanceof Error ? e.message : String(e));
      });
  }, []);

  return { hiddenLibraryIds: ids, isLibraryHidden, setLibraryHidden, error };
}

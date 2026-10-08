/**
 * The selected library's index state, kept fresh without being chatty.
 *
 * While a scan is queued or running the status is re-read every few seconds
 * so the count visibly climbs; otherwise once every half minute, which is
 * enough to notice a scan started from another tab or device. Polling pauses
 * while the tab is hidden — a Raspberry Pi should not answer status requests
 * for a window nobody is looking at.
 *
 * Whenever the counts move — during a scan too, so a new library fills in as
 * it is indexed — and once more when a scan finishes, the library is announced
 * as changed and open pages re-read it.
 */

import { useEffect, useRef, useState } from 'react';

import { notifyLibraryChanged } from '../../api/libraryEvents';
import { getIndexStatus } from '../../api/queries';
import type { IndexStatus } from '../../api/types';

const ACTIVE_MS = 4000;
const IDLE_MS = 30000;

export function isIndexing(status: IndexStatus | null): boolean {
  const job = status?.active_job;
  return job !== undefined && (job.Status === 'running' || job.Status === 'queued');
}

export function useIndexStatus(
  libraryId: string | null,
  enabled = true,
  /** Announce changes; only one watcher per library should. */
  announce = true,
): IndexStatus | null {
  const [state, setState] = useState<{ libraryId: string; status: IndexStatus } | null>(null);
  // What the index looked like last time, to notice when a rescan changed it.
  const lastSignature = useRef<{ libraryId: string; value: string; indexing: boolean } | null>(
    null,
  );

  useEffect(() => {
    if (!libraryId || !enabled) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const schedule = (ms: number) => {
      timer = setTimeout(tick, ms);
    };

    const tick = () => {
      if (cancelled) return;
      if (typeof document !== 'undefined' && document.hidden) {
        schedule(IDLE_MS);
        return;
      }
      getIndexStatus(libraryId)
        .then((resp) => {
          if (cancelled) return;
          setState({ libraryId, status: resp.status });
          const s = resp.status;
          const value = `${s.present}|${s.missing}|${s.deleted}`;
          const indexing = isIndexing(s);
          const before = lastSignature.current;
          lastSignature.current = { libraryId, value, indexing };
          if (
            announce &&
            before &&
            before.libraryId === libraryId &&
            (before.value !== value || (before.indexing && !indexing))
          ) {
            notifyLibraryChanged(libraryId);
          }
          schedule(isIndexing(resp.status) ? ACTIVE_MS : IDLE_MS);
        })
        .catch(() => {
          // A member without index access, or a server without the route:
          // the status line simply stays quiet.
          if (!cancelled) schedule(IDLE_MS * 2);
        });
    };

    tick();
    const onVisible = () => {
      if (!document.hidden) {
        if (timer) clearTimeout(timer);
        tick();
      }
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [libraryId, enabled, announce]);

  return state && state.libraryId === libraryId ? state.status : null;
}

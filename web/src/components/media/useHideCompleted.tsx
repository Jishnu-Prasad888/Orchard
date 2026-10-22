/* eslint-disable react-refresh/only-export-components --
 * The provider and the hooks that read it are one unit.
 */
/**
 * "Hide completed": one checkbox in the top bar that every file listing
 * honours, and "Mark as completed" on every file.
 *
 * The checkbox is the user's choice, kept on the server (so it follows them
 * to another browser), and shared through context so the top bar and the page
 * under it agree. Toggling is optimistic: the listing changes at once, and
 * flips back if the server could not save it.
 *
 * Marking a file is optimistic too, and deliberately does not reload the
 * listing: a reload swaps the whole page for its loading state and drops the
 * pages already scrolled through, so the screen seemed to refresh itself. The
 * change is kept here instead and laid over every listing's `completed`
 * flags, so the one tile changes (or leaves) and nothing else moves.
 *
 * Outside the provider (a page rendered on its own) nothing is hidden and
 * neither the checkbox nor marking does anything.
 */

import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react';

import {
  clearReadingStatus,
  getHideCompleted,
  setHideCompleted as saveHideCompleted,
  setReadingStatus,
} from '../../api/queries';
import type { FileSummary } from '../../api/types';

interface HideCompletedState {
  hideCompleted: boolean;
  setHideCompleted: (hide: boolean) => void;
  /** file id -> completed, for files marked since their listing was fetched. */
  marked: ReadonlyMap<string, boolean>;
  /** Mark files (not) completed. Resolves to how many could not be saved;
   * those are put back as they were. */
  markCompleted: (libraryId: string, files: FileSummary[], value: boolean) => Promise<number>;
}

const NO_MARKS: ReadonlyMap<string, boolean> = new Map();

const HideCompletedContext = createContext<HideCompletedState>({
  hideCompleted: false,
  setHideCompleted: () => {},
  marked: NO_MARKS,
  markCompleted: () => Promise.resolve(0),
});

export function HideCompletedProvider({ children }: { children: ReactNode }) {
  const [hideCompleted, setLocal] = useState(false);
  const [marked, setMarked] = useState<ReadonlyMap<string, boolean>>(NO_MARKS);

  useEffect(() => {
    let cancelled = false;
    getHideCompleted()
      .then((r) => {
        if (!cancelled) setLocal(r.hide_completed);
      })
      // Unreachable server: show everything, which is the safe default.
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  const setHideCompleted = useCallback((hide: boolean) => {
    setLocal(hide);
    saveHideCompleted(hide).catch(() => setLocal(!hide));
  }, []);

  const mark = useCallback((entries: [string, boolean][]) => {
    if (entries.length === 0) return;
    setMarked((prev) => {
      const next = new Map(prev);
      for (const [id, value] of entries) next.set(id, value);
      return next;
    });
  }, []);

  const markCompleted = useCallback(
    async (libraryId: string, files: FileSummary[], value: boolean) => {
      mark(files.map((f) => [f.id, value]));
      const results = await Promise.allSettled(
        files.map((f) =>
          value
            ? setReadingStatus(libraryId, f.id, 'completed')
            : clearReadingStatus(libraryId, f.id),
        ),
      );
      const failed = files.filter((_, i) => results[i]!.status === 'rejected');
      mark(failed.map((f) => [f.id, Boolean(f.completed)]));
      return failed.length;
    },
    [mark],
  );

  const value = useMemo(
    () => ({ hideCompleted, setHideCompleted, marked, markCompleted }),
    [hideCompleted, setHideCompleted, marked, markCompleted],
  );
  return <HideCompletedContext.Provider value={value}>{children}</HideCompletedContext.Provider>;
}

export function useHideCompleted(): HideCompletedState {
  return useContext(HideCompletedContext);
}

/**
 * `files` with the latest marks applied, and without the completed ones while
 * the checkbox is on. The same array comes back when nothing changes, and
 * untouched files keep their identity, so only a marked tile re-renders.
 */
export function useVisibleFiles<T extends FileSummary>(files: T[]): T[] {
  const { hideCompleted, marked } = useHideCompleted();
  return useMemo(() => {
    let out = files;
    if (marked.size > 0 && files.some((f) => isStale(f, marked))) {
      out = files.map((f) => (isStale(f, marked) ? { ...f, completed: marked.get(f.id) } : f));
    }
    return hideCompleted && out.some((f) => f.completed) ? out.filter((f) => !f.completed) : out;
  }, [files, hideCompleted, marked]);
}

function isStale(file: FileSummary, marked: ReadonlyMap<string, boolean>): boolean {
  const value = marked.get(file.id);
  return value !== undefined && value !== Boolean(file.completed);
}

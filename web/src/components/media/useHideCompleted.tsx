/* eslint-disable react-refresh/only-export-components --
 * The provider and the hooks that read it are one unit.
 */
/**
 * "Hide completed": one checkbox in the top bar that every file listing
 * honours.
 *
 * The choice is the user's, kept on the server (so it follows them to another
 * browser), and shared through context so the top bar and the page under it
 * agree. Toggling is optimistic: the listing changes at once, and flips back
 * if the server could not save it.
 *
 * Outside the provider (a page rendered on its own, as in tests) nothing is
 * hidden and the checkbox does nothing.
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

import { getHideCompleted, setHideCompleted as saveHideCompleted } from '../../api/queries';
import type { FileSummary } from '../../api/types';

interface HideCompletedState {
  hideCompleted: boolean;
  setHideCompleted: (hide: boolean) => void;
}

const HideCompletedContext = createContext<HideCompletedState>({
  hideCompleted: false,
  setHideCompleted: () => {},
});

export function HideCompletedProvider({ children }: { children: ReactNode }) {
  const [hideCompleted, setLocal] = useState(false);

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

  const value = useMemo(
    () => ({ hideCompleted, setHideCompleted }),
    [hideCompleted, setHideCompleted],
  );
  return <HideCompletedContext.Provider value={value}>{children}</HideCompletedContext.Provider>;
}

export function useHideCompleted(): HideCompletedState {
  return useContext(HideCompletedContext);
}

/**
 * `files` without the completed ones while the checkbox is on. The same array
 * comes back when nothing is hidden, so callers' memoised work stays put.
 */
export function useVisibleFiles<T extends FileSummary>(files: T[]): T[] {
  const { hideCompleted } = useHideCompleted();
  return useMemo(
    () =>
      hideCompleted && files.some((f) => f.completed) ? files.filter((f) => !f.completed) : files,
    [files, hideCompleted],
  );
}

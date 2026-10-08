/**
 * Loading a library-scoped resource.
 *
 * Most pages are "one library, one list, one error". Writing that effect five
 * times is how the six copies drifted — some swallowed errors, some reset the
 * error on every reload, none of them cancelled in-flight requests properly.
 * This is the one version.
 *
 * The shape here is deliberate. Instead of a `loading` flag that has to be
 * flipped on and off around the request, each result is tagged with the
 * *key* it was fetched for, and `loading`/`data`/`error` are derived during
 * render by comparing keys. That has two benefits: a stale response can never
 * be shown against a new key (the effect that is in flight is cancelled
 * anyway, but the key is what makes it visible in the UI), and there is no
 * synchronous `setState` in the effect body, which is the shape React's own
 * lint rules push you towards.
 */

import { useCallback, useEffect, useRef, useState } from 'react';

import { useLibraryChangeTick } from './libraryEvents';
import { useLibraryGate } from './libraries';

/** The states every page has to handle. */
export interface AsyncResource<T> {
  /** `null` means "not loaded, not loading, or not applicable". */
  data: T | null;
  loading: boolean;
  error: string | null;
  /** Re-run the effect — call after any mutation. */
  reload: () => void;
}

export interface LibraryResource<T> extends AsyncResource<T> {
  /** Empty string when no library is selected. */
  libraryId: string;
}

/** A settled result, tagged with the key it belongs to. */
type Settled<T> =
  { key: string; status: 'ok'; data: T } | { key: string; status: 'error'; message: string } | null;

function toMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * Load `fn` whenever the selected library changes.
 *
 * While the gate is loading, errored, or empty there is nothing to fetch and
 * the result is `null` — never a misleading empty array, which is what made
 * "no libraries" and "no albums" look identical.
 *
 * `deps` are the values the caller wants to re-fetch on beyond the library;
 * the loader itself is read through a ref, so an inline arrow function does not
 * re-trigger the effect on every render. `enabled` lets a page defer the load
 * until it has something to load it for (a selected tag, say).
 */
export function useLibraryResource<T>(
  fn: (libraryId: string) => Promise<T>,
  deps: readonly unknown[] = [],
  enabled = true,
): LibraryResource<T> {
  const gate = useLibraryGate();
  const [settled, setSettled] = useState<Settled<T>>(null);
  const [reloadKey, setReloadKey] = useState(0);
  // The loader is read through a ref so an inline arrow does not re-trigger the
  // effect on every render. The sync effect is declared first so the fetch
  // effect below always sees the current function.
  const fnRef = useRef(fn);
  useEffect(() => {
    fnRef.current = fn;
  });

  const libraryId = gate.kind === 'ready' ? gate.libraryId : null;
  const active = enabled && libraryId !== null;
  // Files changed on disk: fetch again behind the data already on screen.
  const changeTick = useLibraryChangeTick(libraryId);

  // Serialising the inputs gives a cheap, stable identity for "the request this
  // result belongs to". It is a plain string, so comparing it in the effect's
  // dependency list works by value without a memo.
  const key = active ? `${libraryId}|${JSON.stringify(deps)}|${reloadKey}` : null;

  useEffect(() => {
    if (key === null || libraryId === null) return;
    let cancelled = false;
    fnRef
      .current(libraryId)
      .then((data) => {
        if (cancelled) return;
        setSettled({ key, status: 'ok', data });
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        setSettled({ key, status: 'error', message: toMessage(error) });
      });
    return () => {
      cancelled = true;
    };
    // The key encodes every input, so it is the only dependency that matters.
  }, [key, libraryId, changeTick]);

  const current = key !== null && settled?.key === key ? settled : null;

  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  return {
    data: current?.status === 'ok' ? current.data : null,
    loading: key !== null && current === null,
    error: current?.status === 'error' ? current.message : null,
    reload,
    libraryId: libraryId ?? '',
  };
}

/**
 * Load a resource that does not depend on a library (users, backups).
 *
 * `deps` and `enabled` work as in {@link useLibraryResource}; `enabled` lets a
 * page gate the load on a role it has already resolved.
 */
export function useResource<T>(
  fn: () => Promise<T>,
  deps: readonly unknown[] = [],
  enabled = true,
): AsyncResource<T> {
  const [settled, setSettled] = useState<Settled<T>>(null);
  const [reloadKey, setReloadKey] = useState(0);

  const fnRef = useRef(fn);
  useEffect(() => {
    fnRef.current = fn;
  });

  const key = enabled ? `${JSON.stringify(deps)}|${reloadKey}` : null;

  useEffect(() => {
    if (key === null) return;
    let cancelled = false;
    fnRef
      .current()
      .then((data) => {
        if (cancelled) return;
        setSettled({ key, status: 'ok', data });
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        setSettled({ key, status: 'error', message: toMessage(error) });
      });
    return () => {
      cancelled = true;
    };
    // The key encodes every input, so it is the only dependency that matters.
  }, [key]);

  const current = key !== null && settled?.key === key ? settled : null;

  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  return {
    data: current?.status === 'ok' ? current.data : null,
    loading: key !== null && current === null,
    error: current?.status === 'error' ? current.message : null,
    reload,
  };
}

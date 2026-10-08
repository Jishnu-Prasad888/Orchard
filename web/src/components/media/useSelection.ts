/**
 * Multi-selection over an ordered list of files.
 *
 * The rules are the ones people know from photo libraries: a click toggles, a
 * Shift-click extends from the last item clicked (the anchor) to this one, and
 * once anything is selected, Escape clears. Selection state is local to the
 * page that owns the grid; it is not global, and it resets when the list it
 * refers to changes identity (`resetKey`).
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import type { FileSummary } from '../../api/types';

export interface Selection {
  selected: ReadonlySet<string>;
  size: number;
  /** True while at least one item is selected: tiles switch to "click selects". */
  active: boolean;
  isSelected: (id: string) => boolean;
  /** Toggle one item; with `range`, select everything from the anchor to it. */
  toggle: (file: FileSummary, range?: boolean) => void;
  selectAll: () => void;
  clear: () => void;
  /** The selected files, in list order. */
  selectedFiles: FileSummary[];
}

export function useSelection(files: readonly FileSummary[], resetKey: string): Selection {
  const [state, setState] = useState<{ key: string; ids: ReadonlySet<string> }>({
    key: resetKey,
    ids: new Set(),
  });
  const anchorRef = useRef<string | null>(null);

  // A new folder, query, or library means a new list: the old selection points
  // at files that are no longer on screen, so it is dropped.
  const selected = state.key === resetKey ? state.ids : EMPTY;

  const toggle = useCallback(
    (file: FileSummary, range = false) => {
      setState((prev) => {
        const base = prev.key === resetKey ? prev.ids : EMPTY;
        const next = new Set(base);
        const anchor = anchorRef.current;
        if (range && anchor && anchor !== file.id) {
          const a = files.findIndex((f) => f.id === anchor);
          const b = files.findIndex((f) => f.id === file.id);
          if (a !== -1 && b !== -1) {
            const [lo, hi] = a < b ? [a, b] : [b, a];
            for (let i = lo; i <= hi; i++) next.add(files[i]!.id);
            anchorRef.current = file.id;
            return { key: resetKey, ids: next };
          }
        }
        if (next.has(file.id)) {
          next.delete(file.id);
        } else {
          next.add(file.id);
        }
        anchorRef.current = file.id;
        return { key: resetKey, ids: next };
      });
    },
    [files, resetKey],
  );

  const selectAll = useCallback(() => {
    setState({ key: resetKey, ids: new Set(files.map((f) => f.id)) });
  }, [files, resetKey]);

  const clear = useCallback(() => {
    anchorRef.current = null;
    setState({ key: resetKey, ids: new Set() });
  }, [resetKey]);

  const size = selected.size;

  // Escape leaves selection mode — unless a dialog or menu is in charge of
  // the key, in which case it stops propagation before it gets here.
  useEffect(() => {
    if (size === 0) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !event.defaultPrevented) clear();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [size, clear]);

  const selectedFiles = useMemo(
    () => (size === 0 ? [] : files.filter((f) => selected.has(f.id))),
    [files, selected, size],
  );

  const isSelected = useCallback((id: string) => selected.has(id), [selected]);

  return {
    selected,
    size,
    active: size > 0,
    isSelected,
    toggle,
    selectAll,
    clear,
    selectedFiles,
  };
}

const EMPTY: ReadonlySet<string> = new Set();

/**
 * useMemoryDocument — loads a memory and keeps it saved.
 *
 * The local document is the source of truth while editing. Every write goes
 * through one serialized queue (metadata PATCH, then document PUT), each
 * carrying the revision the client last saw, so two tabs or devices can never
 * silently overwrite each other: a stale write comes back 409 and the editor
 * asks which version to keep.
 *
 * Autosave is debounced (text ≈0.8 s, structure ≈0.15 s), never one request
 * per keystroke. Unsaved work is mirrored into localStorage, so a refresh or
 * crash loses nothing: on the next load a draft based on the current server
 * revision is restored silently, and a draft based on an older revision is
 * offered — never applied over newer server content without asking.
 *
 * Structural changes (insert, delete, move, duplicate, image edits) are
 * undoable at document level; typing undo lives in each text block's editor.
 */

import { useCallback, useEffect, useRef, useState } from 'react';

import { ApiError } from '../api/client';
import { getMemoryDocument, patchMemoryMeta, saveMemoryDocument } from './api';
import { mergeServerViews, normalizeBlocks } from './model';
import type { MemoryBlock, MemoryDocument, MemoryMetaPatch } from './types';

export type SaveState = 'saved' | 'saving' | 'unsaved' | 'offline' | 'error' | 'conflict';

export type MemoryMeta = Omit<MemoryDocument, 'blocks'>;

export interface DraftRecord {
  base_revision: number;
  blocks: MemoryBlock[];
  meta: MemoryMetaPatch;
  saved_at: string;
}

export interface UpdateOptions {
  /** Record the change in the document undo history. */
  undoable?: boolean;
  /** Save sooner: structural changes are cheap to send and costly to lose. */
  structural?: boolean;
}

const TEXT_DELAY = 800;
const STRUCTURE_DELAY = 150;
const META_DELAY = 600;
const DRAFT_DELAY = 300;
const OFFLINE_RETRY = 5000;
const UNDO_LIMIT = 60;

const draftKey = (libraryId: string, memoryId: string) =>
  `cairn.memory.draft.${libraryId}.${memoryId}`;

export function readDraft(libraryId: string, memoryId: string): DraftRecord | null {
  try {
    const raw = localStorage.getItem(draftKey(libraryId, memoryId));
    if (!raw) return null;
    const draft = JSON.parse(raw) as DraftRecord;
    return Array.isArray(draft.blocks) && typeof draft.base_revision === 'number' ? draft : null;
  } catch {
    return null;
  }
}

function writeDraft(libraryId: string, memoryId: string, draft: DraftRecord) {
  try {
    localStorage.setItem(draftKey(libraryId, memoryId), JSON.stringify(draft));
  } catch {
    // Storage full or blocked: the server save is still the real persistence.
  }
}

function clearDraft(libraryId: string, memoryId: string) {
  try {
    localStorage.removeItem(draftKey(libraryId, memoryId));
  } catch {
    // ignore
  }
}

function isNetworkError(e: unknown): boolean {
  return e instanceof TypeError || (typeof navigator !== 'undefined' && navigator.onLine === false);
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

export interface MemoryDocumentState {
  loading: boolean;
  loadError: string | null;
  meta: MemoryMeta | null;
  blocks: MemoryBlock[];
  save: SaveState;
  saveError: string | null;
  lastSavedAt: Date | null;
  warnings: string[];
  /** A draft from an older revision found on load, awaiting a decision. */
  recovery: DraftRecord | null;
  /** True once a newer-revision draft was restored silently on load. */
  restoredDraft: boolean;
  canUndo: boolean;
  canRedo: boolean;
  setBlocks: (change: (blocks: MemoryBlock[]) => MemoryBlock[], options?: UpdateOptions) => void;
  setMeta: (patch: MemoryMetaPatch) => void;
  flush: () => void;
  undo: () => void;
  redo: () => void;
  resolveConflict: (keep: 'mine' | 'theirs') => void;
  applyRecovery: () => void;
  discardRecovery: () => void;
  reload: () => void;
}

export function useMemoryDocument(
  libraryId: string,
  memoryId: string,
  options: { autosave: boolean },
): MemoryDocumentState {
  const [loaded, setLoaded] = useState<{ key: string; error: string | null }>({
    key: '',
    error: null,
  });
  const [meta, setMetaState] = useState<MemoryMeta | null>(null);
  const [blocks, setBlocksState] = useState<MemoryBlock[]>([]);
  const [save, setSave] = useState<SaveState>('saved');
  const [saveError, setSaveError] = useState<string | null>(null);
  const [lastSavedAt, setLastSavedAt] = useState<Date | null>(null);
  const [warnings, setWarnings] = useState<string[]>([]);
  const [recovery, setRecovery] = useState<DraftRecord | null>(null);
  const [restoredDraft, setRestoredDraft] = useState(false);
  const [history, setHistory] = useState<{ past: MemoryBlock[][]; future: MemoryBlock[][] }>({
    past: [],
    future: [],
  });
  const [loadNonce, setLoadNonce] = useState(0);
  const loadKey = `${libraryId}/${memoryId}/${loadNonce}`;
  const loading = loaded.key !== loadKey;
  const loadError = loading ? null : loaded.error;

  // Mutable save state shared by timers and the queue.
  const s = useRef({
    blocks: [] as MemoryBlock[],
    meta: null as MemoryMeta | null,
    baseRevision: null as number | null,
    dirtyDoc: false,
    pendingMeta: {} as MemoryMetaPatch,
    inFlight: false,
    conflict: false,
    timer: undefined as ReturnType<typeof setTimeout> | undefined,
    draftTimer: undefined as ReturnType<typeof setTimeout> | undefined,
    retryTimer: undefined as ReturnType<typeof setTimeout> | undefined,
    autosave: options.autosave,
    mounted: true,
  });
  s.current.autosave = options.autosave;

  const persistDraft = useCallback(() => {
    const st = s.current;
    clearTimeout(st.draftTimer);
    st.draftTimer = setTimeout(() => {
      if (st.baseRevision === null) return;
      if (!st.dirtyDoc && Object.keys(st.pendingMeta).length === 0 && !st.inFlight) return;
      writeDraft(libraryId, memoryId, {
        base_revision: st.baseRevision,
        blocks: st.blocks,
        meta: st.pendingMeta,
        saved_at: new Date().toISOString(),
      });
    }, DRAFT_DELAY);
  }, [libraryId, memoryId]);

  const runSave = useCallback(async () => {
    const st = s.current;
    if (st.inFlight || st.conflict || st.baseRevision === null) return;
    clearTimeout(st.timer);
    clearTimeout(st.retryTimer);
    if (!st.dirtyDoc && Object.keys(st.pendingMeta).length === 0) return;
    st.inFlight = true;
    setSave('saving');
    setSaveError(null);
    let patch: MemoryMetaPatch = {};
    let sentDoc = false;
    try {
      for (;;) {
        patch = st.pendingMeta;
        if (Object.keys(patch).length > 0) {
          st.pendingMeta = {};
          const resp = await patchMemoryMeta(libraryId, memoryId, st.baseRevision, patch);
          patch = {};
          st.baseRevision = resp.memory.revision;
          if (st.mounted) {
            setMetaState((m) =>
              m
                ? {
                    ...m,
                    revision: resp.memory.revision,
                    updated_at: resp.memory.updated_at,
                    tags: resp.memory.tags,
                    ...(resp.memory.cover !== undefined ? { cover: resp.memory.cover } : {}),
                  }
                : m,
            );
          }
          continue;
        }
        if (st.dirtyDoc) {
          st.dirtyDoc = false;
          sentDoc = true;
          const resp = await saveMemoryDocument(libraryId, memoryId, st.baseRevision, st.blocks);
          sentDoc = false;
          st.baseRevision = resp.memory.revision;
          const server = normalizeBlocks(resp.memory.blocks);
          st.blocks = mergeServerViews(st.blocks, server);
          if (st.mounted) {
            setBlocksState(st.blocks);
            setWarnings(resp.warnings ?? []);
            setMetaState((m) =>
              m ? { ...m, revision: resp.memory.revision, updated_at: resp.memory.updated_at } : m,
            );
          }
          continue;
        }
        break;
      }
      clearDraft(libraryId, memoryId);
      if (st.mounted) {
        setSave('saved');
        setLastSavedAt(new Date());
      }
    } catch (e) {
      // Put back whatever did not make it, then decide how to recover.
      st.pendingMeta = { ...patch, ...st.pendingMeta };
      if (sentDoc) st.dirtyDoc = true;
      persistDraft();
      if (e instanceof ApiError && e.status === 409 && e.details?.current_revision !== undefined) {
        st.conflict = true;
        if (st.mounted) {
          setSave('conflict');
          setSaveError(e.message);
        }
      } else if (isNetworkError(e)) {
        if (st.mounted) setSave('offline');
        st.retryTimer = setTimeout(() => void runSaveRef.current(), OFFLINE_RETRY);
      } else if (st.mounted) {
        setSave('error');
        setSaveError(message(e));
      }
    } finally {
      st.inFlight = false;
    }
  }, [libraryId, memoryId, persistDraft]);

  const runSaveRef = useRef(runSave);
  runSaveRef.current = runSave;

  const schedule = useCallback(
    (delay: number) => {
      const st = s.current;
      persistDraft();
      if (st.conflict) return;
      if (!st.autosave) {
        setSave('unsaved');
        return;
      }
      if (!st.inFlight) setSave((cur) => (cur === 'offline' ? cur : 'unsaved'));
      clearTimeout(st.timer);
      st.timer = setTimeout(() => void runSaveRef.current(), delay);
    },
    [persistDraft],
  );

  // Load (and reload) the memory.
  useEffect(() => {
    const st = s.current;
    st.mounted = true;
    let cancelled = false;
    getMemoryDocument(libraryId, memoryId)
      .then((resp) => {
        if (cancelled) return;
        const { blocks: serverBlocks, ...rest } = resp.memory;
        const loaded = normalizeBlocks(serverBlocks);
        st.baseRevision = rest.revision;
        st.conflict = false;
        st.dirtyDoc = false;
        st.pendingMeta = {};
        st.blocks = loaded;
        st.meta = rest;

        const draft = readDraft(libraryId, memoryId);
        if (draft && draft.base_revision === rest.revision) {
          // Unsaved work on top of exactly this revision: safe to restore.
          st.blocks = mergeServerViews(normalizeBlocks(draft.blocks), loaded);
          st.pendingMeta = draft.meta ?? {};
          st.dirtyDoc = true;
          setRestoredDraft(true);
          setMetaState(withMeta(rest, draft.meta ?? {}));
          setBlocksState(st.blocks);
          setLoaded({ key: loadKey, error: null });
          schedule(STRUCTURE_DELAY);
          return;
        }
        if (draft) setRecovery(draft);
        setMetaState(rest);
        setBlocksState(loaded);
        setSave('saved');
        setLoaded({ key: loadKey, error: null });
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setLoaded({ key: loadKey, error: message(e) });
      });
    return () => {
      cancelled = true;
    };
  }, [libraryId, memoryId, loadKey, schedule]);

  // Retry when the connection returns; warn before leaving with unsaved work.
  useEffect(() => {
    const st = s.current;
    const onOnline = () => void runSaveRef.current();
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      if (st.dirtyDoc || st.inFlight || Object.keys(st.pendingMeta).length > 0) {
        e.preventDefault();
      }
    };
    window.addEventListener('online', onOnline);
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => {
      window.removeEventListener('online', onOnline);
      window.removeEventListener('beforeunload', onBeforeUnload);
    };
  }, []);

  // Flush on unmount (switching memories): send what is pending.
  useEffect(() => {
    const st = s.current;
    return () => {
      st.mounted = false;
      clearTimeout(st.timer);
      clearTimeout(st.retryTimer);
      clearTimeout(st.draftTimer);
      if (st.dirtyDoc || Object.keys(st.pendingMeta).length > 0) {
        if (st.baseRevision !== null) {
          writeDraft(libraryId, memoryId, {
            base_revision: st.baseRevision,
            blocks: st.blocks,
            meta: st.pendingMeta,
            saved_at: new Date().toISOString(),
          });
        }
        if (st.autosave && !st.conflict) void runSaveRef.current();
      }
    };
  }, [libraryId, memoryId]);

  const setBlocks = useCallback(
    (change: (blocks: MemoryBlock[]) => MemoryBlock[], opts: UpdateOptions = {}) => {
      const st = s.current;
      const before = st.blocks;
      const next = change(before);
      if (next === before) return;
      st.blocks = next;
      st.dirtyDoc = true;
      setBlocksState(next);
      if (opts.undoable) {
        setHistory((h) => ({ past: [...h.past, before].slice(-UNDO_LIMIT), future: [] }));
      }
      schedule(opts.structural || opts.undoable ? STRUCTURE_DELAY : TEXT_DELAY);
    },
    [schedule],
  );

  const setMeta = useCallback(
    (patch: MemoryMetaPatch) => {
      const st = s.current;
      setMetaState((m) => (m ? withMeta(m, patch) : m));
      // A blank title is shown but never sent; the server would reject it.
      const send: MemoryMetaPatch = { ...patch };
      if (send.title !== undefined && send.title.trim() === '') delete send.title;
      if (Object.keys(send).length === 0) return;
      st.pendingMeta = { ...st.pendingMeta, ...send };
      schedule(META_DELAY);
    },
    [schedule],
  );

  const restoreBlocks = useCallback(
    (target: MemoryBlock[]) => {
      const st = s.current;
      st.blocks = target;
      st.dirtyDoc = true;
      setBlocksState(target);
      schedule(STRUCTURE_DELAY);
    },
    [schedule],
  );

  const undo = useCallback(() => {
    setHistory((h) => {
      const prev = h.past[h.past.length - 1];
      if (!prev) return h;
      const current = s.current.blocks;
      restoreBlocks(prev);
      return { past: h.past.slice(0, -1), future: [current, ...h.future] };
    });
  }, [restoreBlocks]);

  const redo = useCallback(() => {
    setHistory((h) => {
      const next = h.future[0];
      if (!next) return h;
      const current = s.current.blocks;
      restoreBlocks(next);
      return { past: [...h.past, current], future: h.future.slice(1) };
    });
  }, [restoreBlocks]);

  const resolveConflict = useCallback(
    (keep: 'mine' | 'theirs') => {
      const st = s.current;
      if (keep === 'theirs') {
        clearDraft(libraryId, memoryId);
        st.conflict = false;
        st.dirtyDoc = false;
        st.pendingMeta = {};
        setHistory({ past: [], future: [] });
        setSave('saved');
        setSaveError(null);
        setLoadNonce((n) => n + 1);
        return;
      }
      // Keep mine: adopt the server's revision as the base and save over it.
      getMemoryDocument(libraryId, memoryId)
        .then((resp) => {
          st.baseRevision = resp.memory.revision;
          st.conflict = false;
          st.dirtyDoc = true;
          const { blocks: _ignored, ...rest } = resp.memory;
          void _ignored;
          // Re-send every local metadata field so the newer server values do
          // not survive underneath "mine".
          const m = st.meta;
          if (m) {
            st.pendingMeta = {
              title: m.title,
              description: m.description,
              location: m.location,
              memory_date: m.memory_date ?? null,
              cover_file_id: m.cover_file_id ?? null,
              tags: m.tags,
              ...st.pendingMeta,
            };
          }
          setMetaState((cur) => (cur ? { ...cur, revision: rest.revision } : cur));
          setSaveError(null);
          void runSaveRef.current();
        })
        .catch((e: unknown) => {
          setSave('error');
          setSaveError(message(e));
        });
    },
    [libraryId, memoryId],
  );

  const applyRecovery = useCallback(() => {
    const draft = recovery;
    if (!draft) return;
    const st = s.current;
    setRecovery(null);
    const restored = mergeServerViews(normalizeBlocks(draft.blocks), st.blocks);
    setHistory((h) => ({ past: [...h.past, st.blocks].slice(-UNDO_LIMIT), future: [] }));
    st.pendingMeta = { ...st.pendingMeta, ...(draft.meta ?? {}) };
    setMetaState((m) => (m ? withMeta(m, draft.meta ?? {}) : m));
    restoreBlocks(restored);
  }, [recovery, restoreBlocks]);

  const discardRecovery = useCallback(() => {
    clearDraft(libraryId, memoryId);
    setRecovery(null);
  }, [libraryId, memoryId]);

  // Keep the ref copy of meta current for conflict resolution.
  useEffect(() => {
    s.current.meta = meta;
  }, [meta]);

  return {
    loading,
    loadError,
    meta,
    blocks,
    save,
    saveError,
    lastSavedAt,
    warnings,
    recovery,
    restoredDraft,
    canUndo: history.past.length > 0,
    canRedo: history.future.length > 0,
    setBlocks,
    setMeta,
    flush: () => void runSaveRef.current(),
    undo,
    redo,
    resolveConflict,
    applyRecovery,
    discardRecovery,
    reload: () => setLoadNonce((n) => n + 1),
  };
}

/** The local view of metadata after applying a PATCH-shaped change. */
export function withMeta(m: MemoryMeta, patch: MemoryMetaPatch): MemoryMeta {
  const next: MemoryMeta = { ...m };
  if (patch.title !== undefined) next.title = patch.title;
  if (patch.description !== undefined) next.description = patch.description;
  if (patch.location !== undefined) next.location = patch.location;
  if (patch.tags !== undefined) next.tags = patch.tags;
  if (patch.memory_date === null) delete next.memory_date;
  else if (patch.memory_date !== undefined) next.memory_date = patch.memory_date;
  if (patch.cover_file_id === null) {
    delete next.cover_file_id;
    delete next.cover;
  } else if (patch.cover_file_id !== undefined && patch.cover_file_id !== m.cover_file_id) {
    next.cover_file_id = patch.cover_file_id;
    delete next.cover; // refreshed from the server after the save
  }
  return next;
}

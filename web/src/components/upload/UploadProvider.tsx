/* eslint-disable react-refresh/only-export-components --
 * The provider and the hook that reads it are one unit.
 */
/**
 * Uploads that keep going while you browse.
 *
 * A page hands files to `enqueue` and is free to unmount; the queue lives in
 * the app shell, runs two uploads at a time, and reports progress to the
 * compact tray in the corner. Each finished upload announces that its
 * library changed, so every open list (Files, Home, search) re-reads itself
 * and the new file appears without a refresh.
 *
 * Each file goes up through {@link resumableUpload}, which rides out dropped
 * connections on its own; while it does, the item carries a `notice` saying
 * what happened, and only failures a person must act on become errors.
 *
 * The Files page registers the folder it is showing as the upload target, so
 * the top bar's Upload button puts files where the person is looking rather
 * than at the library root.
 */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import type { ReactNode } from 'react';

import { notifyLibraryChanged } from '../../api/libraryEvents';
import { joinRelPath } from '../../api/queries';
import { describeUploadError, resumableUpload } from '../../api/resumableUpload';
import type { FileSummary } from '../../api/types';
import { useToast } from '../ui/Toast';

export type UploadStatus = 'queued' | 'uploading' | 'done' | 'error' | 'cancelled';

export interface UploadItem {
  id: number;
  libraryId: string;
  file: File;
  /** Destination path relative to the library root. */
  dest: string;
  status: UploadStatus;
  loaded: number;
  total: number;
  error?: string | undefined;
  /** A hiccup being recovered from ("Connection lost. Retrying in 4 s…"). */
  notice?: string | undefined;
  /** The file as the server indexed it, once uploaded. */
  uploaded?: FileSummary | undefined;
}

/** Where an upload without an explicit folder should go. */
export interface UploadTarget {
  libraryId: string;
  folder: string;
}

export interface UploadsState {
  items: UploadItem[];
  /** Bumped every time an upload finishes successfully. */
  completed: number;
  enqueue: (libraryId: string, files: File[], folder: string) => void;
  cancel: (id: number) => void;
  retry: (id: number) => void;
  /** Remove finished, failed, and cancelled entries from the tray. */
  dismissFinished: () => void;
  /** The folder on screen, if a page showing one has registered it. */
  target: UploadTarget | null;
  setTarget: (target: UploadTarget | null) => void;
}

const UploadsContext = createContext<UploadsState | null>(null);

const CONCURRENCY = 2;

export function UploadProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<UploadItem[]>([]);
  const [completed, setCompleted] = useState(0);
  const [target, setTargetState] = useState<UploadTarget | null>(null);
  const setTarget = useCallback((next: UploadTarget | null) => {
    setTargetState((prev) =>
      prev?.libraryId === next?.libraryId && prev?.folder === next?.folder ? prev : next,
    );
  }, []);
  const nextId = useRef(1);
  const controllers = useRef(new Map<number, AbortController>());
  // Ids already handed to the network, so an effect that runs twice (Strict
  // Mode, or two renders before the status flips) never uploads a file twice.
  const started = useRef(new Set<number>());

  const toast = useToast();

  const patch = useCallback((id: number, change: Partial<UploadItem>) => {
    setItems((list) => list.map((item) => (item.id === id ? { ...item, ...change } : item)));
  }, []);

  const start = useCallback(
    (item: UploadItem) => {
      if (started.current.has(item.id)) return;
      started.current.add(item.id);
      const controller = new AbortController();
      controllers.current.set(item.id, controller);
      patch(item.id, { status: 'uploading', loaded: 0, notice: undefined });
      resumableUpload(item.libraryId, item.file, item.dest, {
        signal: controller.signal,
        onProgress: ({ loaded, total }) => patch(item.id, { loaded, total }),
        onNotice: (notice) => patch(item.id, { notice: notice ?? undefined }),
      })
        .then((file) => {
          patch(item.id, { status: 'done', loaded: item.total, uploaded: file, notice: undefined });
          setCompleted((n) => n + 1);
          notifyLibraryChanged(item.libraryId);
        })
        .catch((error: unknown) => {
          if (error instanceof DOMException && error.name === 'AbortError') {
            patch(item.id, { status: 'cancelled', notice: undefined });
          } else {
            const reason = describeUploadError(error);
            patch(item.id, { status: 'error', error: reason, notice: undefined });
            toast({ tone: 'error', message: `Couldn’t upload ${item.file.name}: ${reason}` });
          }
        })
        .finally(() => controllers.current.delete(item.id));
    },
    [patch, toast],
  );

  // Start queued uploads whenever a slot is free.
  useEffect(() => {
    const running = items.filter((item) => item.status === 'uploading').length;
    const slots = CONCURRENCY - running;
    if (slots <= 0) return;
    items
      .filter((item) => item.status === 'queued')
      .slice(0, slots)
      .forEach(start);
  }, [items, start]);

  const enqueue = useCallback((libraryId: string, files: File[], folder: string) => {
    const added = files.map<UploadItem>((file) => ({
      id: nextId.current++,
      libraryId,
      file,
      dest: joinRelPath(folder, file.name),
      status: 'queued',
      loaded: 0,
      total: file.size,
    }));
    setItems((list) => [...list, ...added]);
  }, []);

  const cancel = useCallback(
    (id: number) => {
      const controller = controllers.current.get(id);
      if (controller) {
        controller.abort();
      } else {
        patch(id, { status: 'cancelled' });
      }
    },
    [patch],
  );

  const retry = useCallback(
    (id: number) => {
      started.current.delete(id);
      patch(id, { status: 'queued', loaded: 0, error: undefined });
    },
    [patch],
  );

  const dismissFinished = useCallback(() => {
    setItems((list) =>
      list.filter((item) => item.status === 'queued' || item.status === 'uploading'),
    );
  }, []);

  // Leaving the page mid-upload would silently drop the rest.
  useEffect(() => {
    const busy = items.some((item) => item.status === 'queued' || item.status === 'uploading');
    if (!busy) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [items]);

  const value = useMemo<UploadsState>(
    () => ({ items, completed, enqueue, cancel, retry, dismissFinished, target, setTarget }),
    [items, completed, enqueue, cancel, retry, dismissFinished, target, setTarget],
  );

  return <UploadsContext.Provider value={value}>{children}</UploadsContext.Provider>;
}

const INERT: UploadsState = {
  items: [],
  completed: 0,
  enqueue: () => {},
  cancel: () => {},
  retry: () => {},
  dismissFinished: () => {},
  target: null,
  setTarget: () => {},
};

/** The upload queue. Outside the shell it is inert. */
export function useUploads(): UploadsState {
  return useContext(UploadsContext) ?? INERT;
}

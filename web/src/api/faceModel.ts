/**
 * The face-recognition model: is it installed, and how far along is a download.
 *
 * The download itself runs on the server and carries on if the browser goes
 * away, so this store only *watches* it: it polls while a download is running
 * and survives the prompt being closed, which is what lets a toast announce
 * the finish after the dialog is gone. Administrators only — others cannot
 * download, and the routes answer 403.
 */

import { useSyncExternalStore } from 'react';

import { getFaceModelStatus, startFaceModelDownload } from './queries';
import type { FaceModelStatus } from './queries';

interface Store {
  status: FaceModelStatus | null;
  /** The explanatory / progress dialog is showing. */
  dialogOpen: boolean;
}

let store: Store = { status: null, dialogOpen: false };
const listeners = new Set<() => void>();
let timer: ReturnType<typeof setTimeout> | undefined;

function set(patch: Partial<Store>) {
  store = { ...store, ...patch };
  listeners.forEach((l) => l());
}

function schedule() {
  clearTimeout(timer);
  if (store.status?.state === 'downloading') timer = setTimeout(() => void refreshFaceModel(), 700);
}

export async function refreshFaceModel(): Promise<FaceModelStatus | null> {
  try {
    const status = await getFaceModelStatus();
    set({ status });
    schedule();
    return status;
  } catch {
    // Not an administrator, or a server without ML: nothing to offer.
    return null;
  }
}

export async function beginFaceModelDownload(): Promise<void> {
  try {
    set({ status: await startFaceModelDownload() });
  } catch (e) {
    set({
      status: {
        installed: false,
        state: 'error',
        downloaded: 0,
        total: 0,
        error: e instanceof Error ? e.message : String(e),
        url: store.status?.url ?? '',
      },
    });
  }
  schedule();
}

export function openFaceModelDialog() {
  set({ dialogOpen: true });
}

export function closeFaceModelDialog() {
  set({ dialogOpen: false });
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function useFaceModel(): Store {
  return useSyncExternalStore(
    subscribe,
    () => store,
    () => store,
  );
}

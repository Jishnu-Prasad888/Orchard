/**
 * Uploads that survive a bad connection.
 *
 * A file goes up in chunks to a session the server keeps on disk (see
 * internal/uploads). When anything goes wrong — Wi-Fi drops, the laptop
 * sleeps, the Pi restarts, the connection silently stalls — this asks the
 * server how much it already has and carries on from there, waiting out
 * offline spells and backing off between attempts. Only failures a person has
 * to act on (a full disk, a name that's taken, no permission) reach them, and
 * then in the server's own words.
 *
 * The session id is remembered per file in localStorage, so after a reload,
 * picking the same file again into the same folder resumes rather than
 * restarts.
 */

import {
  ApiError,
  NetworkError,
  apiDelete,
  apiGet,
  apiPost,
  apiSendWithProgress,
  type UploadProgress,
} from './client';
import type { FileSummary } from './types';

interface UploadSession {
  id: string;
  path: string;
  size: number;
  offset: number;
  chunk_size: number;
  max_chunk_size: number;
  complete: boolean;
  file?: FileSummary;
}

export interface ResumableUploadOptions {
  signal?: AbortSignal;
  onProgress?: (p: UploadProgress) => void;
  /** A recoverable hiccup, as a sentence for the person (null once moving again). */
  onNotice?: (notice: string | null) => void;
}

/** Timings, exposed so tests need not wait in real time. */
export const uploadTiming = {
  /** First retry delay; doubles per consecutive failure. */
  baseDelayMs: 1000,
  maxDelayMs: 30_000,
  /** Abandon a chunk that has sent nothing for this long. */
  stallMs: 45_000,
  /** Consecutive failures (while online, with no progress) before giving up. */
  maxFailures: 8,
};

/** Never send less than this per chunk, however bad the link. */
const MIN_CHUNK = 512 * 1024;

const cancelled = () => new DOMException('Upload cancelled.', 'AbortError');

/** Failures that go away on their own. */
function isTransient(error: unknown): boolean {
  if (error instanceof NetworkError || error instanceof TypeError) return true;
  if (!(error instanceof ApiError)) return false;
  if (
    ['UPLOAD_BUSY', 'UPLOAD_STALLED', 'UPLOAD_INTERRUPTED', 'RATE_LIMITED'].includes(error.code)
  ) {
    return true;
  }
  // Proxies and a restarting server answer 502/503/504, sometimes as HTML.
  return [408, 502, 503, 504].includes(error.status) || error.code === 'INTERNAL';
}

/** Failures worth keeping the session for, so Retry resumes once fixed. */
function isFixableOnServer(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    ['INSUFFICIENT_STORAGE', 'STORAGE_UNWRITABLE', 'STORAGE_ERROR'].includes(error.code)
  );
}

/** What went wrong, for a person. */
export function describeUploadError(error: unknown): string {
  if (typeof navigator !== 'undefined' && navigator.onLine === false) {
    return 'You’re offline.';
  }
  if (error instanceof TypeError) return 'The connection to the server was lost.';
  if (error instanceof ApiError && error.code === 'UNKNOWN') {
    if ([502, 503, 504].includes(error.status)) {
      return 'The server isn’t responding — it may be restarting.';
    }
  }
  if (error instanceof Error && error.message) return error.message;
  return 'The upload failed.';
}

function wait(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(cancelled());
      return;
    }
    const onAbort = () => {
      clearTimeout(timer);
      reject(cancelled());
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}

function waitForOnline(signal?: AbortSignal): Promise<void> {
  if (typeof navigator === 'undefined' || navigator.onLine !== false) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const done = () => {
      window.removeEventListener('online', done);
      signal?.removeEventListener('abort', onAbort);
      resolve();
    };
    const onAbort = () => {
      window.removeEventListener('online', done);
      reject(cancelled());
    };
    window.addEventListener('online', done);
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}

// --- remembering sessions across reloads (best effort) ---

const memoryKey = (libraryId: string, dest: string, file: File) =>
  `orchard.upload:${libraryId}:${file.size}:${file.lastModified}:${dest}`;

function recall(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function remember(key: string, id: string | null) {
  try {
    if (id) localStorage.setItem(key, id);
    else localStorage.removeItem(key);
  } catch {
    // Private mode or storage disabled: resuming after a reload just won't work.
  }
}

/**
 * Upload `file` to `dest` in a library, resuming through any interruption.
 * Resolves with the indexed file; rejects with an error whose message is fit
 * to show, or an `AbortError` if cancelled.
 */
export async function resumableUpload(
  libraryId: string,
  file: File,
  dest: string,
  { signal, onProgress, onNotice }: ResumableUploadOptions = {},
): Promise<FileSummary> {
  const base = `/libraries/${libraryId}/uploads`;
  const key = memoryKey(libraryId, dest, file);
  let session: UploadSession | null = null;
  let needsSync = false;
  let failures = 0;
  let chunk = 0;
  let noticeShown = false;

  const notice = (text: string | null) => {
    if (text === null && !noticeShown) return;
    noticeShown = text !== null;
    onNotice?.(text);
  };
  const report = (sent: number) => onProgress?.({ loaded: sent, total: file.size });

  // A session from before a reload, if the server still has it.
  const earlier = recall(key);
  if (earlier) {
    try {
      const { upload } = await apiGet<{ upload: UploadSession }>(`${base}/${earlier}`);
      if (upload.size === file.size && upload.path === dest) session = upload;
    } catch {
      // Gone or unreachable: start a new one below.
    }
    if (!session) remember(key, null);
  }

  for (;;) {
    if (signal?.aborted) break;
    try {
      let current: UploadSession;
      if (session && needsSync) {
        current = (await apiGet<{ upload: UploadSession }>(`${base}/${session.id}`)).upload;
        needsSync = false;
      } else if (session) {
        current = session;
      } else {
        current = (await apiPost<{ upload: UploadSession }>(base, { path: dest, size: file.size }))
          .upload;
        remember(key, current.id);
      }
      session = current;
      if (current.complete && current.file) {
        remember(key, null);
        report(file.size);
        return current.file;
      }
      chunk ||= current.chunk_size;
      report(current.offset);

      while (current.offset < file.size) {
        const offset = current.offset;
        const end = Math.min(offset + chunk, file.size);
        const response: { upload: UploadSession } = await apiSendWithProgress(
          'PATCH',
          `${base}/${current.id}`,
          file.slice(offset, end),
          {
            headers: {
              'Content-Type': 'application/offset+octet-stream',
              'Upload-Offset': String(offset),
            },
            onProgress: ({ loaded }) => report(offset + loaded),
            signal,
            stallMs: uploadTiming.stallMs,
          },
        );
        current = response.upload;
        session = current;
        failures = 0;
        notice(null);
        // A clean chunk earns back the size a bad patch cost.
        chunk = Math.min(chunk * 2, current.chunk_size);
      }

      const { file: created } = await apiPost<{ file: FileSummary }>(
        `${base}/${current.id}/complete`,
      );
      remember(key, null);
      notice(null);
      return created;
    } catch (error) {
      if (signal?.aborted || (error instanceof DOMException && error.name === 'AbortError')) {
        break;
      }
      const resumeAt = error instanceof ApiError ? error.details?.offset : undefined;
      if (session && typeof resumeAt === 'number') {
        // The server holds a different amount than we thought: carry on from it.
        session = { ...session, offset: resumeAt };
        continue;
      }
      if (error instanceof ApiError && error.code === 'UPLOAD_NOT_FOUND') {
        // The server dropped the session (expired, or its disk was swapped).
        session = null;
        remember(key, null);
        notice('The server lost track of this upload; starting it again.');
        if (++failures > uploadTiming.maxFailures) throw error;
        continue;
      }
      if (!isTransient(error)) {
        if (session && !isFixableOnServer(error)) {
          void apiDelete(`${base}/${session.id}`).catch(() => {});
          remember(key, null);
        }
        throw error instanceof Error
          ? error
          : new Error(describeUploadError(error), { cause: error });
      }

      const offline = typeof navigator !== 'undefined' && navigator.onLine === false;
      const busy = error instanceof ApiError && error.code === 'UPLOAD_BUSY';
      if (!offline && !busy) failures += 1;
      if (failures > uploadTiming.maxFailures) {
        const sent = session?.offset ?? 0;
        throw new Error(
          sent > 0
            ? `${describeUploadError(error)} Press retry to continue from where it stopped.`
            : describeUploadError(error),
          { cause: error },
        );
      }
      // A flaky link does better with smaller chunks: less to resend each time.
      if (!busy) chunk = Math.max(Math.floor(chunk / 2), MIN_CHUNK);
      needsSync = session !== null;

      if (offline) {
        notice('You’re offline — the upload will continue when the connection is back.');
        await waitForOnline(signal);
        continue;
      }
      const delay = busy
        ? 2000
        : Math.min(uploadTiming.baseDelayMs * 2 ** (failures - 1), uploadTiming.maxDelayMs);
      const seconds = Math.max(1, Math.round(delay / 1000));
      notice(
        busy
          ? describeUploadError(error)
          : `${describeUploadError(error)} Retrying in ${seconds} s…`,
      );
      await wait(delay * (0.8 + Math.random() * 0.4), signal);
    }
  }

  // Cancelled: free the server's partial copy.
  if (session) {
    void apiDelete(`${base}/${session.id}`).catch(() => {});
    remember(key, null);
  }
  throw cancelled();
}

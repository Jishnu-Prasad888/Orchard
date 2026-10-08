/**
 * Face passes run in the background on the server. Starting one only queues
 * it, so pages wait for the status to report that nothing is running before
 * they re-read people and faces — re-reading on a timer showed half-finished
 * results (faces found but not yet grouped).
 */

import { getFaceStatus } from './queries';
import type { FaceStatus } from './types';

const POLL_MS = 1000;
const GIVE_UP_MS = 30 * 60 * 1000;

/**
 * Resolve with the final status once no face pass is running. Resolves with
 * null as soon as `signal` aborts, so an unmounted page stops polling.
 */
export async function waitForFacePasses(
  libraryId: string,
  signal?: AbortSignal,
): Promise<FaceStatus | null> {
  const deadline = Date.now() + GIVE_UP_MS;
  // The pass may not have registered yet when this starts; a short grace
  // period avoids returning before it has.
  await new Promise((r) => setTimeout(r, 300));
  while (Date.now() < deadline && !signal?.aborted) {
    let status: FaceStatus;
    try {
      status = await getFaceStatus(libraryId);
    } catch {
      return null;
    }
    if (signal?.aborted) return null;
    if (!status.running) return status;
    await new Promise((r) => setTimeout(r, POLL_MS));
  }
  return null;
}

/**
 * A similarity pass runs in the background on the server. Starting one only
 * queues it, so pages wait for the status to report that nothing is running
 * before they stop showing it as in progress — the same pattern as
 * `waitForFacePasses`, just against the ML status endpoint.
 */

import { getMLStatus } from './queries';
import type { MLStatus } from './types';

const POLL_MS = 1000;
const GIVE_UP_MS = 30 * 60 * 1000;

/**
 * Resolve with the final status once no similarity pass is running. Resolves
 * with null as soon as `signal` aborts, so an unmounted page stops polling.
 */
export async function waitForSimilarityPass(
  libraryId: string,
  signal?: AbortSignal,
): Promise<MLStatus | null> {
  const deadline = Date.now() + GIVE_UP_MS;
  // The pass may not have registered yet when this starts; a short grace
  // period avoids returning before it has.
  await new Promise((r) => setTimeout(r, 300));
  while (Date.now() < deadline && !signal?.aborted) {
    let status: MLStatus;
    try {
      status = await getMLStatus(libraryId);
    } catch {
      return null;
    }
    if (signal?.aborted) return null;
    if (!status.running) return status;
    await new Promise((r) => setTimeout(r, POLL_MS));
  }
  return null;
}

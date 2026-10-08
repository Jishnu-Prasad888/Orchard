/**
 * Whether a person has chosen to show the timeline ruler. Off until asked
 * for, and remembered across Photos, Files, and every album once they turn
 * it on.
 */

const OPEN_KEY = 'cairn.timeline.open';

export function readTimelineOpen(): boolean {
  try {
    return localStorage.getItem(OPEN_KEY) === '1';
  } catch {
    return false;
  }
}

export function storeTimelineOpen(open: boolean): void {
  try {
    if (open) localStorage.setItem(OPEN_KEY, '1');
    else localStorage.removeItem(OPEN_KEY);
  } catch {
    // Not remembered; it just starts closed again next time.
  }
}

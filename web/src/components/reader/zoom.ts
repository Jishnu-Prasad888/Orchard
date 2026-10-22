/**
 * Zooming a reflowable book (EPUB, Markdown, text): steps of its font size,
 * the same setting as the slider in Reading settings, so a zoom is saved and
 * comes back next time. A PDF's pages are fixed and zoom on their own.
 */

import type { ReaderPreferences } from '../../api/types';
import { DEFAULT_READER_PREFERENCES } from '../../api/types';

export const ZOOM_MIN_FONT = 12;
export const ZOOM_MAX_FONT = 32;
const ZOOM_STEP = 2;

/** What a Ctrl/⌘ key does to the text size, or null for other keys. */
export function zoomDirection(key: string): 1 | -1 | 0 | null {
  if (key === '=' || key === '+') return 1;
  if (key === '-' || key === '_') return -1;
  if (key === '0') return 0;
  return null;
}

/** The preferences after one zoom step (0 resets to the default size). */
export function zoomed(preferences: ReaderPreferences, direction: 1 | -1 | 0): ReaderPreferences {
  const font_size =
    direction === 0
      ? DEFAULT_READER_PREFERENCES.font_size
      : Math.min(
          ZOOM_MAX_FONT,
          Math.max(ZOOM_MIN_FONT, preferences.font_size + direction * ZOOM_STEP),
        );
  return { ...preferences, font_size };
}

/** The text size as a percentage of the default, for the toolbar. */
export function zoomPercent(preferences: ReaderPreferences): number {
  return Math.round((preferences.font_size / DEFAULT_READER_PREFERENCES.font_size) * 100);
}

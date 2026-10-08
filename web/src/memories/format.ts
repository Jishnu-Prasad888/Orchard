/**
 * Small presentation helpers shared by the memory components.
 */

import { downloadUrl, thumbnailUrl } from '../components/media';
import type { FileSummary } from '../api/types';
import type { PickedMedia } from './model';

/** Slideshow interval choices offered per section and in Settings. */
export const INTERVAL_CHOICES = [5, 10, 15, 20, 30, 60];

const UNAVAILABLE: Record<string, string> = {
  missing: 'The original is missing from the library.',
  deleted: 'The original is in the trash.',
  forbidden: 'You do not have access to this photo.',
  unknown: 'The original is no longer in this library.',
};

export function unavailableReason(status: string): string {
  return UNAVAILABLE[status] ?? 'The original could not be found.';
}

export function formatMemoryDate(iso?: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
    timeZone: 'UTC',
  });
}

/** A library file as the editor needs it before the next save. */
export function toPicked(libraryId: string, f: FileSummary): PickedMedia {
  return {
    id: f.id,
    name: f.name,
    media_type: f.media_type,
    mime_type: f.mime_type,
    thumbnail_url: thumbnailUrl(libraryId, f),
    original_url: downloadUrl(libraryId, f),
  };
}

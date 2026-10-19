/**
 * Media type presentation and URL builders.
 *
 * One place decides what a media type is called and where its bytes and
 * thumbnails live, so the browser, the viewer, albums, tags, favorites, and
 * the share view all render the same glyph and hit the same endpoint.
 */

import { API_BASE } from '../api/client';
import type { FileSummary } from '../api/types';
import type { IconName } from './ui/Icon';

export const MEDIA_LABEL: Record<string, string> = {
  photo: 'Photo',
  video: 'Video',
  audio: 'Audio',
  document: 'Document',
  other: 'File',
};

/** The media types the API accepts as a `type` filter. */
export const MEDIA_TYPES = ['photo', 'video', 'audio', 'document', 'other'] as const;

export const mediaLabel = (mediaType?: string): string =>
  MEDIA_LABEL[mediaType ?? 'other'] ?? 'File';

/** The original bytes. Streams with Range support, so video seeks without a full download. */
export function downloadUrl(libraryId: string, file: Pick<FileSummary, 'id'>): string {
  return `${API_BASE}/libraries/${libraryId}/files/${file.id}/download`;
}

/**
 * The grid preview for a file. A book with an extracted or uploaded cover
 * uses that; a photo serves its original bytes and lets the browser scale
 * them down (there is no derived photo-thumbnail pipeline yet). Only call
 * this when `hasThumbnail(file)` is true — everything else has no image to
 * show at all.
 */
export function thumbnailUrl(
  libraryId: string,
  file: Pick<FileSummary, 'id' | 'media_type' | 'cover_url' | 'thumbnail_url'>,
): string {
  if (file.media_type === 'document' && file.cover_url) return file.cover_url;
  // A photo's rendered thumbnail is a few dozen KB; the original can be
  // megabytes. Until the server has made one, the tile uses the original.
  if (file.media_type === 'photo' && file.thumbnail_url) return file.thumbnail_url;
  return downloadUrl(libraryId, file);
}

/** Whether a tile has an actual image to show, as opposed to a type glyph:
 * a photo (its rendered thumbnail, or the original until there is one), or a
 * book with a cover. */
export function hasThumbnail(file: Pick<FileSummary, 'media_type' | 'cover_url'>): boolean {
  return file.media_type === 'photo' || (file.media_type === 'document' && !!file.cover_url);
}

/** The file's extension, upper-cased, for a list view's Type column ("JPG"). */
export function fileExtension(name: string): string {
  const dot = name.lastIndexOf('.');
  return dot > 0 && dot < name.length - 1 ? name.slice(dot + 1).toUpperCase() : '';
}

/** Extensions the reader (PDF.js / epub.js / the Markdown-and-text reader)
 * actually knows how to open. A document file outside this set (.docx,
 * .csv, …) still gets the viewer's "no preview, download to open" path. */
const READABLE_EXTENSIONS = new Set(['pdf', 'epub', 'md', 'markdown', 'txt']);

export function isReadableDocument(file: Pick<FileSummary, 'media_type' | 'name'>): boolean {
  if (file.media_type !== 'document') return false;
  const ext = fileExtension(file.name).toLowerCase();
  return READABLE_EXTENSIONS.has(ext);
}

/** The icon that stands for a file's media type, for tiles without a preview. */
export function mediaTypeIcon(mediaType: string | undefined): IconName {
  switch (mediaType) {
    case 'photo':
      return 'photo';
    case 'video':
      return 'video';
    case 'audio':
      return 'audio';
    case 'document':
      return 'document';
    default:
      return 'file';
  }
}

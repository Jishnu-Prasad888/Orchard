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

/** The small JPEG preview the grid uses. Only photos have one. */
export function thumbnailUrl(libraryId: string, file: Pick<FileSummary, 'id'>): string {
  return `${API_BASE}/libraries/${libraryId}/files/${file.id}/thumbnail`;
}

/** The file's extension, upper-cased, for a list view's Type column ("JPG"). */
export function fileExtension(name: string): string {
  const dot = name.lastIndexOf('.');
  return dot > 0 && dot < name.length - 1 ? name.slice(dot + 1).toUpperCase() : '';
}

/** Photo and video get a real preview; everything else falls back to a glyph. */
export function isPreviewable(file: FileSummary): boolean {
  return file.media_type === 'photo' || file.media_type === 'video';
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

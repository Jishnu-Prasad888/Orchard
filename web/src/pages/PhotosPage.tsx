/**
 * Photos — the timeline: every photo in the library, newest first, grouped by
 * day. Folders do not matter here; that is what Files is for.
 */

import MediaPage from './MediaPage';
import type { MediaPageConfig } from './MediaPage';

const CONFIG: MediaPageConfig = {
  title: 'Photos',
  type: 'photo',
  grouping: 'day',
  showTypeFilter: false,
  showTimeline: true,
  emptyTitle: 'No photos yet',
  emptyBody: "Once Cairn finds photos in your library, they'll appear here.",
  emptyIcon: 'photo',
};

export default function PhotosPage() {
  return <MediaPage config={CONFIG} />;
}

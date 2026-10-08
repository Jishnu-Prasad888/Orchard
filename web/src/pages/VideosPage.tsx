/**
 * Videos — every video in the library, newest first, grouped by day.
 */

import MediaPage from './MediaPage';
import type { MediaPageConfig } from './MediaPage';

const CONFIG: MediaPageConfig = {
  title: 'Videos',
  type: 'video',
  grouping: 'day',
  showTypeFilter: false,
  emptyTitle: 'No videos yet',
  emptyBody: "Once Cairn finds videos in your library, they'll appear here.",
  emptyIcon: 'video',
};

export default function VideosPage() {
  return <MediaPage config={CONFIG} />;
}

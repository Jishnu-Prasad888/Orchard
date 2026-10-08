/**
 * Files — the library as it is on disk: folders, every kind of file, grid or
 * list, upload and organize.
 */

import MediaPage from './MediaPage';
import type { MediaPageConfig } from './MediaPage';

const CONFIG: MediaPageConfig = {
  title: 'Files',
  showFolders: true,
  showTimeline: true,
  emptyTitle: 'This folder is empty',
  emptyBody: 'Drop files here or use Upload to add some.',
  emptyIcon: 'folder',
};

export default function FilesPage() {
  return <MediaPage config={CONFIG} />;
}

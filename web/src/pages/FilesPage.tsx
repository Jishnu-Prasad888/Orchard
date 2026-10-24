/**
 * Files — the library's books as they are on disk: folders, PDFs, EPUBs and
 * word-processor documents, grid or list, upload and organize. Other files
 * stay on disk but are not listed here.
 */

import MediaPage from './MediaPage';
import type { MediaPageConfig } from './MediaPage';

const CONFIG: MediaPageConfig = {
  title: 'Files',
  extensions: ['pdf', 'epub', 'doc', 'docx', 'odt'],
  showTypeFilter: false,
  showFolders: true,
  emptyTitle: 'This folder is empty',
  emptyBody: 'Drop files here or use Upload to add some.',
  emptyIcon: 'folder',
};

export default function FilesPage() {
  return <MediaPage config={CONFIG} />;
}

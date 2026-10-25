/**
 * Files — the library's books as they are on disk: folders, PDFs and EPUBs,
 * grid or list, upload and organize. Other files stay on disk but are never
 * indexed (the server skips them), so they don't appear anywhere.
 */

import { BOOK_EXTENSIONS } from '../lib/books';
import MediaPage from './MediaPage';
import type { MediaPageConfig } from './MediaPage';

const CONFIG: MediaPageConfig = {
  title: 'Files',
  extensions: BOOK_EXTENSIONS,
  showTypeFilter: false,
  showFolders: true,
  emptyTitle: 'This folder is empty',
  emptyBody: 'Drop files here or use Upload to add some.',
  emptyIcon: 'folder',
};

export default function FilesPage() {
  return <MediaPage config={CONFIG} />;
}

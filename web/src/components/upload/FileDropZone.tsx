/**
 * Dropping books from the desktop anywhere in the app uploads them: into
 * the folder on screen when a page has registered one (Files does), and
 * otherwise into the root of the current library. Non-books are turned away
 * by the upload queue with a note, as they are from the Upload button.
 */

import type { ReactNode } from 'react';

import { useLibraries } from '../../api/libraries';
import { Icon } from '../ui/Icon';
import { useUploadActions } from './UploadProvider';
import { useFileDrop } from './useFileDrop';

export function FileDropZone({ className, children }: { className: string; children: ReactNode }) {
  const { library } = useLibraries();
  const { enqueue, target } = useUploadActions();
  const canUpload = library !== null && library.status !== 'offline';
  const folder = library && target?.libraryId === library.id ? target.folder : '';
  const drop = useFileDrop((files) => {
    if (library) enqueue(library.id, files, folder);
  }, canUpload);

  return (
    <div
      className={drop.dragging ? `${className} is-dropping` : className}
      data-testid="app-shell"
      {...drop.bind}
    >
      {children}
      {drop.dragging && library && (
        <div className="drop-overlay" aria-hidden="true">
          <div className="drop-overlay-card">
            <Icon name="upload" size={28} />
            <span>Drop to upload to {folder ? folder.split('/').pop() : library.name}</span>
          </div>
        </div>
      )}
    </div>
  );
}

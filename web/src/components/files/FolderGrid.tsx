/**
 * The sub-folders of the current folder, as compact tiles above its files.
 * Each is a drop target for moving files in.
 */

import { useState } from 'react';

import type { Folder } from '../../api/types';
import { Icon } from '../ui/Icon';
import './Files.css';

const numberFormat = new Intl.NumberFormat();

export function FolderGrid({
  folders,
  onOpen,
  onDropFile,
}: {
  folders: Folder[];
  onOpen: (folder: Folder) => void;
  onDropFile?: (event: React.DragEvent, folderPath: string) => void;
}) {
  const [over, setOver] = useState<string | null>(null);
  if (folders.length === 0) return null;

  return (
    <ul className="folder-grid" aria-label="Folders" data-testid="folder-grid">
      {folders.map((folder) => (
        <li key={folder.id}>
          <button
            type="button"
            className={over === folder.rel_path ? 'folder-tile is-drop-target' : 'folder-tile'}
            onClick={() => onOpen(folder)}
            onDragOver={(event) => {
              if (!onDropFile || !event.dataTransfer.types.includes('application/cairn-file'))
                return;
              event.preventDefault();
              setOver(folder.rel_path);
            }}
            onDragLeave={() => setOver(null)}
            onDrop={(event) => {
              setOver(null);
              onDropFile?.(event, folder.rel_path);
            }}
          >
            <Icon name="folder" size={22} className="folder-tile-icon" />
            <span className="folder-tile-text">
              <span className="folder-tile-name">{folder.name}</span>
              <span className="folder-tile-count">
                {numberFormat.format(folder.file_count)}{' '}
                {folder.file_count === 1 ? 'item' : 'items'}
              </span>
            </span>
          </button>
        </li>
      ))}
    </ul>
  );
}

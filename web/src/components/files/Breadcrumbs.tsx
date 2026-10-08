/**
 * Where you are in the library's folders, and the way back up.
 *
 * Every crumb is also a drop target: drag a photo onto "All files" or a parent
 * folder to move it there.
 */

import { useState } from 'react';

import { Icon } from '../ui/Icon';
import './Files.css';

function crumbSegments(folderPath: string): Array<{ label: string; path: string }> {
  const parts = folderPath.split('/').filter(Boolean);
  const crumbs: Array<{ label: string; path: string }> = [];
  let acc = '';
  for (const part of parts) {
    acc = acc ? `${acc}/${part}` : part;
    crumbs.push({ label: part, path: acc });
  }
  return crumbs;
}

export function Breadcrumbs({
  folderPath,
  onNavigate,
  onDropFile,
}: {
  folderPath: string;
  onNavigate: (path: string) => void;
  onDropFile?: (event: React.DragEvent, folderPath: string) => void;
}) {
  const [over, setOver] = useState<string | null>(null);
  const crumbs = [{ label: 'All files', path: '' }, ...crumbSegments(folderPath)];

  return (
    <nav className="breadcrumbs" aria-label="Folders">
      <ol>
        {crumbs.map((crumb, i) => {
          const current = i === crumbs.length - 1;
          return (
            <li key={crumb.path || 'root'}>
              {i > 0 && <Icon name="chevron-right" size={16} className="crumb-sep" />}
              <button
                type="button"
                className={over === crumb.path ? 'crumb is-drop-target' : 'crumb'}
                aria-current={current ? 'page' : undefined}
                onClick={() => onNavigate(crumb.path)}
                onDragOver={(event) => {
                  if (!onDropFile || !event.dataTransfer.types.includes('application/cairn-file'))
                    return;
                  event.preventDefault();
                  setOver(crumb.path);
                }}
                onDragLeave={() => setOver(null)}
                onDrop={(event) => {
                  setOver(null);
                  onDropFile?.(event, crumb.path);
                }}
              >
                {i === 0 && <Icon name="folder" size={18} />}
                {crumb.label}
              </button>
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

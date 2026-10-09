/**
 * The list view: name, type, size, modified, and (for a search) location.
 *
 * A file manager's table, kept light — no zebra stripes or heavy rules, a
 * real thumbnail where there is one, sortable headers, a checkbox per row for
 * selection, and a "more" button that opens the same menu as a right-click.
 * Columns fold away on narrow screens, name first to stay.
 */

import type { FileSort, SortOrder } from '../../api/queries';
import type { FileSummary, Folder } from '../../api/types';
import { formatBytes } from '../../api/types';
import { formatDate } from '../../lib/dates';
import { fileExtension, mediaLabel, thumbnailUrl, mediaTypeIcon } from '../media';
import type { Selection } from '../media/useSelection';
import { Icon } from '../ui/Icon';
import './Files.css';

interface FileTableProps {
  libraryId: string;
  files: FileSummary[];
  folders?: Folder[];
  onOpen: (file: FileSummary) => void;
  onOpenFolder?: (folder: Folder) => void;
  selection: Selection;
  /** Show where each file lives — useful for search results, noise in a folder. */
  showLocation?: boolean;
  sort?: { sort: FileSort; order: SortOrder } | undefined;
  onSort?: ((sort: FileSort, order: SortOrder) => void) | undefined;
  onMenu?: (file: FileSummary, x: number, y: number) => void;
}

const COLUMNS: Array<{ id: FileSort | null; label: string; className: string }> = [
  { id: 'name', label: 'Name', className: 'col-name' },
  { id: 'media_type', label: 'Type', className: 'col-type' },
  { id: 'size', label: 'Size', className: 'col-size' },
  { id: 'mod_time', label: 'Modified', className: 'col-date' },
];

export function FileTable({
  libraryId,
  files,
  folders = [],
  onOpen,
  onOpenFolder,
  selection,
  showLocation = false,
  sort,
  onSort,
  onMenu,
}: FileTableProps) {
  const allSelected = files.length > 0 && selection.size === files.length;

  return (
    <table className="file-table" data-testid="file-list">
      <caption className="visually-hidden">Files, with type, size, and modified date</caption>
      <thead>
        <tr>
          <th scope="col" className="col-check">
            <input
              type="checkbox"
              aria-label={allSelected ? 'Deselect all' : 'Select all'}
              checked={allSelected}
              ref={(el) => {
                if (el) el.indeterminate = selection.size > 0 && !allSelected;
              }}
              onChange={() => (allSelected ? selection.clear() : selection.selectAll())}
            />
          </th>
          {COLUMNS.map((col) => {
            const active = sort && col.id === sort.sort;
            return (
              <th
                key={col.label}
                scope="col"
                className={col.className}
                aria-sort={active ? (sort.order === 'asc' ? 'ascending' : 'descending') : undefined}
              >
                {onSort && col.id ? (
                  <button
                    type="button"
                    className="col-sort"
                    onClick={() => onSort(col.id!, active && sort.order === 'asc' ? 'desc' : 'asc')}
                  >
                    {col.label}
                    {active && (
                      <Icon
                        name="chevron-down"
                        size={14}
                        className={sort.order === 'asc' ? 'is-flipped' : undefined}
                      />
                    )}
                  </button>
                ) : (
                  col.label
                )}
              </th>
            );
          })}
          {showLocation && (
            <th scope="col" className="col-location">
              Location
            </th>
          )}
          <th scope="col" className="col-actions">
            <span className="visually-hidden">Actions</span>
          </th>
        </tr>
      </thead>
      <tbody>
        {folders.map((folder) => (
          <tr key={`folder:${folder.id}`} className="file-row is-folder">
            <td className="col-check" />
            <th scope="row" className="col-name">
              <button
                type="button"
                className="file-row-main"
                onClick={() => onOpenFolder?.(folder)}
              >
                <span className="file-row-thumb is-folder" aria-hidden="true">
                  <Icon name="folder" size={20} />
                </span>
                <span className="file-row-name">{folder.name}</span>
              </button>
            </th>
            <td className="col-type">Folder</td>
            <td className="col-size">
              {folder.file_count} {folder.file_count === 1 ? 'item' : 'items'}
            </td>
            <td className="col-date" />
            {showLocation && <td className="col-location" />}
            <td className="col-actions" />
          </tr>
        ))}
        {files.map((file) => {
          const selected = selection.isSelected(file.id);
          return (
            <tr
              key={file.id}
              className={selected ? 'file-row is-selected' : 'file-row'}
              onContextMenu={(event) => {
                if (!onMenu) return;
                event.preventDefault();
                onMenu(file, event.clientX, event.clientY);
              }}
            >
              <td className="col-check">
                <input
                  type="checkbox"
                  checked={selected}
                  onChange={(event) =>
                    selection.toggle(file, (event.nativeEvent as MouseEvent).shiftKey)
                  }
                  aria-label={`Select ${file.name}`}
                />
              </td>
              <th scope="row" className="col-name">
                <button
                  type="button"
                  className="file-row-main"
                  onClick={(event) => {
                    if (event.shiftKey) return selection.toggle(file, true);
                    if (event.metaKey || event.ctrlKey || selection.active) {
                      return selection.toggle(file);
                    }
                    onOpen(file);
                  }}
                  title={file.rel_path}
                >
                  {file.media_type === 'photo' ? (
                    <img
                      className="file-row-thumb"
                      src={thumbnailUrl(libraryId, file)}
                      alt=""
                      loading="lazy"
                      decoding="async"
                    />
                  ) : (
                    <span className="file-row-thumb" aria-hidden="true">
                      <Icon name={mediaTypeIcon(file.media_type)} size={20} />
                    </span>
                  )}
                  <span className="file-row-name">{file.name}</span>
                </button>
              </th>
              <td className="col-type">
                {fileExtension(file.name) || mediaLabel(file.media_type)}
              </td>
              <td className="col-size">{formatBytes(file.size_bytes)}</td>
              <td className="col-date">
                <time dateTime={file.mod_time}>{formatDate(file.mod_time)}</time>
              </td>
              {showLocation && (
                <td className="col-location" title={file.folder_path || 'All files'}>
                  {file.folder_path || 'All files'}
                </td>
              )}
              <td className="col-actions">
                {onMenu && (
                  <button
                    type="button"
                    className="icon-button"
                    aria-label={`More actions for ${file.name}`}
                    aria-haspopup="menu"
                    onClick={(event) => {
                      const rect = event.currentTarget.getBoundingClientRect();
                      onMenu(file, rect.right - 200, rect.bottom + 4);
                    }}
                  >
                    <Icon name="more" />
                  </button>
                )}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

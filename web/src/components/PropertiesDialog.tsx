/**
 * Properties — everything about one file in a single dialog, opened from a
 * tile's right-click menu.
 *
 * Every file gets its facts (name, type, size, when it changed, where it
 * lives). A book also gets what makes it a book: its cover, which can be
 * replaced here, its reading status, and its bibliographic details.
 */

import { Link } from 'react-router-dom';

import type { FileSummary } from '../api/types';
import { formatBytes } from '../api/types';
import { formatDateTime } from '../lib/dates';
import { Dialog } from './Dialog';
import { fileExtension, mediaLabel } from './media';
import { BookPanel } from './viewer/BookPanel';
import './ViewerModal.css';
import './PropertiesDialog.css';

export interface PropertiesDialogProps {
  libraryId: string;
  /** The file to describe, or null while the dialog is closed. */
  file: FileSummary | null;
  onClose: () => void;
  /** Something a listing shows (the cover, the title) was changed. */
  onChanged: () => void;
}

export function PropertiesDialog({ libraryId, file, onClose, onChanged }: PropertiesDialogProps) {
  if (!file) return null;
  const isBook = file.media_type === 'document';
  const extension = fileExtension(file.name);

  return (
    <Dialog open title="Properties" size="medium" onClose={onClose} testId="properties-dialog">
      <div className="properties">
        <dl className="properties-facts" data-testid="properties-facts">
          <div>
            <dt>Name</dt>
            <dd>{file.name}</dd>
          </div>
          <div>
            <dt>Type</dt>
            <dd>
              {mediaLabel(file.media_type)}
              {extension && ` (${extension})`}
            </dd>
          </div>
          <div>
            <dt>Size</dt>
            <dd>{formatBytes(file.size_bytes)}</dd>
          </div>
          <div>
            <dt>Modified</dt>
            <dd>{formatDateTime(file.mod_time)}</dd>
          </div>
          <div>
            <dt>Folder</dt>
            <dd>
              <Link
                to={
                  file.folder_path
                    ? `/files?folder=${encodeURIComponent(file.folder_path)}`
                    : '/files'
                }
                onClick={onClose}
              >
                {file.folder_path || 'All files'}
              </Link>
            </dd>
          </div>
        </dl>

        {isBook && (
          <section className="properties-book" aria-label="Book details">
            <h3 className="properties-heading">Book details</h3>
            <BookPanel key={file.id} libraryId={libraryId} file={file} onChanged={onChanged} />
          </section>
        )}
      </div>
    </Dialog>
  );
}

/**
 * Duplicates — present files that share exact byte-for-byte content with
 * another present file in the same library, typically the same book
 * imported twice under different names. The indexer already computes a
 * content hash for every file; this is purely a view over it, with a
 * "Move to trash" action per copy so cleaning up reuses the existing
 * trash/restore flow rather than a bespoke delete path.
 */

import { useCallback } from 'react';

import { listDuplicates } from '../api/queries';
import type { DuplicateGroup, Library } from '../api/types';
import { formatBytes } from '../api/types';
import { useLibraryResource } from '../api/resources';
import { useFileOperations } from '../components/FileOperations';
import { LibraryGatePage } from '../components/LibraryGatePage';
import { EmptyState, ErrorState, LibraryOfflineNotice, LoadingState, PageHeader } from '../components/States';
import { Icon } from '../components/ui/Icon';
import { ViewerModal } from '../components/ViewerModal';
import './DuplicatesPage.css';

export default function DuplicatesPage() {
  return (
    <LibraryGatePage title="Duplicates">
      {(library) => <Duplicates library={library} />}
    </LibraryGatePage>
  );
}

function Duplicates({ library }: { library: Library }) {
  const libraryId = library.id;
  const groups = useLibraryResource(
    useCallback(async (id: string) => (await listDuplicates(id)).groups ?? [], []),
  );
  const ops = useFileOperations(libraryId, groups.reload);
  const data: DuplicateGroup[] = groups.data ?? [];
  const totalFiles = data.reduce((n, g) => n + g.files.length, 0);

  return (
    <main className="page page-narrow duplicates-page">
      <PageHeader
        title="Duplicates"
        subtitle={
          data.length > 0
            ? `${data.length} ${data.length === 1 ? 'group' : 'groups'}, ${totalFiles} files`
            : undefined
        }
      />

      {library.status === 'offline' && <LibraryOfflineNotice library={library} />}
      {groups.error && (
        <ErrorState message={groups.error} onRetry={groups.reload} title="Couldn't check for duplicates" />
      )}
      {groups.loading && <LoadingState label="Checking for duplicates…" />}

      {groups.data !== null && data.length === 0 && (
        <EmptyState title="No duplicates found" testId="duplicates-empty" icon="check">
          <p>Every file's content is unique — nothing here shares bytes with another file.</p>
        </EmptyState>
      )}

      {data.length > 0 && (
        <ul className="duplicate-groups" data-testid="duplicate-groups">
          {data.map((group) => (
            <li key={group.content_hash} className="duplicate-group" data-testid="duplicate-group">
              <p className="duplicate-group-head">
                {group.files.length} copies · {formatBytes(group.files[0]?.size_bytes ?? 0)} each
              </p>
              <ul className="duplicate-group-files">
                {group.files.map((file) => (
                  <li key={file.id} className="duplicate-file-row">
                    <button
                      type="button"
                      className="duplicate-file-open"
                      onClick={() => ops.openViewer(file)}
                    >
                      <Icon name="document" size={16} />
                      <span className="duplicate-file-path">
                        {file.folder_path ? `${file.folder_path}/` : ''}
                        {file.name}
                      </span>
                    </button>
                    <button
                      type="button"
                      className="button ghost-button danger-button"
                      onClick={() => ops.requestAction('trash', file)}
                      data-testid={`duplicate-trash-${file.id}`}
                    >
                      Move to trash
                    </button>
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
      )}

      {ops.viewer && (
        <ViewerModal
          libraryId={libraryId}
          file={ops.viewer}
          siblings={data.flatMap((g) => g.files)}
          onNavigate={ops.openViewer}
          onChanged={groups.reload}
          onRequestAction={ops.requestAction}
          onClose={ops.closeViewer}
        />
      )}
      {ops.dialogs}
    </main>
  );
}

/**
 * A dialog for adding library files to a tag or collection from the page that
 * shows it: search the library, tick what to add, confirm. Files already there
 * show ticked and locked, so the list doubles as a view of what is in it.
 *
 * The list is sorted newest first or A–Z. With nothing typed it is the
 * library itself in that order (asked of the server, so the first page is the
 * right one); with a search, the matches are put in that order.
 */

import { useEffect, useState } from 'react';

import { listFiles, searchFiles } from '../api/queries';
import type { FileSummary } from '../api/types';
import { Dialog } from './Dialog';
import './AddFilesDialog.css';

/** How long typing has to pause before the library is searched. */
const SEARCH_DELAY_MS = 250;
const LIMIT = 50;

type SortOrder = 'newest' | 'name';

const displayName = (file: FileSummary) => file.title || file.name;

function sorted(files: FileSummary[], order: SortOrder): FileSummary[] {
  const out = files.slice();
  if (order === 'newest') {
    out.sort((a, b) => b.mod_time.localeCompare(a.mod_time));
  } else {
    out.sort((a, b) =>
      displayName(a).localeCompare(displayName(b), undefined, {
        sensitivity: 'base',
        numeric: true,
      }),
    );
  }
  return out;
}

export function AddFilesDialog({
  open,
  libraryId,
  title,
  existingIds,
  onAdd,
  onClose,
  testId,
}: {
  open: boolean;
  libraryId: string;
  title: string;
  /** Files already in the tag or collection. */
  existingIds: ReadonlySet<string>;
  /** Adds the picked files; a rejection is shown in the dialog. */
  onAdd: (files: FileSummary[]) => Promise<unknown>;
  onClose: () => void;
  testId?: string;
}) {
  if (!open) return null;
  return (
    <AddFilesForm
      libraryId={libraryId}
      title={title}
      existingIds={existingIds}
      onAdd={onAdd}
      onClose={onClose}
      testId={testId}
    />
  );
}

function AddFilesForm({
  libraryId,
  title,
  existingIds,
  onAdd,
  onClose,
  testId,
}: {
  libraryId: string;
  title: string;
  existingIds: ReadonlySet<string>;
  onAdd: (files: FileSummary[]) => Promise<unknown>;
  onClose: () => void;
  testId?: string | undefined;
}) {
  const [query, setQuery] = useState('');
  const [order, setOrder] = useState<SortOrder>('newest');
  const [results, setResults] = useState<FileSummary[] | null>(null);
  const [searchError, setSearchError] = useState<string | null>(null);
  const [picked, setPicked] = useState<Map<string, FileSummary>>(() => new Map());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const q = query.trim();
    const timer = window.setTimeout(
      () => {
        const request = q
          ? searchFiles(libraryId, { q, limit: LIMIT })
          : listFiles(libraryId, {
              recursive: true,
              sort: order === 'newest' ? 'mod_time' : 'name',
              order: order === 'newest' ? 'desc' : 'asc',
              limit: LIMIT,
            });
        request
          .then((r) => {
            if (cancelled) return;
            setResults(sorted(r.files ?? [], order));
            setSearchError(null);
          })
          .catch((e: unknown) => {
            if (!cancelled) setSearchError(e instanceof Error ? e.message : String(e));
          });
      },
      q ? SEARCH_DELAY_MS : 0,
    );
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [libraryId, query, order]);

  const toggle = (file: FileSummary) =>
    setPicked((current) => {
      const next = new Map(current);
      if (next.has(file.id)) next.delete(file.id);
      else next.set(file.id, file);
      return next;
    });

  const confirm = () => {
    setBusy(true);
    setError(null);
    onAdd([...picked.values()])
      .then(onClose)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };

  return (
    <Dialog
      open
      title={title}
      onClose={onClose}
      dismissible={!busy}
      size="medium"
      testId={testId}
      footer={
        <>
          <button type="button" className="button" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="button primary-button"
            onClick={confirm}
            disabled={busy || picked.size === 0}
            data-testid="add-files-confirm"
          >
            {busy ? 'Adding…' : picked.size > 0 ? `Add ${picked.size}` : 'Add'}
          </button>
        </>
      }
    >
      <div className="add-files-controls">
        <input
          type="search"
          className="dialog-input add-files-search"
          placeholder="Search the library…"
          aria-label="Search the library"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <div className="add-files-sort" role="radiogroup" aria-label="Sort">
          {(
            [
              ['newest', 'Newest'],
              ['name', 'A–Z'],
            ] as const
          ).map(([value, label]) => (
            <button
              key={value}
              type="button"
              role="radio"
              aria-checked={order === value}
              className={
                order === value ? 'add-files-sort-option is-active' : 'add-files-sort-option'
              }
              title={value === 'newest' ? 'Newest to oldest' : 'By name, A to Z'}
              onClick={() => setOrder(value)}
              data-testid={`add-files-sort-${value}`}
            >
              {label}
            </button>
          ))}
        </div>
      </div>

      {searchError && (
        <p className="error-text" role="alert">
          {searchError}
        </p>
      )}
      {results === null && !searchError && <p className="muted">Searching…</p>}
      {results !== null && results.length === 0 && (
        <p className="muted">{query.trim() ? `Nothing matches “${query.trim()}”.` : 'No files.'}</p>
      )}
      {results !== null && results.length > 0 && (
        <ul className="add-files-list" aria-label="Files">
          {results.map((file) => {
            const already = existingIds.has(file.id);
            return (
              <li key={file.id}>
                <label className={already ? 'add-files-row is-added' : 'add-files-row'}>
                  <input
                    type="checkbox"
                    checked={already || picked.has(file.id)}
                    disabled={already || busy}
                    onChange={() => toggle(file)}
                  />
                  <span className="add-files-text">
                    <span className="add-files-name">{displayName(file)}</span>
                    <span className="add-files-path">
                      {already ? 'Already added' : file.folder_path || '/'}
                    </span>
                  </span>
                </label>
              </li>
            );
          })}
        </ul>
      )}

      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}
    </Dialog>
  );
}

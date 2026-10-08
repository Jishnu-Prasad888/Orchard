/**
 * Adding files to an album: browse the library's folders or search it, tick
 * what belongs, and add them in one go. Files already in the album are
 * marked rather than hidden, so the picker never looks like files vanished.
 */

import { useCallback, useEffect, useState } from 'react';

import { addAlbumFile, listFiles, listFolders, searchFiles } from '../../api/queries';
import type { Album, FileSummary, Folder } from '../../api/types';
import { Dialog } from '../Dialog';
import { Breadcrumbs } from '../files/Breadcrumbs';
import { thumbnailUrl, mediaTypeIcon } from '../media';
import { VideoThumb } from '../media/VideoThumb';
import { Icon } from '../ui/Icon';
import './AddFilesDialog.css';

/** Adding files to an album: browse folders or search, tick, confirm. */
export function AddFilesDialog({
  libraryId,
  album,
  alreadyIn,
  onClose,
  onAdded,
}: {
  libraryId: string;
  album: Album;
  alreadyIn: string[];
  onClose: () => void;
  onAdded: () => void;
}) {
  // Tabs: 'browse' = folder tree, 'search' = free-text search
  const [tab, setTab] = useState<'browse' | 'search'>('browse');

  // ---- browse state ----
  const [folderPath, setFolderPath] = useState('');
  const [folders, setFolders] = useState<Folder[]>([]);
  const [browseFiles, setBrowseFiles] = useState<FileSummary[]>([]);
  // Loading is derived from which folder the shown listing belongs to, so the
  // effect below never has to set it synchronously.
  const [loadedKey, setLoadedKey] = useState<string | null>(null);
  const browseKey = `${libraryId}|${folderPath}`;
  const browseLoading = tab === 'browse' && loadedKey !== browseKey;

  // ---- search state ----
  const [query, setQuery] = useState('');
  const [searchResults, setSearchResults] = useState<FileSummary[]>([]);

  // ---- shared state ----
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Load folders + files for the current folder path.
  useEffect(() => {
    if (tab !== 'browse') return;
    let cancelled = false;
    void Promise.all([
      listFolders(libraryId, folderPath || undefined),
      listFiles(libraryId, { folder: folderPath, limit: 100 }),
    ])
      .then(([dirs, listing]) => {
        if (cancelled) return;
        setFolders(dirs.folders ?? []);
        setBrowseFiles(listing.files ?? []);
        setLoadedKey(browseKey);
        setError(null);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setLoadedKey(browseKey);
        setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [libraryId, folderPath, tab, browseKey]);

  const runSearch = useCallback(
    async (q: string) => {
      setError(null);
      try {
        const resp = await searchFiles(libraryId, { q, limit: 50 });
        setSearchResults(resp.files ?? []);
      } catch (e: unknown) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [libraryId],
  );

  const toggle = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const submit = () => {
    setBusy(true);
    setError(null);
    Promise.all([...selected].map((id) => addAlbumFile(libraryId, album.id, id)))
      .then(() => {
        onAdded();
        onClose();
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };

  const displayFiles = tab === 'browse' ? browseFiles : searchResults;

  return (
    <Dialog
      open
      size="medium"
      title={`Add files to "${album.name}"`}
      onClose={onClose}
      dismissible={!busy}
      testId="add-files-dialog"
      footer={
        <>
          <button type="button" className="button" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="button primary-button"
            onClick={submit}
            disabled={busy || selected.size === 0}
            data-testid="confirm-add-files"
          >
            {busy ? 'Adding…' : `Add ${selected.size > 0 ? selected.size : ''}`.trim()}
          </button>
        </>
      }
    >
      <div className="add-files">
        {/* Tab switcher */}
        <div className="segmented add-files-tabs" role="tablist">
          <button
            type="button"
            role="tab"
            aria-selected={tab === 'browse'}
            className={tab === 'browse' ? 'segmented-item active' : 'segmented-item'}
            onClick={() => setTab('browse')}
          >
            Browse folders
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === 'search'}
            className={tab === 'search' ? 'segmented-item active' : 'segmented-item'}
            onClick={() => setTab('search')}
          >
            Search
          </button>
        </div>

        {tab === 'browse' && (
          <>
            <Breadcrumbs folderPath={folderPath} onNavigate={setFolderPath} />

            {browseLoading && <p className="muted">Loading…</p>}

            {/* Subfolders */}
            {folders.length > 0 && (
              <ul className="add-files-folders">
                {folders.map((folder) => (
                  <li key={folder.id}>
                    <button
                      type="button"
                      className="add-files-folder"
                      onClick={() => setFolderPath(folder.rel_path)}
                    >
                      <Icon name="folder" size={18} />
                      <span className="add-files-folder-name">{folder.name}</span>
                      <span className="muted">{folder.file_count}</span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}

        {tab === 'search' && (
          <div className="add-files-search-row">
            <label className="visually-hidden" htmlFor="add-files-search">
              Search library files
            </label>
            <input
              id="add-files-search"
              className="search-input"
              type="search"
              placeholder="Search library files…"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                if (e.target.value.trim() === '') setSearchResults([]);
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && query.trim() !== '') void runSearch(query.trim());
              }}
              data-testid="add-files-search"
            />
            <button
              type="button"
              className="button"
              onClick={() => void runSearch(query.trim())}
              disabled={query.trim() === ''}
            >
              Search
            </button>
          </div>
        )}

        {/* Files list */}
        {displayFiles.length > 0 && (
          <ul className="picker-results">
            {displayFiles.map((f) => {
              const inAlbum = alreadyIn.includes(f.id);
              return (
                <li key={f.id} className="picker-row">
                  <label className="picker-label">
                    <input
                      type="checkbox"
                      aria-label={`Select ${f.name}`}
                      checked={selected.has(f.id)}
                      disabled={inAlbum}
                      onChange={() => toggle(f.id)}
                    />
                    {f.media_type === 'video' ? (
                      <VideoThumb libraryId={libraryId} fileId={f.id} className="picker-thumb" />
                    ) : f.media_type === 'photo' ? (
                      <img className="picker-thumb" src={thumbnailUrl(libraryId, f)} alt="" />
                    ) : (
                      <span className="picker-thumb picker-glyph" aria-hidden="true">
                        <Icon name={mediaTypeIcon(f.media_type)} size={18} />
                      </span>
                    )}
                    <span className="picker-name" title={f.rel_path}>
                      {f.name}
                    </span>
                  </label>
                  {inAlbum && <span className="muted picker-note">in album</span>}
                </li>
              );
            })}
          </ul>
        )}

        {tab === 'browse' && !browseLoading && folders.length === 0 && browseFiles.length === 0 && (
          <p className="muted">This folder is empty.</p>
        )}

        {tab === 'search' && query.trim() !== '' && searchResults.length === 0 && (
          <p className="muted">No files match "{query.trim()}".</p>
        )}

        {error && (
          <p className="error-text" role="alert">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  );
}

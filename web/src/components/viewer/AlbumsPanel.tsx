import { useEffect, useState } from 'react';

import { addAlbumFile, listAlbumFiles, listAlbums, removeAlbumFile } from '../../api/queries';
import type { Album, FileSummary } from '../../api/types';
import { Icon } from '../ui/Icon';

/**
 * Albums panel: shows which albums already contain this file, and lets the
 * user add it to any other album in the library.
 */
export function AlbumsPanel({
  libraryId,
  file,
  onChanged,
}: {
  libraryId: string;
  file: FileSummary;
  onChanged: () => void;
}) {
  const [allAlbums, setAllAlbums] = useState<Album[] | null>(null);
  const [memberIds, setMemberIds] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const resp = await listAlbums(libraryId);
        const albums = resp.albums ?? [];
        // Check which albums contain this file.
        const checks = await Promise.all(
          albums.map((a) =>
            listAlbumFiles(libraryId, a.id)
              .then((r) => (r.files ?? []).some((f) => f.id === file.id))
              .catch(() => false),
          ),
        );
        if (cancelled) return;
        setAllAlbums(albums);
        setMemberIds(new Set(albums.filter((_, i) => checks[i]).map((a) => a.id)));
        setError(null);
      } catch (e: unknown) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [libraryId, file.id, reloadKey]);

  const toggle = async (album: Album) => {
    setBusy(true);
    setError(null);
    try {
      if (memberIds.has(album.id)) {
        await removeAlbumFile(libraryId, album.id, file.id);
      } else {
        await addAlbumFile(libraryId, album.id, file.id);
      }
      setReloadKey((k) => k + 1);
      onChanged();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  if (allAlbums === null && error === null) {
    return (
      <p className="muted" role="status">
        Loading albums…
      </p>
    );
  }

  if (error) {
    return (
      <p className="error-text" role="alert">
        {error}
      </p>
    );
  }

  if (!allAlbums || allAlbums.length === 0) {
    return (
      <p className="muted">No albums yet. Create one from the Albums page to group your files.</p>
    );
  }

  return (
    <div data-testid="viewer-albums">
      <ul className="viewer-album-list">
        {allAlbums.map((album) => {
          const inAlbum = memberIds.has(album.id);
          return (
            <li key={album.id} className="viewer-album-row">
              <span className="viewer-album-name">{album.name}</span>
              <button
                type="button"
                className={inAlbum ? 'button viewer-album-btn active' : 'button viewer-album-btn'}
                disabled={busy}
                onClick={() => void toggle(album)}
                aria-pressed={inAlbum}
              >
                <Icon name={inAlbum ? 'check' : 'plus'} size={16} />
                {inAlbum ? 'In album' : 'Add'}
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

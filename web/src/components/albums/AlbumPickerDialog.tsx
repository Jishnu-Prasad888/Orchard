/**
 * "Add to album": choose an album, or name a new one, for a set of files.
 *
 * A short list rather than a form: covers make albums recognizable at a
 * glance, and the most recently changed albums come first because that is
 * usually where the next photo goes.
 */

import { type FormEvent, useEffect, useState } from 'react';

import { addAlbumFile, createAlbum, listAlbums } from '../../api/queries';
import type { Album } from '../../api/types';
import { Dialog } from '../Dialog';
import { thumbnailUrl } from '../media';
import { Icon } from '../ui/Icon';
import './AlbumPickerDialog.css';

export interface AlbumPickResult {
  album: Album;
  added: number;
  failed: number;
}

export function AlbumPickerDialog({
  open,
  libraryId,
  fileIds,
  onClose,
  onDone,
}: {
  open: boolean;
  libraryId: string;
  fileIds: string[];
  onClose: () => void;
  onDone: (result: AlbumPickResult) => void;
}) {
  if (!open) return null;
  return <Picker libraryId={libraryId} fileIds={fileIds} onClose={onClose} onDone={onDone} />;
}

function Picker({
  libraryId,
  fileIds,
  onClose,
  onDone,
}: {
  libraryId: string;
  fileIds: string[];
  onClose: () => void;
  onDone: (result: AlbumPickResult) => void;
}) {
  const [albums, setAlbums] = useState<Album[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [name, setName] = useState('');

  useEffect(() => {
    let cancelled = false;
    listAlbums(libraryId)
      .then((resp) => {
        if (cancelled) return;
        setAlbums(
          [...(resp.albums ?? [])].sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
        );
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [libraryId]);

  const addTo = async (album: Album) => {
    setBusy(true);
    setError(null);
    const results = await Promise.allSettled(
      fileIds.map((id) => addAlbumFile(libraryId, album.id, id)),
    );
    const failed = results.filter((r) => r.status === 'rejected').length;
    setBusy(false);
    onDone({ album, added: fileIds.length - failed, failed });
  };

  const onCreate = async (event: FormEvent) => {
    event.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;
    setBusy(true);
    setError(null);
    try {
      const created = await createAlbum(libraryId, trimmed);
      await addTo(created.album);
    } catch (e: unknown) {
      setBusy(false);
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const count = fileIds.length;

  return (
    <Dialog
      open
      title={`Add ${count} ${count === 1 ? 'item' : 'items'} to an album`}
      onClose={onClose}
      dismissible={!busy}
      testId="album-picker"
    >
      <form className="album-picker-new" onSubmit={(e) => void onCreate(e)}>
        <label className="visually-hidden" htmlFor="album-picker-name">
          New album name
        </label>
        <span className="album-picker-new-icon" aria-hidden="true">
          <Icon name="plus" />
        </span>
        <input
          id="album-picker-name"
          type="text"
          placeholder="New album"
          value={name}
          onChange={(e) => setName(e.target.value)}
          disabled={busy}
        />
        <button type="submit" className="button primary-button" disabled={busy || !name.trim()}>
          Create
        </button>
      </form>

      {albums === null && !error && (
        <p className="muted" role="status">
          Loading albums…
        </p>
      )}
      {albums && albums.length > 0 && (
        <ul className="album-picker-list" aria-label="Albums">
          {albums.map((album) => (
            <li key={album.id}>
              <button
                type="button"
                className="album-picker-item"
                onClick={() => void addTo(album)}
                disabled={busy}
              >
                <span className="album-picker-cover">
                  {album.cover_file_id ? (
                    <img
                      src={thumbnailUrl(libraryId, { id: album.cover_file_id })}
                      alt=""
                      loading="lazy"
                    />
                  ) : (
                    <Icon name="album" />
                  )}
                </span>
                <span className="album-picker-name">{album.name}</span>
              </button>
            </li>
          ))}
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

import { type FormEvent, useEffect, useState } from 'react';

import {
  addCollectionFile,
  createCollection,
  listCollections,
  listFileCollections,
  removeCollectionFile,
} from '../../api/queries';
import type { Collection, FileSummary } from '../../api/types';
import { Icon } from '../ui/Icon';

export function CollectionsPanel({
  libraryId,
  file,
  onChanged,
}: {
  libraryId: string;
  file: FileSummary;
  onChanged: () => void;
}) {
  const [loaded, setLoaded] = useState<{ all: Collection[]; mine: Collection[] } | null>(null);
  const [nameInput, setNameInput] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    void Promise.all([listCollections(libraryId), listFileCollections(libraryId, file.id)])
      .then(([all, mine]) => {
        if (cancelled) return;
        setLoaded({ all: all.collections ?? [], mine: mine.collections ?? [] });
        setError(null);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [libraryId, file.id, reloadKey]);

  const allCollections = loaded?.all ?? [];
  const memberIds = new Set((loaded?.mine ?? []).map((c) => c.id));

  const toggle = async (collection: Collection, member: boolean) => {
    setBusy(true);
    setError(null);
    try {
      if (member) await removeCollectionFile(libraryId, collection.id, file.id);
      else await addCollectionFile(libraryId, collection.id, file.id);
      setReloadKey((k) => k + 1);
      onChanged();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const createAndAdd = async (raw: string) => {
    const name = raw.trim();
    if (!name) return;
    setBusy(true);
    setError(null);
    try {
      const created = await createCollection(libraryId, name);
      await addCollectionFile(libraryId, created.collection.id, file.id);
      setReloadKey((k) => k + 1);
      setNameInput('');
      onChanged();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    void createAndAdd(nameInput);
  };

  return (
    <div data-testid="viewer-collections">
      {allCollections.length > 0 ? (
        <ul className="viewer-collection-list" data-testid="collection-checklist">
          {allCollections.map((c) => {
            const member = memberIds.has(c.id);
            return (
              <li key={c.id}>
                <label className="viewer-collection-item">
                  <input
                    type="checkbox"
                    checked={member}
                    disabled={busy}
                    onChange={() => void toggle(c, member)}
                    data-testid={`collection-checkbox-${c.name}`}
                  />
                  {c.name}
                </label>
              </li>
            );
          })}
        </ul>
      ) : (
        <p className="muted">No collections yet.</p>
      )}

      <form className="viewer-collection-form" onSubmit={onSubmit}>
        <label className="visually-hidden" htmlFor={`viewer-collection-input-${file.id}`}>
          New collection
        </label>
        <input
          id={`viewer-collection-input-${file.id}`}
          type="text"
          placeholder="New collection…"
          value={nameInput}
          onChange={(e) => setNameInput(e.target.value)}
          disabled={busy}
        />
        <button type="submit" className="button" disabled={busy || nameInput.trim() === ''}>
          <Icon name="plus" size={14} />
          Create
        </button>
      </form>

      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

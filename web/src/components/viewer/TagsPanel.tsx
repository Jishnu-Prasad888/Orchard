import { type FormEvent, useEffect, useState } from 'react';

import { addFileTag, createTag, listFileTags, listTags, removeFileTag } from '../../api/queries';
import type { FileSummary, Tag } from '../../api/types';
import { Icon } from '../ui/Icon';

export function TagsPanel({
  libraryId,
  file,
  onChanged,
}: {
  libraryId: string;
  file: FileSummary;
  onChanged: () => void;
}) {
  const [loaded, setLoaded] = useState<{ library: Tag[]; file: Tag[] } | null>(null);
  const [tagInput, setTagInput] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Bumping this re-runs the load; it is the only thing that makes the fetch
  // effect run again, so a reload does not need its own state plumbing.
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    void Promise.all([listTags(libraryId), listFileTags(libraryId, file.id)])
      .then(([all, mine]) => {
        if (cancelled) return;
        setLoaded({ library: all.tags ?? [], file: mine.tags ?? [] });
        setError(null);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [libraryId, file.id, reloadKey]);

  const libraryTags = loaded?.library ?? [];
  const fileTags = loaded?.file ?? [];

  const attachTag = async (raw: string) => {
    const name = raw.trim();
    if (!name) return;
    setBusy(true);
    setError(null);
    try {
      // Typing a new name creates the tag; an existing one is reused. Cairn has
      // no "find-or-create" endpoint, so the lookup is client-side.
      const existing = libraryTags.find((t) => t.name.toLowerCase() === name.toLowerCase());
      let tagId = existing?.id;
      if (!tagId) {
        const created = await createTag(libraryId, name);
        tagId = created.tag.id;
      }
      await addFileTag(libraryId, file.id, tagId);
      setReloadKey((k) => k + 1);
      setTagInput('');
      onChanged();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const detachTag = async (tag: Tag) => {
    setBusy(true);
    setError(null);
    try {
      await removeFileTag(libraryId, file.id, tag.id);
      setReloadKey((k) => k + 1);
      onChanged();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    void attachTag(tagInput);
  };

  return (
    <div data-testid="viewer-tags">
      {fileTags.length > 0 ? (
        <ul className="viewer-tag-list" data-testid="file-tag-list">
          {fileTags.map((t) => (
            <li key={t.id} className="tag-chip" data-testid={`file-tag-${t.name}`}>
              {t.name}
              <button
                type="button"
                aria-label={`Remove tag ${t.name}`}
                onClick={() => void detachTag(t)}
                disabled={busy}
              >
                <Icon name="close" size={14} />
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted">No tags on this file.</p>
      )}

      <form className="viewer-tag-form" onSubmit={onSubmit}>
        <label className="visually-hidden" htmlFor={`viewer-tag-input-${file.id}`}>
          Add a tag
        </label>
        <input
          id={`viewer-tag-input-${file.id}`}
          className="viewer-tag-input"
          type="text"
          list={`viewer-tag-suggestions-${file.id}`}
          placeholder="Add tag…"
          value={tagInput}
          onChange={(e) => setTagInput(e.target.value)}
          disabled={busy}
        />
        <datalist id={`viewer-tag-suggestions-${file.id}`}>
          {libraryTags.map((t) => (
            <option key={t.id} value={t.name} />
          ))}
        </datalist>
        <button type="submit" className="button" disabled={busy || tagInput.trim() === ''}>
          Add
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

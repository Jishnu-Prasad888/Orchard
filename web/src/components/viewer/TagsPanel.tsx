import { type FormEvent, type KeyboardEvent, useEffect, useId, useState } from 'react';

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
  const listId = useId();
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);

  // The dropdown: every library tag this file lacks that matches what is
  // typed, names starting with it first, then an offer to create the typed
  // name when no tag has it exactly.
  const typed = tagInput.trim();
  const needle = typed.toLowerCase();
  const onFile = new Set(fileTags.map((t) => t.id));
  const matches = libraryTags
    .filter((t) => !onFile.has(t.id) && t.name.toLowerCase().includes(needle))
    .sort((a, b) => {
      const ap = a.name.toLowerCase().startsWith(needle) ? 0 : 1;
      const bp = b.name.toLowerCase().startsWith(needle) ? 0 : 1;
      return ap - bp || a.name.localeCompare(b.name);
    });
  const exact = libraryTags.some((t) => t.name.toLowerCase() === needle);
  const options: { key: string; name: string; label: string }[] = [
    ...matches.map((t) => ({ key: t.id, name: t.name, label: t.name })),
    ...(typed && !exact ? [{ key: 'create', name: typed, label: `Create “${typed}”` }] : []),
  ];
  const showList = open && !busy && options.length > 0;
  const activeIndex = Math.min(active, options.length - 1);

  const attachTag = async (raw: string) => {
    const name = raw.trim();
    if (!name) return;
    setBusy(true);
    setError(null);
    try {
      // Typing a new name creates the tag; an existing one is reused. Orchard has
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

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (!open) {
        setOpen(true);
        return;
      }
      const step = e.key === 'ArrowDown' ? 1 : -1;
      setActive((i) => (Math.min(i, options.length - 1) + step + options.length) % options.length);
    } else if (e.key === 'Enter' && showList) {
      e.preventDefault();
      const option = options[activeIndex];
      if (option) void attachTag(option.name);
    } else if (e.key === 'Escape' && showList) {
      // Close the list, not the dialog around it.
      e.stopPropagation();
      setOpen(false);
    }
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
          role="combobox"
          autoComplete="off"
          aria-autocomplete="list"
          aria-expanded={showList}
          aria-controls={listId}
          aria-activedescendant={showList ? `${listId}-${activeIndex}` : undefined}
          placeholder="Search or create a tag…"
          value={tagInput}
          onChange={(e) => {
            setTagInput(e.target.value);
            setActive(0);
            setOpen(true);
          }}
          onFocus={() => setOpen(true)}
          onClick={() => setOpen(true)}
          onBlur={() => setOpen(false)}
          onKeyDown={onKeyDown}
          disabled={busy}
        />
        <button type="submit" className="button" disabled={busy || tagInput.trim() === ''}>
          Add
        </button>
      </form>
      {showList && (
        <ul id={listId} role="listbox" className="viewer-tag-options" aria-label="Tags">
          {options.map((option, i) => (
            <li
              key={option.key}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === activeIndex}
              className={option.key === 'create' ? 'is-create' : undefined}
              // Pick on mousedown so the input keeps focus and the list stays.
              onMouseDown={(e) => {
                e.preventDefault();
                void attachTag(option.name);
              }}
              onMouseEnter={() => setActive(i)}
            >
              <Icon name={option.key === 'create' ? 'plus' : 'tag'} size={14} />
              <span>{option.label}</span>
            </li>
          ))}
        </ul>
      )}

      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

/**
 * The reference picker for the memory editor.
 *
 * Memories link to photos, other memories, albums, people, and tags by id. The
 * ids are opaque and the spec is explicit that nobody should have to type one,
 * so this is a search box per kind: pick a kind, type a few letters, click a
 * result, and the `[[type:id|label]]` reference is inserted at the caret.
 *
 * Results are only fetched once the box has something to search for — a memory
 * editor that fires five listing requests the moment it opens is the reason
 * people avoid opening it.
 */

import { useCallback, useEffect, useState } from 'react';

import { mlWorthAsking } from '../api/mlSwitch';
import { listAlbums, listMemories, listPeople, listTags, searchFiles } from '../api/queries';
import type { RefType } from '../api/types';
import './RefPicker.css';

const KINDS: Array<{ type: RefType; label: string; placeholder: string }> = [
  { type: 'media', label: 'Media', placeholder: 'Search photos, videos, and files…' },
  { type: 'memory', label: 'Memory', placeholder: 'Search memories…' },
  { type: 'album', label: 'Album', placeholder: 'Search albums…' },
  { type: 'person', label: 'Person', placeholder: 'Search people…' },
  { type: 'tag', label: 'Tag', placeholder: 'Search tags…' },
];

interface Candidate {
  id: string;
  label: string;
  detail?: string | undefined;
}

/** Search the resource the reference will point at. */
async function search(libraryId: string, type: RefType, term: string): Promise<Candidate[]> {
  switch (type) {
    case 'media': {
      const resp = await searchFiles(libraryId, { q: term, limit: 25 });
      return (resp.files ?? []).map((file) => ({
        id: file.id,
        label: file.name,
        detail: file.folder_path || 'Library root',
      }));
    }
    case 'memory': {
      const resp = await listMemories(libraryId, { q: term, limit: 25 });
      return (resp.memories ?? []).map((m) => ({ id: m.id, label: m.title }));
    }
    case 'album': {
      const resp = await listAlbums(libraryId);
      const needle = term.toLowerCase();
      return (resp.albums ?? [])
        .filter((a) => a.name.toLowerCase().includes(needle))
        .map((a) => ({ id: a.id, label: a.name, detail: a.description }));
    }
    case 'person': {
      if (!(await mlWorthAsking())) return [];
      const resp = await listPeople(libraryId);
      const needle = term.toLowerCase();
      return (resp.people ?? [])
        .filter((p) => p.name.toLowerCase().includes(needle))
        .map((p) => ({ id: p.id, label: p.name, detail: `${p.face_count} faces` }));
    }
    case 'tag': {
      const resp = await listTags(libraryId);
      const needle = term.toLowerCase();
      return (resp.tags ?? [])
        .filter((t) => t.name.toLowerCase().includes(needle))
        .map((t) => ({ id: t.id, label: t.name }));
    }
  }
}

export function RefPicker({
  libraryId,
  onInsert,
}: {
  libraryId: string;
  /** Called with the finished `[[type:id|label]]` reference. */
  onInsert: (ref: string) => void;
}) {
  const [type, setType] = useState<RefType>('media');
  const [term, setTerm] = useState('');

  // Results are tagged with the query they came from, so changing the term, the
  // kind, or the library invalidates them by comparison rather than by clearing
  // state from inside the effect.
  const needle = term.trim();
  const active = needle.length >= 2;
  const key = active ? `${libraryId}|${type}|${needle}` : null;
  const [settled, setSettled] = useState<
    { key: string; results: Candidate[] } | { key: string; message: string } | null
  >(null);

  // Fetch on a short debounce so typing does not fire a request per keystroke.
  useEffect(() => {
    if (key === null) return;
    let cancelled = false;
    const timer = setTimeout(() => {
      search(libraryId, type, needle)
        .then((found) => {
          if (cancelled) return;
          setSettled({ key, results: found });
        })
        .catch((e: unknown) => {
          if (cancelled) return;
          setSettled({ key, message: e instanceof Error ? e.message : String(e) });
        });
    }, 250);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [key, libraryId, type, needle]);

  const current = key !== null && settled?.key === key ? settled : null;
  const searching = key !== null && current === null;
  const results = current !== null && 'results' in current ? current.results : [];
  const error = current !== null && 'message' in current ? current.message : null;

  const pick = useCallback(
    (candidate: Candidate) => {
      onInsert(`[[${type}:${candidate.id}|${candidate.label}]]`);
      // Clearing the term invalidates the current key, so the results go with it.
      setTerm('');
    },
    [onInsert, type],
  );

  const kind = KINDS.find((k) => k.type === type);

  return (
    <div className="ref-picker" data-testid="ref-picker">
      <div className="ref-picker-kinds" role="tablist" aria-label="Reference kind">
        {KINDS.map((k) => (
          <button
            key={k.type}
            type="button"
            role="tab"
            aria-selected={k.type === type}
            className={k.type === type ? 'ref-kind active' : 'ref-kind'}
            onClick={() => setType(k.type)}
            data-testid={`ref-kind-${k.type}`}
          >
            {k.label}
          </button>
        ))}
      </div>

      <div className="ref-picker-search">
        <label className="visually-hidden" htmlFor="ref-picker-input">
          Search for something to reference
        </label>
        <input
          id="ref-picker-input"
          type="search"
          className="ref-picker-input"
          placeholder={kind?.placeholder}
          value={term}
          onChange={(e) => setTerm(e.target.value)}
          data-testid="ref-search"
        />
        {searching && (
          <span className="muted ref-picker-status" role="status">
            Searching…
          </span>
        )}
      </div>

      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}

      {active && !searching && !error && results.length === 0 && (
        <p className="muted ref-picker-empty" data-testid="ref-empty">
          No {type} match “{needle}”.
        </p>
      )}

      {results.length > 0 && (
        <ul className="ref-picker-results" data-testid="ref-results">
          {results.map((candidate) => (
            <li key={candidate.id}>
              <button
                type="button"
                className="ref-result"
                onClick={() => pick(candidate)}
                data-testid={`ref-result-${candidate.id}`}
              >
                <span className="ref-result-label">{candidate.label}</span>
                {candidate.detail && (
                  <span className="muted ref-result-detail">{candidate.detail}</span>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * MediaPicker — choose photos and videos from the Cairn library.
 *
 * It looks and behaves like the library itself: a date-grouped thumbnail grid
 * with search and the same ways in — folders, albums, tags, people and
 * favorites — plus a date range. Picking never uploads or copies anything:
 * the memory stores references to the originals.
 *
 * Selection: click or Space toggles a photo; Shift+click selects a range.
 * In single mode (replace image, choose cover) one click chooses.
 */

import { Check, Play } from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import {
  listAlbums,
  listFavorites,
  listFiles,
  listPeople,
  listTags,
  searchFiles,
} from '../api/queries';
import type { Album, FileSummary, Person, Tag } from '../api/types';
import { Dialog } from '../components/Dialog';
import { FolderPicker } from '../components/FolderPicker';
import { thumbnailUrl } from '../components/media';
import { VideoThumb } from '../components/media/VideoThumb';
import { toPicked } from './format';
import type { PickedMedia } from './model';

type Source = 'all' | 'folder' | 'album' | 'tag' | 'person' | 'favorites';
type Kind = 'both' | 'photo' | 'video';

interface Props {
  open: boolean;
  libraryId: string;
  title?: string;
  confirmLabel?: string;
  mode?: 'multi' | 'single';
  /** Photos only (e.g. a cover image); videos are hidden. */
  photosOnly?: boolean;
  /** Files already in the section, marked so duplicates are a choice. */
  alreadyIn?: Set<string>;
  onCancel: () => void;
  onConfirm: (media: PickedMedia[]) => void;
}

const PAGE = 60;

function monthKey(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return 'Undated';
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'long' });
}

export function MediaPicker(props: Props) {
  if (!props.open) return null;
  return <MediaPickerDialog {...props} />;
}

function MediaPickerDialog({
  open,
  libraryId,
  title = 'Select photos and videos',
  confirmLabel = 'Add media',
  mode = 'multi',
  photosOnly = false,
  alreadyIn,
  onCancel,
  onConfirm,
}: Props) {
  const [q, setQ] = useState('');
  const [query, setQuery] = useState('');
  const [kind, setKind] = useState<Kind>(photosOnly ? 'photo' : 'both');
  const [source, setSource] = useState<Source>('all');
  const [folder, setFolder] = useState('');
  const [album, setAlbum] = useState('');
  const [tag, setTag] = useState('');
  const [person, setPerson] = useState('');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [page, setPage] = useState<{
    key: string;
    files: FileSummary[];
    cursor: string | null;
    error: string | null;
  }>({
    key: '',
    files: [],
    cursor: null,
    error: null,
  });
  const [loadingMore, setLoadingMore] = useState(false);
  const [selected, setSelected] = useState<Map<string, FileSummary>>(new Map());
  const [anchor, setAnchor] = useState<number | null>(null);
  const [albums, setAlbums] = useState<Album[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [people, setPeople] = useState<Person[]>([]);
  const sentinel = useRef<HTMLDivElement | null>(null);
  const request = useRef(0);

  // Debounce typing into the search box.
  useEffect(() => {
    const t = setTimeout(() => setQuery(q.trim()), 300);
    return () => clearTimeout(t);
  }, [q]);

  // Reset when (re)opened.
  // Collections for the source menus, loaded lazily on first need.
  useEffect(() => {
    if (source === 'album' && albums.length === 0)
      listAlbums(libraryId).then(
        (r) => setAlbums(r.albums ?? []),
        () => {},
      );
    if (source === 'tag' && tags.length === 0)
      listTags(libraryId).then(
        (r) => setTags(r.tags ?? []),
        () => {},
      );
    if (source === 'person' && people.length === 0)
      listPeople(libraryId).then(
        (r) => setPeople(r.people ?? []),
        () => {},
      );
  }, [source, libraryId, albums.length, tags.length, people.length]);

  const allowed = useCallback(
    (f: FileSummary) =>
      (kind === 'both'
        ? f.media_type === 'photo' || f.media_type === 'video'
        : f.media_type === kind) && f.status === 'present',
    [kind],
  );

  const fetchPage = useCallback(
    async (after: string | null): Promise<{ files: FileSummary[]; next: string | null }> => {
      const type = kind === 'both' ? undefined : kind;
      const fromIso = from ? new Date(`${from}T00:00:00`).toISOString() : undefined;
      const toIso = to ? new Date(`${to}T23:59:59`).toISOString() : undefined;
      if (source === 'favorites') {
        const resp = await listFavorites(libraryId);
        const needle = query.toLowerCase();
        return {
          files: (resp.files ?? []).filter((f) => !needle || f.name.toLowerCase().includes(needle)),
          next: null,
        };
      }
      const filtered =
        query || fromIso || toIso || source === 'album' || source === 'tag' || source === 'person';
      if (!filtered) {
        // Plain browsing: newest first, like the library.
        const resp = await listFiles(libraryId, {
          ...(type && { type }),
          ...(source === 'folder' && folder ? { folder } : {}),
          recursive: true,
          sort: 'mod_time',
          order: 'desc',
          limit: PAGE,
          ...(after ? { cursor: after } : {}),
        });
        return { files: resp.files ?? [], next: resp.next_cursor ?? null };
      }
      const resp = await searchFiles(libraryId, {
        ...(query && { q: query }),
        ...(type && { type }),
        ...(source === 'folder' && folder ? { folder } : {}),
        ...(source === 'album' && album ? { album } : {}),
        ...(source === 'tag' && tag ? { tag } : {}),
        ...(source === 'person' && person ? { person } : {}),
        ...(fromIso && { from: fromIso }),
        ...(toIso && { to: toIso }),
        limit: PAGE,
        ...(after ? { cursor: after } : {}),
      });
      return { files: resp.files ?? [], next: resp.next_cursor ?? null };
    },
    [libraryId, kind, source, folder, album, tag, person, query, from, to],
  );

  // The results shown always belong to the current filters; anything else
  // is still loading.
  const needsChoice =
    (source === 'album' && !album) ||
    (source === 'tag' && !tag) ||
    (source === 'person' && !person);
  const filterKey = JSON.stringify([kind, source, folder, album, tag, person, query, from, to]);
  const current = page.key === filterKey;
  const files = useMemo(
    () => (needsChoice || !current ? [] : page.files),
    [needsChoice, current, page.files],
  );
  const cursor = needsChoice || !current ? null : page.cursor;
  const error = current ? page.error : null;
  const loading = (!needsChoice && !current) || loadingMore;

  // First page whenever the filters change.
  useEffect(() => {
    if (needsChoice) return;
    const id = ++request.current;
    fetchPage(null)
      .then((p) => {
        if (id === request.current)
          setPage({ key: filterKey, files: p.files.filter(allowed), cursor: p.next, error: null });
      })
      .catch((e: unknown) => {
        if (id === request.current)
          setPage({
            key: filterKey,
            files: [],
            cursor: null,
            error: e instanceof Error ? e.message : String(e),
          });
      });
  }, [fetchPage, allowed, filterKey, needsChoice]);

  const loadMore = useCallback(() => {
    if (!cursor || loading) return;
    const id = ++request.current;
    setLoadingMore(true);
    fetchPage(cursor)
      .then((p) => {
        if (id !== request.current) return;
        setPage((prev) => ({
          ...prev,
          files: [...prev.files, ...p.files.filter(allowed)],
          cursor: p.next,
        }));
      })
      .catch((e: unknown) => {
        if (id === request.current)
          setPage((prev) => ({ ...prev, error: e instanceof Error ? e.message : String(e) }));
      })
      .finally(() => setLoadingMore(false));
  }, [cursor, loading, fetchPage, allowed]);

  // Infinite scroll.
  useEffect(() => {
    const el = sentinel.current;
    if (!el || !cursor || typeof IntersectionObserver === 'undefined') return;
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) loadMore();
    });
    io.observe(el);
    return () => io.disconnect();
  }, [cursor, loadMore]);

  const groups = useMemo(() => {
    const out: Array<{ label: string; items: Array<{ file: FileSummary; index: number }> }> = [];
    files.forEach((file, index) => {
      const label = monthKey(file.mod_time);
      const last = out[out.length - 1];
      if (last && last.label === label) last.items.push({ file, index });
      else out.push({ label, items: [{ file, index }] });
    });
    return out;
  }, [files]);

  const toggle = (file: FileSummary, index: number, range: boolean) => {
    if (mode === 'single') {
      onConfirm([toPicked(libraryId, file)]);
      return;
    }
    setSelected((prev) => {
      const next = new Map(prev);
      if (range && anchor !== null) {
        const [a, b] = anchor < index ? [anchor, index] : [index, anchor];
        for (let i = a; i <= b; i += 1) {
          const f = files[i];
          if (f) next.set(f.id, f);
        }
      } else if (next.has(file.id)) {
        next.delete(file.id);
      } else {
        next.set(file.id, file);
      }
      return next;
    });
    setAnchor(index);
  };

  const confirm = () => {
    // Keep the order the photos appear in the grid, not click order.
    const chosen = files.filter((f) => selected.has(f.id));
    for (const f of selected.values()) if (!chosen.includes(f)) chosen.push(f);
    onConfirm(chosen.map((f) => toPicked(libraryId, f)));
  };

  const count = selected.size;

  return (
    <Dialog
      open={open}
      title={title}
      onClose={onCancel}
      size="large"
      testId="media-picker"
      footer={
        mode === 'multi' ? (
          <div className="picker-footer">
            <span className="picker-count" role="status" aria-live="polite">
              {count === 0 ? 'Nothing selected' : `${count} selected`}
            </span>
            {count > 0 && (
              <button type="button" className="button" onClick={() => setSelected(new Map())}>
                Clear
              </button>
            )}
            <button type="button" className="button" onClick={onCancel}>
              Cancel
            </button>
            <button
              type="button"
              className="button primary-button"
              disabled={count === 0}
              onClick={confirm}
            >
              {confirmLabel}
              {count > 0 ? ` (${count})` : ''}
            </button>
          </div>
        ) : (
          <div className="picker-footer">
            <button type="button" className="button" onClick={onCancel}>
              Cancel
            </button>
          </div>
        )
      }
    >
      <div className="picker">
        <div className="picker-filters">
          <input
            type="search"
            className="picker-search"
            placeholder="Search by name or path…"
            aria-label="Search the library"
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
          {!photosOnly && (
            <div className="segmented" role="group" aria-label="Media type">
              {(
                [
                  ['both', 'All'],
                  ['photo', 'Photos'],
                  ['video', 'Videos'],
                ] as const
              ).map(([id, label]) => (
                <button
                  key={id}
                  type="button"
                  className={kind === id ? 'segment active' : 'segment'}
                  aria-pressed={kind === id}
                  onClick={() => setKind(id)}
                >
                  {label}
                </button>
              ))}
            </div>
          )}
          <label className="picker-field">
            <span>From</span>
            <select
              value={source}
              onChange={(e) => setSource(e.target.value as Source)}
              aria-label="Browse by"
            >
              <option value="all">Whole library</option>
              <option value="folder">A folder</option>
              <option value="album">An album</option>
              <option value="tag">A tag</option>
              <option value="person">A person</option>
              <option value="favorites">Favorites</option>
            </select>
          </label>
          {source === 'folder' && (
            <div className="picker-field picker-folder">
              <FolderPicker
                libraryId={libraryId}
                value={folder}
                onChange={setFolder}
                label="Folder"
                placeholder="All folders"
              />
            </div>
          )}
          {source === 'album' && (
            <select aria-label="Album" value={album} onChange={(e) => setAlbum(e.target.value)}>
              <option value="">Choose an album…</option>
              {albums.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          )}
          {source === 'tag' && (
            <select aria-label="Tag" value={tag} onChange={(e) => setTag(e.target.value)}>
              <option value="">Choose a tag…</option>
              {tags.map((t) => (
                <option key={t.id} value={t.name}>
                  {t.name}
                </option>
              ))}
            </select>
          )}
          {source === 'person' && (
            <select aria-label="Person" value={person} onChange={(e) => setPerson(e.target.value)}>
              <option value="">Choose a person…</option>
              {people.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          )}
          {source !== 'favorites' && (
            <div className="picker-dates">
              <label>
                <span>After</span>
                <input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
              </label>
              <label>
                <span>Before</span>
                <input type="date" value={to} onChange={(e) => setTo(e.target.value)} />
              </label>
            </div>
          )}
        </div>

        {error && (
          <p className="error-text" role="alert">
            {error}
          </p>
        )}

        <div className="picker-grid-scroll" data-testid="picker-results">
          {groups.map((group) => (
            <section key={group.label} className="picker-group" aria-label={group.label}>
              <h3 className="picker-group-title">{group.label}</h3>
              <div className="picker-grid">
                {group.items.map(({ file, index }) => {
                  const on = selected.has(file.id);
                  const already = alreadyIn?.has(file.id);
                  return (
                    <button
                      key={file.id}
                      type="button"
                      className={`picker-item${on ? ' is-selected' : ''}`}
                      aria-pressed={mode === 'multi' ? on : undefined}
                      aria-label={`${file.name}${file.media_type === 'video' ? ', video' : ''}${already ? ', already in this section' : ''}`}
                      title={file.rel_path}
                      onClick={(e) => toggle(file, index, e.shiftKey)}
                    >
                      {file.media_type === 'video' ? (
                        <VideoThumb
                          libraryId={libraryId}
                          fileId={file.id}
                          className="picker-thumb"
                        />
                      ) : (
                        <img
                          className="picker-thumb"
                          src={thumbnailUrl(libraryId, file)}
                          alt=""
                          loading="lazy"
                          decoding="async"
                          draggable={false}
                        />
                      )}
                      {file.media_type === 'video' && (
                        <span className="picker-badge" aria-hidden="true">
                          <Play size={12} fill="currentColor" />
                        </span>
                      )}
                      {already && <span className="picker-already">In section</span>}
                      {mode === 'multi' && (
                        <span className="picker-check" aria-hidden="true">
                          {on && <Check size={14} strokeWidth={3} />}
                        </span>
                      )}
                    </button>
                  );
                })}
              </div>
            </section>
          ))}
          {!loading && files.length === 0 && !error && (
            <p className="muted picker-empty">
              {(source === 'album' && !album) ||
              (source === 'tag' && !tag) ||
              (source === 'person' && !person)
                ? 'Choose one above to see its media.'
                : 'No photos or videos match.'}
            </p>
          )}
          {loading && <p className="muted picker-loading">Loading…</p>}
          {cursor && !loading && (
            <div ref={sentinel} className="picker-more">
              <button type="button" className="button" onClick={loadMore}>
                Load more
              </button>
            </div>
          )}
        </div>
      </div>
    </Dialog>
  );
}

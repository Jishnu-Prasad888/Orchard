/**
 * The global search box.
 *
 * Search is how a large library is actually used, so it is the most prominent
 * control in the top bar, `/` focuses it from anywhere, and it suggests as you
 * type: the query itself, the query narrowed to photos or videos, and any
 * people, albums, or tags whose names match — all from the real library, never
 * invented. With nothing typed it offers recent searches.
 *
 * It follows the ARIA combobox pattern: the input owns a listbox, arrow keys
 * move the active option, Enter takes it, Escape closes the list.
 */

import { type FormEvent, useEffect, useId, useMemo, useRef, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';

import { useLibraries } from '../../api/libraries';
import { mlWorthAsking } from '../../api/mlSwitch';
import { listAlbums, listPeople, listTags } from '../../api/queries';
import { forgetSearch, readRecentSearches, rememberSearch } from '../../lib/recentSearches';
import { Icon, type IconName } from '../ui/Icon';
import './SearchBox.css';

interface Suggestion {
  id: string;
  label: string;
  hint?: string;
  icon: IconName;
  to: string;
  /** Saved to recent searches when taken. */
  remember?: string;
  /** A recent search, which can be removed from the list. */
  recent?: boolean;
}

interface Entities {
  libraryId: string;
  people: Array<{ id: string; name: string }>;
  albums: Array<{ id: string; name: string }>;
  tags: Array<{ id: string; name: string }>;
}

const MAX_PER_KIND = 3;

function matches(name: string, q: string) {
  return name.toLowerCase().includes(q.toLowerCase());
}

export function SearchBox({
  inputRef: externalRef,
  autoFocus = false,
  className,
}: {
  inputRef?: React.RefObject<HTMLInputElement | null>;
  autoFocus?: boolean;
  className?: string;
}) {
  const navigate = useNavigate();
  const location = useLocation();
  const { libraryId } = useLibraries();
  const listboxId = useId();
  const localRef = useRef<HTMLInputElement | null>(null);
  const inputRef = externalRef ?? localRef;
  const wrapRef = useRef<HTMLFormElement | null>(null);

  const urlQuery =
    location.pathname === '/search' ? (new URLSearchParams(location.search).get('q') ?? '') : '';
  const [query, setQuery] = useState(urlQuery);
  const [prevUrlQuery, setPrevUrlQuery] = useState(urlQuery);
  if (prevUrlQuery !== urlQuery) {
    setPrevUrlQuery(urlQuery);
    setQuery(urlQuery);
  }

  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const [recent, setRecent] = useState<string[]>([]);
  const [entities, setEntities] = useState<Entities | null>(null);

  // Names for suggestions, loaded once per library the first time the box is
  // opened. Each list is optional: a server without face support has no people.
  useEffect(() => {
    if (!open || !libraryId || entities?.libraryId === libraryId) return;
    let cancelled = false;
    void Promise.all([
      mlWorthAsking().then((ask) =>
        ask
          ? listPeople(libraryId)
              .then((r) => r.people ?? [])
              .catch(() => [])
          : [],
      ),
      listAlbums(libraryId)
        .then((r) => r.albums ?? [])
        .catch(() => []),
      listTags(libraryId)
        .then((r) => r.tags ?? [])
        .catch(() => []),
    ]).then(([people, albums, tags]) => {
      if (!cancelled) setEntities({ libraryId, people, albums, tags });
    });
    return () => {
      cancelled = true;
    };
  }, [open, libraryId, entities?.libraryId]);

  const q = query.trim();
  const suggestions = useMemo<Suggestion[]>(() => {
    if (!q) {
      return recent.map((text) => ({
        id: `recent:${text}`,
        label: text,
        icon: 'clock',
        to: `/search?q=${encodeURIComponent(text)}`,
        remember: text,
        recent: true,
      }));
    }
    const encoded = encodeURIComponent(q);
    const list: Suggestion[] = [
      {
        id: 'all',
        label: q,
        hint: 'Search everything',
        icon: 'search',
        to: `/search?q=${encoded}`,
        remember: q,
      },
      {
        id: 'photos',
        label: q,
        hint: 'in Photos',
        icon: 'photo',
        to: `/search?q=${encoded}&type=photo`,
        remember: q,
      },
      {
        id: 'videos',
        label: q,
        hint: 'in Videos',
        icon: 'video',
        to: `/search?q=${encoded}&type=video`,
        remember: q,
      },
    ];
    const current = entities?.libraryId === libraryId ? entities : null;
    if (current) {
      current.people
        .filter((p) => p.name && matches(p.name, q))
        .slice(0, MAX_PER_KIND)
        .forEach((p) =>
          list.push({
            id: `person:${p.id}`,
            label: p.name,
            hint: 'Person',
            icon: 'person',
            to: `/search?person=${p.id}`,
          }),
        );
      current.albums
        .filter((a) => matches(a.name, q))
        .slice(0, MAX_PER_KIND)
        .forEach((a) =>
          list.push({
            id: `album:${a.id}`,
            label: a.name,
            hint: 'Album',
            icon: 'album',
            to: `/albums/${a.id}`,
          }),
        );
      current.tags
        .filter((t) => matches(t.name, q))
        .slice(0, MAX_PER_KIND)
        .forEach((t) =>
          list.push({
            id: `tag:${t.id}`,
            label: t.name,
            hint: 'Tag',
            icon: 'tag',
            to: `/search?tag=${t.id}`,
          }),
        );
    }
    return list;
  }, [q, recent, entities, libraryId]);

  const showList = open && (suggestions.length > 0 || !q);

  const take = (suggestion: Suggestion) => {
    if (suggestion.remember) rememberSearch(suggestion.remember);
    setOpen(false);
    setActive(-1);
    inputRef.current?.blur();
    navigate(suggestion.to);
  };

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const picked = active >= 0 ? suggestions[active] : undefined;
    if (picked) {
      take(picked);
      return;
    }
    if (q) rememberSearch(q);
    setOpen(false);
    inputRef.current?.blur();
    navigate(q ? `/search?q=${encodeURIComponent(q)}` : '/search');
  };

  const openList = () => {
    setRecent(readRecentSearches());
    setOpen(true);
  };

  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      if (!open) openList();
      const count = suggestions.length;
      if (count === 0) return;
      setActive((i) => (event.key === 'ArrowDown' ? (i + 1) % count : (i - 1 + count) % count));
    } else if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      if (open) {
        setOpen(false);
        setActive(-1);
      } else {
        inputRef.current?.blur();
      }
    }
  };

  // Close when focus or a click leaves the box.
  useEffect(() => {
    if (!open) return;
    const onDown = (event: PointerEvent) => {
      if (!wrapRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, [open]);

  return (
    <form
      ref={wrapRef}
      className={['search-box', open ? 'is-open' : '', className].filter(Boolean).join(' ')}
      role="search"
      onSubmit={onSubmit}
    >
      <Icon name="search" className="search-box-icon" />
      <input
        ref={inputRef}
        type="search"
        role="combobox"
        aria-label="Search Cairn"
        aria-expanded={showList}
        aria-controls={listboxId}
        aria-autocomplete="list"
        aria-activedescendant={active >= 0 ? `${listboxId}-${active}` : undefined}
        placeholder="Search your photos, files, people…"
        autoComplete="off"
        spellCheck={false}
        autoFocus={autoFocus}
        value={query}
        onChange={(event) => {
          setQuery(event.target.value);
          setActive(-1);
          if (!open) openList();
        }}
        onFocus={openList}
        onKeyDown={onKeyDown}
        data-testid="global-search"
      />
      {query && (
        <button
          type="button"
          className="search-box-clear"
          aria-label="Clear search"
          onClick={() => {
            setQuery('');
            setActive(-1);
            inputRef.current?.focus();
          }}
        >
          <Icon name="close" size={18} />
        </button>
      )}
      <kbd className="search-box-kbd" aria-hidden="true">
        /
      </kbd>

      {showList && (
        <div className="search-suggestions">
          {suggestions.length === 0 ? (
            <p className="search-suggestions-hint">
              Search by file name, folder, tag, album, or person.
            </p>
          ) : (
            <>
              {!q && <p className="search-suggestions-heading">Recent searches</p>}
              <ul role="listbox" id={listboxId} aria-label="Search suggestions">
                {suggestions.map((s, i) => (
                  <li
                    key={s.id}
                    id={`${listboxId}-${i}`}
                    role="option"
                    aria-selected={i === active}
                    className={i === active ? 'search-option is-active' : 'search-option'}
                    onPointerDown={(event) => event.preventDefault()}
                    onClick={() => take(s)}
                    onMouseEnter={() => setActive(i)}
                  >
                    <Icon name={s.icon} size={18} />
                    <span className="search-option-label">{s.label}</span>
                    {s.hint && <span className="search-option-hint">{s.hint}</span>}
                    {s.recent && (
                      <button
                        type="button"
                        className="search-option-remove"
                        aria-label={`Remove ${s.label} from recent searches`}
                        onClick={(event) => {
                          event.stopPropagation();
                          forgetSearch(s.label);
                          setRecent(readRecentSearches());
                        }}
                      >
                        <Icon name="close" size={14} />
                      </button>
                    )}
                  </li>
                ))}
              </ul>
            </>
          )}
        </div>
      )}
    </form>
  );
}

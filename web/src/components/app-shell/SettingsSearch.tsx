/**
 * Settings search — the top-bar search box.
 *
 * It finds a setting, not a file: type "theme", "slideshow" or "users" and
 * jump straight to that section of the Settings page. It reuses the global
 * search box's look and its ARIA combobox behaviour (arrow keys, Enter,
 * Escape). Administrator-only sections are only offered to administrators.
 */

import { useEffect, useId, useMemo, useRef, useState } from 'react';
import type { FormEvent, KeyboardEvent, RefObject } from 'react';
import { useNavigate } from 'react-router-dom';

import { useAuth } from '../../auth/authContext';
import { Icon } from '../ui/Icon';
import './SearchBox.css';

interface SettingEntry {
  label: string;
  /** Section id on the Settings page. */
  section: string;
  hint: string;
  words: string;
  adminOnly?: boolean;
}

const SETTING_ENTRIES: SettingEntry[] = [
  {
    label: 'Account',
    section: 'account',
    hint: 'Account',
    words: 'profile username role sign out log out logout',
  },
  {
    label: 'Theme',
    section: 'appearance',
    hint: 'Appearance',
    words: 'dark light system appearance colour color mode',
  },
  {
    label: 'Tags, sharing, duplicates and trash',
    section: 'library',
    hint: 'Library',
    words: 'library tags sharing shared duplicates trash deleted',
  },
  {
    label: 'Memory slideshow interval',
    section: 'memories',
    hint: 'Memories',
    words: 'slideshow interval seconds speed',
  },
  {
    label: 'Edited copies of photos',
    section: 'memories',
    hint: 'Memories',
    words: 'edited copies edits save separate copy',
  },
  {
    label: 'Default memory layout and mode',
    section: 'memories',
    hint: 'Memories',
    words: 'layout grid hero masonry mode edit preview',
  },
  {
    label: 'Autosave memories',
    section: 'memories',
    hint: 'Memories',
    words: 'autosave save automatically',
  },
  {
    label: 'Accounts and users',
    section: 'accounts',
    hint: 'Accounts',
    words: 'users people invite create password sessions revoke administrator member',
    adminOnly: true,
  },
  {
    label: 'Advanced',
    section: 'advanced',
    hint: 'Advanced',
    words: 'server tasks maintenance rescan reindex backups',
    adminOnly: true,
  },
  {
    label: 'Access',
    section: 'access',
    hint: 'Access',
    words: 'access libraries permissions shared with you',
  },
  {
    label: 'About Cairn',
    section: 'about',
    hint: 'About',
    words: 'version build health status license',
  },
];

function filterSettings(query: string, isAdmin: boolean): SettingEntry[] {
  const q = query.trim().toLowerCase();
  const visible = SETTING_ENTRIES.filter((e) => isAdmin || !e.adminOnly);
  // "access" is the non-admin spelling of the library/accounts area.
  const list = isAdmin ? visible.filter((e) => e.section !== 'access') : visible;
  if (!q) return list;
  const terms = q.split(/\s+/);
  return list.filter((e) => {
    const hay = `${e.label} ${e.hint} ${e.words}`.toLowerCase();
    return terms.every((t) => hay.includes(t));
  });
}

export function SettingsSearch({ inputRef }: { inputRef?: RefObject<HTMLInputElement | null> }) {
  const navigate = useNavigate();
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  const listboxId = useId();
  const localRef = useRef<HTMLInputElement | null>(null);
  const ref = inputRef ?? localRef;
  const wrapRef = useRef<HTMLFormElement | null>(null);

  const [query, setQuery] = useState('');
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);

  const results = useMemo(() => filterSettings(query, isAdmin), [query, isAdmin]);

  const go = (entry: SettingEntry) => {
    setOpen(false);
    setActive(-1);
    setQuery('');
    ref.current?.blur();
    navigate(`/settings#${entry.section}`);
  };

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const picked = results[active >= 0 ? active : 0];
    if (picked) go(picked);
    else navigate('/settings');
  };

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      setOpen(true);
      if (results.length === 0) return;
      setActive((i) =>
        event.key === 'ArrowDown'
          ? (i + 1) % results.length
          : (i - 1 + results.length) % results.length,
      );
    } else if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      if (open) {
        setOpen(false);
        setActive(-1);
      } else {
        ref.current?.blur();
      }
    }
  };

  useEffect(() => {
    if (!open) return;
    const onDown = (event: PointerEvent) => {
      if (!wrapRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, [open]);

  const showList = open && results.length > 0;

  return (
    <form
      ref={wrapRef}
      className={open ? 'search-box is-open' : 'search-box'}
      role="search"
      onSubmit={onSubmit}
    >
      <Icon name="search" className="search-box-icon" />
      <input
        ref={ref}
        type="search"
        role="combobox"
        aria-label="Search settings"
        aria-expanded={showList}
        aria-controls={listboxId}
        aria-autocomplete="list"
        aria-activedescendant={active >= 0 ? `${listboxId}-${active}` : undefined}
        placeholder="Search settings…"
        autoComplete="off"
        spellCheck={false}
        value={query}
        onChange={(event) => {
          setQuery(event.target.value);
          setActive(-1);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={onKeyDown}
        data-testid="settings-search"
      />
      <kbd className="search-box-kbd" aria-hidden="true">
        /
      </kbd>

      {open && (
        <div className="search-suggestions">
          {results.length === 0 ? (
            <p className="search-suggestions-hint">No setting matches “{query.trim()}”.</p>
          ) : (
            <ul role="listbox" id={listboxId} aria-label="Settings">
              {results.map((entry, i) => (
                <li
                  key={entry.label}
                  id={`${listboxId}-${i}`}
                  role="option"
                  aria-selected={i === active}
                  className={i === active ? 'search-option is-active' : 'search-option'}
                  onPointerDown={(event) => event.preventDefault()}
                  onClick={() => go(entry)}
                  onMouseEnter={() => setActive(i)}
                >
                  <Icon name="settings" size={18} />
                  <span className="search-option-label">{entry.label}</span>
                  <span className="search-option-hint">{entry.hint}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </form>
  );
}

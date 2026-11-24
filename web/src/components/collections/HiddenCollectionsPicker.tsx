/**
 * "Hidden" in the Collections header: a dropdown listing every collection
 * with a tick box, ticked for the ones kept off the page. A hidden
 * collection still turns up in a search, in a folded section of its own.
 * Above them, the libraries: ticking one hides every collection in it.
 */

import { ChevronDown, EyeOff } from 'lucide-react';
import { useEffect, useId, useRef, useState } from 'react';

import type { Collection, Library } from '../../api/types';
import './HiddenCollectionsPicker.css';

export function HiddenCollectionsPicker({
  collections,
  isHidden,
  onToggle,
  libraries = [],
  currentLibraryId,
  isLibraryHidden = () => false,
  onToggleLibrary,
}: {
  collections: Collection[];
  isHidden: (c: Collection) => boolean;
  onToggle: (c: Collection, hidden: boolean) => void;
  libraries?: Library[];
  /** The library whose collections are listed below. */
  currentLibraryId?: string;
  isLibraryHidden?: (id: string) => boolean;
  onToggleLibrary?: (id: string, hidden: boolean) => void;
}) {
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState('');
  const root = useRef<HTMLDivElement | null>(null);
  const button = useRef<HTMLButtonElement | null>(null);
  const panelId = useId();
  const currentHidden = currentLibraryId ? isLibraryHidden(currentLibraryId) : false;
  const hiddenLibraries = libraries.filter((l) => isLibraryHidden(l.id)).length;
  const hiddenCount = currentHidden ? collections.length : collections.filter(isHidden).length;

  // An outside click or Escape closes it; Escape hands focus back.
  useEffect(() => {
    if (!open) return;
    const onDown = (event: MouseEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      setOpen(false);
      button.current?.focus();
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const needle = filter.trim().toLowerCase();
  const listed = collections
    .filter((c) => !needle || c.name.toLowerCase().includes(needle))
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }));

  return (
    <div className="hidden-picker" ref={root}>
      <button
        ref={button}
        type="button"
        className={hiddenCount ? 'button is-active' : 'button'}
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen((o) => !o)}
        data-testid="hidden-collections-button"
      >
        <EyeOff aria-hidden="true" />
        Hidden
        {hiddenCount > 0 && <span className="hidden-picker-count">{hiddenCount}</span>}
        <ChevronDown aria-hidden="true" />
      </button>
      {open && (
        <div
          id={panelId}
          className="hidden-picker-panel"
          role="group"
          aria-label="Collections to hide"
          data-testid="hidden-collections-panel"
        >
          <p className="hidden-picker-help">
            Ticked collections stay off this page and Home. Search still finds them.
          </p>
          {onToggleLibrary && libraries.length > 0 && (
            <>
              <h4 className="hidden-picker-heading">
                Libraries
                {hiddenLibraries > 0 && <span className="muted"> · {hiddenLibraries} hidden</span>}
              </h4>
              <ul className="hidden-picker-list is-short">
                {libraries.map((l) => (
                  <li key={l.id}>
                    <label className="hidden-picker-row">
                      <input
                        type="checkbox"
                        checked={isLibraryHidden(l.id)}
                        onChange={(e) => onToggleLibrary(l.id, e.target.checked)}
                        data-testid={`hide-library-${l.name}`}
                      />
                      <span className="hidden-picker-name">
                        All collections in {l.name}
                        {l.id === currentLibraryId && <span className="muted"> (this one)</span>}
                      </span>
                    </label>
                  </li>
                ))}
              </ul>
              <h4 className="hidden-picker-heading">Collections</h4>
            </>
          )}
          {currentHidden && (
            <p className="hidden-picker-help">
              This whole library is hidden, so all of these are too.
            </p>
          )}
          {collections.length > 8 && (
            <input
              type="search"
              className="dialog-input hidden-picker-filter"
              placeholder="Find a collection…"
              aria-label="Find a collection"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              autoFocus
            />
          )}
          <ul className="hidden-picker-list">
            {listed.map((c) => (
              <li key={c.id}>
                <label className="hidden-picker-row">
                  <input
                    type="checkbox"
                    checked={currentHidden || isHidden(c)}
                    disabled={currentHidden}
                    onChange={(e) => onToggle(c, e.target.checked)}
                    data-testid={`hide-collection-${c.name}`}
                  />
                  <span className="hidden-picker-name">{c.name}</span>
                </label>
              </li>
            ))}
            {listed.length === 0 && <li className="muted">No collection matches.</li>}
          </ul>
        </div>
      )}
    </div>
  );
}

/**
 * Narrowing a listing by when it was modified, how big it is, and — on the
 * Search page only — by book author or collection. Opens under the toolbar
 * when asked for; clearing re-queries rather than only emptying the fields.
 */

import type { Collection } from '../../api/types';
import './Files.css';

import { EMPTY_RANGE, hasRangeFilters, type RangeFilters } from './filters';

export function FilterPanel({
  value,
  onChange,
  author,
  onAuthorChange,
  collectionId,
  onCollectionChange,
  collections,
}: {
  value: RangeFilters;
  onChange: (next: RangeFilters) => void;
  /** Author/collection filters only make sense on the Search page (they go
   * through `/search`, not the plain folder listing), so these are
   * undefined everywhere else and the corresponding fields are hidden. */
  author?: string | undefined;
  onAuthorChange?: ((next: string | undefined) => void) | undefined;
  collectionId?: string | undefined;
  onCollectionChange?: ((next: string | undefined) => void) | undefined;
  collections?: Collection[] | undefined;
}) {
  const set = (patch: Partial<RangeFilters>) => onChange({ ...value, ...patch });
  const anyActive = hasRangeFilters(value) || Boolean(author) || Boolean(collectionId);
  const clearAll = () => {
    onChange(EMPTY_RANGE);
    onAuthorChange?.(undefined);
    onCollectionChange?.(undefined);
  };
  return (
    <fieldset className="filter-panel" id="media-filters" data-testid="media-filters">
      <legend className="visually-hidden">Filters</legend>
      <label className="field">
        <span className="field-label">Modified after</span>
        <input type="date" value={value.from} onChange={(e) => set({ from: e.target.value })} />
      </label>
      <label className="field">
        <span className="field-label">Modified before</span>
        <input type="date" value={value.to} onChange={(e) => set({ to: e.target.value })} />
      </label>
      <label className="field">
        <span className="field-label">Min size (MB)</span>
        <input
          type="number"
          min={0}
          step="any"
          inputMode="decimal"
          placeholder="0"
          value={value.minSizeMb}
          onChange={(e) => set({ minSizeMb: e.target.value })}
        />
      </label>
      <label className="field">
        <span className="field-label">Max size (MB)</span>
        <input
          type="number"
          min={0}
          step="any"
          inputMode="decimal"
          placeholder="Any"
          value={value.maxSizeMb}
          onChange={(e) => set({ maxSizeMb: e.target.value })}
        />
      </label>
      {onAuthorChange && (
        <label className="field">
          <span className="field-label">Author</span>
          <input
            type="text"
            placeholder="Any"
            value={author ?? ''}
            onChange={(e) => onAuthorChange(e.target.value || undefined)}
            data-testid="author-filter-input"
          />
        </label>
      )}
      {onCollectionChange && (
        <label className="field">
          <span className="field-label">Collection</span>
          <select
            value={collectionId ?? ''}
            onChange={(e) => onCollectionChange(e.target.value || undefined)}
            data-testid="collection-filter-select"
          >
            <option value="">Any</option>
            {(collections ?? []).map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </label>
      )}
      {anyActive && (
        <button type="button" className="button ghost-button" onClick={clearAll}>
          Clear filters
        </button>
      )}
    </fieldset>
  );
}

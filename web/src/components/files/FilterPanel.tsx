/**
 * Narrowing a listing by when it was modified and how big it is. Opens under
 * the toolbar when asked for; clearing re-queries rather than only emptying
 * the fields.
 */

import './Files.css';

import { EMPTY_RANGE, hasRangeFilters, type RangeFilters } from './filters';

export function FilterPanel({
  value,
  onChange,
}: {
  value: RangeFilters;
  onChange: (next: RangeFilters) => void;
}) {
  const set = (patch: Partial<RangeFilters>) => onChange({ ...value, ...patch });
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
      {hasRangeFilters(value) && (
        <button type="button" className="button ghost-button" onClick={() => onChange(EMPTY_RANGE)}>
          Clear filters
        </button>
      )}
    </fieldset>
  );
}

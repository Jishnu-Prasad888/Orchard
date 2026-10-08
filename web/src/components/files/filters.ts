/** The date and size bounds a listing can be narrowed by. */

export interface RangeFilters {
  minSizeMb: string;
  maxSizeMb: string;
  from: string;
  to: string;
}

export const EMPTY_RANGE: RangeFilters = { minSizeMb: '', maxSizeMb: '', from: '', to: '' };

export const hasRangeFilters = (f: RangeFilters) =>
  f.minSizeMb !== '' || f.maxSizeMb !== '' || f.from !== '' || f.to !== '';

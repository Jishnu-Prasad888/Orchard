/**
 * The fixed palette a collection can be coloured with — a small set of soft
 * tones, like Google Keep's, rather than a free colour picker, so a shelf
 * of collections stays calm and the colours stay easy to tell apart and to
 * filter by. The server keeps the same names (`collections.Colors`); what
 * each looks like, in light and dark, is in CollectionColors.css.
 */

export const COLLECTION_COLORS = [
  { id: 'coral', label: 'Coral' },
  { id: 'peach', label: 'Peach' },
  { id: 'sand', label: 'Sand' },
  { id: 'mint', label: 'Mint' },
  { id: 'sage', label: 'Sage' },
  { id: 'fog', label: 'Fog' },
  { id: 'storm', label: 'Storm' },
  { id: 'dusk', label: 'Dusk' },
  { id: 'blossom', label: 'Blossom' },
  { id: 'clay', label: 'Clay' },
  { id: 'chalk', label: 'Chalk' },
] as const;

export type CollectionColor = (typeof COLLECTION_COLORS)[number]['id'];

export function isCollectionColor(value: string | null | undefined): value is CollectionColor {
  return COLLECTION_COLORS.some((c) => c.id === value);
}

export function colorLabel(id: string): string {
  return COLLECTION_COLORS.find((c) => c.id === id)?.label ?? 'No colour';
}

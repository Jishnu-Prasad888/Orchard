/**
 * Non-destructive image edits, rendered in the browser.
 *
 * The filter presets mirror internal/memories/edits.go exactly — each is a
 * sequence of CSS filter primitives whose math the server reproduces when it
 * renders an edited copy — so what you see in the editor is what the copy
 * contains. Keep the two tables in sync.
 *
 * Geometry follows the same pipeline as the server: the original as the
 * browser shows it (EXIF orientation applied), then rotation, then a crop
 * rectangle expressed in the rotated image's normalized coordinates.
 */

import type { Adjustments, Crop, FilterName, ImageEdits, Rotation } from './types';

type FilterOp = [kind: string, amount: number];

export const FILTER_PRESETS: Record<FilterName, FilterOp[]> = {
  original: [],
  warm: [
    ['sepia', 0.22],
    ['saturate', 1.15],
    ['brightness', 1.03],
  ],
  cool: [
    ['saturate', 0.9],
    ['hue-rotate', 12],
    ['brightness', 1.02],
  ],
  bw: [
    ['grayscale', 1],
    ['contrast', 1.12],
  ],
  soft: [
    ['contrast', 0.86],
    ['brightness', 1.06],
    ['saturate', 0.85],
  ],
  contrast: [
    ['contrast', 1.22],
    ['saturate', 1.12],
  ],
};

export const FILTERS: Array<{ id: FilterName; label: string }> = [
  { id: 'original', label: 'Original' },
  { id: 'warm', label: 'Warm' },
  { id: 'cool', label: 'Cool' },
  { id: 'bw', label: 'B&W' },
  { id: 'soft', label: 'Soft' },
  { id: 'contrast', label: 'Contrast' },
];

export const CROP_PRESETS: Array<{ id: string; label: string; ratio: number | null }> = [
  { id: 'free', label: 'Free', ratio: null },
  { id: 'original', label: 'Original', ratio: -1 },
  { id: '1:1', label: '1:1', ratio: 1 },
  { id: '4:3', label: '4:3', ratio: 4 / 3 },
  { id: '3:2', label: '3:2', ratio: 3 / 2 },
  { id: '16:9', label: '16:9', ratio: 16 / 9 },
];

/** The CSS `filter` value for a preset plus adjustment sliders. */
export function cssFilter(filter: FilterName, adjustments: Adjustments): string {
  const ops: FilterOp[] = [...(FILTER_PRESETS[filter] ?? [])];
  if (adjustments.brightness) ops.push(['brightness', 1 + adjustments.brightness / 200]);
  if (adjustments.contrast) ops.push(['contrast', 1 + adjustments.contrast / 200]);
  if (adjustments.saturation) ops.push(['saturate', 1 + adjustments.saturation / 100]);
  if (ops.length === 0) return 'none';
  return ops
    .map(([kind, amount]) =>
      kind === 'hue-rotate' ? `hue-rotate(${amount}deg)` : `${kind}(${round(amount)})`,
    )
    .join(' ');
}

function round(v: number): number {
  return Math.round(v * 10000) / 10000;
}

export function isDefaultEdits(e: ImageEdits): boolean {
  return (
    e.crop === null &&
    e.rotation === 0 &&
    e.filter === 'original' &&
    e.adjustments.brightness === 0 &&
    e.adjustments.contrast === 0 &&
    e.adjustments.saturation === 0
  );
}

export function rotateBy(rotation: Rotation, delta: 90 | -90): Rotation {
  return ((((rotation + delta) % 360) + 360) % 360) as Rotation;
}

/** Width/height of the image after rotation (before crop). */
export function rotatedSize(width: number, height: number, rotation: Rotation): [number, number] {
  return rotation === 90 || rotation === 270 ? [height, width] : [width, height];
}

/** Aspect ratio (w/h) of the final, edited image. */
export function editedAspect(
  width: number,
  height: number,
  e: Pick<ImageEdits, 'crop' | 'rotation'>,
): number {
  const [w, h] = rotatedSize(width || 4, height || 3, e.rotation);
  const crop = e.crop ?? { x: 0, y: 0, width: 1, height: 1 };
  const ratio = (crop.width * w) / (crop.height * h);
  return Number.isFinite(ratio) && ratio > 0 ? ratio : 4 / 3;
}

/**
 * Inline styles that show the edited image inside a frame of
 * `editedAspect` ratio. The "rot" box is the full rotated image, offset and
 * enlarged so only the crop rectangle shows; the img inside it is rotated
 * around its center.
 */
export function editLayers(
  width: number,
  height: number,
  e: ImageEdits,
): { rot: React.CSSProperties; img: React.CSSProperties } {
  const crop: Crop = e.crop ?? { x: 0, y: 0, width: 1, height: 1 };
  const w = width || 4;
  const h = height || 3;
  const sideways = e.rotation === 90 || e.rotation === 270;
  const rot: React.CSSProperties = {
    width: `${(100 / crop.width).toFixed(4)}%`,
    height: `${(100 / crop.height).toFixed(4)}%`,
    left: `${((-crop.x / crop.width) * 100).toFixed(4)}%`,
    top: `${((-crop.y / crop.height) * 100).toFixed(4)}%`,
  };
  const filter = cssFilter(e.filter, e.adjustments);
  const img: React.CSSProperties = {
    width: sideways ? `${((w / h) * 100).toFixed(4)}%` : '100%',
    height: sideways ? `${((h / w) * 100).toFixed(4)}%` : '100%',
    transform: `translate(-50%, -50%) rotate(${e.rotation}deg)`,
    ...(filter !== 'none' && { filter }),
  };
  return { rot, img };
}

/** Clamps a crop rectangle to the unit square with a minimum size. */
export function clampCrop(c: Crop, min = 0.02): Crop {
  const width = Math.max(min, Math.min(1, c.width));
  const height = Math.max(min, Math.min(1, c.height));
  const x = Math.max(0, Math.min(1 - width, c.x));
  const y = Math.max(0, Math.min(1 - height, c.y));
  return { x, y, width, height };
}

/**
 * The largest centered crop with `ratio` (w/h, in pixels) inside an image of
 * the given rotated pixel size.
 */
export function centeredCrop(ratio: number, rotatedW: number, rotatedH: number): Crop {
  const imageRatio = rotatedW / rotatedH;
  if (ratio > imageRatio) {
    const height = imageRatio / ratio;
    return { x: 0, y: (1 - height) / 2, width: 1, height };
  }
  const width = ratio / imageRatio;
  return { x: (1 - width) / 2, y: 0, width, height: 1 };
}

/** Drops a crop that covers the whole frame (it is no crop). */
export function normalizeCrop(c: Crop | null): Crop | null {
  if (!c) return null;
  if (c.x < 1e-4 && c.y < 1e-4 && c.width > 1 - 1e-4 && c.height > 1 - 1e-4) return null;
  return c;
}

/**
 * Pure operations on a memory's block list.
 *
 * Every function returns a new array and never mutates its input, so the
 * editor can keep an undo history of structural changes by reference and
 * React can diff blocks cheaply. IDs are minted client-side (the API accepts
 * them), which lets a block be addressed — focused, moved, saved — before the
 * server has ever seen it.
 */

import type {
  FilterName,
  ImageBlock,
  ImageEdits,
  LayoutId,
  MediaView,
  MemoryBlock,
  MemoryImage,
  TextBlock,
} from './types';

/** A random 32-hex-character id, the same shape the server mints. */
export function newId(): string {
  const bytes = new Uint8Array(16);
  if (globalThis.crypto?.getRandomValues) {
    globalThis.crypto.getRandomValues(bytes);
  } else {
    for (let i = 0; i < bytes.length; i += 1) bytes[i] = Math.floor(Math.random() * 256);
  }
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

export const DEFAULT_EDITS: ImageEdits = {
  crop: null,
  rotation: 0,
  filter: 'original',
  adjustments: { brightness: 0, contrast: 0, saturation: 0 },
};

export function newTextBlock(markdown = ''): TextBlock {
  return { id: newId(), type: 'text', markdown };
}

/** A file picked from the library, enough to show it before the next save. */
export interface PickedMedia {
  id: string;
  name?: string;
  media_type?: string;
  mime_type?: string;
  thumbnail_url?: string;
  original_url?: string;
  width?: number;
  height?: number;
}

export function imageFromMedia(media: PickedMedia): MemoryImage {
  const view: MediaView = {
    available: true,
    status: 'present',
    ...(media.name !== undefined && { name: media.name }),
    ...(media.media_type !== undefined && { media_type: media.media_type }),
    ...(media.mime_type !== undefined && { mime_type: media.mime_type }),
    ...(media.thumbnail_url !== undefined && { thumbnail_url: media.thumbnail_url }),
    ...(media.original_url !== undefined && { original_url: media.original_url }),
    ...(media.width !== undefined && { width: media.width }),
    ...(media.height !== undefined && { height: media.height }),
  };
  return {
    id: newId(),
    file_id: media.id,
    caption: '',
    ...structuredCloneEdits(DEFAULT_EDITS),
    edited: false,
    media: view,
    derived: null,
  };
}

export function newImageBlock(layout: LayoutId, media: PickedMedia[] = []): ImageBlock {
  return {
    id: newId(),
    type: 'image',
    layout,
    slideshow: { enabled: false, interval_seconds: null },
    images: media.map(imageFromMedia),
  };
}

function structuredCloneEdits(e: ImageEdits): ImageEdits {
  return {
    crop: e.crop ? { ...e.crop } : null,
    rotation: e.rotation,
    filter: e.filter,
    adjustments: { ...e.adjustments },
  };
}

export function insertBlock(
  blocks: MemoryBlock[],
  index: number,
  block: MemoryBlock,
): MemoryBlock[] {
  const at = Math.max(0, Math.min(index, blocks.length));
  return [...blocks.slice(0, at), block, ...blocks.slice(at)];
}

export function removeBlock(blocks: MemoryBlock[], id: string): MemoryBlock[] {
  return blocks.filter((b) => b.id !== id);
}

/** Moves a block by `delta` positions (−1 up, +1 down), clamped. */
export function moveBlock(blocks: MemoryBlock[], id: string, delta: number): MemoryBlock[] {
  const from = blocks.findIndex((b) => b.id === id);
  if (from < 0) return blocks;
  return moveBlockTo(blocks, id, from + delta);
}

/** Moves a block so it ends up at index `to` (clamped). */
export function moveBlockTo(blocks: MemoryBlock[], id: string, to: number): MemoryBlock[] {
  const from = blocks.findIndex((b) => b.id === id);
  if (from < 0) return blocks;
  const target = Math.max(0, Math.min(to, blocks.length - 1));
  if (target === from) return blocks;
  const next = blocks.slice();
  const [block] = next.splice(from, 1);
  next.splice(target, 0, block!);
  return next;
}

/**
 * Duplicates a block right after itself. An image block keeps its references
 * and configuration — the copy points at the same originals — but every block
 * and image gets a fresh id, and derived copies are not shared.
 */
export function duplicateBlock(blocks: MemoryBlock[], id: string): MemoryBlock[] {
  const at = blocks.findIndex((b) => b.id === id);
  if (at < 0) return blocks;
  const source = blocks[at]!;
  const copy: MemoryBlock =
    source.type === 'text'
      ? { ...source, id: newId() }
      : {
          ...source,
          id: newId(),
          slideshow: { ...source.slideshow },
          images: source.images.map((img) => ({
            ...img,
            ...structuredCloneEdits(img),
            id: newId(),
            derived: null,
          })),
        };
  return insertBlock(blocks, at + 1, copy);
}

export function updateBlock<T extends MemoryBlock>(
  blocks: MemoryBlock[],
  id: string,
  change: (block: T) => T,
): MemoryBlock[] {
  return blocks.map((b) => (b.id === id ? change(b as T) : b));
}

export function setMarkdown(blocks: MemoryBlock[], id: string, markdown: string): MemoryBlock[] {
  return updateBlock<TextBlock>(blocks, id, (b) =>
    b.markdown === markdown ? b : { ...b, markdown },
  );
}

/** Adds images to an existing image block (never creates a new block). */
export function addImages(
  blocks: MemoryBlock[],
  blockId: string,
  media: PickedMedia[],
  index?: number,
): MemoryBlock[] {
  return updateBlock<ImageBlock>(blocks, blockId, (b) => {
    const at =
      index === undefined ? b.images.length : Math.max(0, Math.min(index, b.images.length));
    return {
      ...b,
      images: [...b.images.slice(0, at), ...media.map(imageFromMedia), ...b.images.slice(at)],
    };
  });
}

/** Removes the image reference from the memory. The original is untouched. */
export function removeImage(blocks: MemoryBlock[], imageId: string): MemoryBlock[] {
  return blocks.map((b) =>
    b.type === 'image' && b.images.some((i) => i.id === imageId)
      ? { ...b, images: b.images.filter((i) => i.id !== imageId) }
      : b,
  );
}

export function updateImage(
  blocks: MemoryBlock[],
  imageId: string,
  change: (img: MemoryImage) => MemoryImage,
): MemoryBlock[] {
  return blocks.map((b) =>
    b.type === 'image' && b.images.some((i) => i.id === imageId)
      ? { ...b, images: b.images.map((i) => (i.id === imageId ? change(i) : i)) }
      : b,
  );
}

/** Moves an image within its block to index `to` (clamped). */
export function moveImageTo(images: MemoryImage[], imageId: string, to: number): MemoryImage[] {
  const from = images.findIndex((i) => i.id === imageId);
  if (from < 0) return images;
  const target = Math.max(0, Math.min(to, images.length - 1));
  if (target === from) return images;
  const next = images.slice();
  const [img] = next.splice(from, 1);
  next.splice(target, 0, img!);
  return next;
}

export function findImage(
  blocks: MemoryBlock[],
  imageId: string,
): { block: ImageBlock; image: MemoryImage; index: number } | null {
  for (const b of blocks) {
    if (b.type !== 'image') continue;
    const index = b.images.findIndex((i) => i.id === imageId);
    if (index >= 0) return { block: b, image: b.images[index]!, index };
  }
  return null;
}

/** Replaces an image's source while keeping its caption and place. */
export function replaceImageSource(img: MemoryImage, media: PickedMedia): MemoryImage {
  const fresh = imageFromMedia(media);
  return { ...fresh, id: img.id, caption: img.caption };
}

export function isFilterName(value: string): value is FilterName {
  return ['original', 'warm', 'cool', 'bw', 'soft', 'contrast'].includes(value);
}

/**
 * Normalizes a memory loaded from the API: older or partial payloads may omit
 * fields the editor relies on (empty image arrays, missing slideshow, edits).
 */
export function normalizeBlocks(blocks: MemoryBlock[] | undefined): MemoryBlock[] {
  return (blocks ?? []).map((b) => {
    if (b.type === 'text') return { id: b.id, type: 'text', markdown: b.markdown ?? '' };
    return {
      ...b,
      layout: b.layout ?? 'grid',
      slideshow: b.slideshow ?? { enabled: false, interval_seconds: null },
      images: (b.images ?? []).map((img) => ({
        ...img,
        caption: img.caption ?? '',
        crop: img.crop ?? null,
        rotation: (img.rotation ?? 0) as MemoryImage['rotation'],
        filter: isFilterName(img.filter ?? '') ? img.filter : 'original',
        adjustments: img.adjustments ?? { brightness: 0, contrast: 0, saturation: 0 },
        derived: img.derived ?? null,
        media: img.media ?? { available: false, status: 'unknown' },
      })),
    };
  });
}

/**
 * Applies the server's authoritative view of each image (media availability,
 * URLs, derived copies) to the local document without discarding anything
 * the user changed while the save was in flight.
 */
export function mergeServerViews(local: MemoryBlock[], server: MemoryBlock[]): MemoryBlock[] {
  const views = new Map<
    string,
    Pick<MemoryImage, 'media' | 'derived' | 'file_id'> & { sig: string }
  >();
  for (const b of server) {
    if (b.type !== 'image') continue;
    for (const img of b.images) {
      views.set(img.id, {
        media: img.media,
        derived: img.derived,
        file_id: img.file_id,
        sig: editSignature(img),
      });
    }
  }
  return local.map((b) => {
    if (b.type !== 'image') return b;
    let changed = false;
    const images = b.images.map((img) => {
      const view = views.get(img.id);
      if (!view || view.file_id !== img.file_id) return img;
      // A derived copy only applies while it matches the local edits.
      const derived = view.sig === editSignature(img) ? view.derived : null;
      if (img.media === view.media && img.derived === derived) return img;
      changed = true;
      return { ...img, media: view.media, derived };
    });
    return changed ? { ...b, images } : b;
  });
}

export function editSignature(e: ImageEdits): string {
  const c = e.crop
    ? [e.crop.x, e.crop.y, e.crop.width, e.crop.height].map((v) => v.toFixed(5)).join(',')
    : 'none';
  const a = e.adjustments;
  return `${c}|${e.rotation}|${e.filter}|${a.brightness},${a.contrast},${a.saturation}`;
}

export function blockImageCount(blocks: MemoryBlock[]): number {
  return blocks.reduce((n, b) => n + (b.type === 'image' ? b.images.length : 0), 0);
}

export function wordCount(blocks: MemoryBlock[]): number {
  return blocks.reduce((n, b) => {
    if (b.type !== 'text') return n;
    const t = b.markdown.trim();
    return n + (t === '' ? 0 : t.split(/\s+/).length);
  }, 0);
}

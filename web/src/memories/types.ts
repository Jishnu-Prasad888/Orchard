/**
 * The notebook memory model as the API serves it (docs/memories.md).
 *
 * A memory is an ordered list of blocks. A text block holds Markdown; an image
 * block holds a layout, slideshow settings and an ordered list of image
 * references. Each reference points at an original in the library and carries
 * its own caption and non-destructive edits, so the same photo can appear in
 * many memories with different captions and crops.
 */

export type LayoutId = 'grid' | 'hero' | 'masonry' | 'two_column' | 'filmstrip' | 'featured';

export type FilterName = 'original' | 'warm' | 'cool' | 'bw' | 'soft' | 'contrast';

export type Rotation = 0 | 90 | 180 | 270;

/** A crop rectangle in normalized (0..1) coordinates of the rotated image. */
export interface Crop {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** Slider values in [-100, 100]; 0 is neutral. */
export interface Adjustments {
  brightness: number;
  contrast: number;
  saturation: number;
}

export interface ImageEdits {
  crop: Crop | null;
  rotation: Rotation;
  filter: FilterName;
  adjustments: Adjustments;
}

/**
 * What the viewer may know about the referenced original. Only a present,
 * permitted file carries URLs; anything else renders "Image unavailable" while
 * keeping its caption and place.
 */
export interface MediaView {
  available: boolean;
  status: 'present' | 'missing' | 'deleted' | 'forbidden' | 'unknown' | string;
  name?: string;
  media_type?: string;
  mime_type?: string;
  width?: number;
  height?: number;
  thumbnail_url?: string;
  original_url?: string;
}

/** A server-rendered edited copy (only when "create edited copies" is on). */
export interface DerivedView {
  id: string;
  url: string;
  width: number;
  height: number;
}

export interface MemoryImage extends ImageEdits {
  id: string;
  file_id: string;
  caption: string;
  edited?: boolean;
  media: MediaView;
  derived: DerivedView | null;
}

export interface SlideshowSettings {
  enabled: boolean;
  /** Seconds; null inherits the viewer's "Memory slideshow interval". */
  interval_seconds: number | null;
}

export interface TextBlock {
  id: string;
  type: 'text';
  markdown: string;
}

export interface ImageBlock {
  id: string;
  type: 'image';
  layout: LayoutId;
  slideshow: SlideshowSettings;
  images: MemoryImage[];
}

export type MemoryBlock = TextBlock | ImageBlock;

export interface MemoryDocument {
  id: string;
  title: string;
  body: string;
  memory_date?: string;
  description: string;
  location: string;
  cover_file_id?: string;
  cover?: MediaView;
  tags: string[];
  revision: number;
  deleted: boolean;
  created_at: string;
  updated_at: string;
  blocks: MemoryBlock[];
}

/** A list entry: a memory without its blocks. */
export type MemorySummary = Omit<MemoryDocument, 'blocks'> & { blocks?: MemoryBlock[] };

export interface MemoryResponse {
  memory: MemoryDocument;
  warnings?: string[];
}

export interface MemorySettings {
  slideshow_interval: number;
  edited_copies: boolean;
  default_layout: LayoutId;
  default_mode: 'edit' | 'preview';
  autosave: boolean;
}

export const DEFAULT_MEMORY_SETTINGS: MemorySettings = {
  slideshow_interval: 15,
  edited_copies: false,
  default_layout: 'grid',
  default_mode: 'edit',
  autosave: true,
};

/** Metadata fields PATCH /memories/{id} accepts. `null` clears. */
export interface MemoryMetaPatch {
  title?: string;
  description?: string;
  location?: string;
  memory_date?: string | null;
  cover_file_id?: string | null;
  tags?: string[];
}

/** One saved revision, with its blocks, for previewing and restoring. */
export interface MemoryVersionDetail {
  memory_id: string;
  version: number;
  title: string;
  body: string;
  saved_at: string;
  blocks?: MemoryBlock[];
}

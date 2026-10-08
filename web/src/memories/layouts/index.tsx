/* eslint-disable react-refresh/only-export-components --
   The registry (LAYOUTS, layoutDef) deliberately lives beside the renderers it
   lists, so a new layout is one self-contained edit. */

/**
 * Image block layouts.
 *
 * A layout is a small, self-contained renderer registered here; the editor,
 * the layout picker and the reader all go through {@link LayoutRenderer}, so
 * adding a layout is one entry in {@link LAYOUTS} plus its CSS — nothing in
 * the memory page changes. Each layout decides how every image is fit and
 * where its caption goes, and adapts per breakpoint in memories.css rather
 * than shrinking the desktop arrangement (e.g. masonry → one column, featured
 * → full-width lead plus a two-up grid on phones).
 */

import { type MouseEvent, type ReactNode, useRef } from 'react';

import { type CaptionMode, type ImageFit, MemoryImageView } from '../MemoryImageView';
import type { LayoutId, MemoryImage } from '../types';

export interface LayoutProps {
  images: MemoryImage[];
  /** Larger, eager images for the reader; thumbnails while editing. */
  reading: boolean;
  onImageContextMenu?: (image: MemoryImage, event: MouseEvent<HTMLElement>) => void;
  onImageActivate?: (image: MemoryImage) => void;
}

interface LayoutDef {
  id: LayoutId;
  label: string;
  description: string;
  /** A tiny schematic used by the layout picker. */
  icon: ReactNode;
  Component: (props: LayoutProps) => ReactNode;
}

function item(
  props: LayoutProps,
  image: MemoryImage,
  fit: ImageFit,
  quality: 'thumb' | 'full',
  caption: CaptionMode,
  index: number,
) {
  return (
    <MemoryImageView
      key={image.id}
      image={image}
      fit={fit}
      quality={quality}
      caption={caption}
      eager={props.reading && index < 2}
      {...(props.onImageContextMenu && {
        onContextMenu: (e: MouseEvent<HTMLElement>) => props.onImageContextMenu!(image, e),
      })}
      {...(props.onImageActivate && { onActivate: () => props.onImageActivate!(image) })}
    />
  );
}

function HeroLayout(props: LayoutProps) {
  return (
    <div className="layout layout-hero">
      {props.images.map((img, i) => item(props, img, 'natural', 'full', 'below', i))}
    </div>
  );
}

function GridLayout(props: LayoutProps) {
  const cols = Math.min(3, Math.max(1, props.images.length));
  return (
    <div className={`layout layout-grid layout-cols-${cols}`}>
      {props.images.map((img, i) => item(props, img, 'cover', 'thumb', 'overlay', i))}
    </div>
  );
}

function MasonryLayout(props: LayoutProps) {
  return (
    <div className="layout layout-masonry">
      {props.images.map((img, i) => item(props, img, 'natural', 'thumb', 'overlay', i))}
    </div>
  );
}

function TwoColumnLayout(props: LayoutProps) {
  return (
    <div className="layout layout-two-column">
      {props.images.map((img, i) =>
        item(props, img, 'natural', props.reading ? 'full' : 'thumb', 'overlay', i),
      )}
    </div>
  );
}

function FilmstripLayout(props: LayoutProps) {
  const track = useRef<HTMLDivElement | null>(null);
  const scroll = (dir: -1 | 1) => {
    const el = track.current;
    if (!el) return;
    el.scrollBy({ left: dir * el.clientWidth * 0.8, behavior: 'smooth' });
  };
  return (
    <div className="layout layout-filmstrip">
      <div
        ref={track}
        className="filmstrip-track"
        tabIndex={0}
        role="group"
        aria-label={`Filmstrip, ${props.images.length} photos. Scroll sideways to see them all.`}
      >
        {props.images.map((img, i) => item(props, img, 'natural', 'thumb', 'overlay', i))}
      </div>
      {props.images.length > 1 && (
        <div className="filmstrip-controls">
          <button
            type="button"
            className="icon-button"
            aria-label="Scroll filmstrip left"
            onClick={() => scroll(-1)}
          >
            ‹
          </button>
          <button
            type="button"
            className="icon-button"
            aria-label="Scroll filmstrip right"
            onClick={() => scroll(1)}
          >
            ›
          </button>
        </div>
      )}
    </div>
  );
}

function FeaturedLayout(props: LayoutProps) {
  const [lead, ...rest] = props.images;
  if (!lead) return null;
  return (
    <div className={`layout layout-featured layout-featured-${Math.min(rest.length, 3)}`}>
      <div className="featured-lead">
        {item(
          props,
          lead,
          rest.length ? 'cover' : 'natural',
          'full',
          rest.length ? 'overlay' : 'below',
          0,
        )}
      </div>
      {rest.map((img, i) => item(props, img, 'cover', 'thumb', 'overlay', i + 1))}
    </div>
  );
}

const svg = (children: ReactNode) => (
  <svg viewBox="0 0 32 24" width="44" height="33" aria-hidden="true" className="layout-icon">
    {children}
  </svg>
);

export const LAYOUTS: LayoutDef[] = [
  {
    id: 'grid',
    label: 'Grid',
    description: 'Equal tiles, up to three across',
    icon: svg(
      <>
        <rect x="1" y="1" width="9" height="10" rx="1" />
        <rect x="11.5" y="1" width="9" height="10" rx="1" />
        <rect x="22" y="1" width="9" height="10" rx="1" />
        <rect x="1" y="13" width="9" height="10" rx="1" />
        <rect x="11.5" y="13" width="9" height="10" rx="1" />
        <rect x="22" y="13" width="9" height="10" rx="1" />
      </>,
    ),
    Component: GridLayout,
  },
  {
    id: 'hero',
    label: 'Hero',
    description: 'One large image at a time',
    icon: svg(<rect x="1" y="1" width="30" height="22" rx="1.5" />),
    Component: HeroLayout,
  },
  {
    id: 'masonry',
    label: 'Masonry',
    description: 'Columns that keep each photo’s shape',
    icon: svg(
      <>
        <rect x="1" y="1" width="9" height="14" rx="1" />
        <rect x="1" y="17" width="9" height="6" rx="1" />
        <rect x="11.5" y="1" width="9" height="7" rx="1" />
        <rect x="11.5" y="10" width="9" height="13" rx="1" />
        <rect x="22" y="1" width="9" height="10" rx="1" />
        <rect x="22" y="13" width="9" height="10" rx="1" />
      </>,
    ),
    Component: MasonryLayout,
  },
  {
    id: 'two_column',
    label: 'Two column',
    description: 'Generous pairs, side by side',
    icon: svg(
      <>
        <rect x="1" y="1" width="14" height="22" rx="1" />
        <rect x="17" y="1" width="14" height="22" rx="1" />
      </>,
    ),
    Component: TwoColumnLayout,
  },
  {
    id: 'filmstrip',
    label: 'Filmstrip',
    description: 'A sideways sequence to scroll through',
    icon: svg(
      <>
        <rect x="-4" y="5" width="10" height="14" rx="1" />
        <rect x="8" y="5" width="16" height="14" rx="1" />
        <rect x="26" y="5" width="10" height="14" rx="1" />
      </>,
    ),
    Component: FilmstripLayout,
  },
  {
    id: 'featured',
    label: 'Featured',
    description: 'One lead photo with smaller ones beside it',
    icon: svg(
      <>
        <rect x="1" y="1" width="19" height="22" rx="1" />
        <rect x="22" y="1" width="9" height="10" rx="1" />
        <rect x="22" y="13" width="9" height="10" rx="1" />
      </>,
    ),
    Component: FeaturedLayout,
  },
];

export function layoutDef(id: LayoutId): LayoutDef {
  return LAYOUTS.find((l) => l.id === id) ?? LAYOUTS[0]!;
}

export function LayoutRenderer({ layout, ...props }: LayoutProps & { layout: LayoutId }) {
  const { Component } = layoutDef(layout);
  return <Component {...props} />;
}

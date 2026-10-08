/**
 * Lightbox — "Open image": one memory image, large, with its caption and
 * the rest of its section a key press away.
 */

import { useEffect, useRef, useState } from 'react';

import { trapTab } from '../lib/focusTrap';
import { MemoryImageView } from './MemoryImageView';
import type { MemoryImage } from './types';

interface Props {
  images: MemoryImage[];
  startId: string | null;
  onClose: () => void;
}

export function Lightbox({ images, startId, onClose }: Props) {
  if (startId === null || images.length === 0) return null;
  return <LightboxPanel key={startId} images={images} startId={startId} onClose={onClose} />;
}

function LightboxPanel({ images, startId, onClose }: Props & { startId: string }) {
  const [index, setIndex] = useState(() =>
    Math.max(
      0,
      images.findIndex((i) => i.id === startId),
    ),
  );
  const panel = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    panel.current?.focus();
    return () => previous?.focus?.();
  }, []);

  const image = images[Math.min(index, images.length - 1)]!;
  const go = (d: number) => setIndex((i) => (i + d + images.length) % images.length);

  return (
    <div
      ref={panel}
      className="lightbox"
      role="dialog"
      aria-modal="true"
      aria-label={image.caption || image.media.name || 'Photo'}
      tabIndex={-1}
      onKeyDown={(e) => {
        if (e.key === 'Escape') onClose();
        else if (e.key === 'ArrowLeft') go(-1);
        else if (e.key === 'ArrowRight') go(1);
        else trapTab(panel.current, e);
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      data-testid="lightbox"
    >
      <button
        type="button"
        className="lightbox-close icon-button"
        aria-label="Close"
        onClick={onClose}
      >
        ×
      </button>
      <div className="lightbox-stage">
        <MemoryImageView
          key={image.id}
          image={image}
          fit="contain"
          quality="full"
          caption="below"
          eager
        />
      </div>
      {images.length > 1 && (
        <div className="lightbox-nav">
          <button
            type="button"
            className="icon-button"
            aria-label="Previous photo"
            onClick={() => go(-1)}
          >
            ‹
          </button>
          <span aria-live="polite">
            {index + 1} / {images.length}
          </span>
          <button
            type="button"
            className="icon-button"
            aria-label="Next photo"
            onClick={() => go(1)}
          >
            ›
          </button>
        </div>
      )}
      {image.media.original_url && (
        <a
          className="lightbox-original"
          href={image.media.original_url}
          target="_blank"
          rel="noopener noreferrer"
        >
          Open original
        </a>
      )}
    </div>
  );
}

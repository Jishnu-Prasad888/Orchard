/**
 * Slideshow — an image block shown one photo at a time.
 *
 * Behaviour (docs/memories.md, "Slideshow"):
 *
 *  - Advances automatically every `interval` seconds (the block's override,
 *    or the viewer's "Memory slideshow interval" setting, default 15 s).
 *  - Manual navigation (buttons, arrow keys, swipe) moves immediately and
 *    restarts the countdown, so a photo you chose is shown for a full
 *    interval; it does not stop the show.
 *  - Pauses while the pointer is over it or focus is inside it (so it never
 *    moves under someone reading a caption), while the page is hidden, and
 *    while it is scrolled out of view — no timers run for invisible shows.
 *  - The Pause/Play button stops and resumes it for good.
 *  - With prefers-reduced-motion, slides change without a crossfade.
 */

import { type KeyboardEvent, type MouseEvent, useEffect, useRef, useState } from 'react';

import { MemoryImageView } from './MemoryImageView';
import type { MemoryImage } from './types';

interface Props {
  images: MemoryImage[];
  interval: number;
  label: string;
  onImageContextMenu?: (image: MemoryImage, event: MouseEvent<HTMLElement>) => void;
}

export function Slideshow({ images, interval, label, onImageContextMenu }: Props) {
  const [index, setIndex] = useState(0);
  const [playing, setPlaying] = useState(true);
  const [held, setHeld] = useState({ hover: false, focus: false });
  const [visible, setVisible] = useState(true);
  const [pageVisible, setPageVisible] = useState(() =>
    typeof document === 'undefined' ? true : document.visibilityState !== 'hidden',
  );
  const [announce, setAnnounce] = useState(false);
  const root = useRef<HTMLDivElement | null>(null);
  const swipe = useRef<number | null>(null);

  const count = images.length;
  const current = Math.min(index, Math.max(0, count - 1));

  useEffect(() => {
    const onVisibility = () => setPageVisible(document.visibilityState !== 'hidden');
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, []);

  useEffect(() => {
    const el = root.current;
    if (!el || typeof IntersectionObserver === 'undefined') return;
    const io = new IntersectionObserver(
      (entries) => setVisible(entries.some((e) => e.isIntersecting)),
      {
        threshold: 0.25,
      },
    );
    io.observe(el);
    return () => io.disconnect();
  }, []);

  const running = playing && !held.hover && !held.focus && visible && pageVisible && count > 1;

  // One timer per shown slide: navigating (manually or automatically)
  // re-arms it, which is what restarts the countdown.
  useEffect(() => {
    if (!running) return;
    const t = setTimeout(
      () => {
        setAnnounce(false);
        setIndex((i) => (i + 1) % count);
      },
      Math.max(1, interval) * 1000,
    );
    return () => clearTimeout(t);
  }, [running, current, interval, count]);

  const go = (delta: number) => {
    if (count === 0) return;
    setAnnounce(true);
    setIndex((i) => (((Math.min(i, count - 1) + delta) % count) + count) % count);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'ArrowLeft') {
      e.preventDefault();
      go(-1);
    } else if (e.key === 'ArrowRight') {
      e.preventDefault();
      go(1);
    }
  };

  if (count === 0) return null;
  const image = images[current]!;

  return (
    <div
      ref={root}
      className="slideshow"
      role="region"
      aria-roledescription="carousel"
      aria-label={label}
      tabIndex={0}
      onKeyDown={onKeyDown}
      onMouseEnter={() => setHeld((h) => ({ ...h, hover: true }))}
      onMouseLeave={() => setHeld((h) => ({ ...h, hover: false }))}
      onFocus={() => setHeld((h) => ({ ...h, focus: true }))}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null))
          setHeld((h) => ({ ...h, focus: false }));
      }}
      data-testid="slideshow"
    >
      <div
        className="slideshow-stage"
        aria-live={announce ? 'polite' : 'off'}
        onPointerDown={(e) => {
          swipe.current = e.clientX;
        }}
        onPointerUp={(e) => {
          if (swipe.current === null) return;
          const dx = e.clientX - swipe.current;
          swipe.current = null;
          if (Math.abs(dx) > 40) go(dx < 0 ? 1 : -1);
        }}
      >
        <div
          key={image.id}
          className="slideshow-slide"
          role="group"
          aria-roledescription="slide"
          aria-label={`${current + 1} of ${count}`}
        >
          <MemoryImageView
            image={image}
            fit="contain"
            quality="full"
            caption="below"
            eager
            {...(onImageContextMenu && {
              onContextMenu: (e: MouseEvent<HTMLElement>) => onImageContextMenu(image, e),
            })}
          />
        </div>
      </div>
      {count > 1 && (
        <div className="slideshow-controls">
          <button
            type="button"
            className="icon-button"
            aria-label="Previous photo"
            onClick={() => go(-1)}
          >
            ‹
          </button>
          <span className="slideshow-count" aria-hidden="true">
            {current + 1} / {count}
          </span>
          <button
            type="button"
            className="icon-button slideshow-play"
            aria-label={playing ? 'Pause slideshow' : 'Play slideshow'}
            aria-pressed={!playing}
            onClick={() => setPlaying((p) => !p)}
          >
            {playing ? '❚❚' : '▶'}
          </button>
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
    </div>
  );
}

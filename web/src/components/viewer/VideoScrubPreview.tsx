/**
 * A small frame preview above a video's timeline.
 *
 * The seek bar belongs to the browser's own video controls, so the page cannot
 * see hovers on it. Instead this watches the pointer over the strip along the
 * bottom of the video and *estimates* the hovered time from its x position;
 * the estimate is only approximate because each browser lays its controls out
 * a little differently. After the pointer rests there for a second, a second,
 * muted <video> of the same file seeks to that time and its frame is shown.
 *
 * Nothing involves the server. If the browser cannot decode the video's picture
 * (HEVC on most Linux browsers, for example) the preview simply never appears.
 */

import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';

const HOVER_DELAY_MS = 1000;
const STRIP_HEIGHT = 48; // bottom area of the video that holds the controls
const BAR_LEFT = 130; // play button + time, before the seek bar starts
const BAR_RIGHT = 150; // volume, fullscreen and menu, after it ends
const MOVE_SLOP = 6;
const PREVIEW_WIDTH = 176;

interface Props {
  video: HTMLVideoElement | null;
  src: string;
}

interface Shown {
  x: number;
  y: number;
  time: number;
}

function formatTime(t: number): string {
  const s = Math.max(0, Math.floor(t));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, '0');
  return h ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
}

export function VideoScrubPreview({ video, src }: Props) {
  const [shown, setShown] = useState<Shown | null>(null);
  const holder = useRef<HTMLDivElement | null>(null);
  const preview = useRef<HTMLVideoElement | null>(null);
  const supported = useRef<boolean | null>(null); // null until the first frame is decoded

  useEffect(() => {
    if (!video) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let last = { x: -100, y: -100 };
    let dead = false;

    const clear = () => {
      if (timer) clearTimeout(timer);
      timer = undefined;
      setShown(null);
    };

    // The preview element is created on first use, so a viewer that never
    // hovers the timeline never opens a second connection to the file.
    const ensurePreview = () => {
      if (preview.current || dead) return preview.current;
      const p = document.createElement('video');
      p.muted = true;
      p.preload = 'auto';
      p.playsInline = true;
      p.className = 'scrub-preview-video';
      p.onerror = () => {
        supported.current = false;
        setShown(null);
      };
      p.onloadeddata = () => {
        supported.current = p.videoWidth > 0 && p.videoHeight > 0;
        if (!supported.current) setShown(null);
      };
      p.src = src;
      preview.current = p;
      return p;
    };

    const show = (clientX: number, rect: DOMRect) => {
      if (supported.current === false) return;
      const dur = video.duration;
      // A video the browser plays without a picture cannot be previewed either.
      if (!Number.isFinite(dur) || dur <= 0 || video.videoWidth === 0) return;
      const left = rect.left + BAR_LEFT;
      const right = rect.right - BAR_RIGHT;
      if (right <= left) return;
      const ratio = Math.min(1, Math.max(0, (clientX - left) / (right - left)));
      const time = ratio * dur;
      const p = ensurePreview();
      if (!p) return;
      if (p.readyState >= 1) p.currentTime = time;
      else p.onloadedmetadata = () => (p.currentTime = time);
      const half = PREVIEW_WIDTH / 2;
      const x = Math.min(window.innerWidth - half - 8, Math.max(half + 8, clientX));
      setShown({ x, y: rect.bottom - STRIP_HEIGHT - 8, time });
    };

    const onMove = (e: MouseEvent) => {
      const rect = video.getBoundingClientRect();
      const inStrip =
        e.clientY >= rect.bottom - STRIP_HEIGHT &&
        e.clientY <= rect.bottom &&
        e.clientX >= rect.left + BAR_LEFT &&
        e.clientX <= rect.right - BAR_RIGHT;
      if (!inStrip) {
        clear();
        return;
      }
      if (Math.hypot(e.clientX - last.x, e.clientY - last.y) > MOVE_SLOP) {
        last = { x: e.clientX, y: e.clientY };
        if (timer) clearTimeout(timer);
        // Once it is showing, follow the pointer straight away.
        const wait = holder.current?.dataset.open === 'true' ? 0 : HOVER_DELAY_MS;
        timer = setTimeout(() => show(e.clientX, rect), wait);
      }
    };

    video.addEventListener('mousemove', onMove);
    video.addEventListener('mouseleave', clear);
    video.addEventListener('play', clear);
    video.addEventListener('seeking', clear);
    return () => {
      dead = true;
      video.removeEventListener('mousemove', onMove);
      video.removeEventListener('mouseleave', clear);
      video.removeEventListener('play', clear);
      video.removeEventListener('seeking', clear);
      if (timer) clearTimeout(timer);
      if (preview.current) {
        preview.current.removeAttribute('src');
        preview.current.load();
        preview.current = null;
      }
    };
  }, [video, src]);

  // Mount the preview's own <video> inside the popup while it is visible.
  useEffect(() => {
    const el = holder.current;
    const p = preview.current;
    if (el && p && !el.contains(p)) el.prepend(p);
  }, [shown]);

  if (!shown) return null;
  return createPortal(
    <div
      ref={holder}
      className="scrub-preview"
      data-open="true"
      style={{ left: shown.x, top: shown.y, width: PREVIEW_WIDTH }}
      aria-hidden="true"
    >
      <span className="scrub-preview-time">{formatTime(shown.time)}</span>
    </div>,
    // Browser fullscreen only shows the fullscreen element's subtree.
    document.fullscreenElement ?? document.body,
  );
}

/**
 * VideoThumb — a video's first frame.
 *
 * The server serves a thumbnail when it can extract one (ffmpeg). When it
 * cannot, the image 404s and this component draws the first frame of the
 * video itself into a canvas, shows it, and offers it back to the server
 * (PUT .../thumbnail) so every later viewer gets it for free.
 *
 * Captured frames are kept for the page's lifetime, so scrolling a virtualized
 * grid never decodes the same video twice, and at most two videos are decoded
 * at once so a screen full of clips does not stall the browser.
 */

import { useEffect, useState } from 'react';

import { API_BASE } from '../../api/client';
import { downloadUrl, thumbnailUrl } from '../media';

interface Props {
  libraryId: string;
  fileId: string;
  className?: string;
  onReady?: () => void;
}

const MAX_CONCURRENT = 2;
const CAPTURE_TIMEOUT_MS = 30000;

/** Object URLs of frames drawn in this tab, by `library/file`. */
const captured = new Map<string, string>();
const inflight = new Map<string, Promise<string | null>>();
const waiting: Array<() => void> = [];
let running = 0;

function drawFrame(video: HTMLVideoElement): Promise<Blob | null> {
  const w = video.videoWidth;
  const h = video.videoHeight;
  if (!w || !h) return Promise.resolve(null);
  const scale = Math.min(1, 800 / Math.max(w, h));
  const canvas = document.createElement('canvas');
  canvas.width = Math.round(w * scale);
  canvas.height = Math.round(h * scale);
  const ctx = canvas.getContext('2d');
  if (!ctx) return Promise.resolve(null);
  try {
    ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
  } catch {
    return Promise.resolve(null);
  }
  return new Promise((resolve) => canvas.toBlob((b) => resolve(b), 'image/jpeg', 0.85));
}

function captureFrame(src: string): Promise<Blob | null> {
  return new Promise((resolve) => {
    const video = document.createElement('video');
    let settled = false;
    let drawing = false;
    const finish = (blob: Blob | null) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      video.removeAttribute('src');
      video.load();
      resolve(blob);
    };
    const draw = () => {
      if (drawing || settled) return;
      drawing = true;
      void drawFrame(video).then(finish);
    };
    const timer = setTimeout(() => finish(null), CAPTURE_TIMEOUT_MS);

    video.muted = true;
    video.defaultMuted = true;
    video.playsInline = true;
    video.preload = 'auto';
    video.onerror = () => finish(null);
    video.onseeked = draw;
    // Some encoders start on a black frame, so look a moment in. If the
    // browser never reports the seek, `canplay` draws whatever frame it has.
    video.onloadedmetadata = () => {
      const d = Number.isFinite(video.duration) ? video.duration : 0;
      try {
        video.currentTime = Math.min(0.1, d / 2);
      } catch {
        /* canplay will draw instead */
      }
    };
    video.oncanplay = () => setTimeout(draw, 400);
    video.src = src;
    video.load();
  });
}

function acquire(): Promise<void> {
  if (running < MAX_CONCURRENT) {
    running += 1;
    return Promise.resolve();
  }
  return new Promise((resolve) => waiting.push(resolve));
}

function release() {
  const next = waiting.shift();
  if (next) next();
  else running -= 1;
}

function capture(libraryId: string, fileId: string): Promise<string | null> {
  const key = `${libraryId}/${fileId}`;
  const done = captured.get(key);
  if (done) return Promise.resolve(done);
  let p = inflight.get(key);
  if (!p) {
    p = (async () => {
      await acquire();
      try {
        const blob = await captureFrame(downloadUrl(libraryId, { id: fileId }));
        if (!blob) return null;
        const url = URL.createObjectURL(blob);
        captured.set(key, url);
        // Best effort: viewers without edit rights simply keep their local copy.
        void fetch(`${API_BASE}/libraries/${libraryId}/files/${fileId}/thumbnail`, {
          method: 'PUT',
          headers: { 'Content-Type': 'image/jpeg' },
          body: blob,
        }).catch(() => {});
        return url;
      } finally {
        release();
        inflight.delete(key);
      }
    })();
    inflight.set(key, p);
  }
  return p;
}

type Phase = 'server' | 'capturing' | 'local' | 'none';

export function VideoThumb(props: Props) {
  // Keyed so a different file starts from the server thumbnail again.
  return <VideoThumbInner key={`${props.libraryId}/${props.fileId}`} {...props} />;
}

function VideoThumbInner({ libraryId, fileId, className, onReady }: Props) {
  const cached = captured.get(`${libraryId}/${fileId}`) ?? null;
  const [phase, setPhase] = useState<Phase>(cached ? 'local' : 'server');
  const [local, setLocal] = useState<string | null>(cached);

  // The server had no thumbnail: draw the first frame here.
  useEffect(() => {
    if (phase !== 'capturing') return;
    let cancelled = false;
    void capture(libraryId, fileId).then((url) => {
      if (cancelled) return;
      if (url) {
        setLocal(url);
        setPhase('local');
      } else {
        setPhase('none');
      }
    });
    return () => {
      cancelled = true;
    };
  }, [phase, libraryId, fileId]);

  if (phase === 'capturing' || phase === 'none') {
    // Capturing is "processing": the preview is being drawn. 'none' means the
    // browser cannot decode this video (e.g. HEVC) and the server has no ffmpeg.
    const processing = phase === 'capturing';
    return (
      <span
        className={`video-thumb-empty ${className ?? ''}`}
        role="status"
        title={processing ? 'Processing preview…' : 'No preview available'}
      >
        <span className="video-thumb-hint">{processing ? 'Processing…' : 'No preview'}</span>
      </span>
    );
  }
  return (
    <img
      className={className}
      src={phase === 'local' && local ? local : thumbnailUrl(libraryId, { id: fileId })}
      alt=""
      loading="lazy"
      decoding="async"
      draggable={false}
      onLoad={(e) => {
        e.currentTarget.dataset.loaded = 'true';
        onReady?.();
      }}
      onError={() => {
        if (phase === 'server') setPhase('capturing');
        else setPhase('none');
      }}
    />
  );
}

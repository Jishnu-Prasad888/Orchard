/**
 * MemoryThumbnail — a small, true-to-life version of a memory, for its card.
 *
 * The list API carries no blocks, so the document is fetched when the card
 * scrolls into view (once per revision, then cached) and drawn as a miniature
 * page: title, date, the opening text and the first photos. The page is laid
 * out at a fixed reading width and scaled down to the card, so wrapping and
 * proportions match the real thing. It is static on purpose — no slideshow,
 * no video element, no interaction — and hidden from assistive tech, because
 * the card already carries the title and date as text.
 */

import { useEffect, useMemo, useState } from 'react';

import { VideoThumb } from '../components/media/VideoThumb';
import { renderMarkdown } from '../lib/markdown';
import { getMemoryDocument } from './api';
import { formatMemoryDate } from './format';
import { MemoryImageView } from './MemoryImageView';
import type { ImageBlock, MemoryBlock, MemoryDocument, MemoryImage } from './types';

/** Width the page is laid out at before scaling, in px. */
const PAGE_WIDTH = 640;
const MAX_IMAGES_PER_SECTION = 4;

const cache = new Map<string, MemoryDocument>();
const pending = new Map<string, Promise<MemoryDocument | null>>();

function load(
  libraryId: string,
  memoryId: string,
  revision: number,
): Promise<MemoryDocument | null> {
  const key = `${libraryId}/${memoryId}@${revision}`;
  const hit = cache.get(key);
  if (hit) return Promise.resolve(hit);
  let p = pending.get(key);
  if (!p) {
    p = getMemoryDocument(libraryId, memoryId)
      .then((r) => {
        cache.set(key, r.memory);
        return r.memory;
      })
      .catch(() => null)
      .finally(() => pending.delete(key));
    pending.set(key, p);
  }
  return p;
}

function usable(images: MemoryImage[]): MemoryImage[] {
  return images.filter((i) => i.media.available).slice(0, MAX_IMAGES_PER_SECTION);
}

function Section({ block, libraryId }: { block: ImageBlock; libraryId: string }) {
  // A section with an unreachable photo is not shown in the memory either.
  if (block.images.some((i) => !i.media.available)) return null;
  const images = usable(block.images);
  if (images.length === 0) return null;
  return (
    <div className={`mm-images mm-images-${Math.min(images.length, 4)}`}>
      {images.map((img) =>
        img.media.media_type === 'video' ? (
          <span key={img.id} className="mm-video">
            <VideoThumb libraryId={libraryId} fileId={img.file_id} className="mm-video-img" />
          </span>
        ) : (
          <MemoryImageView key={img.id} image={img} fit="cover" quality="thumb" caption="none" />
        ),
      )}
    </div>
  );
}

function Page({ doc, libraryId }: { doc: MemoryDocument; libraryId: string }) {
  const date = formatMemoryDate(doc.memory_date);
  return (
    <div className="mm-page md-render">
      <h3 className="mm-title">{doc.title || 'Untitled memory'}</h3>
      {(date || doc.location) && (
        <p className="mm-dateline">{[date, doc.location].filter(Boolean).join(' · ')}</p>
      )}
      {doc.blocks.map((block: MemoryBlock) =>
        block.type === 'text' ? (
          <TextBlock key={block.id} markdown={block.markdown} />
        ) : (
          <Section key={block.id} block={block} libraryId={libraryId} />
        ),
      )}
    </div>
  );
}

function TextBlock({ markdown }: { markdown: string }) {
  const html = useMemo(() => renderMarkdown(markdown).html, [markdown]);
  if (!markdown.trim()) return null;
  return <div className="mm-text" dangerouslySetInnerHTML={{ __html: html }} />;
}

interface Props {
  libraryId: string;
  memoryId: string;
  revision: number;
  /** Shown until the page has loaded (and if it never does). */
  fallbackCover?: string | undefined;
}

export function MemoryThumbnail({ libraryId, memoryId, revision, fallbackCover }: Props) {
  const [box, setBox] = useState<HTMLElement | null>(null);
  const [width, setWidth] = useState(0);
  // Without IntersectionObserver (old browsers, jsdom) every card just loads.
  const [visible, setVisible] = useState(typeof IntersectionObserver === 'undefined');
  const key = `${libraryId}/${memoryId}@${revision}`;
  const [fetched, setFetched] = useState<{ key: string; doc: MemoryDocument | null } | null>(null);
  const doc = cache.get(key) ?? (fetched?.key === key ? fetched.doc : null);

  // Scale to the card, however wide it is.
  useEffect(() => {
    if (!box) return;
    const measure = () => setWidth(box.getBoundingClientRect().width);
    measure();
    if (typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(measure);
    ro.observe(box);
    return () => ro.disconnect();
  }, [box]);

  // Only cards on screen cost a request.
  useEffect(() => {
    if (!box || visible) return;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setVisible(true);
          io.disconnect();
        }
      },
      { rootMargin: '200px' },
    );
    io.observe(box);
    return () => io.disconnect();
  }, [box, visible]);

  useEffect(() => {
    if (!visible || cache.has(key)) return;
    let cancelled = false;
    void load(libraryId, memoryId, revision).then((d) => {
      if (!cancelled) setFetched({ key, doc: d });
    });
    return () => {
      cancelled = true;
    };
  }, [visible, key, libraryId, memoryId, revision]);

  const scale = width > 0 ? width / PAGE_WIDTH : 0;

  return (
    <span className="mm-thumb" ref={setBox} aria-hidden="true">
      {doc && scale > 0 ? (
        <span
          className="mm-sheet"
          style={{ width: PAGE_WIDTH, transform: `scale(${scale})` }}
          data-testid="memory-thumbnail"
        >
          <Page doc={doc} libraryId={libraryId} />
        </span>
      ) : fallbackCover ? (
        <img className="mm-fallback" src={fallbackCover} alt="" loading="lazy" />
      ) : null}
      <span className="mm-fade" />
    </span>
  );
}

/**
 * MemoryReader — Preview mode: the memory as a personal article.
 *
 * No editing chrome at all: a centered reading column with comfortable
 * measure and rhythm, image sections breaking out wider than the text, and
 * editorial captions. Text blocks render through the same safe Markdown
 * renderer as everywhere else.
 */

import { type MouseEvent, useMemo, useState } from 'react';

import { renderMarkdown } from '../lib/markdown';
import { formatMemoryDate } from './format';
import { ImageBlockView } from './ImageBlockView';
import { Lightbox } from './Lightbox';
import type { MemoryMeta } from './useMemoryDocument';
import type { MemoryBlock, MemoryImage } from './types';

interface Props {
  meta: MemoryMeta;
  blocks: MemoryBlock[];
  defaultInterval: number;
  onImageContextMenu?: (image: MemoryImage, event: MouseEvent<HTMLElement>) => void;
}

function TextBlockReading({ markdown }: { markdown: string }) {
  const html = useMemo(() => renderMarkdown(markdown).html, [markdown]);
  if (!markdown.trim()) return null;
  return <div className="reader-text md-render" dangerouslySetInnerHTML={{ __html: html }} />;
}

export function MemoryReader({ meta, blocks, defaultInterval, onImageContextMenu }: Props) {
  const [open, setOpen] = useState<{ images: MemoryImage[]; id: string } | null>(null);
  const date = formatMemoryDate(meta.memory_date);

  return (
    <article className="reader" aria-labelledby="reader-title" data-testid="memory-reader">
      <header className="reader-header">
        <h1 id="reader-title" className="reader-title">
          {meta.title || 'Untitled memory'}
        </h1>
        {(date || meta.location) && (
          <p className="reader-dateline">
            {date && <time dateTime={meta.memory_date}>{date}</time>}
            {date && meta.location && <span aria-hidden="true"> · </span>}
            {meta.location && <span>{meta.location}</span>}
          </p>
        )}
        {meta.description && <p className="reader-description">{meta.description}</p>}
      </header>

      {blocks.map((block) =>
        block.type === 'text' ? (
          <TextBlockReading key={block.id} markdown={block.markdown} />
        ) : block.images.length > 0 ? (
          <section
            key={block.id}
            className={`reader-images reader-images-${block.layout}`}
            aria-label="Photos"
          >
            <ImageBlockView
              block={block}
              defaultInterval={defaultInterval}
              reading
              {...(onImageContextMenu && { onImageContextMenu })}
              onImageActivate={(image) => setOpen({ images: block.images, id: image.id })}
            />
          </section>
        ) : null,
      )}

      {meta.tags.length > 0 && (
        <footer className="reader-footer">
          <ul className="reader-tags" aria-label="Tags">
            {meta.tags.map((t) => (
              <li key={t}>#{t}</li>
            ))}
          </ul>
        </footer>
      )}

      <Lightbox
        images={open?.images ?? []}
        startId={open?.id ?? null}
        onClose={() => setOpen(null)}
      />
    </article>
  );
}

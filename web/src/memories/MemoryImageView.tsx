/**
 * MemoryImageView — one memory image, rendered with its edits.
 *
 * Edits are presentation: the original is shown through CSS (crop by
 * offsetting an enlarged, rotated layer inside a frame of the edited aspect
 * ratio; filters through `filter:`). When the server has rendered an edited
 * copy it is shown directly instead. Either way the original file is only
 * ever read.
 *
 * Loading is progressive: the cached 400px thumbnail paints first and the
 * original (when asked for with quality="full") fades in over it once loaded.
 * Everything is lazy-loaded.
 *
 * An unavailable original — missing, deleted, or not permitted — keeps its
 * place, its caption and the layout's rhythm and says "Image unavailable".
 */

import { type CSSProperties, type MouseEvent, useState } from 'react';

import { editLayers, editedAspect, isDefaultEdits } from './edits';
import { unavailableReason } from './format';
import type { MemoryImage } from './types';

export type ImageFit = 'natural' | 'cover' | 'contain';
export type CaptionMode = 'below' | 'overlay' | 'none';

interface Props {
  image: MemoryImage;
  fit: ImageFit;
  quality: 'thumb' | 'full';
  caption?: CaptionMode;
  /** Draft edits to preview instead of the saved ones (image editor). */
  override?: Pick<MemoryImage, 'crop' | 'rotation' | 'filter' | 'adjustments'>;
  eager?: boolean;
  className?: string;
  onContextMenu?: (event: MouseEvent<HTMLElement>) => void;
  onActivate?: () => void;
}

export function MemoryImageView({
  image,
  fit,
  quality,
  caption = 'overlay',
  override,
  eager = false,
  className,
  onContextMenu,
  onActivate,
}: Props) {
  const edits = override ?? image;
  const { media } = image;
  const [natural, setNatural] = useState<[number, number] | null>(null);
  const [fullLoaded, setFullLoaded] = useState(false);
  const [fullFailed, setFullFailed] = useState(false);
  const [showCaption, setShowCaption] = useState(false);

  const text = image.caption.trim();
  const label = text || media.name || 'Photo';
  const classes = ['mi', `mi-${fit}`, className].filter(Boolean).join(' ');
  const captionEl =
    text && caption !== 'none' ? (
      <figcaption className={caption === 'below' ? 'mi-caption-below' : 'mi-caption-overlay'}>
        {text}
      </figcaption>
    ) : null;

  if (!media.available) {
    return (
      <figure
        className={`${classes} mi-unavailable`}
        style={{ '--ratio': 4 / 3 } as CSSProperties}
        onContextMenu={onContextMenu}
        data-image-id={image.id}
        data-testid="memory-image-unavailable"
      >
        <div className="mi-tile">
          <div
            className="mi-placeholder"
            role="img"
            aria-label={`Image unavailable${text ? `: ${text}` : ''}`}
          >
            <svg
              viewBox="0 0 24 24"
              width="28"
              height="28"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
              aria-hidden="true"
            >
              <rect x="3" y="5" width="18" height="14" rx="2" />
              <path d="M3 15l5-5 4 4 3-3 6 6M4 4l16 16" />
            </svg>
            <span className="mi-placeholder-title">Image unavailable</span>
            <span className="mi-placeholder-detail">{unavailableReason(media.status)}</span>
          </div>
        </div>
        {text && caption !== 'none' && <figcaption className="mi-caption-below">{text}</figcaption>}
      </figure>
    );
  }

  if (media.media_type === 'video') {
    return (
      <figure
        className={`${classes} mi-video`}
        style={{ '--ratio': 16 / 9 } as CSSProperties}
        onContextMenu={onContextMenu}
        data-image-id={image.id}
      >
        <div className="mi-tile">
          <video
            className="mi-video-el"
            controls
            preload="metadata"
            src={media.original_url}
            {...(media.thumbnail_url ? { poster: media.thumbnail_url } : {})}
            aria-label={label}
          />
        </div>
        {captionEl}
      </figure>
    );
  }

  // A server-rendered copy is used only for the saved edits it was made from.
  const useDerived = image.derived && !override;
  const baseW = natural?.[0] ?? media.width ?? 0;
  const baseH = natural?.[1] ?? media.height ?? 0;
  const ratio = useDerived
    ? image.derived!.width / image.derived!.height
    : editedAspect(baseW, baseH, edits);
  const plain = useDerived || isDefaultEdits(edits);
  const layers = plain ? null : editLayers(baseW, baseH, edits);

  const thumb = media.thumbnail_url;
  const full = useDerived ? image.derived!.url : media.original_url;
  const wantFull = (quality === 'full' || !thumb || useDerived) && full && !fullFailed;

  const onLoadFull = (event: React.SyntheticEvent<HTMLImageElement>) => {
    const el = event.currentTarget;
    if (!useDerived && el.naturalWidth && el.naturalHeight)
      setNatural([el.naturalWidth, el.naturalHeight]);
    setFullLoaded(true);
  };
  const onLoadThumb = (event: React.SyntheticEvent<HTMLImageElement>) => {
    const el = event.currentTarget;
    // Thumbnails keep the original's proportions; use them until the full
    // image (or the index metadata) says otherwise.
    if (!natural && !media.width && el.naturalWidth && el.naturalHeight) {
      setNatural([el.naturalWidth, el.naturalHeight]);
    }
  };

  const imgs = (
    <>
      {thumb && !(wantFull && fullLoaded) && (
        <img
          className={`mi-img mi-thumb${wantFull ? ' mi-blur' : ''}`}
          src={thumb}
          alt={wantFull ? '' : label}
          loading={eager ? 'eager' : 'lazy'}
          decoding="async"
          draggable={false}
          style={layers?.img}
          onLoad={onLoadThumb}
        />
      )}
      {wantFull && (
        <img
          className={`mi-img mi-full${fullLoaded ? ' is-loaded' : ''}`}
          src={full}
          alt={label}
          loading={eager ? 'eager' : 'lazy'}
          decoding="async"
          draggable={false}
          style={useDerived ? undefined : layers?.img}
          onLoad={onLoadFull}
          onError={() => setFullFailed(true)}
        />
      )}
    </>
  );

  return (
    <figure
      className={`${classes}${showCaption ? ' mi-show-caption' : ''}${text ? ' mi-has-caption' : ''}`}
      style={{ '--ratio': ratio } as CSSProperties}
      onContextMenu={onContextMenu}
      data-image-id={image.id}
      onClick={() => {
        if (onActivate) onActivate();
        else if (text && caption === 'overlay') setShowCaption((v) => !v);
      }}
    >
      <div className="mi-tile">
        <div className="mi-frame">
          {plain ? (
            <div className="mi-rot mi-rot-plain">{imgs}</div>
          ) : (
            <div className="mi-rot" style={layers!.rot}>
              {imgs}
            </div>
          )}
        </div>
        {text && caption === 'overlay' && (
          <span className="mi-caption-indicator" aria-hidden="true">
            i
          </span>
        )}
        {caption === 'overlay' && captionEl}
      </div>
      {caption === 'below' && captionEl}
    </figure>
  );
}

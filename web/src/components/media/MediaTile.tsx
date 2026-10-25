/**
 * One square in the media grid.
 *
 * The image is the tile: there is no card around it. Controls appear only
 * when they are useful — the selection circle and caption on hover or focus,
 * or for every tile while a selection is in progress — and a favorite or a
 * video is marked with a small badge that never covers the subject.
 *
 * The caption names the file and lists its tags in small text. A book's
 * title already sits under its cover (MediaGrid draws it), so a book's
 * caption is just its tags. On a phone, holding a tile shows the caption
 * for as long as the finger stays down.
 */

import { memo, useRef, useState } from 'react';
import type { CSSProperties, MouseEvent as ReactMouseEvent, PointerEvent } from 'react';

import type { FileSummary } from '../../api/types';
import { Icon } from '../ui/Icon';
import { hasThumbnail, thumbnailUrl, mediaTypeIcon } from '../media';

const LONG_PRESS_MS = 450;
const NO_TAGS: string[] = [];
const LONG_PRESS_SLOP = 10;

export interface MediaTileProps {
  libraryId: string;
  file: FileSummary;
  index: number;
  style: CSSProperties;
  selected: boolean;
  selectable: boolean;
  /** A selection is in progress: the check is always shown. */
  selecting: boolean;
  favorite: boolean;
  /** 0-100: how much of the book has been read, shown as a bar and a
   * percentage along the bottom of the cover. Defaults to the file's own
   * `progress_percent`; nothing is shown for an unstarted book. */
  progress?: number | undefined;
  /** Roving tab stop: only one tile in the grid is in the Tab order. */
  tabbable: boolean;
  onActivate: (file: FileSummary, index: number, event: ReactMouseEvent) => void;
  onToggleSelect: (file: FileSummary, index: number, range: boolean) => void;
  onFocusTile: (index: number) => void;
  onContextMenu?: ((file: FileSummary, x: number, y: number) => void) | undefined;
  /** Being pointed out (e.g. just uploaded): the card blinks twice. */
  revealed?: boolean;
  /** The grid shows this file's title under the tile, so the caption
   * leaves the name out. */
  titled?: boolean;
  /** The book's title, written on a plain cover when it has no artwork. */
  coverTitle?: string | undefined;
}

function MediaTileImpl({
  libraryId,
  file,
  index,
  style,
  selected,
  selectable,
  selecting,
  favorite,
  progress,
  tabbable,
  onActivate,
  onToggleSelect,
  onFocusTile,
  onContextMenu,
  revealed = false,
  titled = false,
  coverTitle,
}: MediaTileProps) {
  const [failed, setFailed] = useState(false);
  const press = useRef<{ timer: ReturnType<typeof setTimeout>; x: number; y: number } | null>(null);
  const longPressed = useRef(false);
  const [holding, setHolding] = useState(false);

  const isVideo = file.media_type === 'video';
  const tags = file.tags ?? NO_TAGS;
  const hasThumb = hasThumbnail(file) && !failed;
  const percent = Math.min(Math.max(progress ?? file.progress_percent ?? 0, 0), 100);
  // A book that has been opened never reads as 0%, and only a finished one as 100%.
  const percentLabel = percent >= 100 ? 100 : Math.min(Math.max(Math.round(percent), 1), 99);

  const cancelPress = () => {
    if (press.current) clearTimeout(press.current.timer);
    press.current = null;
    setHolding(false);
  };

  // Touch has no hover and no modifier keys, so a long press is how a phone
  // starts a selection.
  const onPointerDown = (event: PointerEvent) => {
    if (event.pointerType !== 'touch') return;
    longPressed.current = false;
    const { clientX: x, clientY: y } = event;
    press.current = {
      x,
      y,
      timer: setTimeout(() => {
        longPressed.current = true;
        // Still down: keep the caption (and its tags) up until release.
        setHolding(true);
        if (selectable) {
          navigator.vibrate?.(10);
          onToggleSelect(file, index, false);
        }
      }, LONG_PRESS_MS),
    };
  };

  const onPointerMove = (event: PointerEvent) => {
    const start = press.current;
    if (!start) return;
    if (
      !longPressed.current &&
      Math.hypot(event.clientX - start.x, event.clientY - start.y) > LONG_PRESS_SLOP
    ) {
      cancelPress();
    }
  };

  const className = [
    'media-tile',
    selected ? 'is-selected' : '',
    selecting ? 'is-selecting' : '',
    hasThumb ? '' : 'is-placeholder',
    !hasThumb && file.media_type === 'document' ? 'is-plain-cover' : '',
    percent > 0 ? 'has-progress' : '',
    revealed ? 'is-revealed' : '',
    holding ? 'is-holding' : '',
    titled ? 'is-titled' : '',
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <div className={className} style={style} data-testid="media-tile">
      <button
        type="button"
        className="media-tile-main"
        tabIndex={tabbable ? 0 : -1}
        data-index={index}
        aria-label={selected ? `${file.name}, selected` : file.name}
        title={file.rel_path}
        onClick={(event) => {
          if (longPressed.current) {
            longPressed.current = false;
            return;
          }
          onActivate(file, index, event);
        }}
        onFocus={() => onFocusTile(index)}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={cancelPress}
        onPointerCancel={cancelPress}
        onContextMenu={(event) => {
          if (!onContextMenu) return;
          event.preventDefault();
          if (longPressed.current) return;
          onContextMenu(file, event.clientX, event.clientY);
        }}
        draggable
        onDragStart={(event) => {
          event.dataTransfer.setData(
            'application/orchard-file',
            JSON.stringify({ id: file.id, rel_path: file.rel_path, name: file.name }),
          );
          event.dataTransfer.effectAllowed = 'copyMove';
        }}
      >
        {hasThumb ? (
          <img
            className="media-tile-img"
            src={thumbnailUrl(libraryId, file)}
            alt=""
            loading="lazy"
            decoding="async"
            draggable={false}
            onLoad={(event) => {
              event.currentTarget.dataset.loaded = 'true';
            }}
            onError={() => setFailed(true)}
          />
        ) : (
          <span className="media-tile-glyph" aria-hidden="true">
            <Icon
              name={failed ? 'image-off' : mediaTypeIcon(file.media_type)}
              size={isVideo ? 28 : 26}
            />
            {file.media_type === 'document' && (
              // A book without a cover gets a plain one with its title, so
              // a shelf of them is still readable.
              <span className="media-tile-cover-title">
                {coverTitle ?? file.name.replace(/\.[^.]+$/, '')}
              </span>
            )}
          </span>
        )}
        {(!titled || tags.length > 0) && (
          <span className="media-tile-caption">
            {!titled && <span className="media-tile-name">{file.name}</span>}
            {tags.length > 0 && (
              <span className="media-tile-tags" data-testid="media-tile-tags">
                {tags.map((tag) => (
                  <span key={tag} className="media-tile-tag">
                    {tag}
                  </span>
                ))}
              </span>
            )}
          </span>
        )}
      </button>

      {isVideo && (
        <span className="media-tile-badge media-tile-video" aria-hidden="true">
          <Icon name="play" size={14} filled />
        </span>
      )}
      {favorite && (
        <span className="media-tile-badge media-tile-favorite" aria-label="Favorite" role="img">
          <Icon name="star" size={14} filled />
        </span>
      )}
      {percent > 0 && (
        <span className="media-tile-progress" data-testid="media-tile-progress">
          <span
            className="media-tile-progress-track"
            role="progressbar"
            aria-label="Reading progress"
            aria-valuenow={percentLabel}
            aria-valuemin={0}
            aria-valuemax={100}
          >
            <span className="media-tile-progress-fill" style={{ width: `${percent}%` }} />
          </span>
          <span className="media-tile-progress-label" aria-hidden="true">
            {percentLabel}%
          </span>
        </span>
      )}

      {selectable && (
        <button
          type="button"
          className="media-tile-check"
          tabIndex={-1}
          aria-label={selected ? `Deselect ${file.name}` : `Select ${file.name}`}
          aria-pressed={selected}
          onClick={(event) => {
            event.stopPropagation();
            onToggleSelect(file, index, event.shiftKey);
          }}
        >
          <Icon name="check" size={14} />
        </button>
      )}
    </div>
  );
}

export const MediaTile = memo(MediaTileImpl);

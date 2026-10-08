/**
 * One square in the media grid.
 *
 * The image is the tile: there is no card around it. Controls appear only
 * when they are useful — the selection circle and caption on hover or focus,
 * or for every tile while a selection is in progress — and a favorite or a
 * video is marked with a small badge that never covers the subject.
 */

import { memo, useRef, useState } from 'react';
import type { CSSProperties, MouseEvent as ReactMouseEvent, PointerEvent } from 'react';

import type { FileSummary } from '../../api/types';
import { Icon } from '../ui/Icon';
import { thumbnailUrl, mediaTypeIcon } from '../media';
import { VideoThumb } from './VideoThumb';

const LONG_PRESS_MS = 450;
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
  /** Roving tab stop: only one tile in the grid is in the Tab order. */
  tabbable: boolean;
  onActivate: (file: FileSummary, index: number, event: ReactMouseEvent) => void;
  onToggleSelect: (file: FileSummary, index: number, range: boolean) => void;
  onFocusTile: (index: number) => void;
  onContextMenu?: ((file: FileSummary, x: number, y: number) => void) | undefined;
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
  tabbable,
  onActivate,
  onToggleSelect,
  onFocusTile,
  onContextMenu,
}: MediaTileProps) {
  const [failed, setFailed] = useState(false);
  const press = useRef<{ timer: ReturnType<typeof setTimeout>; x: number; y: number } | null>(null);
  const longPressed = useRef(false);

  const isVideo = file.media_type === 'video';
  const hasThumb = (file.media_type === 'photo' || isVideo) && !failed;

  const cancelPress = () => {
    if (press.current) clearTimeout(press.current.timer);
    press.current = null;
  };

  // Touch has no hover and no modifier keys, so a long press is how a phone
  // starts a selection.
  const onPointerDown = (event: PointerEvent) => {
    if (event.pointerType !== 'touch' || !selectable) return;
    longPressed.current = false;
    const { clientX: x, clientY: y } = event;
    press.current = {
      x,
      y,
      timer: setTimeout(() => {
        longPressed.current = true;
        press.current = null;
        navigator.vibrate?.(10);
        onToggleSelect(file, index, false);
      }, LONG_PRESS_MS),
    };
  };

  const onPointerMove = (event: PointerEvent) => {
    const start = press.current;
    if (!start) return;
    if (Math.hypot(event.clientX - start.x, event.clientY - start.y) > LONG_PRESS_SLOP) {
      cancelPress();
    }
  };

  const className = [
    'media-tile',
    selected ? 'is-selected' : '',
    selecting ? 'is-selecting' : '',
    hasThumb ? '' : 'is-placeholder',
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
            'application/cairn-file',
            JSON.stringify({ id: file.id, rel_path: file.rel_path, name: file.name }),
          );
          event.dataTransfer.effectAllowed = 'copyMove';
        }}
      >
        {hasThumb && isVideo ? (
          <VideoThumb libraryId={libraryId} fileId={file.id} className="media-tile-img" />
        ) : hasThumb ? (
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
          </span>
        )}
        <span className="media-tile-caption">{file.name}</span>
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

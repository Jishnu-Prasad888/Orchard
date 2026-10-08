/**
 * An album in the grid: the cover is the card. Title and count sit beneath
 * it, quietly, the way a printed album has its label on the spine.
 */

import type { Album } from '../../api/types';
import { formatDate } from '../../lib/dates';
import { thumbnailUrl } from '../media';
import { Icon } from '../ui/Icon';
import './AlbumCard.css';

const numberFormat = new Intl.NumberFormat();

export function AlbumCard({
  album,
  libraryId,
  onOpen,
}: {
  album: Album;
  libraryId: string;
  onOpen: (album: Album) => void;
}) {
  const preview = album.preview_file_id ?? album.cover_file_id;
  const count = album.file_count;
  return (
    <button type="button" className="album-card" onClick={() => onOpen(album)}>
      <span className="album-card-cover">
        {preview ? (
          <img
            src={thumbnailUrl(libraryId, { id: preview })}
            alt=""
            loading="lazy"
            decoding="async"
            onLoad={(event) => {
              event.currentTarget.dataset.loaded = 'true';
            }}
            onError={(event) => {
              event.currentTarget.hidden = true;
            }}
          />
        ) : null}
        <Icon name="album" size={32} className="album-card-placeholder" />
      </span>
      <span className="album-card-name" title={album.name}>
        {album.name}
      </span>
      <span className="album-card-meta">
        {count !== undefined
          ? `${numberFormat.format(count)} ${count === 1 ? 'item' : 'items'}`
          : `Updated ${formatDate(album.updated_at)}`}
      </span>
    </button>
  );
}

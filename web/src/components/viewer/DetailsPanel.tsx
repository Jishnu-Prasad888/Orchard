/**
 * The facts about a file, in the order people look for them: when it was
 * taken, what it is, what took it, where, and where it lives in the library.
 *
 * Only what exists is shown — a screenshot has no camera and no location, and
 * a wall of empty "Camera: —" rows says nothing useful.
 */

import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';

import type { FileMetadata, FileSummary } from '../../api/types';
import { formatBytes } from '../../api/types';
import { formatDateTime } from '../../lib/dates';
import { fileExtension, mediaLabel } from '../media';
import { Icon, type IconName } from '../ui/Icon';

interface Fact {
  icon: IconName;
  primary: ReactNode;
  secondary?: ReactNode;
}

function formatDuration(seconds: number): string {
  const total = Math.round(seconds);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = String(total % 60).padStart(2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${s}` : `${m}:${s}`;
}

function megapixels(width: number, height: number): string {
  const mp = (width * height) / 1_000_000;
  return mp >= 1 ? `${mp.toFixed(mp >= 10 ? 0 : 1)} MP` : '';
}

export function DetailsPanel({
  file,
  metadata,
}: {
  file: FileSummary;
  metadata: FileMetadata | null;
}) {
  const facts: Fact[] = [];

  // When: the capture time if the file carries one, otherwise the file date.
  if (metadata?.taken_at) {
    facts.push({
      icon: 'calendar',
      primary: formatDateTime(metadata.taken_at),
      secondary: 'Taken',
    });
  } else {
    facts.push({ icon: 'calendar', primary: formatDateTime(file.mod_time), secondary: 'Modified' });
  }

  // What: name, size, dimensions or duration.
  const what: string[] = [formatBytes(file.size_bytes)];
  if (metadata?.width && metadata.height) {
    what.unshift(`${metadata.width} × ${metadata.height}px`);
    const mp = megapixels(metadata.width, metadata.height);
    if (mp) what.push(mp);
  }
  if (metadata?.duration_secs != null && metadata.duration_secs > 0) {
    what.unshift(formatDuration(metadata.duration_secs));
  }
  facts.push({
    icon: file.media_type === 'video' ? 'video' : 'photo',
    primary: <span className="viewer-fact-name">{file.name}</span>,
    secondary: (
      <>
        {what.map((part) => (
          <span key={part} className="viewer-fact-part">
            {part}
          </span>
        ))}
        <span className="viewer-fact-part">
          {fileExtension(file.name) || mediaLabel(file.media_type)}
        </span>
      </>
    ),
  });

  const camera = [metadata?.camera_make, metadata?.camera_model].filter(Boolean).join(' ');
  if (camera) facts.push({ icon: 'camera', primary: camera });

  if (metadata?.latitude != null && metadata.longitude != null) {
    const lat = metadata.latitude.toFixed(5);
    const lon = metadata.longitude.toFixed(5);
    facts.push({
      icon: 'pin',
      primary: (
        <a
          href={`https://www.openstreetmap.org/?mlat=${lat}&mlon=${lon}#map=15/${lat}/${lon}`}
          target="_blank"
          rel="noreferrer"
        >
          {lat}, {lon}
        </a>
      ),
      secondary: 'Opens OpenStreetMap',
    });
  }

  facts.push({
    icon: 'folder',
    primary: (
      <Link
        to={file.folder_path ? `/files?folder=${encodeURIComponent(file.folder_path)}` : '/files'}
      >
        {file.folder_path || 'All files'}
      </Link>
    ),
    secondary: 'Folder',
  });

  if (file.status !== 'present') {
    facts.push({ icon: 'alert', primary: file.status, secondary: 'Status' });
  }

  return (
    <div data-testid="viewer-details-panel">
      <ul className="viewer-facts" data-testid={metadata ? 'viewer-details' : undefined}>
        {facts.map((fact, i) => (
          <li key={i} className="viewer-fact">
            <Icon name={fact.icon} className="viewer-fact-icon" />
            <div className="viewer-fact-text">
              <span className="viewer-fact-primary">{fact.primary}</span>
              {fact.secondary && <span className="viewer-fact-secondary">{fact.secondary}</span>}
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}

/**
 * The facts about a file, in the order people look for them: when it was
 * modified, what it is, and where it lives in the library.
 *
 * Only what exists is shown — a file with no extension has no type label to
 * repeat, and a wall of empty rows says nothing useful.
 */

import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';

import type { FileSummary } from '../../api/types';
import { formatBytes } from '../../api/types';
import { formatDateTime } from '../../lib/dates';
import { fileExtension, mediaLabel, mediaTypeIcon } from '../media';
import { Icon, type IconName } from '../ui/Icon';

interface Fact {
  icon: IconName;
  primary: ReactNode;
  secondary?: ReactNode;
}

export function DetailsPanel({ file }: { file: FileSummary }) {
  const facts: Fact[] = [];

  facts.push({ icon: 'calendar', primary: formatDateTime(file.mod_time), secondary: 'Modified' });

  facts.push({
    icon: mediaTypeIcon(file.media_type),
    primary: <span className="viewer-fact-name">{file.name}</span>,
    secondary: (
      <>
        <span className="viewer-fact-part">{formatBytes(file.size_bytes)}</span>
        <span className="viewer-fact-part">
          {fileExtension(file.name) || mediaLabel(file.media_type)}
        </span>
      </>
    ),
  });

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
      <ul className="viewer-facts" data-testid="viewer-details">
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

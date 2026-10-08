/**
 * The compact upload tray: present only while there is something to report.
 *
 * Collapsed, it is one line and a progress bar ("Uploading 14 files · 12
 * complete"); expanded, it lists each file with its own state and a cancel or
 * retry control. It never blocks the page and never covers the bottom
 * navigation.
 */

import { useState } from 'react';

import { formatBytes } from '../../api/types';
import { Icon } from '../ui/Icon';
import { type UploadItem, useUploads } from './UploadProvider';
import './UploadTray.css';

function summary(items: UploadItem[]) {
  const active = items.filter((i) => i.status === 'queued' || i.status === 'uploading');
  const done = items.filter((i) => i.status === 'done').length;
  const failed = items.filter((i) => i.status === 'error').length;
  const total = items.reduce((n, i) => n + (i.status === 'cancelled' ? 0 : i.total), 0);
  const loaded = items.reduce(
    (n, i) => n + (i.status === 'done' ? i.total : i.status === 'cancelled' ? 0 : i.loaded),
    0,
  );
  return { active: active.length, done, failed, fraction: total > 0 ? loaded / total : 0 };
}

export function UploadTray() {
  const { items, cancel, retry, dismissFinished } = useUploads();
  const [expanded, setExpanded] = useState(false);

  if (items.length === 0) return null;

  const { active, done, failed, fraction } = summary(items);
  const title =
    active > 0
      ? `Uploading ${active} ${active === 1 ? 'file' : 'files'}`
      : failed > 0
        ? `${failed} ${failed === 1 ? 'upload' : 'uploads'} failed`
        : `${done} ${done === 1 ? 'upload' : 'uploads'} complete`;

  // The reason for a failure must not hide behind the collapsed list.
  const firstFailure = items.find((i) => i.status === 'error');
  const failureReason = firstFailure
    ? `${firstFailure.file.name}: ${firstFailure.error ?? 'The upload failed.'}`
    : null;

  return (
    <section className="upload-tray" aria-label="Uploads" data-testid="upload-tray">
      <header className="upload-tray-head">
        <div className="upload-tray-title">
          <span className="upload-tray-heading" role="status" aria-live="polite">
            {title}
          </span>
          <span className="upload-tray-sub">
            {done} complete
            {active > 0 && ` · ${active} remaining`}
            {failed > 0 && ` · ${failed} failed`}
          </span>
          {failureReason && !expanded && (
            <span className="upload-tray-sub upload-tray-reason" title={failureReason}>
              {failureReason}
            </span>
          )}
        </div>
        <button
          type="button"
          className="icon-button"
          aria-expanded={expanded}
          aria-controls="upload-tray-list"
          aria-label={expanded ? 'Hide upload details' : 'Show upload details'}
          onClick={() => setExpanded((v) => !v)}
        >
          <Icon name="chevron-down" className={expanded ? 'is-flipped' : undefined} />
        </button>
        {active === 0 && (
          <button
            type="button"
            className="icon-button"
            aria-label="Close uploads"
            onClick={dismissFinished}
          >
            <Icon name="close" />
          </button>
        )}
      </header>

      <div
        className={active > 0 ? 'progress' : 'progress is-complete'}
        role="progressbar"
        aria-label="Upload progress"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(fraction * 100)}
      >
        <div className="progress-fill" style={{ width: `${Math.round(fraction * 100)}%` }} />
      </div>

      {expanded && (
        <ul className="upload-tray-list" id="upload-tray-list">
          {items.map((item) => (
            <li key={item.id} className={`upload-item is-${item.status}`}>
              <Icon
                name={
                  item.status === 'done'
                    ? 'check'
                    : item.status === 'error'
                      ? 'alert'
                      : item.status === 'cancelled'
                        ? 'close'
                        : 'upload'
                }
                size={18}
              />
              <div className="upload-item-text">
                <span className="upload-item-name" title={item.dest}>
                  {item.file.name}
                </span>
                <span className="upload-item-status">
                  {item.status === 'uploading' &&
                    `${formatBytes(item.loaded)} of ${formatBytes(item.total)}`}
                  {item.status === 'queued' && 'Waiting'}
                  {item.status === 'done' && 'Uploaded'}
                  {item.status === 'cancelled' && 'Cancelled'}
                  {item.status === 'error' && (item.error ?? 'Failed')}
                </span>
              </div>
              {(item.status === 'queued' || item.status === 'uploading') && (
                <button
                  type="button"
                  className="icon-button"
                  aria-label={`Cancel ${item.file.name}`}
                  onClick={() => cancel(item.id)}
                >
                  <Icon name="close" size={18} />
                </button>
              )}
              {(item.status === 'error' || item.status === 'cancelled') && (
                <button
                  type="button"
                  className="icon-button"
                  aria-label={`Retry ${item.file.name}`}
                  onClick={() => retry(item.id)}
                >
                  <Icon name="refresh" size={18} />
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

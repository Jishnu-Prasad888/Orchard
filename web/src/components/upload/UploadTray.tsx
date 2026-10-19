/**
 * The compact upload tray: present only while there is something to report.
 *
 * Collapsed, it is one line and a progress bar ("Uploading 14 files · 12
 * complete"); expanded, it lists each file with its own state and a cancel or
 * retry control. It never blocks the page and never covers the bottom
 * navigation.
 *
 * Once something has uploaded, the tray leads to it, the way a file
 * manager's upload panel does: "View" opens the file, and clicking a
 * finished file (or the tray itself) shows it in its folder, its card
 * blinking twice so it is easy to spot.
 */

import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { useLibraries } from '../../api/libraries';
import type { FileSummary } from '../../api/types';
import { formatBytes } from '../../api/types';
import { isReadableDocument } from '../media';
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

function progressText(item: UploadItem) {
  return `${formatBytes(item.loaded)} of ${formatBytes(item.total)}`;
}

function folderQuery(folder: string): string {
  return folder ? `folder=${encodeURIComponent(folder)}&` : '';
}

export function UploadTray() {
  const { items, cancel, retry, dismissFinished } = useUploads();
  const { libraryId } = useLibraries();
  const navigate = useNavigate();
  const [expanded, setExpanded] = useState(false);

  if (items.length === 0) return null;

  // Finished uploads in the library on screen can be opened or shown; one in
  // another library would land on the wrong library's Files page.
  const reachable = (item: UploadItem): item is UploadItem & { uploaded: FileSummary } =>
    item.status === 'done' && !!item.uploaded && item.libraryId === libraryId;
  const finished = items.filter(reachable);
  const latest = finished[finished.length - 1];

  /** Open the file: books in the reader, anything else in the viewer. */
  const view = (file: FileSummary) => {
    if (isReadableDocument(file)) navigate(`/read/${libraryId}/${file.id}`);
    else navigate(`/files?${folderQuery(file.folder_path)}view=${encodeURIComponent(file.id)}`);
  };

  /** Show files in their folder, their cards blinking. Uploads can span
   * folders; the latest one's folder is the one shown. */
  const reveal = (files: FileSummary[]) => {
    const last = files[files.length - 1];
    if (!last) return;
    const here = files.filter((f) => f.folder_path === last.folder_path).map((f) => f.id);
    navigate(`/files?${folderQuery(last.folder_path)}reveal=${here.join(',')}`);
  };

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
  // Nor should a connection problem being ridden out: say why it paused.
  const recovering = items.find((i) => i.status === 'uploading' && i.notice);
  const recoveryNote = recovering ? `${recovering.file.name}: ${recovering.notice}` : null;

  const heading = (
    <>
      <span className="upload-tray-heading" role="status" aria-live="polite">
        {title}
      </span>
      <span className="upload-tray-sub">
        {done} complete
        {active > 0 && ` · ${active} remaining`}
        {failed > 0 && ` · ${failed} failed`}
      </span>
      {recoveryNote && !failureReason && !expanded && (
        <span className="upload-tray-sub upload-tray-notice" title={recoveryNote}>
          {recoveryNote}
        </span>
      )}
      {failureReason && !expanded && (
        <span className="upload-tray-sub upload-tray-reason" title={failureReason}>
          {failureReason}
        </span>
      )}
    </>
  );

  return (
    <section className="upload-tray" aria-label="Uploads" data-testid="upload-tray">
      <header className="upload-tray-head">
        {finished.length > 0 ? (
          <button
            type="button"
            className="upload-tray-title is-actionable"
            onClick={() => reveal(finished.map((i) => i.uploaded))}
            title="Show in folder"
            data-testid="upload-reveal"
          >
            {heading}
          </button>
        ) : (
          <div className="upload-tray-title">{heading}</div>
        )}
        {latest && (
          <button
            type="button"
            className="button ghost-button upload-tray-view"
            onClick={() => view(latest.uploaded)}
            aria-label={`View ${latest.uploaded.name}`}
            data-testid="upload-view"
          >
            View
          </button>
        )}
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
            <li
              key={item.id}
              className={`upload-item is-${item.status}${item.notice ? ' has-notice' : ''}`}
            >
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
              {reachable(item) ? (
                <button
                  type="button"
                  className="upload-item-text is-actionable"
                  onClick={() => reveal([item.uploaded])}
                  title="Show in folder"
                >
                  <span className="upload-item-name" title={item.dest}>
                    {item.file.name}
                  </span>
                  <span className="upload-item-status">
                    {item.status === 'uploading' && (item.notice ?? progressText(item))}
                    {item.status === 'queued' && 'Waiting'}
                    {item.status === 'done' && 'Uploaded'}
                    {item.status === 'cancelled' && 'Cancelled'}
                    {item.status === 'error' && (item.error ?? 'Failed')}
                  </span>
                </button>
              ) : (
                <div className="upload-item-text">
                  <span className="upload-item-name" title={item.dest}>
                    {item.file.name}
                  </span>
                  <span className="upload-item-status">
                    {item.status === 'uploading' && (item.notice ?? progressText(item))}
                    {item.status === 'queued' && 'Waiting'}
                    {item.status === 'done' && 'Uploaded'}
                    {item.status === 'cancelled' && 'Cancelled'}
                    {item.status === 'error' && (item.error ?? 'Failed')}
                  </span>
                </div>
              )}
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
              {reachable(item) && (
                <button
                  type="button"
                  className="button ghost-button upload-item-view"
                  onClick={() => view(item.uploaded)}
                  aria-label={`View ${item.uploaded.name}`}
                >
                  View
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

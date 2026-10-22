/**
 * Looking at a file behind a share link, without leaving the page.
 *
 * Photos, video and audio play in place; PDF, EPUB, Markdown and text open in
 * the same readers the app uses (loaded only when one is needed). Anything
 * else says it has no preview. It works whether or not the link allows
 * downloads: the bytes come from the share's view route, which needs only
 * "view". A password link is unlocked by a cookie the server sets once the
 * password is right, so the page's <img>, <video> and readers need no header.
 *
 * ← and → step through the share's files, Escape closes.
 */

import { Suspense, useEffect, useRef } from 'react';

import { publicShareContentUrl } from '../api/queries';
import { DEFAULT_READER_PREFERENCES, formatBytes } from '../api/types';
import type { FileSummary } from '../api/types';
import { fileExtension, isReadableDocument, mediaTypeIcon } from '../components/media';
import { Icon } from '../components/ui/Icon';
import { LoadingState } from '../components/States';
import { useFocusTrap } from '../lib/focusTrap';
import { lazyPage } from '../lib/lazyPage';
import { useAppTheme } from '../lib/useAppTheme';
import { TextReader } from '../components/reader/TextReader';
import './PublicShareViewer.css';

const PdfReader = lazyPage(() =>
  import('../components/reader/PdfReader').then((m) => ({ default: m.PdfReader })),
);
const EpubReader = lazyPage(() =>
  import('../components/reader/EpubReader').then((m) => ({ default: m.EpubReader })),
);

const noop = () => {};

function previewKind(
  file: FileSummary,
): 'image' | 'video' | 'audio' | 'pdf' | 'epub' | 'text' | null {
  if (file.media_type === 'photo') return 'image';
  if (file.media_type === 'video') return 'video';
  if (file.media_type === 'audio') return 'audio';
  if (!isReadableDocument(file)) return null;
  const ext = fileExtension(file.name).toLowerCase();
  return ext === 'pdf' ? 'pdf' : ext === 'epub' ? 'epub' : 'text';
}

export function PublicShareViewer({
  token,
  files,
  index,
  canDownload,
  downloading,
  onNavigate,
  onDownload,
  onClose,
}: {
  token: string;
  files: FileSummary[];
  index: number;
  canDownload: boolean;
  downloading: boolean;
  onNavigate: (index: number) => void;
  onDownload: (file: FileSummary) => void;
  onClose: () => void;
}) {
  const file = files[index]!;
  const kind = previewKind(file);
  const url = publicShareContentUrl(token, file.id);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const appTheme = useAppTheme();
  const preferences = { ...DEFAULT_READER_PREFERENCES, theme: appTheme };
  const hasPrev = index > 0;
  const hasNext = index < files.length - 1;

  // Arrow keys page through the share, except inside a reader, where they
  // turn the book's own pages.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onClose();
        return;
      }
      if (kind === 'pdf' || kind === 'epub' || kind === 'text') return;
      if (event.key === 'ArrowLeft' && hasPrev) onNavigate(index - 1);
      if (event.key === 'ArrowRight' && hasNext) onNavigate(index + 1);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [kind, index, hasPrev, hasNext, onNavigate, onClose]);

  // Keyboard focus stays in the viewer while it is open, and goes back to
  // whatever opened it afterwards.
  useFocusTrap(panelRef);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    panelRef.current?.focus();
    return () => previous?.focus?.();
  }, []);

  const readerProps = {
    libraryId: '',
    fileId: file.id,
    fileName: file.name,
    contentUrl: url,
    initialLocation: '',
    preferences,
    onProgress: noop,
    onToc: noop,
    onError: noop,
    onSelectionChange: noop,
    highlights: [],
  };

  return (
    <div
      ref={panelRef}
      className="share-viewer"
      role="dialog"
      aria-modal="true"
      aria-label={file.name}
      tabIndex={-1}
      data-testid="share-viewer"
    >
      <header className="share-viewer-bar">
        <button
          type="button"
          className="icon-button"
          onClick={onClose}
          aria-label="Close"
          data-testid="share-viewer-close"
        >
          <Icon name="close" />
        </button>
        <div className="share-viewer-title">
          <span className="share-viewer-name">{file.name}</span>
          <span className="share-viewer-meta">
            {index + 1} of {files.length} · {formatBytes(file.size_bytes)}
          </span>
        </div>
        {canDownload && (
          <button
            type="button"
            className="button"
            onClick={() => onDownload(file)}
            disabled={downloading}
            data-testid="share-viewer-download"
          >
            <Icon name="download" size={18} />
            {downloading ? 'Downloading…' : 'Download'}
          </button>
        )}
      </header>

      <div className={`share-viewer-stage share-viewer-${kind ?? 'none'}`}>
        {kind === 'image' && (
          <img key={file.id} src={url} alt={file.name} className="share-viewer-media" />
        )}
        {kind === 'video' && (
          // A visitor chooses when to play; nothing starts on its own.
          <video key={file.id} src={url} controls playsInline className="share-viewer-media" />
        )}
        {kind === 'audio' && (
          <div className="share-viewer-audio">
            <Icon name="audio" size={48} />
            <audio key={file.id} src={url} controls />
          </div>
        )}
        {(kind === 'pdf' || kind === 'epub' || kind === 'text') && (
          <Suspense fallback={<LoadingState label="Opening…" />}>
            <div className="share-viewer-reader" key={file.id}>
              {kind === 'pdf' && <PdfReader {...readerProps} />}
              {kind === 'epub' && <EpubReader {...readerProps} />}
              {kind === 'text' && <TextReader {...readerProps} />}
            </div>
          </Suspense>
        )}
        {kind === null && (
          <div className="share-viewer-none" data-testid="share-viewer-no-preview">
            <Icon name={mediaTypeIcon(file.media_type)} size={48} />
            <p>There is no preview for this kind of file.</p>
            {canDownload ? (
              <p className="muted">Download it to open it.</p>
            ) : (
              <p className="muted">This link does not allow downloads.</p>
            )}
          </div>
        )}
      </div>

      {hasPrev && (
        <button
          type="button"
          className="share-viewer-step share-viewer-prev"
          onClick={() => onNavigate(index - 1)}
          aria-label="Previous file"
          data-testid="share-viewer-prev"
        >
          <Icon name="chevron-left" />
        </button>
      )}
      {hasNext && (
        <button
          type="button"
          className="share-viewer-step share-viewer-next"
          onClick={() => onNavigate(index + 1)}
          aria-label="Next file"
          data-testid="share-viewer-next"
        >
          <Icon name="chevron-right" />
        </button>
      )}
    </div>
  );
}

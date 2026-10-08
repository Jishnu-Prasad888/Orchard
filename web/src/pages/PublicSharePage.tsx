/**
 * The public view behind a share link — `/s/:token`.
 *
 * This route is deliberately outside the app shell and outside `RequireAuth`:
 * the token in the URL *is* the credential, so requiring a session would make
 * every share link unusable for the people it was made for. The page therefore
 * has no library, no chrome, and no session — just what the share exposes.
 *
 * A share that has a password needs unlocking before the file list is visible.
 * The password travels in the `X-Cairn-Share-Password` header on every request
 * and is kept in component state only; it is never written to storage, because
 * putting a share credential in localStorage outlives the decision to share.
 */

import { useCallback, useEffect, useState } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';

import { ApiError } from '../api/client';
import { downloadPublicShareFile, getPublicShare, listPublicShareFiles } from '../api/queries';
import type { FileSummary, PublicShareInfo } from '../api/types';
import { formatBytes } from '../api/types';
import { mediaTypeIcon } from '../components/media';
import { Icon } from '../components/ui/Icon';
import { EmptyState, ErrorState, LoadingState } from '../components/States';
import './PublicSharePage.css';

type ShareState =
  | { kind: 'loading' }
  | { kind: 'denied'; message: string; needsPassword: boolean }
  | { kind: 'unlocked'; share: PublicShareInfo };

export default function PublicSharePage() {
  const { token = '' } = useParams();
  const [searchParams] = useSearchParams();

  const [password, setPassword] = useState('');
  const [unlocked, setUnlocked] = useState(false);
  const [downloading, setDownloading] = useState<string | null>(null);
  const [downloadError, setDownloadError] = useState<string | null>(null);

  // A share can be scoped to a subfolder; the link the owner copies carries it.
  const folder = searchParams.get('folder') ?? '';

  // Both requests are tagged with what they were for, so a new token or a new
  // password invalidates the previous answer by comparison. That is what keeps
  // a wrong password from leaving the previous share's files on screen while
  // the retry is in flight.
  const shareKey = `${token}|${unlocked ? password : ''}`;
  const [settledShare, setSettledShare] = useState<{
    key: string;
    value: Exclude<ShareState, { kind: 'loading' }>;
  } | null>(null);

  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    getPublicShare(token, unlocked ? password : undefined)
      .then((resp) => {
        if (!cancelled)
          setSettledShare({ key: shareKey, value: { kind: 'unlocked', share: resp.share } });
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        const unauthorized = e instanceof ApiError && e.status === 401;
        setSettledShare({
          key: shareKey,
          value: {
            kind: 'denied',
            message: unauthorized
              ? 'This share needs a password.'
              : e instanceof Error
                ? e.message
                : String(e),
            // A 401 without a password attempted is almost always a wrong or
            // missing password; either way the only useful move is to ask.
            needsPassword: true,
          },
        });
      });
    return () => {
      cancelled = true;
    };
  }, [shareKey, token, unlocked, password]);

  const state: ShareState =
    token === ''
      ? { kind: 'denied', message: 'This link is missing its share token.', needsPassword: false }
      : settledShare?.key === shareKey
        ? settledShare.value
        : { kind: 'loading' };

  // The first page is derived from the fetch; "load more" appends to `extra`.
  const filesKey = state.kind === 'unlocked' ? `${shareKey}|${folder}` : null;
  const [settledFiles, setSettledFiles] = useState<
    | {
        key: string;
        value: { files: FileSummary[]; next?: string | undefined };
      }
    | { key: string; error: string }
    | null
  >(null);
  const [extra, setExtra] = useState<FileSummary[]>([]);
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const [loadingMore, setLoadingMore] = useState(false);

  useEffect(() => {
    if (filesKey === null) return;
    let cancelled = false;
    listPublicShareFiles(token, unlocked ? password : undefined, { folder, limit: 200 })
      .then((page) => {
        if (cancelled) return;
        setSettledFiles({
          key: filesKey,
          value: { files: page.files ?? [], next: page.next_cursor },
        });
        setExtra([]);
        setCursor(page.next_cursor);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setSettledFiles({ key: filesKey, error: e instanceof Error ? e.message : String(e) });
      });
    return () => {
      cancelled = true;
    };
  }, [filesKey, token, password, unlocked, folder]);

  const currentFiles = filesKey !== null && settledFiles?.key === filesKey ? settledFiles : null;
  const files = [
    ...(currentFiles && 'value' in currentFiles ? currentFiles.value.files : []),
    ...extra,
  ];
  const filesError = currentFiles && 'error' in currentFiles ? currentFiles.error : null;
  const filesLoading = (filesKey !== null && currentFiles === null) || loadingMore;

  const loadMore = useCallback(async () => {
    if (filesKey === null || !cursor || loadingMore) return;
    setLoadingMore(true);
    try {
      const page = await listPublicShareFiles(token, unlocked ? password : undefined, {
        folder,
        limit: 200,
        cursor,
      });
      setExtra((prev) => [...prev, ...(page.files ?? [])]);
      setCursor(page.next_cursor);
    } catch {
      // The next cursor is unchanged, so "Load more" stays available.
    } finally {
      setLoadingMore(false);
    }
  }, [filesKey, cursor, loadingMore, token, password, unlocked, folder]);

  const submitPassword = (event: React.FormEvent) => {
    event.preventDefault();
    setUnlocked(true);
  };

  const download = async (file: FileSummary) => {
    setDownloading(file.id);
    setDownloadError(null);
    try {
      await downloadPublicShareFile(token, file.id, file.name, unlocked ? password : undefined);
    } catch (e: unknown) {
      setDownloadError(e instanceof Error ? e.message : String(e));
    } finally {
      setDownloading(null);
    }
  };

  return (
    <main className="share-view" data-testid="public-share-page">
      <header className="share-view-header">
        <span className="brand share-brand">Cairn</span>
        {state.kind === 'unlocked' && state.share.library && (
          <p className="muted">Shared from {state.share.library}</p>
        )}
      </header>

      {state.kind === 'loading' && <LoadingState label="Opening this share…" />}

      {state.kind === 'denied' && (
        <section className="share-gate" aria-labelledby="share-gate-title">
          <h1 id="share-gate-title">This share is locked</h1>
          <p className="muted">{state.message}</p>
          {state.needsPassword && (
            <form className="share-gate-form" onSubmit={submitPassword}>
              <label htmlFor="share-password">Password</label>
              <div className="share-gate-row">
                <input
                  id="share-password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="off"
                  data-testid="share-password-input"
                />
                <button type="submit" className="button primary-button" data-testid="share-unlock">
                  Open
                </button>
              </div>
            </form>
          )}
          <p className="muted share-gate-note">
            Shares are capabilities, not accounts. If this link should not be working, ask the
            person who shared it to make a new one — a lost token cannot be recovered.
          </p>
        </section>
      )}

      {state.kind === 'unlocked' && (
        <>
          <h1 className="share-view-title">
            {state.share.resource_key}
            {folder && <span className="muted"> — {folder}</span>}
          </h1>
          {state.share.expires_at && (
            <p className="muted">
              This link expires on{' '}
              <time dateTime={state.share.expires_at}>
                {new Date(state.share.expires_at).toLocaleDateString()}
              </time>
              .
            </p>
          )}

          {filesError && <ErrorState message={filesError} />}
          {downloadError && (
            <p className="error-text" role="alert">
              {downloadError}
            </p>
          )}
          {filesLoading && files.length === 0 && <LoadingState label="Loading files…" />}

          {files.length === 0 && !filesLoading && !filesError && (
            <EmptyState title="Nothing here" testId="share-empty">
              <p className="muted">This share is empty, or everything in it has been removed.</p>
            </EmptyState>
          )}

          {/* Only rendered once there is something in it: an empty list is
              announced as a section with nothing in it. */}
          {files.length > 0 && (
            <ul className="share-file-list" data-testid="share-file-list">
              {files.map((file) => (
                <li key={file.id} className="share-file">
                  <span className="share-file-glyph" aria-hidden="true">
                    <Icon name={mediaTypeIcon(file.media_type)} />
                  </span>
                  <div className="share-file-meta">
                    <span className="share-file-name">{file.name}</span>
                    <span className="muted">
                      {file.media_type} · {formatBytes(file.size_bytes)}
                    </span>
                  </div>
                  <button
                    type="button"
                    className="button"
                    onClick={() => void download(file)}
                    disabled={downloading === file.id}
                    data-testid={`download-${file.id}`}
                  >
                    {downloading === file.id ? 'Downloading…' : 'Download'}
                  </button>
                </li>
              ))}
            </ul>
          )}

          {cursor && (
            <div className="share-pagination">
              <button
                type="button"
                className="button"
                onClick={() => void loadMore()}
                disabled={filesLoading}
                data-testid="share-load-more"
              >
                {filesLoading ? 'Loading…' : 'Load more'}
              </button>
            </div>
          )}
        </>
      )}
    </main>
  );
}

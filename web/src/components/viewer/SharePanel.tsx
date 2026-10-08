import { useCallback, useState } from 'react';

import { createShare } from '../../api/queries';
import type { FileSummary } from '../../api/types';

/**
 * Sharing. Shares are created at a resource key, and a file's key is
 * `file:<library>/<rel-path>`, so a share here exposes exactly this file.
 */
export function SharePanel({ libraryId, file }: { libraryId: string; file: FileSummary }) {
  const [shareUrl, setShareUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);

  const resourceKey = `file:${libraryId}/${file.rel_path}`;

  const create = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const resp = await createShare(libraryId, { key: resourceKey, caps: ['read'] });
      setShareUrl(`${window.location.origin}/s/${resp.token}`);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, [libraryId, resourceKey]);

  const copy = async () => {
    if (!shareUrl) return;
    try {
      await navigator.clipboard.writeText(shareUrl);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  };

  return (
    <div data-testid="viewer-share">
      <p className="muted">
        A read-only link that anyone with the URL (and the password, if you set one) can open
        without a Cairn account. Manage links from the Sharing page.
      </p>
      <p className="viewer-share-key">
        <code>{resourceKey}</code>
      </p>
      {shareUrl ? (
        <div className="viewer-share-result">
          <label className="visually-hidden" htmlFor="viewer-share-url">
            Share link
          </label>
          <input id="viewer-share-url" className="viewer-tag-input" readOnly value={shareUrl} />
          <button type="button" className="button" onClick={() => void copy()}>
            {copied ? 'Copied' : 'Copy link'}
          </button>
        </div>
      ) : (
        <button
          type="button"
          className="button primary-button"
          onClick={() => void create()}
          disabled={busy}
          data-testid="viewer-create-share"
        >
          {busy ? 'Creating…' : 'Create a read-only link'}
        </button>
      )}
      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

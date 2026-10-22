/**
 * Making a share link: one form, used from the Sharing page, the viewer's
 * Share tab, and "Share…" on the right-click menu of a file, folder, tag or
 * collection.
 *
 * What the link exposes is always a real thing chosen here — the library, a
 * folder, a tag, a collection, or the file it was opened for — never a typed
 * key, and "Create link" stays disabled until something is chosen. The
 * server checks it again and builds the link's scope itself.
 *
 * A link can only let visitors see, and optionally download. The token is
 * shown once, right here, with a copy button: the server never returns it
 * again.
 */

import { useCallback, useEffect, useId, useRef, useState } from 'react';

import { createShare, listCollections, listTags } from '../api/queries';
import type { Collection, Share, ShareTarget, ShareTargetKind, Tag } from '../api/types';
import { Dialog } from './Dialog';
import { FolderPicker } from './FolderPicker';
import { Icon } from './ui/Icon';
import './ShareDialog.css';

/** A target the form was opened for, with the name to show for it. */
export interface FixedShareTarget {
  target: ShareTarget;
  name: string;
}

const MIN_PASSWORD = 8;

const KIND_NOUN: Record<ShareTargetKind, string> = {
  library: 'the whole library',
  folder: 'folder',
  file: 'file',
  tag: 'tag',
  collection: 'collection',
};

const PICKABLE: { kind: Exclude<ShareTargetKind, 'file'>; label: string }[] = [
  { kind: 'folder', label: 'A folder' },
  { kind: 'tag', label: 'A tag' },
  { kind: 'collection', label: 'A collection' },
  { kind: 'library', label: 'The whole library' },
];

function shareUrl(token: string): string {
  return `${window.location.origin}/s/${token}`;
}

/** A datetime-local value for "now", so the expiry picker cannot go back. */
function nowLocal(): string {
  const d = new Date();
  d.setSeconds(0, 0);
  return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
}

export function ShareForm({
  libraryId,
  fixed,
  onCreated,
  onBusyChange,
  footer,
}: {
  libraryId: string;
  /** Share exactly this; omit to let the person choose. */
  fixed?: FixedShareTarget | undefined;
  onCreated?: ((share: Share) => void) | undefined;
  onBusyChange?: ((busy: boolean) => void) | undefined;
  /** Rendered after the link once it exists ("Done"). */
  footer?: ((created: boolean) => React.ReactNode) | undefined;
}) {
  const id = useId();
  const [kind, setKind] = useState<Exclude<ShareTargetKind, 'file'>>('folder');
  const [folder, setFolder] = useState('');
  const [tagId, setTagId] = useState('');
  const [collectionId, setCollectionId] = useState('');
  const [tags, setTags] = useState<Tag[] | null>(null);
  const [collections, setCollections] = useState<Collection[] | null>(null);
  const [download, setDownload] = useState(fixed?.target.kind === 'file');
  const [password, setPassword] = useState('');
  const [expires, setExpires] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [link, setLink] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const linkRef = useRef<HTMLInputElement | null>(null);

  // The pickers' choices, loaded only when the person picks that kind.
  useEffect(() => {
    if (fixed || kind !== 'tag' || tags !== null) return;
    let cancelled = false;
    listTags(libraryId)
      .then((r) => !cancelled && setTags(r.tags ?? []))
      .catch(() => !cancelled && setTags([]));
    return () => {
      cancelled = true;
    };
  }, [fixed, kind, tags, libraryId]);
  useEffect(() => {
    if (fixed || kind !== 'collection' || collections !== null) return;
    let cancelled = false;
    listCollections(libraryId)
      .then((r) => !cancelled && setCollections(r.collections ?? []))
      .catch(() => !cancelled && setCollections([]));
    return () => {
      cancelled = true;
    };
  }, [fixed, kind, collections, libraryId]);

  const target: ShareTarget | null = fixed
    ? fixed.target
    : kind === 'library'
      ? { kind: 'library' }
      : kind === 'folder'
        ? folder.trim().replace(/^\/+|\/+$/g, '')
          ? { kind: 'folder', path: folder.trim().replace(/^\/+|\/+$/g, '') }
          : null
        : kind === 'tag'
          ? tagId
            ? { kind: 'tag', id: tagId }
            : null
          : collectionId
            ? { kind: 'collection', id: collectionId }
            : null;

  const passwordTooShort = password.length > 0 && password.length < MIN_PASSWORD;
  const canCreate = target !== null && !passwordTooShort && !busy;

  const setBusyBoth = useCallback(
    (value: boolean) => {
      setBusy(value);
      onBusyChange?.(value);
    },
    [onBusyChange],
  );

  const submit = () => {
    if (!target || !canCreate) return;
    setBusyBoth(true);
    setError(null);
    createShare(libraryId, {
      target,
      caps: download ? ['read', 'download'] : ['read'],
      ...(password ? { password } : {}),
      ...(expires ? { expires_at: new Date(expires).toISOString() } : {}),
    })
      .then((resp) => {
        setLink(shareUrl(resp.token));
        onCreated?.(resp.share);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusyBoth(false));
  };

  // The async Clipboard API exists only on secure pages, and Orchard is often
  // reached over plain HTTP on a home network; there, copy from the visible
  // field instead. Either way the link stays selected, ready for Ctrl+C.
  const copy = async () => {
    if (!link) return;
    try {
      await navigator.clipboard.writeText(link);
      setCopied(true);
      return;
    } catch {
      // Not available or not allowed: fall through.
    }
    const field = linkRef.current;
    field?.focus();
    field?.select();
    setCopied(document.execCommand?.('copy') ?? false);
  };

  if (link) {
    return (
      <div className="share-form" data-testid="share-created">
        <p className="share-created-title">
          <Icon name="check" size={18} /> Link created
        </p>
        <p className="share-hint">
          Copy it now — Orchard shows it only once. If it is lost, revoke it on the Sharing page and
          make a new one.
        </p>
        <div className="share-link-row">
          <label className="visually-hidden" htmlFor={`${id}-link`}>
            Share link
          </label>
          <input
            id={`${id}-link`}
            className="share-input"
            ref={linkRef}
            // The form that had focus is gone; put it on the link, so it can
            // be copied straight away and Escape still reaches the dialog.
            autoFocus
            readOnly
            value={link}
            onFocus={(e) => e.currentTarget.select()}
            data-testid="share-link"
          />
          <button type="button" className="button primary-button" onClick={() => void copy()}>
            {copied ? 'Copied' : 'Copy link'}
          </button>
        </div>
        {footer?.(true)}
      </div>
    );
  }

  return (
    <form
      className="share-form"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      {fixed ? (
        <p className="share-target" data-testid="share-target">
          Sharing {KIND_NOUN[fixed.target.kind]}{' '}
          {fixed.target.kind !== 'library' && <strong>“{fixed.name}”</strong>}
        </p>
      ) : (
        <fieldset className="share-pick">
          <legend>What to share</legend>
          <div className="share-kinds" role="radiogroup">
            {PICKABLE.map((option) => (
              <label key={option.kind} className="share-kind">
                <input
                  type="radio"
                  name={`${id}-kind`}
                  checked={kind === option.kind}
                  onChange={() => setKind(option.kind)}
                  data-testid={`share-kind-${option.kind}`}
                />
                <span>{option.label}</span>
              </label>
            ))}
          </div>

          {kind === 'folder' && (
            <FolderPicker
              libraryId={libraryId}
              value={folder}
              onChange={setFolder}
              label="Folder"
              placeholder="2024/Japan"
              unavailableNote="Some folders are not listed; type the path to reach one anyway."
              testId="share-folder"
            />
          )}
          {kind === 'tag' && (
            <label className="share-field">
              <span>Tag</span>
              <select
                className="share-input"
                value={tagId}
                onChange={(e) => setTagId(e.target.value)}
                data-testid="share-tag"
              >
                <option value="">{tags === null ? 'Loading…' : 'Choose a tag'}</option>
                {(tags ?? []).map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name}
                  </option>
                ))}
              </select>
            </label>
          )}
          {kind === 'collection' && (
            <label className="share-field">
              <span>Collection</span>
              <select
                className="share-input"
                value={collectionId}
                onChange={(e) => setCollectionId(e.target.value)}
                data-testid="share-collection"
              >
                <option value="">
                  {collections === null ? 'Loading…' : 'Choose a collection'}
                </option>
                {(collections ?? []).map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </label>
          )}
          {kind === 'library' && (
            <p className="share-warning" role="note">
              Anyone with the link sees every file in this library.
            </p>
          )}
          <p className="share-hint">To share one file, right-click it and choose Share….</p>
        </fieldset>
      )}

      <label className="share-check">
        <input
          type="checkbox"
          checked={download}
          onChange={(e) => setDownload(e.target.checked)}
          data-testid="share-allow-download"
        />
        <span>Let visitors download</span>
      </label>
      <p className="share-hint">
        Without it, visitors can see what is shared but not save the files.
      </p>

      <div className="share-form-row">
        <label className="share-field">
          <span>Password (optional)</span>
          <input
            type="password"
            className="share-input"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="new-password"
            aria-invalid={passwordTooShort || undefined}
            data-testid="share-password"
          />
          {passwordTooShort && (
            <span className="share-field-error">At least {MIN_PASSWORD} characters.</span>
          )}
        </label>
        <label className="share-field">
          <span>Expires (optional)</span>
          <input
            type="datetime-local"
            className="share-input"
            value={expires}
            min={nowLocal()}
            onChange={(e) => setExpires(e.target.value)}
            data-testid="share-expires"
          />
        </label>
      </div>

      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}

      <div className="share-actions">
        {footer?.(false)}
        <button
          type="submit"
          className="button primary-button"
          disabled={!canCreate}
          data-testid="confirm-new-share"
        >
          {busy ? 'Creating…' : 'Create link'}
        </button>
      </div>
    </form>
  );
}

/** The form in a dialog, for the Sharing page and right-click menus. */
export function ShareDialog({
  libraryId,
  fixed,
  open,
  onClose,
  onCreated,
}: {
  libraryId: string;
  fixed?: FixedShareTarget | undefined;
  open: boolean;
  onClose: () => void;
  onCreated?: ((share: Share) => void) | undefined;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <Dialog
      open={open}
      size="medium"
      title={fixed ? `Share “${fixed.name}”` : 'Create a share link'}
      onClose={onClose}
      dismissible={!busy}
      testId="share-dialog"
    >
      {open && (
        <ShareForm
          // A new target (or reopening) starts a fresh form.
          key={fixed ? JSON.stringify(fixed.target) : 'pick'}
          libraryId={libraryId}
          fixed={fixed}
          onCreated={onCreated}
          onBusyChange={setBusy}
          footer={(created) => (
            <button
              type="button"
              className={created ? 'button share-done' : 'button'}
              onClick={onClose}
              disabled={busy}
            >
              {created ? 'Done' : 'Cancel'}
            </button>
          )}
        />
      )}
    </Dialog>
  );
}

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { apiDelete, apiGet, apiPut } from '../api/client';
import type { FileNote as FileNoteData } from '../api/types';
import { renderMarkdown } from '../lib/markdown';

interface FileNoteProps {
  libraryId: string;
  fileId: string;
}

type SaveStatus = 'idle' | 'saving' | 'saved' | 'error';

/**
 * A per-file Markdown note, shown as a caption over the bottom of the photo.
 *
 * The note is stored server-side (the file's note endpoint) and autosaves with
 * a short debounce — the same pattern the memories editor uses. Missing notes
 * load as empty and are never an error.
 */
export function FileNote({ libraryId, fileId }: FileNoteProps) {
  const [loaded, setLoaded] = useState(false);
  const [draft, setDraft] = useState('');
  const [editing, setEditing] = useState(false);
  const [status, setStatus] = useState<SaveStatus>('idle');
  const [error, setError] = useState<string | null>(null);

  // Latest draft is always available to the (debounced) autosave timer.
  const draftRef = useRef('');
  useEffect(() => {
    draftRef.current = draft;
  }, [draft]);

  // The last body persisted on the server; the timer skips unchanged drafts.
  const lastSavedRef = useRef('');

  // Load the stored note. The parent keys this component by file id, so
  // changing file remounts it with clean state rather than needing a reset here.
  useEffect(() => {
    let cancelled = false;
    apiGet<{ note: FileNoteData }>(`/libraries/${libraryId}/files/${fileId}/note`)
      .then((resp) => {
        if (cancelled) return;
        lastSavedRef.current = resp.note.body;
        setDraft(resp.note.body);
        setLoaded(true);
      })
      .catch(() => {
        // Notes are optional; a failed read behaves like an empty note.
        if (cancelled) return;
        lastSavedRef.current = '';
        setLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, [libraryId, fileId]);

  const save = useCallback(() => {
    const body = draftRef.current;
    if (body === lastSavedRef.current) return;
    setStatus('saving');
    setError(null);
    apiPut<{ note: FileNoteData }>(`/libraries/${libraryId}/files/${fileId}/note`, { body })
      .then((resp) => {
        lastSavedRef.current = resp.note.body;
        setStatus('saved');
      })
      .catch((e: unknown) => {
        setStatus('error');
        setError(e instanceof Error ? e.message : String(e));
      });
  }, [libraryId, fileId]);

  // Debounced autosave once the note has loaded and differs from the server.
  useEffect(() => {
    if (!loaded) return;
    if (draftRef.current === lastSavedRef.current) return;
    const timer = setTimeout(save, 800);
    return () => clearTimeout(timer);
  }, [draft, loaded, save]);

  const clear = async () => {
    setDraft('');
    setError(null);
    try {
      await apiDelete(`/libraries/${libraryId}/files/${fileId}/note`);
      lastSavedRef.current = '';
      setStatus('saved');
    } catch (e: unknown) {
      setStatus('error');
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const preview = useMemo(() => (draft ? renderMarkdown(draft).html : ''), [draft]);
  const hasNote = draft.trim() !== '';

  // Nothing to show until the stored note has been read, so a caption never
  // flashes in as "Add a caption" over a photo that already has one.
  if (!loaded) return null;

  return (
    <section className="viewer-caption" data-testid="viewer-note">
      {editing ? (
        <div className="viewer-caption-edit">
          <textarea
            className="viewer-note-input"
            aria-label="Markdown note"
            placeholder="Write a caption. Markdown works."
            value={draft}
            onChange={(e) => {
              setDraft(e.target.value);
              setStatus('idle');
            }}
            rows={3}
            autoFocus
          />
          <div className="viewer-caption-actions">
            <span className={`note-status note-status-${status}`} role="status" aria-live="polite">
              {status === 'saving' && 'Saving…'}
              {status === 'saved' && 'Saved'}
              {status === 'error' && 'Save failed'}
            </span>
            {hasNote && (
              <button type="button" className="viewer-caption-button" onClick={() => void clear()}>
                Clear
              </button>
            )}
            <button
              type="button"
              className="viewer-caption-button primary"
              onClick={() => setEditing(false)}
            >
              Preview
            </button>
          </div>
        </div>
      ) : hasNote ? (
        <div className="viewer-caption-view">
          <div
            className="viewer-caption-body"
            aria-label="Markdown note preview"
            dangerouslySetInnerHTML={{ __html: preview }}
          />
          <button type="button" className="viewer-caption-button" onClick={() => setEditing(true)}>
            Edit
          </button>
        </div>
      ) : (
        <button type="button" className="viewer-caption-add" onClick={() => setEditing(true)}>
          Add a caption
        </button>
      )}

      {error && (
        <p className="error-text" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}

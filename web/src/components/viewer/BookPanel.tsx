/**
 * Book details: title/author/publisher/year/ISBN/description, a cover, and
 * a reading status. Shown only for document-type files (the viewer decides
 * that), since this is bibliographic data about a book, not something every
 * file has.
 *
 * Metadata extracted from the file (EPUB/PDF) is editable here — editing
 * marks it "user_edited" server-side, so a later re-scan never silently
 * overwrites what was typed in.
 */

import { type ChangeEvent, type FormEvent, useEffect, useRef, useState } from 'react';

import {
  clearReadingStatus,
  getBookMetadata,
  getReadingStatus,
  setReadingStatus,
  updateBookMetadata,
  uploadCover,
} from '../../api/queries';
import type { BookMetadata, FileSummary, ReadingStatus } from '../../api/types';
import { READING_STATUS_LABEL } from '../../api/types';
import { Icon } from '../ui/Icon';

const READING_STATUS_OPTIONS: ReadingStatus[] = ['want_to_read', 'reading', 'completed', 'on_hold'];

interface FormState {
  title: string;
  author: string;
  publisher: string;
  year: string;
  isbn: string;
  description: string;
}

function formFromMetadata(m: BookMetadata): FormState {
  return {
    title: m.title,
    author: m.author,
    publisher: m.publisher,
    year: m.year,
    isbn: m.isbn,
    description: m.description,
  };
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function BookPanel({
  libraryId,
  file,
  onChanged,
}: {
  libraryId: string;
  file: FileSummary;
  /** Called after the cover or the details are saved, so whatever shows
   * them elsewhere (a grid's thumbnail, a title) can refresh. */
  onChanged?: (() => void) | undefined;
}) {
  const [book, setBook] = useState<BookMetadata | null>(null);
  const [form, setForm] = useState<FormState | null>(null);
  const [status, setStatus] = useState<ReadingStatus | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [saveBusy, setSaveBusy] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const [coverBusy, setCoverBusy] = useState(false);
  const [coverError, setCoverError] = useState<string | null>(null);

  const [statusBusy, setStatusBusy] = useState(false);
  const [statusError, setStatusError] = useState<string | null>(null);

  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    let cancelled = false;
    Promise.all([getBookMetadata(libraryId, file.id), getReadingStatus(libraryId, file.id)])
      .then(([bookResp, statusResp]) => {
        if (cancelled) return;
        setBook(bookResp.book);
        setForm(formFromMetadata(bookResp.book));
        setStatus(statusResp.status);
      })
      .catch((e: unknown) => {
        if (!cancelled) setLoadError(message(e));
      });
    return () => {
      cancelled = true;
    };
  }, [libraryId, file.id]);

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    if (!form) return;
    setSaveBusy(true);
    setSaveError(null);
    setSaved(false);
    updateBookMetadata(libraryId, file.id, form)
      .then((resp) => {
        setBook(resp.book);
        setForm(formFromMetadata(resp.book));
        setSaved(true);
        onChanged?.();
      })
      .catch((e: unknown) => setSaveError(message(e)))
      .finally(() => setSaveBusy(false));
  };

  const onCoverChosen = (event: ChangeEvent<HTMLInputElement>) => {
    const chosen = event.target.files?.[0];
    event.target.value = '';
    if (!chosen) return;
    setCoverBusy(true);
    setCoverError(null);
    uploadCover(libraryId, file.id, chosen)
      .then((resp) => {
        setBook(resp.book);
        onChanged?.();
      })
      .catch((e: unknown) => setCoverError(message(e)))
      .finally(() => setCoverBusy(false));
  };

  const onStatusChange = (next: ReadingStatus | '') => {
    setStatusBusy(true);
    setStatusError(null);
    const call =
      next === ''
        ? clearReadingStatus(libraryId, file.id)
        : setReadingStatus(libraryId, file.id, next);
    call
      .then(() => setStatus(next === '' ? null : next))
      .catch((e: unknown) => setStatusError(message(e)))
      .finally(() => setStatusBusy(false));
  };

  if (loadError) {
    return (
      <p className="error-text" role="alert">
        {loadError}
      </p>
    );
  }
  if (!book || !form) {
    return <p className="muted">Loading book details…</p>;
  }

  return (
    <div data-testid="viewer-book">
      <div className="book-panel-cover-row">
        <div className="book-panel-cover" aria-hidden={!book.cover_url}>
          {book.cover_url ? (
            <img src={book.cover_url} alt="" />
          ) : (
            <Icon name="document" className="book-panel-cover-placeholder" />
          )}
        </div>
        <div className="book-panel-cover-actions">
          <button
            type="button"
            className="button ghost-button"
            onClick={() => fileInputRef.current?.click()}
            disabled={coverBusy}
          >
            {coverBusy ? 'Uploading…' : book.cover_url ? 'Change cover image' : 'Add cover image'}
          </button>
          <p className="book-panel-cover-note muted">JPEG, PNG, WebP or GIF, up to 8 MB.</p>
          <input
            ref={fileInputRef}
            type="file"
            accept="image/jpeg,image/png,image/webp,image/gif"
            className="visually-hidden"
            onChange={onCoverChosen}
            aria-label="Choose a cover image"
          />
          {coverError && (
            <p className="error-text" role="alert">
              {coverError}
            </p>
          )}
        </div>
      </div>

      <label className="field book-panel-status-field">
        <span className="field-label">Reading status</span>
        <select
          value={status ?? ''}
          onChange={(e) => onStatusChange(e.target.value as ReadingStatus | '')}
          disabled={statusBusy}
          data-testid="reading-status-select"
        >
          <option value="">No status</option>
          {READING_STATUS_OPTIONS.map((s) => (
            <option key={s} value={s}>
              {READING_STATUS_LABEL[s]}
            </option>
          ))}
        </select>
      </label>
      {statusError && (
        <p className="error-text" role="alert">
          {statusError}
        </p>
      )}

      <form className="book-panel-form" onSubmit={onSubmit}>
        <label className="field">
          <span className="field-label">Title</span>
          <input
            value={form.title}
            onChange={(e) => setForm({ ...form, title: e.target.value })}
            disabled={saveBusy}
          />
        </label>
        <label className="field">
          <span className="field-label">Author</span>
          <input
            value={form.author}
            onChange={(e) => setForm({ ...form, author: e.target.value })}
            disabled={saveBusy}
          />
        </label>
        <label className="field">
          <span className="field-label">Publisher</span>
          <input
            value={form.publisher}
            onChange={(e) => setForm({ ...form, publisher: e.target.value })}
            disabled={saveBusy}
          />
        </label>
        <div className="book-panel-row">
          <label className="field">
            <span className="field-label">Year</span>
            <input
              value={form.year}
              onChange={(e) => setForm({ ...form, year: e.target.value })}
              disabled={saveBusy}
            />
          </label>
          <label className="field">
            <span className="field-label">ISBN</span>
            <input
              value={form.isbn}
              onChange={(e) => setForm({ ...form, isbn: e.target.value })}
              disabled={saveBusy}
            />
          </label>
        </div>
        <label className="field">
          <span className="field-label">Description</span>
          <textarea
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            disabled={saveBusy}
            rows={4}
          />
        </label>

        {saveError && (
          <p className="error-text" role="alert">
            {saveError}
          </p>
        )}
        <div className="book-panel-save-row">
          <button type="submit" className="button primary-button" disabled={saveBusy}>
            {saveBusy ? 'Saving…' : 'Save'}
          </button>
          {saved && !saveBusy && (
            <span className="settings-created" role="status">
              Saved.
            </span>
          )}
        </div>
      </form>
    </div>
  );
}

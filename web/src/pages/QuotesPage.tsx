/**
 * Quotes — passages saved from books, with the user's own commentary and
 * citation details. Independent of highlights: a quote can be typed in by
 * hand from this page, not only selected while reading.
 */

import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';

import { useAuth } from '../auth/authContext';
import { useFileLabels } from '../api/fileLabels';
import { useLibraryGate } from '../api/libraries';
import { useLibraryResource } from '../api/resources';
import {
  createQuote,
  deleteQuote,
  listFiles,
  listLibraryQuotes,
  updateQuote,
} from '../api/queries';
import type { FileSummary, Quote } from '../api/types';
import { ConfirmDialog, Dialog } from '../components/Dialog';
import {
  EmptyState,
  ErrorState,
  LibraryOfflineNotice,
  LoadingState,
  NoLibrariesState,
  PageHeader,
} from '../components/States';
import { Icon } from '../components/ui/Icon';
import './QuotesPage.css';

function formatDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

function citation(q: Quote, bookTitle: string): string {
  const parts = [q.author || undefined, q.chapter || undefined, q.page ? `p. ${q.page}` : undefined]
    .filter(Boolean)
    .join(', ');
  const source = [bookTitle, parts].filter(Boolean).join(' — ');
  return source ? `“${q.text}”\n— ${source}` : `“${q.text}”`;
}

function quotesToMarkdown(quotes: Quote[], titleFor: (fileId: string) => string): string {
  return quotes
    .map((q) => {
      const lines = [`> ${q.text.replace(/\n/g, '\n> ')}`, ''];
      const cite = [q.author, titleFor(q.file_id), q.chapter, q.page ? `p. ${q.page}` : undefined]
        .filter(Boolean)
        .join(', ');
      if (cite) lines.push(`— ${cite}`);
      if (q.commentary) lines.push('', q.commentary);
      return lines.join('\n');
    })
    .join('\n\n---\n\n');
}

function downloadText(fileName: string, text: string) {
  const blob = new Blob([text], { type: 'text/markdown;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = fileName;
  a.click();
  URL.revokeObjectURL(url);
}

function AddQuoteDialog({
  libraryId,
  onClose,
  onCreated,
}: {
  libraryId: string;
  onClose: () => void;
  onCreated: () => void;
}) {
  const [books, setBooks] = useState<FileSummary[] | null>(null);
  const [fileId, setFileId] = useState('');
  const [text, setText] = useState('');
  const [author, setAuthor] = useState('');
  const [chapter, setChapter] = useState('');
  const [page, setPage] = useState('');
  const [commentary, setCommentary] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    listFiles(libraryId, { type: 'document', recursive: true, limit: 500 })
      .then((resp) => setBooks(resp.files ?? []))
      .catch(() => setBooks([]));
  }, [libraryId]);

  const submit = () => {
    if (!fileId || !text.trim()) return;
    setBusy(true);
    setError(null);
    createQuote(libraryId, fileId, {
      text: text.trim(),
      author: author.trim() || undefined,
      chapter: chapter.trim() || undefined,
      page: page.trim() || undefined,
      commentary: commentary.trim() || undefined,
    })
      .then(() => onCreated())
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };

  return (
    <Dialog
      open
      size="medium"
      title="Save a quote"
      onClose={onClose}
      dismissible={!busy}
      testId="add-quote-dialog"
      footer={
        <>
          <button type="button" className="button" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            type="button"
            className="button primary-button"
            onClick={submit}
            disabled={busy || !fileId || !text.trim()}
            data-testid="confirm-add-quote"
          >
            {busy ? 'Saving…' : 'Save quote'}
          </button>
        </>
      }
    >
      <div className="quote-form">
        <label className="quote-field">
          <span>Book</span>
          <select
            className="quote-input"
            value={fileId}
            onChange={(e) => setFileId(e.target.value)}
            data-testid="quote-book-select"
          >
            <option value="">{books === null ? 'Loading…' : 'Choose a book'}</option>
            {books?.map((f) => (
              <option key={f.id} value={f.id}>
                {f.name}
              </option>
            ))}
          </select>
        </label>
        <label className="quote-field">
          <span>Quote</span>
          <textarea
            className="quote-input quote-textarea"
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={3}
            data-testid="quote-text-input"
          />
        </label>
        <div className="quote-form-row">
          <label className="quote-field">
            <span>Author (optional)</span>
            <input className="quote-input" value={author} onChange={(e) => setAuthor(e.target.value)} />
          </label>
          <label className="quote-field">
            <span>Chapter (optional)</span>
            <input className="quote-input" value={chapter} onChange={(e) => setChapter(e.target.value)} />
          </label>
          <label className="quote-field">
            <span>Page (optional)</span>
            <input className="quote-input" value={page} onChange={(e) => setPage(e.target.value)} />
          </label>
        </div>
        <label className="quote-field">
          <span>Your thoughts (optional)</span>
          <textarea
            className="quote-input quote-textarea"
            value={commentary}
            onChange={(e) => setCommentary(e.target.value)}
            rows={2}
          />
        </label>
        {error && (
          <p className="error-text" role="alert">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  );
}

export default function QuotesPage() {
  const gate = useLibraryGate();
  const { user } = useAuth();
  const libraryId = gate.kind === 'ready' ? gate.libraryId : '';

  const [favoriteOnly, setFavoriteOnly] = useState(false);
  const [adding, setAdding] = useState(false);
  const [deleting, setDeleting] = useState<Quote | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [copiedId, setCopiedId] = useState<string | null>(null);

  const quotes = useLibraryResource(
    useCallback(
      async (id: string) => (await listLibraryQuotes(id, { favoriteOnly })).quotes ?? [],
      [favoriteOnly],
    ),
    [favoriteOnly],
  );

  const fileIds = (quotes.data ?? []).map((q) => q.file_id);
  const labels = useFileLabels(libraryId, fileIds);

  const toggleFavorite = async (q: Quote) => {
    try {
      await updateQuote(libraryId, q.id, { favorite: !q.favorite });
      quotes.reload();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : String(e));
    }
  };

  const copyQuote = async (q: Quote) => {
    try {
      await navigator.clipboard.writeText(citation(q, labels[q.file_id]?.title ?? ''));
      setCopiedId(q.id);
      setTimeout(() => setCopiedId((c) => (c === q.id ? null : c)), 2000);
    } catch {
      setActionError('Could not copy to the clipboard.');
    }
  };

  const exportQuotes = () => {
    const list = quotes.data ?? [];
    downloadText('quotes.md', quotesToMarkdown(list, (id) => labels[id]?.title ?? ''));
  };

  const header = (
    <PageHeader
      title="Quotes"
      subtitle="Passages you've saved, with your own thoughts."
      controls={
        <>
          <button
            type="button"
            className="button"
            onClick={exportQuotes}
            disabled={!quotes.data || quotes.data.length === 0}
            data-testid="export-quotes-button"
          >
            Export
          </button>
          <button
            type="button"
            className="button primary-button"
            onClick={() => setAdding(true)}
            disabled={gate.kind !== 'ready'}
            data-testid="new-quote-button"
          >
            Save a quote
          </button>
        </>
      }
    />
  );

  if (gate.kind === 'loading') {
    return (
      <main className="page quotes-page">
        {header}
        <LoadingState label="Loading libraries…" />
      </main>
    );
  }
  if (gate.kind === 'error') {
    return (
      <main className="page quotes-page">
        {header}
        <ErrorState message={gate.message} onRetry={quotes.reload} />
      </main>
    );
  }
  if (gate.kind === 'empty') {
    return (
      <main className="page quotes-page">
        {header}
        <NoLibrariesState isAdmin={user?.role === 'admin'} />
      </main>
    );
  }

  const library = gate.library;

  return (
    <main className="page quotes-page">
      {header}
      {library.status === 'offline' && <LibraryOfflineNotice library={library} />}
      {actionError && (
        <p className="error-text" role="alert">
          {actionError}
        </p>
      )}

      <label className="quote-filter">
        <input
          type="checkbox"
          checked={favoriteOnly}
          onChange={(e) => setFavoriteOnly(e.target.checked)}
        />
        <span>Favorites only</span>
      </label>

      {quotes.loading && <LoadingState />}
      {quotes.error && <ErrorState message={quotes.error} onRetry={quotes.reload} />}
      {quotes.data !== null && quotes.data.length === 0 && (
        <EmptyState title="No quotes yet" testId="quotes-empty" icon="quote">
          <p>Select text while reading and choose “Save quote”, or add one by hand.</p>
        </EmptyState>
      )}

      {quotes.data !== null && quotes.data.length > 0 && (
        <ul className="quote-list" data-testid="quotes-list">
          {quotes.data.map((q) => {
            const label = labels[q.file_id];
            return (
              <li key={q.id} className="quote-card">
                <blockquote className="quote-card-text">“{q.text}”</blockquote>
                <div className="quote-card-meta">
                  <Link to={`/read/${libraryId}/${q.file_id}${q.location ? `?loc=${encodeURIComponent(q.location)}` : ''}`}>
                    {label?.title ?? '…'}
                  </Link>
                  {q.author && <span className="muted"> · {q.author}</span>}
                  {q.chapter && <span className="muted"> · {q.chapter}</span>}
                  {q.page && <span className="muted"> · p. {q.page}</span>}
                  <span className="muted annotation-card-date"> · {formatDate(q.created_at)}</span>
                </div>
                {q.commentary && <p className="quote-card-commentary">{q.commentary}</p>}
                <div className="quote-card-actions">
                  <button
                    type="button"
                    className={q.favorite ? 'icon-button active' : 'icon-button'}
                    aria-label={q.favorite ? 'Unfavorite' : 'Favorite'}
                    onClick={() => void toggleFavorite(q)}
                  >
                    <Icon name="star" filled={q.favorite} />
                  </button>
                  <button
                    type="button"
                    className="button ghost-button"
                    onClick={() => void copyQuote(q)}
                  >
                    <Icon name="copy" /> {copiedId === q.id ? 'Copied!' : 'Copy with citation'}
                  </button>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label="Delete quote"
                    onClick={() => setDeleting(q)}
                  >
                    <Icon name="trash" />
                  </button>
                </div>
              </li>
            );
          })}
        </ul>
      )}

      {adding && (
        <AddQuoteDialog
          libraryId={libraryId}
          onClose={() => setAdding(false)}
          onCreated={() => {
            setAdding(false);
            quotes.reload();
          }}
        />
      )}

      <ConfirmDialog
        open={deleting !== null}
        title="Delete this quote?"
        message={<p>This quote will be removed. The highlight or passage it came from, if any, is not affected.</p>}
        destructive
        confirmLabel="Delete quote"
        busy={busy}
        error={actionError}
        onCancel={() => setDeleting(null)}
        onConfirm={() => {
          if (!deleting) return;
          const target = deleting;
          setBusy(true);
          setActionError(null);
          deleteQuote(libraryId, target.id)
            .then(() => {
              setDeleting(null);
              quotes.reload();
            })
            .catch((e: unknown) => setActionError(e instanceof Error ? e.message : String(e)))
            .finally(() => setBusy(false));
        }}
        testId="delete-quote-dialog"
      />
    </main>
  );
}

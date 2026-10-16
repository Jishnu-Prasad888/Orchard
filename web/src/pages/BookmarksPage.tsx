/**
 * Bookmarks & Highlights — everything a user has marked across every book in
 * the library, in one place. Opening an entry deep-links into the reader at
 * that exact location via the shared `?loc=` query param (see ReaderPage).
 */

import { useCallback } from 'react';
import { Link } from 'react-router-dom';

import { useAuth } from '../auth/authContext';
import { useFileLabels } from '../api/fileLabels';
import { useLibraryGate } from '../api/libraries';
import { useLibraryResource } from '../api/resources';
import { deleteBookmark, deleteHighlight, listLibraryBookmarks, listLibraryHighlights } from '../api/queries';
import {
  EmptyState,
  ErrorState,
  LibraryOfflineNotice,
  LoadingState,
  NoLibrariesState,
  PageHeader,
} from '../components/States';
import { matchesQuery, usePageQuery } from '../components/app-shell/pageSearch';
import { Icon } from '../components/ui/Icon';
import './BookmarksPage.css';

function formatDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

export default function BookmarksPage() {
  const gate = useLibraryGate();
  const { user } = useAuth();
  const libraryId = gate.kind === 'ready' ? gate.libraryId : '';

  const bookmarks = useLibraryResource(
    useCallback(async (id: string) => (await listLibraryBookmarks(id)).bookmarks ?? [], []),
  );
  const highlights = useLibraryResource(
    useCallback(async (id: string) => (await listLibraryHighlights(id)).highlights ?? [], []),
  );

  const fileIds = [
    ...(bookmarks.data ?? []).map((b) => b.file_id),
    ...(highlights.data ?? []).map((h) => h.file_id),
  ];
  const labels = useFileLabels(libraryId, fileIds);

  // The top-bar search filters both lists in place (see pageSearch.ts).
  const pageQuery = usePageQuery();
  const shownBookmarks =
    bookmarks.data?.filter((b) => matchesQuery(pageQuery, b.label, labels[b.file_id]?.title)) ??
    null;
  const shownHighlights =
    highlights.data?.filter((h) =>
      matchesQuery(pageQuery, h.text, h.note, labels[h.file_id]?.title),
    ) ?? null;
  const noMatch = <p className="muted">No matches for “{pageQuery}”.</p>;

  const removeBookmark = async (id: string) => {
    await deleteBookmark(libraryId, id);
    bookmarks.reload();
  };
  const removeHighlight = async (id: string) => {
    await deleteHighlight(libraryId, id);
    highlights.reload();
  };

  const header = (
    <PageHeader
      title="Bookmarks & Highlights"
      subtitle="Everything you've marked, across your whole library."
    />
  );

  if (gate.kind === 'loading') {
    return (
      <main className="page bookmarks-page">
        {header}
        <LoadingState label="Loading libraries…" />
      </main>
    );
  }
  if (gate.kind === 'error') {
    return (
      <main className="page bookmarks-page">
        {header}
        <ErrorState message={gate.message} onRetry={bookmarks.reload} />
      </main>
    );
  }
  if (gate.kind === 'empty') {
    return (
      <main className="page bookmarks-page">
        {header}
        <NoLibrariesState isAdmin={user?.role === 'admin'} />
      </main>
    );
  }

  const library = gate.library;

  return (
    <main className="page bookmarks-page">
      {header}
      {library.status === 'offline' && <LibraryOfflineNotice library={library} />}

      <section className="annotation-section">
        <h2>Bookmarks</h2>
        {bookmarks.loading && <LoadingState />}
        {bookmarks.error && <ErrorState message={bookmarks.error} onRetry={bookmarks.reload} />}
        {bookmarks.data !== null && bookmarks.data.length === 0 && (
          <EmptyState title="No bookmarks yet" testId="bookmarks-empty" icon="bookmark">
            <p>Open a book and use the bookmark button to mark a page.</p>
          </EmptyState>
        )}
        {shownBookmarks !== null &&
          bookmarks.data!.length > 0 &&
          shownBookmarks.length === 0 &&
          noMatch}
        {shownBookmarks !== null && shownBookmarks.length > 0 && (
          <ul className="annotation-list" data-testid="bookmarks-list">
            {shownBookmarks.map((b) => {
              const label = labels[b.file_id];
              return (
                <li key={b.id} className="annotation-card">
                  <Link
                    className="annotation-card-main"
                    to={`/read/${libraryId}/${b.file_id}?loc=${encodeURIComponent(b.location)}`}
                  >
                    <span className="annotation-card-title">{label?.title ?? '…'}</span>
                    {b.label && <span className="annotation-card-note">{b.label}</span>}
                    <span className="muted annotation-card-date">{formatDate(b.created_at)}</span>
                  </Link>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label="Delete bookmark"
                    onClick={() => void removeBookmark(b.id)}
                  >
                    <Icon name="close" />
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <section className="annotation-section">
        <h2>Highlights</h2>
        {highlights.loading && <LoadingState />}
        {highlights.error && <ErrorState message={highlights.error} onRetry={highlights.reload} />}
        {highlights.data !== null && highlights.data.length === 0 && (
          <EmptyState title="No highlights yet" testId="highlights-empty" icon="highlighter">
            <p>Select text while reading to save a highlight.</p>
          </EmptyState>
        )}
        {shownHighlights !== null &&
          highlights.data!.length > 0 &&
          shownHighlights.length === 0 &&
          noMatch}
        {shownHighlights !== null && shownHighlights.length > 0 && (
          <ul className="annotation-list" data-testid="highlights-list">
            {shownHighlights.map((h) => {
              const label = labels[h.file_id];
              return (
                <li key={h.id} className="annotation-card">
                  <Link
                    className="annotation-card-main"
                    style={{ borderLeftColor: h.color }}
                    to={`/read/${libraryId}/${h.file_id}?loc=${encodeURIComponent(h.location)}`}
                  >
                    <span className="annotation-card-title">{label?.title ?? '…'}</span>
                    <span className="annotation-card-excerpt">“{h.text}”</span>
                    {h.note && <span className="annotation-card-note">{h.note}</span>}
                    <span className="muted annotation-card-date">{formatDate(h.created_at)}</span>
                  </Link>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label="Delete highlight"
                    onClick={() => void removeHighlight(h.id)}
                  >
                    <Icon name="close" />
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </main>
  );
}

/**
 * Search — where the top-bar search lands. A results page driven by the
 * address bar (`?q=`, `?type=`, `?person=`, `?album=`, `?tag=`), so a search
 * is linkable and survives a reload.
 */

import { useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';

import { useFileLabels } from '../api/fileLabels';
import { useLibraries } from '../api/libraries';
import { searchContent } from '../api/queries';
import type { ContentHit, ContentSourceType } from '../api/types';
import { SearchBox } from '../components/app-shell/SearchBox';
import MediaPage from './MediaPage';
import type { MediaPageConfig } from './MediaPage';
import './SearchPage.css';

const SOURCE_LABEL: Record<ContentSourceType, string> = {
  book_text: 'In this book',
  metadata: 'Book details',
  quote: 'Your quote',
  highlight: 'Your highlight',
  note: 'Your note',
};

/** Where clicking a content hit should go. Quote/highlight hits jump
 * straight into the reader at the saved location when one is known
 * (falling back to their list page, which can locate it too, if not); a
 * note hit deep-links to itself via NotesPage's `?open=` support; book
 * text/metadata open the book itself — extraction doesn't preserve a
 * page/CFI anchor, so this is "open the book", not a pinpoint jump, for
 * those two source types specifically. */
function linkFor(hit: ContentHit, libraryId: string): string {
  switch (hit.source_type) {
    case 'note':
      return `/notes?open=${hit.source_id}`;
    case 'quote':
      return hit.file_id && hit.location
        ? `/read/${libraryId}/${hit.file_id}?loc=${encodeURIComponent(hit.location)}`
        : '/quotes';
    case 'highlight':
      return hit.file_id && hit.location
        ? `/read/${libraryId}/${hit.file_id}?loc=${encodeURIComponent(hit.location)}`
        : '/bookmarks';
    case 'metadata':
    case 'book_text':
      return hit.file_id ? `/read/${libraryId}/${hit.file_id}` : '/search';
  }
}

/** Matches against extracted book text, book metadata, quotes, highlights,
 * and notes — a secondary results section below the file grid, since this
 * content has no place in a file-oriented MediaGrid. Fails soft: an error
 * here never blocks the primary (file path) search above it. */
function ContentSearchResults({ libraryId, q }: { libraryId: string; q: string }) {
  const [hits, setHits] = useState<ContentHit[] | null>(null);

  // Mounted only when both libraryId and q are already truthy (see the
  // call site), so there is no "nothing to search yet" case to guard here.
  useEffect(() => {
    let cancelled = false;
    searchContent(libraryId, q)
      .then((resp) => {
        if (!cancelled) setHits(resp.hits ?? []);
      })
      .catch(() => {
        if (!cancelled) setHits([]);
      });
    return () => {
      cancelled = true;
    };
  }, [libraryId, q]);

  const fileIds = (hits ?? []).map((h) => h.file_id).filter((id): id is string => !!id);
  const labels = useFileLabels(libraryId, fileIds);

  if (!hits || hits.length === 0) return null;

  return (
    <section className="content-search-results" data-testid="content-search-results">
      <h2>Also found in your library</h2>
      <ul className="content-hit-list">
        {hits.map((h, i) => (
          <li key={`${h.source_type}-${h.source_id}-${i}`}>
            <Link to={linkFor(h, libraryId)} className="content-hit-card">
              <span className="content-hit-type">{SOURCE_LABEL[h.source_type]}</span>
              {h.file_id && <span className="content-hit-file">{labels[h.file_id]?.title ?? '…'}</span>}
              <span className="content-hit-excerpt">{h.excerpt}</span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

export default function SearchPage() {
  const [params] = useSearchParams();
  const { libraryId } = useLibraries();
  const q = params.get('q') ?? '';
  const scoped = params.has('person') || params.has('album') || params.has('tag');

  const config: MediaPageConfig = {
    title: q ? `Results for “${q}”` : scoped ? 'Search results' : 'Search',
    searchPage: true,
    showTypeFilter: true,
    emptyTitle: 'Search your library',
    emptyBody: 'Find photos and files by name, folder, tag, album, or person.',
    emptyIcon: 'search',
  };

  return (
    <>
      {/* On phones the top bar has no search box; the page brings its own. */}
      <div className="search-page-box">
        <SearchBox autoFocus={!q && !scoped} />
      </div>
      <MediaPage config={config} />
      {q && libraryId && <ContentSearchResults libraryId={libraryId} q={q} />}
    </>
  );
}

/**
 * Search — where the top-bar search lands. A results page driven by the
 * address bar (`?q=`, `?type=`, `?person=`, `?album=`, `?tag=`), so a search
 * is linkable and survives a reload.
 */

import { useSearchParams } from 'react-router-dom';

import { SearchBox } from '../components/app-shell/SearchBox';
import MediaPage from './MediaPage';
import type { MediaPageConfig } from './MediaPage';
import './SearchPage.css';

export default function SearchPage() {
  const [params] = useSearchParams();
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
    </>
  );
}

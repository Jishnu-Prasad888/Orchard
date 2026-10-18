/**
 * The top bar: a page-aware search in the middle (see pageSearch.ts);
 * upload, help, and the account on the right. On phones the bar shrinks to the
 * wordmark and the account, and search moves to its own tab.
 */

import { useRef, type RefObject } from 'react';
import { Link, useLocation } from 'react-router-dom';

import { useLibraries } from '../../api/libraries';
import { Icon } from '../ui/Icon';
import { useUploads } from '../upload/UploadProvider';
import { AccountMenu } from './AccountMenu';
import { PageFilterSearch } from './PageFilterSearch';
import { pageSearchMode } from './pageSearch';
import { SearchBox } from './SearchBox';
import { SettingsSearch } from './SettingsSearch';

export function TopBar({
  searchRef,
  onShowShortcuts,
}: {
  searchRef: RefObject<HTMLInputElement | null>;
  onShowShortcuts: () => void;
}) {
  const { library } = useLibraries();
  const { enqueue, target } = useUploads();
  const fileRef = useRef<HTMLInputElement | null>(null);
  const canUpload = library !== null && library.status !== 'offline';
  // Upload into the folder on screen (Files registers it), else the root.
  const uploadFolder = library && target?.libraryId === library.id ? target.folder : '';
  const { pathname } = useLocation();
  const mode = pageSearchMode(pathname);

  return (
    <header className="topbar" data-sticky-top>
      <Link to="/" className="topbar-brand" aria-label="Orchard home">
        <span className="brand-mark" aria-hidden="true">
          <span />
          <span />
          <span />
        </span>
        <span className="brand">Orchard</span>
      </Link>

      <div className="topbar-search">
        {mode.kind === 'settings' && <SettingsSearch inputRef={searchRef} />}
        {mode.kind === 'files' && <SearchBox inputRef={searchRef} />}
        {mode.kind === 'filter' && (
          // Keyed by page so each page's box starts from its own URL.
          <PageFilterSearch key={pathname} noun={mode.noun} inputRef={searchRef} />
        )}
      </div>

      <div className="topbar-actions">
        {canUpload && (
          <>
            <button
              type="button"
              className="button ghost-button topbar-upload"
              onClick={() => fileRef.current?.click()}
              title={`Upload to ${library.name}${uploadFolder ? ` / ${uploadFolder}` : ''}`}
            >
              <Icon name="upload" />
              <span className="topbar-upload-label">Upload</span>
            </button>
            <input
              ref={fileRef}
              type="file"
              multiple
              hidden
              aria-label={`Upload files to ${library.name}`}
              onChange={(event) => {
                const files = Array.from(event.target.files ?? []);
                if (files.length) enqueue(library.id, files, uploadFolder);
                event.target.value = '';
              }}
            />
          </>
        )}
        <button
          type="button"
          className="icon-button topbar-help"
          aria-label="Keyboard shortcuts"
          title="Keyboard shortcuts (?)"
          onClick={onShowShortcuts}
        >
          <Icon name="keyboard" />
        </button>
        <AccountMenu onShowShortcuts={onShowShortcuts} />
      </div>
    </header>
  );
}

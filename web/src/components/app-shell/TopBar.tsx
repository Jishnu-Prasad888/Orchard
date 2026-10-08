/**
 * The top bar: settings search in the middle;
 * upload, help, and the account on the right. On phones the bar shrinks to the
 * wordmark and the account, and search moves to its own tab.
 */

import { useRef, type RefObject } from 'react';
import { Link } from 'react-router-dom';

import { useLibraries } from '../../api/libraries';
import { Icon } from '../ui/Icon';
import { useUploads } from '../upload/UploadProvider';
import { AccountMenu } from './AccountMenu';
import { SettingsSearch } from './SettingsSearch';

export function TopBar({
  searchRef,
  onShowShortcuts,
}: {
  searchRef: RefObject<HTMLInputElement | null>;
  onShowShortcuts: () => void;
}) {
  const { library } = useLibraries();
  const { enqueue } = useUploads();
  const fileRef = useRef<HTMLInputElement | null>(null);
  const canUpload = library !== null && library.status !== 'offline';

  return (
    <header className="topbar" data-sticky-top>
      <Link to="/" className="topbar-brand" aria-label="Cairn home">
        <span className="cairn-mark" aria-hidden="true">
          <span />
          <span />
          <span />
        </span>
        <span className="brand">Cairn</span>
      </Link>

      <div className="topbar-search">
        <SettingsSearch inputRef={searchRef} />
      </div>

      <div className="topbar-actions">
        {canUpload && (
          <>
            <button
              type="button"
              className="button ghost-button topbar-upload"
              onClick={() => fileRef.current?.click()}
              title={`Upload to ${library.name}`}
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
                if (files.length) enqueue(library.id, files, '');
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

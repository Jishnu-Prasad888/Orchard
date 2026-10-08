/**
 * The library picker for the page header.
 *
 * This is the control the user reaches for constantly, so it is one component
 * rather than a `<select>` copy-pasted into six pages — and it carries the
 * offline marker, which is the difference between "this library is empty" and
 * "this drive is unplugged".
 */

import { useLibraries } from '../api/libraries';
import './States.css';

export default function LibraryPicker({ label = 'Library' }: { label?: string }) {
  const { libraries, libraryId, selectLibrary, libraryOffline } = useLibraries();

  if (libraries.length === 0) return null;

  return (
    <div className="library-picker">
      <select
        aria-label={label}
        value={libraryId ?? ''}
        onChange={(event) => selectLibrary(event.target.value)}
        data-testid="library-picker"
      >
        {libraries.map((lib) => (
          <option key={lib.id} value={lib.id}>
            {lib.name}
            {lib.status === 'offline' ? ' · offline' : ''}
          </option>
        ))}
      </select>
      {libraryOffline && (
        <span className="status-badge status-badge-offline" data-testid="library-picker-offline">
          Offline
        </span>
      )}
    </div>
  );
}

/**
 * Picking a destination folder.
 *
 * Move and copy both need a path relative to the library root, and typing one
 * is only reasonable if you already know it. This offers the folders that
 * actually exist as a menu, and keeps the text field beside it: the list comes
 * from a library's indexed folders, so a path that has never held a file is not
 * in it, and a dropdown on its own would make those destinations unreachable.
 *
 * The menu is a plain <select> of flattened paths rather than a tree widget.
 * That is one control instead of five, it inherits keyboard and screen-reader
 * behaviour for free, and the slash-separated paths carry the hierarchy.
 */

import { useEffect, useId, useMemo, useState } from 'react';

import { listFolders } from '../api/queries';
import './FolderPicker.css';

/**
 * How deep to walk before giving up. A deeper path is still reachable by
 * typing it, so this only bounds the menu, never the dialog.
 */
const MAX_DEPTH = 12;

export interface FolderPickerProps {
  libraryId: string;
  /** The chosen path, relative to the library root. '' is the root itself. */
  value: string;
  onChange: (path: string) => void;
  /** Accessible name for the text field. */
  label: string;
  placeholder?: string;
  /**
   * Shown under the field when the menu could not list every folder. Optional:
   * a caller with nothing to say can omit it, which only hides the menu when
   * there was nothing to list anyway.
   */
  unavailableNote?: string;
  disabled?: boolean;
  testId?: string;
}

/**
 * Collects every folder in the library.
 *
 * The API lists the children of one folder, so the tree is walked a level at a
 * time: one request per level with every parent in it, rather than one request
 * per folder. A library with no nesting costs exactly one request.
 *
 * `incomplete` is set when the walk errored or hit `MAX_DEPTH`, so a caller can
 * say so instead of presenting a partial list as the whole library.
 */
function useFolderPaths(libraryId: string): { paths: string[]; incomplete: boolean } {
  const [paths, setPaths] = useState<string[]>([]);
  const [incomplete, setIncomplete] = useState(false);

  useEffect(() => {
    if (!libraryId) return;
    let cancelled = false;

    void (async () => {
      const seen: string[] = [];
      // Breadth-first: `parents` is every folder one level below the last
      // request, or `[undefined]` for the root.
      let parents: (string | undefined)[] = [undefined];
      let truncated = false;
      try {
        for (let depth = 0; parents.length > 0; depth += 1) {
          if (depth === MAX_DEPTH) {
            truncated = true;
            break;
          }
          const levels = await Promise.all(parents.map((p) => listFolders(libraryId, p)));
          if (cancelled) return;
          const next: string[] = [];
          for (const level of levels) {
            for (const folder of level.folders ?? []) {
              // A folder can come back from more than one parent if the data
              // is inconsistent; the dedupe is also what stops a cycle in the
              // tree from walking forever.
              if (seen.includes(folder.rel_path)) continue;
              seen.push(folder.rel_path);
              next.push(folder.rel_path);
            }
          }
          parents = next;
        }
        if (cancelled) return;
        setPaths(seen);
        setIncomplete(truncated);
      } catch {
        // A library that cannot list its folders still accepts a typed path,
        // so this degrades to the text field rather than an error.
        if (cancelled) return;
        setPaths([]);
        setIncomplete(true);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [libraryId]);

  return { paths, incomplete };
}

export function FolderPicker({
  libraryId,
  value,
  onChange,
  label,
  placeholder,
  unavailableNote,
  disabled,
  testId,
}: FolderPickerProps) {
  const selectId = useId();
  const inputId = useId();
  const { paths, incomplete } = useFolderPaths(libraryId);

  // The current value is always an option, even when it is a folder the index
  // has never seen — otherwise the menu would snap back to the root every time
  // someone typed a fresh path into the field.
  const listed = useMemo(
    () => (value === '' || paths.includes(value) ? paths : [value, ...paths]),
    [paths, value],
  );

  // A menu offering nothing but the root is worse than no menu at all: it says
  // the library has no folders, which is not what an error means.
  const showMenu = !incomplete || paths.length > 0;

  return (
    <div className="folder-picker">
      {showMenu && (
        <div className="folder-picker-menu">
          <label htmlFor={selectId}>Pick an existing folder</label>
          <select
            id={selectId}
            value={value}
            disabled={disabled}
            onChange={(e) => onChange(e.target.value)}
            data-testid={testId ? `${testId}-select` : undefined}
          >
            <option value="">Library root (top level)</option>
            {listed
              .filter((p) => p !== '')
              .map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
          </select>
        </div>
      )}

      <div className="folder-picker-field">
        <label htmlFor={inputId}>{label}</label>
        <input
          id={inputId}
          type="text"
          value={value}
          placeholder={placeholder}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
          data-testid={testId}
        />
        {incomplete && unavailableNote && (
          <p className="muted folder-picker-note">{unavailableNote}</p>
        )}
      </div>
    </div>
  );
}

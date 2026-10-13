/**
 * Resolves display labels (book title/author, or filename as a fallback)
 * for a set of file ids. Bookmarks, highlights, and quotes only carry a
 * `file_id` — the library-wide listing pages need a title to show, so this
 * fetches it once per id and caches it for the life of the hook.
 */

import { useEffect, useRef, useState } from 'react';

import { getBookMetadata, getFile } from './queries';

export interface FileLabel {
  name: string;
  title: string;
  author?: string | undefined;
}

export function useFileLabels(libraryId: string, fileIds: string[]): Record<string, FileLabel> {
  const [labels, setLabels] = useState<Record<string, FileLabel>>({});
  const fetched = useRef<Set<string>>(new Set());

  useEffect(() => {
    if (!libraryId) return;
    const toFetch = Array.from(new Set(fileIds)).filter((id) => id && !fetched.current.has(id));
    if (toFetch.length === 0) return;
    toFetch.forEach((id) => fetched.current.add(id));

    let cancelled = false;
    Promise.all(
      toFetch.map((id) =>
        Promise.all([getFile(libraryId, id), getBookMetadata(libraryId, id)])
          .then(
            ([fileResp, bookResp]) =>
              [
                id,
                {
                  name: fileResp.file.name,
                  title: bookResp.book.title || fileResp.file.name,
                  author: bookResp.book.author || undefined,
                },
              ] as const,
          )
          .catch(() => [id, { name: id, title: id }] as const),
      ),
    ).then((entries) => {
      if (cancelled) return;
      setLabels((prev) => {
        const next = { ...prev };
        for (const [id, label] of entries) next[id] = label;
        return next;
      });
    });
    return () => {
      cancelled = true;
    };
  }, [libraryId, fileIds]);

  return labels;
}

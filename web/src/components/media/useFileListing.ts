/**
 * Loading a listing of files, page by page.
 *
 * One hook for every media surface (Photos, Videos, Files, search), so the
 * rules are the same everywhere:
 *
 * - a plain listing uses `GET /files`, which honours sort order and folders;
 * - a query, a person/album/tag, or a date or size filter uses `GET /search`,
 *   because only search understands those (it returns relevance order, or id
 *   order without a query);
 * - pages are fetched with the server's cursor and appended; a new query
 *   discards the old pages and cancels anything in flight.
 */

import { useCallback, useEffect, useRef, useState } from 'react';

import { useLibraryChangeTick } from '../../api/libraryEvents';

import { listFiles, listFolders, searchFiles } from '../../api/queries';
import type { FileSort, MediaFilter, SortOrder } from '../../api/queries';
import type { FileListResponse, FileSummary, Folder } from '../../api/types';

export const PAGE_SIZE = 100;
const MB = 1024 * 1024;

export interface ListingQuery {
  q: string;
  type?: MediaFilter | undefined;
  /** Folder to list; `undefined` with `recursive` means the whole library. */
  folder?: string | undefined;
  recursive?: boolean | undefined;
  sort: FileSort;
  order: SortOrder;
  /** Megabytes, as typed. Empty means no bound. */
  minSizeMb: string;
  maxSizeMb: string;
  /** `YYYY-MM-DD`, as a date input gives it. */
  from: string;
  to: string;
  person?: string | undefined;
  album?: string | undefined;
  tag?: string | undefined;
  /** Load the sub-folders of `folder` alongside the files. */
  withFolders?: boolean | undefined;
}

export interface FileListing {
  files: FileSummary[];
  folders: Folder[];
  total: number;
  /** True until the first page of the current query has arrived. */
  loading: boolean;
  error: string | null;
  hasMore: boolean;
  loadingMore: boolean;
  loadMore: () => void;
  reload: () => void;
  /** Whether this listing went through search (no sort order). */
  searching: boolean;
}

export function usesSearch(query: ListingQuery): boolean {
  return Boolean(
    query.q.trim() ||
    query.person ||
    query.album ||
    query.tag ||
    query.minSizeMb ||
    query.maxSizeMb ||
    query.from ||
    query.to,
  );
}

function toBytes(mb: string): number | undefined {
  if (!mb) return undefined;
  const n = Number(mb);
  return Number.isFinite(n) && n >= 0 ? Math.round(n * MB) : undefined;
}

function fetchPage(
  libraryId: string,
  query: ListingQuery,
  cursor?: string,
): Promise<FileListResponse> {
  if (usesSearch(query)) {
    return searchFiles(libraryId, {
      q: query.q.trim() || undefined,
      type: query.type,
      folder: query.recursive ? undefined : query.folder || undefined,
      person: query.person,
      album: query.album,
      tag: query.tag,
      min_size: toBytes(query.minSizeMb),
      max_size: toBytes(query.maxSizeMb),
      from: query.from ? new Date(query.from).toISOString() : undefined,
      to: query.to ? new Date(`${query.to}T23:59:59`).toISOString() : undefined,
      cursor,
      limit: PAGE_SIZE,
    });
  }
  return listFiles(libraryId, {
    type: query.type,
    folder: query.folder,
    recursive: query.recursive || undefined,
    sort: query.sort,
    order: query.order,
    cursor,
    limit: PAGE_SIZE,
  });
}

interface Loaded {
  key: string;
  files: FileSummary[];
  folders: Folder[];
  total: number;
  cursor: string | undefined;
  error: string | null;
}

export function useFileListing(
  libraryId: string | null,
  query: ListingQuery,
  { enabled = true, refreshKey = 0 }: { enabled?: boolean; refreshKey?: number } = {},
): FileListing {
  const [run, setRun] = useState(0);
  // Files changed on disk: re-read behind the list already on screen.
  const changeTick = useLibraryChangeTick(libraryId);
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);

  const key =
    enabled && libraryId ? `${libraryId}|${JSON.stringify(query)}|${run}|${refreshKey}` : null;
  const queryRef = useRef(query);
  useEffect(() => {
    queryRef.current = query;
  });

  useEffect(() => {
    if (key === null || libraryId === null) return;
    let cancelled = false;
    const current = queryRef.current;
    const wantFolders = current.withFolders && !usesSearch(current);
    Promise.all([
      wantFolders
        ? listFolders(libraryId, current.folder || undefined)
            .then((r) => r.folders ?? [])
            .catch(() => [] as Folder[])
        : Promise.resolve([] as Folder[]),
      fetchPage(libraryId, current),
    ])
      .then(([folders, page]) => {
        if (cancelled) return;
        setLoaded({
          key,
          folders,
          files: filterType(page.files ?? [], current.type),
          total: page.total ?? page.files?.length ?? 0,
          cursor: page.next_cursor || undefined,
          error: null,
        });
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        setLoaded({
          key,
          folders: [],
          files: [],
          total: 0,
          cursor: undefined,
          error: error instanceof Error ? error.message : String(error),
        });
      });
    return () => {
      cancelled = true;
    };
  }, [key, libraryId, changeTick]);

  const current = loaded && loaded.key === key ? loaded : null;

  const loadMore = useCallback(() => {
    if (!libraryId || !current?.cursor || loadingMore) return;
    const pageKey = current.key;
    setLoadingMore(true);
    fetchPage(libraryId, queryRef.current, current.cursor)
      .then((page) => {
        setLoaded((prev) =>
          prev && prev.key === pageKey
            ? {
                ...prev,
                files: [...prev.files, ...filterType(page.files ?? [], queryRef.current.type)],
                cursor: page.next_cursor || undefined,
              }
            : prev,
        );
      })
      .catch((error: unknown) => {
        setLoaded((prev) =>
          prev && prev.key === pageKey
            ? { ...prev, error: error instanceof Error ? error.message : String(error) }
            : prev,
        );
      })
      .finally(() => setLoadingMore(false));
  }, [libraryId, current, loadingMore]);

  const reload = useCallback(() => setRun((n) => n + 1), []);

  return {
    files: current?.files ?? EMPTY_FILES,
    folders: current?.folders ?? EMPTY_FOLDERS,
    total: current?.total ?? 0,
    loading: key !== null && current === null,
    error: current?.error ?? null,
    hasMore: Boolean(current?.cursor),
    loadingMore,
    loadMore,
    reload,
    searching: usesSearch(query),
  };
}

/** Guard against an index that classified a file differently from the filter. */
function filterType(files: FileSummary[], type: MediaFilter | undefined): FileSummary[] {
  return type ? files.filter((f) => f.media_type === type) : files;
}

const EMPTY_FILES: FileSummary[] = [];
const EMPTY_FOLDERS: Folder[] = [];

/**
 * Typed endpoint wrappers for the parts of the API the UI reads most.
 *
 * Keeping URL construction and envelope unwrapping in one place is what lets a
 * client be written from the API documentation alone: every call below
 * corresponds to one documented route, and the shapes come from the shared
 * types module.
 */

import {
  API_BASE,
  ApiError,
  apiDelete,
  apiGet,
  apiPatch,
  apiPost,
  apiPut,
  apiPutFile,
  apiRequest,
  apiUpload,
  apiUploadWithProgress,
  query,
} from './client';
import type { UploadProgress } from './client';
import type {
  BackupRecord,
  BookMetadata,
  BookMetadataEnvelope,
  BookmarkEnvelope,
  BookmarkListResponse,
  Collection,
  CollectionEnvelope,
  CollectionListResponse,
  ContentHitListResponse,
  DuplicateGroupListResponse,
  FileCollectionResponse,
  FileListResponse,
  FileNote,
  FileSummary,
  FolderListResponse,
  GrantListResponse,
  Highlight,
  HighlightEnvelope,
  HighlightListResponse,
  HomeLayoutResponse,
  HomeLayoutSection,
  InProgressListResponse,
  IndexStatus,
  IndexTriggerResponse,
  Library,
  LibraryCreateResponse,
  LibraryListResponse,
  LibraryProbeResponse,
  Note,
  NoteEnvelope,
  NoteListResponse,
  PendingSignup,
  PendingSignupListResponse,
  PermissionGrant,
  PublicShareInfo,
  Quote,
  QuoteEnvelope,
  QuoteListResponse,
  RecentSearchListResponse,
  ReaderPreferences,
  ReaderPreferencesResponse,
  ReadingProgress,
  ReadingProgressResponse,
  ReadingStatus,
  ReadingStatusResponse,
  ShareCreateResponse,
  ShareListResponse,
  Tag,
  TagListResponse,
  User,
  UserListResponse,
} from './types';

/* ------------------------- re-exported types ------------------------- */

/**
 * Pages that render a grid need the file shape and the sort vocabulary next to
 * the functions that return them, so they are re-exported here rather than
 * forcing every caller to import from two modules.
 */
export type { Collection, FileSummary, Folder, Tag } from './types';
export { formatBytes } from './types';

/* ---------------------------- libraries ---------------------------- */

export const listLibraries = () => apiGet<LibraryListResponse>('/libraries');

export const getLibrary = (id: string) => apiGet<{ library: Library }>(`/libraries/${id}`);

export const probeLibrary = (path: string) =>
  apiPost<LibraryProbeResponse>('/libraries/probe', { path });

/** One level of the server's directory tree, for the library folder chooser. */
export interface DirListing {
  path: string;
  /** Empty at the top; the parent of a top-level folder is the places list. */
  parent: string;
  /** True for the starting list of places: home, the root, and mounted drives. */
  roots?: boolean;
  dirs: Array<{ name: string; path: string }>;
}

export const browseServerDirs = (path?: string) =>
  apiGet<DirListing>(path ? `/fs/dirs?path=${encodeURIComponent(path)}` : '/fs/dirs');

export const registerLibrary = (path: string, name?: string) =>
  apiPost<LibraryCreateResponse>('/libraries', name ? { path, name } : { path });

/** Re-check connectivity, or reconnect the library at a new path. */
export const refreshLibrary = (id: string, path?: string) =>
  apiPost<{ library: Library }>(`/libraries/${id}/refresh`, path ? { path } : {});

export const unregisterLibrary = (id: string) => apiDelete<void>(`/libraries/${id}`);

/* ----------------------------- indexing ---------------------------- */

export const triggerIndex = (id: string) =>
  apiPost<IndexTriggerResponse>(`/libraries/${id}/index`, {});

export const getIndexStatus = (id: string) =>
  apiGet<{ status: IndexStatus }>(`/libraries/${id}/index/status`);

/* ------------------------------ files ------------------------------ */

export type FileSort = 'name' | 'size' | 'mod_time' | 'media_type';
export type SortOrder = 'asc' | 'desc';
export type MediaFilter = 'photo' | 'video' | 'audio' | 'document' | 'other';
export type FileStatusFilter = 'present' | 'missing' | 'trashed';

/**
 * Listing parameters. Every field is `| undefined` rather than merely optional
 * because the project compiles with `exactOptionalPropertyTypes`: a page that
 * computes `{ type: maybeUndefined }` has to be allowed to say so.
 */
export interface ListFilesParams {
  folder?: string | undefined;
  recursive?: boolean | undefined;
  type?: MediaFilter | undefined;
  status?: FileStatusFilter | undefined;
  sort?: FileSort | undefined;
  order?: SortOrder | undefined;
  cursor?: string | undefined;
  limit?: number | undefined;
}

export const listFiles = (libraryId: string, params: ListFilesParams = {}) =>
  apiGet<FileListResponse>(`/libraries/${libraryId}/files${query({ ...params })}`);

export const getFileCounts = (libraryId: string) =>
  apiGet<{
    counts: Partial<Record<'photo' | 'video' | 'audio' | 'document' | 'other', number>>;
    tags: number;
    favorites: number;
  }>(`/libraries/${libraryId}/files/counts`);

export const listFolders = (libraryId: string, parent?: string) =>
  apiGet<FolderListResponse>(`/libraries/${libraryId}/folders${query({ parent })}`);

export const createFolder = (libraryId: string, path: string) =>
  apiPost<{ folder: import('./types').Folder }>(`/libraries/${libraryId}/folders`, { path });

export const getFile = (libraryId: string, fileId: string) =>
  apiGet<{ file: FileSummary }>(`/libraries/${libraryId}/files/${fileId}`);

/* --------------------------- file mutations -------------------------- */

/**
 * Multipart upload. `destPath` is the destination relative to the library
 * root and defaults server-side to the uploaded file's name.
 */
export const uploadFile = (libraryId: string, file: File, destPath?: string) =>
  apiUpload<{ file: FileSummary }>(`/libraries/${libraryId}/files/upload`, file, destPath);

/** The same upload, with progress events and cancellation, for the upload tray. */
export const uploadFileWithProgress = (
  libraryId: string,
  file: File,
  destPath: string | undefined,
  options: { onProgress?: (p: UploadProgress) => void; signal?: AbortSignal } = {},
) =>
  apiUploadWithProgress<{ file: FileSummary }>(
    `/libraries/${libraryId}/files/upload`,
    file,
    destPath,
    options,
  );

/**
 * Soft-delete (move to trash).
 *
 * The server resolves and re-authorizes the file from the `path` in the body,
 * not from the id in the URL, so both are sent. The id keeps the call shaped
 * like every other file route; the path is what actually acts.
 */
export const softDeleteFile = (libraryId: string, relPath: string, fileId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/files/${fileId}`, { path: relPath });

export const renameFile = (libraryId: string, relPath: string, fileId: string, newName: string) =>
  apiPost<{ file: FileSummary }>(`/libraries/${libraryId}/files/${fileId}/rename`, {
    path: relPath,
    new_name: newName,
  });

/** `destFolder` is relative to the library root; `""` or `"."` means root. */
export const moveFile = (
  libraryId: string,
  relPath: string,
  fileId: string,
  destFolder: string,
  fileName: string,
) =>
  apiPost<{ file: FileSummary }>(`/libraries/${libraryId}/files/${fileId}/move`, {
    path: relPath,
    new_path: joinRelPath(destFolder, fileName),
  });

export const copyFile = (
  libraryId: string,
  relPath: string,
  fileId: string,
  destFolder: string,
  fileName: string,
) =>
  apiPost<{ file: FileSummary }>(`/libraries/${libraryId}/files/${fileId}/copy`, {
    path: relPath,
    dest_path: joinRelPath(destFolder, fileName),
  });

/** Join a folder and a file name into a library-relative path. */
export function joinRelPath(folder: string, name: string): string {
  const trimmed = folder.replace(/^\/+|\/+$/g, '');
  return trimmed && trimmed !== '.' ? `${trimmed}/${name}` : name;
}

/* ------------------------------ notes -------------------------------- */

/** Returns an empty body (not a 404) when the file has no note yet. */
export const getFileNote = (libraryId: string, fileId: string) =>
  apiGet<{ note?: FileNote }>(`/libraries/${libraryId}/files/${fileId}/note`);

export const setFileNote = (libraryId: string, fileId: string, body: string) =>
  apiPut<{ note: FileNote }>(`/libraries/${libraryId}/files/${fileId}/note`, { body });

export const clearFileNote = (libraryId: string, fileId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/files/${fileId}/note`);

/* --------------------------- book metadata --------------------------- */

export const getBookMetadata = (libraryId: string, fileId: string) =>
  apiGet<BookMetadataEnvelope>(`/libraries/${libraryId}/files/${fileId}/book-metadata`);

export const updateBookMetadata = (
  libraryId: string,
  fileId: string,
  edit: Partial<
    Pick<BookMetadata, 'title' | 'author' | 'publisher' | 'year' | 'isbn' | 'description'>
  >,
) => apiPatch<BookMetadataEnvelope>(`/libraries/${libraryId}/files/${fileId}/book-metadata`, edit);

export const uploadCover = (libraryId: string, fileId: string, file: File) =>
  apiPutFile<BookMetadataEnvelope>(`/libraries/${libraryId}/files/${fileId}/cover`, file);

export const coverUrl = (libraryId: string, fileId: string) =>
  `${API_BASE}/libraries/${libraryId}/files/${fileId}/cover`;

/* --------------------------- reading status --------------------------- */

export const getReadingStatus = (libraryId: string, fileId: string) =>
  apiGet<ReadingStatusResponse>(`/libraries/${libraryId}/files/${fileId}/reading-status`);

export const setReadingStatus = (libraryId: string, fileId: string, status: ReadingStatus) =>
  apiPut<{ status: ReadingStatus }>(`/libraries/${libraryId}/files/${fileId}/reading-status`, {
    status,
  });

export const clearReadingStatus = (libraryId: string, fileId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/files/${fileId}/reading-status`);

/* -------------------------- reading progress --------------------------- */

export const getReadingProgress = (libraryId: string, fileId: string) =>
  apiGet<ReadingProgressResponse>(`/libraries/${libraryId}/files/${fileId}/progress`);

export const setReadingProgress = (
  libraryId: string,
  fileId: string,
  location: string,
  percent: number,
) => apiPut<{ progress: ReadingProgress }>(`/libraries/${libraryId}/files/${fileId}/progress`, {
  location,
  percent,
});

export const clearReadingProgress = (libraryId: string, fileId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/files/${fileId}/progress`);

/** The acting user's most recently updated unfinished books, for Home's
 * "continue reading" section. */
export const listInProgress = (libraryId: string, limit?: number) =>
  apiGet<InProgressListResponse>(`/libraries/${libraryId}/reading/in-progress${query({ limit })}`);

/* ----------------------------- home layout ----------------------------- */

/** Home's section order/visibility — a server-level preference, like reader
 * preferences, not scoped to any one library. */
export const getHomeLayout = () => apiGet<HomeLayoutResponse>('/settings/home-layout');

export const setHomeLayout = (sections: HomeLayoutSection[]) =>
  apiPut<HomeLayoutResponse>('/settings/home-layout', { sections });

/* ------------------------- reader preferences --------------------------- */

export const getReaderPreferences = () =>
  apiGet<ReaderPreferencesResponse>('/settings/reader');

export const setReaderPreferences = (preferences: ReaderPreferences) =>
  apiPut<ReaderPreferencesResponse>('/settings/reader', preferences);

/* ------------------------------ recents -------------------------------- */

export const markFileOpened = (libraryId: string, fileId: string) =>
  apiPost<void>(`/libraries/${libraryId}/files/${fileId}/opened`);

export const listRecentlyOpened = (libraryId: string, limit?: number) =>
  apiGet<FileCollectionResponse>(
    `/libraries/${libraryId}/files/recently-opened${query({ limit })}`,
  );

/* ----------------------------- duplicates ------------------------------ */

export const listDuplicates = (libraryId: string) =>
  apiGet<DuplicateGroupListResponse>(`/libraries/${libraryId}/files/duplicates`);

/* ----------------------------- search ------------------------------ */

export interface SearchParams {
  q?: string | undefined;
  type?: MediaFilter | undefined;
  folder?: string | undefined;
  tag?: string | undefined;
  author?: string | undefined;
  collection?: string | undefined;
  min_size?: number | undefined;
  max_size?: number | undefined;
  from?: string | undefined;
  to?: string | undefined;
  cursor?: string | undefined;
  limit?: number | undefined;
}

export const searchFiles = (libraryId: string, params: SearchParams = {}) =>
  apiGet<FileListResponse>(`/libraries/${libraryId}/search${query({ ...params })}`);

/** Searches extracted book text, book metadata, quotes, highlights, and
 * notes content — as opposed to searchFiles, which only matches paths. */
export const searchContent = (libraryId: string, q: string) =>
  apiGet<ContentHitListResponse>(`/libraries/${libraryId}/search/content${query({ q })}`);

/** The reader-local variant: content search scoped to a single book. */
export const searchFileContent = (libraryId: string, fileId: string, q: string) =>
  apiGet<ContentHitListResponse>(`/libraries/${libraryId}/files/${fileId}/search/content${query({ q })}`);

export const listRecentSearches = (libraryId: string, limit?: number) =>
  apiGet<RecentSearchListResponse>(`/libraries/${libraryId}/search/recent${query({ limit })}`);

export const recordRecentSearch = (libraryId: string, q: string) =>
  apiPost<void>(`/libraries/${libraryId}/search/recent`, { query: q });

export const forgetRecentSearch = (libraryId: string, q: string) =>
  apiDelete<void>(`/libraries/${libraryId}/search/recent${query({ q })}`);

/* ------------------------------ trash ------------------------------ */

export const listTrash = (libraryId: string) =>
  apiGet<FileCollectionResponse>(`/libraries/${libraryId}/trash`);

export const restoreFile = (libraryId: string, fileId: string) =>
  apiPost<{ file: FileSummary }>(`/libraries/${libraryId}/files/${fileId}/restore`, {});

export const deleteForever = (libraryId: string, fileId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/files/${fileId}/permanent`);

/* ---------------------------- favorites ---------------------------- */

export const listFavorites = (libraryId: string) =>
  apiGet<FileCollectionResponse>(`/libraries/${libraryId}/favorites`);

export const addFavorite = (libraryId: string, fileId: string) =>
  apiPost<void>(`/libraries/${libraryId}/files/${fileId}/favorite`);

export const removeFavorite = (libraryId: string, fileId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/files/${fileId}/favorite`);

/* ------------------------------- tags ------------------------------ */

export const listTags = (libraryId: string) =>
  apiGet<TagListResponse>(`/libraries/${libraryId}/tags`);

export const createTag = (libraryId: string, name: string, color?: string) =>
  apiPost<{ tag: Tag }>(`/libraries/${libraryId}/tags`, color ? { name, color } : { name });

export const deleteTag = (libraryId: string, tagId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/tags/${tagId}`);

export const listFileTags = (libraryId: string, fileId: string) =>
  apiGet<TagListResponse>(`/libraries/${libraryId}/files/${fileId}/tags`);

export const addFileTag = (libraryId: string, fileId: string, tagId: string) =>
  apiPost<void>(`/libraries/${libraryId}/files/${fileId}/tags`, { tag_id: tagId });

export const removeFileTag = (libraryId: string, fileId: string, tagId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/files/${fileId}/tags/${tagId}`);

/* --------------------------- collections --------------------------- */

export const listCollections = (libraryId: string) =>
  apiGet<CollectionListResponse>(`/libraries/${libraryId}/collections`);

export const createCollection = (libraryId: string, name: string, description?: string) =>
  apiPost<CollectionEnvelope>(`/libraries/${libraryId}/collections`, { name, description });

export const updateCollection = (
  libraryId: string,
  collectionId: string,
  patch: Partial<Pick<Collection, 'name' | 'description' | 'pinned'>>,
) => apiPatch<CollectionEnvelope>(`/libraries/${libraryId}/collections/${collectionId}`, patch);

export const deleteCollection = (libraryId: string, collectionId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/collections/${collectionId}`);

export const listCollectionFiles = (libraryId: string, collectionId: string) =>
  apiGet<FileCollectionResponse>(`/libraries/${libraryId}/collections/${collectionId}/files`);

export const addCollectionFile = (libraryId: string, collectionId: string, fileId: string) =>
  apiPost<void>(`/libraries/${libraryId}/collections/${collectionId}/files`, { file_id: fileId });

export const removeCollectionFile = (libraryId: string, collectionId: string, fileId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/collections/${collectionId}/files/${fileId}`);

export const listFileCollections = (libraryId: string, fileId: string) =>
  apiGet<CollectionListResponse>(`/libraries/${libraryId}/files/${fileId}/collections`);

/* --------------------------- bookmarks ------------------------------ */

export const listLibraryBookmarks = (libraryId: string) =>
  apiGet<BookmarkListResponse>(`/libraries/${libraryId}/bookmarks`);

export const listFileBookmarks = (libraryId: string, fileId: string) =>
  apiGet<BookmarkListResponse>(`/libraries/${libraryId}/files/${fileId}/bookmarks`);

export const createBookmark = (
  libraryId: string,
  fileId: string,
  location: string,
  label?: string,
) =>
  apiPost<BookmarkEnvelope>(`/libraries/${libraryId}/files/${fileId}/bookmarks`, {
    location,
    label,
  });

export const updateBookmark = (libraryId: string, bookmarkId: string, label: string) =>
  apiPatch<BookmarkEnvelope>(`/libraries/${libraryId}/bookmarks/${bookmarkId}`, { label });

export const deleteBookmark = (libraryId: string, bookmarkId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/bookmarks/${bookmarkId}`);

/* --------------------------- highlights ------------------------------ */

export const listLibraryHighlights = (libraryId: string) =>
  apiGet<HighlightListResponse>(`/libraries/${libraryId}/highlights`);

export const listFileHighlights = (libraryId: string, fileId: string) =>
  apiGet<HighlightListResponse>(`/libraries/${libraryId}/files/${fileId}/highlights`);

export const createHighlight = (
  libraryId: string,
  fileId: string,
  location: string,
  text: string,
  color?: string,
  note?: string,
) =>
  apiPost<HighlightEnvelope>(`/libraries/${libraryId}/files/${fileId}/highlights`, {
    location,
    text,
    color,
    note,
  });

export const updateHighlight = (
  libraryId: string,
  highlightId: string,
  patch: Partial<Pick<Highlight, 'color' | 'note'>>,
) => apiPatch<HighlightEnvelope>(`/libraries/${libraryId}/highlights/${highlightId}`, patch);

export const deleteHighlight = (libraryId: string, highlightId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/highlights/${highlightId}`);

/* ----------------------------- quotes -------------------------------- */

export interface ListQuotesParams {
  favoriteOnly?: boolean | undefined;
  limit?: number | undefined;
}

export const listLibraryQuotes = (libraryId: string, params: ListQuotesParams = {}) =>
  apiGet<QuoteListResponse>(
    `/libraries/${libraryId}/quotes${query({
      favorite: params.favoriteOnly ? '1' : undefined,
      limit: params.limit,
    })}`,
  );

export const listFileQuotes = (libraryId: string, fileId: string) =>
  apiGet<QuoteListResponse>(`/libraries/${libraryId}/files/${fileId}/quotes`);

export const createQuote = (
  libraryId: string,
  fileId: string,
  fields: Pick<Quote, 'text'> &
    Partial<Pick<Quote, 'location' | 'author' | 'chapter' | 'page' | 'commentary'>>,
) => apiPost<QuoteEnvelope>(`/libraries/${libraryId}/files/${fileId}/quotes`, fields);

export const updateQuote = (
  libraryId: string,
  quoteId: string,
  patch: Partial<
    Pick<Quote, 'text' | 'author' | 'chapter' | 'page' | 'commentary' | 'favorite'>
  >,
) => apiPatch<QuoteEnvelope>(`/libraries/${libraryId}/quotes/${quoteId}`, patch);

export const deleteQuote = (libraryId: string, quoteId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/quotes/${quoteId}`);

/* ------------------------------ notes --------------------------------- */

export interface ListNotesParams {
  fileId?: string | undefined;
  tagId?: string | undefined;
  standalone?: boolean | undefined;
  limit?: number | undefined;
}

export const listNotes = (libraryId: string, params: ListNotesParams = {}) =>
  apiGet<NoteListResponse>(
    `/libraries/${libraryId}/notes${query({
      file_id: params.fileId,
      tag_id: params.tagId,
      standalone: params.standalone ? '1' : undefined,
      limit: params.limit,
    })}`,
  );

export const listFileNotes = (libraryId: string, fileId: string) =>
  apiGet<NoteListResponse>(`/libraries/${libraryId}/files/${fileId}/notes`);

export const createNote = (
  libraryId: string,
  fields: Partial<Pick<Note, 'title' | 'body' | 'file_id' | 'quote_id' | 'highlight_id'>>,
) => apiPost<NoteEnvelope>(`/libraries/${libraryId}/notes`, fields);

export const getNote = (libraryId: string, noteId: string) =>
  apiGet<NoteEnvelope>(`/libraries/${libraryId}/notes/${noteId}`);

export const updateNote = (
  libraryId: string,
  noteId: string,
  patch: Partial<Pick<Note, 'title' | 'body'>>,
) => apiPatch<NoteEnvelope>(`/libraries/${libraryId}/notes/${noteId}`, patch);

export const deleteNote = (libraryId: string, noteId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/notes/${noteId}`);

export const listNoteTags = (libraryId: string, noteId: string) =>
  apiGet<TagListResponse>(`/libraries/${libraryId}/notes/${noteId}/tags`);

export const addNoteTag = (libraryId: string, noteId: string, tagId: string) =>
  apiPost<void>(`/libraries/${libraryId}/notes/${noteId}/tags`, { tag_id: tagId });

export const removeNoteTag = (libraryId: string, noteId: string, tagId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/notes/${noteId}/tags/${tagId}`);

export const listNoteLinks = (libraryId: string, noteId: string) =>
  apiGet<NoteListResponse>(`/libraries/${libraryId}/notes/${noteId}/links`);

export const addNoteLink = (libraryId: string, noteId: string, linkedNoteId: string) =>
  apiPost<void>(`/libraries/${libraryId}/notes/${noteId}/links`, { linked_note_id: linkedNoteId });

export const removeNoteLink = (libraryId: string, noteId: string, linkedNoteId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/notes/${noteId}/links/${linkedNoteId}`);

/* ------------------------- permissions/shares ---------------------- */

export const listGrants = (libraryId: string) =>
  apiGet<GrantListResponse>(`/libraries/${libraryId}/permissions`);

export const createGrant = (
  libraryId: string,
  input: { user_id: string; key: string; caps: string[]; effect: 'allow' | 'deny' },
) => apiPost<{ grant: PermissionGrant }>(`/libraries/${libraryId}/permissions`, input);

export const revokeGrant = (libraryId: string, grantId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/permissions/${grantId}`);

export const listShares = (libraryId: string) =>
  apiGet<ShareListResponse>(`/libraries/${libraryId}/shares`);
export const createShare = (
  libraryId: string,
  input: { key: string; caps: string[]; password?: string; expires_at?: string },
) => apiPost<ShareCreateResponse>(`/libraries/${libraryId}/shares`, input);

export const revokeShare = (libraryId: string, shareId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/shares/${shareId}`);

/* ------------------------------ backups ---------------------------- */

/** GET /backups returns a bare JSON array, unlike every other listing. */
export const listBackups = () => apiGet<BackupRecord[]>('/backups');

export const getBackup = (id: string) => apiGet<BackupRecord>(`/backups/${id}`);

export const runBackup = () => apiPost<BackupRecord>('/backups', {});

export const verifyBackup = (id: string) => apiPost<BackupRecord>(`/backups/${id}/verify`);

export const restoreBackup = (id: string, destination: string) =>
  apiPost<BackupRecord>(`/backups/${id}/restore`, { destination });

/** Every library's book metadata (title/author/publisher/year/isbn/
 * description, and whether a cover exists) as one CSV — a human-readable
 * catalog export, independent of the raw-SQLite backup/restore path. */
export const exportBookMetadataUrl = (): string => `${API_BASE}/backups/export/book-metadata.csv`;

/* ------------------------------- users ----------------------------- */

export const listUsers = () => apiGet<UserListResponse>('/users');

export const createUser = (input: { username: string; password: string; role?: string }) =>
  apiPost<{ user: User }>('/users', input);

export const updateUser = (userId: string, input: { role?: string; enabled?: boolean }) =>
  apiPatch<{ user: User }>(`/users/${userId}`, input);

export const deleteUser = (userId: string) => apiDelete<void>(`/users/${userId}`);

export const revokeUserSessions = (userId: string) =>
  apiPost<void>(`/users/${userId}/sessions/revoke`);

/* --------------------------- signup requests ------------------------ */

export const requestSignup = (input: { username: string; password: string }) =>
  apiPost<{ request: PendingSignup }>('/auth/signup-requests', input);

export const listPendingSignups = () => apiGet<PendingSignupListResponse>('/signup-requests');

export const approveSignup = (id: string, role?: string) =>
  apiPost<{ user: User }>(`/signup-requests/${id}/approve`, role ? { role } : {});

export const denySignup = (id: string) => apiPost<void>(`/signup-requests/${id}/deny`);

export const setSignupRequestsEnabled = (enabled: boolean) =>
  apiPut<{ enabled: boolean }>('/settings/signup-requests', { enabled });

/* ------------------------------ shares ----------------------------- */

/** The public share view is unauthenticated, so it needs its own helpers. */
export const getPublicShare = (token: string, password?: string) =>
  shareRequest<{ share: PublicShareInfo }>(`/shares/${token}`, password);

export const listPublicShareFiles = (token: string, password?: string, params: SearchParams = {}) =>
  shareRequest<FileListResponse>(`/shares/${token}/files${query({ ...params })}`, password);

export const authenticatePublicShare = (token: string, password: string) =>
  apiPost<{ authenticated: boolean }>(`/shares/${token}/authenticate`, { password });

export const getPublicShareFile = (token: string, fileId: string, password?: string) =>
  shareRequest<{ file: FileSummary }>(`/shares/${token}/files/${fileId}`, password);

/**
 * A share's only byte route is `download`, and the server sends it as an
 * attachment — so it is a link to click, never an `<img src>`. There is
 * deliberately no public thumbnail: a share is narrow on purpose.
 */
export const publicShareDownloadUrl = (token: string, fileId: string): string =>
  `${API_BASE}/shares/${token}/files/${fileId}/download`;

/**
 * Downloading a share file needs the password header, which an `<a download>`
 * cannot carry. The public view therefore fetches the bytes and saves them via
 * an object URL instead of navigating.
 */
export async function downloadPublicShareFile(
  token: string,
  fileId: string,
  fileName: string,
  password?: string,
): Promise<void> {
  const response = await fetch(publicShareDownloadUrl(token, fileId), {
    headers: password ? { 'X-Orchard-Share-Password': password } : {},
  });
  if (!response.ok) {
    throw new ApiError(
      {
        code: 'DOWNLOAD_FAILED',
        message: `Could not download ${fileName}.`,
        request_id: '',
      },
      response.status,
    );
  }
  const blob = await response.blob();
  const objectUrl = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = objectUrl;
  anchor.download = fileName;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  // Give the browser a tick to start the save before releasing the blob.
  setTimeout(() => URL.revokeObjectURL(objectUrl), 0);
}

/**
 * A share request carries no session — the token and optional password are
 * the only credentials — so it goes through apiRequest directly with the
 * password header rather than the cookie-based helpers.
 */
function shareRequest<T>(path: string, password?: string): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (password) headers['X-Orchard-Share-Password'] = password;
  return apiRequest<T>(path, { headers });
}

/**
 * Typed endpoint wrappers for the parts of the API the UI reads most.
 *
 * Keeping URL construction and envelope unwrapping in one place is what lets a
 * React Native client (see docs/mobile-development.md) be written from the API
 * documentation alone: every call below corresponds to one documented route,
 * and the shapes come from the shared types module.
 */

import {
  API_BASE,
  ApiError,
  apiDelete,
  apiGet,
  apiPatch,
  apiPost,
  apiPut,
  apiRequest,
  apiUpload,
  apiUploadWithProgress,
  query,
} from './client';
import type { UploadProgress } from './client';
import type {
  Album,
  AlbumListResponse,
  BackupRecord,
  DuplicatesResponse,
  FaceStatus,
  FaceSummary,
  FileCollectionResponse,
  FileListResponse,
  FileMetadata,
  FileNote,
  FileSummary,
  FolderListResponse,
  GrantListResponse,
  IndexStatus,
  IndexTriggerResponse,
  Library,
  LibraryCreateResponse,
  LibraryListResponse,
  LibraryProbeResponse,
  Memory,
  MemoryListResponse,
  MemoryRef,
  MemoryVersion,
  MLStatus,
  PermissionGrant,
  Person,
  PersonDetail,
  PersonEnvelope,
  PublicShareInfo,
  ShareCreateResponse,
  ShareListResponse,
  SimilarFilesResponse,
  SimilarityGroupsResponse,
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
export type {
  Album,
  DuplicateGroup,
  FileMetadata,
  FileSummary,
  Folder,
  Memory,
  Person,
  PersonDetail,
  SimilarFile,
  SimilarityGroup,
  Tag,
} from './types';
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
    albums: number;
    tags: number;
    favorites: number;
  }>(`/libraries/${libraryId}/files/counts`);

export const listFolders = (libraryId: string, parent?: string) =>
  apiGet<FolderListResponse>(`/libraries/${libraryId}/folders${query({ parent })}`);

export const createFolder = (libraryId: string, path: string) =>
  apiPost<{ folder: import('./types').Folder }>(`/libraries/${libraryId}/folders`, { path });

export const getFile = (libraryId: string, fileId: string) =>
  apiGet<{ file: FileSummary }>(`/libraries/${libraryId}/files/${fileId}`);

export const getFileMetadata = (libraryId: string, fileId: string) =>
  apiGet<{ metadata: FileMetadata }>(`/libraries/${libraryId}/files/${fileId}/metadata`);

export const listDuplicates = (libraryId: string, cursor?: string, limit = 50) =>
  apiGet<DuplicatesResponse>(`/libraries/${libraryId}/files/duplicates${query({ cursor, limit })}`);

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

/* ----------------------------- search ------------------------------ */

export interface SearchParams {
  q?: string | undefined;
  type?: MediaFilter | undefined;
  folder?: string | undefined;
  tag?: string | undefined;
  album?: string | undefined;
  person?: string | undefined;
  min_size?: number | undefined;
  max_size?: number | undefined;
  from?: string | undefined;
  to?: string | undefined;
  cursor?: string | undefined;
  limit?: number | undefined;
}

export const searchFiles = (libraryId: string, params: SearchParams = {}) =>
  apiGet<FileListResponse>(`/libraries/${libraryId}/search${query({ ...params })}`);

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

/* ------------------------------ albums ----------------------------- */

export const listAlbums = (libraryId: string) =>
  apiGet<AlbumListResponse>(`/libraries/${libraryId}/albums`);

export const createAlbum = (libraryId: string, name: string, description?: string) =>
  apiPost<{ album: Album }>(
    `/libraries/${libraryId}/albums`,
    description ? { name, description } : { name },
  );

/** Rename, describe, or set the cover of an album. An empty cover clears it. */
export const updateAlbum = (
  libraryId: string,
  albumId: string,
  input: { name?: string; description?: string; cover_file_id?: string },
) => apiPatch<{ album: Album }>(`/libraries/${libraryId}/albums/${albumId}`, input);

export const deleteAlbum = (libraryId: string, albumId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/albums/${albumId}`);

export const listAlbumFiles = (libraryId: string, albumId: string) =>
  apiGet<FileCollectionResponse>(`/libraries/${libraryId}/albums/${albumId}/files`);

export const addAlbumFile = (libraryId: string, albumId: string, fileId: string) =>
  apiPost<void>(`/libraries/${libraryId}/albums/${albumId}/files/${fileId}`);

export const removeAlbumFile = (libraryId: string, albumId: string, fileId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/albums/${albumId}/files/${fileId}`);

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

/* ----------------------------- memories ---------------------------- */

export const listMemories = (libraryId: string, params: { q?: string; limit?: number } = {}) =>
  apiGet<MemoryListResponse>(`/libraries/${libraryId}/memories${query({ ...params })}`);

export const createMemory = (
  libraryId: string,
  input: { title: string; body: string; memory_date?: string },
) => apiPost<{ memory: Memory }>(`/libraries/${libraryId}/memories`, input);

export const updateMemory = (
  libraryId: string,
  memoryId: string,
  input: { title: string; body: string; memory_date?: string; clear_date?: boolean },
) => apiPut<{ memory: Memory }>(`/libraries/${libraryId}/memories/${memoryId}`, input);

export const deleteMemory = (libraryId: string, memoryId: string) =>
  apiDelete<void>(`/libraries/${libraryId}/memories/${memoryId}`);

export const restoreMemory = (libraryId: string, memoryId: string) =>
  apiPost<void>(`/libraries/${libraryId}/memories/${memoryId}/restore`, {});

export const listMemoryVersions = (libraryId: string, memoryId: string) =>
  apiGet<{ versions: MemoryVersion[] }>(`/libraries/${libraryId}/memories/${memoryId}/versions`);

export const getMemoryVersion = (libraryId: string, memoryId: string, version: number) =>
  apiGet<{ version: MemoryVersion }>(
    `/libraries/${libraryId}/memories/${memoryId}/versions/${version}`,
  );

/**
 * The resources a memory mentions. References are written inline in the
 * Markdown body as `[[media:<file-id>]]`, so this is the only way to discover
 * that, say, a memory is about a particular photo.
 */
export const listMemoryRefs = (libraryId: string, memoryId: string) =>
  apiGet<{ refs: MemoryRef[] }>(`/libraries/${libraryId}/memories/${memoryId}/refs`);

export const getMemory = (libraryId: string, memoryId: string) =>
  apiGet<{ memory: Memory }>(`/libraries/${libraryId}/memories/${memoryId}`);

/* ------------------------------ people ----------------------------- */

export const listPeople = (libraryId: string) =>
  apiGet<{ people: Person[] }>(`/libraries/${libraryId}/people`);

export const createPerson = (libraryId: string, name: string) =>
  apiPost<PersonEnvelope>(`/libraries/${libraryId}/people`, { name });

export const renamePerson = (libraryId: string, personId: string, name: string) =>
  apiPost<{ renamed: boolean }>(`/libraries/${libraryId}/people/${personId}/rename`, { name });

/** Delete a person; with `withFaces` their faces go too, so the group is not rebuilt. */
export const deletePerson = (libraryId: string, personId: string, withFaces = false) =>
  apiDelete<{ deleted: boolean; faces_removed?: number }>(
    `/libraries/${libraryId}/people/${personId}${withFaces ? '?faces=delete' : ''}`,
  );

/** Delete one detected face for good; it is not detected again. */
export const deleteFace = (libraryId: string, faceId: string) =>
  apiDelete<{ deleted: boolean }>(`/libraries/${libraryId}/faces/${faceId}`);

export const getFaceStatus = (libraryId: string) =>
  apiGet<FaceStatus>(`/libraries/${libraryId}/ml/faces`);

/**
 * The unassigned-face pool, i.e. the faces clustering has not yet given a
 * name to. Available only when the server was built with face support;
 * otherwise the route is absent and the call 404s.
 */
export const listUnassignedFaces = (libraryId: string) =>
  apiGet<{ faces: FaceSummary[] }>(`/libraries/${libraryId}/faces`);

/**
 * A person plus the faces assigned to them. The faces are a sibling of
 * `person`, not a field on it — the listing does not include them, so they
 * cost a request and are only fetched when a card is opened.
 */
export const getPerson = (libraryId: string, personId: string) =>
  apiGet<PersonDetail>(`/libraries/${libraryId}/people/${personId}`);

export const setPersonCover = (libraryId: string, personId: string, faceId: string) =>
  apiPost<{ cover_set: boolean }>(`/libraries/${libraryId}/people/${personId}/cover`, {
    face_id: faceId,
  });

/**
 * Merge `sourceId` into `personId`: the source person disappears and their
 * faces move to the target. The direction matters — the URL is the survivor.
 */
export const mergePeople = (libraryId: string, personId: string, sourceId: string) =>
  apiPost<{ merged: boolean; consolidated?: number }>(
    `/libraries/${libraryId}/people/${personId}/merge`,
    {
      source_person_id: sourceId,
    },
  );

export const assignFace = (libraryId: string, personId: string, faceId: string) =>
  apiPost<{ assigned: boolean }>(`/libraries/${libraryId}/people/${personId}/faces/${faceId}`, {});

export const unassignFace = (libraryId: string, personId: string, faceId: string) =>
  apiDelete<{ unassigned: boolean }>(`/libraries/${libraryId}/people/${personId}/faces/${faceId}`);

export const faceImageUrl = (libraryId: string, faceId: string): string =>
  `${API_BASE}/libraries/${libraryId}/faces/${faceId}/image`;

/* ---------------------------- face passes ---------------------------- */

export const runFacePass = (libraryId: string) =>
  apiPost<{ library_id: string; status: string }>(`/libraries/${libraryId}/ml/faces/pass`);

export const clusterFaces = (libraryId: string) =>
  apiPost<{ library_id: string; status: string }>(`/libraries/${libraryId}/ml/faces/cluster`);

export const purgeFaces = (libraryId: string) =>
  apiPost<{ library_id: string; faces_removed: number }>(`/libraries/${libraryId}/ml/faces/purge`);

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

/* --------------------------------- ml ------------------------------ */

export const getMLStatus = (libraryId: string) => apiGet<MLStatus>(`/libraries/${libraryId}/ml`);

export const runSimilarityPass = (libraryId: string) =>
  apiPost<{ library_id: string; status: string }>(`/libraries/${libraryId}/ml/similarity/pass`);

export const purgeSimilarity = (libraryId: string) =>
  apiPost<{ library_id: string; purged: number }>(`/libraries/${libraryId}/ml/purge`);

export const getSimilarFiles = (libraryId: string, fileId: string) =>
  apiGet<SimilarFilesResponse>(`/libraries/${libraryId}/files/${fileId}/similar`);

export const listSimilarityGroups = (libraryId: string) =>
  apiGet<SimilarityGroupsResponse>(`/libraries/${libraryId}/ml/similarity/groups`);

/* ------------------------------ backups ---------------------------- */

/** GET /backups returns a bare JSON array, unlike every other listing. */
export const listBackups = () => apiGet<BackupRecord[]>('/backups');

export const getBackup = (id: string) => apiGet<BackupRecord>(`/backups/${id}`);

export const runBackup = () => apiPost<BackupRecord>('/backups', {});

export const verifyBackup = (id: string) => apiPost<BackupRecord>(`/backups/${id}/verify`);

export const restoreBackup = (id: string, destination: string) =>
  apiPost<BackupRecord>(`/backups/${id}/restore`, { destination });

/* ------------------------------- users ----------------------------- */

export const listUsers = () => apiGet<UserListResponse>('/users');

export const createUser = (input: { username: string; password: string; role?: string }) =>
  apiPost<{ user: User }>('/users', input);

export const revokeUserSessions = (userId: string) =>
  apiPost<void>(`/users/${userId}/sessions/revoke`);

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
    headers: password ? { 'X-Cairn-Share-Password': password } : {},
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
  if (password) headers['X-Cairn-Share-Password'] = password;
  return apiRequest<T>(path, { headers });
}

/* ------------------------------ ml switch ------------------------------ */

export interface MLSettings {
  enabled: boolean;
  /** How alike two faces must be to be grouped as one person (0..1). */
  face_threshold: number;
  face_threshold_default: number;
  /** New, unprocessed images that must pile up before the models run. */
  batch_size: number;
}

export const getMLSettings = () => apiGet<MLSettings>('/ml/settings');

export const setMLSettings = (enabled: boolean) => apiPut<MLSettings>('/ml/settings', { enabled });

/** Set the face-matching threshold; 0 restores the model's default. */
export const setFaceThreshold = (face_threshold: number) =>
  apiPut<MLSettings>('/ml/settings', { face_threshold });

/** Run the models once this many new images are waiting (1 = every image). */
export const setBatchSize = (batch_size: number) =>
  apiPut<MLSettings>('/ml/settings', { batch_size });

/* --------------------------- system capabilities --------------------------- */

export interface SystemCapabilities {
  /** Whether the server found ffmpeg (video thumbnails). */
  ffmpeg: boolean;
  /** Whether the learned face-recognition model is loaded. */
  face_model: boolean;
}

export const getSystemCapabilities = () => apiGet<SystemCapabilities>('/system/capabilities');

/* ----------------------- face-recognition model download ---------------------- */

export interface FaceModelStatus {
  installed: boolean;
  state: 'idle' | 'downloading' | 'done' | 'error';
  downloaded: number;
  /** 0 when the server did not report a size. */
  total: number;
  error?: string;
  url: string;
}

export const getFaceModelStatus = () => apiGet<FaceModelStatus>('/ml/model');

export const startFaceModelDownload = () => apiPost<FaceModelStatus>('/ml/model/download', {});

/** Shared API types. Kept in one place so clients stay consistent. */

export interface HealthResponse {
  status: 'ok' | 'degraded';
  database: 'ok' | 'error';
}

export type UserRole = 'admin' | 'user';

export interface User {
  id: string;
  username: string;
  role: UserRole;
  created_at: string;
}

export interface UserEnvelope {
  user: User;
}

export interface UserListResponse {
  users: User[];
}

/** GET /auth/status — public bootstrap + authentication state. */
export interface AuthStatusResponse {
  bootstrap_required: boolean;
  authenticated: boolean;
  user?: User;
}

export interface VersionResponse {
  version: string;
  commit: string;
  build_date: string;
  go_version: string;
  platform: string;
}

/** Connectivity of a library's backing storage. */
export type LibraryStatus = 'online' | 'offline';

/**
 * A registered storage location. `root` is the last known absolute path and
 * may be stale while the library is offline; identity always follows `id`.
 */
export interface Library {
  id: string;
  name: string;
  root: string;
  status: LibraryStatus;
  volume_id?: string;
  schema_version: number;
  created_at: string;
  updated_at: string;
}

export interface LibraryListResponse {
  libraries: Library[];
}

export interface LibraryEnvelope {
  library: Library;
}

/** What a probe can learn about a candidate path before registering it. */
export interface LibraryProbe {
  path_exists: boolean;
  is_directory: boolean;
  is_writable: boolean;
  has_metadata: boolean;
  existing_id?: string;
  existing_name?: string;
  registered: boolean;
  volume_id?: string;
}

export interface LibraryProbeResponse {
  probe: LibraryProbe;
}

/** Whether a registration created fresh metadata or adopted an existing one. */
export type LibraryAdoptMode = 'create' | 'adopt' | string;

export interface LibraryCreateResponse {
  library: Library;
  mode: LibraryAdoptMode;
}

/** The persistent state of a library's last indexing pass. */
/**
 * GET /libraries/{id}/index/status.
 *
 * The counts are the library's whole index, not the last scan's progress, so
 * there is no percentage to show — "1,204 files indexed" is the truth.
 */
export interface IndexStatus {
  library_id?: string;
  /** Indexed files currently present on disk. */
  present?: number;
  /** Indexed files whose bytes are currently unavailable. */
  missing?: number;
  /** Indexed files sitting in the library trash. */
  deleted?: number;
  /** The running or next-queued scan, when any. */
  active_job?: IndexJob;
  /** Id of the most recent scan, when any. */
  last_job_id?: string;
}

/**
 * A background job as the server serializes it. The struct carries no JSON
 * tags, so these keys are capitalised — `Status`, not `status`.
 */
export interface IndexJob {
  ID: string;
  Kind: string;
  Status: 'queued' | 'running' | 'completed' | 'failed' | 'cancelled' | string;
  ErrorMsg?: string;
  FinishedAt?: string;
}

/** POST /libraries/{id}/index — a scan was accepted for background execution. */
export interface IndexTriggerResponse {
  job_id?: string;
  library_id?: string;
}

export type RefType = 'media' | 'memory' | 'album' | 'person' | 'tag';

export interface MemoryRef {
  type: RefType;
  id: string;
}

export interface Memory {
  id: string;
  title: string;
  body: string;
  memory_date?: string;
  deleted: boolean;
  created_at: string;
  updated_at: string;
}

export interface MemoryListResponse {
  memories: Memory[];
  next_cursor?: string;
}

export interface MemoryVersion {
  memory_id: string;
  version: number;
  title: string;
  body: string;
  saved_at: string;
}

/** Optional local-ML capability status for a library. */
export interface MLStatus {
  enabled: boolean;
  provider?: string;
  provider_version?: number;
  /** Photos with a stored signature. */
  signatured?: number;
  /** Present photos with no signature yet. */
  pending?: number;
  /** A similarity pass is running or queued on the server. */
  running?: boolean;
  last_pass_at?: string;
  error?: string;
}

export interface FaceStatus {
  enabled: boolean;
  provider: string;
  provider_version: number;
  faces: number;
  people: number;
  unassigned: number;
  /** A detection or grouping pass is running or queued on the server. */
  running?: boolean;
  /** Photos waiting for face detection. */
  pending?: number;
}

export interface Person {
  id: string;
  name: string;
  cover_face_id?: string;
  cover_file_id?: string;
  face_count: number;
  created_at: string;
  updated_at: string;
}

export interface PersonEnvelope {
  person: Person;
}

/** `GET /people/{id}` returns the person with its faces alongside it. */
export interface PersonDetail extends PersonEnvelope {
  faces: FaceSummary[];
}

export interface FaceSummary {
  id: string;
  file_id: string;
  file_path?: string;
  x: number;
  y: number;
  width: number;
  height: number;
  confidence: number;
}

export interface FileSummary {
  id: string;
  library_id: string;
  rel_path: string;
  name: string;
  folder_path: string;
  size_bytes: number;
  mod_time: string;
  media_type: string;
  mime_type: string;
  status: string;
  content_hash?: string;
}

export interface FileEnvelope {
  file: FileSummary;
}

export interface FileListResponse {
  files: FileSummary[];
  next_cursor?: string;
  total: number;
}

/** A nearest match from perceptual-hash similarity, when ML is enabled. */
export interface SimilarFile {
  file_id: string;
  file_path: string;
  distance: number;
  similarity: number;
  file?: FileSummary;
}

export interface SimilarFilesResponse {
  similar: SimilarFile[];
}

export interface DuplicateGroup {
  content_hash: string;
  size_bytes: number;
  files: FileSummary[];
}

export interface DuplicatesResponse {
  groups: DuplicateGroup[];
  next_cursor?: string;
  total: number;
}

/** A cluster of present photos whose perceptual signatures are close enough
 * to one another to be visual near-duplicates. Unlike a `DuplicateGroup`
 * there is no single hash the group shares — members are connected by a
 * chain of near-enough distances, not an exact match. */
export interface SimilarityGroup {
  files: FileSummary[];
}

export interface SimilarityGroupsResponse {
  groups: SimilarityGroup[];
  total: number;
}

export interface Folder {
  id: string;
  library_id?: string;
  rel_path: string;
  parent_id?: string;
  name: string;
  file_count: number;
}

export interface FolderListResponse {
  folders: Folder[];
}

export interface Album {
  id: string;
  name: string;
  description?: string;
  cover_file_id?: string;
  created_at: string;
  updated_at: string;
  /** How many files the album holds. */
  file_count?: number;
  /** The file that represents the album: its cover, else its first photo. */
  preview_file_id?: string;
}

export interface AlbumListResponse {
  albums: Album[];
}

export interface AlbumEnvelope {
  album: Album;
}

export interface Tag {
  id: string;
  name: string;
  color?: string;
  created_at: string;
}

export interface TagListResponse {
  tags: Tag[];
}

export interface TagEnvelope {
  tag: Tag;
}

export interface FileCollectionResponse {
  files: FileSummary[];
}

/** A per-file Markdown note (caption / description) shown under the media. */
export interface FileNote {
  file_id: string;
  body: string;
  updated_at?: string;
}

/** Extracted media metadata (dimensions, camera, GPS) for a file. */
export interface FileMetadata {
  file_id: string;
  media_type?: string;
  mime_type?: string;
  width?: number;
  height?: number;
  duration_secs?: number;
  taken_at?: string;
  camera_make?: string;
  camera_model?: string;
  latitude?: number;
  longitude?: number;
  has_thumbnail: boolean;
  updated_at?: string;
}

/* ------------------------------------------------------------------ *
 * Authorization
 * ------------------------------------------------------------------ */

/** The closed capability set from ADR-0005. */
export type Capability =
  'read' | 'download' | 'create' | 'edit' | 'move' | 'delete' | 'share' | 'manage';

export const ALL_CAPABILITIES: Capability[] = [
  'read',
  'download',
  'create',
  'edit',
  'move',
  'delete',
  'share',
  'manage',
];

export const CAPABILITY_LABEL: Record<Capability, string> = {
  read: 'View',
  download: 'Download',
  create: 'Add',
  edit: 'Edit',
  move: 'Move',
  delete: 'Delete',
  share: 'Share',
  manage: 'Manage',
};

export type GrantEffect = 'allow' | 'deny';

export interface PermissionGrant {
  id: string;
  user_id: string;
  resource_key: string;
  capabilities: Capability[];
  effect: GrantEffect;
  created_at: string;
}

export interface GrantListResponse {
  grants: PermissionGrant[];
}

export interface GrantEnvelope {
  grant: PermissionGrant;
}

/* ------------------------------------------------------------------ *
 * Sharing
 * ------------------------------------------------------------------ */

export interface Share {
  id: string;
  resource_key: string;
  capabilities: Capability[];
  has_password: boolean;
  expires_at?: string;
  revoked_at?: string;
  created_at: string;
}

/** Creating a share returns the raw token exactly once. */
export interface ShareCreateResponse {
  share: Share;
  token: string;
}

export interface ShareListResponse {
  shares: Share[];
}

/**
 * What an anonymous visitor to a share link may learn before unlocking it.
 * `library` is the library's *name*, not an object — the public route leaks no
 * ids, only the scope and the label a visitor would see.
 */
export interface PublicShareInfo {
  resource_key: string;
  library?: string;
  has_password: boolean;
  expires_at?: string;
}

export interface PublicShareInfoResponse {
  share: PublicShareInfo;
}

/* ------------------------------------------------------------------ *
 * Backups
 * ------------------------------------------------------------------ */

export type BackupStatus = 'running' | 'ok' | 'failed' | string;

export interface BackupRecord {
  id: string;
  status: BackupStatus;
  destination: string;
  /** The server always sets this; a record without it is not a record. */
  started_at: string;
  finished_at?: string;
  libraries: number;
  files: number;
  files_skipped: number;
  bytes: number;
  stored_bytes: number;
  encrypted: boolean;
  same_device: boolean;
  verify_status?: string;
  verify_checked?: number;
  verify_errors?: number;
  error_msg?: string;
  created_at: string;
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value < 10 && unit > 0 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`;
}

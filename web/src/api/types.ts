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
  enabled: boolean;
  created_at: string;
}

export interface UserEnvelope {
  user: User;
}

export interface UserListResponse {
  users: User[];
}

/** A public "request an account" submission awaiting admin approval or denial. */
export interface PendingSignup {
  id: string;
  username: string;
  requested_at: string;
}

export interface PendingSignupListResponse {
  requests: PendingSignup[];
}

/** GET /auth/status — public bootstrap + authentication state. */
export interface AuthStatusResponse {
  bootstrap_required: boolean;
  authenticated: boolean;
  signup_requests_enabled: boolean;
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

/** Book-style metadata for a document file, library-wide (not per-user). */
export interface BookMetadata {
  file_id: string;
  title: string;
  author: string;
  publisher: string;
  year: string;
  isbn: string;
  description: string;
  cover_url?: string;
  user_edited: boolean;
  extracted_at?: string;
}

export interface BookMetadataEnvelope {
  book: BookMetadata;
}

/** A user's progress through a book. null means no status has been set. */
export type ReadingStatus = 'want_to_read' | 'reading' | 'completed' | 'on_hold';

export const READING_STATUS_LABEL: Record<ReadingStatus, string> = {
  want_to_read: 'Want to read',
  reading: 'Reading',
  completed: 'Completed',
  on_hold: 'On hold',
};

export interface ReadingStatusResponse {
  status: ReadingStatus | null;
}

/** One set of present files that share a content hash. */
export interface DuplicateGroup {
  content_hash: string;
  files: FileSummary[];
}

export interface DuplicateGroupListResponse {
  groups: DuplicateGroup[];
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

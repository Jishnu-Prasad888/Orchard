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
  /** Whether the signed-in user may create share links here. Set by the
   * library list. */
  can_share?: boolean;
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
  /** The most recently finished scan's summary — independent of whether
   * another scan is active or queued right now. */
  last_scan?: ScanSummary;
  /** Changes whenever the library's contents may have — files, covers,
   * edits from any device. Compare it to know when to re-read listings. */
  revision?: string;
}

/** One path that failed during a scan, with a short reason. */
export interface ScanError {
  Path: string;
  Reason: string;
}

/** The gist of a finished scan: aggregate counts plus capped detail lists
 * (see the server's maxScanDetailEntries) — enough to show "what happened"
 * without the full per-file change list. */
export interface ScanSummary {
  total_files: number;
  new_files: number;
  modified: number;
  moved: number;
  missing: number;
  unchanged: number;
  errors: number;
  error_details?: ScanError[];
  missing_paths?: string[];
  started_at: string;
  finished_at: string;
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
  /** Set only for a document file with an extracted or uploaded cover —
   * absent otherwise, including for non-document media types. */
  cover_url?: string;
  /** A photo's small grid image, once the server has rendered it. */
  thumbnail_url?: string;
  /** How far the signed-in user has read this book, 0-100. Set by file
   * listings only, and absent for a book they have not started. */
  progress_percent?: number;
  /** Whether the signed-in user has marked this file completed. Set by file
   * listings only; absent means not completed. */
  completed?: boolean;
  /** A book's own title, from its metadata. Set by file listings only. */
  title?: string;
  /** Names of the file's tags. Set by file listings only. */
  tags?: string[];
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

/** A personal, user-owned grouping of files — unlike a Tag, never shared
 * with other users of the same library. */
export interface Collection {
  id: string;
  name: string;
  description?: string;
  pinned: boolean;
  item_count: number;
  created_at: string;
  updated_at: string;
}

export interface CollectionListResponse {
  collections: Collection[];
}

export interface CollectionEnvelope {
  collection: Collection;
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

/** A user's resume point in a book. location is opaque — a page number for
 * PDF, a CFI for EPUB, a scroll fraction for Markdown/text — meaningful
 * only to the reader that wrote it. percent is reader-agnostic (0-100). */
export interface ReadingProgress {
  file_id: string;
  location: string;
  percent: number;
  updated_at?: string;
}

export interface ReadingProgressResponse {
  progress: ReadingProgress | null;
}

/** `auto` follows the app's own light/dark appearance. */
export type ReaderTheme = 'auto' | 'light' | 'dark' | 'sepia';

/** A user's reading preferences, applied across every book (no per-book
 * override yet). */
export interface ReaderPreferences {
  font_size: number;
  line_spacing: number;
  margin: number;
  reading_width: number;
  theme: ReaderTheme;
  /** Which way the book advances: `horizontal` turns pages side to side,
   * `vertical` scrolls them top to bottom. */
  reading_direction: ReadingDirection;
  /** Pages shown next to each other: one, or a two-page spread. */
  pages_per_view: PagesPerView;
}

export type ReadingDirection = 'horizontal' | 'vertical';
export type PagesPerView = 1 | 2;

export const DEFAULT_READER_PREFERENCES: ReaderPreferences = {
  font_size: 18,
  line_spacing: 1.5,
  margin: 24,
  reading_width: 720,
  theme: 'auto',
  reading_direction: 'horizontal',
  pages_per_view: 1,
};

export interface ReaderPreferencesResponse {
  preferences: ReaderPreferences;
}

/** A place in a file a user wants to return to. location is opaque — a page
 * number for PDF, a CFI for EPUB, a scroll anchor for Markdown/text —
 * meaningful only to the reader that wrote it, following the same
 * convention as ReadingProgress.location. */
export interface Bookmark {
  id: string;
  file_id: string;
  location: string;
  label?: string;
  created_at: string;
}

export interface BookmarkListResponse {
  bookmarks: Bookmark[];
}

export interface BookmarkEnvelope {
  bookmark: Bookmark;
}

/** A user-selected passage of text, with an optional note and color. */
export interface Highlight {
  id: string;
  file_id: string;
  location: string;
  text: string;
  color: string;
  note?: string;
  created_at: string;
  updated_at: string;
}

export const HIGHLIGHT_COLORS = ['yellow', 'green', 'blue', 'pink', 'orange'] as const;
export type HighlightColor = (typeof HIGHLIGHT_COLORS)[number];

export interface HighlightListResponse {
  highlights: Highlight[];
}

export interface HighlightEnvelope {
  highlight: Highlight;
}

/* ---------------------------- dictionaries ---------------------------- */

export type DictionaryKind = 'wordnet' | 'freedict';

/** A dictionary installed on the server. */
export interface Dictionary {
  id: string;
  kind: DictionaryKind;
  name: string;
  description: string;
  version: string;
  license: string;
  size_bytes: number;
  installed_at: string;
  enabled: boolean;
}

/** A dictionary that can be installed. */
export interface DictionarySource {
  id: string;
  kind: DictionaryKind;
  name: string;
  description: string;
  version: string;
  license: string;
  download_bytes: number;
  headwords?: number;
}

export type DictionaryJobPhase =
  'downloading' | 'verifying' | 'extracting' | 'indexing' | 'done' | 'failed' | 'cancelled';

/** The current or most recent install. `done`/`total` measure the current
 * phase; `total` is 0 when it can't be known. */
export interface DictionaryJob {
  id: string;
  dictionary_id: string;
  name: string;
  phase: DictionaryJobPhase;
  done: number;
  total: number;
  error?: string;
  started_at: string;
  finished_at?: string;
}

export interface DictionaryListResponse {
  dictionaries: Dictionary[];
  /** Whether any installed dictionary is on — whether the reader offers
   * "Search in dictionary". */
  available: boolean;
  job: DictionaryJob | null;
}

export interface DictionaryCatalogResponse {
  sources: DictionarySource[];
  /** Why FreeDict's list couldn't be loaded; WordNet is offered anyway. */
  freedict_error?: string;
}

export interface DictionarySense {
  definition: string;
  examples?: string[];
  synonyms?: string[];
  notes?: string[];
}

export interface DictionaryEntry {
  headword: string;
  part_of_speech?: string;
  pronunciation?: string;
  senses: DictionarySense[];
}

export interface DictionaryResult {
  dictionary_id: string;
  dictionary_name: string;
  entries: DictionaryEntry[];
}

export interface DictionaryLookupResponse {
  word: string;
  results: DictionaryResult[];
}

/** A word looked up in the reader and kept, with the meaning read. */
export interface SavedWord {
  id: string;
  file_id: string;
  word: string;
  location: string;
  /** The exact text selected in the book, found again to paint it. */
  text: string;
  definition: string;
  source?: string;
  created_at: string;
}

export interface SavedWordListResponse {
  words: SavedWord[];
}

export interface SavedWordEnvelope {
  word: SavedWord;
}

/** A saved passage of text with citation details and commentary,
 * independent of any highlight — it can be typed in by hand. */
export interface Quote {
  id: string;
  file_id: string;
  text: string;
  location?: string | undefined;
  author?: string | undefined;
  chapter?: string | undefined;
  page?: string | undefined;
  commentary?: string | undefined;
  favorite: boolean;
  created_at: string;
  updated_at: string;
}

export interface QuoteListResponse {
  quotes: Quote[];
}

export interface QuoteEnvelope {
  quote: Quote;
}

/** A file the acting user has started reading but not finished, carrying
 * their resume point alongside the usual file fields. */
export interface InProgressFile extends FileSummary {
  percent: number;
  location: string;
  updated_at?: string;
}

export interface InProgressListResponse {
  files: InProgressFile[];
}

/** One Home dashboard section's position and visibility, as the user has
 * (or hasn't) customized it. */
export type HomeSectionId =
  | 'continue-reading'
  | 'recently-opened'
  | 'recently-added'
  | 'favorites'
  | 'pinned-collections'
  | 'recent-quotes';

export interface HomeLayoutSection {
  id: HomeSectionId;
  visible: boolean;
}

export interface HomeLayoutResponse {
  sections: HomeLayoutSection[];
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
  /** What the grant applies to, in words: kind and path. */
  scope_kind: ShareTargetKind;
  scope_path?: string;
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

/** What a share link exposes. The server builds the link's scope from this
 * and checks that the thing exists; nothing else is accepted. */
export type ShareTarget =
  | { kind: 'library' }
  | { kind: 'folder'; path: string }
  | { kind: 'file'; id: string }
  | { kind: 'tag'; id: string }
  | { kind: 'collection'; id: string };

export type ShareTargetKind = ShareTarget['kind'];

export const SHARE_KIND_LABEL: Record<ShareTargetKind, string> = {
  library: 'Library',
  folder: 'Folder',
  file: 'File',
  tag: 'Tag',
  collection: 'Collection',
};

export interface Share {
  id: string;
  resource_key: string;
  /** What the link exposes, in words. */
  target_kind: ShareTargetKind;
  target_name: string;
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
  kind: ShareTargetKind;
  /** The shared thing's name: a folder, file, tag or collection. */
  name: string;
  library?: string;
  has_password: boolean;
  expires_at?: string;
  /** Whether the link lets visitors download, not only see the list. */
  can_download: boolean;
  /** For a folder share: the folder browsing starts in. */
  folder?: string;
}

/** A share's listing: files, and for a library or folder share the
 * sub-folders of the folder being shown. */
export interface PublicShareListing extends FileListResponse {
  folders?: Folder[];
  folder?: string;
}

export interface PublicShareInfoResponse {
  share: PublicShareInfo;
}

/* ------------------------------------------------------------------ *
 * Backups
 * ------------------------------------------------------------------ */

/** As the server sends it. (`ok` was never one: the page used to look for
 * it, which drew every good backup as unfinished and kept Verify and Restore
 * disabled.) */
export type BackupStatus = 'running' | 'completed' | 'failed' | 'pruned' | 'restoring';

/** Where backups are written, and how they're kept. */
export interface BackupConfig {
  dir: string;
  /** `setting` (chosen in the app), `env` (ORCHARD_BACKUP_DIR), or
   * `default` (inside Orchard's data directory). */
  dir_source: 'setting' | 'env' | 'default' | '';
  keep: number;
  interval_minutes: number;
  encrypted: boolean;
  /** The folder's drive, and whether it is plugged in right now. */
  drive?: BackupDriveStatus;
  /** Where backups go: the folder above, or the network server. */
  destination?: 'local' | 'remote';
  remote?: { config: BackupRemoteConfig; has_secret: boolean };
}

/** A network destination's settings. Never holds a password. */
export interface BackupRemoteConfig {
  kind: 'sftp' | 'ftp' | 'smb';
  host: string;
  port?: number;
  user?: string;
  path: string;
  share?: string;
  domain?: string;
  ftp_security?: 'explicit' | 'implicit' | 'none';
  /** The confirmed server identity: SSH host key, or a self-signed cert. */
  fingerprint?: string;
}

export interface BackupRemoteSecret {
  password?: string;
  private_key?: string;
  key_passphrase?: string;
}

export interface BackupDriveStatus {
  mount?: string;
  device?: string;
  fs_type?: string;
  /** On the disk the system runs from (a Pi's SD card). */
  system: boolean;
  free: number;
  total: number;
  connected: boolean;
  /** Why backups cannot be written there now. */
  problem?: string;
}

/** A mounted drive a backup folder could go on. */
export interface BackupDrive {
  path: string;
  device: string;
  fs_type: string;
  uuid?: string;
  total: number;
  free: number;
  system: boolean;
}

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
  /** Why it ran. */
  trigger?: BackupTrigger;
}

export type BackupTrigger = 'manual' | 'scheduled' | 'catch-up' | 'size';

export const BACKUP_TRIGGER_LABEL: Record<BackupTrigger, string> = {
  manual: 'By hand',
  scheduled: 'Scheduled',
  'catch-up': 'Missed schedule',
  size: 'New data',
};

/** When backups run by the clock. `at` is "HH:MM" in the server's time. */
export interface BackupSchedule {
  kind: 'off' | 'interval' | 'daily' | 'weekly';
  every_minutes?: number;
  at?: string;
  /** 0 = Sunday. */
  weekday?: number;
  since?: string;
}

/** Back up once new data since the last backup adds up to `bytes`. */
export interface BackupSizeTrigger {
  enabled: boolean;
  bytes: number;
  quiet_minutes: number;
}

export interface BackupPolicy {
  schedule: BackupSchedule;
  size_trigger: BackupSizeTrigger;
  next_run?: string;
  timezone: string;
  pending: { files: number; bytes: number; last_change?: string };
}

/* ------------------------------------------------------------------ *
 * Content search (Phase 6)
 * ------------------------------------------------------------------ */

/** What kind of row a ContentHit came from. */
export type ContentSourceType = 'book_text' | 'metadata' | 'quote' | 'highlight';

/** A match against extracted book text, book metadata, a quote, or a
 * highlight — as opposed to a file search hit, which only ever
 * matches a filename/path. */
export interface ContentHit {
  source_type: ContentSourceType;
  source_id: string;
  file_id?: string | undefined;
  excerpt: string;
  /** The opaque reader location to jump to, when known (quote/highlight
   * hits only — fts_content itself carries no location, so this comes
   * from a best-effort server-side lookup). */
  location?: string | undefined;
}

export interface ContentHitListResponse {
  hits: ContentHit[];
}

/** A search query a user has run recently, remembered per-user so it
 * follows them across devices (not the per-browser localStorage list this
 * replaced). */
export interface RecentSearch {
  query: string;
  searched_at: string;
}

export interface RecentSearchListResponse {
  searches: RecentSearch[];
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

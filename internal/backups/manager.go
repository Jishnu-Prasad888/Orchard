package backups

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/sanitize"
)

// Config drives the backup subsystem.
type Config struct {
	// Dir is the destination directory for backups. Empty disables backups.
	Dir string

	// Keep is how many completed backups to retain; older ones are pruned.
	Keep int

	// Passphrase optionally encrypts every backup payload with the shared
	// AEAD kernel (AES-256-GCM via crypto.NewKeysFromKey, Phase 13/14).
	// Restore requires the same passphrase.
	Passphrase string

	// Interval is the scheduled backup cadence (0 disables scheduling).
	Interval time.Duration
}

// Manager orchestrates backups: an optional scheduler, one-shot runs, sample
// verification, retention pruning, listing, and restore.
type Manager struct {
	db     *sql.DB
	logger *slog.Logger
	libs   *library.Manager
	store  *Store
	cfg    Config
	server string // absolute path of the server-level cairn.db

	busy    atomic.Bool
	skipped atomic.Int64
	trigger chan struct{}
}

// NewManager builds the backup manager. serverDB is the absolute path of the
// server-level cairn.db so it can be snapshotted.
func NewManager(db *sql.DB, logger *slog.Logger, libs *library.Manager, cfg Config, serverDB string) *Manager {
	if cfg.Keep <= 0 {
		cfg.Keep = 4
	}
	return &Manager{
		db:      db,
		logger:  logger,
		libs:    libs,
		store:   NewStore(db),
		cfg:     cfg,
		server:  serverDB,
		trigger: make(chan struct{}, 1),
	}
}

// Store exposes the backup record store for endpoint reads.
func (m *Manager) Store() *Store { return m.store }

// List returns the most recent backup records.
func (m *Manager) List(ctx context.Context, limit int) ([]Record, error) {
	return m.store.List(ctx, limit)
}

// Get returns one backup record.
func (m *Manager) Get(ctx context.Context, id string) (Record, error) {
	return m.store.Get(ctx, id)
}

// BackupConfig exposes the effective configuration (for the API response).
func (m *Manager) BackupConfig() Config { return m.cfg }

// Trigger requests a backup run from the scheduler (non-blocking).
func (m *Manager) Trigger() {
	select {
	case m.trigger <- struct{}{}:
	default:
		m.logger.Warn("backup trigger dropped: a run is already pending/running")
	}
}

// Start launches the scheduler goroutine. Call it exactly once.
func (m *Manager) Start(ctx context.Context) {
	if m.cfg.Dir == "" {
		m.logger.Info("backups disabled: no backup directory configured")
		return
	}
	go func() {
		var tick <-chan time.Time
		if m.cfg.Interval > 0 {
			t := time.NewTicker(m.cfg.Interval)
			defer t.Stop()
			tick = t.C
			m.logger.Info("backup scheduler started",
				"interval", m.cfg.Interval.String(),
				"dir", m.cfg.Dir, "keep", m.cfg.Keep,
				"encrypted", m.cfg.Passphrase != "")
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick:
				m.run(15 * time.Minute)
			case <-m.trigger:
				m.run(4 * time.Hour)
			}
		}
	}()
}

// run is the single-flight wrapper around RunBackup.
func (m *Manager) run(timeout time.Duration) {
	if !m.busy.CompareAndSwap(false, true) {
		m.logger.Warn("backup skipped: another run is in progress")
		return
	}
	defer m.busy.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	rec, err := m.RunBackup(ctx)
	if err != nil {
		m.logger.Error("backup run errored", "error", err)
		return
	}
	m.logger.Info("backup run finished", "backup_id", rec.ID, "status", rec.Status,
		"files", rec.Files, "bytes", rec.Bytes, "stored_bytes", rec.StoredBytes)
}

// Run performs one full backup synchronously and returns the record. It is
// safe to call from API handlers; concurrent runs are rejected.
func (m *Manager) Run(ctx context.Context) (*Record, error) {
	if !m.busy.CompareAndSwap(false, true) {
		return nil, errors.New("a backup is already running")
	}
	defer m.busy.Store(false)
	return m.RunBackup(ctx)
}

// Verify re-checks a completed backup's integrity by restoring a sample of
// entries and comparing plaintext sizes/hashes. Rejected when the backup is
// encrypted but the configured passphrase differs.
func (m *Manager) Verify(ctx context.Context, id string) (*Record, error) {
	rec, err := m.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec.Status != StatusCompleted || rec.Destination == "" {
		return nil, errors.New("backup is not verifiable")
	}
	var key []byte
	if rec.Encrypted {
		meta, err := readHeader(rec.Destination)
		if err != nil {
			return nil, err
		}
		if m.cfg.Passphrase == "" {
			return nil, errors.New("backup is encrypted; configure the passphrase to verify")
		}
		salt, err := hex.DecodeString(meta.Salt)
		if err != nil {
			return nil, err
		}
		key = deriveKey(m.cfg.Passphrase, salt)
	}
	mf, err := readManifest(rec.Destination, key)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	rec.VerifyStatus = ""
	rec.VerifyChecked = 0
	rec.VerifyErrors = 0
	if err := m.verifySample(ctx, &rec, rec.Destination, mf, key); err != nil {
		return nil, err
	}
	if err := m.store.Update(ctx, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// RunBackup performs one full backup synchronously. Callers should normally
// use Run; this is exposed for tests.
func (m *Manager) RunBackup(ctx context.Context) (*Record, error) {
	if m.cfg.Dir == "" {
		return nil, errors.New("backup directory not configured")
	}
	now := time.Now().UTC()
	m.skipped.Store(0)
	rec := &Record{
		ID:        newID(),
		Status:    StatusRunning,
		StartedAt: now,
		CreatedAt: now,
		ServerDB:  m.server,
	}
	if err := m.store.Create(ctx, rec); err != nil {
		return nil, err
	}
	m.logger.Info("backup starting", "backup_id", rec.ID)

	destBase := filepath.Join(m.cfg.Dir, "cairn-backup-"+now.Format("20060102-150405"))
	dest := destBase
	for i := 1; ; i++ {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			break
		}
		if i > 10000 {
			m.fail(ctx, rec, fmt.Errorf("too many backup directories for this second"))
			return rec, nil
		}
		dest = fmt.Sprintf("%s-%d", destBase, i)
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		m.fail(ctx, rec, fmt.Errorf("create backup dir: %w", err))
		return rec, nil
	}
	rec.Destination = dest

	encrypted := m.cfg.Passphrase != ""
	var key []byte
	salt := hex.EncodeToString(newSalt())
	if encrypted {
		kb, err := hex.DecodeString(salt)
		if err != nil {
			m.fail(ctx, rec, fmt.Errorf("backup salt: %w", err))
			return rec, nil
		}
		key = deriveKey(m.cfg.Passphrase, kb)
	}
	if err := writeHeader(dest, encrypted, salt); err != nil {
		m.fail(ctx, rec, fmt.Errorf("write backup header: %w", err))
		return rec, nil
	}

	// Same-device warning: destination on the same volume as any source.
	dstDev, _ := deviceID(dest)
	same := false
	if srcDev, ok := deviceID(filepath.Dir(m.server)); ok && dstDev == srcDev {
		same = true
	}

	mf := &Manifest{
		Format:    "cairn-backup",
		Version:   1,
		CreatedAt: now,
		Encrypted: encrypted,
	}

	// 1. Server database.
	srv, err := m.snapshotServerDB(ctx, dest, encrypted, key)
	if err != nil {
		m.fail(ctx, rec, fmt.Errorf("server database: %w", err))
		return rec, nil
	}
	mf.ServerDB = &srv
	rec.Bytes = srv.Size

	// 2. Libraries: metadata + media.
	libs, err := m.libs.List(ctx)
	if err != nil {
		m.fail(ctx, rec, fmt.Errorf("list libraries: %w", err))
		return rec, nil
	}
	prev := m.loadPrevious(ctx, dest)
	for _, lib := range libs {
		lf, err := m.backupLibrary(ctx, dest, lib, encrypted, key, prev)
		if err != nil {
			m.fail(ctx, rec, fmt.Errorf("library %s: %w", lib.ID, err))
			return rec, nil
		}
		if dev, ok := deviceID(lib.Root); ok && dstDev == dev {
			same = true
		}
		mf.Libraries = append(mf.Libraries, lf)
		rec.Libraries++
		rec.Files += len(lf.Files)
		if lf.DB != nil {
			rec.Bytes += lf.DB.Size
		}
		if lf.Identity != nil {
			rec.Bytes += lf.Identity.Size
		}
		for _, e := range lf.Files {
			rec.Bytes += e.Size
		}
	}

	if err := writeManifest(dest, mf, encrypted, key); err != nil {
		m.fail(ctx, rec, fmt.Errorf("write manifest: %w", err))
		return rec, nil
	}
	if stored, err := dirBytes(dest); err == nil {
		rec.StoredBytes = stored
	}

	rec.Status = StatusCompleted
	rec.Encrypted = encrypted
	rec.SameDevice = same
	rec.FilesSkipped = int(m.skipped.Load())
	rec.FinishedAt = time.Now().UTC()
	if err := m.store.Update(ctx, rec); err != nil {
		return nil, err
	}

	// 3. Sample verification (restore-a-sample integrity checks).
	if err := m.verifySample(ctx, rec, dest, mf, key); err != nil {
		m.logger.Warn("backup verification aborted", "backup_id", rec.ID, "error", err)
	}
	if err := m.store.Update(ctx, rec); err != nil {
		return nil, err
	}

	// 4. Retention pruning.
	m.prune(ctx, rec)
	m.logger.Info("backup completed", "backup_id", rec.ID,
		"files", rec.Files, "bytes", rec.Bytes, "stored_bytes", rec.StoredBytes,
		"skipped", rec.FilesSkipped, "verify_errors", rec.VerifyErrors)
	return rec, nil
}

// snapshotServerDB copies the server cairn.db into the backup after forcing a
// WAL checkpoint so the file is self-contained.
func (m *Manager) snapshotServerDB(ctx context.Context, dest string, encrypted bool, key []byte) (Entry, error) {
	if m.server == "" {
		return Entry{}, errors.New("no server database path configured")
	}
	if _, err := m.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return Entry{}, err
	}
	rel := path.Join("server", "cairn.db")
	e, err := copyFileEntry(m.server, filepath.Join(dest, "server", "cairn.db"), rel, true, encrypted, key)
	if err != nil {
		return Entry{}, err
	}
	e.Original = m.server
	return e, nil
}

// snapshotLibraryDB checkpoints a library's metadata db and copies it.
func (m *Manager) snapshotLibraryDB(ctx context.Context, libDir, libID, cairnDir string, encrypted bool, key []byte) (Entry, error) {
	dbPath := filepath.Join(cairnDir, "library.db")
	if _, err := os.Stat(dbPath); err != nil {
		return Entry{}, err
	}
	db, err := librarydb.Open(cairnDir)
	if err != nil {
		return Entry{}, err
	}
	_, _ = db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	_ = db.Close()
	rel := path.Join("libraries", libID, "library.db")
	return copyFileEntry(dbPath, filepath.Join(libDir, "library.db"), rel, true, encrypted, key)
}

// backupLibrary copies one library's .cairn metadata and its media files.
func (m *Manager) backupLibrary(ctx context.Context, dest string, lib library.Library, encrypted bool, key []byte, prev *prevBackup) (LibraryBackup, error) {
	cairnDir := filepath.Join(lib.Root, ".cairn")
	lb := LibraryBackup{ID: lib.ID, Name: lib.Name, Root: lib.Root}
	libDir := filepath.Join(dest, "libraries", lib.ID)

	dbEntry, err := m.snapshotLibraryDB(ctx, libDir, lib.ID, cairnDir, encrypted, key)
	if err != nil {
		m.logger.Warn("library db backup skipped", "library_id", lib.ID, "error", err)
	} else {
		lb.DB = &dbEntry
	}

	idSrc := filepath.Join(cairnDir, "library.json")
	if _, err := os.Stat(idSrc); err == nil {
		idRel := path.Join("libraries", lib.ID, "library.json")
		entry, cerr := copyFileEntry(idSrc, filepath.Join(dest, filepath.FromSlash(idRel)), idRel, true, encrypted, key)
		if cerr != nil {
			return lb, cerr
		}
		lb.Identity = &entry
	}

	filesDir := filepath.Join(libDir, "files")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		return lb, err
	}
	walkErr := filepath.WalkDir(lib.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, lib.Root)
		rel = strings.TrimPrefix(rel, string(filepath.Separator))
		if rel == "" {
			return nil
		}
		if d.IsDir() {
			if rel == ".cairn" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		logical := path.Join("libraries", lib.ID, "files", filepath.ToSlash(rel))
		entry, err := m.copyFile(p, dest, logical, fi, encrypted, key, prev)
		if err != nil {
			return err
		}
		lb.Files = append(lb.Files, entry)
		return nil
	})
	return lb, walkErr
}

// copyFile writes one media file into the backup, hard-linking from the
// previous backup when the source is byte-for-byte unchanged (same size and
// mtime, previous run unencrypted).
func (m *Manager) copyFile(srcPath, destBase, logical string, fi fs.FileInfo, encrypted bool, key []byte, prev *prevBackup) (Entry, error) {
	if prev != nil {
		if pe, ok := findPrev(prev.mf, logical); ok &&
			pe.Size == fi.Size() && pe.MTime.Equal(fi.ModTime()) && !encrypted {
			prevPath := filepath.Join(prev.dir, filepath.FromSlash(logical))
			dstPath := filepath.Join(destBase, filepath.FromSlash(logical))
			if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err == nil {
				if err := os.Link(prevPath, dstPath); err == nil {
					rec := Entry{
						Logical:    logical,
						Original:   srcPath,
						Size:       pe.Size,
						MTime:      fi.ModTime(),
						SHA256:     pe.SHA256,
						Compressed: pe.Compressed,
					}
					m.logger.Debug("backup reused unchanged file", "logical", logical)
					m.skipped.Add(1)
					return rec, nil
				}
			}
		}
	}
	dstPath := filepath.Join(destBase, filepath.FromSlash(logical))
	return copyFileEntry(srcPath, dstPath, logical, shouldCompress(logical), encrypted, key)
}

// loadPrevious finds the most recent completed, reusable backup for
// incremental hard-linking. Encrypted backups are never reused (a fresh nonce
// re-encrypts everything each run).
func (m *Manager) loadPrevious(ctx context.Context, currentDest string) *prevBackup {
	if m.cfg.Passphrase != "" {
		return nil
	}
	recs, err := m.store.List(ctx, 50)
	if err != nil {
		return nil
	}
	for _, r := range recs {
		if r.Status != StatusCompleted || r.Destination == "" || r.Destination == currentDest {
			continue
		}
		if _, err := os.Stat(filepath.Join(r.Destination, manifestFilename)); err != nil {
			continue
		}
		mf, err := readManifest(r.Destination, nil)
		if err != nil {
			m.logger.Debug("previous backup manifest unreadable; full copy", "backup_id", r.ID, "error", err)
			return nil
		}
		return &prevBackup{dir: r.Destination, mf: mf}
	}
	return nil
}

type prevBackup struct {
	dir string
	mf  *Manifest
}

// verifySample runs the restore-a-sample stream verification and updates the
// record's per-sample counters.
func (m *Manager) verifySample(ctx context.Context, rec *Record, dest string, mf *Manifest, key []byte) error {
	checks := []Entry{}
	if mf.ServerDB != nil {
		checks = append(checks, *mf.ServerDB)
	}
	for _, lb := range mf.Libraries {
		if lb.DB != nil {
			checks = append(checks, *lb.DB)
		}
		if lb.Identity != nil {
			checks = append(checks, *lb.Identity)
		}
		for i := 0; i < len(lb.Files); i++ {
			if i < 3 || i%27 == 0 {
				checks = append(checks, lb.Files[i])
			}
		}
	}
	if len(checks) > 12 {
		checks = checks[:12]
	}
	rec.VerifyStatus = VerifyOK
	for _, e := range checks {
		if err := ctx.Err(); err != nil {
			return err
		}
		rec.VerifyChecked++
		if !m.verifyEntry(dest, e, key) {
			rec.VerifyErrors++
			rec.VerifyStatus = VerifyFail
			m.logger.Error("backup verification failed", "logical", e.Logical)
		}
	}
	return nil
}

// verifyEntry streams a stored payload back and compares the plaintext hash.
func (m *Manager) verifyEntry(dest string, e Entry, key []byte) bool {
	src, err := os.Open(filepath.Join(dest, filepath.FromSlash(e.Logical)))
	if err != nil {
		return false
	}
	defer func() { _ = src.Close() }()
	pr, err := openPayload(src, key)
	if err != nil {
		return false
	}
	if _, err := io.Copy(io.Discard, pr); err != nil {
		return false
	}
	if err := pr.Err(); err != nil {
		return false
	}
	return pr.Header().PlaintextSize == uint64(e.Size)
}

// Restore writes the contents of a completed backup into dest, mirroring the
// stored layout (server/cairn.db, libraries/<id>/...).
func (m *Manager) Restore(ctx context.Context, id, dest string) (*Record, error) {
	rec, err := m.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec.Status != StatusCompleted || rec.Destination == "" {
		return nil, errors.New("backup is not restorable")
	}
	if dest == "" {
		return nil, errors.New("restore destination required")
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return nil, err
	}

	var key []byte
	if rec.Encrypted {
		meta, err := readHeader(rec.Destination)
		if err != nil {
			return nil, err
		}
		if meta.Salt == "" {
			return nil, errors.New("backup is encrypted but its header has no salt")
		}
		if m.cfg.Passphrase == "" {
			return nil, errors.New("backup is encrypted; configure the same passphrase to restore")
		}
		salt, err := hex.DecodeString(meta.Salt)
		if err != nil {
			return nil, err
		}
		key = deriveKey(m.cfg.Passphrase, salt)
	}

	mf, err := readManifest(rec.Destination, key)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	count := 0
	restoreAll := func(entries []Entry) error {
		for _, e := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := restoreEntry(rec.Destination, dest, e, key); err != nil {
				return err
			}
			count++
		}
		return nil
	}
	if mf.ServerDB != nil {
		if err := restoreAll([]Entry{*mf.ServerDB}); err != nil {
			return nil, err
		}
	}
	for _, lb := range mf.Libraries {
		var entries []Entry
		if lb.DB != nil {
			entries = append(entries, *lb.DB)
		}
		if lb.Identity != nil {
			entries = append(entries, *lb.Identity)
		}
		entries = append(entries, lb.Files...)
		if err := restoreAll(entries); err != nil {
			return nil, err
		}
	}
	m.logger.Info("backup restored", "backup_id", id, "destination", dest, "entries", count)
	return &rec, nil
}

func (m *Manager) fail(ctx context.Context, rec *Record, err error) {
	rec.Status = StatusFailed
	rec.FinishedAt = time.Now().UTC()
	rec.ErrorMsg = err.Error()
	if uerr := m.store.Update(ctx, rec); uerr != nil {
		m.logger.Error("failed to persist backup failure", "error", uerr)
	}
}

// prune enforces the retention policy after a completed backup. Keep counts
// the total retained (including the current run); the oldest completed backups
// beyond that are removed and their records marked pruned.
func (m *Manager) prune(ctx context.Context, current *Record) {
	recs, err := m.store.List(ctx, 500)
	if err != nil {
		return
	}
	sort.Slice(recs, func(i, j int) bool {
		return recs[i].CreatedAt.After(recs[j].CreatedAt)
	})
	kept := 0
	for _, r := range recs {
		if r.ID == current.ID || r.Status != StatusCompleted || r.Destination == "" {
			continue
		}
		if !strings.HasPrefix(r.Destination, m.cfg.Dir+string(filepath.Separator)) {
			continue
		}
		kept++
		if kept <= m.cfg.Keep-1 {
			continue
		}
		if err := os.RemoveAll(r.Destination); err != nil {
			m.logger.Warn("prune: remove backup dir failed", "dir", r.Destination, "error", err)
			continue
		}
		r.Status = StatusPruned
		if err := m.store.Update(ctx, &r); err != nil {
			m.logger.Warn("prune: update record failed", "backup_id", r.ID, "error", err)
		}
		m.logger.Info("pruned old backup", "backup_id", r.ID, "dir", r.Destination)
	}
}

// --- helpers ---

func newSalt() []byte {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return []byte(fmt.Sprintf("%016d", time.Now().UnixNano()))
	}
	return b
}

func newID() string {
	b := make([]byte, 6)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return fmt.Sprintf("bkp-%d", time.Now().UnixNano())
	}
	return "bkp-" + hex.EncodeToString(b)
}

// copyFileEntry streams src into dst through the payload codec.
func copyFileEntry(srcPath, dstPath, logical string, compress, encrypted bool, key []byte) (Entry, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return Entry{}, err
	}
	defer func() { _ = src.Close() }()
	fi, err := src.Stat()
	if err != nil {
		return Entry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
		return Entry{}, err
	}
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return Entry{}, err
	}
	sink, err := newPayloadSink(compress, encrypted, key)
	if err != nil {
		_ = dst.Close()
		return Entry{}, err
	}
	defer sink.abort()
	if _, err := io.Copy(sink, src); err != nil {
		_ = dst.Close()
		return Entry{}, err
	}
	hh, err := sink.Materialize(dst)
	if err != nil {
		_ = dst.Close()
		return Entry{}, err
	}
	if err := dst.Close(); err != nil {
		return Entry{}, err
	}
	return Entry{
		Logical:    logical,
		Original:   srcPath,
		Size:       fi.Size(),
		MTime:      fi.ModTime(),
		SHA256:     hh.SHA256Hex,
		Compressed: hh.Compressed,
		Encrypted:  hh.Encrypted,
	}, nil
}

// restoreEntry writes one stored payload back to plaintext under destBase,
// mirroring its logical path and restoring the original mtime. The logical path
// is validated so a crafted or tampered manifest cannot write outside the
// destination directory.
func restoreEntry(backupRoot, destBase string, e Entry, key []byte) error {
	rel, err := sanitize.RelPath(e.Logical)
	if err != nil {
		return fmt.Errorf("unsafe manifest logical path %q: %w", e.Logical, err)
	}
	fsRel := filepath.FromSlash(rel)
	dst := filepath.Join(destBase, fsRel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	src, err := os.Open(filepath.Join(backupRoot, fsRel))
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	pr, err := openPayload(src, key)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, pr); err != nil {
		_ = out.Close()
		return err
	}
	if err := pr.Err(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, e.MTime, e.MTime)
}

var alreadyCompressedExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".avif": true, ".heic": true, ".heif": true, ".tiff": true, ".bmp": true,
	".mp4": true, ".m4v": true, ".mov": true, ".webm": true, ".mkv": true,
	".avi": true, ".wmv": true, ".flv": true, ".mpg": true, ".mpeg": true,
	".3gp": true, ".ts": true, ".m2ts": true,
	".mp3": true, ".aac": true, ".flac": true, ".ogg": true, ".opus": true,
	".wma": true, ".m4a": true, ".wav": true, ".aiff": true,
	".zip": true, ".gz": true, ".gzip": true, ".7z": true, ".rar": true,
	".tar": true, ".zst": true, ".tgz": true, ".xz": true, ".bz2": true,
}

// shouldCompress reports whether gzip is worth applying. Already-compressed
// formats are stored raw to avoid wasted CPU and needless expansion.
func shouldCompress(name string) bool {
	ext := strings.ToLower(name[strings.LastIndex(name, ".")+1:])
	if fi := filepath.Ext(name); fi != "" {
		ext = strings.ToLower(fi)
	}
	return !alreadyCompressedExt[ext]
}

// findPrev looks up a logical path in a previous backup's manifest.
func findPrev(mf *Manifest, logical string) (Entry, bool) {
	for _, lb := range mf.Libraries {
		for _, e := range lb.Files {
			if e.Logical == logical {
				return e, true
			}
		}
	}
	return Entry{}, false
}

// dirBytes sums the on-disk size of everything under dir (compressed and/or
// encrypted payloads included).
func dirBytes(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total, err
}

// writeHeader writes header.json (always plaintext).
func writeHeader(dest string, encrypted bool, salt string) error {
	h := headerMetadata{
		Format:    "cairn-backup",
		Version:   1,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Encrypted: encrypted,
		Salt:      salt,
	}
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dest, "header.json"), b, 0o600)
}

// readHeader reads header.json.
func readHeader(dest string) (headerMetadata, error) {
	b, err := os.ReadFile(filepath.Join(dest, "header.json"))
	if err != nil {
		return headerMetadata{}, err
	}
	var h headerMetadata
	if err := json.Unmarshal(b, &h); err != nil {
		return headerMetadata{}, err
	}
	return h, nil
}

// writeManifest writes the manifest as a payload (encrypted when configured).
func writeManifest(dest string, mf *Manifest, encrypted bool, key []byte) error {
	b, err := json.Marshal(mf)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(filepath.Join(dest, manifestFilename), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	sink, err := newPayloadSink(true, encrypted, key)
	if err != nil {
		return err
	}
	defer sink.abort()
	if _, err := sink.Write(b); err != nil {
		return err
	}
	_, err = sink.Materialize(out)
	return err
}

// readManifest reads and decodes a backup manifest.
func readManifest(dest string, key []byte) (*Manifest, error) {
	f, err := os.Open(filepath.Join(dest, manifestFilename))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	pr, err := openPayload(f, key)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(pr)
	if err != nil {
		return nil, err
	}
	if err := pr.Err(); err != nil {
		return nil, err
	}
	var mf Manifest
	if err := json.Unmarshal(b, &mf); err != nil {
		return nil, err
	}
	return &mf, nil
}

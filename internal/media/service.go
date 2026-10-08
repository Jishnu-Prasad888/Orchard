package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/sanitize"
)

const trashDirName = ".cairn/trash"

// Service executes media file operations against the filesystem and keeps the
// FileStore in sync. The libraryRoot is the absolute path to the library root.
type Service struct {
	store          *FileStore
	libraryRoot    string
	maxUploadBytes int64
}

// DefaultMaxUploadBytes is the fallback upload limit when none is configured.
const DefaultMaxUploadBytes int64 = 2 << 30 // 2 GiB

// MaxUploadBytes returns the configured upload cap in bytes.
func (svc *Service) MaxUploadBytes() int64 {
	if svc.maxUploadBytes <= 0 {
		return DefaultMaxUploadBytes
	}
	return svc.maxUploadBytes
}

// NewService returns a new Service.
func NewService(store *FileStore, libraryRoot string, opts ...Option) *Service {
	svc := &Service{store: store, libraryRoot: libraryRoot}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// Option configures a Service at construction.
type Option func(*Service)

// WithMaxUploadBytes caps uploads accepted by WriteUpload. Values <= 0 keep
// the DefaultMaxUploadBytes fallback.
func WithMaxUploadBytes(n int64) Option {
	return func(svc *Service) { svc.maxUploadBytes = n }
}

// Store returns the underlying FileStore for direct queries.
func (svc *Service) Store() *FileStore { return svc.store }

// ListFolders returns the direct sub-folders of parent as they exist on disk.
//
// The folders table is only a cache of file counts and is never populated by
// the indexer, so listing from it alone hid every folder — including ones just
// created and still empty. The filesystem is the source of truth; counts are
// counted from the index (files in the folder and everything beneath it). Hidden folders (which
// includes the .cairn metadata directory) and symlinks are skipped.
func (svc *Service) ListFolders(ctx context.Context, parent string) ([]*Folder, error) {
	if parent == "." {
		parent = ""
	}
	dir := svc.libraryRoot
	if parent != "" {
		var err error
		if dir, err = svc.absPath(parent); err != nil {
			return nil, err
		}
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		// A folder that is not on disk (yet) simply has no sub-folders.
		return []*Folder{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]*Folder, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		rel := e.Name()
		if parent != "" {
			rel = parent + "/" + e.Name()
		}
		f := &Folder{ID: rel, RelPath: rel, Name: e.Name()}
		if n, err := svc.store.CountPresentUnder(ctx, rel); err == nil {
			f.FileCount = n
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// CreateFolder makes the directory at relPath, with any missing parents, so a
// folder exists on disk as soon as it is created rather than only once a file
// is put in it. Creating a folder that already exists succeeds. Hidden
// segments are refused: they are not listed, so the folder would vanish.
func (svc *Service) CreateFolder(relPath string) (*Folder, error) {
	safe, err := SafeRelPath(relPath)
	if err != nil {
		return nil, err
	}
	for _, seg := range strings.Split(safe, "/") {
		if strings.HasPrefix(seg, ".") {
			return nil, fmt.Errorf("%w: hidden folder names are not allowed", ErrPathTraversal)
		}
	}
	abs, err := svc.absPath(safe)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create folder: %w", err)
	}
	return &Folder{ID: safe, RelPath: safe, Name: filepath.Base(abs)}, nil
}

// absPath resolves a relative path to an absolute path under the library root.
// It rejects traversal attempts.
func (svc *Service) absPath(relPath string) (string, error) {
	safe, err := SafeRelPath(relPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(svc.libraryRoot, filepath.FromSlash(safe)), nil
}

// --- download (stream) ---

// OpenFile opens a file for streaming download. The caller must close the
// returned *os.File when done. Never loads the file into memory.
func (svc *Service) OpenFile(ctx context.Context, relPath string) (*os.File, *File, error) {
	safe, err := SafeRelPath(relPath)
	if err != nil {
		return nil, nil, err
	}
	f, err := svc.store.GetByRelPath(ctx, safe)
	if err != nil {
		return nil, nil, err
	}
	abs, _ := svc.absPath(safe)
	fh, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("open file: %w", err)
	}
	return fh, f, nil
}

// --- upload ---

// WriteUpload streams an upload reader to a new file at destRelPath inside the
// library root. Uses a temp file + rename for atomicity. Updates the index.
// Returns the created File.
func (svc *Service) WriteUpload(ctx context.Context, destRelPath string, src io.Reader) (*File, error) {
	safe, err := SafeRelPath(destRelPath)
	if err != nil {
		return nil, err
	}
	abs, err := svc.absPath(safe)
	if err != nil {
		return nil, err
	}

	// Check destination does not already exist.
	if _, statErr := os.Stat(abs); statErr == nil {
		return nil, ErrAlreadyExists
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("create upload directory: %w", err)
	}

	// Write to temp file in the same directory for atomic rename.
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".cairn-upload-*")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	// An aborted upload must never strand a temp file (a crash between
	// CreateTemp and Rename would otherwise leave junk that the scanner may
	// later index). After a successful Rename the temp path no longer exists
	// and this cleanup is a no-op.
	defer func() { _ = os.Remove(tmpName) }()

	limit := svc.MaxUploadBytes()
	written, copyErr := io.Copy(tmp, io.LimitReader(src, limit+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("stream upload: %w", copyErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close temp file: %w", closeErr)
	}
	if written > limit {
		return nil, ErrUploadTooLarge
	}

	if err := os.Rename(tmpName, abs); err != nil {
		return nil, fmt.Errorf("commit upload: %w", err)
	}

	now := time.Now().UTC()
	if err := svc.store.UpsertFromPath(ctx, safe, written, now); err != nil {
		return nil, fmt.Errorf("index upload: %w", err)
	}
	return svc.store.GetByRelPath(ctx, safe)
}

// --- rename ---

// Rename renames a file within its current directory. newName must be a bare
// filename (no slashes).
func (svc *Service) Rename(ctx context.Context, relPath, newName string) (*File, error) {
	safe, err := SafeRelPath(relPath)
	if err != nil {
		return nil, err
	}
	// newName must be a bare filename, validated before any path composition so
	// it cannot smuggle a directory change (e.g. "../x" or "a/b").
	name, err := sanitize.BareName(newName)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrPathTraversal, err)
	}
	f, err := svc.store.GetByRelPath(ctx, safe)
	if err != nil {
		return nil, err
	}

	newRelPath := filepath.ToSlash(filepath.Join(filepath.Dir(filepath.FromSlash(safe)), name))
	newSafe, err := SafeRelPath(newRelPath)
	if err != nil {
		return nil, err
	}

	oldAbs, _ := svc.absPath(safe)
	newAbs, _ := svc.absPath(newSafe)

	if _, statErr := os.Stat(newAbs); statErr == nil {
		return nil, ErrAlreadyExists
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		return nil, fmt.Errorf("rename: %w", err)
	}
	if err := svc.store.UpdateRelPath(ctx, f.ID, newSafe); err != nil {
		return nil, fmt.Errorf("update index after rename: %w", err)
	}
	return svc.store.GetByRelPath(ctx, newSafe)
}

// --- move ---

// Move moves a file to a new relative path (may change directory and name).
func (svc *Service) Move(ctx context.Context, relPath, newRelPath string) (*File, error) {
	safe, err := SafeRelPath(relPath)
	if err != nil {
		return nil, err
	}
	newSafe, err := SafeRelPath(newRelPath)
	if err != nil {
		return nil, err
	}
	f, err := svc.store.GetByRelPath(ctx, safe)
	if err != nil {
		return nil, err
	}

	oldAbs, _ := svc.absPath(safe)
	newAbs, _ := svc.absPath(newSafe)

	if _, statErr := os.Stat(newAbs); statErr == nil {
		return nil, ErrAlreadyExists
	}
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return nil, fmt.Errorf("create destination directory: %w", err)
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		return nil, fmt.Errorf("move: %w", err)
	}
	if err := svc.store.UpdateRelPath(ctx, f.ID, newSafe); err != nil {
		return nil, fmt.Errorf("update index after move: %w", err)
	}
	return svc.store.GetByRelPath(ctx, newSafe)
}

// --- copy ---

// Copy copies a file to destRelPath, streaming in 32 KiB chunks.
// Never loads the full file into memory. Returns the new File record.
func (svc *Service) Copy(ctx context.Context, srcRelPath, destRelPath string) (*File, error) {
	srcSafe, err := SafeRelPath(srcRelPath)
	if err != nil {
		return nil, err
	}
	destSafe, err := SafeRelPath(destRelPath)
	if err != nil {
		return nil, err
	}

	srcAbs, _ := svc.absPath(srcSafe)
	destAbs, _ := svc.absPath(destSafe)

	if _, statErr := os.Stat(destAbs); statErr == nil {
		return nil, ErrAlreadyExists
	}
	if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
		return nil, fmt.Errorf("create copy destination directory: %w", err)
	}

	src, err := os.Open(srcAbs)
	if err != nil {
		return nil, fmt.Errorf("open source for copy: %w", err)
	}
	defer func() { _ = src.Close() }()

	tmp, err := os.CreateTemp(filepath.Dir(destAbs), ".cairn-copy-*")
	if err != nil {
		return nil, fmt.Errorf("create temp for copy: %w", err)
	}
	tmpName := tmp.Name()

	buf := make([]byte, 32*1024)
	written, copyErr := io.CopyBuffer(tmp, src, buf)
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("copy stream: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("close copy temp: %w", closeErr)
	}
	if err := os.Rename(tmpName, destAbs); err != nil {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("commit copy: %w", err)
	}

	now := time.Now().UTC()
	if err := svc.store.UpsertFromPath(ctx, destSafe, written, now); err != nil {
		return nil, fmt.Errorf("index copy: %w", err)
	}
	return svc.store.GetByRelPath(ctx, destSafe)
}

// --- soft delete (trash) ---

// Delete moves a file to the library's trash directory and marks it deleted
// in the index. The original file is not permanently removed.
func (svc *Service) Delete(ctx context.Context, relPath, actorID string) error {
	safe, err := SafeRelPath(relPath)
	if err != nil {
		return err
	}
	f, err := svc.store.GetByRelPath(ctx, safe)
	if err != nil {
		return err
	}
	if f.Status == FileStatusDeleted {
		return ErrInTrash
	}

	trashDir := filepath.Join(svc.libraryRoot, filepath.FromSlash(trashDirName))
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		return fmt.Errorf("create trash directory: %w", err)
	}

	// Use file ID as trash filename to avoid name collisions.
	trashRelPath := trashDirName + "/" + f.ID
	trashAbs := filepath.Join(svc.libraryRoot, filepath.FromSlash(trashRelPath))
	srcAbs, _ := svc.absPath(safe)

	if err := os.Rename(srcAbs, trashAbs); err != nil {
		return fmt.Errorf("move to trash: %w", err)
	}

	entry := &TrashEntry{
		FileID:       f.ID,
		OriginalPath: safe,
		TrashPath:    trashRelPath,
		DeletedAt:    time.Now().UTC(),
		DeletedBy:    actorID,
	}
	if err := svc.store.AddToTrash(ctx, entry); err != nil {
		return fmt.Errorf("record trash entry: %w", err)
	}
	return svc.store.MarkDeleted(ctx, f.ID)
}

// --- restore ---

// Restore moves a file from trash back to its original path.
func (svc *Service) Restore(ctx context.Context, fileID string) (*File, error) {
	f, err := svc.store.GetByID(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if f.Status != FileStatusDeleted {
		return nil, ErrNotInTrash
	}

	entry, err := svc.store.GetTrashEntry(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, ErrNotInTrash
	}

	trashAbs := filepath.Join(svc.libraryRoot, filepath.FromSlash(entry.TrashPath))
	restoreAbs := filepath.Join(svc.libraryRoot, filepath.FromSlash(entry.OriginalPath))

	if _, statErr := os.Stat(restoreAbs); statErr == nil {
		return nil, ErrAlreadyExists
	}
	if err := os.MkdirAll(filepath.Dir(restoreAbs), 0o755); err != nil {
		return nil, fmt.Errorf("create restore directory: %w", err)
	}
	if err := os.Rename(trashAbs, restoreAbs); err != nil {
		return nil, fmt.Errorf("restore from trash: %w", err)
	}

	if err := svc.store.MarkPresent(ctx, fileID, entry.OriginalPath); err != nil {
		return nil, fmt.Errorf("mark restored: %w", err)
	}
	if err := svc.store.RemoveTrashEntry(ctx, fileID); err != nil {
		return nil, fmt.Errorf("remove trash record: %w", err)
	}
	return svc.store.GetByID(ctx, fileID)
}

// --- permanent delete ---

// PermanentDelete removes a trashed file from the filesystem and the index
// permanently. This cannot be undone.
func (svc *Service) PermanentDelete(ctx context.Context, fileID string) error {
	f, err := svc.store.GetByID(ctx, fileID)
	if err != nil {
		return err
	}
	if f.Status != FileStatusDeleted {
		return ErrNotInTrash
	}

	entry, err := svc.store.GetTrashEntry(ctx, fileID)
	if err != nil {
		return err
	}
	if entry != nil {
		trashAbs := filepath.Join(svc.libraryRoot, filepath.FromSlash(entry.TrashPath))
		_ = os.Remove(trashAbs) // best-effort; file may already be gone
		if err := svc.store.RemoveTrashEntry(ctx, fileID); err != nil {
			return err
		}
	}
	return svc.store.DeleteRow(ctx, fileID)
}

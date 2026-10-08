package memories

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
)

// Derived edited copies (docs/storage.md, "Memory media").
//
// When a user enables "Create edited copies in the library", every memory
// image with non-default edits gets a rendered JPEG at
//
//	<library>/.cairn/memory-media/<memory-id>/<memory-image-id>-<signature>.jpg
//
// .cairn/ is Cairn's reserved, documented namespace inside every library; the
// indexer never descends into it, so copies are never picked up as new media
// and never appear as a user folder. The memory-media directory is claimed
// with a marker file: if something else already occupies that path (a
// non-directory, a symlink, or a non-empty directory without the marker),
// Cairn refuses to write there and never moves or overwrites what it found.
//
// Lifecycle: a copy is attached to exactly one memory image and is valid only
// while its edit signature matches. Changing the edits detaches it; resetting
// the edits to the original detaches it and the image references the original
// again. Detached copies are garbage collected; copies of soft-deleted
// memories stay attached (and so are kept) until the memory is gone for good.
// Version snapshots store edit parameters rather than files, so a restored
// version simply renders new copies.

// DerivedDirName is the reserved directory, inside .cairn/, for edited copies.
const DerivedDirName = "memory-media"

// derivedMarker claims DerivedDirName for Cairn.
const derivedMarker = ".cairn-memory-media"

// derivedJPEGQuality balances fidelity and size for edited copies.
const derivedJPEGQuality = 90

// ErrReservedPathConflict is returned when the reserved memory-media path is
// occupied by something Cairn did not create.
var ErrReservedPathConflict = errors.New("the reserved .cairn/memory-media path is occupied by something Cairn did not create")

// ErrDerivedNotFound is returned when an image has no current derived copy.
var ErrDerivedNotFound = errors.New("no edited copy for this memory image")

// Deriver renders and stores edited copies for one library.
type Deriver struct {
	LibraryRoot string
	CairnDir    string
	Keys        *crypto.Keys // optional at-rest encryption, as for thumbnails
}

// ensureDir returns the reserved directory, creating and claiming it when
// absent. It never modifies a pre-existing path it does not own.
func (d *Deriver) ensureDir() (string, error) {
	dir := filepath.Join(d.CairnDir, DerivedDirName)
	marker := filepath.Join(dir, derivedMarker)
	fi, err := os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create memory-media dir: %w", err)
		}
		return dir, writeMarker(marker)
	case err != nil:
		return "", err
	case fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir():
		return "", ErrReservedPathConflict
	}
	if _, err := os.Lstat(marker); err == nil {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(entries) > 0 {
		return "", ErrReservedPathConflict
	}
	return dir, writeMarker(marker)
}

func writeMarker(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim memory-media dir: %w", err)
	}
	_, werr := f.WriteString("Managed by Cairn: edited copies of photos used in Memories.\n" +
		"Originals are never modified. Safe to delete; Cairn regenerates what it needs.\n")
	cerr := f.Close()
	return errors.Join(werr, cerr)
}

// SyncResult reports what a derived-media pass did.
type SyncResult struct {
	Created  int
	Detached int
	Removed  int
	Failed   int
}

// SyncDerived brings memoryID's edited copies in line with its images:
// detaching stale or reset copies, rendering missing ones when enabled is
// true, then garbage collecting copies no image references. Originals are
// only ever opened for reading.
func (s *MemoryStore) SyncDerived(ctx context.Context, memoryID string, d *Deriver, enabled bool) (SyncResult, error) {
	var res SyncResult
	blocks, err := loadBlocks(ctx, s.db, memoryID)
	if err != nil {
		return res, err
	}
	var ids []string
	for _, b := range blocks {
		for _, img := range b.Images {
			ids = append(ids, img.SourceFileID)
		}
	}
	sources, err := lookupSources(ctx, s.db, ids)
	if err != nil {
		return res, err
	}

	for _, b := range blocks {
		for _, img := range b.Images {
			sig := img.Edits.Signature(img.SourceFileID)
			if img.DerivedID != "" {
				var have string
				err := s.db.QueryRowContext(ctx,
					`SELECT edit_signature FROM memory_derived_media WHERE id = ?`, img.DerivedID).Scan(&have)
				if err == nil && have == sig && !img.Edits.IsDefault() {
					continue // current
				}
				if err != nil && err != sql.ErrNoRows {
					return res, err
				}
				if _, err := s.db.ExecContext(ctx,
					`UPDATE memory_images SET derived_id = NULL WHERE id = ?`, img.ID); err != nil {
					return res, err
				}
				res.Detached++
			}
			if !enabled || img.Edits.IsDefault() {
				continue
			}
			src := sources[img.SourceFileID]
			if !src.Available() || src.MediaType != "photo" {
				continue
			}
			if err := s.renderDerived(ctx, d, memoryID, img, src, sig); err != nil {
				if errors.Is(err, ErrReservedPathConflict) {
					return res, err
				}
				res.Failed++
				continue
			}
			res.Created++
		}
	}
	removed, err := s.CollectDerived(ctx, d)
	res.Removed = removed
	return res, err
}

func (s *MemoryStore) renderDerived(ctx context.Context, d *Deriver, memoryID string, img *Image, src *Source, sig string) error {
	if !idPattern.MatchString(memoryID) || !idPattern.MatchString(img.ID) {
		return fmt.Errorf("unsafe id for a derived path")
	}
	root, err := d.ensureDir()
	if err != nil {
		return err
	}
	rendered, err := RenderEdited(filepath.Join(d.LibraryRoot, filepath.FromSlash(src.RelPath)), img.Edits)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, rendered, &jpeg.Options{Quality: derivedJPEGQuality}); err != nil {
		return fmt.Errorf("encode edited copy: %w", err)
	}
	data := d.Keys.Seal(buf.Bytes())

	dir := filepath.Join(root, memoryID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create memory dir: %w", err)
	}
	name := img.ID + "-" + sig + ".jpg"
	dest := filepath.Join(dir, name)
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write edited copy: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit edited copy: %w", err)
	}

	now := rfc3339(s.now())
	rel := filepath.ToSlash(filepath.Join(DerivedDirName, memoryID, name))
	derivedID := newID()
	b := rendered.Bounds()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_derived_media (id, memory_id, memory_image_id, source_file_id, rel_path,
			edit_signature, width, height, size_bytes, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(rel_path) DO UPDATE SET id = excluded.id, width = excluded.width,
			height = excluded.height, size_bytes = excluded.size_bytes, updated_at = excluded.updated_at`,
		derivedID, memoryID, img.ID, img.SourceFileID, rel, sig, b.Dx(), b.Dy(), len(data), now, now); err != nil {
		return fmt.Errorf("record edited copy: %w", err)
	}
	// Attach only if the image was not edited again while rendering; an
	// unattached copy is collected by the next pass.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE memory_images SET derived_id = ? WHERE id = ? AND updated_at = ?`,
		derivedID, img.ID, rfc3339(img.UpdatedAt)); err != nil {
		return fmt.Errorf("attach edited copy: %w", err)
	}
	return nil
}

// CollectDerived deletes edited copies (rows and files) that no memory image
// references. Referenced copies — including those of soft-deleted memories —
// are never touched.
func (s *MemoryStore) CollectDerived(ctx context.Context, d *Deriver) (int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT d.id, d.rel_path FROM memory_derived_media d
		 WHERE NOT EXISTS (SELECT 1 FROM memory_images i WHERE i.derived_id = d.id)`)
	if err != nil {
		return 0, fmt.Errorf("find unreferenced edited copies: %w", err)
	}
	type orphan struct{ id, rel string }
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.id, &o.rel); err != nil {
			_ = rows.Close()
			return 0, err
		}
		orphans = append(orphans, o)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()

	removed := 0
	for _, o := range orphans {
		path, ok := d.derivedPath(o.rel)
		if ok {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				continue
			}
			_ = os.Remove(filepath.Dir(path)) // drop the memory's dir once empty
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM memory_derived_media WHERE id = ?`, o.id); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// derivedPath maps a stored rel_path to an absolute path, refusing anything
// that would escape the reserved directory.
func (d *Deriver) derivedPath(rel string) (string, bool) {
	root := filepath.Join(d.CairnDir, DerivedDirName)
	abs := filepath.Join(d.CairnDir, filepath.FromSlash(rel))
	inside, err := filepath.Rel(root, abs)
	if err != nil || inside == "." || inside == ".." || filepath.IsAbs(inside) ||
		len(inside) >= 2 && inside[:2] == ".." {
		return "", false
	}
	return abs, true
}

// DerivedFile is a decoded edited copy ready to serve.
type DerivedFile struct {
	Data    []byte
	ModTime time.Time
	Width   int
	Height  int
}

// OpenDerived returns the current edited copy for a memory image.
func (s *MemoryStore) OpenDerived(ctx context.Context, d *Deriver, memoryID, imageID string) (*DerivedFile, error) {
	var rel string
	var w, h int
	err := s.db.QueryRowContext(ctx,
		`SELECT d.rel_path, d.width, d.height FROM memory_images i
		 JOIN memory_derived_media d ON d.id = i.derived_id
		 WHERE i.id = ? AND i.memory_id = ?`, imageID, memoryID).Scan(&rel, &w, &h)
	if err == sql.ErrNoRows {
		return nil, ErrDerivedNotFound
	}
	if err != nil {
		return nil, err
	}
	path, ok := d.derivedPath(rel)
	if !ok {
		return nil, ErrDerivedNotFound
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, ErrDerivedNotFound
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	plain, err := d.Keys.Open(raw)
	if err != nil {
		return nil, fmt.Errorf("decrypt edited copy: %w", err)
	}
	return &DerivedFile{Data: plain, ModTime: info.ModTime(), Width: w, Height: h}, nil
}

// DerivedInfo describes the attached copy of each image (by image id).
type DerivedInfo struct {
	ID     string
	Width  int
	Height int
}

// ListDerived returns the attached, current copies for a memory's images.
func (s *MemoryStore) ListDerived(ctx context.Context, memoryID string) (map[string]DerivedInfo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT i.id, d.id, d.width, d.height FROM memory_images i
		 JOIN memory_derived_media d ON d.id = i.derived_id WHERE i.memory_id = ?`, memoryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]DerivedInfo{}
	for rows.Next() {
		var imageID string
		var info DerivedInfo
		if err := rows.Scan(&imageID, &info.ID, &info.Width, &info.Height); err != nil {
			return nil, err
		}
		out[imageID] = info
	}
	return out, rows.Err()
}

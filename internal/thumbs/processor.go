package thumbs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/jobs"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
)

// Dir is where thumbnails live, relative to a library's .orchard folder.
const Dir = "thumbs"

// Processor renders photo thumbnails from process_media jobs. It implements
// indexer.MediaProcessor and indexer.BackfillingProcessor.
type Processor struct {
	logger *slog.Logger
}

// NewProcessor returns a Processor.
func NewProcessor(logger *slog.Logger) *Processor { return &Processor{logger: logger} }

// version identifies the file contents a thumbnail was made from. It is the
// indexed modification time and size, so an edited photo gets a new one.
const versionExpr = `f.mod_time || ':' || f.size_bytes`

// Handle renders the thumbnail for one photo. A photo it cannot shrink (an
// unknown format, too large) is recorded with no path, so it is not retried
// until the file changes; its tile keeps showing the original.
func (p *Processor) Handle(ctx context.Context, db *sql.DB, job *jobs.Job) error {
	fileID, _ := job.Payload["file_id"].(string)
	relPath, _ := job.Payload["rel_path"].(string)
	root, _ := job.Payload["root"].(string)
	orchardDir, _ := job.Payload["orchard_dir"].(string)
	if fileID == "" || relPath == "" || root == "" || orchardDir == "" ||
		media.DetectMediaType(relPath) != media.MediaTypePhoto {
		return nil
	}

	var version string
	err := db.QueryRowContext(ctx,
		`SELECT `+versionExpr+` FROM indexed_files f WHERE f.id = ? AND f.status = 'present'`,
		fileID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // gone since the job was queued
	}
	if err != nil {
		return fmt.Errorf("look up photo: %w", err)
	}

	data, err := Render(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		p.logger.Info("no thumbnail for photo", "file", relPath, "reason", err)
		return record(ctx, db, fileID, "", version)
	}

	rel := Dir + "/" + fileID + ".jpg"
	if err := writeAtomic(filepath.Join(orchardDir, filepath.FromSlash(rel)), data); err != nil {
		return err
	}
	return record(ctx, db, fileID, rel, version)
}

// Pending lists photos without an up-to-date thumbnail, so existing
// libraries get thumbnails after an upgrade and edited photos get new ones.
func (p *Processor) Pending(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT f.id, f.rel_path FROM indexed_files f
		LEFT JOIN thumbnails t ON t.file_id = f.id
		WHERE f.status = 'present' AND f.media_type = 'photo'
		  AND (t.file_id IS NULL OR t.version != `+versionExpr+`)`)
	if err != nil {
		return nil, fmt.Errorf("list photos without thumbnails: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var id, rel string
		if err := rows.Scan(&id, &rel); err != nil {
			return nil, err
		}
		out[id] = rel
	}
	return out, rows.Err()
}

// Versions returns, for those of fileIDs that have a thumbnail, the version
// to put in its URL — one query for a whole page of tiles.
func Versions(ctx context.Context, db *sql.DB, fileIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(fileIDs))
	if len(fileIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(fileIDs))
	for i, id := range fileIDs {
		args[i] = id
	}
	rows, err := db.QueryContext(ctx,
		`SELECT file_id, version FROM thumbnails WHERE path != '' AND file_id IN (`+
			strings.TrimSuffix(strings.Repeat("?,", len(fileIDs)), ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("list thumbnails: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, version string
		if err := rows.Scan(&id, &version); err != nil {
			return nil, err
		}
		out[id] = version
	}
	return out, rows.Err()
}

// Path returns the thumbnail's path relative to .orchard, or "" if none.
func Path(ctx context.Context, db *sql.DB, fileID string) (string, error) {
	var rel string
	err := db.QueryRowContext(ctx, `SELECT path FROM thumbnails WHERE file_id = ?`, fileID).Scan(&rel)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return rel, err
}

func record(ctx context.Context, db *sql.DB, fileID, rel, version string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO thumbnails (file_id, path, version, generated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(file_id) DO UPDATE SET
			path = excluded.path, version = excluded.version, generated_at = excluded.generated_at`,
		fileID, rel, version, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("record thumbnail: %w", err)
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create thumbnail folder: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".thumb-*")
	if err != nil {
		return fmt.Errorf("create thumbnail: %w", err)
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write thumbnail: %w", werr)
	}
	return nil
}

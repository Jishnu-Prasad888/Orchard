package bookmeta

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/jobs"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
	"github.com/Jishnu-Prasad888/Orchard/internal/search"
)

// Processor implements indexer.MediaProcessor for document files: it
// extracts book-style metadata, a cover image, and plain text, and stores
// them as a book_metadata row plus files under the library's .orchard/
// folder. The indexer enqueues a process_media job for every new or
// modified file regardless of type, so Handle's first job is to ignore
// anything that isn't a document (photos, videos, audio).
type Processor struct {
	logger *slog.Logger
}

// NewProcessor returns a Processor. Extraction failures for a single file
// (a malformed EPUB/PDF) are logged and degrade to a filename-derived
// title rather than failing the process_media job.
func NewProcessor(logger *slog.Logger) *Processor {
	return &Processor{logger: logger}
}

// Handle implements indexer.MediaProcessor.
func (p *Processor) Handle(ctx context.Context, db *sql.DB, job *jobs.Job) error {
	fileID, _ := job.Payload["file_id"].(string)
	relPath, _ := job.Payload["rel_path"].(string)
	root, _ := job.Payload["root"].(string)
	orchardDir, _ := job.Payload["orchard_dir"].(string)
	if fileID == "" || relPath == "" || root == "" || orchardDir == "" {
		return nil
	}
	if media.DetectMediaType(relPath) != media.MediaTypeDocument {
		return nil
	}

	m := Metadata{FileID: fileID, Title: titleFromFilename(relPath), ExtractedAt: time.Now().UTC()}

	var (
		ex  Extracted
		err error
	)
	switch strings.ToLower(filepath.Ext(relPath)) {
	case ".epub":
		ex, err = ExtractEPUB(filepath.Join(root, relPath))
	case ".pdf":
		ex, err = ExtractPDF(filepath.Join(root, relPath))
	default:
		// Markdown, plain text, and anything else without a dedicated
		// extractor yet: keep the filename-derived title only.
	}
	if err != nil {
		p.logger.Warn("extract book metadata", "file", relPath, "error", err)
	} else {
		applyExtracted(&m, ex, fileID, orchardDir, p.logger)
	}

	if err := NewStore(db).Upsert(ctx, m); err != nil {
		return err
	}
	// book_text has no DB row of its own to trigger fts_content off of (the
	// extracted text lives in a file under .orchard/text/), so it is synced
	// explicitly here rather than by a trigger.
	return search.SyncContent(ctx, db, search.ContentBookText, fileID, fileID, "", ex.Text)
}

// applyExtracted merges what was extracted into m, writing the cover image
// and extracted text out to the library's .orchard/ folder and recording
// their paths (relative to .orchard/) on m.
func applyExtracted(m *Metadata, ex Extracted, fileID, orchardDir string, logger *slog.Logger) {
	if ex.Title != "" {
		m.Title = ex.Title
	}
	m.Author = ex.Author
	m.Publisher = ex.Publisher
	m.Year = ex.Year
	m.ISBN = ex.ISBN
	m.Description = ex.Description

	if len(ex.Cover) > 0 && ex.CoverExt != "" {
		rel := filepath.Join("covers", fileID+ex.CoverExt)
		if err := writeUnder(orchardDir, rel, ex.Cover); err != nil {
			logger.Warn("write extracted cover", "file_id", fileID, "error", err)
		} else {
			m.CoverPath = filepath.ToSlash(rel)
		}
	}
	if strings.TrimSpace(ex.Text) != "" {
		rel := filepath.Join("text", fileID+".txt")
		if err := writeUnder(orchardDir, rel, []byte(ex.Text)); err != nil {
			logger.Warn("write extracted text", "file_id", fileID, "error", err)
		} else {
			m.TextPath = filepath.ToSlash(rel)
		}
	}
}

func writeUnder(orchardDir, relPath string, data []byte) error {
	full := filepath.Join(orchardDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func titleFromFilename(relPath string) string {
	base := filepath.Base(relPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

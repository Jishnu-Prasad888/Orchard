package bookmeta

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jishnu-Prasad888/Orchard/internal/jobs"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestLibrary creates a real library root (not just the .orchard db) so
// the Processor can read source files via root+rel_path, and returns the
// opened library db alongside it.
func newTestLibrary(t *testing.T) (root string, db *librarydb.DB) {
	t.Helper()
	root = t.TempDir()
	orchardDir := filepath.Join(root, ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.OpenDB(orchardDir)
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return root, db
}

func seedIndexedFile(t *testing.T, db *librarydb.DB, fileID, relPath string) {
	t.Helper()
	if _, err := db.DB().Exec(`INSERT INTO indexed_files
		(id, rel_path, size_bytes, mod_time, status, first_seen_at, last_seen_at, indexed_at)
		VALUES (?, ?, 1, '', 'present', '', '', '')`, fileID, relPath); err != nil {
		t.Fatalf("seed indexed_files: %v", err)
	}
}

func TestProcessor_Handle_EPUB_ExtractsMetadataCoverAndText(t *testing.T) {
	root, db := newTestLibrary(t)
	orchardDir := filepath.Join(root, ".orchard")

	// Reuse the real EPUB builder from epub_test.go, but place it inside
	// the library root at a specific relative path.
	srcPath := buildTestEPUB(t)
	relPath := "books/test.epub"
	destPath := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	seedIndexedFile(t, db, "f1", relPath)

	p := NewProcessor(testLogger())
	job := &jobs.Job{
		ID:   "job1",
		Kind: jobs.KindProcessMedia,
		Payload: map[string]any{
			"library_id":  "lib1",
			"file_id":     "f1",
			"rel_path":    relPath,
			"root":        root,
			"orchard_dir": orchardDir,
		},
	}
	if err := p.Handle(context.Background(), db.DB(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	store := NewStore(db.DB())
	m, ok, err := store.Get(context.Background(), "f1")
	if err != nil || !ok {
		t.Fatalf("Get after Handle: ok=%v err=%v", ok, err)
	}
	if m.Title != "The Great Test" {
		t.Errorf("Title = %q, want %q", m.Title, "The Great Test")
	}
	if m.Author != "Jane Author" {
		t.Errorf("Author = %q, want %q", m.Author, "Jane Author")
	}
	if m.CoverPath == "" {
		t.Error("expected a non-empty CoverPath")
	}
	if m.TextPath == "" {
		t.Error("expected a non-empty TextPath")
	}

	coverFull := filepath.Join(orchardDir, m.CoverPath)
	if _, err := os.Stat(coverFull); err != nil {
		t.Errorf("cover file not written at %s: %v", coverFull, err)
	}
	textFull := filepath.Join(orchardDir, m.TextPath)
	textBytes, err := os.ReadFile(textFull)
	if err != nil {
		t.Fatalf("text file not written at %s: %v", textFull, err)
	}
	if len(textBytes) == 0 {
		t.Error("extracted text file is empty")
	}
}

// TestProcessor_Handle_EPUB_IndexesExtractedTextAndMetadata proves Handle
// indexes both the book's extracted text and its metadata into fts_content
// — book_text has no DB row to trigger off, and book_metadata is written
// via UPSERT, so both are synced explicitly rather than by a trigger.
func TestProcessor_Handle_EPUB_IndexesExtractedTextAndMetadata(t *testing.T) {
	root, db := newTestLibrary(t)
	orchardDir := filepath.Join(root, ".orchard")

	srcPath := buildTestEPUB(t)
	relPath := "books/test.epub"
	destPath := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	seedIndexedFile(t, db, "f1", relPath)

	p := NewProcessor(testLogger())
	job := &jobs.Job{
		Kind: jobs.KindProcessMedia,
		Payload: map[string]any{
			"file_id":     "f1",
			"rel_path":    relPath,
			"root":        root,
			"orchard_dir": orchardDir,
		},
	}
	if err := p.Handle(context.Background(), db.DB(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var sourceType string
	if err := db.DB().QueryRow(
		`SELECT source_type FROM fts_content WHERE fts_content MATCH 'stormy'`,
	).Scan(&sourceType); err != nil {
		t.Fatalf("expected the extracted chapter text to be indexed: %v", err)
	}
	if sourceType != "book_text" {
		t.Errorf("source_type = %q, want book_text", sourceType)
	}
	if err := db.DB().QueryRow(
		`SELECT source_type FROM fts_content WHERE fts_content MATCH 'Author'`,
	).Scan(&sourceType); err != nil {
		t.Fatalf("expected the extracted author to be indexed: %v", err)
	}
	if sourceType != "metadata" {
		t.Errorf("source_type = %q, want metadata", sourceType)
	}
}

func TestProcessor_Handle_NonDocumentIsNoOp(t *testing.T) {
	root, db := newTestLibrary(t)
	orchardDir := filepath.Join(root, ".orchard")
	relPath := "photos/beach.jpg"
	seedIndexedFile(t, db, "f1", relPath)

	p := NewProcessor(testLogger())
	job := &jobs.Job{
		Kind: jobs.KindProcessMedia,
		Payload: map[string]any{
			"file_id":     "f1",
			"rel_path":    relPath,
			"root":        root,
			"orchard_dir": orchardDir,
		},
	}
	if err := p.Handle(context.Background(), db.DB(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	store := NewStore(db.DB())
	_, ok, err := store.Get(context.Background(), "f1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("expected no book_metadata row for a non-document file")
	}
}

func TestProcessor_Handle_MarkdownGetsFilenameTitleOnly(t *testing.T) {
	root, db := newTestLibrary(t)
	orchardDir := filepath.Join(root, ".orchard")
	relPath := "notes/my-essay.md"
	destPath := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destPath, []byte("# Hello\n\nSome markdown."), 0o644); err != nil {
		t.Fatal(err)
	}
	seedIndexedFile(t, db, "f1", relPath)

	p := NewProcessor(testLogger())
	job := &jobs.Job{
		Kind: jobs.KindProcessMedia,
		Payload: map[string]any{
			"file_id":     "f1",
			"rel_path":    relPath,
			"root":        root,
			"orchard_dir": orchardDir,
		},
	}
	if err := p.Handle(context.Background(), db.DB(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	store := NewStore(db.DB())
	m, ok, err := store.Get(context.Background(), "f1")
	if err != nil || !ok {
		t.Fatalf("Get after Handle: ok=%v err=%v", ok, err)
	}
	if m.Title != "my-essay" {
		t.Errorf("Title = %q, want %q", m.Title, "my-essay")
	}
	if m.CoverPath != "" || m.TextPath != "" {
		t.Errorf("expected no cover/text for markdown, got cover=%q text=%q", m.CoverPath, m.TextPath)
	}
}

func TestProcessor_Handle_MissingPayloadFieldsIsNoOp(t *testing.T) {
	root, db := newTestLibrary(t)
	p := NewProcessor(testLogger())
	job := &jobs.Job{Kind: jobs.KindProcessMedia, Payload: map[string]any{"rel_path": "book.epub"}}
	if err := p.Handle(context.Background(), db.DB(), job); err != nil {
		t.Fatalf("Handle with incomplete payload should no-op, got error: %v", err)
	}
	_ = root
}

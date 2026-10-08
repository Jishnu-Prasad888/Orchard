package search_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/albums"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/media"
	"github.com/Jishnu-Prasad888/Cairn/internal/search"
	"github.com/Jishnu-Prasad888/Cairn/internal/tags"
)

// --- test helpers ---

// newTestStoreWithMediaStore returns a SearchStore, FileStore, and the raw
// database so tests can seed indexed_files, tags, and albums.
func newTestStoreWithMediaStore(t *testing.T) (*search.SearchStore, *media.FileStore, *sql.DB) {
	t.Helper()
	root := t.TempDir()
	cairnDir := filepath.Join(root, ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.Open(cairnDir)
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ss := search.NewSearchStore(db, "lib1")
	fs := media.NewFileStore(db, "lib1")
	return ss, fs, db
}

func seedFile(t *testing.T, fs *media.FileStore, relPath string) {
	t.Helper()
	err := fs.UpsertFromPath(context.Background(), relPath, 100, time.Now())
	if err != nil {
		t.Fatalf("seed %s: %v", relPath, err)
	}
}

// --- tests ---

func TestSearchEmpty(t *testing.T) {
	ss, fs, _ := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	seedFile(t, fs, "photo.jpg")
	seedFile(t, fs, "video.mp4")
	seedFile(t, fs, "sub/doc.pdf")

	page, err := ss.Search(ctx, search.SearchQuery{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Results) != 3 {
		t.Errorf("empty query returned %d results, want 3", len(page.Results))
	}
}

func TestSearchTextMatch(t *testing.T) {
	ss, fs, _ := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	seedFile(t, fs, "holiday/beach.jpg")
	seedFile(t, fs, "holiday/mountains.jpg")
	seedFile(t, fs, "work/report.pdf")

	page, err := ss.Search(ctx, search.SearchQuery{Text: "beach"})
	if err != nil {
		t.Fatalf("Search beach: %v", err)
	}
	if len(page.Results) != 1 {
		t.Errorf("text search returned %d results, want 1", len(page.Results))
	}
	if len(page.Results) > 0 && page.Results[0].File.Name != "beach.jpg" {
		t.Errorf("wrong file: got %q, want beach.jpg", page.Results[0].File.Name)
	}
}

func TestSearchTextMatchPartial(t *testing.T) {
	ss, fs, _ := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	seedFile(t, fs, "vacations/beachday.jpg")
	seedFile(t, fs, "vacations/hiking.jpg")

	// Prefix search: "beach" should match "beachday".
	page, err := ss.Search(ctx, search.SearchQuery{Text: "beach"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Results) != 1 {
		t.Errorf("prefix search returned %d results, want 1", len(page.Results))
	}
}

func TestSearchFolderFilter(t *testing.T) {
	ss, fs, _ := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	seedFile(t, fs, "2022/img1.jpg")
	seedFile(t, fs, "2022/img2.jpg")
	seedFile(t, fs, "2023/img3.jpg")

	page, err := ss.Search(ctx, search.SearchQuery{FolderPath: "2022"})
	if err != nil {
		t.Fatalf("Search with folder: %v", err)
	}
	if len(page.Results) != 2 {
		t.Errorf("folder filter returned %d results, want 2", len(page.Results))
	}
}

func TestSearchPagination(t *testing.T) {
	ss, fs, _ := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	for _, name := range []string{"a.jpg", "b.jpg", "c.jpg", "d.jpg", "e.jpg"} {
		seedFile(t, fs, name)
	}

	page1, err := ss.Search(ctx, search.SearchQuery{Limit: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Results) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1.Results))
	}
	if page1.NextCursor == "" {
		t.Error("expected NextCursor for page 1")
	}

	page2, err := ss.Search(ctx, search.SearchQuery{Limit: 2, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Results) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(page2.Results))
	}

	// No overlap.
	seen := map[string]bool{}
	for _, r := range page1.Results {
		seen[r.File.ID] = true
	}
	for _, r := range page2.Results {
		if seen[r.File.ID] {
			t.Errorf("overlap: %s appeared in both pages", r.File.ID)
		}
	}
}

func TestSearchDateRange(t *testing.T) {
	ss, fs, _ := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	// Seed 3 files with distinct mtime.
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-1 * time.Hour)

	_ = fs.UpsertFromPath(ctx, "old_photo.jpg", 100, old)
	_ = fs.UpsertFromPath(ctx, "recent_photo.jpg", 100, recent)
	_ = fs.UpsertFromPath(ctx, "now_photo.jpg", 100, now)

	// Only files from the last 2 hours.
	page, err := ss.Search(ctx, search.SearchQuery{
		DateFrom: now.Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Search with date range: %v", err)
	}
	if len(page.Results) != 2 {
		t.Errorf("date range returned %d results, want 2", len(page.Results))
	}
}

func TestSearchNoResults(t *testing.T) {
	ss, fs, _ := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	seedFile(t, fs, "beach.jpg")

	page, err := ss.Search(ctx, search.SearchQuery{Text: "zzznomatch"})
	if err != nil {
		t.Fatalf("Search no results: %v", err)
	}
	if len(page.Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(page.Results))
	}
	if page.NextCursor != "" {
		t.Error("expected empty NextCursor when no results")
	}
}

func TestSearchDefaultsLimit(t *testing.T) {
	q := search.SearchQuery{}
	q.Defaults()
	if q.Limit != 50 {
		t.Errorf("default limit = %d, want 50", q.Limit)
	}
}

func TestSearchClampsLimit(t *testing.T) {
	q := search.SearchQuery{Limit: 9999}
	q.Defaults()
	if q.Limit != 200 {
		t.Errorf("clamped limit = %d, want 200", q.Limit)
	}
}

func TestSearchResultHasFileMetadata(t *testing.T) {
	ss, fs, _ := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	seedFile(t, fs, "photos/cat.jpg")

	page, err := ss.Search(ctx, search.SearchQuery{Text: "cat"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Results) == 0 {
		t.Fatal("expected at least one result")
	}
	f := page.Results[0].File
	if f.ID == "" {
		t.Error("result File.ID is empty")
	}
	if f.Name == "" {
		t.Error("result File.Name is empty")
	}
	if f.LibraryID != "lib1" {
		t.Errorf("LibraryID = %q, want lib1", f.LibraryID)
	}
}

func TestSearchTagFilter(t *testing.T) {
	ss, fs, db := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	seedFile(t, fs, "trip/beach.jpg")
	seedFile(t, fs, "trip/mountains.jpg")

	f1, err := fs.GetByRelPath(ctx, "trip/beach.jpg")
	if err != nil {
		t.Fatalf("GetByRelPath: %v", err)
	}
	tagStore := tags.NewTagStore(db)
	tag, err := tagStore.Create(ctx, "Holiday", "")
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	if err := tagStore.Attach(ctx, f1.ID, tag.ID); err != nil {
		t.Fatalf("attach tag: %v", err)
	}

	page, err := ss.Search(ctx, search.SearchQuery{Tag: "holiday"})
	if err != nil {
		t.Fatalf("Search by tag: %v", err)
	}
	if len(page.Results) != 1 {
		t.Fatalf("tag filter returned %d results, want 1", len(page.Results))
	}
	if page.Results[0].File.Name != "beach.jpg" {
		t.Errorf("wrong file for tag filter: got %q", page.Results[0].File.Name)
	}
}

func TestSearchAlbumFilter(t *testing.T) {
	ss, fs, db := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	seedFile(t, fs, "a.jpg")
	seedFile(t, fs, "b.jpg")
	seedFile(t, fs, "c.jpg")

	albumStore := albums.NewAlbumStore(db, "lib1")
	album, err := albumStore.Create(ctx, "Summer", "")
	if err != nil {
		t.Fatalf("create album: %v", err)
	}
	for _, path := range []string{"a.jpg", "c.jpg"} {
		f, err := fs.GetByRelPath(ctx, path)
		if err != nil {
			t.Fatalf("GetByRelPath %s: %v", path, err)
		}
		if err := albumStore.AddFile(ctx, album.ID, f.ID); err != nil {
			t.Fatalf("add to album: %v", err)
		}
	}

	page, err := ss.Search(ctx, search.SearchQuery{AlbumID: album.ID})
	if err != nil {
		t.Fatalf("Search by album: %v", err)
	}
	if len(page.Results) != 2 {
		t.Fatalf("album filter returned %d results, want 2", len(page.Results))
	}
}

func TestSearchPersonFilter(t *testing.T) {
	ss, fs, db := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	seedFile(t, fs, "a.jpg")
	seedFile(t, fs, "b.jpg")
	seedFile(t, fs, "c.jpg")

	// Person "Mom" assigned the face in a.jpg and c.jpg.
	personID := "person-mom-1"
	faceIDs := []string{"face-a", "face-b"}
	now := "2026-01-01T00:00:00.000Z"
	for i, rel := range []string{"a.jpg", "c.jpg"} {
		f, err := fs.GetByRelPath(ctx, rel)
		if err != nil {
			t.Fatalf("GetByRelPath %s: %v", rel, err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO faces (id, file_id, provider, version, x, y, width, height,
			                   confidence, descriptor, created_at, updated_at)
			VALUES (?, ?, 'test', 1, 0, 0, 20, 20, 0.9, X'00000000', ?, ?)`,
			faceIDs[i], f.ID, now, now); err != nil {
			t.Fatalf("insert face: %v", err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO people (id, name, created_at, updated_at) VALUES (?, 'Mom', ?, ?)`,
		personID, now, now); err != nil {
		t.Fatalf("insert person: %v", err)
	}
	for _, faceID := range faceIDs {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO person_faces (person_id, face_id, assigned_by, created_at)
			VALUES (?, ?, 'auto', ?)`, personID, faceID, now); err != nil {
			t.Fatalf("assign face: %v", err)
		}
	}

	page, err := ss.Search(ctx, search.SearchQuery{PersonID: personID})
	if err != nil {
		t.Fatalf("Search by person: %v", err)
	}
	if len(page.Results) != 2 {
		t.Fatalf("person filter returned %d results, want 2", len(page.Results))
	}

	page, err = ss.Search(ctx, search.SearchQuery{PersonID: "no-such-person"})
	if err != nil {
		t.Fatalf("Search by missing person: %v", err)
	}
	if len(page.Results) != 0 {
		t.Fatalf("person filter for missing person returned %d results, want 0", len(page.Results))
	}
}

func TestSearchSizeFilter(t *testing.T) {
	ss, fs, _ := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	_ = fs.UpsertFromPath(ctx, "small.jpg", 1000, time.Now())
	_ = fs.UpsertFromPath(ctx, "big.jpg", 9000, time.Now())

	page, err := ss.Search(ctx, search.SearchQuery{MinSize: 5000})
	if err != nil {
		t.Fatalf("Search min size: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].File.Name != "big.jpg" {
		t.Errorf("min-size filter returned wrong results: %+v", page.Results)
	}

	page, err = ss.Search(ctx, search.SearchQuery{MaxSize: 2000})
	if err != nil {
		t.Fatalf("Search max size: %v", err)
	}
	if len(page.Results) != 1 || page.Results[0].File.Name != "small.jpg" {
		t.Errorf("max-size filter returned wrong results: %+v", page.Results)
	}
}

func TestSearchAdvancedSyntax(t *testing.T) {
	ss, fs, _ := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	seedFile(t, fs, "beach.jpg")
	seedFile(t, fs, "beachday.jpg")
	seedFile(t, fs, "mountains.jpg")

	// OR: either term matches.
	page, err := ss.Search(ctx, search.SearchQuery{Text: "beach OR mountains"})
	if err != nil {
		t.Fatalf("Search OR: %v", err)
	}
	if len(page.Results) != 3 {
		t.Errorf("OR search returned %d results, want 3", len(page.Results))
	}

	// NOT / minus: exclude a term.
	page, err = ss.Search(ctx, search.SearchQuery{Text: "beach -beachday"})
	if err != nil {
		t.Fatalf("Search NOT: %v", err)
	}
	if len(page.Results) != 1 {
		t.Errorf("NOT search returned %d results, want 1", len(page.Results))
	}
}

func TestSearchPhrase(t *testing.T) {
	ss, fs, _ := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	seedFile(t, fs, "moon landing.jpg")
	seedFile(t, fs, "moon_crater.jpg")

	page, err := ss.Search(ctx, search.SearchQuery{Text: `"moon landing"`})
	if err != nil {
		t.Fatalf("Search phrase: %v", err)
	}
	if len(page.Results) != 1 {
		t.Errorf("phrase search returned %d results, want 1", len(page.Results))
	}
}

func TestSearchFTSKeysetPagination(t *testing.T) {
	ss, fs, _ := newTestStoreWithMediaStore(t)
	ctx := context.Background()

	for i := 0; i < 9; i++ {
		seedFile(t, fs, fmt.Sprintf("beach_%d.jpg", i))
	}

	page1, err := ss.Search(ctx, search.SearchQuery{Text: "beach", Limit: 4})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Results) != 4 {
		t.Fatalf("page1 len = %d, want 4", len(page1.Results))
	}
	if page1.NextCursor == "" {
		t.Fatal("page1 has no NextCursor")
	}

	page2, err := ss.Search(ctx, search.SearchQuery{Text: "beach", Limit: 4, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Results) != 4 {
		t.Fatalf("page2 len = %d, want 4", len(page2.Results))
	}
	if page2.NextCursor == "" {
		t.Fatal("page2 has no NextCursor")
	}

	page3, err := ss.Search(ctx, search.SearchQuery{Text: "beach", Limit: 4, Cursor: page2.NextCursor})
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3.Results) != 1 {
		t.Fatalf("page3 len = %d, want 1 (remainder)", len(page3.Results))
	}
	if page3.NextCursor != "" {
		t.Fatal("page3 should have no NextCursor")
	}

	seen := map[string]bool{}
	for _, r := range append(append(page1.Results, page2.Results...), page3.Results...) {
		if seen[r.File.ID] {
			t.Errorf("duplicate file across pages: %s", r.File.ID)
		}
		seen[r.File.ID] = true
	}
}

func TestSearchInvalidQuery(t *testing.T) {
	ss, fs, _ := newTestStoreWithMediaStore(t)
	ctx := context.Background()
	seedFile(t, fs, "beach.jpg")

	for _, expr := range []string{`"unterminated`, `a AND`, `a b) c`, `-`} {
		if _, err := ss.Search(ctx, search.SearchQuery{Text: expr}); !errors.Is(err, search.ErrInvalidQuery) {
			t.Errorf("Search(%q) err = %v, want ErrInvalidQuery", expr, err)
		}
	}

	// Unbalanced size ranges are rejected before hitting SQL.
	if _, err := ss.Search(ctx, search.SearchQuery{MinSize: 10, MaxSize: 5}); !errors.Is(err, search.ErrInvalidQuery) {
		t.Errorf("Search(min>max) err = %v, want ErrInvalidQuery", err)
	}
}

func TestSearchPunctuationOnlyQuery(t *testing.T) {
	ss, fs, _ := newTestStoreWithMediaStore(t)
	ctx := context.Background()
	seedFile(t, fs, "beach.jpg")

	page, err := ss.Search(ctx, search.SearchQuery{Text: "//---!"})
	if err != nil {
		t.Fatalf("Search punctuation-only: %v", err)
	}
	if len(page.Results) != 0 {
		t.Errorf("punctuation-only query returned %d results, want 0", len(page.Results))
	}
}

package search_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/search"
)

func TestSearchContentFindsAcrossSourceTypes(t *testing.T) {
	_, fs, db := newTestStoreWithMediaStore(t)
	ctx := context.Background()
	ss := search.NewSearchStore(db, "lib1")

	seedFile(t, fs, "dune.epub")
	file, err := fs.GetByRelPath(ctx, "dune.epub")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO quotes (id, user_id, file_id, text, commentary, created_at, updated_at)
		VALUES ('q1', 'u1', ?, 'fear is the mindkiller', '', ?, ?)`, file.ID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO highlights (id, user_id, file_id, location, text, note, created_at, updated_at)
		VALUES ('h1', 'u1', ?, 'page:1', 'the spice must flow', '', ?, ?)`, file.ID, now, now); err != nil {
		t.Fatal(err)
	}

	hits, err := ss.SearchContent(ctx, search.ContentQuery{Text: "mindkiller", UserID: "u1"})
	if err != nil {
		t.Fatalf("SearchContent: %v", err)
	}
	if len(hits) != 1 || hits[0].SourceType != search.ContentQuote || hits[0].SourceID != "q1" {
		t.Fatalf("hits = %+v, want one quote hit", hits)
	}

	hits, err = ss.SearchContent(ctx, search.ContentQuery{Text: "spice", UserID: "u1"})
	if err != nil {
		t.Fatalf("SearchContent: %v", err)
	}
	if len(hits) != 1 || hits[0].SourceType != search.ContentHighlight {
		t.Fatalf("hits = %+v, want one highlight hit", hits)
	}
}

func TestSearchContentScopedToUser(t *testing.T) {
	_, fs, db := newTestStoreWithMediaStore(t)
	ctx := context.Background()
	ss := search.NewSearchStore(db, "lib1")

	seedFile(t, fs, "dune.epub")
	file, err := fs.GetByRelPath(ctx, "dune.epub")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO quotes (id, user_id, file_id, text, commentary, created_at, updated_at)
		VALUES ('q1', 'u2', ?, 'a secret quote', '', ?, ?)`, file.ID, now, now); err != nil {
		t.Fatal(err)
	}

	hits, err := ss.SearchContent(ctx, search.ContentQuery{Text: "secret", UserID: "u1"})
	if err != nil {
		t.Fatalf("SearchContent: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("another user's quote leaked into the search, hits = %+v", hits)
	}

	hits, err = ss.SearchContent(ctx, search.ContentQuery{Text: "secret", UserID: "u2"})
	if err != nil {
		t.Fatalf("SearchContent: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("owner's own quote missing from search, hits = %+v", hits)
	}
}

func TestSearchContentLibraryWideMetadataVisibleToAnyUser(t *testing.T) {
	_, fs, db := newTestStoreWithMediaStore(t)
	ctx := context.Background()
	ss := search.NewSearchStore(db, "lib1")

	seedFile(t, fs, "dune.epub")
	file, err := fs.GetByRelPath(ctx, "dune.epub")
	if err != nil {
		t.Fatal(err)
	}
	if err := search.SyncContent(ctx, db, search.ContentMetadata, file.ID, file.ID, "", "Dune Frank Herbert"); err != nil {
		t.Fatal(err)
	}

	hits, err := ss.SearchContent(ctx, search.ContentQuery{Text: "Herbert", UserID: "someone-else"})
	if err != nil {
		t.Fatalf("SearchContent: %v", err)
	}
	if len(hits) != 1 || hits[0].SourceType != search.ContentMetadata {
		t.Errorf("library-wide metadata not visible to another user, hits = %+v", hits)
	}
}

func TestSearchContentScopedToFile(t *testing.T) {
	_, fs, db := newTestStoreWithMediaStore(t)
	ctx := context.Background()
	ss := search.NewSearchStore(db, "lib1")

	seedFile(t, fs, "a.epub")
	seedFile(t, fs, "b.epub")
	fileA, _ := fs.GetByRelPath(ctx, "a.epub")
	fileB, _ := fs.GetByRelPath(ctx, "b.epub")

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO quotes (id, user_id, file_id, text, commentary, created_at, updated_at)
		VALUES ('qa', 'u1', ?, 'overlap term', '', ?, ?)`, fileA.ID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO quotes (id, user_id, file_id, text, commentary, created_at, updated_at)
		VALUES ('qb', 'u1', ?, 'overlap term', '', ?, ?)`, fileB.ID, now, now); err != nil {
		t.Fatal(err)
	}

	hits, err := ss.SearchContent(ctx, search.ContentQuery{Text: "overlap", UserID: "u1", FileID: fileA.ID})
	if err != nil {
		t.Fatalf("SearchContent: %v", err)
	}
	if len(hits) != 1 || hits[0].SourceID != "qa" {
		t.Errorf("hits = %+v, want only the quote scoped to fileA", hits)
	}
}

func TestSearchContentEmptyQuery(t *testing.T) {
	_, _, db := newTestStoreWithMediaStore(t)
	ctx := context.Background()
	ss := search.NewSearchStore(db, "lib1")

	hits, err := ss.SearchContent(ctx, search.ContentQuery{Text: "   ", UserID: "u1"})
	if err != nil {
		t.Fatalf("SearchContent: %v", err)
	}
	if hits != nil {
		t.Errorf("hits = %+v, want nil for an empty/blank query", hits)
	}
}

func TestSearchContentExcerptSurroundsMatch(t *testing.T) {
	_, fs, db := newTestStoreWithMediaStore(t)
	ctx := context.Background()
	ss := search.NewSearchStore(db, "lib1")

	seedFile(t, fs, "dune.epub")
	file, _ := fs.GetByRelPath(ctx, "dune.epub")
	longText := "the quick brown fox jumps over the lazy dog and keeps running through the arrakis desert sands forever"
	if err := search.SyncContent(ctx, db, search.ContentBookText, file.ID, file.ID, "", longText); err != nil {
		t.Fatal(err)
	}

	hits, err := ss.SearchContent(ctx, search.ContentQuery{Text: "arrakis", UserID: "u1"})
	if err != nil {
		t.Fatalf("SearchContent: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v, want one book_text hit", hits)
	}
	if !strings.Contains(strings.ToLower(hits[0].Excerpt), "arrakis") {
		t.Errorf("Excerpt = %q, want it to contain the match", hits[0].Excerpt)
	}
	if len(hits[0].Excerpt) >= len(longText) {
		t.Errorf("Excerpt = %q, want a window shorter than the full text", hits[0].Excerpt)
	}
}

func TestSyncContentReplacesAndRemoves(t *testing.T) {
	_, fs, db := newTestStoreWithMediaStore(t)
	ctx := context.Background()
	ss := search.NewSearchStore(db, "lib1")

	seedFile(t, fs, "dune.epub")
	file, _ := fs.GetByRelPath(ctx, "dune.epub")

	if err := search.SyncContent(ctx, db, search.ContentBookText, file.ID, file.ID, "", "original text"); err != nil {
		t.Fatal(err)
	}
	hits, _ := ss.SearchContent(ctx, search.ContentQuery{Text: "original", UserID: "u1"})
	if len(hits) != 1 {
		t.Fatalf("expected the original text to be indexed, hits = %+v", hits)
	}

	if err := search.SyncContent(ctx, db, search.ContentBookText, file.ID, file.ID, "", "replaced text"); err != nil {
		t.Fatal(err)
	}
	hits, _ = ss.SearchContent(ctx, search.ContentQuery{Text: "original", UserID: "u1"})
	if len(hits) != 0 {
		t.Errorf("old text still indexed after replacement, hits = %+v", hits)
	}
	hits, _ = ss.SearchContent(ctx, search.ContentQuery{Text: "replaced", UserID: "u1"})
	if len(hits) != 1 {
		t.Errorf("replacement text not indexed, hits = %+v", hits)
	}

	if err := search.SyncContent(ctx, db, search.ContentBookText, file.ID, file.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	hits, _ = ss.SearchContent(ctx, search.ContentQuery{Text: "replaced", UserID: "u1"})
	if len(hits) != 0 {
		t.Errorf("row should be removed by an empty-text sync, hits = %+v", hits)
	}
}

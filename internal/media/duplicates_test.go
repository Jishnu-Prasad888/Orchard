package media_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/media"
)

// seedIndexedFile inserts an indexed_files row directly with an explicit
// content hash so duplicate tests don't depend on the scanner/ML pipeline.
func seedIndexedFile(t *testing.T, store *media.FileStore, root, id, relPath string, size int64, hash, status string) {
	t.Helper()
	ldb, err := librarydb.Open(filepath.Join(root, ".cairn"))
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	defer func() { _ = ldb.Close() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = ldb.Exec(`INSERT INTO indexed_files
		(id, rel_path, size_bytes, mod_time, content_hash, status, first_seen_at, last_seen_at, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, relPath, size, now, hash, status, now, now, now)
	if err != nil {
		t.Fatalf("insert %s: %v", relPath, err)
	}
}

func TestListDuplicates(t *testing.T) {
	store, root := newTestStore(t)
	ctx := context.Background()

	const (
		hashA = "sha256-aaaa"
		hashB = "sha256-bbbb"
		hashC = "sha256-cccc"
	)

	// Duplicate pair of identical photos.
	seedIndexedFile(t, store, root, "f1", "IMG_0001.png", 1000, hashA, "present")
	seedIndexedFile(t, store, root, "f2", "2024/IMG_0001_copy.png", 1000, hashA, "present")
	// A second pair in a different folder.
	seedIndexedFile(t, store, root, "f3", "video/a.mp4", 500000, hashC, "present")
	seedIndexedFile(t, store, root, "f4", "video/a_copy.mp4", 500000, hashC, "present")
	// Unique content.
	seedIndexedFile(t, store, root, "f5", "notes.txt", 200, hashB, "present")
	// Missing/deleted rows share hashA but must not form groups.
	seedIndexedFile(t, store, root, "f6", "gone.png", 1000, hashA, "missing")
	seedIndexedFile(t, store, root, "f7", "trashed.png", 1000, hashA, "deleted")
	// Hashless rows are ignored.
	seedIndexedFile(t, store, root, "f8", "no-hash.bin", 300, "", "present")

	page, err := store.ListDuplicates(ctx, media.DuplicateOptions{Limit: 50})
	if err != nil {
		t.Fatalf("ListDuplicates: %v", err)
	}

	if page.Total != 2 {
		t.Errorf("Total = %d, want 2", page.Total)
	}
	if len(page.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(page.Groups))
	}
	if page.NextCursor != "" {
		t.Errorf("NextCursor = %q, want empty on final page", page.NextCursor)
	}

	// Groups are ordered by content hash.
	if page.Groups[0].ContentHash != hashA || page.Groups[1].ContentHash != hashC {
		t.Errorf("group order = [%s, %s], want [%s, %s]",
			page.Groups[0].ContentHash, page.Groups[1].ContentHash, hashA, hashC)
	}

	// Group A: two present members, correct size, sorted by rel_path.
	g := page.Groups[0]
	if g.SizeBytes != 1000 {
		t.Errorf("group size = %d, want 1000", g.SizeBytes)
	}
	if len(g.Files) != 2 {
		t.Fatalf("group A members = %d, want 2 (missing/deleted ignored)", len(g.Files))
	}
	if g.Files[0].RelPath != "2024/IMG_0001_copy.png" || g.Files[1].RelPath != "IMG_0001.png" {
		t.Errorf("group A members = [%s, %s], want sorted by rel_path",
			g.Files[0].RelPath, g.Files[1].RelPath)
	}
	for _, f := range g.Files {
		if f.Status != media.FileStatusPresent {
			t.Errorf("member %s status = %q, want present", f.RelPath, f.Status)
		}
		if f.LibraryID != "lib1" {
			t.Errorf("member %s library_id = %q", f.RelPath, f.LibraryID)
		}
	}
}

func TestListDuplicatesPagination(t *testing.T) {
	store, root := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		h := "sha256-h" + string(rune('a'+i))
		seedIndexedFile(t, store, root, "f"+string(rune('a'+i))+"a", "dup"+string(rune('a'+i))+"-1.txt", 10, h, "present")
		seedIndexedFile(t, store, root, "f"+string(rune('a'+i))+"b", "dup"+string(rune('a'+i))+"-2.txt", 10, h, "present")
	}

	// Page 1 of 1 group.
	page, err := store.ListDuplicates(ctx, media.DuplicateOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 {
		t.Errorf("Total = %d, want 3", page.Total)
	}
	if len(page.Groups) != 1 || page.NextCursor == "" {
		t.Fatalf("page1: groups=%d cursor=%q, want 1 group + cursor", len(page.Groups), page.NextCursor)
	}

	// Page 2 continues after the cursor.
	page2, err := store.ListDuplicates(ctx, media.DuplicateOptions{Limit: 1, Cursor: page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Groups) != 1 || page2.NextCursor == "" {
		t.Fatalf("page2: groups=%d cursor=%q, want 1 group + cursor", len(page2.Groups), page2.NextCursor)
	}
	if page2.Groups[0].ContentHash == page.Groups[0].ContentHash {
		t.Error("page2 repeats page1 group")
	}

	// Page 3 is the last page and never splits a group.
	page3, err := store.ListDuplicates(ctx, media.DuplicateOptions{Limit: 2, Cursor: page2.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(page3.Groups) != 1 || page3.NextCursor != "" {
		t.Fatalf("page3: groups=%d cursor=%q, want 1 group, no cursor", len(page3.Groups), page3.NextCursor)
	}
}

func TestListDuplicatesEmptyLibrary(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	page, err := store.ListDuplicates(ctx, media.DuplicateOptions{})
	if err != nil {
		t.Fatalf("ListDuplicates: %v", err)
	}
	if page.Total != 0 || len(page.Groups) != 0 || page.NextCursor != "" {
		t.Errorf("empty result = total=%d groups=%d cursor=%q, want 0/0/''",
			page.Total, len(page.Groups), page.NextCursor)
	}
}

func TestListDuplicatesBoundaryCursor(t *testing.T) {
	store, root := newTestStore(t)
	ctx := context.Background()

	seedIndexedFile(t, store, root, "f1", "a.txt", 10, "sha256-x", "present")
	seedIndexedFile(t, store, root, "f2", "b.txt", 10, "sha256-x", "present")

	// A cursor past every hash returns an empty page with the same total.
	page, err := store.ListDuplicates(ctx, media.DuplicateOptions{Cursor: "zzzz", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Errorf("Total = %d, want 1", page.Total)
	}
	if len(page.Groups) != 0 || page.NextCursor != "" {
		t.Errorf("past-cursor page = groups=%d cursor=%q, want 0/''", len(page.Groups), page.NextCursor)
	}
}

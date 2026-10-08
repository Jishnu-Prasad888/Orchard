package media_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/media"
)

// --- helpers ---

func newTestStore(t *testing.T) (*media.FileStore, string) {
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
	return media.NewFileStore(db, "lib1"), root
}

func newTestService(t *testing.T) (*media.Service, *media.FileStore, string) {
	t.Helper()
	store, root := newTestStore(t)
	svc := media.NewService(store, root)
	return svc, store, root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedFile(t *testing.T, store *media.FileStore, root, relPath string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	writeFile(t, abs, "content of "+relPath)
	info, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFromPath(context.Background(), relPath, info.Size(), info.ModTime()); err != nil {
		t.Fatalf("seed %s: %v", relPath, err)
	}
}

// --- DetectMediaType ---

func TestDetectMediaType(t *testing.T) {
	cases := []struct {
		path string
		want media.MediaType
	}{
		{"photo.jpg", media.MediaTypePhoto},
		{"photo.JPEG", media.MediaTypePhoto},
		{"video.mp4", media.MediaTypeVideo},
		{"audio.mp3", media.MediaTypeAudio},
		{"doc.pdf", media.MediaTypeDocument},
		{"archive.zip", media.MediaTypeOther},
		{"noext", media.MediaTypeOther},
	}
	for _, tc := range cases {
		got := media.DetectMediaType(tc.path)
		if got != tc.want {
			t.Errorf("DetectMediaType(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// --- SafeRelPath ---

func TestSafeRelPath(t *testing.T) {
	ok := []string{"photo.jpg", "2022/img.jpg", "a/b/c.mp4", "2022/", "a//b//c.mp4", "a.b/c"}
	for _, p := range ok {
		if _, err := media.SafeRelPath(p); err != nil {
			t.Errorf("SafeRelPath(%q) unexpected error: %v", p, err)
		}
	}
	bad := []string{"", "../escape.jpg", "/absolute.jpg", "a/../../escape", ".", "..", "./x", "a/./b", ".cairn/db", "a\x00b", "a\nb"}
	for _, p := range bad {
		if _, err := media.SafeRelPath(p); err == nil {
			t.Errorf("SafeRelPath(%q) expected error", p)
		}
	}
}

// --- FileStore listing ---

func TestListFiles(t *testing.T) {
	store, root := newTestStore(t)
	ctx := context.Background()

	seedFile(t, store, root, "a.jpg")
	seedFile(t, store, root, "b.mp4")
	seedFile(t, store, root, "sub/c.png")

	// Root-level only.
	page, err := store.List(ctx, media.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Files) != 2 {
		t.Errorf("root files = %d, want 2", len(page.Files))
	}

	// Recursive — all 3.
	page, err = store.List(ctx, media.ListOptions{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Files) != 3 {
		t.Errorf("recursive files = %d, want 3", len(page.Files))
	}
}

func TestListFilesSortByName(t *testing.T) {
	store, root := newTestStore(t)
	ctx := context.Background()
	for _, name := range []string{"c.jpg", "a.jpg", "b.jpg"} {
		seedFile(t, store, root, name)
	}
	page, err := store.List(ctx, media.ListOptions{Sort: media.SortByName, Order: media.SortAsc})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(page.Files))
	for i, f := range page.Files {
		names[i] = f.Name
	}
	if names[0] != "a.jpg" || names[1] != "b.jpg" || names[2] != "c.jpg" {
		t.Errorf("sort order = %v", names)
	}
}

func TestListFilesPagination(t *testing.T) {
	store, root := newTestStore(t)
	ctx := context.Background()
	for _, name := range []string{"a.jpg", "b.jpg", "c.jpg", "d.jpg", "e.jpg"} {
		seedFile(t, store, root, name)
	}

	// Page 1: limit=2
	page1, err := store.List(ctx, media.ListOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Files) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1.Files))
	}
	if page1.NextCursor == "" {
		t.Error("expected NextCursor for page 1")
	}

	// Page 2: cursor from page1
	page2, err := store.List(ctx, media.ListOptions{Limit: 2, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Files) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(page2.Files))
	}

	// No overlap between pages.
	seen := map[string]bool{}
	for _, f := range page1.Files {
		seen[f.RelPath] = true
	}
	for _, f := range page2.Files {
		if seen[f.RelPath] {
			t.Errorf("overlap: %s appeared in both pages", f.RelPath)
		}
	}
}

// TestListFilesFilterByType proves the media-type filter reaches the query.
//
// The Videos page asks for ?type=video and was answered with the whole
// library, because the handler parsed the type and the store dropped it. The
// count is asserted as well: a filter that narrows the rows but not the count
// makes the pager promise a page the query will never return.
func TestListFilesFilterByType(t *testing.T) {
	store, root := newTestStore(t)
	ctx := context.Background()
	seedFile(t, store, root, "a.jpg")
	seedFile(t, store, root, "b.png")
	seedFile(t, store, root, "c.mp4")
	seedFile(t, store, root, "d.mov")
	seedFile(t, store, root, "notes.pdf")
	seedFile(t, store, root, "sub/e.mp4")

	cases := []struct {
		typ  media.MediaType
		want []string
	}{
		{media.MediaTypeVideo, []string{"c.mp4", "d.mov", "sub/e.mp4"}},
		{media.MediaTypePhoto, []string{"a.jpg", "b.png"}},
		{media.MediaTypeDocument, []string{"notes.pdf"}},
		{media.MediaTypeAudio, nil},
	}
	for _, tc := range cases {
		page, err := store.List(ctx, media.ListOptions{Type: tc.typ, Recursive: true})
		if err != nil {
			t.Fatalf("List(%s): %v", tc.typ, err)
		}
		var got []string
		for _, f := range page.Files {
			got = append(got, f.RelPath)
			if f.MediaType != tc.typ {
				t.Errorf("List(%s) returned %s whose type is %s", tc.typ, f.RelPath, f.MediaType)
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("List(%s) = %v, want %v", tc.typ, got, tc.want)
		}
		if page.Total != len(tc.want) {
			t.Errorf("List(%s) total = %d, want %d", tc.typ, page.Total, len(tc.want))
		}
	}

	// The type survives the type-specific scans, not just the listing.
	f, err := store.GetByRelPath(ctx, "c.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if f.MediaType != media.MediaTypeVideo {
		t.Errorf("GetByRelPath(c.mp4).MediaType = %s, want video", f.MediaType)
	}
}

// TestUpdateRelPathReclassifies proves a rename that changes the extension
// moves the file between the typed listings.
func TestUpdateRelPathReclassifies(t *testing.T) {
	store, root := newTestStore(t)
	ctx := context.Background()
	seedFile(t, store, root, "clip.mp4")
	f, err := store.GetByRelPath(ctx, "clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRelPath(ctx, f.ID, "clip.mov"); err != nil {
		t.Fatal(err)
	}

	page, err := store.List(ctx, media.ListOptions{Type: media.MediaTypeVideo, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Files) != 1 || page.Files[0].MediaType != media.MediaTypeVideo {
		t.Errorf("after rename to .mov, video listing = %v", page.Files)
	}
	page, err = store.List(ctx, media.ListOptions{Type: media.MediaTypePhoto, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Files) != 0 {
		t.Errorf("after rename to .mov, photo listing = %v, want empty", page.Files)
	}
}

func TestListFilesFilterByFolder(t *testing.T) {
	store, root := newTestStore(t)
	ctx := context.Background()
	seedFile(t, store, root, "2022/jan.jpg")
	seedFile(t, store, root, "2022/feb.jpg")
	seedFile(t, store, root, "2023/mar.jpg")

	page, err := store.List(ctx, media.ListOptions{FolderPath: "2022", Recursive: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Files) != 2 {
		t.Errorf("folder filter = %d files, want 2", len(page.Files))
	}
}

// TestListFolderEscapesLikeWildcards proves a folder whose name contains a
// LIKE wildcard (here an underscore) is matched literally and does not pull
// in siblings.
func TestListFolderEscapesLikeWildcards(t *testing.T) {
	store, root := newTestStore(t)
	ctx := context.Background()
	for _, p := range []string{
		"a_b/one.jpg",
		"a_b/two.jpg",
		"ab/three.jpg",
		"100%/four.jpg", // percent is not a path char issue on linux but is a LIKE wildcard
		"100/five.jpg",
	} {
		seedFile(t, store, root, p)
	}

	page, err := store.List(ctx, media.ListOptions{FolderPath: "a_b", Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Files) != 2 {
		names := []string{}
		for _, f := range page.Files {
			names = append(names, f.RelPath)
		}
		t.Errorf("folder a_b = %v, want exactly 2 entries", names)
	}

	page, err = store.List(ctx, media.ListOptions{FolderPath: "100%", Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Files) != 1 || page.Files[0].RelPath != "100%/four.jpg" {
		t.Errorf("folder 100%% = %v, want only 100%%/four.jpg", page.Files)
	}
}

// --- Upload ---

func TestUpload(t *testing.T) {
	svc, store, _ := newTestService(t)
	ctx := context.Background()

	content := "hello upload content"
	f, err := svc.WriteUpload(ctx, "uploads/new.txt", strings.NewReader(content))
	if err != nil {
		t.Fatalf("WriteUpload: %v", err)
	}
	if f.Name != "new.txt" {
		t.Errorf("name = %q, want new.txt", f.Name)
	}
	if f.SizeBytes != int64(len(content)) {
		t.Errorf("size = %d, want %d", f.SizeBytes, len(content))
	}

	// File must be indexed.
	got, err := store.GetByRelPath(ctx, "uploads/new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("uploaded file not found in index")
	}
}

func TestUploadRejectsExisting(t *testing.T) {
	svc, store, root := newTestService(t)
	ctx := context.Background()
	seedFile(t, store, root, "exists.jpg")

	_, err := svc.WriteUpload(ctx, "exists.jpg", strings.NewReader("data"))
	if err == nil {
		t.Error("expected ErrAlreadyExists")
	}
}

func TestUploadPathTraversalRejected(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, err := svc.WriteUpload(context.Background(), "../escape.txt", strings.NewReader("x"))
	if err == nil {
		t.Error("expected path traversal error")
	}
}

func TestUploadCapEnforced(t *testing.T) {
	store, root := newTestStore(t)
	svc := media.NewService(store, root, media.WithMaxUploadBytes(64))
	ctx := context.Background()

	_, err := svc.WriteUpload(ctx, "big.bin", strings.NewReader("x"+strings.Repeat("y", 64)))
	if !errors.Is(err, media.ErrUploadTooLarge) {
		t.Fatalf("oversized upload error = %v, want ErrUploadTooLarge", err)
	}

	// Nothing may be committed the over-limit file.
	if _, err := store.GetByRelPath(ctx, "big.bin"); !errors.Is(err, media.ErrNotFound) {
		t.Errorf("over-limit upload was indexed: %v", err)
	}

	// And no temp residue remains in the library root.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".cairn-upload") {
			t.Errorf("stale upload temp file left behind: %s", e.Name())
		}
	}
}

func TestUploadWithinCapSucceeds(t *testing.T) {
	store, root := newTestStore(t)
	svc := media.NewService(store, root, media.WithMaxUploadBytes(64))
	content := strings.Repeat("z", 64)
	f, err := svc.WriteUpload(context.Background(), "at-limit.bin", strings.NewReader(content))
	if err != nil {
		t.Fatalf("WriteUpload at exact cap: %v", err)
	}
	if f.SizeBytes != int64(len(content)) {
		t.Errorf("size = %d, want %d", f.SizeBytes, len(content))
	}
}

// --- Rename ---

func TestRename(t *testing.T) {
	svc, store, root := newTestService(t)
	ctx := context.Background()
	seedFile(t, store, root, "old.jpg")

	updated, err := svc.Rename(ctx, "old.jpg", "new.jpg")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if updated.Name != "new.jpg" {
		t.Errorf("name = %q, want new.jpg", updated.Name)
	}
	// Old path gone from index.
	f, _ := store.GetByRelPath(ctx, "old.jpg")
	if f != nil {
		t.Error("old path still in index after rename")
	}
}

func TestRenameRejectsTraversalName(t *testing.T) {
	svc, store, root := newTestService(t)
	ctx := context.Background()
	seedFile(t, store, root, "old.jpg")

	for _, name := range []string{"../escape.jpg", "a/b.jpg", ".cairn", "x\x00.jpg"} {
		if _, err := svc.Rename(ctx, "old.jpg", name); err == nil {
			t.Errorf("Rename with new_name %q expected error", name)
		}
	}
	// Original is untouched and still present.
	if f, err := store.GetByRelPath(ctx, "old.jpg"); err != nil || f == nil {
		t.Errorf("old path not present after rejected renames: err=%v", err)
	}
}

// --- Move ---

func TestMove(t *testing.T) {
	svc, store, root := newTestService(t)
	ctx := context.Background()
	seedFile(t, store, root, "src/photo.jpg")

	updated, err := svc.Move(ctx, "src/photo.jpg", "dst/photo.jpg")
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if updated.RelPath != "dst/photo.jpg" {
		t.Errorf("rel_path = %q, want dst/photo.jpg", updated.RelPath)
	}
	f, _ := store.GetByRelPath(ctx, "src/photo.jpg")
	if f != nil {
		t.Error("old path still in index after move")
	}
}

// --- Copy ---

func TestCopy(t *testing.T) {
	svc, store, root := newTestService(t)
	ctx := context.Background()
	seedFile(t, store, root, "original.jpg")

	copied, err := svc.Copy(ctx, "original.jpg", "copy.jpg")
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if copied.RelPath != "copy.jpg" {
		t.Errorf("rel_path = %q, want copy.jpg", copied.RelPath)
	}
	// Both files should exist on disk.
	if _, err := os.Stat(filepath.Join(root, "original.jpg")); err != nil {
		t.Error("original missing after copy")
	}
	if _, err := os.Stat(filepath.Join(root, "copy.jpg")); err != nil {
		t.Error("copy not on disk")
	}
	// Both in index.
	orig, _ := store.GetByRelPath(ctx, "original.jpg")
	if orig == nil {
		t.Error("original not in index")
	}
}

// --- Delete (trash) and Restore ---

func TestDeleteAndRestore(t *testing.T) {
	svc, store, root := newTestService(t)
	ctx := context.Background()
	seedFile(t, store, root, "photo.jpg")

	// Soft delete.
	if err := svc.Delete(ctx, "photo.jpg", "user1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	f, _ := store.GetByRelPath(ctx, "photo.jpg")
	if f == nil {
		t.Fatal("file removed from index; should be kept as deleted")
	}
	if f.Status != media.FileStatusDeleted {
		t.Errorf("status = %q, want deleted", f.Status)
	}
	// File moved to trash on disk.
	if _, err := os.Stat(filepath.Join(root, "photo.jpg")); err == nil {
		t.Error("original file still at original path after delete")
	}

	// Restore.
	restored, err := svc.Restore(ctx, f.ID)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.Status != media.FileStatusPresent {
		t.Errorf("restored status = %q, want present", restored.Status)
	}
	if _, err := os.Stat(filepath.Join(root, "photo.jpg")); err != nil {
		t.Error("file not back at original path after restore")
	}
}

// --- Permanent delete ---

func TestPermanentDelete(t *testing.T) {
	svc, store, root := newTestService(t)
	ctx := context.Background()
	seedFile(t, store, root, "trash_me.jpg")

	if err := svc.Delete(ctx, "trash_me.jpg", ""); err != nil {
		t.Fatal(err)
	}
	f, _ := store.GetByRelPath(ctx, "trash_me.jpg")

	if err := svc.PermanentDelete(ctx, f.ID); err != nil {
		t.Fatalf("PermanentDelete: %v", err)
	}
	// Row gone from index.
	gone, _ := store.GetByID(ctx, f.ID)
	if gone != nil {
		t.Error("file still in index after permanent delete")
	}
}

// --- Original file invariant ---

func TestDownloadDoesNotModifyFile(t *testing.T) {
	svc, store, root := newTestService(t)
	ctx := context.Background()
	content := "original video bytes — must never change"
	seedFile(t, store, root, "video.mp4")
	// Write actual content so hash is stable.
	if err := os.WriteFile(filepath.Join(root, "video.mp4"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(root, "video.mp4"))
	sizeBefore := info.Size()
	mtimeBefore := info.ModTime()

	fh, _, err := svc.OpenFile(ctx, "video.mp4")
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	_ = fh.Close()

	info2, _ := os.Stat(filepath.Join(root, "video.mp4"))
	if info2.Size() != sizeBefore {
		t.Errorf("size changed: %d → %d", sizeBefore, info2.Size())
	}
	if !info2.ModTime().Equal(mtimeBefore) {
		t.Errorf("mtime changed after open")
	}
	_ = time.Now() // silence unused import
}

func TestListFoldersReadsDiskIncludingEmptyFolders(t *testing.T) {
	svc, _, root := newTestService(t)
	for _, d := range []string{"trips/2024", "empty", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	top, err := svc.ListFolders(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 || top[0].RelPath != "empty" || top[1].RelPath != "trips" {
		t.Fatalf("top-level folders = %+v", top)
	}
	sub, err := svc.ListFolders(context.Background(), "trips")
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 1 || sub[0].RelPath != "trips/2024" {
		t.Fatalf("sub folders = %+v", sub)
	}
}

func TestCreateFolderMakesDirectoryOnDisk(t *testing.T) {
	svc, _, root := newTestService(t)
	if _, err := svc.CreateFolder("trips/2025"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(root, "trips", "2025")); err != nil || !fi.IsDir() {
		t.Fatalf("folder not created: %v", err)
	}
	if _, err := svc.CreateFolder(".cairn/x"); err == nil {
		t.Fatal("hidden folder should be rejected")
	}
	if _, err := svc.CreateFolder("../escape"); err == nil {
		t.Fatal("traversal should be rejected")
	}
	if got, err := svc.ListFolders(context.Background(), "nope"); err != nil || len(got) != 0 {
		t.Fatalf("missing parent: %v %v", got, err)
	}
}

func TestListFoldersCountsIndexedFiles(t *testing.T) {
	svc, store, root := newTestService(t)
	if _, err := svc.CreateFolder("trips/2024"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if n, err := store.CountPresentUnder(ctx, "trips"); err != nil || n != 0 {
		t.Fatalf("empty count = %d, %v", n, err)
	}
	if err := os.WriteFile(filepath.Join(root, "trips", "2024", "a.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertFromPath(ctx, "trips/2024/a.jpg", 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	top, err := svc.ListFolders(ctx, "")
	if err != nil || len(top) != 1 || top[0].FileCount != 1 {
		t.Fatalf("top = %+v, %v", top, err)
	}
}

func TestCountsByTypeAndCollections(t *testing.T) {
	_, store, _ := newTestService(t)
	ctx := context.Background()
	for _, p := range []string{"a/1.jpg", "a/2.jpg", "b/v.mp4"} {
		if err := store.UpsertFromPath(ctx, p, 1, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := store.CountsByType(ctx)
	if err != nil || counts[media.MediaTypePhoto] != 2 || counts[media.MediaTypeVideo] != 1 {
		t.Fatalf("counts = %v, %v", counts, err)
	}
	c, err := store.CountCollections(ctx)
	if err != nil || c != (media.CollectionCounts{}) {
		t.Fatalf("collections = %+v, %v", c, err)
	}
}

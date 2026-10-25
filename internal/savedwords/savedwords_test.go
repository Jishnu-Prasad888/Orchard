package savedwords_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
	"github.com/Jishnu-Prasad888/Orchard/internal/savedwords"
)

func newTestStore(t *testing.T) (*savedwords.Store, *media.FileStore) {
	t.Helper()
	orchardDir := filepath.Join(t.TempDir(), ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.Open(orchardDir)
	if err != nil {
		t.Fatalf("open library db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return savedwords.NewStore(db), media.NewFileStore(db, "lib1")
}

func seedFile(t *testing.T, fs *media.FileStore, relPath string) string {
	t.Helper()
	ctx := context.Background()
	if err := fs.UpsertFromPath(ctx, relPath, 100, time.Now()); err != nil {
		t.Fatalf("seed %s: %v", relPath, err)
	}
	f, err := fs.GetByRelPath(ctx, relPath)
	if err != nil {
		t.Fatalf("get seeded file %s: %v", relPath, err)
	}
	return f.ID
}

func TestCreateListDelete(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")
	other := seedFile(t, fs, "other.epub")

	w, err := store.Create(ctx, savedwords.Word{
		UserID: "u1", FileID: fileID, Word: " ephemeral ", Location: "page:3",
		Definition: "adjective · lasting a very short time", Source: "WordNet",
	})
	if err != nil {
		t.Fatal(err)
	}
	if w.Word != "ephemeral" || w.Text != "ephemeral" || w.Source != "WordNet" || w.ID == "" {
		t.Errorf("created = %+v", w)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := store.Create(ctx, savedwords.Word{
		UserID: "u1", FileID: fileID, Word: "Running", Text: "running,", Location: "page:4",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, savedwords.Word{UserID: "u2", FileID: fileID, Word: "x", Location: "page:1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, savedwords.Word{UserID: "u1", FileID: other, Word: "y", Location: "page:1"}); err != nil {
		t.Fatal(err)
	}

	list, err := store.ListByFile(ctx, "u1", fileID)
	if err != nil || len(list) != 2 || list[0].Word != "Running" || list[0].Text != "running," {
		t.Fatalf("list = %+v, %v", list, err)
	}

	// Another user can neither see nor delete them.
	if err := store.Delete(ctx, w.ID, "u2"); !errors.Is(err, savedwords.ErrNotFound) {
		t.Errorf("delete as other user: %v", err)
	}
	if err := store.Delete(ctx, w.ID, "u1"); err != nil {
		t.Fatal(err)
	}
	if list, _ := store.ListByFile(ctx, "u1", fileID); len(list) != 1 {
		t.Errorf("after delete = %+v", list)
	}
}

func TestCreateValidates(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")
	for name, w := range map[string]savedwords.Word{
		"empty word":   {Word: " ", Location: "page:1"},
		"no location":  {Word: "word"},
		"long word":    {Word: strings.Repeat("a", savedwords.MaxWordRunes+1), Location: "page:1"},
		"long text":    {Word: "a", Text: strings.Repeat("a", savedwords.MaxTextRunes+1), Location: "page:1"},
		"long meaning": {Word: "a", Location: "page:1", Definition: strings.Repeat("a", savedwords.MaxDefinitionRunes+1)},
	} {
		w.UserID, w.FileID = "u1", fileID
		if _, err := store.Create(ctx, w); !errors.Is(err, savedwords.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}

func TestDeletedFileTakesItsWords(t *testing.T) {
	store, fs := newTestStore(t)
	ctx := context.Background()
	fileID := seedFile(t, fs, "book.epub")
	if _, err := store.Create(ctx, savedwords.Word{UserID: "u1", FileID: fileID, Word: "a", Location: "page:1"}); err != nil {
		t.Fatal(err)
	}
	if err := fs.DeleteRow(ctx, fileID); err != nil {
		t.Fatal(err)
	}
	if list, _ := store.ListByFile(ctx, "u1", fileID); len(list) != 0 {
		t.Errorf("words outlived their file: %+v", list)
	}
}

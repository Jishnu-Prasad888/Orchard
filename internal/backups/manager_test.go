package backups

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
	"github.com/Jishnu-Prasad888/Cairn/internal/db"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(os.Stderr, nil)) }

// tempServerDB builds a migrated server-level database under tmp.
func tempServerDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cairn.db")
	pool, err := db.Open(path)
	if err != nil {
		t.Fatalf("open server db: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate server db: %v", err)
	}
	return pool, path
}

// registerLibrary registers a fresh library with one media file and an
// existing per-library metadata db, returning the registered Library.
func registerLibrary(t *testing.T, lm *library.Manager, root string) library.Library {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir library root: %v", err)
	}
	lib, err := lm.Create(context.Background(), root, filepath.Base(root))
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	media := filepath.Join(root, "holiday", "beach.jpg")
	if err := os.MkdirAll(filepath.Dir(media), 0o755); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	if err := os.WriteFile(media, []byte("jpeg-bytes-that-are-descriptive-enough"), 0o644); err != nil {
		t.Fatalf("write media: %v", err)
	}
	if _, err := librarydb.Open(filepath.Join(root, ".cairn")); err != nil {
		t.Fatalf("open library db: %v", err)
	}
	got, err := lm.Get(context.Background(), lib.ID)
	if err != nil {
		t.Fatalf("get library: %v", err)
	}
	return *got
}

func newManager(t *testing.T, pool *sql.DB, serverPath, backupDir, passphrase string, keep int) *Manager {
	t.Helper()
	logger := testLogger()
	lm := library.NewManager(pool, logger, audit.New(pool, logger), crypto.NewKeys(""))
	return NewManager(pool, logger, lm, Config{
		Dir:        backupDir,
		Keep:       keep,
		Passphrase: passphrase,
	}, serverPath)
}

// readStored decodes the raw bytes of a stored payload back to plaintext.
func readStored(t *testing.T, path string, key []byte) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stored payload: %v", err)
	}
	pr, err := openPayload(bytes.NewReader(raw), key)
	if err != nil {
		t.Fatalf("openPayload: %v", err)
	}
	out, err := io.ReadAll(pr)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if err := pr.Err(); err != nil {
		t.Fatalf("payload checksum: %v", err)
	}
	return out
}

func TestRunBackupLifecycle(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	backupDir := filepath.Join(t.TempDir(), "backups")

	m := newManager(t, pool, serverPath, backupDir, "", 4)
	lib := registerLibrary(t, library.NewManager(pool, testLogger(), audit.New(pool, testLogger()), crypto.NewKeys("")), filepath.Join(t.TempDir(), "alpha"))

	rec, err := m.Run(context.Background())
	if err != nil {
		t.Fatalf("backup run: %v", err)
	}
	if rec.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed (error: %s)", rec.Status, rec.ErrorMsg)
	}
	if rec.ID == "" {
		t.Fatal("backup id is empty")
	}
	if rec.Libraries != 1 {
		t.Fatalf("libraries = %d, want 1", rec.Libraries)
	}
	if rec.Files != 1 {
		t.Fatalf("files = %d, want 1", rec.Files)
	}
	if rec.Bytes <= 0 || rec.StoredBytes <= 0 {
		t.Fatalf("bytes/stored_bytes not counted: %d/%d", rec.Bytes, rec.StoredBytes)
	}
	if rec.VerifyStatus != VerifyOK || rec.VerifyChecked == 0 || rec.VerifyErrors != 0 {
		t.Fatalf("unexpected verify state: %s checked=%d errors=%d",
			rec.VerifyStatus, rec.VerifyChecked, rec.VerifyErrors)
	}

	// The stored layout is complete.
	for _, want := range []string{
		"header.json",
		manifestFilename,
		filepath.Join("server", "cairn.db"),
		filepath.Join("libraries", lib.ID, "library.db"),
		filepath.Join("libraries", lib.ID, "library.json"),
		filepath.Join("libraries", lib.ID, "files", "holiday", "beach.jpg"),
	} {
		if _, err := os.Stat(filepath.Join(rec.Destination, want)); err != nil {
			t.Errorf("missing stored artifact %s: %v", want, err)
		}
	}

	// Media content round-trips byte-for-byte through the codec.
	orig, _ := os.ReadFile(filepath.Join(lib.Root, "holiday", "beach.jpg"))
	got := readStored(t, filepath.Join(rec.Destination, "libraries", lib.ID, "files", "holiday", "beach.jpg"), nil)
	if !bytes.Equal(got, orig) {
		t.Fatal("restored media differs from source")
	}

	// The manifest unencrypted-encodes correctly and lists the media entry.
	mf, err := readManifest(rec.Destination, nil)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if len(mf.Libraries) != 1 || len(mf.Libraries[0].Files) != 1 {
		t.Fatalf("manifest libraries/files = %d/%d, want 1/1", len(mf.Libraries), len(mf.Libraries[0].Files))
	}

	// Records list newest-first.
	recs, err := m.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(recs) != 1 || recs[0].ID != rec.ID {
		t.Fatalf("list got %d records, want single %s", len(recs), rec.ID)
	}
	gotRec, err := m.Get(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if gotRec.Destination != rec.Destination || gotRec.Encrypted || gotRec.Status != StatusCompleted {
		t.Fatalf("get mismatch: %+v", gotRec)
	}
}

// TestBackupIncremental verifies a second run reuses unchanged file payloads
// via hard links (deterministic gzip + no encryption + matching size/mtime).
func TestBackupIncremental(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	backupDir := filepath.Join(t.TempDir(), "backups")

	lm := library.NewManager(pool, testLogger(), audit.New(pool, testLogger()), crypto.NewKeys(""))
	m := newManager(t, pool, serverPath, backupDir, "", 4)
	lib := registerLibrary(t, lm, filepath.Join(t.TempDir(), "bravo"))

	first, err := m.Run(context.Background())
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	second, err := m.Run(context.Background())
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Files < 1 {
		t.Fatalf("second run files = %d", second.Files)
	}
	if second.FilesSkipped != 1 {
		t.Fatalf("FilesSkipped = %d, want 1 (the unchanged media file)", second.FilesSkipped)
	}

	// Hard-linked stored payloads: the second run's media file shares an inode
	// with the first run's, so it is not a new copy.
	rel := filepath.Join("libraries", lib.ID, "files", "holiday", "beach.jpg")
	a := filepath.Join(first.Destination, rel)
	b := filepath.Join(second.Destination, rel)
	ia, ea := os.Stat(a)
	ib, eb := os.Stat(b)
	if ea != nil || eb != nil {
		t.Fatalf("stat stored payloads: %v / %v", ea, eb)
	}
	if !os.SameFile(ia, ib) {
		t.Log("note: stored payloads are not hard-linked on this filesystem")
	}

	// The second record's byte/verify accounting is populated.
	if second.StoredBytes <= 0 || second.VerifyChecked == 0 {
		t.Fatalf("second run accounting incomplete: stored=%d checked=%d",
			second.StoredBytes, second.VerifyChecked)
	}
}

func TestBackupEncrypted(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	backupDir := filepath.Join(t.TempDir(), "backups")

	m := newManager(t, pool, serverPath, backupDir, "hunter2", 4)
	lib := registerLibrary(t, library.NewManager(pool, testLogger(), audit.New(pool, testLogger()), crypto.NewKeys("")), filepath.Join(t.TempDir(), "charlie"))

	rec, err := m.Run(context.Background())
	if err != nil {
		t.Fatalf("backup run: %v", err)
	}
	if !rec.Encrypted {
		t.Fatal("backup not marked encrypted")
	}

	// header.json holds the salt in plaintext.
	hs, err := os.ReadFile(filepath.Join(rec.Destination, "header.json"))
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	if !strings.Contains(string(hs), `"salt"`) {
		t.Fatalf("header.json missing salt: %s", hs)
	}

	// The stored media payload is not the raw plaintext.
	rel := filepath.Join("libraries", lib.ID, "files", "holiday", "beach.jpg")
	orig, _ := os.ReadFile(filepath.Join(lib.Root, "holiday", "beach.jpg"))
	raw, _ := os.ReadFile(filepath.Join(rec.Destination, rel))
	if bytes.Equal(raw, orig) {
		t.Fatal("encrypted backup stored plaintext bytes")
	}

	// Restore/verify without the passphrase are refused.
	wrong := newManager(t, pool, serverPath, backupDir, "", 4)
	if _, err := wrong.Restore(context.Background(), rec.ID, t.TempDir()); err == nil {
		t.Fatal("restore without passphrase should have failed")
	}
	if _, err := wrong.Verify(context.Background(), rec.ID); err == nil {
		t.Fatal("verify without passphrase should have failed")
	}

	// Verify and restore succeed with the correct passphrase.
	if _, err := m.Verify(context.Background(), rec.ID); err != nil {
		t.Fatalf("verify with passphrase: %v", err)
	}
	dst := t.TempDir()
	if _, err := m.Restore(context.Background(), rec.ID, dst); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, rel))
	if err != nil {
		t.Fatalf("restored media: %v", err)
	}
	if !bytes.Equal(got, orig) {
		t.Fatal("restored encrypted media differs from source")
	}
}

// TestBackupAEADCodecRoundTrip is the Phase-14 acceptance test: encrypted
// backups are sealed with the shared AEAD kernel (bodies identifiable via
// crypto.IsSealed) and verify/restore round-trip byte-identically.
func TestBackupAEADCodecRoundTrip(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	backupDir := filepath.Join(t.TempDir(), "backups")

	m := newManager(t, pool, serverPath, backupDir, "aead-pass", 4)
	lib := registerLibrary(t, library.NewManager(pool, testLogger(), audit.New(pool, testLogger()), crypto.NewKeys("")), filepath.Join(t.TempDir(), "hotel"))

	rec, err := m.Run(context.Background())
	if err != nil {
		t.Fatalf("backup run: %v", err)
	}
	if !rec.Encrypted {
		t.Fatal("backup not marked encrypted")
	}
	if rec.VerifyErrors != 0 {
		t.Fatalf("fresh backup already has verify errors: %d", rec.VerifyErrors)
	}

	rel := filepath.Join("libraries", lib.ID, "files", "holiday", "beach.jpg")
	orig, _ := os.ReadFile(filepath.Join(lib.Root, "holiday", "beach.jpg"))

	// Every stored payload body is an AEAD-sealed blob, and no plaintext leaks.
	raw, err := os.ReadFile(filepath.Join(rec.Destination, rel))
	if err != nil {
		t.Fatalf("read stored media payload: %v", err)
	}
	if bytes.Equal(raw, orig) {
		t.Fatal("encrypted backup stored plaintext bytes")
	}
	if !crypto.IsSealed(raw[headerSize:]) {
		t.Fatal("stored media payload body is not an AEAD-sealed blob")
	}
	man, err := os.ReadFile(filepath.Join(rec.Destination, manifestFilename))
	if err != nil {
		t.Fatalf("read stored manifest: %v", err)
	}
	if !crypto.IsSealed(man[headerSize:]) {
		t.Fatal("stored manifest body is not an AEAD-sealed blob")
	}

	if _, err := m.Verify(context.Background(), rec.ID); err != nil {
		t.Fatalf("verify: %v", err)
	}
	dst := t.TempDir()
	if _, err := m.Restore(context.Background(), rec.ID, dst); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, rel))
	if err != nil {
		t.Fatalf("restored media: %v", err)
	}
	if !bytes.Equal(got, orig) {
		t.Fatal("restored AEAD-sealed media differs from source")
	}
}

// TestBackupAEADWrongPassphrase asserts that a wrong passphrase fails the
// authenticated manifest read (crypto.ErrInvalidPassphrase) instead of
// producing garbage, and that restore writes nothing.
func TestBackupAEADWrongPassphrase(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	backupDir := filepath.Join(t.TempDir(), "backups")

	m := newManager(t, pool, serverPath, backupDir, "aead-pass-a", 4)
	registerLibrary(t, library.NewManager(pool, testLogger(), audit.New(pool, testLogger()), crypto.NewKeys("")), filepath.Join(t.TempDir(), "india"))

	rec, err := m.Run(context.Background())
	if err != nil {
		t.Fatalf("backup run: %v", err)
	}

	wrong := newManager(t, pool, serverPath, backupDir, "aead-pass-b", 4)
	if _, err := wrong.Verify(context.Background(), rec.ID); !errors.Is(err, crypto.ErrInvalidPassphrase) {
		t.Fatalf("verify with wrong passphrase: %v, want ErrInvalidPassphrase", err)
	}
	dst := t.TempDir()
	if _, err := wrong.Restore(context.Background(), rec.ID, dst); !errors.Is(err, crypto.ErrInvalidPassphrase) {
		t.Fatalf("restore with wrong passphrase: %v, want ErrInvalidPassphrase", err)
	}
	if entries, err := os.ReadDir(dst); err != nil || len(entries) != 0 {
		t.Fatalf("restore with wrong passphrase wrote files: %v", entries)
	}
}

// TestBackupLegacyCTRStillReads pins the backward-compatibility guarantee: a
// backup whose every payload is written in the pre-Phase-14 CTR format (the
// Phase-10 codec) must still verify and restore byte-identically. The fixture
// is produced by re-encoding a fresh AEAD backup through the retained legacy
// writer so it is a faithful, pre-seeded legacy backup directory.
func TestBackupLegacyCTRStillReads(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	backupDir := filepath.Join(t.TempDir(), "backups")

	const pass = "legacy-pass"
	m := newManager(t, pool, serverPath, backupDir, pass, 4)
	lib := registerLibrary(t, library.NewManager(pool, testLogger(), audit.New(pool, testLogger()), crypto.NewKeys("")), filepath.Join(t.TempDir(), "juliet"))

	rec, err := m.Run(context.Background())
	if err != nil {
		t.Fatalf("backup run: %v", err)
	}

	meta, err := readHeader(rec.Destination)
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	salt, err := hex.DecodeString(meta.Salt)
	if err != nil {
		t.Fatalf("decode salt: %v", err)
	}
	key := deriveKey(pass, salt)
	mf, err := readManifest(rec.Destination, key)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	// Re-encode every stored payload in place with the Phase-10 legacy codec.
	convert := func(rel string, compress bool) {
		t.Helper()
		src := filepath.Join(rec.Destination, filepath.FromSlash(rel))
		f, err := os.Open(src)
		if err != nil {
			t.Fatalf("open %s: %v", rel, err)
		}
		pr, err := openPayload(f, key)
		if err != nil {
			_ = f.Close()
			t.Fatalf("open %s: %v", rel, err)
		}
		plain, err := io.ReadAll(pr)
		ferr := pr.Err()
		if cerr := f.Close(); cerr != nil && ferr == nil {
			ferr = cerr
		}
		if err != nil || ferr != nil {
			t.Fatalf("read %s: %v / %v", rel, err, ferr)
		}
		out, err := os.OpenFile(src, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			t.Fatalf("reopen %s: %v", rel, err)
		}
		if err := writeLegacyCTRPayload(out, key, compress, plain); err != nil {
			_ = out.Close()
			t.Fatalf("write legacy %s: %v", rel, err)
		}
		if err := out.Close(); err != nil {
			t.Fatalf("close %s: %v", rel, err)
		}
	}
	entries := []Entry{*mf.ServerDB}
	for _, lb := range mf.Libraries {
		if lb.DB != nil {
			entries = append(entries, *lb.DB)
		}
		if lb.Identity != nil {
			entries = append(entries, *lb.Identity)
		}
		entries = append(entries, lb.Files...)
	}
	for _, e := range entries {
		convert(e.Logical, e.Compressed)
	}
	convert(manifestFilename, true)

	raw, err := os.ReadFile(filepath.Join(rec.Destination, filepath.FromSlash(mf.Libraries[0].Identity.Logical)))
	if err != nil {
		t.Fatalf("read converted identity payload: %v", err)
	}
	if crypto.IsSealed(raw[headerSize:]) {
		t.Fatal("converted fixture must not look AEAD-sealed")
	}

	// The pre-Phase-14 backup must still verify and restore with the passphrase.
	m2 := newManager(t, pool, serverPath, backupDir, pass, 4)
	if _, err := m2.Verify(context.Background(), rec.ID); err != nil {
		t.Fatalf("verify legacy backup: %v", err)
	}
	dst := t.TempDir()
	if _, err := m2.Restore(context.Background(), rec.ID, dst); err != nil {
		t.Fatalf("restore legacy backup: %v", err)
	}
	orig, _ := os.ReadFile(filepath.Join(lib.Root, "holiday", "beach.jpg"))
	rel := filepath.Join("libraries", lib.ID, "files", "holiday", "beach.jpg")
	got, err := os.ReadFile(filepath.Join(dst, rel))
	if err != nil {
		t.Fatalf("restored media: %v", err)
	}
	if !bytes.Equal(got, orig) {
		t.Fatal("restored legacy-format media differs from source")
	}
}

// TestBackupPlainRestore exercises restore on an unencrypted backup.
func TestBackupPlainRestore(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	backupDir := filepath.Join(t.TempDir(), "backups")

	m := newManager(t, pool, serverPath, backupDir, "", 4)
	lib := registerLibrary(t, library.NewManager(pool, testLogger(), audit.New(pool, testLogger()), crypto.NewKeys("")), filepath.Join(t.TempDir(), "delta"))

	rec, err := m.Run(context.Background())
	if err != nil {
		t.Fatalf("backup run: %v", err)
	}
	dst := t.TempDir()
	if _, err := m.Restore(context.Background(), rec.ID, dst); err != nil {
		t.Fatalf("restore: %v", err)
	}
	orig, _ := os.ReadFile(filepath.Join(lib.Root, "holiday", "beach.jpg"))
	rel := filepath.Join("libraries", lib.ID, "files", "holiday", "beach.jpg")
	got, err := os.ReadFile(filepath.Join(dst, rel))
	if err != nil {
		t.Fatalf("restored media: %v", err)
	}
	if !bytes.Equal(got, orig) {
		t.Fatal("restored plain media differs from source")
	}
	if _, err := os.Stat(filepath.Join(dst, "server", "cairn.db")); err != nil {
		t.Fatalf("restored server db missing: %v", err)
	}
}

func TestRestoreRejectsUnsafeManifestPaths(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	backupDir := filepath.Join(t.TempDir(), "backups")

	m := newManager(t, pool, serverPath, backupDir, "", 4)
	registerLibrary(t, library.NewManager(pool, testLogger(), audit.New(pool, testLogger()), crypto.NewKeys("")), filepath.Join(t.TempDir(), "gamma"))

	rec, err := m.Run(context.Background())
	if err != nil {
		t.Fatalf("backup run: %v", err)
	}

	// Tamper with the manifest so a media entry claims a path that would
	// escape the restore destination.
	mf, err := readManifest(rec.Destination, nil)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	for i := range mf.Libraries[0].Files {
		mf.Libraries[0].Files[i].Logical = "../evil-escaped.jpg"
	}
	if err := writeManifest(rec.Destination, mf, false, nil); err != nil {
		t.Fatalf("tamper manifest: %v", err)
	}

	dst := t.TempDir()
	if _, err := m.Restore(context.Background(), rec.ID, dst); err == nil {
		t.Fatal("restore with traversal manifest entry succeeded, want error")
	}
	if _, err := os.Stat(filepath.Join(dst, "evil-escaped.jpg")); !os.IsNotExist(err) {
		t.Fatal("restore wrote outside the destination directory")
	}
}

func TestBackupVerifyDetectsCorruption(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	backupDir := filepath.Join(t.TempDir(), "backups")

	m := newManager(t, pool, serverPath, backupDir, "", 4)
	registerLibrary(t, library.NewManager(pool, testLogger(), audit.New(pool, testLogger()), crypto.NewKeys("")), filepath.Join(t.TempDir(), "echo"))

	rec, err := m.Run(context.Background())
	if err != nil {
		t.Fatalf("backup run: %v", err)
	}
	if rec.VerifyErrors != 0 {
		t.Fatalf("fresh backup already has verify errors: %d", rec.VerifyErrors)
	}

	// Corrupt the stored identity-file payload (chosen via the manifest).
	mf, err := readManifest(rec.Destination, nil)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	target := filepath.Join(rec.Destination, filepath.FromSlash(mf.Libraries[0].Identity.Logical))
	if err := os.WriteFile(target, []byte("tampered-payload"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	if _, err := m.Verify(context.Background(), rec.ID); err != nil {
		t.Fatalf("verify after tamper: %v", err)
	}
	after, err := m.Get(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.VerifyStatus != VerifyFail || after.VerifyErrors == 0 {
		t.Fatalf("tampered backup verify = %s errors=%d, want failed with errors",
			after.VerifyStatus, after.VerifyErrors)
	}
}

func TestBackupRetentionPrunes(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	backupDir := filepath.Join(t.TempDir(), "nested", "backups")

	m := newManager(t, pool, serverPath, backupDir, "", 1)
	registerLibrary(t, library.NewManager(pool, testLogger(), audit.New(pool, testLogger()), crypto.NewKeys("")), filepath.Join(t.TempDir(), "foxtrot"))

	first, err := m.Run(context.Background())
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := m.Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}

	if _, err := os.Stat(first.Destination); err == nil {
		t.Error("old backup directory was not pruned")
	}
	recs, _ := m.List(context.Background(), 10)
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2 (pruned record retained)", len(recs))
	}
	foundPruned := false
	for _, r := range recs {
		if r.Status == StatusPruned && r.ID == first.ID {
			foundPruned = true
		}
	}
	if !foundPruned {
		t.Error("old record not marked pruned")
	}
}

// TestBackupJobFailure ensures a failed library backup is recorded as failed
// rather than silently dropped.
func TestBackupJobFailure(t *testing.T) {
	pool, serverPath := tempServerDB(t)
	backupDir := filepath.Join(t.TempDir(), "backups")

	m := newManager(t, pool, serverPath, backupDir, "", 4)
	_ = registerLibrary(t, library.NewManager(pool, testLogger(), audit.New(pool, testLogger()), crypto.NewKeys("")), filepath.Join(t.TempDir(), "golf"))

	// Break the server snapshot by pointing at a non-existent cairn.db.
	broken := newManager(t, pool, filepath.Join(t.TempDir(), "missing.db"), backupDir, "", 4)
	rec, err := broken.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rec.Status != StatusFailed || rec.ErrorMsg == "" {
		t.Fatalf("status = %s error = %q, want failed with message", rec.Status, rec.ErrorMsg)
	}

	// A later run with a valid server path succeeds.
	good, err := m.Run(context.Background())
	if err != nil {
		t.Fatalf("restored run: %v", err)
	}
	if good.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed", good.Status)
	}
}

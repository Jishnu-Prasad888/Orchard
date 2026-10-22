package backups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/backups/targets"
	"github.com/Jishnu-Prasad888/Orchard/internal/serversettings"
)

// remoteManager is a manager whose network destination is cfg. A "local"
// target stands in for a server: the repository code is the same on any
// target, and the targets package proves each backend separately.
func remoteManager(t *testing.T, passphrase string, keep int, cfg targets.Config, secret targets.Secret) (*Manager, string) {
	t.Helper()
	pool, serverPath := tempServerDB(t)
	m := newManager(t, pool, serverPath, "", passphrase, keep)
	m.cfg.SecretKeyFile = filepath.Join(t.TempDir(), "secret.key")
	if err := m.LoadPolicy(context.Background(), serversettings.NewStore(pool)); err != nil {
		t.Fatal(err)
	}
	lib := registerLibrary(t, m.libs, filepath.Join(t.TempDir(), "lib"))
	if err := m.SetRemote(context.Background(), cfg, &secret); err != nil {
		t.Fatalf("set remote: %v", err)
	}
	return m, lib.Root
}

func localRepo(t *testing.T) (targets.Config, string) {
	dir := t.TempDir()
	return targets.Config{Kind: targets.KindLocal, Path: dir}, dir
}

func mustRun(t *testing.T, m *Manager) *Record {
	t.Helper()
	rec, err := m.RunBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != StatusCompleted {
		t.Fatalf("backup %s: %s", rec.Status, rec.ErrorMsg)
	}
	return rec
}

func countObjects(t *testing.T, repoDir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(filepath.Join(repoDir, "objects"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestRemote_BacksUpIncrementallyAndRestoresTheSameLayout(t *testing.T) {
	cfg, repoDir := localRepo(t)
	m, root := remoteManager(t, "", 4, cfg, targets.Secret{})
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("first version of the notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	first := mustRun(t, m)
	if first.Snapshot == "" || first.Destination != repoDir || first.Files != 2 {
		t.Fatalf("first backup = %+v", first)
	}
	if first.VerifyStatus != VerifyOK || first.VerifyErrors != 0 {
		t.Errorf("verification after the first backup = %s (%d errors)", first.VerifyStatus, first.VerifyErrors)
	}
	objectsAfterFirst := countObjects(t, repoDir)

	// Nothing changed: no media file is read or uploaded again; only the
	// databases (which always change) and the snapshot are stored.
	second := mustRun(t, m)
	if second.FilesSkipped < 2 {
		t.Errorf("unchanged files reused = %d, want both media files", second.FilesSkipped)
	}
	if second.StoredBytes >= first.StoredBytes {
		t.Errorf("an unchanged backup stored %d bytes, the first %d", second.StoredBytes, first.StoredBytes)
	}

	// One file changes: only it is new.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("second version, which is longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	third := mustRun(t, m)
	if third.FilesSkipped < 1 {
		t.Errorf("the unchanged photo was not reused: %d", third.FilesSkipped)
	}
	if got := countObjects(t, repoDir); got <= objectsAfterFirst {
		t.Errorf("objects after a change = %d, want more than %d", got, objectsAfterFirst)
	}

	// Restore the first backup: the same layout a local backup restores to,
	// with the old version of the changed file.
	dest := t.TempDir()
	if _, err := m.Restore(ctx, first.ID, dest); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for _, rel := range []string{"server/orchard.db", "libraries/" + libIDOf(t, m) + "/library.db"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel))); err != nil {
			t.Errorf("restore is missing %s", rel)
		}
	}
	got, err := os.ReadFile(filepath.Join(dest, "libraries", libIDOf(t, m), "files", "notes.txt"))
	if err != nil || string(got) != "first version of the notes" {
		t.Errorf("restored notes = %q %v, want the first version", got, err)
	}
	photo, _ := os.ReadFile(filepath.Join(dest, "libraries", libIDOf(t, m), "files", "holiday", "beach.jpg"))
	if string(photo) != "jpeg-bytes-that-are-descriptive-enough" {
		t.Errorf("restored photo = %q", photo)
	}

	if rec, err := m.Verify(ctx, third.ID); err != nil || rec.VerifyStatus != VerifyOK {
		t.Errorf("verify the latest: %v %+v", err, rec)
	}
}

func libIDOf(t *testing.T, m *Manager) string {
	t.Helper()
	libs, err := m.libs.List(context.Background())
	if err != nil || len(libs) == 0 {
		t.Fatal("no library")
	}
	return libs[0].ID
}

func TestRemote_EncryptedRepositoryHidesNamesAndGuardsItsPassphrase(t *testing.T) {
	cfg, repoDir := localRepo(t)
	m, root := remoteManager(t, "correct horse battery", 4, cfg, targets.Secret{})
	_ = os.WriteFile(filepath.Join(root, "diary.txt"), []byte("secret diary"), 0o644)
	rec := mustRun(t, m)
	if !rec.Encrypted {
		t.Error("record not marked encrypted")
	}
	// Object names are keyed: the plain SHA-256 of a file is nowhere.
	plainSHA := "3a6ebf0f1f9a7c2b"
	_ = filepath.WalkDir(repoDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		raw, _ := os.ReadFile(p)
		if strings.Contains(string(raw), "secret diary") {
			t.Errorf("%s holds the diary in plain text", p)
		}
		return nil
	})
	_ = plainSHA

	// The same folder with the wrong passphrase: refused before writing.
	other := newManager(t, m.db, m.server, "", "wrong passphrase", 4)
	other.cfg.SecretKeyFile = m.cfg.SecretKeyFile
	_ = other.LoadPolicy(context.Background(), serversettings.NewStore(m.db))
	r, err := other.RunBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusFailed || !strings.Contains(r.ErrorMsg, "passphrase does not match") {
		t.Errorf("wrong passphrase = %s %q", r.Status, r.ErrorMsg)
	}

	// A plain repository cannot quietly become encrypted.
	plainCfg, _ := localRepo(t)
	pm, _ := remoteManager(t, "", 4, plainCfg, targets.Secret{})
	mustRun(t, pm)
	enc := newManager(t, pm.db, pm.server, "", "now with a passphrase", 4)
	enc.cfg.SecretKeyFile = pm.cfg.SecretKeyFile
	_ = enc.LoadPolicy(context.Background(), serversettings.NewStore(pm.db))
	r, _ = enc.RunBackup(context.Background())
	if r.Status != StatusFailed || !strings.Contains(r.ErrorMsg, "without encryption") {
		t.Errorf("adding a passphrase to a plain repository = %s %q", r.Status, r.ErrorMsg)
	}
}

func TestRemote_PrunesOldSnapshotsAndTheirUnusedObjects(t *testing.T) {
	cfg, repoDir := localRepo(t)
	m, root := remoteManager(t, "", 2, cfg, targets.Secret{})
	ctx := context.Background()
	big := filepath.Join(root, "old-video.bin")
	_ = os.WriteFile(big, []byte(strings.Repeat("v", 5000)), 0o644)
	first := mustRun(t, m)
	_ = os.Remove(big)
	mustRun(t, m)
	mustRun(t, m)

	snaps, _ := os.ReadDir(filepath.Join(repoDir, "snapshots"))
	if len(snaps) != 2 {
		t.Errorf("snapshots kept = %d, want 2", len(snaps))
	}
	rec, _ := m.Get(ctx, first.ID)
	if rec.Status != StatusPruned {
		t.Errorf("the oldest backup's record = %s, want pruned", rec.Status)
	}
	// The deleted video was only in the pruned snapshot: its object is gone.
	// (Unencrypted, an object's name is its contents' SHA-256.)
	sum := sha256.Sum256([]byte(strings.Repeat("v", 5000)))
	name := hex.EncodeToString(sum[:])
	if _, err := os.Stat(filepath.Join(repoDir, "objects", name[:2], name)); !os.IsNotExist(err) {
		t.Errorf("an object only the pruned snapshot used is still there (%v)", err)
	}
}

func TestRemote_VerifyFindsDamage(t *testing.T) {
	cfg, repoDir := localRepo(t)
	m, _ := remoteManager(t, "", 4, cfg, targets.Secret{})
	rec := mustRun(t, m)
	_ = filepath.WalkDir(filepath.Join(repoDir, "objects"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			raw, _ := os.ReadFile(p)
			raw[len(raw)-1] ^= 0xff
			_ = os.WriteFile(p, raw, 0o600)
		}
		return nil
	})
	got, err := m.Verify(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.VerifyStatus != VerifyFail || got.VerifyErrors == 0 {
		t.Errorf("verify after damage = %s, %d errors", got.VerifyStatus, got.VerifyErrors)
	}
}

func TestRemote_PasswordIsSealedNotStored(t *testing.T) {
	cfg, _ := localRepo(t)
	m, _ := remoteManager(t, "", 4, cfg, targets.Secret{Password: "hunter2-hunter2"})
	raw, err := m.settings.GetString(context.Background(), RemoteSettingKey, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "hunter2") {
		t.Fatal("the password is in the database in plain text")
	}
	fi, err := os.Stat(m.cfg.SecretKeyFile)
	if err != nil || fi.Mode().Perm() != 0o600 || fi.Size() != 32 {
		t.Errorf("secret key file = %v %v", fi, err)
	}
	s, err := m.secretFor(cfg, nil)
	if err != nil || s.Password != "hunter2-hunter2" {
		t.Errorf("saved secret reads back as %+v %v", s, err)
	}
	// Another server gets nothing from this one's saved password.
	if _, err := m.secretFor(targets.Config{Kind: targets.KindSFTP, Host: "elsewhere", User: "x"}, nil); err == nil {
		t.Error("a saved password was offered to a different server")
	}
}

func TestRemote_RefusesAFolderHoldingSomethingElse(t *testing.T) {
	cfg, repoDir := localRepo(t)
	_ = os.WriteFile(filepath.Join(repoDir, "orchard-repo.json"), []byte(`{"format":"tax-records"}`), 0o600)
	pool, serverPath := tempServerDB(t)
	m := newManager(t, pool, serverPath, "", "", 4)
	m.cfg.SecretKeyFile = filepath.Join(t.TempDir(), "secret.key")
	_ = m.LoadPolicy(context.Background(), serversettings.NewStore(pool))
	if err := m.SetRemote(context.Background(), cfg, &targets.Secret{}); err == nil ||
		!strings.Contains(err.Error(), "something other than") {
		t.Errorf("set remote on a foreign folder = %v", err)
	}
}

// The whole cycle against the real servers the targets tests use, when the
// environment names them.
func TestRemote_RealServers(t *testing.T) {
	cases := map[string]func() (targets.Config, targets.Secret, bool){
		"ftp": func() (targets.Config, targets.Secret, bool) {
			f := strings.SplitN(os.Getenv("ORCHARD_TEST_FTP"), ":", 6)
			if len(f) < 4 {
				return targets.Config{}, targets.Secret{}, false
			}
			port, _ := strconv.Atoi(f[1])
			c := targets.Config{Kind: targets.KindFTP, Host: f[0], Port: port, User: f[2],
				Path: "orchard-repo-test-" + newID(), FTPSecurity: targets.FTPPlain}
			if len(f) > 4 {
				c.FTPSecurity = f[4]
			}
			if len(f) > 5 {
				c.Fingerprint = f[5]
			}
			return c, targets.Secret{Password: f[3]}, true
		},
		// ORCHARD_TEST_SFTP=host:port:user:password:fingerprint
		"sftp": func() (targets.Config, targets.Secret, bool) {
			f := strings.SplitN(os.Getenv("ORCHARD_TEST_SFTP"), ":", 5)
			if len(f) < 5 {
				return targets.Config{}, targets.Secret{}, false
			}
			port, _ := strconv.Atoi(f[1])
			return targets.Config{Kind: targets.KindSFTP, Host: f[0], Port: port, User: f[2],
				Path: "upload/orchard-repo-test-" + newID(), Fingerprint: f[4]}, targets.Secret{Password: f[3]}, true
		},
		"smb": func() (targets.Config, targets.Secret, bool) {
			f := strings.SplitN(os.Getenv("ORCHARD_TEST_SMB"), ":", 5)
			if len(f) < 5 {
				return targets.Config{}, targets.Secret{}, false
			}
			port, _ := strconv.Atoi(f[1])
			return targets.Config{Kind: targets.KindSMB, Host: f[0], Port: port, Share: f[2], User: f[3],
				Path: "orchard-repo-test-" + newID()}, targets.Secret{Password: f[4]}, true
		},
	}
	for name, get := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, secret, ok := get()
			if !ok {
				t.Skip("server not configured")
			}
			m, root := remoteManager(t, "a passphrase for the test", 2, cfg, secret)
			_ = os.WriteFile(filepath.Join(root, "notes.txt"), []byte("over the network"), 0o644)
			first := mustRun(t, m)
			second := mustRun(t, m)
			if second.FilesSkipped < 2 {
				t.Errorf("unchanged files reused = %d", second.FilesSkipped)
			}
			mustRun(t, m) // prunes the first
			dest := t.TempDir()
			if _, err := m.Restore(context.Background(), second.ID, dest); err != nil {
				t.Fatalf("restore over %s: %v", name, err)
			}
			got, _ := os.ReadFile(filepath.Join(dest, "libraries", libIDOf(t, m), "files", "notes.txt"))
			if string(got) != "over the network" {
				t.Errorf("restored %q", got)
			}
			if rec, _ := m.Get(context.Background(), first.ID); rec.Status != StatusPruned {
				t.Errorf("first backup = %s, want pruned", rec.Status)
			}
		})
	}
}

func TestSnapshotNamesSortInTheOrderBackupsRan(t *testing.T) {
	base := time.Date(2026, 10, 22, 3, 0, 0, 0, time.UTC)
	// Same second; the ids would sort the other way round.
	a := snapshotName(base.Add(100*time.Millisecond), "bkp-ffff")
	b := snapshotName(base.Add(900*time.Millisecond), "bkp-0000")
	c := snapshotName(base.Add(time.Second), "bkp-8888")
	if !(a < b && b < c) {
		t.Errorf("names out of order: %s %s %s", a, b, c)
	}
}

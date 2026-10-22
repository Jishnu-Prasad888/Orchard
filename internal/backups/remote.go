package backups

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/backups/targets"
	"github.com/Jishnu-Prasad888/Orchard/internal/crypto"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/sanitize"
)

// Settings keys for a network destination.
const (
	// RemoteSettingKey holds the server's settings and its sealed secret.
	RemoteSettingKey = "backups.remote"
	// DestinationSettingKey is "local" (a folder or drive) or "remote".
	DestinationSettingKey = "backups.destination"
)

// Destination kinds.
const (
	DestinationLocal  = "local"
	DestinationRemote = "remote"
)

// savedRemote is how a network destination is kept: its settings, and its
// password or key sealed with the server's secret key, so a copy of
// orchard.db — including the one inside every backup — does not give it
// away.
type savedRemote struct {
	Config targets.Config `json:"config"`
	Sealed string         `json:"sealed,omitempty"`
}

// secretKey reads the server's secret key, creating it on first use: 32
// random bytes in a file only the server's user can read.
func (m *Manager) secretKey() (*crypto.Keys, error) {
	if m.cfg.SecretKeyFile == "" {
		return nil, errors.New("no secret key file is configured")
	}
	raw, err := os.ReadFile(m.cfg.SecretKeyFile)
	if errors.Is(err, os.ErrNotExist) {
		raw = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, raw); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(m.cfg.SecretKeyFile), 0o700); err != nil {
			return nil, err
		}
		// O_EXCL: two starts racing must not each write a different key.
		f, err := os.OpenFile(m.cfg.SecretKeyFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return m.secretKey()
			}
			return nil, err
		}
		_, werr := f.Write(raw)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return nil, werr
		}
	} else if err != nil {
		return nil, err
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("secret key file %s is damaged", m.cfg.SecretKeyFile)
	}
	return crypto.NewKeysFromKey(raw), nil
}

func (m *Manager) sealSecret(s targets.Secret) (string, error) {
	keys, err := m.secretKey()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(keys.Seal(raw)), nil
}

func (m *Manager) openSecret(sealed string) (targets.Secret, error) {
	if sealed == "" {
		return targets.Secret{}, nil
	}
	keys, err := m.secretKey()
	if err != nil {
		return targets.Secret{}, err
	}
	blob, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return targets.Secret{}, err
	}
	raw, err := keys.Open(blob)
	if err != nil {
		return targets.Secret{}, errors.New("the saved password could not be read: " +
			"the server's secret key file has changed; enter the password again")
	}
	var s targets.Secret
	return s, json.Unmarshal(raw, &s)
}

// RemoteStatus is the network destination as the Backups page sees it.
type RemoteStatus struct {
	Config    targets.Config `json:"config"`
	HasSecret bool           `json:"has_secret"`
}

// Remote is the saved network destination, if any, and whether backups go
// there (rather than to the local folder).
func (m *Manager) Remote() (*RemoteStatus, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.remote == nil {
		return nil, false
	}
	return &RemoteStatus{Config: m.remote.Config, HasSecret: m.remote.Sealed != ""},
		m.destination == DestinationRemote
}

// TestRemote connects to a network destination and writes and removes a
// small file, without saving anything. A server whose identity is not yet
// confirmed returns *targets.UnknownServerError with its fingerprint. A nil
// secret means "the one already saved".
func (m *Manager) TestRemote(ctx context.Context, c targets.Config, s *targets.Secret) error {
	// Settings first: a mistake there is the thing to point out.
	if err := c.Validate(); err != nil {
		return err
	}
	secret, err := m.secretFor(c, s)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	t, err := targets.Open(ctx, c, secret)
	if err != nil {
		return err
	}
	defer func() { _ = t.Close() }()
	probe := ".orchard-write-test-" + newID()
	if err := t.Put(ctx, probe, strings.NewReader("orchard")); err != nil {
		return fmt.Errorf("signed in, but could not write there: %w", err)
	}
	if err := t.Remove(ctx, probe); err != nil {
		return fmt.Errorf("signed in and wrote a file, but could not remove it: %w", err)
	}
	// The folder must be empty or one of ours, never someone else's data.
	if _, err := openRepo(ctx, t, m.cfg.Passphrase); err != nil {
		return err
	}
	return nil
}

// secretFor is s, or the saved secret when s is nil and c is the same
// server and account as the saved destination.
func (m *Manager) secretFor(c targets.Config, s *targets.Secret) (targets.Secret, error) {
	if s != nil {
		return *s, nil
	}
	m.mu.RLock()
	saved := m.remote
	m.mu.RUnlock()
	if saved == nil || !sameServer(saved.Config, c) {
		return targets.Secret{}, errors.New("enter the password (or key) for this server")
	}
	return m.openSecret(saved.Sealed)
}

// sameServer is whether a and b sign in to the same account on the same
// server, so a saved password belongs to both.
func sameServer(a, b targets.Config) bool {
	return a.Kind == b.Kind && strings.EqualFold(a.Host, b.Host) && a.Port == b.Port &&
		a.User == b.User && a.Domain == b.Domain && strings.EqualFold(a.Share, b.Share)
}

// sameRepo is whether a and b are the same backup folder on the same server.
func sameRepo(a, b targets.Config) bool {
	return sameServer(a, b) && path.Clean("/"+a.Path) == path.Clean("/"+b.Path)
}

// SetRemote tests and saves a network destination and sends backups there.
// A nil secret keeps the saved one (same server only).
func (m *Manager) SetRemote(ctx context.Context, c targets.Config, s *targets.Secret) error {
	if err := m.TestRemote(ctx, c, s); err != nil {
		return err
	}
	secret, err := m.secretFor(c, s)
	if err != nil {
		return err
	}
	sealed, err := m.sealSecret(secret)
	if err != nil {
		return err
	}
	saved := &savedRemote{Config: c, Sealed: sealed}
	if err := m.saveSetting(ctx, RemoteSettingKey, saved); err != nil {
		return err
	}
	if err := m.settings.SetString(ctx, DestinationSettingKey, DestinationRemote); err != nil {
		return err
	}
	m.mu.Lock()
	m.remote = saved
	m.destination = DestinationRemote
	m.mu.Unlock()
	return nil
}

// UseLocal sends backups to the local folder again. The network settings
// are kept, so switching back needs no password.
func (m *Manager) UseLocal(ctx context.Context) error {
	if m.settings != nil {
		if err := m.settings.SetString(ctx, DestinationSettingKey, DestinationLocal); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.destination = DestinationLocal
	m.mu.Unlock()
	return nil
}

// loadRemote reads the saved network destination.
func (m *Manager) loadRemote(ctx context.Context) {
	if m.settings == nil {
		return
	}
	var saved savedRemote
	if raw, err := m.settings.GetString(ctx, RemoteSettingKey, ""); err == nil && raw != "" &&
		json.Unmarshal([]byte(raw), &saved) == nil && saved.Config.Validate() == nil {
		m.mu.Lock()
		m.remote = &saved
		m.mu.Unlock()
	}
	dest, _ := m.settings.GetString(ctx, DestinationSettingKey, DestinationLocal)
	m.mu.Lock()
	if dest == DestinationRemote && m.remote != nil {
		m.destination = DestinationRemote
	} else {
		m.destination = DestinationLocal
	}
	m.mu.Unlock()
}

// openRemote connects to the saved network destination's repository.
func (m *Manager) openRemote(ctx context.Context, want *targets.Config) (*repo, targets.Config, error) {
	m.mu.RLock()
	saved := m.remote
	m.mu.RUnlock()
	if saved == nil {
		return nil, targets.Config{}, errors.New("no network destination is set up")
	}
	if want != nil && !sameRepo(*want, saved.Config) {
		return nil, targets.Config{}, fmt.Errorf("this backup is on %s, which is no longer the network destination; "+
			"set it up again to restore from it", want.String())
	}
	secret, err := m.openSecret(saved.Sealed)
	if err != nil {
		return nil, targets.Config{}, err
	}
	t, err := targets.Open(ctx, saved.Config, secret)
	if err != nil {
		return nil, targets.Config{}, err
	}
	r, err := openRepo(ctx, t, m.cfg.Passphrase)
	if err != nil {
		_ = t.Close()
		return nil, targets.Config{}, err
	}
	return r, saved.Config, nil
}

// runRemoteBackup is runBackup for a network destination.
func (m *Manager) runRemoteBackup(ctx context.Context, rec *Record) (*Record, error) {
	r, cfg, err := m.openRemote(ctx, nil)
	if err != nil {
		m.fail(ctx, rec, err)
		return rec, nil
	}
	defer func() { _ = r.t.Close() }()
	rec.Destination = cfg.String()
	if raw, err := json.Marshal(cfg); err == nil {
		rec.Target = string(raw)
	}
	_ = m.store.Update(ctx, rec)

	// The previous snapshot: files unchanged since it are not read again.
	prev := map[string]Entry{}
	if names, err := r.listSnapshots(ctx); err == nil && len(names) > 0 {
		if mf, err := r.readSnapshot(ctx, names[len(names)-1]); err == nil {
			for _, e := range mf.entries() {
				prev[e.Logical] = e
			}
		}
	}

	staging := filepath.Join(filepath.Dir(m.server), "backup-staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		m.fail(ctx, rec, err)
		return rec, nil
	}
	defer func() { _ = os.RemoveAll(staging) }()

	mf := &Manifest{Format: repoFormat + "-snapshot", Version: 1, CreatedAt: rec.StartedAt, Encrypted: r.encrypted}
	var stored int64
	put := func(src, logical string, stable bool) (Entry, error) {
		if stable {
			if fi, err := os.Stat(src); err == nil {
				if pe, ok := prev[logical]; ok && pe.Object != "" &&
					pe.Size == fi.Size() && pe.MTime.Equal(fi.ModTime()) {
					m.skipped.Add(1)
					pe.Original = src
					return pe, nil
				}
			}
		}
		e, n, err := r.putFile(ctx, src, logical)
		stored += n
		return e, err
	}

	// Databases change while Orchard runs, so each is copied to staging
	// first and the copy is what gets hashed and uploaded.
	srvCopy := filepath.Join(staging, "orchard.db")
	if _, err := m.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		m.fail(ctx, rec, fmt.Errorf("server database: %w", err))
		return rec, nil
	}
	if err := copyPlain(m.server, srvCopy); err != nil {
		m.fail(ctx, rec, fmt.Errorf("server database: %w", err))
		return rec, nil
	}
	srv, err := put(srvCopy, path.Join("server", "orchard.db"), false)
	if err != nil {
		m.fail(ctx, rec, fmt.Errorf("server database: %w", err))
		return rec, nil
	}
	srv.Original = m.server
	mf.ServerDB = &srv
	rec.Bytes = srv.Size

	libs, err := m.libs.List(ctx)
	if err != nil {
		m.fail(ctx, rec, fmt.Errorf("list libraries: %w", err))
		return rec, nil
	}
	for _, lib := range libs {
		lb, err := m.remoteLibrary(ctx, lib, staging, put)
		if err != nil {
			m.fail(ctx, rec, fmt.Errorf("library %s: %w", lib.ID, err))
			return rec, nil
		}
		mf.Libraries = append(mf.Libraries, lb)
		rec.Libraries++
		rec.Files += len(lb.Files)
		for _, e := range lb.Files {
			rec.Bytes += e.Size
		}
		if lb.DB != nil {
			rec.Bytes += lb.DB.Size
		}
		if lb.Identity != nil {
			rec.Bytes += lb.Identity.Size
		}
	}

	name := snapshotName(rec.StartedAt, rec.ID)
	n, err := r.writeSnapshot(ctx, name, mf)
	if err != nil {
		m.fail(ctx, rec, fmt.Errorf("write snapshot: %w", err))
		return rec, nil
	}
	rec.Snapshot = name
	rec.StoredBytes = stored + n
	rec.Encrypted = r.encrypted
	rec.Status = StatusCompleted
	rec.FilesSkipped = int(m.skipped.Load())
	rec.FinishedAt = time.Now().UTC()
	if err := m.store.Update(ctx, rec); err != nil {
		return nil, err
	}

	m.verifyRepoSample(ctx, r, rec, mf)
	_ = m.store.Update(ctx, rec)

	removed, err := r.prune(ctx, m.cfg.Keep)
	if err != nil {
		m.logger.Warn("prune network backups", "error", err)
	}
	m.markPruned(ctx, removed)
	m.logger.Info("network backup completed", "backup_id", rec.ID, "destination", rec.Destination,
		"files", rec.Files, "stored_bytes", rec.StoredBytes, "reused", rec.FilesSkipped)
	return rec, nil
}

// remoteLibrary backs one library up into the repository.
func (m *Manager) remoteLibrary(
	ctx context.Context, lib library.Library, staging string,
	put func(src, logical string, stable bool) (Entry, error),
) (LibraryBackup, error) {
	orchardDir := filepath.Join(lib.Root, ".orchard")
	lb := LibraryBackup{ID: lib.ID, Name: lib.Name, Root: lib.Root}
	if dbPath := filepath.Join(orchardDir, "library.db"); fileExists(dbPath) {
		if db, release, err := librarydb.Acquire(orchardDir); err == nil {
			_, _ = db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
			release()
		}
		cp := filepath.Join(staging, lib.ID+".db")
		if err := copyPlain(dbPath, cp); err != nil {
			m.logger.Warn("library db backup skipped", "library_id", lib.ID, "error", err)
		} else if e, err := put(cp, path.Join("libraries", lib.ID, "library.db"), false); err == nil {
			e.Original = dbPath
			lb.DB = &e
		} else {
			return lb, err
		}
	}
	if idPath := filepath.Join(orchardDir, "library.json"); fileExists(idPath) {
		e, err := put(idPath, path.Join("libraries", lib.ID, "library.json"), true)
		if err != nil {
			return lb, err
		}
		lb.Identity = &e
	}
	err := filepath.WalkDir(lib.Root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, lib.Root), string(filepath.Separator))
		if rel == "" {
			return nil
		}
		if d.IsDir() {
			if rel == ".orchard" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		logical := path.Join("libraries", lib.ID, "files", filepath.ToSlash(rel))
		e, err := put(p, logical, true)
		if errors.Is(err, errChangedWhileWriting) {
			// Changed mid-upload: the next backup takes it.
			m.logger.Warn("file changed during backup; it will be in the next one", "file", p)
			return nil
		}
		if err != nil {
			return err
		}
		lb.Files = append(lb.Files, e)
		return nil
	})
	return lb, err
}

func (m *Manager) markPruned(ctx context.Context, snapshots []string) {
	if len(snapshots) == 0 {
		return
	}
	gone := map[string]bool{}
	for _, s := range snapshots {
		gone[s] = true
	}
	recs, err := m.store.List(ctx, 500)
	if err != nil {
		return
	}
	for _, rec := range recs {
		if rec.Snapshot != "" && gone[rec.Snapshot] && rec.Status == StatusCompleted {
			rec.Status = StatusPruned
			_ = m.store.Update(ctx, &rec)
		}
	}
}

// verifyRepoSample reads a sample of a snapshot's objects back.
func (m *Manager) verifyRepoSample(ctx context.Context, r *repo, rec *Record, mf *Manifest) {
	var checks []Entry
	for i, e := range mf.entries() {
		if i < 5 || i%27 == 0 {
			checks = append(checks, e)
		}
	}
	if len(checks) > 12 {
		checks = checks[:12]
	}
	rec.VerifyStatus, rec.VerifyChecked, rec.VerifyErrors = VerifyOK, 0, 0
	for _, e := range checks {
		if ctx.Err() != nil {
			return
		}
		rec.VerifyChecked++
		if err := m.checkObject(ctx, r, e); err != nil {
			rec.VerifyErrors++
			rec.VerifyStatus = VerifyFail
			m.logger.Error("network backup verification failed", "logical", e.Logical, "error", err)
		}
	}
}

func (m *Manager) checkObject(ctx context.Context, r *repo, e Entry) error {
	pr, closer, err := r.openObject(ctx, e.Object)
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	if _, err := io.Copy(io.Discard, pr); err != nil {
		return err
	}
	if err := pr.Err(); err != nil {
		return err
	}
	if pr.Header().SHA256Hex != e.SHA256 {
		return errors.New("contents do not match the snapshot")
	}
	return nil
}

// recordTarget is the network destination a record's backup is on.
func recordTarget(rec Record) (*targets.Config, error) {
	var c targets.Config
	if err := json.Unmarshal([]byte(rec.Target), &c); err != nil {
		return nil, errors.New("this backup does not say which server it is on")
	}
	return &c, nil
}

func (m *Manager) verifyRemote(ctx context.Context, rec Record) (*Record, error) {
	want, err := recordTarget(rec)
	if err != nil {
		return nil, err
	}
	r, _, err := m.openRemote(ctx, want)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.t.Close() }()
	mf, err := r.readSnapshot(ctx, rec.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	m.verifyRepoSample(ctx, r, &rec, mf)
	if err := m.store.Update(ctx, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (m *Manager) restoreRemote(ctx context.Context, rec Record, dest string) (*Record, error) {
	want, err := recordTarget(rec)
	if err != nil {
		return nil, err
	}
	r, _, err := m.openRemote(ctx, want)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.t.Close() }()
	mf, err := r.readSnapshot(ctx, rec.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return nil, err
	}
	count := 0
	for _, e := range mf.entries() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := m.restoreObject(ctx, r, dest, e); err != nil {
			return nil, fmt.Errorf("restore %s: %w", e.Logical, err)
		}
		count++
	}
	m.logger.Info("network backup restored", "backup_id", rec.ID, "destination", dest, "entries", count)
	return &rec, nil
}

// restoreObject writes one entry under dest, at the same place a local
// backup's restore puts it.
func (m *Manager) restoreObject(ctx context.Context, r *repo, dest string, e Entry) error {
	rel, err := sanitize.RelPath(e.Logical)
	if err != nil {
		return fmt.Errorf("unsafe snapshot path %q: %w", e.Logical, err)
	}
	dst := filepath.Join(dest, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	pr, closer, err := r.openObject(ctx, e.Object)
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, pr); err != nil {
		_ = out.Close()
		return err
	}
	if err := pr.Err(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, e.MTime, e.MTime)
}

func copyPlain(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

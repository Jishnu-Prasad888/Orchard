package backups

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/backups/targets"
	"github.com/Jishnu-Prasad888/Orchard/internal/crypto"
)

// A repository is how backups are kept on a server Orchard reaches over the
// network (SFTP, FTP, SMB). A folder there holds:
//
//	orchard-repo.json          format, encryption salt, and a passphrase check
//	objects/ab/abcdef…         each distinct file's contents, once
//	snapshots/<time>-<id>.json one manifest per backup, written last
//
// A local backup hard-links unchanged files from the previous one; there are
// no hard links over the network, so here each file's contents are stored
// once under a name derived from its SHA-256 and every snapshot refers to
// them. A file unchanged since the last snapshot (same size and modified
// time) is not even read again. Pruning deletes old snapshots, then every
// object no remaining snapshot refers to. Objects are written with the
// streamed payload codec, so nothing needs to seek — FTP included.

const (
	repoHeaderName = "orchard-repo.json"
	repoFormat     = "orchard-repo"
	repoVersion    = 1
	// repoCheckPlain is sealed into the header so a wrong passphrase is
	// found before anything is written.
	repoCheckPlain = "orchard backup repository"
)

type repoHeader struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Encrypted bool      `json:"encrypted"`
	Salt      string    `json:"salt,omitempty"`
	Check     string    `json:"check,omitempty"`
}

// repo is an open repository.
type repo struct {
	t         targets.Target
	encrypted bool
	key       []byte
	// objects caches which object names exist, one two-character folder at
	// a time, listed on first use.
	objects map[string]map[string]bool
}

// openRepo reads the repository's header, or starts a new repository when
// the folder has none. Encryption is fixed when a repository is made: an
// encrypted one needs its passphrase, and a passphrase cannot be added to a
// plain one later (choose a new folder for that).
func openRepo(ctx context.Context, t targets.Target, passphrase string) (*repo, error) {
	r := &repo{t: t, objects: map[string]map[string]bool{}}
	rc, err := t.Open(ctx, repoHeaderName)
	if errors.Is(err, targets.ErrNotExist) {
		return r, r.create(ctx, passphrase)
	}
	if err != nil {
		return nil, fmt.Errorf("read the backup repository: %w", err)
	}
	defer func() { _ = rc.Close() }()
	var h repoHeader
	if err := json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&h); err != nil || h.Format != repoFormat {
		return nil, errors.New("the folder holds something other than an Orchard backup repository")
	}
	if h.Version > repoVersion {
		return nil, errors.New("the backup repository was made by a newer Orchard")
	}
	r.encrypted = h.Encrypted
	if !h.Encrypted {
		if passphrase != "" {
			return nil, errors.New("this backup folder was set up without encryption; " +
				"choose a new folder to start encrypted backups")
		}
		return r, nil
	}
	if passphrase == "" {
		return nil, errors.New("this backup folder is encrypted; set ORCHARD_BACKUP_PASSPHRASE to use it")
	}
	salt, err := hex.DecodeString(h.Salt)
	if err != nil {
		return nil, errors.New("the backup repository's header is damaged")
	}
	r.key = deriveKey(passphrase, salt)
	sealed, err := base64.StdEncoding.DecodeString(h.Check)
	if err != nil {
		return nil, errors.New("the backup repository's header is damaged")
	}
	if plain, err := crypto.NewKeysFromKey(r.key).Open(sealed); err != nil || string(plain) != repoCheckPlain {
		return nil, errors.New("the backup passphrase does not match this backup folder")
	}
	return r, nil
}

func (r *repo) create(ctx context.Context, passphrase string) error {
	h := repoHeader{Format: repoFormat, Version: repoVersion, CreatedAt: time.Now().UTC()}
	if passphrase != "" {
		salt := newSalt()
		r.encrypted = true
		r.key = deriveKey(passphrase, salt)
		h.Encrypted = true
		h.Salt = hex.EncodeToString(salt)
		h.Check = base64.StdEncoding.EncodeToString(crypto.NewKeysFromKey(r.key).Seal([]byte(repoCheckPlain)))
	}
	raw, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return r.t.Put(ctx, repoHeaderName, bytes.NewReader(raw))
}

// objectName is where a file with this SHA-256 is stored. Encrypted, it is
// a keyed hash, so the server cannot tell which known files a backup holds.
func (r *repo) objectName(sha256Hex string) string {
	if !r.encrypted {
		return sha256Hex
	}
	mac := hmac.New(sha256.New, r.key)
	mac.Write([]byte("orchard-object:" + sha256Hex))
	return hex.EncodeToString(mac.Sum(nil))
}

func objectPath(name string) string { return "objects/" + name[:2] + "/" + name }

func (r *repo) hasObject(ctx context.Context, name string) (bool, error) {
	prefix := name[:2]
	set, ok := r.objects[prefix]
	if !ok {
		names, err := r.t.List(ctx, "objects/"+prefix)
		if err != nil {
			return false, err
		}
		set = make(map[string]bool, len(names))
		for _, n := range names {
			set[n] = true
		}
		r.objects[prefix] = set
	}
	return set[name], nil
}

// putFile stores the file at src unless its contents are stored already,
// and returns its entry. It reads the file once to hash it and, only when
// the contents are new, again to upload them.
func (r *repo) putFile(ctx context.Context, src, logical string) (Entry, int64, error) {
	f, err := os.Open(src)
	if err != nil {
		return Entry{}, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return Entry{}, 0, err
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	_ = f.Close()
	if err != nil {
		return Entry{}, 0, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	e := Entry{
		Logical: logical, Original: src, Size: n, MTime: fi.ModTime(), SHA256: sum,
		Compressed: shouldCompress(logical), Encrypted: r.encrypted, Object: r.objectName(sum),
	}
	have, err := r.hasObject(ctx, e.Object)
	if err != nil {
		return Entry{}, 0, err
	}
	if have {
		return e, 0, nil
	}
	f, err = os.Open(src)
	if err != nil {
		return Entry{}, 0, err
	}
	defer func() { _ = f.Close() }()
	stored, err := r.putStream(ctx, objectPath(e.Object), f, sum, n, e.Compressed)
	if err != nil {
		return Entry{}, 0, err
	}
	r.objects[e.Object[:2]][e.Object] = true
	return e, stored, nil
}

// countingWriter counts what passes through.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// putStream encodes src (sha256Hex, size) with the streamed codec straight
// into an upload of name, returning the bytes stored.
func (r *repo) putStream(ctx context.Context, name string, src io.Reader, sha256Hex string, size int64, compress bool) (int64, error) {
	pr, pw := io.Pipe()
	counter := &countingWriter{w: pw}
	go func() {
		sink, err := newStreamSink(counter, sha256Hex, size, compress, r.encrypted, r.key)
		if err == nil {
			_, err = io.Copy(sink, src)
		}
		if err == nil {
			_, err = sink.Finish()
		}
		_ = pw.CloseWithError(err)
	}()
	if err := r.t.Put(ctx, name, pr); err != nil {
		_ = pr.CloseWithError(err)
		return 0, err
	}
	return counter.n, nil
}

// openObject reads an object back as plaintext.
func (r *repo) openObject(ctx context.Context, name string) (*payloadReader, io.Closer, error) {
	rc, err := r.t.Open(ctx, objectPath(name))
	if err != nil {
		return nil, nil, err
	}
	pr, err := openPayload(rc, r.key)
	if err != nil {
		_ = rc.Close()
		return nil, nil, err
	}
	return pr, rc, nil
}

// snapshotName sorts by time, so the newest is last. The time is to the
// nanosecond, at a fixed width: two backups in the same second must still
// sort in the order they ran, or pruning would keep the wrong ones.
func snapshotName(at time.Time, id string) string {
	return at.UTC().Format("20060102T150405.000000000Z") + "-" + id + ".json"
}

func (r *repo) writeSnapshot(ctx context.Context, name string, mf *Manifest) (int64, error) {
	raw, err := json.Marshal(mf)
	if err != nil {
		return 0, err
	}
	sum := sha256.Sum256(raw)
	return r.putStream(ctx, "snapshots/"+name, bytes.NewReader(raw), hex.EncodeToString(sum[:]), int64(len(raw)), true)
}

func (r *repo) readSnapshot(ctx context.Context, name string) (*Manifest, error) {
	rc, err := r.t.Open(ctx, "snapshots/"+name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	pr, err := openPayload(rc, r.key)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(pr)
	if err != nil {
		return nil, err
	}
	if err := pr.Err(); err != nil {
		return nil, err
	}
	var mf Manifest
	if err := json.Unmarshal(raw, &mf); err != nil {
		return nil, err
	}
	return &mf, nil
}

func (r *repo) listSnapshots(ctx context.Context) ([]string, error) {
	names, err := r.t.List(ctx, "snapshots")
	if err != nil {
		return nil, err
	}
	out := names[:0]
	for _, n := range names {
		if strings.HasSuffix(n, ".json") && !strings.HasPrefix(n, ".") {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

// entries is every stored entry of a manifest.
func (mf *Manifest) entries() []Entry {
	var out []Entry
	if mf.ServerDB != nil {
		out = append(out, *mf.ServerDB)
	}
	for _, lb := range mf.Libraries {
		if lb.DB != nil {
			out = append(out, *lb.DB)
		}
		if lb.Identity != nil {
			out = append(out, *lb.Identity)
		}
		out = append(out, lb.Files...)
	}
	return out
}

// prune keeps the newest keep snapshots and deletes objects nothing refers
// to any more. It returns the names of the snapshots it removed.
func (r *repo) prune(ctx context.Context, keep int) ([]string, error) {
	names, err := r.listSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	var removed []string
	if len(names) > keep {
		for _, n := range names[:len(names)-keep] {
			if err := r.t.Remove(ctx, "snapshots/"+n); err != nil {
				return removed, err
			}
			removed = append(removed, n)
		}
		names = names[len(names)-keep:]
	}
	// Every object a remaining snapshot refers to. A snapshot that cannot be
	// read stops the clean-up: deleting objects it might need would be worse
	// than leaving some unused ones behind.
	live := map[string]bool{}
	for _, n := range names {
		mf, err := r.readSnapshot(ctx, n)
		if err != nil {
			return removed, fmt.Errorf("read snapshot %s: %w", n, err)
		}
		for _, e := range mf.entries() {
			live[e.Object] = true
		}
	}
	for i := 0; i < 256; i++ {
		prefix := fmt.Sprintf("%02x", i)
		objs, err := r.t.List(ctx, path.Join("objects", prefix))
		if err != nil {
			return removed, err
		}
		for _, o := range objs {
			if !live[o] && !strings.HasPrefix(o, ".") {
				if err := r.t.Remove(ctx, path.Join("objects", prefix, o)); err != nil {
					return removed, err
				}
			}
		}
	}
	return removed, nil
}

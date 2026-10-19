// Package uploads keeps resumable upload sessions on disk.
//
// A big upload over a flaky link (Wi-Fi, a phone, a tunnel to a Raspberry Pi)
// should survive the connection dropping, the browser retrying, and even the
// server restarting. So instead of one long request, a client opens a session,
// sends the file in chunks, and asks the server how much it already has
// whenever something goes wrong.
//
// Each session is a directory under <library>/.orchard/uploads/<id>/ holding a
// small session.json and the bytes received so far (data). The bytes go
// straight to the library's own disk — never to /tmp, which on a Raspberry Pi
// is often RAM-backed — and the offset is simply the data file's size, so
// there is no second record to keep in step with it. Living on the library's
// filesystem also makes the final step a rename rather than a copy.
package uploads

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultChunkSize is what the server suggests a client send per request:
	// small enough that a dropped connection costs little and one request
	// never outlives the server's timeouts, large enough that per-request and
	// fsync overhead stay negligible on an SD card.
	DefaultChunkSize int64 = 8 << 20
	// MaxChunkSize caps one request's body.
	MaxChunkSize int64 = 32 << 20

	// StaleAfter is how long an unfinished session survives without new data.
	StaleAfter = 24 * time.Hour
	// KeepCommitted is how long a finished session is remembered, so a client
	// whose "complete" response was lost can still learn the result.
	KeepCommitted = time.Hour

	// ReserveBytes is left free on the library's disk when accepting an
	// upload, so the databases and the OS never run out of room for it.
	ReserveBytes int64 = 64 << 20

	sessionFile = "session.json"
	dataFile    = "data"
	tempPrefix  = "form-"
)

var (
	// ErrNotFound means there is no such session (it expired, was cancelled,
	// or belongs to someone else).
	ErrNotFound = errors.New("upload session not found")
	// ErrCommitted means the session already finished; no more data fits.
	ErrCommitted = errors.New("upload already completed")
	// ErrTooMuchData means a chunk would run past the declared size.
	ErrTooMuchData = errors.New("chunk exceeds declared upload size")
	// ErrIncomplete means completion was asked for before every byte arrived.
	ErrIncomplete = errors.New("upload is incomplete")
)

// OffsetMismatchError reports that a chunk did not start where the server's
// copy ends — usually because an earlier attempt partly landed.
type OffsetMismatchError struct{ Current int64 }

func (e *OffsetMismatchError) Error() string {
	return fmt.Sprintf("upload offset mismatch: server has %d bytes", e.Current)
}

// Session is one resumable upload.
type Session struct {
	ID        string    `json:"id"`
	LibraryID string    `json:"library_id"`
	UserID    string    `json:"user_id"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	// FileID is set once the upload has been committed to the library.
	FileID string `json:"file_id,omitempty"`

	// Offset is how many bytes have arrived; it is read from disk, not stored.
	Offset int64 `json:"-"`
}

// Committed reports whether the session has become a library file.
func (s *Session) Committed() bool { return s.FileID != "" }

// Store holds the sessions of one library.
type Store struct {
	root string
}

// Dir returns the session directory for a library rooted at libraryRoot.
func Dir(libraryRoot string) string {
	return filepath.Join(libraryRoot, ".orchard", "uploads")
}

// Open returns the session store for a library. Nothing is created until a
// session is.
func Open(libraryRoot string) *Store {
	return &Store{root: Dir(libraryRoot)}
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (st *Store) dir(id string) string { return filepath.Join(st.root, id) }

// DataPath is where the session's received bytes live.
func (st *Store) DataPath(id string) string { return filepath.Join(st.dir(id), dataFile) }

// Create starts a session for a file of size bytes bound for path.
func (st *Store) Create(libraryID, userID, path string, size int64) (*Session, error) {
	id, err := newID()
	if err != nil {
		return nil, fmt.Errorf("generate upload id: %w", err)
	}
	if err := os.MkdirAll(st.dir(id), 0o755); err != nil {
		return nil, fmt.Errorf("create upload session: %w", err)
	}
	s := &Session{
		ID: id, LibraryID: libraryID, UserID: userID, Path: path, Size: size,
		CreatedAt: time.Now().UTC(),
	}
	if err := st.writeSession(s); err != nil {
		_ = os.RemoveAll(st.dir(id))
		return nil, err
	}
	f, err := os.OpenFile(st.DataPath(id), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		_ = os.RemoveAll(st.dir(id))
		return nil, fmt.Errorf("create upload data: %w", err)
	}
	_ = f.Close()
	return s, nil
}

// Get loads a session and how much of it has arrived.
func (st *Store) Get(id string) (*Session, error) {
	if !idPattern.MatchString(id) {
		return nil, ErrNotFound
	}
	raw, err := os.ReadFile(filepath.Join(st.dir(id), sessionFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read upload session: %w", err)
	}
	var s Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("decode upload session: %w", err)
	}
	if s.Committed() {
		s.Offset = s.Size
		return &s, nil
	}
	info, err := os.Stat(st.DataPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("stat upload data: %w", err)
	}
	s.Offset = info.Size()
	return &s, nil
}

// copyBufs recycles the copy buffer: a Pi has little memory to spare, and a
// fresh 256 KiB slice per chunk would churn the garbage collector.
var copyBufs = sync.Pool{New: func() any { b := make([]byte, 256<<10); return &b }}

// Append writes a chunk that starts at offset. Whatever arrives before src
// fails is kept and flushed to disk, so a retry resumes from the last byte
// actually received; the returned offset says where that is, even alongside
// an error.
func (st *Store) Append(s *Session, offset int64, src io.Reader) (int64, error) {
	if s.Committed() {
		return s.Size, ErrCommitted
	}
	if offset != s.Offset {
		return s.Offset, &OffsetMismatchError{Current: s.Offset}
	}
	f, err := os.OpenFile(st.DataPath(s.ID), os.O_WRONLY|os.O_APPEND, 0)
	if errors.Is(err, os.ErrNotExist) {
		return s.Offset, ErrNotFound
	}
	if err != nil {
		return s.Offset, fmt.Errorf("open upload data: %w", err)
	}
	remaining := s.Size - offset
	buf := copyBufs.Get().(*[]byte)
	n, copyErr := io.CopyBuffer(f, io.LimitReader(src, remaining), *buf)
	copyBufs.Put(buf)

	// Flush before acknowledging: if the Pi loses power, the offset reported
	// after reboot must never count bytes that were only in the page cache.
	syncErr := f.Sync()
	closeErr := f.Close()
	s.Offset = offset + n
	switch {
	case copyErr != nil:
		return s.Offset, copyErr
	case syncErr != nil:
		return s.Offset, fmt.Errorf("flush upload data: %w", syncErr)
	case closeErr != nil:
		return s.Offset, fmt.Errorf("close upload data: %w", closeErr)
	}
	if n == remaining {
		// The declared size is reached; anything more is a client bug.
		var one [1]byte
		if extra, _ := src.Read(one[:]); extra > 0 {
			return s.Offset, ErrTooMuchData
		}
	}
	return s.Offset, nil
}

// MarkCommitted records that the session became library file fileID and
// drops its data (which the commit has already moved away).
func (st *Store) MarkCommitted(s *Session, fileID string) error {
	s.FileID = fileID
	s.Offset = s.Size
	_ = os.Remove(st.DataPath(s.ID))
	return st.writeSession(s)
}

// Remove deletes a session and whatever it received.
func (st *Store) Remove(id string) error {
	if !idPattern.MatchString(id) {
		return ErrNotFound
	}
	return os.RemoveAll(st.dir(id))
}

// CreateTemp makes a scratch file next to the sessions, for the one-shot
// form upload, which must receive the body before it knows where it goes.
// Sweep removes any a crash leaves behind.
func (st *Store) CreateTemp() (*os.File, error) {
	if err := os.MkdirAll(st.root, 0o755); err != nil {
		return nil, fmt.Errorf("create upload directory: %w", err)
	}
	return os.CreateTemp(st.root, tempPrefix+"*")
}

// Sweep deletes sessions nobody has touched for StaleAfter, finished ones
// after KeepCommitted, and scratch files a crash left behind. It is cheap —
// one directory listing — so it runs whenever a new session starts.
func (st *Store) Sweep(now time.Time) {
	entries, err := os.ReadDir(st.root)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() {
			if strings.HasPrefix(name, tempPrefix) && olderThan(filepath.Join(st.root, name), now, StaleAfter) {
				_ = os.Remove(filepath.Join(st.root, name))
			}
			continue
		}
		if !idPattern.MatchString(name) {
			continue
		}
		s, err := st.Get(name)
		if err != nil {
			// Half-created or damaged; give it the same grace period.
			if olderThan(st.dir(name), now, StaleAfter) {
				_ = os.RemoveAll(st.dir(name))
			}
			continue
		}
		if s.Committed() {
			if olderThan(filepath.Join(st.dir(name), sessionFile), now, KeepCommitted) {
				_ = os.RemoveAll(st.dir(name))
			}
			continue
		}
		if olderThan(st.DataPath(name), now, StaleAfter) {
			_ = os.RemoveAll(st.dir(name))
		}
	}
}

func olderThan(path string, now time.Time, age time.Duration) bool {
	info, err := os.Stat(path)
	return err == nil && now.Sub(info.ModTime()) > age
}

func (st *Store) writeSession(s *Session) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode upload session: %w", err)
	}
	final := filepath.Join(st.dir(s.ID), sessionFile)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("write upload session: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write upload session: %w", err)
	}
	return nil
}

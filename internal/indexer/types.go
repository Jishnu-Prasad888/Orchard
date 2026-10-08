// Package indexer implements the incremental filesystem scanner for Cairn
// libraries.
//
// A scan walks the library root and reconciles each discovered file against
// the persisted index in the library-level SQLite database. It uses staged
// identity to minimise expensive operations (no content hashing unless the
// file has changed).
//
// Detection stages (in order):
//  1. Relative path — unchanged path, same size, same mtime → skip
//  2. Size + mtime mismatch → compute content hash, mark modified
//  3. New path with no matching hash in index → new file
//  4. New path whose hash matches an existing entry → moved file
//  5. After walk: entries not seen → missing (metadata retained)
package indexer

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Status describes the current state of an indexed file.
type Status string

const (
	// StatusPresent means the file was seen on the last scan.
	StatusPresent Status = "present"
	// StatusMissing means the file was not found on the last scan but its
	// metadata is retained. It may reappear (reconnected disk) or be
	// confirmed deleted.
	StatusMissing Status = "missing"
	// StatusDeleted means the file has been explicitly removed from the index.
	StatusDeleted Status = "deleted"
)

// IndexedFile is one entry in the persistent file index. All paths are
// relative to the library root so the library remains portable.
type IndexedFile struct {
	ID          string
	RelPath     string
	SizeBytes   int64
	ModTime     time.Time
	ContentHash string // SHA-256 hex; empty until hashed
	Status      Status
	FirstSeenAt time.Time
	LastSeenAt  time.Time
	IndexedAt   time.Time
}

// ChangeKind describes what happened to a file during a scan.
type ChangeKind string

const (
	ChangeNew       ChangeKind = "new"
	ChangeModified  ChangeKind = "modified"
	ChangeMoved     ChangeKind = "moved" // path changed, content same
	ChangeMissing   ChangeKind = "missing"
	ChangeUnchanged ChangeKind = "unchanged"
)

// FileChange records one detected change from a scan.
type FileChange struct {
	Kind    ChangeKind
	File    *IndexedFile
	OldPath string // populated for ChangeMoved
}

// Result summarises a completed scan.
type Result struct {
	LibraryID  string
	StartedAt  time.Time
	FinishedAt time.Time
	TotalFiles int
	NewFiles   int
	Modified   int
	Moved      int
	Missing    int
	Unchanged  int
	Errors     int
	Changes    []FileChange
}

// newID returns a new random identifier as 16 hex-encoded random bytes.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Package indexer implements the incremental filesystem scanner for Orchard
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

// maxScanDetailEntries bounds how many individual error/missing-path
// entries a Result keeps for reporting. The aggregate counts (Errors,
// Missing) are always exact; a library with a systemic problem (a whole
// subtree gone unreadable) must not make the detail lists — which outlive
// the scan, serialized onto the job record — grow unboundedly.
const maxScanDetailEntries = 50

// ScanError is one path that could not be processed during a scan, with a
// short human-readable reason.
type ScanError struct {
	Path   string
	Reason string
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
	// Skipped counts files on disk that aren't books (see media.IsBook) and
	// so weren't indexed; Removed counts rows for such files deleted from an
	// older index.
	Skipped int
	Removed int
	Changes []FileChange
	// ErrorDetails holds up to maxScanDetailEntries of the files that
	// failed this scan, newest first is not guaranteed — insertion order.
	// Errors is always the exact count even when this list is capped.
	ErrorDetails []ScanError
	// MissingPaths holds up to maxScanDetailEntries of the paths newly
	// marked missing this scan (present before, not seen now). Missing is
	// always the exact count even when this list is capped.
	MissingPaths []string
}

// addError records one failed path, incrementing the exact count and
// appending to the capped detail list.
func (r *Result) addError(path string, err error) {
	r.Errors++
	if len(r.ErrorDetails) < maxScanDetailEntries {
		r.ErrorDetails = append(r.ErrorDetails, ScanError{Path: path, Reason: err.Error()})
	}
}

// ScanSummary is the JSON-serializable gist of a Result: the aggregate
// counts plus the capped error/missing detail lists, with no *IndexedFile
// pointers — this is what gets persisted onto the completed index job's
// Result field (the full Result, with a Changes entry per ordinary file,
// is sized for one process's memory, not for storage or an HTTP response).
type ScanSummary struct {
	TotalFiles   int         `json:"total_files"`
	NewFiles     int         `json:"new_files"`
	Modified     int         `json:"modified"`
	Moved        int         `json:"moved"`
	Missing      int         `json:"missing"`
	Unchanged    int         `json:"unchanged"`
	Errors       int         `json:"errors"`
	ErrorDetails []ScanError `json:"error_details,omitempty"`
	MissingPaths []string    `json:"missing_paths,omitempty"`
	StartedAt    time.Time   `json:"started_at"`
	FinishedAt   time.Time   `json:"finished_at"`
}

// Summary extracts the persistable gist of a completed Result.
func (r *Result) Summary() ScanSummary {
	return ScanSummary{
		TotalFiles:   r.TotalFiles,
		NewFiles:     r.NewFiles,
		Modified:     r.Modified,
		Moved:        r.Moved,
		Missing:      r.Missing,
		Unchanged:    r.Unchanged,
		Errors:       r.Errors,
		ErrorDetails: r.ErrorDetails,
		MissingPaths: r.MissingPaths,
		StartedAt:    r.StartedAt,
		FinishedAt:   r.FinishedAt,
	}
}

// newID returns a new random identifier as 16 hex-encoded random bytes.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

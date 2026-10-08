package backups

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Status is the lifecycle state of a backup record.
type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusPruned    Status = "pruned"
	StatusRestoring Status = "restoring"
)

// VerifyStatus values.
const (
	VerifyOK   = "ok"
	VerifyFail = "failed"
)

// Record is one backup run, as persisted in the server-level backups table.
type Record struct {
	ID            string
	Status        Status
	Destination   string
	StartedAt     time.Time
	FinishedAt    time.Time
	ServerDB      string
	Libraries     int
	Files         int
	FilesSkipped  int
	Bytes         int64
	StoredBytes   int64
	Encrypted     bool
	SameDevice    bool
	VerifyStatus  string
	VerifyChecked int
	VerifyErrors  int
	ErrorMsg      string
	CreatedAt     time.Time
}

// Store provides row-level access to backup records.
type Store struct {
	db *sql.DB
}

// NewStore wraps the server database.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const recordCols = `id, status, destination, started_at, finished_at,
	server_db_path, libraries, files, files_skipped, bytes, stored_bytes,
	encrypted, same_device, verify_status, verify_checked, verify_errors,
	error_msg, created_at`

func (s *Store) Create(ctx context.Context, r *Record) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO backups (`+recordCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, string(r.Status), r.Destination,
		rf(r.StartedAt), rf(r.FinishedAt),
		r.ServerDB, r.Libraries, r.Files, r.FilesSkipped,
		r.Bytes, r.StoredBytes, b2i(r.Encrypted), b2i(r.SameDevice),
		r.VerifyStatus, r.VerifyChecked, r.VerifyErrors,
		r.ErrorMsg, rf(r.CreatedAt))
	return err
}

// Update overwrites all mutable fields for a running record.
func (s *Store) Update(ctx context.Context, r *Record) error {
	_, err := s.db.ExecContext(ctx, `UPDATE backups SET
		status=?, destination=?, started_at=?, finished_at=?,
		server_db_path=?, libraries=?, files=?, files_skipped=?, bytes=?,
		stored_bytes=?, encrypted=?, same_device=?, verify_status=?,
		verify_checked=?, verify_errors=?, error_msg=?
		WHERE id=?`,
		string(r.Status), r.Destination, rf(r.StartedAt), rf(r.FinishedAt),
		r.ServerDB, r.Libraries, r.Files, r.FilesSkipped, r.Bytes,
		r.StoredBytes, b2i(r.Encrypted), b2i(r.SameDevice),
		r.VerifyStatus, r.VerifyChecked, r.VerifyErrors,
		r.ErrorMsg, r.ID)
	return err
}

func (s *Store) Get(ctx context.Context, id string) (Record, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+recordCols+` FROM backups WHERE id=?`, id)
	r, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, fmt.Errorf("%w: backup %s", ErrNotFound, id)
	}
	return r, err
}

// List returns the most recent records, newest first.
func (s *Store) List(ctx context.Context, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+recordCols+` FROM backups ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type recordScanner interface {
	Scan(dest ...any) error
}

func scanRecord(sc recordScanner) (Record, error) {
	var r Record
	var (
		status, destination, serverDB string
		started, finished, created    string
		sameDev, encrypted            int
	)
	err := sc.Scan(&r.ID, &status, &destination, &started, &finished,
		&serverDB, &r.Libraries, &r.Files, &r.FilesSkipped,
		&r.Bytes, &r.StoredBytes, &encrypted, &sameDev,
		&r.VerifyStatus, &r.VerifyChecked, &r.VerifyErrors,
		&r.ErrorMsg, &created)
	if err != nil {
		return Record{}, err
	}
	r.Status = Status(status)
	r.Destination = destination
	r.ServerDB = serverDB
	r.Encrypted = encrypted != 0
	r.SameDevice = sameDev != 0
	r.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	r.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished)
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return r, nil
}

// ErrNotFound is returned when a backup record does not exist.
var ErrNotFound = errors.New("backup not found")

// rf formats a time as RFC3339Nano (zero time → empty string).
func rf(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

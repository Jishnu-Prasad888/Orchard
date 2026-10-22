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
	// Trigger is why it ran: TriggerManual, TriggerScheduled, TriggerCatchUp
	// or TriggerSize.
	Trigger string
	// Snapshot and Target say where a network backup is (repo.go); both
	// are empty for a local one.
	Snapshot string
	Target   string
}

// Why a backup ran.
const (
	TriggerManual    = "manual"
	TriggerScheduled = "scheduled"
	TriggerCatchUp   = "catch-up"
	TriggerSize      = "size"
)

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
	error_msg, created_at, trigger, snapshot, target`

func (s *Store) Create(ctx context.Context, r *Record) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO backups (`+recordCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, string(r.Status), r.Destination,
		rf(r.StartedAt), rf(r.FinishedAt),
		r.ServerDB, r.Libraries, r.Files, r.FilesSkipped,
		r.Bytes, r.StoredBytes, b2i(r.Encrypted), b2i(r.SameDevice),
		r.VerifyStatus, r.VerifyChecked, r.VerifyErrors,
		r.ErrorMsg, rf(r.CreatedAt), triggerOrManual(r.Trigger), r.Snapshot, r.Target)
	return err
}

func triggerOrManual(t string) string {
	if t == "" {
		return TriggerManual
	}
	return t
}

// LastStarted is when the most recent backup of any outcome started (zero
// if none has): what a schedule counts from, so a failed run is not retried
// in a tight loop but at the next slot.
func (s *Store) LastStarted(ctx context.Context) (time.Time, error) {
	return s.lastStart(ctx, `SELECT started_at FROM backups ORDER BY started_at DESC LIMIT 1`)
}

// LastCompletedStart is when the most recent successful backup started (zero
// if none has): everything changed after it is not in any backup yet.
func (s *Store) LastCompletedStart(ctx context.Context) (time.Time, error) {
	return s.lastStart(ctx, `SELECT started_at FROM backups WHERE status IN ('completed', 'pruned')
		ORDER BY started_at DESC LIMIT 1`)
}

func (s *Store) lastStart(ctx context.Context, query string) (time.Time, error) {
	var v string
	err := s.db.QueryRowContext(ctx, query).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t, nil
}

// Update overwrites all mutable fields for a running record.
func (s *Store) Update(ctx context.Context, r *Record) error {
	_, err := s.db.ExecContext(ctx, `UPDATE backups SET
		status=?, destination=?, started_at=?, finished_at=?,
		server_db_path=?, libraries=?, files=?, files_skipped=?, bytes=?,
		stored_bytes=?, encrypted=?, same_device=?, verify_status=?,
		verify_checked=?, verify_errors=?, error_msg=?, snapshot=?, target=?
		WHERE id=?`,
		string(r.Status), r.Destination, rf(r.StartedAt), rf(r.FinishedAt),
		r.ServerDB, r.Libraries, r.Files, r.FilesSkipped, r.Bytes,
		r.StoredBytes, b2i(r.Encrypted), b2i(r.SameDevice),
		r.VerifyStatus, r.VerifyChecked, r.VerifyErrors,
		r.ErrorMsg, r.Snapshot, r.Target, r.ID)
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
		&r.ErrorMsg, &created, &r.Trigger, &r.Snapshot, &r.Target)
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

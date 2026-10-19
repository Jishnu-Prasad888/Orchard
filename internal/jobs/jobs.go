// Package jobs implements Orchard's persistent background job system.
//
// Jobs are written to the per-library SQLite database so they survive process
// restarts. A job that was running when the process stopped is reset to
// "queued" on startup. Workers pull queued jobs, execute them, and record the
// outcome.
//
// Design goals:
//   - Restart survival: all state lives in SQLite, never only in memory
//   - Bounded concurrency: configurable worker count (default 1 for safety on
//     Raspberry Pi)
//   - Retries with exponential backoff
//   - Graceful shutdown: workers finish their current job then stop
package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Status values for a job record.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// KindIndex is the job kind for a library filesystem scan.
const KindIndex = "index"

// KindProcessMedia is the job kind for deriving data from a single indexed
// file (e.g. metadata extraction or cover generation) via a pluggable
// indexer.MediaProcessor. Nothing registers one yet.
const KindProcessMedia = "process_media"

// DefaultMaxAttempts is the default number of times a job is retried before
// being marked permanently failed.
const DefaultMaxAttempts = 3

// Job is one unit of background work stored in the database.
type Job struct {
	ID          string
	Kind        string
	Status      string
	Priority    int
	Payload     map[string]any
	Result      string
	ErrorMsg    string
	Attempt     int
	MaxAttempts int
	CreatedAt   time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
	NextRunAt   time.Time
}

// Handler is a function that executes a job. It should respect ctx
// cancellation and return an error on failure.
type Handler func(ctx context.Context, job *Job) error

// Queue manages the job table in a per-library database.
type Queue struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewQueue wraps a per-library database pool.
func NewQueue(db *sql.DB, logger *slog.Logger) *Queue {
	return &Queue{db: db, logger: logger}
}

// RawDB returns the underlying *sql.DB. Intended for tests only.
func (q *Queue) RawDB() *sql.DB { return q.db }

// Enqueue inserts a new job in the queued state. Returns the new job ID.
func (q *Queue) Enqueue(ctx context.Context, kind string, payload map[string]any) (string, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	id := newID()
	now := time.Now().UTC()
	const query = `
		INSERT INTO index_jobs
			(id, kind, status, priority, payload, attempt, max_attempts, created_at, next_run_at)
		VALUES (?, ?, 'queued', 0, ?, 0, ?, ?, ?)`
	if _, err := q.db.ExecContext(ctx, query, id, kind, string(raw),
		DefaultMaxAttempts, rfc3339(now), rfc3339(now)); err != nil {
		return "", fmt.Errorf("enqueue job: %w", err)
	}
	q.logger.Info("job enqueued", "job_id", id, "kind", kind)
	wake(q.db)
	return id, nil
}

// wakeups holds, per library database pool, the channel its worker sleeps
// on, so a new job starts at once instead of at the next poll.
var wakeups sync.Map // *sql.DB → chan struct{}

func wakeChan(db *sql.DB) chan struct{} {
	ch, _ := wakeups.LoadOrStore(db, make(chan struct{}, 1))
	return ch.(chan struct{})
}

func wake(db *sql.DB) {
	select {
	case wakeChan(db) <- struct{}{}:
	default: // a wakeup is already pending
	}
}

const (
	// keepCompleted and keepFailed bound how long finished jobs stay in the
	// table. Autoscan adds a job a minute per library, so without pruning
	// the table grows by half a million rows a year.
	keepCompleted = 24 * time.Hour
	keepFailed    = 30 * 24 * time.Hour
)

// Prune deletes finished jobs older than their retention, always keeping
// the most recent finished job of each kind (it carries the last scan's
// summary).
func (q *Queue) Prune(ctx context.Context, now time.Time) (int64, error) {
	res, err := q.db.ExecContext(ctx, `
		DELETE FROM index_jobs
		WHERE (
			(status = 'completed' AND COALESCE(NULLIF(finished_at, ''), created_at) < ?)
			OR (status IN ('failed', 'cancelled') AND COALESCE(NULLIF(finished_at, ''), created_at) < ?)
		)
		AND id NOT IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY kind ORDER BY finished_at DESC) AS rn
				FROM index_jobs
				WHERE status IN ('completed', 'failed') AND finished_at IS NOT NULL AND finished_at != ''
			) WHERE rn = 1
		)`,
		rfc3339(now.Add(-keepCompleted)), rfc3339(now.Add(-keepFailed)))
	if err != nil {
		return 0, fmt.Errorf("prune jobs: %w", err)
	}
	return res.RowsAffected()
}

// Get returns a single job by ID, or (nil, nil) when not found.
func (q *Queue) Get(ctx context.Context, id string) (*Job, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, kind, status, priority, payload, result, error_msg,
		        attempt, max_attempts, created_at, started_at, finished_at, next_run_at
		 FROM index_jobs WHERE id = ?`, id)
	return scanJob(row)
}

// ListByStatus returns all jobs with the given status, ordered by created_at.
func (q *Queue) ListByStatus(ctx context.Context, status string) ([]*Job, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, kind, status, priority, payload, result, error_msg,
		        attempt, max_attempts, created_at, started_at, finished_at, next_run_at
		 FROM index_jobs WHERE status = ? ORDER BY created_at`, status)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanJobs(rows)
}

// LastFinished returns the most recently finished (completed or failed) job
// of the given kind, or nil if none has finished yet.
func (q *Queue) LastFinished(ctx context.Context, kind string) (*Job, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, kind, status, priority, payload, result, error_msg,
		        attempt, max_attempts, created_at, started_at, finished_at, next_run_at
		 FROM index_jobs
		 WHERE kind = ? AND status IN ('completed', 'failed') AND finished_at IS NOT NULL AND finished_at != ''
		 ORDER BY finished_at DESC LIMIT 1`, kind)
	job, err := scanJob(row)
	if err != nil {
		return nil, fmt.Errorf("last finished job: %w", err)
	}
	return job, nil
}

// Cancel marks a queued or running job as cancelled.
func (q *Queue) Cancel(ctx context.Context, id string) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE index_jobs SET status = 'cancelled', finished_at = ?
		 WHERE id = ? AND status IN ('queued', 'running')`,
		rfc3339(time.Now().UTC()), id)
	return err
}

// ResetStuck resets any jobs that are stuck in the "running" state back to
// "queued". Called at startup to recover from a process crash.
func (q *Queue) ResetStuck(ctx context.Context) (int, error) {
	now := rfc3339(time.Now().UTC())
	res, err := q.db.ExecContext(ctx,
		`UPDATE index_jobs
		 SET status = 'queued', started_at = NULL, next_run_at = ?
		 WHERE status = 'running'`, now)
	if err != nil {
		return 0, fmt.Errorf("reset stuck jobs: %w", err)
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		q.logger.Info("reset stuck jobs", "count", n)
	}
	return int(n), nil
}

// Worker pulls jobs from the queue and executes them. It runs until the
// context is cancelled, then finishes its current job and returns.
//
// It works through queued jobs back to back, then sleeps until a job is
// enqueued (or pollInterval passes, which catches retries coming due). It
// used to run at most one job per poll, so a few hundred new books took
// many minutes to get covers.
type Worker struct {
	queue        *Queue
	handlers     map[string]Handler
	logger       *slog.Logger
	pollInterval time.Duration
	lastPrune    time.Time
}

// pruneEvery is how often a worker trims its finished jobs.
const pruneEvery = 6 * time.Hour

// NewWorker constructs a Worker for the given queue.
func NewWorker(queue *Queue, logger *slog.Logger) *Worker {
	return &Worker{
		queue:        queue,
		handlers:     make(map[string]Handler),
		logger:       logger,
		pollInterval: 10 * time.Second,
	}
}

// Register registers a handler for a job kind.
func (w *Worker) Register(kind string, h Handler) {
	w.handlers[kind] = h
}

// Run starts the worker loop. It blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("job worker started")
	wakeup := wakeChan(w.queue.db)
	timer := time.NewTimer(w.pollInterval)
	defer timer.Stop()
	for {
		w.drain(ctx)
		w.pruneIfDue(ctx)
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(w.pollInterval)
		select {
		case <-ctx.Done():
			w.logger.Info("job worker stopped")
			return
		case <-wakeup:
		case <-timer.C:
		}
	}
}

// drain runs queued jobs until none is runnable, an error occurs (so a
// broken database cannot spin the CPU), or ctx ends.
func (w *Worker) drain(ctx context.Context) {
	for ctx.Err() == nil {
		err := w.processOne(ctx)
		if errors.Is(err, errNoJob) {
			return
		}
		if err != nil {
			w.logger.Error("job worker error", "error", err)
			return
		}
	}
}

func (w *Worker) pruneIfDue(ctx context.Context) {
	now := time.Now()
	if now.Sub(w.lastPrune) < pruneEvery {
		return
	}
	w.lastPrune = now
	if n, err := w.queue.Prune(ctx, now.UTC()); err != nil {
		w.logger.Warn("prune finished jobs", "error", err)
	} else if n > 0 {
		w.logger.Info("pruned finished jobs", "count", n)
	}
}

// RunOnce processes exactly one queued job and returns. If no job is available
// it returns immediately. Intended for testing.
func (w *Worker) RunOnce(ctx context.Context) error {
	err := w.processOne(ctx)
	if errors.Is(err, errNoJob) {
		return nil
	}
	return err
}

var errNoJob = errors.New("no job available")

// processOne claims and executes exactly one queued job. Returns errNoJob
// when the queue is empty.
func (w *Worker) processOne(ctx context.Context) error {
	job, err := w.claimNext(ctx)
	if err != nil {
		return err
	}
	if job == nil {
		return errNoJob
	}

	handler, ok := w.handlers[job.Kind]
	if !ok {
		w.logger.Error("no handler for job kind", "kind", job.Kind, "job_id", job.ID)
		return w.failJob(ctx, job, fmt.Sprintf("no handler registered for kind %q", job.Kind))
	}

	w.logger.Info("executing job", "job_id", job.ID, "kind", job.Kind, "attempt", job.Attempt)
	jobErr := handler(ctx, job)
	if jobErr != nil {
		return w.handleFailure(ctx, job, jobErr)
	}
	return w.completeJob(ctx, job)
}

// claimNext atomically picks the next runnable job and marks it running.
func (w *Worker) claimNext(ctx context.Context) (*Job, error) {
	tx, err := w.queue.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin claim tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	row := tx.QueryRowContext(ctx,
		`SELECT id, kind, status, priority, payload, result, error_msg,
		        attempt, max_attempts, created_at, started_at, finished_at, next_run_at
		 FROM index_jobs
		 WHERE status = 'queued' AND next_run_at <= ?
		 ORDER BY priority DESC, created_at
		 LIMIT 1`, rfc3339(now))

	job, err := scanJob(row)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, nil
	}

	job.Attempt++
	job.Status = StatusRunning
	job.StartedAt = now
	_, err = tx.ExecContext(ctx,
		`UPDATE index_jobs SET status = 'running', attempt = ?, started_at = ? WHERE id = ?`,
		job.Attempt, rfc3339(now), job.ID)
	if err != nil {
		return nil, fmt.Errorf("claim job: %w", err)
	}
	return job, tx.Commit()
}

func (w *Worker) completeJob(ctx context.Context, job *Job) error {
	now := rfc3339(time.Now().UTC())
	_, err := w.queue.db.ExecContext(ctx,
		`UPDATE index_jobs SET status = 'completed', result = ?, finished_at = ? WHERE id = ?`,
		job.Result, now, job.ID)
	if err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	w.logger.Info("job completed", "job_id", job.ID, "kind", job.Kind)
	return nil
}

func (w *Worker) failJob(ctx context.Context, job *Job, errMsg string) error {
	now := rfc3339(time.Now().UTC())
	_, err := w.queue.db.ExecContext(ctx,
		`UPDATE index_jobs SET status = 'failed', error_msg = ?, finished_at = ? WHERE id = ?`,
		errMsg, now, job.ID)
	return err
}

// handleFailure decides whether to retry or permanently fail a job.
func (w *Worker) handleFailure(ctx context.Context, job *Job, jobErr error) error {
	w.logger.Warn("job failed", "job_id", job.ID, "kind", job.Kind,
		"attempt", job.Attempt, "max", job.MaxAttempts, "error", jobErr)

	if job.Attempt >= job.MaxAttempts {
		return w.failJob(ctx, job, jobErr.Error())
	}

	// Exponential backoff: 10s, 30s, 90s, …
	backoff := time.Duration(10*job.Attempt) * 3 * time.Second
	nextRun := rfc3339(time.Now().UTC().Add(backoff))
	_, err := w.queue.db.ExecContext(ctx,
		`UPDATE index_jobs SET status = 'queued', error_msg = ?, next_run_at = ? WHERE id = ?`,
		jobErr.Error(), nextRun, job.ID)
	return err
}

// --- row scanning helpers ---

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (*Job, error) {
	var (
		j           Job
		result      sql.NullString
		errMsg      sql.NullString
		startedStr  sql.NullString
		finishedStr sql.NullString
		payloadStr  string
		createdStr  string
		nextRunStr  string
	)
	err := row.Scan(&j.ID, &j.Kind, &j.Status, &j.Priority, &payloadStr,
		&result, &errMsg, &j.Attempt, &j.MaxAttempts,
		&createdStr, &startedStr, &finishedStr, &nextRunStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan job: %w", err)
	}
	j.Result = result.String
	j.ErrorMsg = errMsg.String
	if err := json.Unmarshal([]byte(payloadStr), &j.Payload); err != nil {
		j.Payload = map[string]any{}
	}
	if err := parseTime(createdStr, &j.CreatedAt); err != nil {
		return nil, err
	}
	if startedStr.Valid {
		if err := parseTime(startedStr.String, &j.StartedAt); err != nil {
			return nil, err
		}
	}
	if finishedStr.Valid {
		if err := parseTime(finishedStr.String, &j.FinishedAt); err != nil {
			return nil, err
		}
	}
	if err := parseTime(nextRunStr, &j.NextRunAt); err != nil {
		return nil, err
	}
	return &j, nil
}

func scanJobs(rows *sql.Rows) ([]*Job, error) {
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string, out *time.Time) error {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("parse time %q: %w", s, err)
	}
	*out = t
	return nil
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

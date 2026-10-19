package jobs_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/jobs"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
)

func newTestQueue(t *testing.T) *jobs.Queue {
	t.Helper()
	orchardDir := filepath.Join(t.TempDir(), ".orchard")
	if err := os.MkdirAll(orchardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.OpenDB(orchardDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return jobs.NewQueue(db.DB(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func newTestWorker(t *testing.T, q *jobs.Queue) *jobs.Worker {
	t.Helper()
	return jobs.NewWorker(q, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestEnqueueAndGet(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()

	id, err := q.Enqueue(ctx, jobs.KindIndex, map[string]any{"root": "/tmp/photos"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if id == "" {
		t.Error("empty job id")
	}

	job, err := q.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job == nil {
		t.Fatal("Get returned nil")
	}
	if job.Status != jobs.StatusQueued {
		t.Errorf("status = %q, want queued", job.Status)
	}
	if job.Kind != jobs.KindIndex {
		t.Errorf("kind = %q, want %q", job.Kind, jobs.KindIndex)
	}
	if job.Payload["root"] != "/tmp/photos" {
		t.Errorf("payload root = %v", job.Payload["root"])
	}
}

func TestListByStatus(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := q.Enqueue(ctx, jobs.KindIndex, nil); err != nil {
			t.Fatal(err)
		}
	}

	queued, err := q.ListByStatus(ctx, jobs.StatusQueued)
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(queued) != 3 {
		t.Errorf("queued count = %d, want 3", len(queued))
	}

	completed, err := q.ListByStatus(ctx, jobs.StatusCompleted)
	if err != nil {
		t.Fatal(err)
	}
	if len(completed) != 0 {
		t.Errorf("completed = %d, want 0", len(completed))
	}
}

func TestCancel(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()

	id, _ := q.Enqueue(ctx, jobs.KindIndex, nil)
	if err := q.Cancel(ctx, id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	job, _ := q.Get(ctx, id)
	if job.Status != jobs.StatusCancelled {
		t.Errorf("status = %q, want cancelled", job.Status)
	}
}

func TestResetStuck(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()

	// Manually insert a "running" job to simulate a stuck state.
	id, _ := q.Enqueue(ctx, jobs.KindIndex, nil)
	rawDB := q.RawDB()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := rawDB.ExecContext(ctx,
		`UPDATE index_jobs SET status = 'running', started_at = ? WHERE id = ?`,
		now, id); err != nil {
		t.Fatalf("set running: %v", err)
	}

	n, err := q.ResetStuck(ctx)
	if err != nil {
		t.Fatalf("ResetStuck: %v", err)
	}
	if n != 1 {
		t.Errorf("reset count = %d, want 1", n)
	}

	job, _ := q.Get(ctx, id)
	if job.Status != jobs.StatusQueued {
		t.Errorf("after reset status = %q, want queued", job.Status)
	}
}

func TestWorkerExecutesJob(t *testing.T) {
	q := newTestQueue(t)
	w := newTestWorker(t, q)
	ctx := context.Background()

	var executed atomic.Bool
	w.Register(jobs.KindIndex, func(_ context.Context, _ *jobs.Job) error {
		executed.Store(true)
		return nil
	})

	id, _ := q.Enqueue(ctx, jobs.KindIndex, nil)

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := w.RunOnce(runCtx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if !executed.Load() {
		t.Error("handler not called")
	}

	job, _ := q.Get(ctx, id)
	if job.Status != jobs.StatusCompleted {
		t.Errorf("final status = %q, want completed", job.Status)
	}
}

func TestWorkerPersistsJobResultSetByHandler(t *testing.T) {
	q := newTestQueue(t)
	w := newTestWorker(t, q)
	ctx := context.Background()

	w.Register(jobs.KindIndex, func(_ context.Context, job *jobs.Job) error {
		job.Result = `{"errors":2}`
		return nil
	})

	id, _ := q.Enqueue(ctx, jobs.KindIndex, nil)
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := w.RunOnce(runCtx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	job, err := q.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if job.Result != `{"errors":2}` {
		t.Errorf("Result = %q, want the handler's JSON", job.Result)
	}
}

func TestLastFinished(t *testing.T) {
	q := newTestQueue(t)
	w := newTestWorker(t, q)
	ctx := context.Background()

	if last, err := q.LastFinished(ctx, jobs.KindIndex); err != nil || last != nil {
		t.Fatalf("LastFinished before any job = (%+v, %v), want (nil, nil)", last, err)
	}

	w.Register(jobs.KindIndex, func(_ context.Context, job *jobs.Job) error {
		job.Result = `{"total_files":3}`
		return nil
	})
	id, _ := q.Enqueue(ctx, jobs.KindIndex, nil)
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := w.RunOnce(runCtx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	last, err := q.LastFinished(ctx, jobs.KindIndex)
	if err != nil {
		t.Fatalf("LastFinished: %v", err)
	}
	if last == nil || last.ID != id || last.Result != `{"total_files":3}` {
		t.Errorf("LastFinished = %+v, want the completed job with its result", last)
	}
}

func TestWorkerRetryOnFailure(t *testing.T) {
	q := newTestQueue(t)
	w := newTestWorker(t, q)
	ctx := context.Background()

	w.Register(jobs.KindIndex, func(_ context.Context, _ *jobs.Job) error {
		return errors.New("transient error")
	})

	id, _ := q.Enqueue(ctx, jobs.KindIndex, nil)

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Execute the job once; it should fail and be re-queued for retry.
	if err := w.RunOnce(runCtx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	job, _ := q.Get(ctx, id)
	// After one failure below max_attempts, should be back to queued.
	if job.Status != jobs.StatusQueued && job.Status != jobs.StatusFailed {
		t.Errorf("status after first failure = %q, want queued or failed", job.Status)
	}
	if job.ErrorMsg == "" {
		t.Error("error_msg should be set after failure")
	}
}

func TestWorkerPermanentFailure(t *testing.T) {
	q := newTestQueue(t)
	w := newTestWorker(t, q)
	ctx := context.Background()

	w.Register(jobs.KindIndex, func(_ context.Context, _ *jobs.Job) error {
		return errors.New("permanent error")
	})

	id, _ := q.Enqueue(ctx, jobs.KindIndex, nil)

	// Force max_attempts to 1 so a single failure results in permanent failure.
	rawDB := q.RawDB()
	if _, err := rawDB.ExecContext(ctx,
		`UPDATE index_jobs SET max_attempts = 1 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := w.RunOnce(runCtx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	job, _ := q.Get(ctx, id)
	if job.Status != jobs.StatusFailed {
		t.Errorf("status = %q, want failed", job.Status)
	}
}

// The worker must work through a backlog back to back and pick up a new job
// as soon as it is enqueued — well inside its 10s idle poll.
func TestWorkerDrainsBacklogAndWakesOnEnqueue(t *testing.T) {
	q := newTestQueue(t)
	w := newTestWorker(t, q)
	var ran atomic.Int32
	w.Register("noop", func(context.Context, *jobs.Job) error {
		ran.Add(1)
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for range 20 {
		if _, err := q.Enqueue(ctx, "noop", nil); err != nil {
			t.Fatal(err)
		}
	}
	go w.Run(ctx)

	waitFor := func(n int32) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for ran.Load() < n {
			if time.Now().After(deadline) {
				t.Fatalf("ran %d jobs, want %d", ran.Load(), n)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitFor(20)
	if _, err := q.Enqueue(ctx, "noop", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(21)
}

func TestPruneKeepsRecentAndTheLastOfEachKind(t *testing.T) {
	q := newTestQueue(t)
	ctx := context.Background()
	db := q.RawDB()
	insert := func(id, kind, status, finished string) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO index_jobs
			(id, kind, status, priority, payload, attempt, max_attempts, created_at, next_run_at, finished_at)
			VALUES (?, ?, ?, 0, '{}', 1, 3, ?, ?, ?)`, id, kind, status, finished, finished, finished)
		if err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour).Format(time.RFC3339Nano)
	older := now.Add(-72 * time.Hour).Format(time.RFC3339Nano)
	insert("old-scan", jobs.KindIndex, jobs.StatusCompleted, older)
	insert("last-scan", jobs.KindIndex, jobs.StatusCompleted, old)
	insert("recent-failure", jobs.KindProcessMedia, jobs.StatusFailed, old)
	insert("fresh", jobs.KindProcessMedia, jobs.StatusCompleted, now.Format(time.RFC3339Nano))

	n, err := q.Prune(ctx, now)
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v; want 1", n, err)
	}
	for _, id := range []string{"last-scan", "recent-failure", "fresh"} {
		if j, _ := q.Get(ctx, id); j == nil {
			t.Errorf("%s was pruned", id)
		}
	}
	if j, _ := q.Get(ctx, "old-scan"); j != nil {
		t.Error("old-scan survived")
	}
}

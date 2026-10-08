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

	"github.com/Jishnu-Prasad888/Cairn/internal/jobs"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
)

func newTestQueue(t *testing.T) *jobs.Queue {
	t.Helper()
	cairnDir := filepath.Join(t.TempDir(), ".cairn")
	if err := os.MkdirAll(cairnDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := librarydb.OpenDB(cairnDir)
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

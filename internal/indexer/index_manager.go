// Package indexer - index_manager.go wires the library DB, scanner, and job
// queue together into a single IndexManager that callers (HTTP handlers,
// library manager) use.
package indexer

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/Jishnu-Prasad888/Cairn/internal/jobs"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
	"github.com/Jishnu-Prasad888/Cairn/internal/metadata"
)

// IndexStatus is the current state of a library's index.
type IndexStatus struct {
	LibraryID string    `json:"library_id"`
	Present   int       `json:"present"`
	Missing   int       `json:"missing"`
	Deleted   int       `json:"deleted"`
	ActiveJob *jobs.Job `json:"active_job,omitempty"`
	LastJobID string    `json:"last_job_id,omitempty"`
}

// IndexManager coordinates opening the per-library database, enqueueing scan
// jobs, and reporting index status. Each library gets its own job queue backed
// by its library.db and one background worker that drains it.
type IndexManager struct {
	logger *slog.Logger

	// AfterScan, when non-nil, is invoked with the library root after a scan
	// job completes. main wires this to the ML manager so similarity passes
	// run in the background once indexing settles.
	AfterScan func(libraryID, root string)

	// mediaProcessor, when set, handles process_media jobs (metadata
	// extraction and thumbnail generation) on every library worker.
	mediaProcessor *metadata.Processor

	// baseCtx is the server-lifetime context (signal context from main) that
	// library workers live under. Request-scoped contexts must never parent a
	// worker, or it would stop the moment the triggering request returns.
	baseCtx context.Context

	// workerMu guards workers, one running worker per library. token gives
	// cleanup a unique identity so a stale worker cannot cancel its successor.
	workerMu sync.Mutex
	workers  map[string]*workerHandle
}

// SetBaseContext anchors library workers to a server-lifetime context. Call
// once at startup with the process signal context; workers started for new
// libraries and reconnected volumes then survive the requests that triggered
// them.
func (m *IndexManager) SetBaseContext(ctx context.Context) {
	m.baseCtx = ctx
}

// workerHandle tracks one running per-library job worker.
type workerHandle struct {
	cancel context.CancelFunc
	token  *struct{}
}

// NewIndexManager returns a new IndexManager.
func NewIndexManager(logger *slog.Logger) *IndexManager {
	return &IndexManager{logger: logger, workers: make(map[string]*workerHandle)}
}

// SetMediaProcessor configures the handler for process_media jobs. When nil,
// process_media jobs are executed anyway but metadata and thumbnails are not
// produced (jobs fail with "no handler" only when the processor is required).
func (m *IndexManager) SetMediaProcessor(p *metadata.Processor) {
	m.mediaProcessor = p
}

// StartLibraryWorker opens the library database for root and starts a
// background jobs.Worker for its queue, so queued scan and media-processing
// jobs are executed on their own. It is idempotent per library: a library that
// already has a worker is left untouched. The worker stops when ctx is
// cancelled or StopLibraryWorker is called.
func (m *IndexManager) StartLibraryWorker(ctx context.Context, libraryID, root string) error {
	m.workerMu.Lock()
	defer m.workerMu.Unlock()
	if _, ok := m.workers[libraryID]; ok {
		return nil
	}
	ldb, err := m.openLibraryDB(root)
	if err != nil {
		return fmt.Errorf("open library db for worker %s: %w", libraryID, err)
	}
	// Anchor the worker to the server lifetime; the triggering request context
	// ends almost immediately.
	parent := ctx
	if m.baseCtx != nil {
		parent = m.baseCtx
	}
	wctx, cancel := context.WithCancel(parent)
	q := jobs.NewQueue(ldb.DB(), m.logger)
	w := jobs.NewWorker(q, m.logger)
	w.Register(jobs.KindIndex, m.RunScanJob)
	if p := m.mediaProcessor; p != nil {
		db := ldb.DB()
		w.Register(jobs.KindProcessMedia, func(jctx context.Context, job *jobs.Job) error {
			return p.Handle(metadata.WithDB(jctx, db), job)
		})
	}
	handle := &workerHandle{cancel: cancel, token: &struct{}{}}
	m.workers[libraryID] = handle
	go func() {
		defer func() {
			_ = ldb.Close()
			m.workerMu.Lock()
			if m.workers[libraryID] == handle {
				delete(m.workers, libraryID)
			}
			m.workerMu.Unlock()
		}()
		w.Run(wctx)
	}()
	m.logger.Info("library job worker started", "library_id", libraryID, "root", root)
	return nil
}

// StopLibraryWorker cancels the background worker for a library, if one is
// running. Queued jobs remain persisted in the library database.
func (m *IndexManager) StopLibraryWorker(libraryID string) {
	m.workerMu.Lock()
	if handle, ok := m.workers[libraryID]; ok {
		delete(m.workers, libraryID)
		handle.cancel()
	}
	m.workerMu.Unlock()
}

// StartWorkers starts a background job worker for every registered library so
// that persisted scan jobs are recovered after a restart. Libraries that are
// offline (for example an unplugged drive) are skipped with a warning and are
// picked up on the next TriggerScan once the volume is back.
func (m *IndexManager) StartWorkers(ctx context.Context, libs []library.Library) {
	for _, lib := range libs {
		if err := m.StartLibraryWorker(ctx, lib.ID, lib.Root); err != nil {
			m.logger.Warn("skip library worker at startup",
				"library_id", lib.ID, "root", lib.Root, "error", err)
		}
	}
}

// openLibraryDB opens (and migrates) the library-level SQLite database for the
// library rooted at root. The .cairn directory must already exist.
func (m *IndexManager) openLibraryDB(root string) (*librarydb.DB, error) {
	cairnDir := filepath.Join(root, ".cairn")
	return librarydb.OpenDB(cairnDir)
}

// TriggerScan opens the library DB, resets any stuck jobs, and enqueues a new
// index scan job. Returns the job ID. The library's background worker is
// (re)started first so the job is actually executed: this covers libraries
// registered after startup and volumes that were offline when the server
// booted. It is safe to call even if a scan is already queued (a second scan
// will simply queue behind the first).
func (m *IndexManager) TriggerScan(ctx context.Context, libraryID, root string) (string, error) {
	if err := m.StartLibraryWorker(ctx, libraryID, root); err != nil {
		m.logger.Warn("cannot ensure scan worker", "library_id", libraryID, "error", err)
		// Fall through: if the library database is genuinely missing the
		// enqueue below returns the authoritative error.
	}
	ldb, err := m.openLibraryDB(root)
	if err != nil {
		return "", fmt.Errorf("open library db for %s: %w", libraryID, err)
	}
	defer func() { _ = ldb.Close() }()

	q := jobs.NewQueue(ldb.DB(), m.logger)
	if _, err := q.ResetStuck(ctx); err != nil {
		m.logger.Warn("reset stuck jobs", "library_id", libraryID, "error", err)
	}
	jobID, err := q.Enqueue(ctx, jobs.KindIndex, map[string]any{
		"library_id": libraryID,
		"root":       root,
	})
	if err != nil {
		return "", fmt.Errorf("enqueue scan job for %s: %w", libraryID, err)
	}
	m.logger.Info("scan job enqueued", "library_id", libraryID, "job_id", jobID)
	return jobID, nil
}

// Status returns the current index status for the library at root.
func (m *IndexManager) Status(ctx context.Context, libraryID, root string) (*IndexStatus, error) {
	ldb, err := m.openLibraryDB(root)
	if err != nil {
		return nil, fmt.Errorf("open library db for %s: %w", libraryID, err)
	}
	defer func() { _ = ldb.Close() }()

	store := NewStateStore(ldb.DB())
	present, missing, deleted, err := store.Counts(ctx)
	if err != nil {
		return nil, fmt.Errorf("count indexed files for %s: %w", libraryID, err)
	}

	status := &IndexStatus{
		LibraryID: libraryID,
		Present:   present,
		Missing:   missing,
		Deleted:   deleted,
	}

	// Find the most recent active job.
	q := jobs.NewQueue(ldb.DB(), m.logger)
	running, err := q.ListByStatus(ctx, jobs.StatusRunning)
	if err == nil && len(running) > 0 {
		status.ActiveJob = running[0]
		status.LastJobID = running[0].ID
	} else {
		queued, err := q.ListByStatus(ctx, jobs.StatusQueued)
		if err == nil && len(queued) > 0 {
			status.LastJobID = queued[0].ID
		}
	}
	return status, nil
}

// RunScanJob is the jobs.Handler for KindIndex. It reads the root from the job
// payload, opens the library DB, runs a full scanner pass, then enqueues
// process_media jobs for all new and modified files.
func (m *IndexManager) RunScanJob(ctx context.Context, job *jobs.Job) error {
	libraryID, _ := job.Payload["library_id"].(string)
	root, _ := job.Payload["root"].(string)
	if root == "" {
		return fmt.Errorf("index job %s missing root", job.ID)
	}

	cairnDir := root + "/.cairn"
	ldb, err := m.openLibraryDB(root)
	if err != nil {
		return fmt.Errorf("open library db: %w", err)
	}
	defer func() { _ = ldb.Close() }()

	store := NewStateStore(ldb.DB())
	scanner := NewScanner(store, m.logger)
	result, err := scanner.Scan(ctx, libraryID, root)
	if err != nil {
		return err
	}
	m.logger.Info("scan job finished",
		"library_id", libraryID,
		"total", result.TotalFiles,
		"new", result.NewFiles,
		"modified", result.Modified,
		"moved", result.Moved,
		"missing", result.Missing,
	)

	// Enqueue media-processing jobs for new and modified files.
	q := jobs.NewQueue(ldb.DB(), m.logger)
	enqueued := 0
	for _, change := range result.Changes {
		if change.Kind != ChangeNew && change.Kind != ChangeModified {
			continue
		}
		if _, err := q.Enqueue(ctx, jobs.KindProcessMedia, map[string]any{
			"library_id": libraryID,
			"file_id":    change.File.ID,
			"rel_path":   change.File.RelPath,
			"root":       root,
			"cairn_dir":  cairnDir,
		}); err != nil {
			m.logger.Warn("enqueue process_media job", "file_id", change.File.ID, "error", err)
		} else {
			enqueued++
		}
	}
	if enqueued > 0 {
		m.logger.Info("enqueued media processing jobs", "library_id", libraryID, "count", enqueued)
	}
	if m.AfterScan != nil {
		m.AfterScan(libraryID, root)
	}
	return nil
}

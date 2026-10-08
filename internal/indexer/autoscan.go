package indexer

import (
	"context"
	"fmt"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/jobs"
	"github.com/Jishnu-Prasad888/Cairn/internal/library"
)

// StartAutoScan rescans every online library on a timer, so files added,
// moved or removed with a file manager (or any other program) show up without
// anyone pressing a button. A rescan only stats files, and re-reads one only
// when its size or modification time changed, so an unchanged library costs
// little. It stops when ctx ends. An interval of zero or less disables it.
//
// list returns the libraries to look at; main passes a function that first
// reconciles library status, so a drive that was plugged back in is noticed.
func (m *IndexManager) StartAutoScan(ctx context.Context, interval time.Duration,
	list func(context.Context) ([]library.Library, error)) {
	if interval <= 0 {
		return
	}
	pass := func() {
		libs, err := list(ctx)
		if err != nil {
			m.logger.Warn("auto scan: list libraries", "error", err)
			return
		}
		for _, lib := range libs {
			if lib.Status == library.StatusOffline {
				continue
			}
			if err := m.scheduleScan(ctx, lib.ID, lib.Root); err != nil {
				m.logger.Warn("auto scan", "library_id", lib.ID, "error", err)
			}
		}
	}
	go func() {
		// Once at startup, to catch up on whatever changed while Cairn was off.
		pass()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pass()
			}
		}
	}()
}

// scheduleScan queues a scan unless one is already waiting or running, so a
// slow scan of a big library never piles up behind itself.
func (m *IndexManager) scheduleScan(ctx context.Context, libraryID, root string) error {
	if err := m.StartLibraryWorker(ctx, libraryID, root); err != nil {
		return err
	}
	ldb, err := m.openLibraryDB(root)
	if err != nil {
		return fmt.Errorf("open library db: %w", err)
	}
	defer func() { _ = ldb.Close() }()

	q := jobs.NewQueue(ldb.DB(), m.logger)
	busy, err := hasActiveIndexJob(ctx, q)
	if err != nil || busy {
		return err
	}
	_, err = q.Enqueue(ctx, jobs.KindIndex, map[string]any{"library_id": libraryID, "root": root})
	return err
}

// hasActiveIndexJob reports whether a scan is already queued or running.
func hasActiveIndexJob(ctx context.Context, q *jobs.Queue) (bool, error) {
	for _, status := range []string{jobs.StatusRunning, jobs.StatusQueued} {
		active, err := q.ListByStatus(ctx, status)
		if err != nil {
			return false, err
		}
		for _, j := range active {
			if j.Kind == jobs.KindIndex {
				return true, nil
			}
		}
	}
	return false, nil
}

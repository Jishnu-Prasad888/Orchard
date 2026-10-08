package ml

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// settingKey is the server_settings row holding the master switch.
const settingKey = "ml.enabled"

// thresholdKey holds the administrator's face-matching threshold.
const thresholdKey = "ml.face_threshold"

// batchKey holds how many unprocessed images must pile up before a finished
// scan starts the models.
const batchKey = "ml.batch_size"

// MaxBatchSize bounds the batch-size setting.
const MaxBatchSize = 10000

// Target is a library a pass can run over.
type Target struct {
	ID     string
	Root   string
	Online bool
}

// Runtime owns the administrator-controlled ML master switch.
//
// The switch is stored in the server database, so it survives restarts and
// wins over the CAIRN_ML_ENABLED default once an administrator has used it.
// Turning it on enables similarity and people recognition together and
// immediately starts a pass over every online library; from then on every
// finished library scan (new uploads included) feeds new images through the
// same pipeline: detect faces, then match them to existing people by name or
// create "Person N" for anyone new. Turning it off stops all work; people,
// names and signatures already stored are kept.
type Runtime struct {
	db         *sql.DB
	logger     *slog.Logger
	sim        *Manager
	faces      *FaceManager
	similarity bool
	libraries  func(ctx context.Context) ([]Target, error)

	// batch is the number of unprocessed images that triggers a run after a
	// scan (always at least 1).
	batch atomic.Int64

	// run serializes passes so a scan finishing mid-backfill queues behind it
	// instead of decoding the same images twice.
	run sync.Mutex
}

// NewRuntime wires the switch to the managers. similarity says whether the
// similarity capability should run alongside people recognition.
func NewRuntime(db *sql.DB, logger *slog.Logger, sim *Manager, faces *FaceManager,
	similarity bool, libraries func(ctx context.Context) ([]Target, error)) *Runtime {
	r := &Runtime{db: db, logger: logger, sim: sim, faces: faces, similarity: similarity, libraries: libraries}
	r.batch.Store(1)
	return r
}

// BatchSize reports how many unprocessed images trigger a run after a scan.
func (r *Runtime) BatchSize() int { return int(r.batch.Load()) }

// SetBatchSize stores and applies the batch size (clamped to 1..MaxBatchSize)
// and, when ML is on, immediately checks every online library against it, so
// lowering the number releases images that were waiting for a bigger batch.
func (r *Runtime) SetBatchSize(ctx context.Context, n int) error {
	if n < 1 {
		n = 1
	}
	if n > MaxBatchSize {
		n = MaxBatchSize
	}
	raw, _ := json.Marshal(n)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO server_settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		batchKey, string(raw), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	r.batch.Store(int64(n))
	if r.Enabled() {
		r.checkAll()
	}
	return nil
}

// checkAll applies the batch rule to every online library in the background.
func (r *Runtime) checkAll() {
	go func() {
		libs, err := r.libraries(context.Background())
		if err != nil {
			r.logger.Warn("ml: list libraries", "error", err)
			return
		}
		for _, l := range libs {
			if l.Online {
				r.process(l.ID, l.Root, r.BatchSize())
			}
		}
	}()
}

// Enabled reports whether ML is currently on.
func (r *Runtime) Enabled() bool { return r.sim.Enabled() || r.faces.Enabled() }

func (r *Runtime) apply(on bool) {
	r.sim.SetEnabled(on)
	r.faces.SetEnabled(on)
}

// Load applies the stored switch, or def when the administrator never set one.
func (r *Runtime) Load(ctx context.Context, def bool) error {
	on := def
	var raw string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE key = ?`, settingKey).Scan(&raw)
	switch {
	case err == nil:
		_ = json.Unmarshal([]byte(raw), &on)
	case errors.Is(err, sql.ErrNoRows):
	default:
		return err
	}
	r.apply(on)
	if err := r.db.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE key = ?`, batchKey).Scan(&raw); err == nil {
		var n int
		if json.Unmarshal([]byte(raw), &n) == nil && n >= 1 && n <= MaxBatchSize {
			r.batch.Store(int64(n))
		}
	}
	if err := r.db.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE key = ?`, thresholdKey).Scan(&raw); err == nil {
		var v float64
		if json.Unmarshal([]byte(raw), &v) == nil {
			r.faces.SetThreshold(v)
		}
	}
	return nil
}

// SetFaceThreshold stores and applies the face-matching threshold (0 resets
// it to the recognition model's default) and regroups every online library
// with it in the background. Named people and hand-placed faces are kept.
func (r *Runtime) SetFaceThreshold(ctx context.Context, v float64) error {
	r.faces.SetThreshold(v)
	var err error
	if v <= 0 {
		_, err = r.db.ExecContext(ctx, `DELETE FROM server_settings WHERE key = ?`, thresholdKey)
	} else {
		raw, _ := json.Marshal(v)
		_, err = r.db.ExecContext(ctx, `
			INSERT INTO server_settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			thresholdKey, string(raw), time.Now().UTC().Format(time.RFC3339Nano))
	}
	if err != nil {
		return err
	}
	if r.faces.Enabled() {
		r.regroupAll()
	}
	return nil
}

// regroupAll runs a grouping pass over every online library in the background.
func (r *Runtime) regroupAll() {
	go func() {
		libs, err := r.libraries(context.Background())
		if err != nil {
			r.logger.Warn("ml: list libraries", "error", err)
			return
		}
		for _, l := range libs {
			if !l.Online {
				continue
			}
			if _, err := r.faces.ClusterPass(context.Background(), l.Root); err != nil && !errors.Is(err, ErrFacesDisabled) {
				r.logger.Warn("ml: face grouping", "library_id", l.ID, "error", err)
			}
		}
	}()
}

// FaceThreshold reports the active threshold and the model's default.
func (r *Runtime) FaceThreshold() (current, def float64) {
	return r.faces.Threshold(), r.faces.DefaultThreshold()
}

// Set stores and applies the switch. Turning it on starts a pass over every
// online library in the background.
func (r *Runtime) Set(ctx context.Context, on bool) error {
	raw, _ := json.Marshal(on)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO server_settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		settingKey, string(raw), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	r.apply(on)
	if on {
		r.StartAll()
	}
	return nil
}

// StartAll runs a pass over every online library in the background.
func (r *Runtime) StartAll() {
	go func() {
		libs, err := r.libraries(context.Background())
		if err != nil {
			r.logger.Warn("ml: list libraries", "error", err)
			return
		}
		for _, l := range libs {
			if l.Online {
				r.process(l.ID, l.Root, 1)
			}
		}
	}()
}

// AfterScan is the indexer hook, called after every scan — including the
// catch-up scan at startup, so images added while Cairn was off count too. It
// returns immediately so the index worker is never held up by ML. The models
// run once at least BatchSize images are waiting; fewer stay pending (the
// library database is the record of what has not been processed) and are
// counted again after the next scan.
func (r *Runtime) AfterScan(libraryID, root string) {
	if !r.Enabled() {
		return
	}
	go r.process(libraryID, root, r.BatchSize())
}

// pending counts the images the models have not seen yet in the library at
// root: the larger of the face and similarity backlogs.
func (r *Runtime) pending(ctx context.Context, root string) (int, error) {
	n := 0
	if r.faces.Enabled() && r.faces.prov() != nil {
		c, err := r.faces.Pending(ctx, root)
		if err != nil {
			return 0, err
		}
		n = c
	}
	if r.similarity && r.sim.Enabled() {
		c, err := r.sim.Pending(ctx, root)
		if err != nil {
			return 0, err
		}
		// Both steps look at the same new photos, so the larger backlog is
		// the number of new images, not the sum.
		if c > n {
			n = c
		}
	}
	return n, nil
}

// Pending reports how many images in the library at root are waiting to be
// processed.
func (r *Runtime) Pending(ctx context.Context, root string) (int, error) {
	if !r.Enabled() {
		return 0, nil
	}
	return r.pending(ctx, root)
}

// process runs similarity, face detection and clustering for one library, but
// only when at least minPending images are waiting (1 means "whenever there is
// anything", which is also what explicit backfills use).
func (r *Runtime) process(libraryID, root string, minPending int) {
	r.run.Lock()
	defer r.run.Unlock()
	ctx := context.Background()
	if minPending > 1 {
		n, err := r.pending(ctx, root)
		if err != nil {
			r.logger.Warn("ml: count pending images", "library_id", libraryID, "error", err)
			return
		}
		if n < minPending {
			return
		}
	}
	if r.similarity && r.sim.Enabled() {
		if _, err := r.sim.Pass(ctx, libraryID, root); err != nil && !errors.Is(err, ErrDisabled) {
			r.logger.Warn("ml: similarity pass", "library_id", libraryID, "error", err)
		}
	}
	if r.faces.Enabled() {
		if _, err := r.faces.Pass(ctx, root); err != nil && !errors.Is(err, ErrFacesDisabled) {
			r.logger.Warn("ml: face pass", "library_id", libraryID, "error", err)
			return
		}
		if _, err := r.faces.ClusterPass(ctx, root); err != nil && !errors.Is(err, ErrFacesDisabled) {
			r.logger.Warn("ml: face clustering", "library_id", libraryID, "error", err)
		}
	}
}

// UsesFaceModel reports whether the learned face-recognition model is loaded.
func (r *Runtime) UsesFaceModel() bool { return r.faces.UsesEmbeddings() }

// ModelReady swaps in a freshly downloaded recognition model and, when ML is
// on, re-processes every online library with it.
func (r *Runtime) ModelReady(p *SCRFDEmbeddingFaceProvider) {
	r.faces.SetProvider(p)
	if r.Enabled() {
		r.StartAll()
	}
}

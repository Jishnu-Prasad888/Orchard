package ml

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/librarydb"
)

// Config controls the ML manager. Everything is off unless Enabled is true.
type Config struct {
	// Enabled is the master switch: all ML capabilities are off until true.
	Enabled bool
	// Workers is the max concurrent files processed per similarity pass.
	Workers int
	// DistanceThreshold is the maximum Hamming distance (0-64) at or below
	// which files are reported as similar.
	DistanceThreshold int
}

// DefaultThreshold is the default similarity distance threshold.
const DefaultThreshold = 10

// Manager runs local ML passes over libraries. It is inert and stores nothing
// while ML is disabled. It is safe for concurrent use.
type Manager struct {
	logger   *slog.Logger
	cfg      Config
	provider Provider
	enabled  atomic.Bool
	running  atomic.Int32 // passes currently running

	mu         sync.Mutex // guards lastPassAt and lastErr
	lastPassAt time.Time
	lastErr    string
}

// NewManager returns an ML manager for the given provider. The provider must
// be non-nil (the perceptual-hash provider is the default).
func NewManager(logger *slog.Logger, cfg Config, provider Provider) *Manager {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.DistanceThreshold == 0 {
		cfg.DistanceThreshold = DefaultThreshold
	}
	if provider == nil {
		provider = PerceptualHashProvider{}
	}
	m := &Manager{logger: logger, cfg: cfg, provider: provider}
	m.enabled.Store(cfg.Enabled)
	return m
}

// Enabled reports whether ML is on.
func (m *Manager) Enabled() bool { return m.enabled.Load() }

// SetEnabled flips the master switch at runtime (see Runtime).
func (m *Manager) SetEnabled(on bool) { m.enabled.Store(on) }

// ProviderName returns the active provider identifier (one provider is compiled
// in this phase; a registry follows when more exist).
func (m *Manager) ProviderName() string { return m.provider.Name() }

// ProviderVersion returns the active provider's algorithm version.
func (m *Manager) ProviderVersion() int { return m.provider.Version() }

// Pending counts the present images that have no signature yet.
func (m *Manager) Pending(ctx context.Context, root string) (int, error) {
	db, err := openLibraryDB(root)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	files, err := NewStore(db.DB()).IDsWithoutSignature(ctx, m.provider.Name(), m.provider.Version())
	return len(files), err
}

// Pass computes signatures for every present image missing one, with bounded
// concurrency. Results are written per-file so a cancelled pass still makes
// progress and is fully resumable. Pass is safe to call concurrently with pass
// work from other goroutines. Returns the number of files processed.
func (m *Manager) Pass(ctx context.Context, libraryID, root string) (int, error) {
	if !m.Enabled() {
		return 0, ErrDisabled
	}

	db, err := openLibraryDB(root)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	store := NewStore(db.DB())

	files, err := store.IDsWithoutSignature(ctx, m.provider.Name(), m.provider.Version())
	if err != nil {
		return 0, err
	}
	if len(files) == 0 {
		return 0, nil
	}

	m.running.Add(1)
	defer m.running.Add(-1)

	m.logger.Info("ml pass started", "library_id", libraryID, "files", len(files))

	workers := m.cfg.Workers
	if workers > len(files) {
		workers = len(files)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan UnsignedFile)
	var wg sync.WaitGroup
	var (
		mu        sync.Mutex
		processed int
		firstErr  error
	)

	start := time.Now()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for uf := range jobs {
				if err := m.signature(ctx, store, root, uf); err != nil {
					m.logger.Warn("ml signature failed",
						"library_id", libraryID, "file_id", uf.FileID, "error", err)
					var skip skipError
					if errors.As(err, &skip) {
						// A file that cannot be read or decoded must not stop
						// the pass (or leave the feeder blocked with no worker).
						continue
					}
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					cancel()
					continue
				}
				mu.Lock()
				processed++
				mu.Unlock()
			}
		}()
	}

feed:
	for _, uf := range files {
		select {
		case <-ctx.Done():
			break feed
		case jobs <- uf:
		}
	}
	close(jobs)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	elapsed := time.Since(start)
	m.logger.Info("ml pass finished", "library_id", libraryID,
		"processed", processed, "elapsed", elapsed.String())

	m.recordPassResult(firstErr)
	if firstErr != nil {
		return processed, firstErr
	}
	return processed, nil
}

// recordPassResult stores when a pass last finished and, if it failed, why —
// so the status endpoint can tell the UI the pass is done without the caller
// having to stay on the page that started it.
func (m *Manager) recordPassResult(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastPassAt = time.Now()
	if err != nil {
		m.lastErr = err.Error()
	} else {
		m.lastErr = ""
	}
}

// skipError marks a per-file failure (unreadable or undecodable image) that
// only skips that file, as opposed to a storage failure that stops the pass.
type skipError struct{ error }

func (e skipError) Unwrap() error { return e.error }

// signature derives and stores a signature for one file, best-effort tolerant
// of undecodable or deleted files (they are simply skipped).
func (m *Manager) signature(ctx context.Context, store *Store, root string, uf UnsignedFile) error {
	abs := filepath.Join(root, filepath.FromSlash(uf.RelPath))
	img, err := openImage(abs)
	if err != nil {
		return skipError{err}
	}
	sig, err := m.provider.Signature(img)
	if err != nil {
		return skipError{err}
	}
	rec := SignatureRecord{
		FileID:    uf.FileID,
		Provider:  m.provider.Name(),
		Version:   m.provider.Version(),
		Signature: sig,
	}
	if err := store.Upsert(ctx, rec); err != nil {
		return err
	}
	return nil
}

// Status describes the current similarity state for a library.
type Status struct {
	LibraryID       string `json:"library_id"`
	Enabled         bool   `json:"enabled"`
	Provider        string `json:"provider"`
	ProviderVersion int    `json:"provider_version"`
	SigCount        int    `json:"signatured"`
	DistanceLimit   int    `json:"distance_limit"`
	// Running is true while a similarity pass is running or queued, so the UI
	// can wait for it instead of guessing.
	Running bool `json:"running"`
	// Pending is how many present photos have no signature yet.
	Pending int `json:"pending"`
	// LastPassAt is when a pass last finished, nil if none has yet. A pointer
	// so the zero time is omitted instead of serialized as a real-looking date.
	LastPassAt *time.Time `json:"last_pass_at,omitempty"`
	// Error is the error the last pass finished with, if any.
	Error string `json:"error,omitempty"`
}

// Status reports the ML similarity state for the library at root.
func (m *Manager) Status(ctx context.Context, libraryID, root string) (*Status, error) {
	m.mu.Lock()
	lastPassAt := m.lastPassAt
	lastErr := m.lastErr
	m.mu.Unlock()

	st := &Status{
		LibraryID:       libraryID,
		Enabled:         m.Enabled(),
		Provider:        m.ProviderName(),
		ProviderVersion: m.ProviderVersion(),
		DistanceLimit:   m.cfg.DistanceThreshold,
		Running:         m.running.Load() > 0,
		Error:           lastErr,
	}
	if !lastPassAt.IsZero() {
		st.LastPassAt = &lastPassAt
	}
	if !st.Enabled {
		return st, nil
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	store := NewStore(db.DB())
	unsigned, err := store.IDsWithoutSignature(ctx, m.provider.Name(), m.provider.Version())
	if err != nil {
		return nil, err
	}
	st.Pending = len(unsigned)
	n, err := store.Count(ctx)
	if err != nil {
		return nil, err
	}
	st.SigCount = n
	return st, nil
}

// Similar returns files in the library whose signature is near the given
// file's, by Hamming distance, capped at limit and filtered by the configured
// distance threshold. distanceLimit == 0 means only exact-distance-0 filters
// apply; the manager's configured threshold is used when the caller passes a
// non-positive value via the API layer.
func (m *Manager) Similar(ctx context.Context, libraryID, root, fileID string, limit int) ([]SimilarHit, error) {
	if !m.Enabled() {
		return nil, ErrDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	store := NewStore(db.DB())

	rec, err := store.Get(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if rec == nil || rec.Provider != m.provider.Name() || rec.Version != m.provider.Version() {
		// The file being looked at has not been (or is no longer correctly)
		// signed — sign it now rather than answering "nothing similar".
		rec, err = m.signNow(ctx, store, root, fileID)
		if err != nil || rec == nil {
			return nil, err
		}
	}
	hits, err := store.Similar(ctx, rec.Signature, 0, fileID)
	if err != nil {
		return nil, err
	}
	thr := m.cfg.DistanceThreshold
	out := hits[:0]
	seen := make(map[string]bool, len(hits))
	for _, h := range hits {
		if h.Distance <= thr {
			out = append(out, h)
			seen[h.FileID] = true
		}
	}
	// Byte-identical files are the surest match there is, and they are found
	// even when their own signature has not been computed yet.
	same, err := store.SameContent(ctx, fileID)
	if err != nil {
		return nil, err
	}
	for _, id := range same {
		if !seen[id] {
			out = append(out, SimilarHit{FileID: id, Distance: 0, Similarity: 1})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Distance < out[j].Distance })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Groups clusters every present photo's signature into similarity groups —
// two or more files connected by the same distance rule Similar applies to
// one file at a time — so the whole library's near-duplicates can be
// reviewed at once.
func (m *Manager) Groups(ctx context.Context, libraryID, root string) ([]SimilarityGroup, error) {
	if !m.Enabled() {
		return nil, ErrDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	store := NewStore(db.DB())

	sigs, err := store.AllSignatures(ctx, m.provider.Name(), m.provider.Version())
	if err != nil {
		return nil, err
	}
	return clusterSignatures(sigs, m.cfg.DistanceThreshold), nil
}

// signNow computes and stores the signature of one photo. It returns nil when
// the file is not a readable photo.
func (m *Manager) signNow(ctx context.Context, store *Store, root, fileID string) (*SignatureRecord, error) {
	rel, err := store.RelPath(ctx, fileID)
	if err != nil || rel == "" {
		return nil, err
	}
	if err := m.signature(ctx, store, root, UnsignedFile{FileID: fileID, RelPath: rel}); err != nil {
		var skip skipError
		if errors.As(err, &skip) {
			return nil, nil
		}
		return nil, err
	}
	return store.Get(ctx, fileID)
}

// Purge deletes all derived similarity signatures for the library. Originals
// are untouched and the pass can regenerate them.
func (m *Manager) Purge(ctx context.Context, libraryID, root string) (int, error) {
	if !m.Enabled() {
		return 0, ErrDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	store := NewStore(db.DB())
	n, err := store.Count(ctx)
	if err != nil {
		return 0, err
	}
	if err := store.Purge(ctx); err != nil {
		return 0, err
	}
	m.logger.Info("ml signatures purged", "library_id", libraryID, "count", n)
	return n, nil
}

// openLibraryDB opens (and migrates) the per-library database at root and
// must be closed by the caller.
func openLibraryDB(root string) (*librarydb.DB, error) {
	cairnDir := filepath.Join(root, ".cairn")
	return librarydb.OpenDB(cairnDir)
}

// ErrDisabled is returned by ML operations when ML is not enabled.
var ErrDisabled = errors.New("local ML is disabled")

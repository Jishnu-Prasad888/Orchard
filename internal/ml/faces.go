package ml

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// FaceConfig controls the local face capability off the Phase 11 ML seam.
// Everything is off unless FaceConfig.Enabled (CAIRN_ML_FACES) is true, which
// in turn requires the master ML switch (CAIRN_ML_ENABLED).
type FaceConfig struct {
	// Enabled gates this capability independently of similarity.
	Enabled bool
	// Workers is the max concurrent files scanned per face pass.
	Workers int
	// Version is the detector algorithm version consulted for stored rows.
	MinConfidence float64
	MinSize       int
	// Threshold is the minimum cosine similarity (0..1) for an unassigned
	// face to join an existing person.
	Threshold float64
}

// Defaults for face recognition. MinConfidence maps to pigo's raw score ~5
// (normalized by /100).
const (
	DefaultFaceWorkers       = 2
	DefaultFaceMinConfidence = 0.05
	DefaultFaceMinSize       = 48
	DefaultFaceThreshold     = 0.82
)

// ErrFacesDisabled is returned by face operations when the capability is off.
var ErrFacesDisabled = errors.New("local face recognition is disabled")

// FaceManager runs the face capability: detection passes, clustering passes,
// people curation, and privacy purges. It is inert while disabled and safe
// for concurrent use.
type FaceManager struct {
	logger   *slog.Logger
	cfg      FaceConfig
	provider FaceProvider // guarded by mu: swapped when the model is downloaded
	mu       sync.RWMutex
	// passMu serializes detection and clustering passes however they were
	// started (scan hook, model install, API buttons). Overlapping passes both
	// see a photo as unscanned and store its faces twice, and clustering while
	// detection is still inserting leaves faces ungrouped.
	passMu  sync.Mutex
	running atomic.Int32 // passes running or waiting to run
	// pending marks libraries with a RegroupSoon pass queued but not started.
	pendingMu sync.Mutex
	pending   map[string]bool
	enabled   atomic.Bool
	// threshold holds the clustering similarity as float64 bits so an
	// administrator can change it while passes are running.
	threshold atomic.Uint64
	// defaultThreshold is the provider's own default (reset target).
	defaultThreshold float64
	// customThreshold is set once an administrator (or the environment) chose one.
	customThreshold atomic.Bool
}

// Threshold is the cosine similarity at which a face joins an existing person.
func (m *FaceManager) Threshold() float64 { return math.Float64frombits(m.threshold.Load()) }

// DefaultThreshold is the provider's recommended threshold.
func (m *FaceManager) DefaultThreshold() float64 { return m.defaultThreshold }

// SetThreshold changes the clustering similarity; 0 restores the default.
func (m *FaceManager) SetThreshold(v float64) {
	m.customThreshold.Store(v > 0)
	if v <= 0 {
		v = m.defaultThreshold
	}
	m.threshold.Store(math.Float64bits(v))
}

// NewFaceManager returns a face manager using provider. When provider is nil
// the manager starts with no face capability: Pass() will return
// ErrFacesDisabled until a working provider is installed via SetProvider().
// This replaces the old behaviour of silently falling back to the basic
// appearance descriptor, which produced low-quality groupings.
func NewFaceManager(logger *slog.Logger, cfg FaceConfig, provider FaceProvider) *FaceManager {
	if cfg.Workers <= 0 {
		cfg.Workers = DefaultFaceWorkers
	}
	if cfg.MinConfidence <= 0 {
		cfg.MinConfidence = DefaultFaceMinConfidence
	}
	if cfg.MinSize <= 0 {
		cfg.MinSize = DefaultFaceMinSize
	}
	// Do NOT fall back to PigoFaceProvider when provider is nil. A nil provider
	// means "model not yet installed": face passes are disabled until the model
	// arrives. Wrong face grouping is worse than no face grouping.
	configThresholdSet := cfg.Threshold > 0 && cfg.Threshold <= 1
	if !configThresholdSet {
		cfg.Threshold = DefaultFaceThreshold
		if d, ok := provider.(interface{ DefaultThreshold() float64 }); ok {
			cfg.Threshold = d.DefaultThreshold()
		}
	}
	m := &FaceManager{logger: logger, cfg: cfg, provider: provider, defaultThreshold: DefaultFaceThreshold}
	if d, ok := provider.(interface{ DefaultThreshold() float64 }); ok {
		m.defaultThreshold = d.DefaultThreshold()
	}
	m.threshold.Store(math.Float64bits(cfg.Threshold))
	m.customThreshold.Store(configThresholdSet)
	m.enabled.Store(cfg.Enabled && provider != nil)
	return m
}

// Enabled reports whether the face capability is on.
func (m *FaceManager) Enabled() bool { return m.enabled.Load() }

// SetEnabled flips the face capability at runtime (see Runtime).
func (m *FaceManager) SetEnabled(on bool) { m.enabled.Store(on) }

// ProviderName returns the active face provider identifier.
func (m *FaceManager) ProviderName() string {
	p := m.prov()
	if p == nil {
		return "none"
	}
	return p.Name()
}

// ProviderVersion returns the active face provider's algorithm version.
func (m *FaceManager) ProviderVersion() int {
	p := m.prov()
	if p == nil {
		return 0
	}
	return p.Version()
}

// FaceStatus describes the current face state for a library.
type FaceStatus struct {
	Enabled         bool   `json:"enabled"`
	Provider        string `json:"provider"`
	ProviderVersion int    `json:"provider_version"`
	Faces           int    `json:"faces"`
	People          int    `json:"people"`
	Unassigned      int    `json:"unassigned"`
	// Running is true while a detection or grouping pass is running or
	// queued, so the UI can wait for it instead of guessing.
	Running bool `json:"running"`
	// Pending is how many photos are waiting for face detection; BatchSize is
	// how many must be waiting before the next run starts by itself.
	Pending   int `json:"pending"`
	BatchSize int `json:"batch_size"`
}

// Status reports the face state for the library at root.
func (m *FaceManager) Status(ctx context.Context, root string) (*FaceStatus, error) {
	provName := "none"
	provVersion := 0
	if p := m.prov(); p != nil {
		provName = p.Name()
		provVersion = p.Version()
	}
	st := &FaceStatus{
		Enabled:         m.Enabled(),
		Provider:        provName,
		ProviderVersion: provVersion,
		Running:         m.running.Load() > 0,
	}
	if !st.Enabled {
		return st, nil
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	store := NewFaceStore(db.DB())

	if st.Faces, err = store.CountFaces(ctx); err != nil {
		return nil, err
	}
	if st.People, err = store.CountPeople(ctx); err != nil {
		return nil, err
	}
	unassigned, err := store.FacesUnassigned(ctx)
	if err != nil {
		return nil, err
	}
	st.Unassigned = len(unassigned)
	if p := m.prov(); p != nil {
		files, err := store.FilesToScan(ctx, p.Name(), p.Version())
		if err != nil {
			return nil, err
		}
		st.Pending = len(files)
	}
	return st, nil
}

// Pending counts the present photos that face detection has not seen yet.
func (m *FaceManager) Pending(ctx context.Context, root string) (int, error) {
	prov := m.prov()
	if prov == nil {
		return 0, nil
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	files, err := NewFaceStore(db.DB()).FilesToScan(ctx, prov.Name(), prov.Version())
	return len(files), err
}

// Pass runs face detection over every present photo missing a signature for
// the current provider version, with bounded concurrency, writing one face
// row per detection and recording each scanned file. Returns the number of
// files scanned.
//
// Pass returns ErrFacesDisabled when the face capability is off or when no
// model has been installed yet. In the latter case the error message is more
// specific.
func (m *FaceManager) Pass(ctx context.Context, root string) (int, error) {
	if !m.Enabled() {
		return 0, ErrFacesDisabled
	}
	if m.prov() == nil {
		return 0, fmt.Errorf("%w: face recognition model not installed — "+
			"download the SCRFD detector and ArcFace recognizer models from the ML settings page",
			ErrFacesDisabled)
	}
	m.running.Add(1)
	defer m.running.Add(-1)
	m.passMu.Lock()
	defer m.passMu.Unlock()
	db, err := openLibraryDB(root)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	store := NewFaceStore(db.DB())

	prov := m.prov()
	if n, err := store.DedupeFaces(ctx); err != nil {
		return 0, err
	} else if n > 0 {
		m.logger.Info("removed duplicate faces", "faces", n)
	}
	if n, err := store.PurgeStaleFaces(ctx, prov.Name(), prov.Version()); err != nil {
		return 0, err
	} else if n > 0 {
		m.logger.Info("discarded faces from a previous recognition algorithm",
			"faces", n, "provider", prov.Name(), "version", prov.Version())
	}

	files, err := store.FilesToScan(ctx, prov.Name(), prov.Version())
	if err != nil {
		return 0, err
	}
	if len(files) == 0 {
		return 0, nil
	}

	workers := m.cfg.Workers
	if workers > len(files) {
		workers = len(files)
	}
	// A storage failure stops the pass; a photo that cannot be read or
	// analysed is only skipped, so one bad file never stalls the library.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan UnsignedFaceFile)
	var wg sync.WaitGroup
	var (
		mu       sync.Mutex
		scanned  int
		firstErr error
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for uf := range jobs {
				if err := m.scanFile(ctx, store, root, uf); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					cancel()
					continue
				}
				mu.Lock()
				scanned++
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
	return scanned, firstErr
}

// scanFile detects faces in one file and stores them together with a record
// that the file was scanned, so photos without faces are not analysed again
// on every pass. Only storage errors are returned: a photo that cannot be
// decoded is recorded as scanned (it would fail the same way next time), and
// a detector failure is logged and retried on a later pass.
func (m *FaceManager) scanFile(ctx context.Context, store *FaceStore, root string, uf UnsignedFaceFile) error {
	if ctx.Err() != nil {
		return nil
	}
	prov := m.prov()
	abs := filepath.Join(root, filepath.FromSlash(uf.RelPath))
	img, err := openImage(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			m.logger.Warn("face scan: cannot open photo", "path", uf.RelPath, "error", err)
			return nil
		}
		m.logger.Warn("face scan: cannot decode photo; skipping it", "path", uf.RelPath, "error", err)
		return store.RecordFaceScan(ctx, uf.FileID, prov.Name(), prov.Version(), nil)
	}
	boxes, err := prov.Detect(img)
	if err != nil {
		m.logger.Warn("face scan: detection failed", "path", uf.RelPath, "error", err)
		return nil
	}
	faces := make([]FaceRecord, 0, len(boxes))
	for _, box := range boxes {
		// Faces this small are background passers-by; their descriptors are
		// too unreliable to group and would only clutter "Who is this?".
		if box.Width < m.cfg.MinSize || box.Height < m.cfg.MinSize {
			continue
		}
		desc, err := prov.Embed(img, box)
		if err != nil {
			m.logger.Warn("face scan: embedding failed", "path", uf.RelPath, "error", err)
			return nil
		}
		id, err := newID()
		if err != nil {
			return err
		}
		faces = append(faces, FaceRecord{
			ID:         id,
			FileID:     uf.FileID,
			Provider:   prov.Name(),
			Version:    prov.Version(),
			Box:        box,
			Descriptor: desc,
		})
	}
	return store.RecordFaceScan(ctx, uf.FileID, prov.Name(), prov.Version(), faces)
}

// FaceClusterResult reports a grouping pass outcome.
type FaceClusterResult struct {
	// Inspected is the number of faces that were free to be (re)grouped.
	Inspected int `json:"inspected"`
	// Assigned is the number of faces placed with an existing person.
	Assigned int `json:"assigned"`
	// Created is the number of new people.
	Created int `json:"created"`
}

// ClusterPass (re)groups faces into people; see face_cluster.go for the
// algorithm. People with a chosen name, and faces a human placed, are never
// changed; untouched "Person N" groups are rebuilt so the result does not
// depend on the order photos were scanned in and follows the current
// threshold. Running it again without new faces changes nothing.
func (m *FaceManager) ClusterPass(ctx context.Context, root string) (*FaceClusterResult, error) {
	if !m.Enabled() {
		return nil, ErrFacesDisabled
	}
	m.running.Add(1)
	defer m.running.Add(-1)
	m.passMu.Lock()
	defer m.passMu.Unlock()
	db, err := openLibraryDB(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	store := NewFaceStore(db.DB())

	faces, anchors, auto, err := store.groupingState(ctx)
	if err != nil {
		return nil, err
	}
	plan := planGroups(faces, anchors, auto, m.Threshold())
	if err := store.applyGroupPlan(ctx, faces, plan); err != nil {
		return nil, err
	}
	res := &FaceClusterResult{Inspected: plan.Inspected, Assigned: plan.Assigned, Created: plan.Created}
	m.logger.Info("face grouping finished", "inspected", res.Inspected,
		"assigned", res.Assigned, "created", res.Created, "threshold", m.Threshold())
	return res, nil
}

// People lists people with their face counts.
func (m *FaceManager) People(ctx context.Context, root string) ([]Person, error) {
	if !m.Enabled() {
		return nil, ErrFacesDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return NewFaceStore(db.DB()).ListPeople(ctx)
}

// PersonFaces lists the faces assigned to a person.
func (m *FaceManager) PersonFaces(ctx context.Context, root, personID string) ([]FaceRecord, error) {
	if !m.Enabled() {
		return nil, ErrFacesDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return NewFaceStore(db.DB()).PersonFaces(ctx, personID)
}

// Unassigned lists faces that belong to no person.
func (m *FaceManager) Unassigned(ctx context.Context, root string) ([]FaceRecord, error) {
	if !m.Enabled() {
		return nil, ErrFacesDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	faces, err := NewFaceStore(db.DB()).FacesUnassigned(ctx)
	if err != nil {
		return nil, err
	}
	if faces == nil {
		faces = []FaceRecord{}
	}
	return faces, nil
}

// CreatePerson is a pass-through used by the API for manual curation.
func (m *FaceManager) CreatePerson(ctx context.Context, root, name string) (*Person, error) {
	if !m.Enabled() {
		return nil, ErrFacesDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	p, err := NewFaceStore(db.DB()).CreatePerson(ctx, name)
	if err != nil {
		return nil, err
	}
	m.logger.Info("person created", "person_id", p.ID, "name", name)
	return p, nil
}

// RenamePerson updates a person's display name.
func (m *FaceManager) RenamePerson(ctx context.Context, root, personID, name string) error {
	if !m.Enabled() {
		return ErrFacesDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return NewFaceStore(db.DB()).RenamePerson(ctx, personID, name)
}

// SetCover selects which assigned face backs a person's cover thumbnail.
func (m *FaceManager) SetCover(ctx context.Context, root, personID, faceID string) error {
	if !m.Enabled() {
		return ErrFacesDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	store := NewFaceStore(db.DB())

	faces, err := store.PersonFaces(ctx, personID)
	if err != nil {
		return err
	}
	owned := false
	for _, f := range faces {
		if f.ID == faceID {
			owned = true
			break
		}
	}
	if !owned {
		return fmt.Errorf("face %q is not assigned to person %q", faceID, personID)
	}
	return store.SetCover(ctx, personID, faceID)
}

// DeletePerson removes a person (and their assignments, not their faces).
func (m *FaceManager) DeletePerson(ctx context.Context, root, personID string) error {
	if !m.Enabled() {
		return ErrFacesDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return NewFaceStore(db.DB()).DeletePerson(ctx, personID)
}

// DeletePersonAndFaces removes a person and every face assigned to them, so
// the group does not come back on the next grouping pass.
func (m *FaceManager) DeletePersonAndFaces(ctx context.Context, root, personID string) (int, error) {
	if !m.Enabled() {
		return 0, ErrFacesDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	return NewFaceStore(db.DB()).DeletePersonAndFaces(ctx, personID)
}

// DeleteFace removes one face for good; it is not detected again.
func (m *FaceManager) DeleteFace(ctx context.Context, root, faceID string) error {
	if !m.Enabled() {
		return ErrFacesDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return NewFaceStore(db.DB()).DeleteFace(ctx, faceID)
}

// RegroupSoon runs a grouping pass for the library in the background, so a
// hand edit (a new person, a moved face, a merge) is taken into account at
// once. Requests that arrive while one is already waiting are folded into it.
func (m *FaceManager) RegroupSoon(root string) {
	if !m.Enabled() {
		return
	}
	m.pendingMu.Lock()
	if m.pending == nil {
		m.pending = map[string]bool{}
	}
	if m.pending[root] {
		m.pendingMu.Unlock()
		return
	}
	m.pending[root] = true
	m.pendingMu.Unlock()
	// Count it as running from now, so the UI waits for it.
	m.running.Add(1)
	go func() {
		defer m.running.Add(-1)
		m.passMu.Lock()
		m.pendingMu.Lock()
		delete(m.pending, root)
		m.pendingMu.Unlock()
		m.passMu.Unlock()
		if _, err := m.ClusterPass(context.Background(), root); err != nil && !errors.Is(err, ErrFacesDisabled) {
			m.logger.Warn("face regroup after edit", "error", err)
		}
	}()
}

// MergePerson folds source into keep (used for consolidating duplicates).
func (m *FaceManager) MergePerson(ctx context.Context, root, keepID, sourceID string) (consolidated int, err error) {
	if !m.Enabled() {
		return 0, ErrFacesDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	store := NewFaceStore(db.DB())
	// A hand merge is the user saying "these are the same person".
	if err := store.MergeAs(ctx, keepID, sourceID, "manual"); err != nil {
		return 0, err
	}
	n, err := m.consolidate(ctx, store, keepID)
	if err != nil {
		// The merge itself succeeded; the sweep is a bonus.
		m.logger.Warn("face consolidation after merge", "error", err)
	}
	return n, nil
}

// consolidate folds into keepID every untouched automatic group ("Person N")
// that looks like it: its faces match the merged person's faces at least as
// well as a new face would have to. People with a name, or any face a human
// assigned, are never merged automatically.
func (m *FaceManager) consolidate(ctx context.Context, store *FaceStore, keepID string) (int, error) {
	ex, err := store.PersonExemplars(ctx, maxExemplars)
	if err != nil {
		return 0, err
	}
	auto, err := store.AutoNamedPeople(ctx)
	if err != nil {
		return 0, err
	}
	threshold := m.Threshold()
	merged := 0
	for pid, set := range ex {
		if pid == keepID || !auto[pid] {
			continue
		}
		keep := ex[keepID]
		if len(keep) == 0 {
			break
		}
		if groupSimilarity(set, keep) >= threshold {
			if err := store.MergeAs(ctx, keepID, pid, "auto"); err != nil {
				return merged, err
			}
			ex[keepID] = append(ex[keepID], set...)
			if len(ex[keepID]) > maxExemplars {
				ex[keepID] = ex[keepID][:maxExemplars]
			}
			merged++
		}
	}
	return merged, nil
}

// AssignFace manually attaches a face to a person.
func (m *FaceManager) AssignFace(ctx context.Context, root, personID, faceID string) error {
	if !m.Enabled() {
		return ErrFacesDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return NewFaceStore(db.DB()).AssignPerson(ctx, personID, faceID, "manual")
}

// UnassignFace frees a face from a person (it becomes cluster-eligible).
func (m *FaceManager) UnassignFace(ctx context.Context, root, personID, faceID string) error {
	if !m.Enabled() {
		return ErrFacesDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return NewFaceStore(db.DB()).UnassignPerson(ctx, personID, faceID)
}

// Purge deletes every derived face and assignment for the library, keeping
// people (names) intact. Returns the number of faces removed.
func (m *FaceManager) Purge(ctx context.Context, root string) (int, error) {
	if !m.Enabled() {
		return 0, ErrFacesDisabled
	}
	db, err := openLibraryDB(root)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	n, err := NewFaceStore(db.DB()).PurgeFaces(ctx)
	if err != nil {
		return 0, err
	}
	m.logger.Info("face data purged", "count", n)
	return n, nil
}

// UsesEmbeddings reports whether people are matched with the learned
// recognition model (true) or the basic appearance fallback (false).
func (m *FaceManager) UsesEmbeddings() bool {
	_, ok := m.prov().(*SCRFDEmbeddingFaceProvider)
	return ok
}

func (m *FaceManager) prov() FaceProvider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.provider
}

// SetProvider swaps the recognition algorithm at runtime (the model finished
// downloading). Faces made by the old provider are discarded by the next pass.
// If ML is globally enabled, SetProvider also enables the face capability
// (which was suspended while waiting for the model).
func (m *FaceManager) SetProvider(p FaceProvider) {
	m.mu.Lock()
	m.provider = p
	m.mu.Unlock()
	m.defaultThreshold = DefaultFaceThreshold
	if d, ok := p.(interface{ DefaultThreshold() float64 }); ok {
		m.defaultThreshold = d.DefaultThreshold()
	}
	if !m.customThreshold.Load() {
		m.threshold.Store(math.Float64bits(m.defaultThreshold))
	}
	// A newly installed model should make the face capability usable without
	// requiring a server restart.
	if m.cfg.Enabled {
		m.enabled.Store(true)
	}
}

// maxExemplars bounds how many faces of one person are compared against.
const maxExemplars = 40

// matchScore rates how well a face fits a person: the better of its similarity
// to the person's average and the mean of its two best matches among their
// individual faces. Two supporting faces are required (when the person has two)
// so a single odd photo cannot pull strangers in.
func matchScore(desc, centroid []float32, ex []Exemplar) float64 {
	best := DescriptorCosine(desc, centroid)
	var top1, top2 float64 = -2, -2
	for _, e := range ex {
		sim := DescriptorCosine(desc, e.Descriptor)
		if sim > top1 {
			top1, top2 = sim, top1
		} else if sim > top2 {
			top2 = sim
		}
	}
	var viaFaces float64
	switch {
	case top1 <= -2:
		return best
	case top2 <= -2:
		viaFaces = top1
	default:
		viaFaces = (top1 + top2) / 2
	}
	return math.Max(best, viaFaces)
}

// groupSimilarity rates two groups of faces against each other: the mean of the
// two strongest cross-group pairs.
func groupSimilarity(a, b []Exemplar) float64 {
	var top1, top2 float64 = -2, -2
	for _, x := range a {
		for _, y := range b {
			sim := DescriptorCosine(x.Descriptor, y.Descriptor)
			if sim > top1 {
				top1, top2 = sim, top1
			} else if sim > top2 {
				top2 = sim
			}
		}
	}
	switch {
	case top1 <= -2:
		return -1
	case top2 <= -2:
		return top1
	}
	return (top1 + top2) / 2
}

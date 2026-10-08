package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
)

// Manager registers, probes, and reconciles storage libraries. It owns the
// server-level registrar table and the on-disk .cairn metadata. Callers are
// expected to already hold whatever authorization the app layer demands
// (resource-based authorization; per-call capabilities gate access).
type Manager struct {
	db     *sql.DB
	logger *slog.Logger
	audit  *audit.Service
	keys   *crypto.Keys
}

// NewManager returns a Manager. keys are the optional at-rest encryption key
// used to seal the on-disk library identity; pass encryption.NewKeys("") (or
// nil — treated as disabled) when encryption is not configured.
func NewManager(db *sql.DB, logger *slog.Logger, audit *audit.Service, keys *crypto.Keys) *Manager {
	return &Manager{db: db, logger: logger, audit: audit, keys: keys}
}

// Probe inspects a candidate root and reports what a registration would do. It
// is read-only on both the filesystem and the registrar. Non-existent paths are
// accepted: probe reports PathExists=false in that case rather than an error,
// which allows the "create or open" wizard to surface a "will be created" hint.
func (m *Manager) Probe(ctx context.Context, root string) (*ProbeResult, error) {
	// Lightweight normalisation: reject obviously unworkable input (empty,
	// relative) but do not require the path to exist yet.
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, &ValidationError{Field: "path", Reason: "must not be empty"}
	}
	if !filepath.IsAbs(root) {
		return nil, &ValidationError{Field: "path", Reason: "must be an absolute path"}
	}
	clean := filepath.Clean(root)

	var result ProbeResult
	info, statErr := os.Stat(clean)
	if statErr == nil {
		result.PathExists = true
		result.IsDirectory = info.IsDir()
		result.IsWritable = isWritable(clean)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("stat %s: %w", clean, statErr)
	}
	// PathExists=false is a valid, non-error result.

	result.HasMetadata = hasMetadata(clean)
	if result.HasMetadata {
		if ident, err := loadIdentity(clean, m.keys); err == nil {
			result.ExistingID = ident.ID
			result.ExistingName = ident.Name
		}
	}
	if vid, ok := volumeID(clean); ok {
		result.VolumeID = vid
	}

	exists, err := m.registeredRoot(ctx, clean)
	if err != nil {
		return nil, err
	}
	result.Registered = exists
	return &result, nil
}

// Register adds a library at root, creating fresh metadata when the directory
// has none, or adopting the existing .cairn metadata when it does. If the
// metadata's identity already exists in the registrar (a disk reconnected at a
// new path), the registration rebinds instead of duplicating. The returned
// mode is "created" or "adopted".
//
// When root does not yet exist it is created (including any missing parents) so
// the user can register a library before placing files there.
func (m *Manager) Register(ctx context.Context, root, name string) (*Library, string, error) {
	// Normalise and validate the path first so we can give early errors for
	// obviously bad input (empty, relative, metadata-dir) before creating dirs.
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, "", &ValidationError{Field: "path", Reason: "must not be empty"}
	}
	if !filepath.IsAbs(root) {
		return nil, "", &ValidationError{Field: "path", Reason: "must be an absolute path"}
	}
	root = filepath.Clean(root)
	if filepath.Base(root) == cairnDirName {
		return nil, "", &ValidationError{Field: "path",
			Reason: "cannot register a Cairn metadata directory"}
	}

	// Create the directory (and any missing parents) if it does not yet exist.
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		if mkErr := os.MkdirAll(root, 0o755); mkErr != nil {
			return nil, "", fmt.Errorf("create library directory %s: %w", root, mkErr)
		}
	}

	clean, err := cleanRoot(root)
	if err != nil {
		return nil, "", err
	}

	existingRoots, err := m.allRoots(ctx)
	if err != nil {
		return nil, "", err
	}
	// Exact match: already registered at this root.
	if m.rootIn(clean, existingRoots) {
		return nil, "", ErrExists
	}
	if conflict, ok := overlappingRoot(clean, existingRoots); ok {
		return nil, "", &ValidationError{Field: "path",
			Reason: fmt.Sprintf("overlaps registered library at %s", conflict)}
	}

	if hasMetadata(clean) {
		lib, err := m.adopt(ctx, clean, name)
		return lib, "adopted", err
	}
	lib, err := m.create(ctx, clean, name)
	return lib, "created", err
}

// Create is the explicit "brand-new library" path. A directory that already
// carries .cairn metadata is rejected in favor of adoption.
func (m *Manager) Create(ctx context.Context, root, name string) (*Library, error) {
	clean, err := cleanRoot(root)
	if err != nil {
		return nil, err
	}
	if hasMetadata(clean) {
		return nil, &ValidationError{Field: "path",
			Reason: "already contains Cairn metadata; adopt it instead"}
	}
	registered, err := m.registeredRoot(ctx, clean)
	if err != nil {
		return nil, err
	}
	if registered {
		return nil, ErrExists
	}
	return m.create(ctx, clean, name)
}

// Adopt registers an existing library at root, reusing its on-disk metadata. A
// library whose identity is already registered (reconnected at a new path) is
// rebind rather than duplicated.
func (m *Manager) Adopt(ctx context.Context, root string) (*Library, error) {
	clean, err := cleanRoot(root)
	if err != nil {
		return nil, err
	}
	return m.adopt(ctx, clean, "")
}

// List returns the registered libraries ordered by name.
func (m *Manager) List(ctx context.Context) ([]Library, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, name, root, status, volume_id, schema_version, created_at, updated_at
		FROM libraries ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var libraries []Library
	for rows.Next() {
		lib, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		libraries = append(libraries, *lib)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate libraries: %w", err)
	}
	return libraries, nil
}

// Get returns the registered library with the given id, or ErrNotFound.
func (m *Manager) Get(ctx context.Context, id string) (*Library, error) {
	row := m.db.QueryRowContext(ctx, `
		SELECT id, name, root, status, volume_id, schema_version, created_at, updated_at
		FROM libraries WHERE id = ?`, id)
	lib, err := scanLibrary(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return lib, nil
}

// Refresh rechecks connectivity for a library. When newPath is non-empty it is
// treated as an explicit reconnection: the metadata identity must match, the
// root is rebound, and status returns to online. With no newPath the last known
// root is re-stat and status reconciled to online/offline.
func (m *Manager) Refresh(ctx context.Context, id, newPath string) (*Library, error) {
	lib, err := m.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if newPath != "" {
		return m.reconnect(ctx, lib, newPath)
	}
	return m.reconcileStatus(ctx, lib)
}

// Remove unregisters the library: the registrar row is deleted, the on-disk
// .cairn metadata is deliberately left in place so the library can be adopted
// again (or backed up) later.
func (m *Manager) Remove(ctx context.Context, id string) error {
	res, err := m.db.ExecContext(ctx, `DELETE FROM libraries WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("remove library: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	m.audit.Record(ctx, audit.Event{
		ActorUserID: actorIDFrom(ctx),
		Action:      audit.ActionLibraryDeleted,
		Details:     map[string]any{"library_id": id},
	})
	m.logger.Info("library unregistered", "library_id", id)
	return nil
}

// RefreshAll reconciles connectivity for every registered library. It is called
// at startup so a disconnected disk is immediately surfaced as offline.
func (m *Manager) RefreshAll(ctx context.Context) error {
	libs, err := m.List(ctx)
	if err != nil {
		return err
	}
	for i := range libs {
		if _, err := m.reconcileStatus(ctx, &libs[i]); err != nil {
			m.logger.Error("refresh library status", "library_id", libs[i].ID, "error", err)
		}
	}
	return nil
}

// --- internals ---

func (m *Manager) create(ctx context.Context, root, name string) (*Library, error) {
	label := resolveName(name, filepath.Base(root))

	vid, _ := volumeID(root)
	ident, err := createIdentity(root, label, vid, m.keys)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	lib := &Library{
		ID:            ident.ID,
		Name:          label,
		Root:          root,
		Status:        StatusOnline,
		VolumeID:      vid,
		SchemaVersion: ident.SchemaVersion,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := m.insert(ctx, lib); err != nil {
		// Registration failed; do not leave half a library behind.
		_ = os.RemoveAll(metadataDir(root))
		return nil, err
	}

	m.audit.Record(ctx, audit.Event{
		ActorUserID:  actorIDFrom(ctx),
		Action:       audit.ActionLibraryCreated,
		TargetUserID: "",
		Details:      map[string]any{"library_id": lib.ID, "root": root},
	})
	m.logger.Info("library created", "library_id", lib.ID, "root", root)
	return lib, nil
}

func (m *Manager) adopt(ctx context.Context, root, name string) (*Library, error) {
	ident, err := loadIdentity(root, m.keys)
	if err != nil {
		return nil, err
	}

	// Already registered under this identity: rebind rather than duplicate.
	existing, err := m.Get(ctx, ident.ID)
	switch {
	case err == nil:
		existing.Root = root
		existing.Status = StatusOnline
		if vid, ok := volumeID(root); ok {
			existing.VolumeID = vid
		}
		if label, err := validateName(name); err == nil {
			existing.Name = label
		}
		existing.UpdatedAt = time.Now().UTC()
		if err := m.update(ctx, existing); err != nil {
			return nil, err
		}
		m.audit.Record(ctx, audit.Event{
			ActorUserID: actorIDFrom(ctx),
			Action:      audit.ActionLibraryAdopted,
			Details:     map[string]any{"library_id": existing.ID, "root": root, "reconnected": true},
		})
		m.logger.Info("library reconnected", "library_id", existing.ID, "root", root)
		return existing, nil

	case errors.Is(err, ErrNotFound):
		// Crosses to the new-adoption branch below.

	default:
		return nil, err
	}

	vid, _ := volumeID(root)
	now := time.Now().UTC()
	label := resolveName(name, ident.Name)
	lib := &Library{
		ID:            ident.ID,
		Name:          label,
		Root:          root,
		Status:        StatusOnline,
		VolumeID:      vid,
		SchemaVersion: ident.SchemaVersion,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := m.insert(ctx, lib); err != nil {
		return nil, err
	}

	m.audit.Record(ctx, audit.Event{
		ActorUserID:  actorIDFrom(ctx),
		Action:       audit.ActionLibraryAdopted,
		TargetUserID: "",
		Details:      map[string]any{"library_id": lib.ID, "root": root},
	})
	m.logger.Info("library adopted", "library_id", lib.ID, "root", root)
	return lib, nil
}

func (m *Manager) insert(ctx context.Context, lib *Library) error {
	const query = `
		INSERT INTO libraries (id, name, root, status, volume_id, schema_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := m.db.ExecContext(ctx, query,
		lib.ID, lib.Name, lib.Root, string(lib.Status), lib.VolumeID, lib.SchemaVersion,
		rfc3339(lib.CreatedAt), rfc3339(lib.UpdatedAt)); err != nil {
		return fmt.Errorf("insert library: %w", err)
	}
	return nil
}

func (m *Manager) update(ctx context.Context, lib *Library) error {
	const query = `
		UPDATE libraries
		SET name = ?, root = ?, status = ?, volume_id = ?, updated_at = ?
		WHERE id = ?`
	if _, err := m.db.ExecContext(ctx, query,
		lib.Name, lib.Root, string(lib.Status), lib.VolumeID,
		rfc3339(lib.UpdatedAt), lib.ID); err != nil {
		return fmt.Errorf("update library: %w", err)
	}
	return nil
}

// reconcileStatus re-stats the last known root and flips status accordingly.
func (m *Manager) reconcileStatus(ctx context.Context, lib *Library) (*Library, error) {
	_, err := os.Stat(lib.Root)
	newStatus := StatusOffline
	if err == nil {
		newStatus = StatusOnline
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat library root %s: %w", lib.Root, err)
	}

	if lib.Status != newStatus {
		lib.Status = newStatus
		lib.UpdatedAt = time.Now().UTC()
		if err := m.update(ctx, lib); err != nil {
			return nil, err
		}
		m.logger.Info("library status changed", "library_id", lib.ID, "status", newStatus)
	}
	return lib, nil
}

// reconnect binds an offline or moved library to a new root. The metadata
// identity must match the registered library.
func (m *Manager) reconnect(ctx context.Context, lib *Library, newPath string) (*Library, error) {
	clean, err := cleanRoot(newPath)
	if err != nil {
		return nil, err
	}

	ident, identErr := loadIdentity(clean, m.keys)
	if identErr == nil {
		if ident.ID != lib.ID {
			return nil, &ValidationError{Field: "path",
				Reason: "points to a different library"}
		}
	} else if !errors.Is(identErr, errNoMetadata) {
		return nil, identErr
	}

	lib.Root = clean
	lib.Status = StatusOnline
	if vid, ok := volumeID(clean); ok {
		lib.VolumeID = vid
	}
	lib.UpdatedAt = time.Now().UTC()
	if err := m.update(ctx, lib); err != nil {
		return nil, err
	}

	m.audit.Record(ctx, audit.Event{
		ActorUserID: actorIDFrom(ctx),
		Action:      audit.ActionLibraryRefreshed,
		Details:     map[string]any{"library_id": lib.ID, "root": clean, "status": string(lib.Status)},
	})
	m.logger.Info("library reconnected", "library_id", lib.ID, "root", clean)
	return lib, nil
}

func (m *Manager) registeredRoot(ctx context.Context, root string) (bool, error) {
	var id string
	err := m.db.QueryRowContext(ctx, `SELECT id FROM libraries WHERE root = ?`, root).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query registered root: %w", err)
	}
	return true, nil
}

func (m *Manager) rootIn(root string, roots []string) bool {
	clean := filepath.Clean(root)
	for _, r := range roots {
		if filepath.Clean(r) == clean {
			return true
		}
	}
	return false
}

func (m *Manager) allRoots(ctx context.Context) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT root FROM libraries`)
	if err != nil {
		return nil, fmt.Errorf("list library roots: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var roots []string
	for rows.Next() {
		var root string
		if err := rows.Scan(&root); err != nil {
			return nil, fmt.Errorf("scan library root: %w", err)
		}
		roots = append(roots, root)
	}
	return roots, rows.Err()
}

// resolveName picks a validated display name, falling back to a derived label
// when the caller supplied none. An explicitly invalid name is an error here.
func resolveName(name, fallback string) string {
	if label, err := validateName(name); err == nil {
		return label
	}
	label, err := validateName(fallback)
	if err != nil {
		label = "Untitled"
	}
	return label
}

/*** row scanning and small helpers ***/

type rowScanner interface {
	Scan(dest ...any) error
}

func scanLibrary(row rowScanner) (*Library, error) {
	var (
		lib        Library
		status     string
		volumeID   sql.NullString
		createdStr string
		updatedStr string
	)
	err := row.Scan(&lib.ID, &lib.Name, &lib.Root, &status, &volumeID,
		&lib.SchemaVersion, &createdStr, &updatedStr)
	if err != nil {
		return nil, err
	}
	lib.Status = Status(status)
	lib.VolumeID = volumeID.String
	if err := parseTime(createdStr, &lib.CreatedAt); err != nil {
		return nil, err
	}
	if err := parseTime(updatedStr, &lib.UpdatedAt); err != nil {
		return nil, err
	}
	return &lib, nil
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(value string, out *time.Time) error {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return fmt.Errorf("parse stored timestamp %q: %w", value, err)
	}
	*out = t
	return nil
}

// isWritable probes a directory by creating then removing a unique temp file.
// It is deliberately portable (works on Windows too), where an access-bit check
// would not be.
func isWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".cairn-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

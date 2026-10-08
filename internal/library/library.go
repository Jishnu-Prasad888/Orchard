package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Status is the connectivity state of a library's backing storage.
type Status string

const (
	// StatusOnline means the library root is present and accessible.
	StatusOnline Status = "online"
	// StatusOffline means the backing storage (disk, network share, ...) is
	// currently unreachable at its last known root. Metadata is retained and
	// still browsable; media bytes are unavailable.
	StatusOffline Status = "offline"
)

// ValidationError describes a root path or name that failed registration rules.
type ValidationError struct{ Field, Reason string }

func (e *ValidationError) Error() string { return fmt.Sprintf("invalid %s: %s", e.Field, e.Reason) }

// Sentinel errors surfaced by the manager.
var (
	// ErrNotFound means no registered library has the given id.
	ErrNotFound = errors.New("library not found")
	// ErrExists means the root is already registered (as the same or a
	// different library).
	ErrExists = errors.New("library already registered at this path")
	// ErrNoMetadata means adopt was called on a path without Cairn metadata.
	ErrNoMetadata = errors.New("no cairn metadata found at path")
)

// Library is the registrar's view of a storage location, plus its current
// connectivity state. Root is the last known absolute path (which may be stale
// while offline); identity always follows the id.
type Library struct {
	ID            string
	Name          string
	Root          string
	Status        Status
	VolumeID      string
	SchemaVersion int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ProbeResult reports what Cairn can learn about a candidate root without
// changing anything. It drives the "create or open" wizard.
type ProbeResult struct {
	PathExists   bool   `json:"path_exists"`
	IsDirectory  bool   `json:"is_directory"`
	IsWritable   bool   `json:"is_writable"`
	HasMetadata  bool   `json:"has_metadata"`
	ExistingID   string `json:"existing_id,omitempty"`
	ExistingName string `json:"existing_name,omitempty"`
	Registered   bool   `json:"registered"`
	VolumeID     string `json:"volume_id,omitempty"`
}

// cleanRoot validates and normalizes a candidate library root: it must be
// non-empty, absolute, an existing directory, and never the metadata directory
// itself.
func cleanRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", &ValidationError{Field: "path", Reason: "must not be empty"}
	}
	if !filepath.IsAbs(root) {
		return "", &ValidationError{Field: "path", Reason: "must be an absolute path"}
	}
	root = filepath.Clean(root)

	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &ValidationError{Field: "path", Reason: "does not exist"}
		}
		return "", fmt.Errorf("stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return "", &ValidationError{Field: "path", Reason: "is not a directory"}
	}

	// Registering the metadata directory itself would nest libraries in .cairn.
	if filepath.Base(root) == cairnDirName {
		return "", &ValidationError{Field: "path",
			Reason: "cannot register a Cairn metadata directory"}
	}
	return root, nil
}

// validateName applies the bookkeeping rules for the human-readable library
// label.
func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", &ValidationError{Field: "name", Reason: "must not be empty"}
	case len(name) > 128:
		return "", &ValidationError{Field: "name", Reason: "must be at most 128 characters"}
	}
	return name, nil
}

// overlappingRoot reports whether the candidate root conflicts with an existing
// registered root: it is the same directory, a descendant of an existing root,
// or contains an existing root. This guarantees clean cross-library isolation
// (a later phase builds permissions on top of it).
func overlappingRoot(candidate string, existingRoots []string) (string, bool) {
	candidate = filepath.Clean(candidate)
	for _, root := range existingRoots {
		root = filepath.Clean(root)
		if candidate == root {
			return root, true
		}
		if strings.HasPrefix(candidate, root+string(filepath.Separator)) {
			return root, true
		}
		if strings.HasPrefix(root, candidate+string(filepath.Separator)) {
			return root, true
		}
	}
	return "", false
}

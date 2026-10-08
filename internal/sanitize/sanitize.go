// Package sanitize provides the canonical path and filename validators used at
// every point where a user-supplied value becomes a filesystem path. Keeping
// them here means one rule set is enforced by every entry point: media
// operations, folder filters, and backup restores.
package sanitize

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Sentinel errors returned by the validators.
var (
	ErrEmpty     = errors.New("path is empty")
	ErrAbsolute  = errors.New("path must be relative")
	ErrTraversal = errors.New("path must not contain '.' or '..' components")
	ErrReserved  = errors.New("path must not target the Cairn metadata directory")
	ErrControl   = errors.New("path must not contain control characters")
	ErrNotBare   = errors.New("filename must be a single name without separators")
)

// ReservedDir is the top-level directory name that stores Cairn's own library
// metadata. Library content may never address it.
const ReservedDir = ".cairn"

// RelPath validates and canonicalizes a caller-supplied relative path scoped to
// a library root. It returns the canonical slash-separated relative path, or an
// error for input that is empty, absolute, traversing ('.' or '..' components,
// including mid-path ones), targets the top-level metadata directory, or
// contains control characters.
func RelPath(raw string) (string, error) {
	if raw == "" {
		return "", ErrEmpty
	}
	if containsControl(raw) {
		return "", ErrControl
	}

	// Inspect the raw components before cleaning so a mid-path "./x" or
	// "a/../b" is rejected outright rather than silently collapsed. Empty
	// components from leading/trailing separators are handled by Clean.
	for _, part := range strings.Split(raw, "/") {
		switch part {
		case ".", "..":
			return "", fmt.Errorf("%w: %q", ErrTraversal, raw)
		}
	}

	clean := filepath.Clean(filepath.FromSlash(raw))
	// After cleaning, an absolute path means the input started with a slash
	// (e.g. "/etc/passwd").
	if filepath.IsAbs(clean) {
		return "", ErrAbsolute
	}
	slash := filepath.ToSlash(clean)

	// Reject the reserved metadata directory as the top-level component.
	if first := firstComponent(slash); first == ReservedDir {
		return "", fmt.Errorf("%w: %q", ErrReserved, raw)
	}
	return slash, nil
}

// BareName validates a single filename with no directory structure (e.g. a
// rename destination). In addition to the RelPath rules it rejects any path
// separator, so a "new_name" can never smuggle a directory change.
func BareName(raw string) (string, error) {
	if raw == "" {
		return "", ErrEmpty
	}
	if strings.ContainsAny(raw, "/\\") {
		return "", fmt.Errorf("%w: %q", ErrNotBare, raw)
	}
	if containsControl(raw) {
		return "", ErrControl
	}
	if raw == "." || raw == ".." || raw == "" {
		return "", fmt.Errorf("%w: %q", ErrTraversal, raw)
	}
	if raw == ReservedDir {
		return "", fmt.Errorf("%w: %q", ErrReserved, raw)
	}
	return raw, nil
}

// firstComponent returns the path component before the first slash.
func firstComponent(path string) string {
	if i := strings.IndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return path
}

func containsControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

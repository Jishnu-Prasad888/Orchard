// Package library manages Cairn's storage libraries: existing directories the
// server catalogs in place (ADR-0004). A library is self-describing: its
// persistent identity and configuration live in <root>/.cairn/library.json, so
// a library can be reconnected after being mounted at a different path and
// adopted by a fresh installation without reprocessing anything.
package library

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/crypto"
)

// cairnDirName is the single metadata directory Cairn keeps inside each
// library. It is never indexed as user media and never user-modifiable state.
const cairnDirName = ".cairn"

// metadataFileName is the library identity file inside the metadata directory.
const metadataFileName = "library.json"

// metadataSchemaVersion guards the identity file format. Bump on format
// changes; a file with a mismatched version is rejected rather than assumed.
const metadataSchemaVersion = 1

// errNoMetadata is returned when a path holds no Cairn metadata. It is wrapped
// by callers that need to distinguish "adoptable" from "not a library".
var errNoMetadata = errors.New("no cairn metadata at path")

// identity is the durable, portable identity of a library. It travels with the
// library directory itself, so identity never depends on the mount path.
type identity struct {
	ID            string    `json:"id"`
	SchemaVersion int       `json:"schema_version"`
	Name          string    `json:"name"`
	VolumeID      string    `json:"volume_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// metadataDir returns the absolute metadata directory for a library root.
func metadataDir(root string) string {
	return filepath.Join(root, cairnDirName)
}

// createIdentity writes a fresh identity file into root/.cairn/. The metadata
// directory is created as needed. Callers must already have validated the root.
// The on-disk identity is sealed with keys when encryption is enabled.
func createIdentity(root, name, volumeID string, keys *crypto.Keys) (*identity, error) {
	id := newID()
	now := time.Now().UTC()
	ident := &identity{
		ID:            id,
		SchemaVersion: metadataSchemaVersion,
		Name:          name,
		VolumeID:      volumeID,
		CreatedAt:     now,
	}
	dir := metadataDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create metadata directory %s: %w", dir, err)
	}
	if err := writeIdentityFile(dir, ident, keys); err != nil {
		return nil, err
	}
	return ident, nil
}

// loadIdentity reads and validates the identity file at root/.cairn/library.json.
// Sealed identities are decrypted with keys; the passphrase must therefore
// match the one used when the library was created or adopted.
func loadIdentity(root string, keys *crypto.Keys) (*identity, error) {
	path := filepath.Join(metadataDir(root), metadataFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errNoMetadata
		}
		return nil, fmt.Errorf("read identity %s: %w", path, err)
	}

	plain, err := keys.Open(data)
	if err != nil {
		return nil, fmt.Errorf("decrypt identity %s: %w", path, err)
	}

	var ident identity
	if err := json.Unmarshal(plain, &ident); err != nil {
		return nil, fmt.Errorf("parse identity %s: %w", path, err)
	}
	if ident.ID == "" {
		return nil, fmt.Errorf("identity %s is missing a library id", path)
	}
	if ident.SchemaVersion != metadataSchemaVersion {
		return nil, fmt.Errorf("identity %s uses schema version %d, want %d",
			path, ident.SchemaVersion, metadataSchemaVersion)
	}
	return &ident, nil
}

// writeIdentityFile persists the identity as pretty JSON, sealed when keys are
// enabled. The write is made atomic (temp file + rename) so a crash can never
// leave a truncated identity.
func writeIdentityFile(dir string, ident *identity, keys *crypto.Keys) error {
	data, err := json.MarshalIndent(ident, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize identity: %w", err)
	}
	data = append(data, '\n')
	data = keys.Seal(data)

	path := filepath.Join(dir, metadataFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write identity: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit identity: %w", err)
	}
	return nil
}

// hasMetadata reports whether root already carries a Cairn metadata directory.
func hasMetadata(root string) bool {
	_, err := os.Stat(filepath.Join(metadataDir(root), metadataFileName))
	return err == nil
}

// newID returns a new persistent identifier as 16 random bytes hex-encoded.
func newID() string {
	const n = 16
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

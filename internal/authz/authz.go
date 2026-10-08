// Package authz implements Cairn's resource-based authorization subsystem
// (ADR-0005).
//
// Authorization is a dedicated, first-class subsystem: never scattered
// `if admin` checks, never `return true` stubs. Permissions are grants on
// resources that inherit down the resource hierarchy:
//
//	Library
//	  └── Folder
//	       └── Folder
//	            └── Photo
//
// Grants are stored flat with path-like resource keys so evaluation is
// efficient (no recursive traversal of huge trees per request) and decisions
// can be cached with deterministic invalidation.
package authz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Jishnu-Prasad888/Cairn/internal/audit"
)

// Capability is a single operation a principal may perform on a resource.
// The set is closed; new operations must be added here first so the whole
// subsystem is aware of them.
type Capability string

const (
	// CapRead is listing, viewing, and metadata access.
	CapRead Capability = "read"
	// CapDownload is fetching the original media bytes.
	CapDownload Capability = "download"
	// CapCreate is creating new resources inside a container.
	CapCreate Capability = "create"
	// CapEdit is modifying an existing resource.
	CapEdit Capability = "edit"
	// CapMove is relocating a resource within the hierarchy.
	CapMove Capability = "move"
	// CapDelete is removing (or permanently deleting) a resource.
	CapDelete Capability = "delete"
	// CapShare is creating and managing shares rooted at the resource.
	CapShare Capability = "share"
	// CapManage is administering permissions and configuration for the
	// resource; it is the highest capability.
	CapManage Capability = "manage"
)

// AllCapabilities is every capability in the system, in canonical order.
var AllCapabilities = []Capability{
	CapRead, CapDownload, CapCreate, CapEdit, CapMove, CapDelete, CapShare, CapManage,
}

// Effect is whether a grant permits or forbids its capabilities.
type Effect string

const (
	// EffectAllow permits the granted capabilities.
	EffectAllow Effect = "allow"
	// EffectDeny forbids the granted capabilities. A deny on the same key
	// beats an inherited allow; a deny on a more specific key beats an allow
	// on an ancestor.
	EffectDeny Effect = "deny"
)

// CapSet is a set of capabilities with simple set operations.
type CapSet map[Capability]bool

// All reports whether every given capability is present.
func (c CapSet) All(caps ...Capability) bool {
	for _, cap := range caps {
		if !c[cap] {
			return false
		}
	}
	return true
}

// List returns the capabilities of the set in canonical order.
func (c CapSet) List() []Capability {
	var out []Capability
	for _, cap := range AllCapabilities {
		if c[cap] {
			out = append(out, cap)
		}
	}
	return out
}

// capSetFromStrings validates and normalizes a set of capability strings.
func capSetFromStrings(raw []string) (CapSet, error) {
	set := make(CapSet, len(raw))
	seen := make(map[Capability]bool, len(raw))
	var unknown []string
	for _, r := range raw {
		c := Capability(strings.ToLower(strings.TrimSpace(r)))
		switch c {
		case CapRead, CapDownload, CapCreate, CapEdit, CapMove, CapDelete, CapShare, CapManage:
		default:
			unknown = append(unknown, r)
			continue
		}
		if !seen[c] {
			seen[c] = true
			set[c] = true
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown capability %q", strings.Join(unknown, ", "))
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("at least one capability is required")
	}
	return set, nil
}

// CapSetFromStrings is the exported validation used by the HTTP layer.
func CapSetFromStrings(raw []string) (CapSet, error) { return capSetFromStrings(raw) }

// ResourceType identifies the kind of resource a capability applies to.
type ResourceType string

const (
	ResourceLibrary ResourceType = "library"
	ResourceFolder  ResourceType = "folder"
	ResourceFile    ResourceType = "file"
	ResourceAlbum   ResourceType = "album"
	ResourceMemory  ResourceType = "memory"
	ResourceTag     ResourceType = "tag"
)

// keySep separates the levels of a resource key.
const keySep = "/"

// Type markers in resource keys. A library key is its bare id and every other
// resource hangs beneath it, so grants inherit by string prefix:
//
//	L                 the library
//	L/f:dir           a folder
//	L/f:dir/f:sub     a nested folder
//	L/f:dir/x:file    a file
//	L/x:file          a file at the library root
//	L/a:albumID       an album
//	L/m:memoryID      a memory
//	L/t:tagID         a tag
//
// The markers make folders, files, and entities mutually unambiguous on a
// single key space while remaining a plain string prefix path.
const (
	markerFolder = "f:"
	markerFile   = "x:"
	markerAlbum  = "a:"
	markerMemory = "m:"
	markerTag    = "t:"
)

// LibraryKey returns the key of a library: the library itself is the root of
// its subtree, so every descendant has this key as a prefix.
func LibraryKey(libID string) string { return libID }

// FolderKey builds a folder key from its path relative to the library root,
// with the library prefix and per-level folder markers.
// Example: ("lib-1", "2024/Japan") -> "lib-1/f:2024/f:Japan".
func FolderKey(libID, relPath string) string {
	if libID == "" {
		return ""
	}
	parts := strings.Split(relPath, keySep)
	for i, p := range parts {
		parts[i] = markerFolder + p
	}
	return libID + keySep + strings.Join(parts, keySep)
}

// FileKey builds a file key from its path relative to the library root. The
// final component carries the file marker; parent components keep the folder
// marker. Example: ("lib-1", "2024/Japan/img.jpg") ->
// "lib-1/f:2024/f:Japan/x:img.jpg".
func FileKey(libID, relPath string) string {
	if libID == "" {
		return ""
	}
	idx := strings.LastIndex(relPath, keySep)
	if idx < 0 {
		return libID + keySep + markerFile + relPath
	}
	return FolderKey(libID, relPath[:idx]) + keySep + markerFile + relPath[idx+len(keySep):]
}

// EntityKey builds a key for a library-scoped non-filesystem entity (album,
// memory, tag). These hang off the library and inherit its grants but are not
// part of the folder tree.
func EntityKey(prefix, libID, id string) string {
	return libID + keySep + prefix + ":" + id
}

// ParseKeyType reports the resource type encoded in a key. Used by the audit
// layer and share targets for human-readable context.
func ParseKeyType(key string) ResourceType {
	switch {
	case strings.Contains(key, keySep+markerAlbum):
		return ResourceAlbum
	case strings.Contains(key, keySep+markerMemory):
		return ResourceMemory
	case strings.Contains(key, keySep+markerTag):
		return ResourceTag
	case strings.Contains(key, keySep+markerFile):
		return ResourceFile
	case strings.Contains(key, keySep+markerFolder):
		return ResourceFolder
	default:
		return ResourceLibrary
	}
}

// --- storage model ---

// grant is a persisted permission row.
type grant struct {
	ID           string
	UserID       string
	ResourceKey  string
	Capabilities CapSet
	Effect       Effect
	CreatedAt    time.Time
}

// Share is a persisted public share. Only TokenHash (or PasswordHash) are ever
// persisted; the raw share token is returned to the creator exactly once.
type Share struct {
	ID           string
	ResourceKey  string
	Capabilities CapSet
	TokenHash    string
	PasswordHash sql.NullString
	ExpiresAt    *time.Time
	RevokedAt    *time.Time
	CreatedBy    string
	CreatedAt    time.Time
}

// grantCacheKey identifies a principal's cached grant rows.
type grantCacheKey struct {
	userID string
}

// cachedGrants binds a grant list to the service generation that produced it.
type cachedGrants struct {
	generation int64
	grants     []grant
}

// decisionCacheKey identifies a cached policy decision for one principal on
// one resource.
type decisionCacheKey struct {
	userID string
	key    string
}

// ValidationError describes an invalid grant or share operation.
type ValidationError struct{ Field, Reason string }

func (e *ValidationError) Error() string { return fmt.Sprintf("invalid %s: %s", e.Field, e.Reason) }

// Sentinel errors surfaced by the authorization service.
var (
	ErrNotFound     = fmt.Errorf("not found")
	ErrConflict     = fmt.Errorf("conflict")
	ErrUnauthorized = fmt.Errorf("unauthorized")
	// ErrUserNotFound reports a grant/serve user id that does not exist.
	ErrUserNotFound = fmt.Errorf("user not found")
)

// Service is the resource-based authorization engine. It owns the persisted
// grants and shares, evaluates decisions with a bounded internal cache, and
// records permission/share events to the audit log.
type Service struct {
	db     *sql.DB
	logger *slog.Logger
	audit  *audit.Service

	// generation is bumped on every grant or share mutation. Cached decisions
	// are tagged with the generation that produced them; a mismatch forces a
	// recomputation, providing deterministic invalidation.
	generation int64

	mu      sync.RWMutex
	grants  map[grantCacheKey]cachedGrants
	decides map[decisionCacheKey]decision
}

// decision is a cached evaluation for a single (principal, resource).
type decision struct {
	caps       CapSet
	generation int64
}

// NewService builds an authorization Service over the server database. The
// optional audit service records permission and share events; audience-heavy
// tests may pass nil.
func NewService(db *sql.DB, logger *slog.Logger, audit *audit.Service) *Service {
	return &Service{
		db:      db,
		logger:  logger,
		audit:   audit,
		grants:  make(map[grantCacheKey]cachedGrants),
		decides: make(map[decisionCacheKey]decision),
	}
}

// UserExists reports whether a user account with the given id exists. Grant
// creation requires it so permissions never reference phantom accounts.
func (s *Service) UserExists(ctx context.Context, userID string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ?`, userID).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("lookup user: %w", err)
	}
	return true, nil
}

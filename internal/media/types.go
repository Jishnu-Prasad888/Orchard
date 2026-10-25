// Package media provides the domain types and repository for Orchard's media and
// file browsing layer. It sits on top of the per-library SQLite database and
// the indexed_files table that the indexer maintains.
//
// Design principles:
//   - Never load large file content into memory; use streaming I/O.
//   - Paths are always relative to the library root inside the package;
//     absolute paths are constructed by callers from Library.Root + RelPath.
//   - Soft-delete (trash) is the default; permanent delete is explicit.
//   - The indexer is the authority on file presence; media operations
//     update both the filesystem and the index atomically where possible.
package media

import (
	"errors"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jishnu-Prasad888/Orchard/internal/sanitize"
)

// --- sentinel errors ---

var (
	ErrNotFound       = errors.New("file not found")
	ErrFolderNotFound = errors.New("folder not found")
	ErrAlreadyExists  = errors.New("file already exists at destination")
	ErrInTrash        = errors.New("file is in trash")
	ErrNotInTrash     = errors.New("file is not in trash")
	ErrPathTraversal  = errors.New("path traversal detected")
	ErrUploadTooLarge = errors.New("upload exceeds the configured size limit")
	ErrNotABook       = errors.New("only EPUB and PDF files can be added to a library")
)

// --- books ---

// BookExtensions are the only files Orchard indexes, lists, and accepts:
// Orchard is a reading library, and everything else on a library's disk
// (installers, AppImages, archives, photos) stays there untouched but is
// never scanned, hashed, or shown.
var BookExtensions = []string{"epub", "pdf"}

// IsBook reports whether relPath names a file Orchard indexes, judged by its
// extension, case-insensitively ("Novel.EPUB" counts).
func IsBook(relPath string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(relPath), "."))
	for _, b := range BookExtensions {
		if ext == b {
			return true
		}
	}
	return false
}

// --- media type ---

// MediaType classifies a file by its content type for filtering purposes.
type MediaType string

const (
	MediaTypePhoto    MediaType = "photo"
	MediaTypeVideo    MediaType = "video"
	MediaTypeAudio    MediaType = "audio"
	MediaTypeDocument MediaType = "document"
	MediaTypeOther    MediaType = "other"
)

// photoExts lists common image file extensions (lowercase).
var photoExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".bmp": true, ".tiff": true, ".tif": true,
	".heic": true, ".heif": true, ".avif": true, ".raw": true,
	".cr2": true, ".cr3": true, ".nef": true, ".arw": true,
	".dng": true, ".orf": true, ".rw2": true,
}

var videoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".mov": true, ".avi": true,
	".wmv": true, ".flv": true, ".webm": true, ".m4v": true,
	".mpg": true, ".mpeg": true, ".3gp": true, ".ogv": true,
	".ts": true, ".mts": true, ".m2ts": true,
}

var audioExts = map[string]bool{
	".mp3": true, ".flac": true, ".aac": true, ".ogg": true,
	".opus": true, ".wav": true, ".m4a": true, ".wma": true,
	".aiff": true, ".alac": true,
}

var documentExts = map[string]bool{
	".pdf": true, ".doc": true, ".docx": true, ".odt": true,
	".xls": true, ".xlsx": true, ".ods": true, ".csv": true,
	".ppt": true, ".pptx": true, ".odp": true, ".txt": true,
	".md": true, ".rtf": true, ".epub": true,
}

// DetectMediaType returns the MediaType for a file based on its extension.
func DetectMediaType(relPath string) MediaType {
	ext := strings.ToLower(filepath.Ext(relPath))
	switch {
	case photoExts[ext]:
		return MediaTypePhoto
	case videoExts[ext]:
		return MediaTypeVideo
	case audioExts[ext]:
		return MediaTypeAudio
	case documentExts[ext]:
		return MediaTypeDocument
	default:
		return MediaTypeOther
	}
}

// DetectMIME returns a best-effort MIME type for a file path.
func DetectMIME(relPath string) string {
	ext := strings.ToLower(filepath.Ext(relPath))
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	switch DetectMediaType(relPath) {
	case MediaTypePhoto:
		return "image/jpeg"
	case MediaTypeVideo:
		return "video/mp4"
	case MediaTypeAudio:
		return "audio/mpeg"
	default:
		return "application/octet-stream"
	}
}

// --- File ---

// File is the API representation of an indexed file. All paths are relative to
// the library root. AbsPath is the derived absolute path; it is never stored.
type File struct {
	ID          string
	LibraryID   string
	RelPath     string
	Name        string // filepath.Base(RelPath)
	FolderPath  string // filepath.Dir(RelPath), "." for root
	SizeBytes   int64
	ModTime     time.Time
	ContentHash string
	MediaType   MediaType
	MIMEType    string
	Status      FileStatus
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// FileStatus mirrors indexer.Status but is defined here to avoid circular deps.
type FileStatus string

const (
	FileStatusPresent FileStatus = "present"
	FileStatusMissing FileStatus = "missing"
	FileStatusDeleted FileStatus = "deleted"
)

// --- Folder ---

// Folder represents a directory path within a library.
type Folder struct {
	ID        string
	LibraryID string
	RelPath   string
	ParentID  string
	Name      string
	FileCount int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// --- TrashEntry ---

// TrashEntry records a soft-deleted file.
type TrashEntry struct {
	FileID       string
	OriginalPath string
	TrashPath    string
	DeletedAt    time.Time
	DeletedBy    string
}

// --- ListOptions ---

// SortField specifies which field to sort file listings by.
type SortField string

const (
	SortByName    SortField = "name"
	SortBySize    SortField = "size"
	SortByModTime SortField = "mod_time"
	// SortByReading puts the files UserID opened most recently first, then
	// the ones they never opened, newest first. Order does not apply.
	SortByReading SortField = "reading"
)

// SortOrder is ascending or descending.
type SortOrder string

const (
	SortAsc  SortOrder = "asc"
	SortDesc SortOrder = "desc"
)

// ListOptions controls pagination, filtering, and sorting for file listings.
type ListOptions struct {
	// FolderPath filters to files directly inside this folder path.
	// Empty string or "." means the library root level only.
	// Set Recursive=true to include all descendants.
	FolderPath string
	Recursive  bool

	// Type filters to files of a specific MediaType. Empty means all.
	Type MediaType

	// Extensions filters to files ending in one of these (see
	// ParseExtensions). Empty means all.
	Extensions []string

	// Status filters by file status. Defaults to "present" when empty.
	Status FileStatus

	// Sort controls ordering.
	Sort  SortField
	Order SortOrder

	// UserID is whose opens SortByReading orders by.
	UserID string

	// Cursor-based pagination: Cursor is the last seen rel_path from the
	// previous page. Empty means start from the beginning.
	Cursor string
	Limit  int // default 50, max 200
}

// ParseExtensions reads a comma-separated extension list ("pdf,epub") from a
// query string, lowercased and without dots. Anything that is not a short
// run of letters and digits is dropped, so the result is safe in a LIKE.
func ParseExtensions(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(part), "."))
		if ext == "" || len(ext) > 10 || strings.IndexFunc(ext, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9')
		}) >= 0 {
			continue
		}
		out = append(out, ext)
		if len(out) == 16 {
			break
		}
	}
	return out
}

// ExtensionCondition is the WHERE fragment matching col against any of exts
// (from ParseExtensions), or "" when exts is empty. LIKE is case-insensitive
// for ASCII in SQLite, so "Book.PDF" matches "pdf".
func ExtensionCondition(col string, exts []string) (string, []any) {
	if len(exts) == 0 {
		return "", nil
	}
	ors := make([]string, len(exts))
	args := make([]any, len(exts))
	for i, ext := range exts {
		ors[i] = col + " LIKE ?"
		args[i] = "%." + ext
	}
	return "(" + strings.Join(ors, " OR ") + ")", args
}

// Defaults fills in zero values with sensible defaults.
func (o *ListOptions) Defaults() {
	if o.Status == "" {
		o.Status = FileStatusPresent
	}
	if o.Sort == "" {
		o.Sort = SortByName
	}
	if o.Order == "" {
		o.Order = SortAsc
	}
	if o.Limit <= 0 {
		o.Limit = 50
	}
	if o.Limit > 200 {
		o.Limit = 200
	}
}

// Page is one page of file listing results.
type Page struct {
	Files      []*File
	NextCursor string // empty when this is the last page
	Total      int    // approximate total matching (without pagination)
}

// --- path safety ---

// SafeRelPath validates that a caller-supplied relative path does not escape
// the library root via traversal sequences. Returns a cleaned forward-slash
// path on success. It is a thin wrapper over the shared sanitize package so a
// single rule set is enforced by every entry point; ErrPathTraversal is
// wrapped for the HTTP layer's error mapping.
func SafeRelPath(raw string) (string, error) {
	safe, err := sanitize.RelPath(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrPathTraversal, err)
	}
	return safe, nil
}

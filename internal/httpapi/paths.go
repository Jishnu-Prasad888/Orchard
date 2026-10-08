package httpapi

import (
	"github.com/Jishnu-Prasad888/Cairn/internal/sanitize"
)

// canonicalFolder normalizes a folder query parameter for use in both
// authorization keys and SQL filters. Empty and "." mean the library root and
// stay as "". Any other value is validated with the shared sanitizer so the
// same canonical string is used everywhere. Returns ok=false for values that
// would be traversal, absolute, or target the metadata directory.
func canonicalFolder(raw string) (string, bool) {
	if raw == "" || raw == "." {
		return "", true
	}
	clean, err := sanitize.RelPath(raw)
	if err != nil {
		return "", false
	}
	return clean, true
}

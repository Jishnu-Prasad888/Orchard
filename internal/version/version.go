// Package version holds build-time metadata for the Cairn binary.
//
// The fields in this package are intended to be overridden at build time via
// the -ldflags flag. See the Makefile's `build` target for details.
package version

import (
	"fmt"
	"runtime"
	"strings"
)

// These values may be set at build time:
//
//	-X "github.com/Jishnu-Prasad888/Cairn/internal/version.Version=..."
var (
	// Version is the semantic version of the build. "dev" indicates a
	// local development build.
	Version = "dev"

	// Commit is the git commit hash the binary was built from.
	Commit = "none"

	// BuildDate is the UTC build timestamp.
	BuildDate = "unknown"
)

// Info describes the running binary.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// Get returns the version information for the running binary.
func Get() Info {
	return Info{
		Version:   strings.TrimSpace(Version),
		Commit:    strings.TrimSpace(Commit),
		BuildDate: strings.TrimSpace(BuildDate),
		GoVersion: runtime.Version(),
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

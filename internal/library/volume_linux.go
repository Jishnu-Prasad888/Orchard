//go:build linux

package library

import (
	"fmt"
	"syscall"
)

// volumeID returns a best-effort filesystem volume identifier for the given
// path, and false when the platform or filesystem does not expose one. It is a
// secondary identity signal used to recognize a library when paths differ. The
// stable, primary identity always remains the id inside <root>/.cairn/library.json.
func volumeID(path string) (string, bool) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return "", false
	}
	if st.Dev == 0 {
		return "", false
	}
	return fmt.Sprintf("%x", uint64(st.Dev)), true
}

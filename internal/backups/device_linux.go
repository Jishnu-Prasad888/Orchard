//go:build linux

package backups

import (
	"syscall"
)

// deviceID returns the st_dev of the filesystem containing path.
func deviceID(path string) (uint64, bool) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, false
	}
	return uint64(st.Dev), true
}

//go:build linux || darwin || freebsd || openbsd || netbsd

package uploads

import "syscall"

// FreeBytes reports the space available to unprivileged writers on the
// filesystem holding path. ok is false when it cannot be determined.
func FreeBytes(path string) (free int64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true //nolint:unconvert // Bsize's type varies by platform
}

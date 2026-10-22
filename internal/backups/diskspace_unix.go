//go:build linux || darwin || freebsd || openbsd || netbsd

package backups

import "syscall"

// diskSpace is the total and free bytes of the filesystem holding path.
func diskSpace(path string) (int64, int64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	return int64(st.Blocks) * int64(st.Bsize), int64(st.Bavail) * int64(st.Bsize) //nolint:unconvert // field types vary by platform
}

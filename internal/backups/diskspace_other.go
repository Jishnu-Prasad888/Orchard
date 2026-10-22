//go:build !(linux || darwin || freebsd || openbsd || netbsd)

package backups

func diskSpace(string) (int64, int64) { return 0, 0 }

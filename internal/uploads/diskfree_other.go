//go:build !(linux || darwin || freebsd || openbsd || netbsd)

package uploads

// FreeBytes cannot tell on this platform; callers skip the check.
func FreeBytes(string) (int64, bool) { return 0, false }

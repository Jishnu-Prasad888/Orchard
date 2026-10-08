//go:build !linux

package backups

// deviceID is unavailable off Linux; callers treat it as unknown (false).
func deviceID(path string) (uint64, bool) { return 0, false }

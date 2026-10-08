//go:build !linux && !darwin

package library

// volumeID is unavailable on this platform; callers treat the absent volume
// identity as "unknown" and rely on the .cairn identity file alone.
func volumeID(string) (string, bool) {
	return "", false
}

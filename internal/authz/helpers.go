package authz

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// newID returns a cryptographically random hex identifier. On entropy failure
// it degrades to a time-based fallback rather than failing the operation.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("id-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// rfc3339 renders a timestamp the way every Cairn table stores it.
func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// parseTime reads a stored RFC3339Nano timestamp. A zero/empty value yields
// the zero time, matching NULL columns that carry optional timestamps.
func parseTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored timestamp %q: %w", value, err)
	}
	return t, nil
}

// capCSV renders a capability set as a comma-separated string for storage.
func capCSV(c CapSet) string {
	return strings.Join(capStrings(c), ",")
}

// capStrings renders a capability set in canonical order.
func capStrings(c CapSet) []string {
	list := c.List()
	out := make([]string, len(list))
	for i, cap := range list {
		out[i] = string(cap)
	}
	return out
}

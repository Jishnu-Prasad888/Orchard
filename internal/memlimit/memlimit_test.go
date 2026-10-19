package memlimit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMeminfoTotal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	_ = os.WriteFile(path, []byte("MemTotal:        3884732 kB\nMemFree:  1000 kB\n"), 0o644)
	if got := meminfoTotal(path); got != 3884732<<10 {
		t.Fatalf("total = %d", got)
	}
	if got := meminfoTotal(filepath.Join(t.TempDir(), "missing")); got != 0 {
		t.Fatalf("missing file = %d", got)
	}
}

func TestParseLimit(t *testing.T) {
	for in, want := range map[string]int64{
		"max\n":                 0,
		"536870912\n":           536870912,
		"9223372036854771712\n": 0, // cgroup v1 "no limit"
		"":                      0,
		"garbage":               0,
	} {
		if got := parseLimit(in); got != want {
			t.Errorf("parseLimit(%q) = %d, want %d", in, got, want)
		}
	}
}

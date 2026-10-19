// Package memlimit gives the Go runtime a soft memory limit sized to the
// machine, unless the operator set one.
//
// Without a limit, Go lets the heap grow to twice what is live before
// collecting, and keeps memory it no longer needs mapped for a while. After a
// spike (rendering a big PDF's cover, decoding a large photo) a Raspberry Pi
// can sit on hundreds of megabytes it is not using. A soft limit makes the
// garbage collector work harder only as memory approaches it, and return
// memory to the OS sooner. It is a target, not a cap: it cannot stop the
// process growing past it if that much is genuinely in use.
package memlimit

import (
	"bufio"
	"log/slog"
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// Share is the fraction of the machine's (or container's) memory Orchard
// aims to stay under, leaving the rest to the OS page cache — which serves
// photos and the databases — and to anything else on the device.
const Share = 0.5

// minLimit keeps a tiny or misreported memory size from setting a limit so
// low the collector would run constantly.
const minLimit = 128 << 20

// Apply sets the default limit and reports what it did. It does nothing
// when GOMEMLIMIT is set or the available memory cannot be determined.
func Apply(logger *slog.Logger) {
	if os.Getenv("GOMEMLIMIT") != "" {
		logger.Info("memory limit set by GOMEMLIMIT", "limit_bytes", debug.SetMemoryLimit(-1))
		return
	}
	total := available()
	if total <= 0 {
		return
	}
	limit := max(int64(float64(total)*Share), minLimit)
	debug.SetMemoryLimit(limit)
	logger.Info("memory limit set", "limit_mib", limit>>20, "of_mib", total>>20,
		"hint", "set GOMEMLIMIT to override")
}

// available is the smaller of the machine's RAM and a cgroup (container)
// memory limit, in bytes, or 0 if unknown.
func available() int64 {
	total := meminfoTotal("/proc/meminfo")
	if c := cgroupLimit(); c > 0 && (total == 0 || c < total) {
		total = c
	}
	return total
}

// meminfoTotal reads MemTotal from a /proc/meminfo-style file.
func meminfoTotal(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil || kb <= 0 {
				return 0
			}
			return kb << 10
		}
	}
	_ = sc.Err() // a read error just means "unknown"
	return 0
}

// cgroupLimit reads a container's memory limit (cgroup v2, then v1).
func cgroupLimit() int64 {
	for _, path := range []string{
		"/sys/fs/cgroup/memory.max",
		"/sys/fs/cgroup/memory/memory.limit_in_bytes",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return parseLimit(string(raw))
	}
	return 0
}

// parseLimit reads a cgroup limit value; "max" or an absurd v1 sentinel
// means unlimited (0).
func parseLimit(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "max" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n >= math.MaxInt64/2 {
		return 0
	}
	return n
}

// Package metrics exposes lightweight metrics for the Cairn server in the
// Prometheus text exposition format.
//
// The package deliberately has no third-party dependencies: the signals needed
// to operate a self-hosted server — request throughput, latency distribution,
// in-flight requests, process uptime, and Go runtime state — are cheap to
// maintain with the standard library, and avoiding a metrics dependency keeps
// the single-binary build small and CGO-free.
//
// The exposed names follow the Prometheus conventions so the endpoint can be
// scraped by Prometheus, Grafana Agent, or simple curl-based checks without
// translation.
package metrics

import (
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// durationBuckets are the fixed upper bounds (in seconds) of the request
// latency histogram. Values map to the widest bucket not smaller than the
// observed duration, mirroring the standard Prometheus histogram model.
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Name is the metrics namespace prefix for all Cairn metrics.
const Name = "cairn"

// Registry accumulates metrics for the server process. A zero-value Registry
// is not usable; use New.
type Registry struct {
	start  time.Time
	mu     sync.Mutex
	series map[string]*atomic.Uint64 // "method\x00status" -> request counter

	inFlight atomic.Int64

	// Histogram state: cumulative per-bucket counters plus total count and
	// total duration sum (nanoseconds), so Prometheus can compute quantiles.
	histBuckets []*atomic.Uint64
	histCount   atomic.Uint64
	histSumNS   atomic.Uint64
}

// New returns an empty Registry with the histogram buckets initialized.
func New() *Registry {
	r := &Registry{
		start:  time.Now(),
		series: make(map[string]*atomic.Uint64),
	}
	for range durationBuckets {
		r.histBuckets = append(r.histBuckets, &atomic.Uint64{})
	}
	return r
}

// Observe records one completed HTTP request. method and status label the
// request counter; dur is the handler latency.
func (r *Registry) Observe(method string, status int, dur time.Duration) {
	key := method + "\x00" + itoa(status)
	r.mu.Lock()
	c, ok := r.series[key]
	if !ok {
		c = &atomic.Uint64{}
		r.series[key] = c
	}
	r.mu.Unlock()
	c.Add(1)

	r.histCount.Add(1)
	r.histSumNS.Add(uint64(dur.Nanoseconds()))
	if n := dur.Nanoseconds(); n >= 0 {
		secs := float64(n) / 1e9
		// Prometheus histograms are cumulative: every bucket whose upper
		// bound is >= the observation is incremented.
		for i, b := range durationBuckets {
			if secs <= b {
				r.histBuckets[i].Add(1)
			}
		}
	}
}

// AddInFlight increments or decrements the number of requests currently being
// handled. The HTTP middleware calls it with 1 on entry and -1 on exit.
func (r *Registry) AddInFlight(delta int64) {
	r.inFlight.Add(delta)
}

// Handler returns an http.Handler that renders the registry in Prometheus
// text format. It is safe for concurrent scraping.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(r.Render())
	})
}

// Render returns the current metric set in Prometheus text format. The output
// is deterministic: series are emitted in sorted order and every metric is
// guarded by its HELP/TYPE lines.
func (r *Registry) Render() []byte {
	var b strings.Builder

	// --- HTTP request totals ---
	b.WriteString("# HELP cairn_http_requests_total Total HTTP requests by method and status code.\n")
	b.WriteString("# TYPE cairn_http_requests_total counter\n")
	r.mu.Lock()
	keys := make([]string, 0, len(r.series))
	for k := range r.series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		method, status := splitSeriesKey(k)
		fmt.Fprintf(&b, "cairn_http_requests_total{method=%q,status=%q} %d\n",
			method, status, r.series[k].Load())
	}
	r.mu.Unlock()

	// --- Latency histogram ---
	b.WriteString("# HELP cairn_http_request_duration_seconds HTTP request latency.\n")
	b.WriteString("# TYPE cairn_http_request_duration_seconds histogram\n")
	for i, bound := range durationBuckets {
		fmt.Fprintf(&b, "cairn_http_request_duration_seconds_bucket{le=%q} %d\n",
			strconvFormatFloat(bound), r.histBuckets[i].Load())
	}
	fmt.Fprintf(&b, "cairn_http_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", r.histCount.Load())
	sum := float64(r.histSumNS.Load()) / 1e9
	fmt.Fprintf(&b, "cairn_http_request_duration_seconds_sum %s\n", strconvFormatFloat(sum))
	fmt.Fprintf(&b, "cairn_http_request_duration_seconds_count %d\n", r.histCount.Load())

	// --- In-flight gauge ---
	b.WriteString("# HELP cairn_http_requests_in_flight HTTP requests currently being handled.\n")
	b.WriteString("# TYPE cairn_http_requests_in_flight gauge\n")
	fmt.Fprintf(&b, "cairn_http_requests_in_flight %d\n", r.inFlight.Load())

	// --- Process info ---
	b.WriteString("# HELP cairn_process_start_time_seconds Start time of the process in unix seconds.\n")
	b.WriteString("# TYPE cairn_process_start_time_seconds gauge\n")
	fmt.Fprintf(&b, "cairn_process_start_time_seconds %d\n", r.start.Unix())
	b.WriteString("# HELP cairn_process_uptime_seconds Seconds since the server process started.\n")
	b.WriteString("# TYPE cairn_process_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "cairn_process_uptime_seconds %d\n", int64(time.Since(r.start).Seconds()))

	// --- Go runtime gauges ---
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	b.WriteString("# HELP go_goroutines Number of goroutines that currently exist.\n")
	b.WriteString("# TYPE go_goroutines gauge\n")
	fmt.Fprintf(&b, "go_goroutines %d\n", runtime.NumGoroutine())
	b.WriteString("# HELP go_memstats_alloc_bytes Number of bytes allocated and still in use.\n")
	b.WriteString("# TYPE go_memstats_alloc_bytes gauge\n")
	fmt.Fprintf(&b, "go_memstats_alloc_bytes %d\n", mem.Alloc)
	b.WriteString("# HELP go_memstats_heap_alloc_bytes Number of heap bytes allocated and still in use.\n")
	b.WriteString("# TYPE go_memstats_heap_alloc_bytes gauge\n")
	fmt.Fprintf(&b, "go_memstats_heap_alloc_bytes %d\n", mem.HeapAlloc)
	b.WriteString("# HELP go_memstats_heap_objects Number of allocated objects.\n")
	b.WriteString("# TYPE go_memstats_heap_objects gauge\n")
	fmt.Fprintf(&b, "go_memstats_heap_objects %d\n", mem.HeapObjects)
	b.WriteString("# HELP go_memstats_num_gc Number of completed GC cycles.\n")
	b.WriteString("# TYPE go_memstats_num_gc gauge\n")
	fmt.Fprintf(&b, "go_memstats_num_gc %d\n", mem.NumGC)

	return []byte(b.String())
}

// itoa formats a small integer without allocating via strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func splitSeriesKey(k string) (method, status string) {
	if i := strings.IndexByte(k, '\x00'); i >= 0 {
		return k[:i], k[i+1:]
	}
	return k, ""
}

// strconvFormatFloat renders a float64 without scientific notation, matching
// the Go formatting Prometheus clients expect in the text format.
func strconvFormatFloat(f float64) string {
	return fmt.Sprintf("%v", f)
}

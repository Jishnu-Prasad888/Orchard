package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestObserveAndRender(t *testing.T) {
	r := New()
	r.AddInFlight(1)
	r.Observe("GET", 200, 4*time.Millisecond)
	r.Observe("GET", 200, 120*time.Millisecond)
	r.Observe("POST", 409, 2*time.Second)
	r.AddInFlight(-1)

	out := string(r.Render())

	// Series are labelled by method and exact status.
	if !strings.Contains(out, `cairn_http_requests_total{method="GET",status="200"} 2`) {
		t.Errorf("missing GET/200 counter: %s", out)
	}
	if !strings.Contains(out, `cairn_http_requests_total{method="POST",status="409"} 1`) {
		t.Errorf("missing POST/409 counter: %s", out)
	}

	// Histogram: 2s request lands in the 2.5s bucket; 120ms in the 0.25s one.
	if !strings.Contains(out, `cairn_http_request_duration_seconds_bucket{le="0.25"} 2`) {
		t.Errorf("expected 2 requests in the 0.25s bucket: %s", out)
	}
	if !strings.Contains(out, `cairn_http_request_duration_seconds_bucket{le="2.5"} 3`) {
		t.Errorf("expected all 3 requests in the 2.5s bucket: %s", out)
	}
	if !strings.Contains(out, `cairn_http_request_duration_seconds_bucket{le="+Inf"} 3`) {
		t.Errorf("expected +Inf bucket to equal count: %s", out)
	}
	if !strings.Contains(out, "cairn_http_request_duration_seconds_count 3") {
		t.Errorf("expected count 3: %s", out)
	}

	// Gauge restored after the paired in-flight calls.
	if !strings.Contains(out, "cairn_http_requests_in_flight 0") {
		t.Errorf("expected in-flight 0: %s", out)
	}

	// Process and runtime gauges always present.
	for _, want := range []string{
		"cairn_process_start_time_seconds",
		"cairn_process_uptime_seconds",
		"go_goroutines",
		"go_memstats_alloc_bytes",
		"go_memstats_heap_objects",
		"go_memstats_num_gc",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in output", want)
		}
	}

	// Every series must be guarded by HELP and TYPE lines (order-free check).
	for _, name := range []string{
		"cairn_http_requests_total",
		"cairn_http_request_duration_seconds",
		"cairn_http_requests_in_flight",
		"cairn_process_start_time_seconds",
		"cairn_process_uptime_seconds",
	} {
		if !strings.Contains(out, "# HELP "+name) {
			t.Errorf("missing HELP for %s", name)
		}
		if !strings.Contains(out, "# TYPE "+name) {
			t.Errorf("missing TYPE for %s", name)
		}
	}
}

func TestDeterministicSeriesOrder(t *testing.T) {
	r := New()
	r.Observe("GET", 404, time.Millisecond)
	r.Observe("POST", 200, time.Millisecond)
	r.Observe("GET", 200, time.Millisecond)

	// Series emitted by the registry (request counters, histogram, gauges) are
	// sorted; only the runtime gauges (goroutines, memstats, uptime) drift
	// between renders. Extract the deterministic prefix and compare.
	splitAt := "# HELP go_goroutines "
	first := string(r.Render())
	second := string(r.Render())
	if i := strings.Index(first, splitAt); i >= 0 {
		first = first[:i]
	}
	if i := strings.Index(second, splitAt); i >= 0 {
		second = second[:i]
	}
	if first != second {
		t.Fatal("request-derived series must render byte-identically")
	}
}

func TestConcurrentObserve(t *testing.T) {
	r := New()
	const workers = 8
	const perWorker = 500
	done := make(chan struct{}, workers)
	for range workers {
		go func() {
			defer func() { done <- struct{}{} }()
			for range perWorker {
				r.Observe("GET", 200, time.Microsecond)
			}
		}()
	}
	for range workers {
		<-done
	}
	out := string(r.Render())
	if !strings.Contains(out, `cairn_http_requests_total{method="GET",status="200"} 4000`) {
		t.Errorf("expected exactly 4000 requests recorded, got: %s", out)
	}
}

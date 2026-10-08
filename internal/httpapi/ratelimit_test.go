package httpapi

import (
	"net/http"
	"testing"
	"time"
)

func fixedClock(t0 time.Time) *rateLimiter {
	return &rateLimiter{buckets: map[string]*clientBucket{}, now: func() time.Time { return t0 }}
}

func TestRateLimiterAllowsBurstThenThrottles(t *testing.T) {
	clock := time.Now()
	rl := fixedClock(clock)
	// Full capacity first.
	for i := 0; i < rateLimitCapacity; i++ {
		if ok, _ := rl.allow("203.0.113.9", "bob", true); !ok {
			t.Fatalf("request %d within burst should be allowed", i)
		}
	}
	if ok, _ := rl.allow("203.0.113.9", "bob", true); ok {
		t.Fatal("request past capacity must be denied")
	}
}

func TestRateLimiterRefills(t *testing.T) {
	clock := time.Now()
	rl := fixedClock(clock)
	for i := 0; i < rateLimitCapacity; i++ {
		rl.allow("10.0.0.1", "bob", true)
	}
	// Advance past a full refill interval.
	rl.now = func() time.Time { return clock.Add(3 * rateLimitRefill) }
	if ok, _ := rl.allow("10.0.0.1", "bob", true); !ok {
		t.Fatal("request after three refill windows must be allowed")
	}
}

func TestRateLimiterLockout(t *testing.T) {
	clock := time.Now()
	rl := fixedClock(clock)
	ip := "198.51.100.7"
	// Fail up to (but not past) the threshold.
	for i := 0; i < rateLimitFailureThreshold-1; i++ {
		if _, atLimit := rl.recordFailure(ip, "alice"); atLimit {
			t.Fatalf("locked out at %d failures, want threshold %d", i+1, rateLimitFailureThreshold)
		}
	}
	if ok, _ := rl.allow(ip, "alice", true); !ok {
		t.Fatal("not yet at threshold; still allowed")
	}
	_, atLimit := rl.recordFailure(ip, "alice")
	if !atLimit {
		t.Fatal("threshold crossing must lock out")
	}
	if ok, _ := rl.allow(ip, "alice", true); ok {
		t.Fatal("locked-out client must be denied")
	}
	// A different username from the same IP is unaffected.
	if ok, _ := rl.allow(ip, "bob", true); !ok {
		t.Fatal("unrelated username on same IP must not be locked out")
	}
}

func TestRateLimiterSuccessResetsLockout(t *testing.T) {
	clock := time.Now()
	rl := fixedClock(clock)
	ip := "6.6.6.6"
	for i := 0; i < rateLimitFailureThreshold; i++ {
		rl.recordFailure(ip, "carol")
	}
	// Lockout expired.
	rl.now = func() time.Time { return clock.Add(rateLimitLockout + time.Second) }
	rl.recordSuccess(ip, "carol")
	if ok, _ := rl.allow(ip, "carol", true); !ok {
		t.Fatal("success after expired lockout must clear the counter")
	}
}

// TestHandleLoginRateLimit exercises the wiring end to end: the first batch
// of bad attempts get 401, then the client is throttled with 429.
func TestHandleLoginRateLimit(t *testing.T) {
	handler, client := newAuthTestServer(t)
	for i := 0; i < rateLimitFailureThreshold; i++ {
		rec := client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
			`{"username":"inexistent","password":"not-the-password"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, rec.Code)
		}
	}
	rec := client.roundTrip(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"inexistent","password":"not-the-password"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over-budget attempt: status = %d, want 429", rec.Code)
	}
	if retry := rec.Header().Get("Retry-After"); retry == "" {
		t.Fatal("429 must carry Retry-After")
	}
	_ = handler
}

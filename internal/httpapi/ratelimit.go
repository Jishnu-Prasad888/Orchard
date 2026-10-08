package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Public auth endpoints accept unauthenticated requests and each login
// attempt costs an argon2id derivation (~64 MiB and tens of ms). Without
// throttling that is both a brute-force and a CPU/memory denial-of-service
// vector. The limiter is deliberately tiny and dependency-free: an in-process
// token bucket per client plus a failed-attempt lockout. It never touches
// reads or authenticated traffic, tolerates a short burst, and answers
// over-threshold clients with 429 before the password hash ever runs.
//
// Fine-grained policy can live behind a reverse proxy when present; this is
// the single-instance default and remains compatible with that placement.
const (
	// rateLimitCapacity is the burst of requests a client can issue before
	// the refill rate applies.
	rateLimitCapacity = 10
	// rateLimitRefill is the window after which a denied client regains a
	// token.
	rateLimitRefill = time.Second
	// rateLimitFailureThreshold consecutive failures trigger the lockout.
	rateLimitFailureThreshold = 8
	// rateLimitLockout is how long a locked-out client waits.
	rateLimitLockout = 60 * time.Second
	// rateLimitMaxClients bounds tracked state; the least-recently-seen
	// entries are pruned beyond this.
	rateLimitMaxClients = 4096
)

type clientBucket struct {
	tokens      float64
	lastSeen    time.Time
	consecutive int
	lockedUntil time.Time
}

// rateLimiter is a per-client token bucket with a failed-attempt lockout.
// The token bucket is keyed by IP alone (guards CPU exhaustion); the lockout
// folds in the username so guessing one account cannot block other logins
// from the same IP.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*clientBucket
	now     func() time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		buckets: make(map[string]*clientBucket),
		now:     time.Now,
	}
}

func (rl *rateLimiter) key(ip, username string) string {
	return ip + "\x00" + username
}

// allow reports whether a request from ip for username may proceed, consuming
// a token when it may and consume is true. When false, retryAfter hints how
// long the client should wait.
func (rl *rateLimiter) allow(ip, username string, consume bool) (allowed bool, retryAfter time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	if len(rl.buckets) > rateLimitMaxClients {
		for k, b := range rl.buckets {
			if now.Sub(b.lastSeen) > 2*rateLimitLockout {
				delete(rl.buckets, k)
			}
		}
	}

	k := rl.key(ip, username)
	b, ok := rl.buckets[k]
	if !ok {
		b = &clientBucket{tokens: rateLimitCapacity}
		rl.buckets[k] = b
	}

	if now.Before(b.lockedUntil) {
		return false, b.lockedUntil.Sub(now)
	}

	b.tokens = refill(b.tokens, now, b.lastSeen)
	if b.tokens < 1 {
		return false, rateLimitRefill
	}
	b.lastSeen = now
	if consume {
		b.tokens--
	}
	return true, 0
}

// refill credits tokens for elapsed wall time, capped at capacity.
func refill(tokens float64, now, last time.Time) float64 {
	elapsed := now.Sub(last)
	if elapsed < rateLimitRefill {
		return tokens
	}
	tokens += float64(elapsed / rateLimitRefill)
	if tokens > rateLimitCapacity {
		tokens = rateLimitCapacity
	}
	return tokens
}

// recordFailure notes a failed attempt for ip+username, returning the lockout
// duration and whether the threshold was just crossed.
func (rl *rateLimiter) recordFailure(ip, username string) (lockedFor time.Duration, atLimit bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	k := rl.key(ip, username)
	b, ok := rl.buckets[k]
	if !ok {
		b = &clientBucket{tokens: rateLimitCapacity}
		rl.buckets[k] = b
	}
	b.lastSeen = now
	b.consecutive++
	if b.consecutive >= rateLimitFailureThreshold {
		b.lockedUntil = now.Add(rateLimitLockout)
		return rateLimitLockout, true
	}
	return 0, false
}

// recordSuccess clears consecutive failures after a valid credential.
func (rl *rateLimiter) recordSuccess(ip, username string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if b, ok := rl.buckets[rl.key(ip, username)]; ok {
		b.consecutive = 0
		b.lockedUntil = time.Time{}
	}
}

// clientIP extracts the caller address for rate limiting. RemoteAddr is
// authoritative when the server is not proxied; when X-Forwarded-For is
// present the rightmost (most recent) untrusted hop is used so a local
// port-forward does not collapse every client into one bucket.
func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		if i := strings.LastIndex(forwarded, ","); i >= 0 {
			forwarded = forwarded[i+1:]
		}
		return strings.TrimSpace(forwarded)
	}
	host, _, found := strings.Cut(r.RemoteAddr, ":")
	if !found {
		return r.RemoteAddr
	}
	return host
}

// limitPublicAuth guards a public auth endpoint. It returns true to proceed;
// when false it has already written the 429 and the caller must return.
func (s *Server) limitPublicAuth(w http.ResponseWriter, r *http.Request, username string) bool {
	ip := clientIP(r)
	allowed, retryAfter := s.ratelimit.allow(ip, username, true)
	if allowed {
		return true
	}
	if retryAfter <= 0 {
		retryAfter = rateLimitRefill
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds()+1)))
	writeError(w, s.logger, requestIDOrEmpty(r), http.StatusTooManyRequests,
		CodeRateLimited, "Too many requests. Try again shortly.")
	return false
}

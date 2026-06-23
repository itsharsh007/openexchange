// Package middleware holds cross-cutting HTTP concerns: rate limiting and JWT
// auth. They are plain http.Handler wrappers so they compose in any order.
package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Token-bucket rate limiter (per client).
//
// WHY token bucket:
//   - It allows short BURSTS (up to `burst` tokens) while bounding the SUSTAINED
//     rate to `ratePerSec`. A trader legitimately fires a few orders back-to-
//     back; a fixed-window counter would either reject those or allow a 2x
//     spike at window boundaries. Token bucket is smooth and burst-friendly.
//   - It's O(1) and needs no background goroutine: we compute refill lazily from
//     elapsed time on each request ("leaky refill"). Tokens = min(burst,
//     tokens + elapsed * rate). If tokens >= 1 we allow and subtract 1.
// ─────────────────────────────────────────────────────────────────────────────

// bucket is one client's token bucket.
type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// RateLimiter holds a bucket per client key (here: client IP). For a real
// multi-instance deployment you'd back this with Redis (atomic counters) so the
// limit is shared across gateway replicas; this in-memory version is per-process
// and documented as such.
type RateLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*bucket
	ratePerSec float64
	burst      float64
	now        func() time.Time // injectable clock for deterministic tests
}

// NewRateLimiter builds a limiter with the given sustained rate and burst.
func NewRateLimiter(ratePerSec float64, burst int) *RateLimiter {
	return &RateLimiter{
		buckets:    make(map[string]*bucket),
		ratePerSec: ratePerSec,
		burst:      float64(burst),
		now:        time.Now,
	}
}

// Allow reports whether a request from `key` may proceed, consuming one token.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	b, ok := rl.buckets[key]
	if !ok {
		// New client starts with a full bucket so first requests never block.
		rl.buckets[key] = &bucket{tokens: rl.burst - 1, lastSeen: now}
		return true
	}

	// Lazily refill based on elapsed time, capped at burst.
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens = min(rl.burst, b.tokens+elapsed*rl.ratePerSec)
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// Middleware wraps a handler, rejecting over-limit clients with 429.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := clientIP(r)
		if !rl.Allow(key) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts a best-effort client identity. X-Forwarded-For is honored
// because the gateway typically sits behind a load balancer. In production you
// must only trust XFF from known proxies, otherwise clients can spoof it.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

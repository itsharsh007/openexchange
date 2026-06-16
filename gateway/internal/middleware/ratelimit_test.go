package middleware

import (
	"testing"
	"time"
)

// TestRateLimiterBurstThenRefill drives the limiter with a fake clock so the
// test is deterministic (no sleeps, no flakiness).
func TestRateLimiterBurstThenRefill(t *testing.T) {
	// 10 tokens/sec sustained, burst of 5.
	rl := NewRateLimiter(10, 5)

	now := time.Unix(0, 0)
	rl.now = func() time.Time { return now }

	// First request creates a full bucket and consumes one; the burst of 5
	// should all succeed back-to-back at the same instant.
	for i := 0; i < 5; i++ {
		if !rl.Allow("client-a") {
			t.Fatalf("request %d within burst should be allowed", i)
		}
	}
	// The 6th at the same instant must be denied (bucket exhausted).
	if rl.Allow("client-a") {
		t.Fatal("request beyond burst at t=0 should be denied")
	}

	// Advance 200ms => 0.2s * 10 tokens/s = 2 tokens refilled.
	now = now.Add(200 * time.Millisecond)
	if !rl.Allow("client-a") {
		t.Fatal("expected allow after 1st refilled token")
	}
	if !rl.Allow("client-a") {
		t.Fatal("expected allow after 2nd refilled token")
	}
	if rl.Allow("client-a") {
		t.Fatal("only 2 tokens should have refilled; 3rd must be denied")
	}
}

// TestRateLimiterIsolatesClients ensures one client's usage doesn't drain
// another's bucket (per-client keying).
func TestRateLimiterIsolatesClients(t *testing.T) {
	rl := NewRateLimiter(1, 1)
	now := time.Unix(0, 0)
	rl.now = func() time.Time { return now }

	if !rl.Allow("a") {
		t.Fatal("client a first request should pass")
	}
	if rl.Allow("a") {
		t.Fatal("client a second request should be denied")
	}
	// Different client must still have a full bucket.
	if !rl.Allow("b") {
		t.Fatal("client b should be unaffected by client a")
	}
}

// TestRateLimiterCapsAtBurst makes sure idle time doesn't accumulate tokens
// beyond the burst ceiling.
func TestRateLimiterCapsAtBurst(t *testing.T) {
	rl := NewRateLimiter(10, 3)
	now := time.Unix(0, 0)
	rl.now = func() time.Time { return now }

	rl.Allow("c") // create bucket, leaves 2 tokens

	// Idle for a long time; tokens must cap at burst (3), not grow unbounded.
	now = now.Add(1 * time.Hour)
	allowed := 0
	for i := 0; i < 10; i++ {
		if rl.Allow("c") {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("expected exactly burst=3 allowed after long idle, got %d", allowed)
	}
}

// Package cache wraps Redis for caching top-of-book snapshots.
//
// WHY cache the book: GET /book is the hottest read in a trading UI (depth
// charts poll it). The engine is authoritative but a sub-second-stale snapshot
// is fine for display, so we shield the engine from read amplification.
//
// GRACEFUL DEGRADATION is the headline behavior: if Redis is down or slow, the
// cache layer NEVER errors out the request — Get reports a miss and Set is a
// no-op. The gateway then falls back to the engine. A cache outage degrades
// latency, not availability.
package cache

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/itsharsh007/openexchange/gateway/internal/engine"
)

// Cache is a thin Redis wrapper. A nil *Cache is valid and behaves as "no
// cache" (every Get is a miss), which keeps call sites branch-free.
type Cache struct {
	rdb *redis.Client
	ttl time.Duration
}

// New connects (lazily — go-redis dials on first use) to Redis. It returns a
// usable cache plus a bool indicating whether the initial ping succeeded, so
// the operator gets a clear boot-time signal without it being fatal.
func New(ctx context.Context, addr string, ttl time.Duration) (*Cache, bool) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  500 * time.Millisecond,
		WriteTimeout: 500 * time.Millisecond,
	})
	c := &Cache{rdb: rdb, ttl: ttl}

	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	healthy := rdb.Ping(pingCtx).Err() == nil
	return c, healthy
}

func bookKey(symbol string, depth int32) string {
	return "book:" + symbol + ":" + itoa(depth)
}

// GetBook returns a cached snapshot and true on a hit. Any error (including
// Redis being down) is treated as a miss — never propagated to the caller.
func (c *Cache) GetBook(ctx context.Context, symbol string, depth int32) (engine.BookSnapshot, bool) {
	if c == nil || c.rdb == nil {
		return engine.BookSnapshot{}, false
	}
	raw, err := c.rdb.Get(ctx, bookKey(symbol, depth)).Bytes()
	if err != nil {
		return engine.BookSnapshot{}, false
	}
	var snap engine.BookSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return engine.BookSnapshot{}, false
	}
	return snap, true
}

// SetBook stores a snapshot with the configured TTL. Errors are swallowed: a
// failed cache write must not fail the user's request.
func (c *Cache) SetBook(ctx context.Context, depth int32, snap engine.BookSnapshot) {
	if c == nil || c.rdb == nil {
		return
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return
	}
	_ = c.rdb.Set(ctx, bookKey(snap.Symbol, depth), raw, c.ttl).Err()
}

// Ping reports current Redis health (used by /healthz for a richer status).
func (c *Cache) Ping(ctx context.Context) bool {
	if c == nil || c.rdb == nil {
		return false
	}
	return c.rdb.Ping(ctx).Err() == nil
}

// Close releases the connection pool.
func (c *Cache) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// itoa avoids pulling strconv just for one small int->string.
func itoa(n int32) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
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

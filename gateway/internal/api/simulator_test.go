package api

import (
	"context"
	"testing"

	"github.com/itsharsh007/openexchange/gateway/internal/engine"
)

// The simulator should keep the market alive: over a handful of ticks it must
// produce real trades and leave a populated, two-sided book — all through the
// normal engine path (so the ledger and self-match prevention apply).
func TestMarketSimProducesActivity(t *testing.T) {
	eng := engine.NewLocalEngine("AAPL")
	sim := newMarketSim(eng, []string{"AAPL"}, 42) // fixed seed → deterministic
	ctx := context.Background()

	trades := 0
	for i := 0; i < 30; i++ {
		for _, b := range sim.step(ctx) {
			trades += len(b.trades)
		}
	}
	if trades == 0 {
		t.Fatal("simulator produced no trades over 30 ticks — demo would look dead")
	}

	// The maker keeps both sides quoted, so the book is alive and two-sided.
	book, _ := eng.GetBook(ctx, engine.BookRequest{Symbol: "AAPL", Depth: 5})
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		t.Fatalf("expected a two-sided book, got bids=%d asks=%d", len(book.Bids), len(book.Asks))
	}
}

// Quotes are re-issued each tick, so the book must not grow without bound — the
// maker cancels its previous orders before re-quoting.
func TestMarketSimDoesNotLeakOrders(t *testing.T) {
	eng := engine.NewLocalEngine("AAPL")
	sim := newMarketSim(eng, []string{"AAPL"}, 7)
	ctx := context.Background()

	for i := 0; i < 50; i++ {
		sim.step(ctx)
	}
	// With simLevels per side re-quoted each tick (and seeded liquidity), depth stays
	// small and bounded rather than accumulating 50 ticks' worth of orders.
	book, _ := eng.GetBook(ctx, engine.BookRequest{Symbol: "AAPL", Depth: 100})
	if len(book.Bids) > 4*simLevels || len(book.Asks) > 4*simLevels {
		t.Fatalf("book grew unbounded: bids=%d asks=%d", len(book.Bids), len(book.Asks))
	}
}

package engine

import (
	"context"
	"testing"
)

func mkt() context.Context { return context.Background() }

func submit(t *testing.T, e *LocalEngine, acct string, side Side, typ OrderType, price, qty int64) OrderAck {
	t.Helper()
	ack, err := e.SubmitOrder(mkt(), NewOrder{
		AccountID: acct, Symbol: "AAPL", Side: side, Type: typ, PriceTicks: price, Quantity: qty,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return ack
}

// A resting bid and a crossing sell from a DIFFERENT account must trade — the
// core "two traders match" behavior the live demo depends on.
func TestCrossingOrdersTradeBetweenAccounts(t *testing.T) {
	e := NewLocalEngine() // empty book, no seeded maker

	rest := submit(t, e, "alice", SideBuy, OrderTypeLimit, 10000, 5)
	if rest.Status != StatusAccepted || rest.FilledQuantity != 0 {
		t.Fatalf("resting bid should be ACCEPTED unfilled, got %+v", rest)
	}

	hit := submit(t, e, "bob", SideSell, OrderTypeLimit, 9990, 5)
	if hit.Status != StatusFilled || hit.FilledQuantity != 5 {
		t.Fatalf("crossing sell should be FILLED 5, got %+v", hit)
	}
	if len(hit.Trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(hit.Trades))
	}
	tr := hit.Trades[0]
	if tr.PriceTicks != 10000 { // trades print at the resting (maker) price
		t.Errorf("trade price = %d, want 10000 (maker price)", tr.PriceTicks)
	}
	if tr.BuyOrderID != rest.OrderID || tr.SellOrderID != hit.OrderID {
		t.Errorf("trade order ids wrong: %+v (rest=%s hit=%s)", tr, rest.OrderID, hit.OrderID)
	}
}

// Self-match prevention: one account can't trade with itself. Its crossing order
// skips its own resting orders (which stay put) and only matches other accounts.
func TestSelfMatchIsPrevented(t *testing.T) {
	e := NewLocalEngine() // empty book, no seeded maker

	rest := submit(t, e, "alice", SideBuy, OrderTypeLimit, 10000, 5)
	// Alice's own crossing sell must NOT trade against her resting bid.
	self := submit(t, e, "alice", SideSell, OrderTypeLimit, 10000, 5)
	if self.Status != StatusAccepted || self.FilledQuantity != 0 || len(self.Trades) != 0 {
		t.Fatalf("self-match must be prevented (rest, no fill), got %+v", self)
	}

	// A different account at the same price DOES fill against Alice's resting bid.
	other := submit(t, e, "bob", SideSell, OrderTypeLimit, 10000, 5)
	if other.Status != StatusFilled || other.FilledQuantity != 5 {
		t.Fatalf("other account should fill against the resting bid, got %+v", other)
	}
	if other.Trades[0].BuyOrderID != rest.OrderID {
		t.Fatalf("trade should hit alice's resting bid %s, got %+v", rest.OrderID, other.Trades[0])
	}
}

// Price-time priority: among equal-priced resting orders, the earliest fills first.
func TestPriceTimePriority(t *testing.T) {
	e := NewLocalEngine()
	first := submit(t, e, "a", SideBuy, OrderTypeLimit, 10000, 3)
	_ = submit(t, e, "b", SideBuy, OrderTypeLimit, 10000, 3) // same price, later
	// A sell of 3 should hit `first` (earliest at the best price), fully.
	ack := submit(t, e, "c", SideSell, OrderTypeLimit, 10000, 3)
	if len(ack.Trades) != 1 || ack.Trades[0].BuyOrderID != first.OrderID {
		t.Fatalf("earliest order at best price should fill first; got %+v", ack.Trades)
	}
}

// A market order takes the best price available and partially fills against thin
// liquidity; with no liquidity it is rejected (nothing to take).
func TestMarketOrderBehaviour(t *testing.T) {
	e := NewLocalEngine()
	submit(t, e, "mm", SideSell, OrderTypeLimit, 10001, 4) // only 4 available to buy

	partial := submit(t, e, "x", SideBuy, OrderTypeMarket, 0, 10)
	if partial.Status != StatusPartiallyFilled || partial.FilledQuantity != 4 {
		t.Fatalf("market buy should take all 4, got %+v", partial)
	}
	// Book now empty on the ask side → next market buy finds no liquidity.
	none := submit(t, e, "y", SideBuy, OrderTypeMarket, 0, 1)
	if none.Status != StatusRejected {
		t.Fatalf("market buy with no liquidity should be REJECTED, got %+v", none)
	}
}

// Partial fill: a large incoming order fills what it can and rests the remainder.
func TestPartialFillRestsRemainder(t *testing.T) {
	e := NewLocalEngine()
	submit(t, e, "mm", SideSell, OrderTypeLimit, 10000, 2)
	ack := submit(t, e, "big", SideBuy, OrderTypeLimit, 10000, 5)
	if ack.Status != StatusPartiallyFilled || ack.FilledQuantity != 2 {
		t.Fatalf("expected PARTIALLY_FILLED 2, got %+v", ack)
	}
	// The remaining 3 should now rest as the best bid.
	book, _ := e.GetBook(mkt(), BookRequest{Symbol: "AAPL", Depth: 5})
	if len(book.Bids) != 1 || book.Bids[0].PriceTicks != 10000 || book.Bids[0].Quantity != 3 {
		t.Fatalf("remainder should rest as bid 10000x3, got %+v", book.Bids)
	}
}

// Cancel removes a resting order; only the owner may cancel it.
func TestCancelOwnership(t *testing.T) {
	e := NewLocalEngine()
	rest := submit(t, e, "alice", SideBuy, OrderTypeLimit, 9999, 5)

	if ack, _ := e.CancelOrder(mkt(), CancelOrder{OrderID: rest.OrderID, AccountID: "mallory"}); ack.Status != StatusRejected {
		t.Fatalf("a non-owner must not cancel; got %+v", ack)
	}
	if ack, _ := e.CancelOrder(mkt(), CancelOrder{OrderID: rest.OrderID, AccountID: "alice"}); ack.Status != StatusCancelled {
		t.Fatalf("owner cancel should succeed; got %+v", ack)
	}
	book, _ := e.GetBook(mkt(), BookRequest{Symbol: "AAPL", Depth: 5})
	if len(book.Bids) != 0 {
		t.Fatalf("book should be empty after cancel, got %+v", book.Bids)
	}
}

// GetBook aggregates quantity across orders resting at the same price and orders
// the levels best-first per side.
func TestGetBookAggregatesAndSorts(t *testing.T) {
	e := NewLocalEngine()
	submit(t, e, "a", SideBuy, OrderTypeLimit, 9999, 10)
	submit(t, e, "b", SideBuy, OrderTypeLimit, 9999, 5) // same level → aggregates to 15
	submit(t, e, "c", SideBuy, OrderTypeLimit, 9998, 7)

	book, _ := e.GetBook(mkt(), BookRequest{Symbol: "AAPL", Depth: 5})
	if len(book.Bids) != 2 {
		t.Fatalf("expected 2 bid levels, got %+v", book.Bids)
	}
	if book.Bids[0].PriceTicks != 9999 || book.Bids[0].Quantity != 15 {
		t.Errorf("best bid should be 9999x15 (aggregated), got %+v", book.Bids[0])
	}
	if book.Bids[1].PriceTicks != 9998 {
		t.Errorf("second bid level should be 9998, got %+v", book.Bids[1])
	}
}

// The seeded constructor gives immediate two-sided liquidity around mid 10000.
func TestSeededBookHasLiquidity(t *testing.T) {
	e := NewLocalEngine("AAPL")
	book, _ := e.GetBook(mkt(), BookRequest{Symbol: "AAPL", Depth: 5})
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		t.Fatalf("seeded book should have both sides, got bids=%d asks=%d", len(book.Bids), len(book.Asks))
	}
	if book.Bids[0].PriceTicks >= book.Asks[0].PriceTicks {
		t.Errorf("best bid (%d) must be below best ask (%d)", book.Bids[0].PriceTicks, book.Asks[0].PriceTicks)
	}
}

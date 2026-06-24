package engine

import "testing"

// snapshot is a tiny helper to read an account back out of the engine.
func snap(t *testing.T, e *LocalEngine, acct string) AccountSnapshot {
	t.Helper()
	s, err := e.GetAccount(mkt(), acct)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	return s
}

// A fill must move cash and positions for BOTH sides, and cash must be conserved:
// what the buyer pays, the seller receives.
func TestFillUpdatesBothLedgersAndConservesCash(t *testing.T) {
	e := NewLocalEngine()
	// alice rests a buy; bob sells into it → they trade 5 @ 10000.
	submit(t, e, "alice", SideBuy, OrderTypeLimit, 10000, 5)
	submit(t, e, "bob", SideSell, OrderTypeLimit, 10000, 5)

	a, b := snap(t, e, "alice"), snap(t, e, "bob")
	if a.CashTicks != StartingCashTicks-10000*5 {
		t.Errorf("buyer cash = %d, want %d", a.CashTicks, StartingCashTicks-50000)
	}
	if b.CashTicks != StartingCashTicks+10000*5 {
		t.Errorf("seller cash = %d, want %d", b.CashTicks, StartingCashTicks+50000)
	}
	// Cash conservation: total cash across both equals 2× the opening balance.
	if a.CashTicks+b.CashTicks != 2*StartingCashTicks {
		t.Errorf("cash not conserved: %d + %d != %d", a.CashTicks, b.CashTicks, 2*StartingCashTicks)
	}
	if len(a.Positions) != 1 || a.Positions[0].Quantity != 5 || a.Positions[0].AvgPriceTicks != 10000 {
		t.Errorf("buyer should be long 5 @ 10000, got %+v", a.Positions)
	}
	if len(b.Positions) != 1 || b.Positions[0].Quantity != -5 {
		t.Errorf("seller should be short 5, got %+v", b.Positions)
	}
}

// Buying then selling higher books a realized profit and returns the account flat.
func TestRealizedPnlOnRoundTrip(t *testing.T) {
	e := NewLocalEngine()
	// Seed asks at 10000 and bids at 10010 from a maker so our trader can buy then sell.
	submit(t, e, "mm", SideSell, OrderTypeLimit, 10000, 10) // trader buys here
	submit(t, e, "trader", SideBuy, OrderTypeMarket, 0, 10) // long 10 @ 10000
	submit(t, e, "mm", SideBuy, OrderTypeLimit, 10010, 10)  // trader sells here
	submit(t, e, "trader", SideSell, OrderTypeMarket, 0, 10) // close 10 @ 10010

	s := snap(t, e, "trader")
	if len(s.Positions) != 0 {
		t.Errorf("trader should be flat, got %+v", s.Positions)
	}
	if s.RealizedPnlTicks != (10010-10000)*10 {
		t.Errorf("realized P&L = %d, want %d", s.RealizedPnlTicks, 100)
	}
	if s.UnrealizedPnlTicks != 0 {
		t.Errorf("flat account should have 0 unrealized, got %d", s.UnrealizedPnlTicks)
	}
	// Net cash gain equals the realized profit.
	if s.CashTicks != StartingCashTicks+100 {
		t.Errorf("cash = %d, want %d", s.CashTicks, StartingCashTicks+100)
	}
}

// Adding to a position blends the average cost; the mark-to-market drives unrealized.
func TestAvgCostAndUnrealized(t *testing.T) {
	e := NewLocalEngine()
	submit(t, e, "mm", SideSell, OrderTypeLimit, 10000, 4)
	submit(t, e, "mm", SideSell, OrderTypeLimit, 10020, 6)
	submit(t, e, "t", SideBuy, OrderTypeMarket, 0, 10) // 4@10000 + 6@10020

	s := snap(t, e, "t")
	wantAvg := int64((10000*4 + 10020*6) / 10) // = 10012
	if s.Positions[0].AvgPriceTicks != wantAvg {
		t.Errorf("avg price = %d, want %d", s.Positions[0].AvgPriceTicks, wantAvg)
	}
	// Last trade printed at 10020 → mark to 10020; unrealized = (10020-avg)*10.
	if want := (10020 - wantAvg) * 10; s.UnrealizedPnlTicks != want {
		t.Errorf("unrealized = %d, want %d", s.UnrealizedPnlTicks, want)
	}
}

// Realized P&L must reconcile EXACTLY with the cash change when the position
// returns to flat — even when the average entry price isn't a whole tick.
// Regression: a rounded integer average drifted realized away from cash (a buy
// of 1@10001 + 1@10002 has avg 10001.5, which truncated to 10001 and lost 0.10).
func TestRealizedTiesToCashWhenFlat(t *testing.T) {
	e := NewLocalEngine()
	// Buy 2 across two prices → avg 10001.5 (not a whole tick).
	submit(t, e, "mm", SideSell, OrderTypeLimit, 10001, 1)
	submit(t, e, "mm", SideSell, OrderTypeLimit, 10002, 1)
	submit(t, e, "t", SideBuy, OrderTypeMarket, 0, 2)
	// Sell both back at 10000 → flat.
	submit(t, e, "mm", SideBuy, OrderTypeLimit, 10000, 2)
	submit(t, e, "t", SideSell, OrderTypeMarket, 0, 2)

	s := snap(t, e, "t")
	if len(s.Positions) != 0 {
		t.Fatalf("expected flat, got %+v", s.Positions)
	}
	// True realized = proceeds − cost = 10000*2 − (10001+10002) = −3 (not −2).
	if s.RealizedPnlTicks != -3 {
		t.Errorf("realized = %d, want -3", s.RealizedPnlTicks)
	}
	// THE INVARIANT: flat ⇒ cash change == realized, to the tick.
	if got := s.CashTicks - StartingCashTicks; got != s.RealizedPnlTicks {
		t.Errorf("ledger does not reconcile when flat: cash change %d != realized %d", got, s.RealizedPnlTicks)
	}
}

// An untouched account reports the opening balance and no positions.
func TestFreshAccountOpeningBalance(t *testing.T) {
	e := NewLocalEngine()
	s := snap(t, e, "newcomer")
	if s.CashTicks != StartingCashTicks || len(s.Positions) != 0 {
		t.Errorf("fresh account = %+v, want opening cash and flat", s)
	}
}

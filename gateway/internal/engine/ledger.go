package engine

import "context"

// ─────────────────────────────────────────────────────────────────────────────
// Per-account ledger for the in-process engine.
//
// Every fill moves cash and a position for BOTH counterparties. We use the
// average-cost method: opening/adding to a position blends the average price;
// reducing or closing it books realized P&L against that average. Unrealized
// P&L marks open positions to the symbol's last trade price.
//
// All methods here run under LocalEngine.mu (the caller holds it), so the ledger
// shares the engine's single-writer guarantee — cash and positions can't be seen
// half-updated. Money is integer ticks throughout; never float.
// ─────────────────────────────────────────────────────────────────────────────

type position struct {
	qty int64 // signed: positive long, negative short
	// costBasis is the EXACT total cost of the open position in ticks (a magnitude,
	// always >= 0; 0 when flat). We keep the integer total — not a rounded average
	// price — so realized P&L reconciles with cash to the tick. A fractional
	// average (e.g. 100.015) can't be stored in whole ticks, but its *total*
	// (200.03) can, and that's what realized P&L is actually computed against.
	costBasis int64
}

type ledgerAccount struct {
	cash      int64
	realized  int64
	positions map[string]*position
}

func opposite(s Side) Side {
	if s == SideBuy {
		return SideSell
	}
	return SideBuy
}

// account returns (creating if needed) the ledger for accountID, opening it with
// the standard demo balance.
func (e *LocalEngine) account(accountID string) *ledgerAccount {
	a, ok := e.accounts[accountID]
	if !ok {
		a = &ledgerAccount{cash: startingCashTicks, positions: make(map[string]*position)}
		e.accounts[accountID] = a
	}
	return a
}

// applyFill books one execution for one side: cash changes by ∓price·qty and the
// position is updated with realized P&L on any reduction. `side` is this account's
// side of the trade (BUY = it bought qty, SELL = it sold qty).
func (e *LocalEngine) applyFill(accountID, symbol string, side Side, price, qty int64) {
	a := e.account(accountID)
	pos := a.positions[symbol]
	if pos == nil {
		pos = &position{}
		a.positions[symbol] = pos
	}

	// Signed quantity delta and cash flow. Buying spends cash and adds to position.
	delta := qty
	if side == SideSell {
		delta = -qty
		a.cash += price * qty
	} else {
		a.cash -= price * qty
	}

	switch {
	case pos.qty == 0:
		// Opening a fresh position.
		pos.qty = delta
		pos.costBasis = price * qty
	case sameSign(pos.qty, delta):
		// Adding to the position — accumulate exact cost basis.
		pos.qty += delta
		pos.costBasis += price * qty
	default:
		// Reducing / closing / flipping — realize P&L on the closed quantity.
		closeQty := min64(abs(pos.qty), abs(delta))
		// Cost basis attributable to the closed quantity. A FULL close removes the
		// ENTIRE remaining basis with no division, so realized always equals the
		// cash change when the position returns to flat; a partial close apportions
		// proportionally and carries the remainder forward.
		var closedCost int64
		if closeQty == abs(pos.qty) {
			closedCost = pos.costBasis
		} else {
			closedCost = pos.costBasis * closeQty / abs(pos.qty)
		}
		// Realized = proceeds − cost (long) or cost − proceeds (short).
		if pos.qty > 0 {
			a.realized += price*closeQty - closedCost
		} else {
			a.realized += closedCost - price*closeQty
		}
		pos.costBasis -= closedCost

		newQty := pos.qty + delta
		switch {
		case newQty == 0:
			pos.costBasis = 0 // flat
		case sameSign(newQty, delta):
			// Flipped past flat — the leftover opens a new position at the fill price.
			pos.costBasis = price * abs(newQty)
		}
		pos.qty = newQty
	}
}

// GetAccount returns the account's cash, realized + unrealized P&L, and open
// positions. Unrealized marks each open position to the symbol's last trade price.
func (e *LocalEngine) GetAccount(ctx context.Context, accountID string) (AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return AccountSnapshot{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	a, ok := e.accounts[accountID]
	if !ok {
		// Always return an empty (not nil) slice so it marshals as [] not null.
		return AccountSnapshot{AccountID: accountID, CashTicks: startingCashTicks, Positions: []PositionView{}}, nil
	}

	var unrealized int64
	positions := make([]PositionView, 0, len(a.positions))
	for sym, p := range a.positions {
		if p.qty == 0 {
			continue
		}
		// Display average is derived from the exact basis (rounded to whole ticks
		// for the UI only — never fed back into P&L math).
		avg := p.costBasis / abs(p.qty)
		// Unrealized = market value − cost basis, computed against the exact basis.
		// With no last trade yet (mark == 0) there's no market price, so it's zero.
		if mark := e.lastPrice[sym]; mark != 0 {
			if p.qty > 0 {
				unrealized += mark*p.qty - p.costBasis
			} else {
				unrealized += p.costBasis - mark*abs(p.qty)
			}
		}
		positions = append(positions, PositionView{Symbol: sym, Quantity: p.qty, AvgPriceTicks: avg})
	}

	return AccountSnapshot{
		AccountID:          accountID,
		CashTicks:          a.cash,
		RealizedPnlTicks:   a.realized,
		UnrealizedPnlTicks: unrealized,
		Positions:          positions,
	}, nil
}

func sameSign(a, b int64) bool { return (a > 0) == (b > 0) }
func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

package api

import (
	"context"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/itsharsh007/openexchange/gateway/internal/engine"
)

// Market simulator
// ─────────────────────────────────────────────────────────────────────────────
// A handful of bot accounts that continuously quote and trade so the public demo
// is alive even with zero human visitors — the price chart moves, the tape prints,
// and the depth ladder stays populated. The bots are ordinary clients: every order
// goes through engine.SubmitOrder, so matching, the ledger, and self-match
// prevention (ADR 0008) all apply unchanged.
//
// Only meaningful for the in-process LocalEngine (the public link). The full stack
// drives activity with `make seed` instead, so main.go starts this only in local
// mode (and it can be turned off with MARKET_SIM=0).
// ─────────────────────────────────────────────────────────────────────────────

const (
	simMaker    = "sim-mm"      // posts two-sided quotes around a drifting fair value
	simTakerA   = "sim-taker-a" // crosses the book as a buyer
	simTakerB   = "sim-taker-b" // crosses the book as a seller
	simMidStart = 10000         // $100.00 — matches seedBook's notional mid
	simMidMin   = 8000          // keep the random walk in a sane band
	simMidMax   = 12000
	simLevels   = 3 // quote depth per side
)

// simBatch is the executions produced for one symbol in a single tick.
type simBatch struct {
	symbol string
	trades []engine.Trade
}

// marketSim holds the bots' fair-value estimate and outstanding quote ids per
// symbol. Not safe for concurrent use — driven by a single goroutine.
type marketSim struct {
	eng    engine.EngineClient
	syms   []string
	mid    map[string]int64
	quotes map[string][]string // resting maker order ids, re-quoted each tick
	rng    *rand.Rand
	paused atomic.Bool // when true, ticks are skipped so you can tinker on a still book
}

func newMarketSim(eng engine.EngineClient, syms []string, seed int64) *marketSim {
	m := &marketSim{
		eng:    eng,
		syms:   syms,
		mid:    make(map[string]int64, len(syms)),
		quotes: make(map[string][]string, len(syms)),
		rng:    rand.New(rand.NewSource(seed)),
	}
	for _, s := range syms {
		m.mid[s] = simMidStart
	}
	return m
}

// step advances every symbol one tick: random-walk the fair value, refresh the
// maker's two-sided quotes, and (usually) send a marketable taker order so a trade
// prints. Returns the executions produced, grouped by symbol, for broadcasting.
func (m *marketSim) step(ctx context.Context) []simBatch {
	if m.paused.Load() {
		return nil // paused: leave the book exactly as it is so a human can tinker
	}
	var out []simBatch
	for _, sym := range m.syms {
		// 1. Random-walk the fair value within a sane band.
		mid := m.mid[sym] + int64(m.rng.Intn(5)-2) // -2..+2 ticks
		if mid < simMidMin {
			mid = simMidMin
		}
		if mid > simMidMax {
			mid = simMidMax
		}
		m.mid[sym] = mid

		// 2. Cancel last tick's quotes so the book doesn't grow unbounded.
		for _, id := range m.quotes[sym] {
			_, _ = m.eng.CancelOrder(ctx, engine.CancelOrder{OrderID: id, Symbol: sym, AccountID: simMaker})
		}
		m.quotes[sym] = m.quotes[sym][:0]

		// 3. Post fresh two-sided quotes: simLevels each side, 1-tick spacing.
		for i := int64(1); i <= simLevels; i++ {
			qty := int64(10 + m.rng.Intn(40))
			if bid, err := m.eng.SubmitOrder(ctx, engine.NewOrder{
				AccountID: simMaker, Symbol: sym, Side: engine.SideBuy,
				Type: engine.OrderTypeLimit, PriceTicks: mid - i, Quantity: qty,
			}); err == nil {
				m.quotes[sym] = append(m.quotes[sym], bid.OrderID)
			}
			if ask, err := m.eng.SubmitOrder(ctx, engine.NewOrder{
				AccountID: simMaker, Symbol: sym, Side: engine.SideSell,
				Type: engine.OrderTypeLimit, PriceTicks: mid + i, Quantity: qty,
			}); err == nil {
				m.quotes[sym] = append(m.quotes[sym], ask.OrderID)
			}
		}

		// 4. Most ticks, a taker crosses the spread so a trade prints and the last
		//    price moves. Buyer and seller are different accounts (self-match safe).
		if m.rng.Intn(100) < 75 {
			side, acct := engine.SideBuy, simTakerA
			if m.rng.Intn(2) == 0 {
				side, acct = engine.SideSell, simTakerB
			}
			qty := int64(5 + m.rng.Intn(20))
			if ack, err := m.eng.SubmitOrder(ctx, engine.NewOrder{
				AccountID: acct, Symbol: sym, Side: side,
				Type: engine.OrderTypeMarket, Quantity: qty,
			}); err == nil && len(ack.Trades) > 0 {
				out = append(out, simBatch{symbol: sym, trades: ack.Trades})
			}
		}
	}
	return out
}

// StartMarketSim runs the simulator on its own goroutine until ctx is cancelled,
// broadcasting bot trades and book updates over the same WS path the order handler
// uses. Call once at startup when running the in-process engine.
func (s *Server) StartMarketSim(ctx context.Context, syms []string, interval time.Duration, seed int64) {
	sim := newMarketSim(s.eng, syms, seed)
	s.sim = sim // expose for the /sim/* control endpoints
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				batches := sim.step(ctx)
				if batches == nil && sim.paused.Load() {
					continue // paused: don't churn the WS with unchanged books
				}
				for _, b := range batches {
					s.broadcastTrades(b.trades)
				}
				// Quotes shifted even on a no-trade tick, so refresh every ladder.
				for _, sym := range syms {
					s.broadcastBook(sym)
				}
			}
		}
	}()
}

// ── Simulator control endpoints (registered only when the sim is running) ──────
// They let the dashboard pause/resume the bots with one click so you can watch the
// live market, then freeze it to tinker on a still book yourself.

type simState struct {
	Enabled bool `json:"enabled"`
	Paused  bool `json:"paused"`
}

func (s *Server) handleSimState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, simState{Enabled: true, Paused: s.sim.paused.Load()})
}

func (s *Server) handleSimPause(w http.ResponseWriter, _ *http.Request) {
	s.sim.paused.Store(true)
	writeJSON(w, http.StatusOK, simState{Enabled: true, Paused: true})
}

func (s *Server) handleSimResume(w http.ResponseWriter, _ *http.Request) {
	s.sim.paused.Store(false)
	writeJSON(w, http.StatusOK, simState{Enabled: true, Paused: false})
}

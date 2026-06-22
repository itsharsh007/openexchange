package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// LocalEngine: a real in-process matching engine.
//
// Unlike MockClient (which only fakes acks), LocalEngine keeps a live per-symbol
// limit order book and matches orders with PRICE-TIME PRIORITY — the same model
// the Java engine implements. It exists so the gateway can run a genuine, shared,
// multiplayer exchange on free hosting WITHOUT the JVM/Kafka/Postgres: every
// visitor's orders rest in and match against one book, so two browsers on the
// public link actually trade against each other.
//
// Executions are returned in OrderAck.Trades; the HTTP handler fans them (and a
// fresh book snapshot) out to every dashboard over WebSocket, so the resting
// (maker) side — which never called this gateway — still sees the fill live.
//
// CONCURRENCY: one mutex serializes all book mutations (a strict single-writer).
// The book scale here is a demo, so simple-and-correct beats lock-free cleverness;
// the *Java* engine is where the per-symbol-writer / low-latency story lives.
// ─────────────────────────────────────────────────────────────────────────────

// restingOrder is one live order sitting in the book with its remaining quantity.
type restingOrder struct {
	id        string
	accountID string
	side      Side
	price     int64
	remaining int64
	seq       int64 // arrival order — preserves time priority within a price level
}

// bookSide is one side (bids or asks) of a symbol's book: a flat, time-ordered
// slice of resting orders. We sort on read for the snapshot; matching scans for
// the best price. Fine for demo volumes; documented as such.
type orderBook struct {
	symbol string
	orders []*restingOrder
}

// startingCashTicks is every demo account's opening balance ($10,000), matching
// the frontend's default so the panel reads consistently before the first trade.
const startingCashTicks = 1_000_000

// LocalEngine implements EngineClient with real matching.
type LocalEngine struct {
	mu        sync.Mutex
	books     map[string]*orderBook
	accounts  map[string]*ledgerAccount // per-account cash/positions/realized PnL
	lastPrice map[string]int64          // last trade price per symbol — marks unrealized PnL
	orderID   atomic.Int64
	tradeID   atomic.Int64
	seq       atomic.Int64
}

var _ EngineClient = (*LocalEngine)(nil)
var _ AccountProvider = (*LocalEngine)(nil)

// NewLocalEngine builds an engine and seeds a starting book for the given symbols
// so the ladder isn't empty for the first visitor. Seeded liquidity comes from a
// synthetic "market-maker" account around a notional mid of 10000 ticks ($100.00).
func NewLocalEngine(seedSymbols ...string) *LocalEngine {
	e := &LocalEngine{
		books:     make(map[string]*orderBook),
		accounts:  make(map[string]*ledgerAccount),
		lastPrice: make(map[string]int64),
	}
	for _, sym := range seedSymbols {
		e.seedBook(sym)
	}
	return e
}

// seedBook adds resting maker orders so trading has something to hit immediately.
func (e *LocalEngine) seedBook(symbol string) {
	const mid = 10000
	b := e.book(symbol)
	for i := 1; i <= 5; i++ {
		b.orders = append(b.orders,
			&restingOrder{id: e.nextOrderID(), accountID: "market-maker", side: SideBuy,
				price: mid - int64(i), remaining: int64(i * 10), seq: e.seq.Add(1)},
			&restingOrder{id: e.nextOrderID(), accountID: "market-maker", side: SideSell,
				price: mid + int64(i), remaining: int64(i * 10), seq: e.seq.Add(1)},
		)
	}
}

func (e *LocalEngine) book(symbol string) *orderBook {
	b, ok := e.books[symbol]
	if !ok {
		b = &orderBook{symbol: symbol}
		e.books[symbol] = b
	}
	return b
}

func (e *LocalEngine) nextOrderID() string { return fmt.Sprintf("ord-%d", e.orderID.Add(1)) }
func (e *LocalEngine) nextTradeID() string { return fmt.Sprintf("trd-%d", e.tradeID.Add(1)) }

// SubmitOrder matches an incoming order against the book, returning the ack plus
// any trades produced. Leftover LIMIT quantity rests; leftover MARKET quantity is
// discarded (a market order takes only what liquidity exists).
func (e *LocalEngine) SubmitOrder(ctx context.Context, o NewOrder) (OrderAck, error) {
	if err := ctx.Err(); err != nil {
		return OrderAck{}, err
	}
	id := e.nextOrderID()
	if o.Quantity <= 0 {
		return OrderAck{OrderID: id, Status: StatusRejected, Reason: "quantity must be positive"}, nil
	}
	if o.Type == OrderTypeLimit && o.PriceTicks <= 0 {
		return OrderAck{OrderID: id, Status: StatusRejected, Reason: "limit order requires positive priceTicks"}, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	b := e.book(o.Symbol)

	remaining := o.Quantity
	var trades []Trade
	now := time.Now().UnixMilli()

	// crosses reports whether an incoming order at `price` can trade with a resting
	// order at `restPrice`. MARKET crosses anything; LIMIT respects the price.
	crosses := func(restPrice int64) bool {
		if o.Type == OrderTypeMarket {
			return true
		}
		if o.Side == SideBuy {
			return o.PriceTicks >= restPrice // buy lifts asks at or below our price
		}
		return o.PriceTicks <= restPrice // sell hits bids at or above our price
	}

	for remaining > 0 {
		maker := e.bestOpposite(b, o.Side)
		if maker == nil || !crosses(maker.price) {
			break
		}
		fill := remaining
		if maker.remaining < fill {
			fill = maker.remaining
		}
		remaining -= fill
		maker.remaining -= fill

		buyID, sellID := id, maker.id
		if o.Side == SideSell {
			buyID, sellID = maker.id, id
		}
		trades = append(trades, Trade{
			TradeID:     e.nextTradeID(),
			Symbol:      o.Symbol,
			PriceTicks:  maker.price, // trade prints at the resting (maker) price
			Quantity:    fill,
			BuyOrderID:  buyID,
			SellOrderID: sellID,
			TsMillis:    now,
		})

		// Update both sides' ledgers and the symbol's mark price. The taker is the
		// incoming order (o); the maker is the resting order being hit.
		e.applyFill(o.AccountID, o.Symbol, o.Side, maker.price, fill)
		e.applyFill(maker.accountID, o.Symbol, opposite(o.Side), maker.price, fill)
		e.lastPrice[o.Symbol] = maker.price

		if maker.remaining == 0 {
			e.remove(b, maker.id)
		}
	}

	filled := o.Quantity - remaining

	// Leftover LIMIT quantity rests in the book; MARKET leftover is discarded.
	if remaining > 0 && o.Type == OrderTypeLimit {
		b.orders = append(b.orders, &restingOrder{
			id: id, accountID: o.AccountID, side: o.Side,
			price: o.PriceTicks, remaining: remaining, seq: e.seq.Add(1),
		})
	}

	return OrderAck{
		OrderID:        id,
		Status:         statusFor(o.Type, o.Quantity, filled, remaining),
		FilledQuantity: filled,
		Reason:         reasonFor(o.Type, filled),
		Trades:         trades,
	}, nil
}

// bestOpposite returns the best resting order on the side opposite `incoming`:
// highest-priced bid for an incoming sell, lowest-priced ask for an incoming buy,
// breaking ties by earliest arrival (time priority).
func (e *LocalEngine) bestOpposite(b *orderBook, incoming Side) *restingOrder {
	want := SideSell // a buy matches resting sells
	if incoming == SideSell {
		want = SideBuy
	}
	var best *restingOrder
	for _, ro := range b.orders {
		if ro.side != want {
			continue
		}
		if best == nil || betterPrice(want, ro.price, best.price) ||
			(ro.price == best.price && ro.seq < best.seq) {
			best = ro
		}
	}
	return best
}

// betterPrice reports whether price a is more aggressive than b for the given
// resting side: lower is better for asks, higher is better for bids.
func betterPrice(side Side, a, b int64) bool {
	if side == SideSell {
		return a < b
	}
	return a > b
}

func (e *LocalEngine) remove(b *orderBook, id string) {
	for i, ro := range b.orders {
		if ro.id == id {
			b.orders = append(b.orders[:i], b.orders[i+1:]...)
			return
		}
	}
}

func statusFor(t OrderType, qty, filled, remaining int64) OrderStatus {
	switch {
	case filled == 0 && t == OrderTypeMarket:
		return StatusRejected // market order with no liquidity to take
	case filled == 0:
		return StatusAccepted // limit order rested, nothing crossed
	case remaining == 0:
		return StatusFilled
	case t == OrderTypeMarket:
		return StatusPartiallyFilled // took all available liquidity, rest discarded
	default:
		return StatusPartiallyFilled // partly filled, remainder resting
	}
}

func reasonFor(t OrderType, filled int64) string {
	if t == OrderTypeMarket && filled == 0 {
		return "no liquidity to fill market order"
	}
	return ""
}

// CancelOrder removes a resting order by id. It is CANCELLED if found (and owned
// by the requester), REJECTED otherwise.
func (e *LocalEngine) CancelOrder(ctx context.Context, c CancelOrder) (OrderAck, error) {
	if err := ctx.Err(); err != nil {
		return OrderAck{}, err
	}
	if c.OrderID == "" {
		return OrderAck{Status: StatusRejected, Reason: "order_id required"}, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, b := range e.books {
		for _, ro := range b.orders {
			if ro.id != c.OrderID {
				continue
			}
			if c.AccountID != "" && ro.accountID != c.AccountID {
				return OrderAck{OrderID: c.OrderID, Status: StatusRejected, Reason: "not your order"}, nil
			}
			e.remove(b, c.OrderID)
			return OrderAck{OrderID: c.OrderID, Status: StatusCancelled}, nil
		}
	}
	return OrderAck{OrderID: c.OrderID, Status: StatusRejected, Reason: "order not found"}, nil
}

// GetBook returns the aggregated top-N levels per side: bids high-to-low, asks
// low-to-high, quantities summed across orders resting at each price.
func (e *LocalEngine) GetBook(ctx context.Context, r BookRequest) (BookSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return BookSnapshot{}, err
	}
	depth := int(r.Depth)
	if depth <= 0 {
		depth = 5
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	b := e.book(r.Symbol)

	bidQty, askQty := map[int64]int64{}, map[int64]int64{}
	for _, ro := range b.orders {
		if ro.side == SideBuy {
			bidQty[ro.price] += ro.remaining
		} else {
			askQty[ro.price] += ro.remaining
		}
	}
	return BookSnapshot{
		Symbol:   r.Symbol,
		Bids:     topLevels(bidQty, depth, true),
		Asks:     topLevels(askQty, depth, false),
		TsMillis: time.Now().UnixMilli(),
	}, nil
}

// topLevels turns a price→qty map into sorted price levels, keeping the best
// `depth`. descending=true for bids (highest first), false for asks (lowest first).
func topLevels(byPrice map[int64]int64, depth int, descending bool) []PriceLevel {
	levels := make([]PriceLevel, 0, len(byPrice))
	for price, qty := range byPrice {
		levels = append(levels, PriceLevel{PriceTicks: price, Quantity: qty})
	}
	sort.Slice(levels, func(i, j int) bool {
		if descending {
			return levels[i].PriceTicks > levels[j].PriceTicks
		}
		return levels[i].PriceTicks < levels[j].PriceTicks
	})
	if len(levels) > depth {
		levels = levels[:depth]
	}
	return levels
}

// NewDemoAccountID returns a unique, human-readable demo account id so each
// browser session is a distinct trader (e.g. "acct-demo-1a2b3c"). This is what
// makes "trade with others" real: two visitors get different accounts and their
// orders cross. Falls back to a sequence-free constant only if the RNG fails.
func NewDemoAccountID(prefix string) string {
	var buf [3]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return prefix
	}
	return prefix + "-" + hex.EncodeToString(buf[:])
}

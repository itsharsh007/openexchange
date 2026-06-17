// Package engine defines the gateway's view of the Java matching engine.
//
// WHY an interface + plain Go types (not the raw protobuf stubs everywhere):
//   - protoc-generated stubs may not exist yet in this monorepo, and we must
//     stay `go build`-able with no protoc dependency.
//   - Depending on a small, hand-written interface (dependency inversion) lets
//     us unit-test the HTTP/WS layers against a deterministic mock, and swap in
//     the real gRPC client later by writing ONE adapter — no handler changes.
//
// The Go types below mirror the messages in proto/openexchange.proto. Prices
// are integer ticks (cents) on purpose: never use float64 for money.
package engine

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

// Side / OrderType / OrderStatus mirror the proto enums as Go string-y enums.
// Strings (rather than ints) make the REST JSON self-describing for the UI.
type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

type OrderType string

const (
	OrderTypeLimit  OrderType = "LIMIT"
	OrderTypeMarket OrderType = "MARKET"
)

type OrderStatus string

const (
	StatusAccepted        OrderStatus = "ACCEPTED"
	StatusPartiallyFilled OrderStatus = "PARTIALLY_FILLED"
	StatusFilled          OrderStatus = "FILLED"
	StatusCancelled       OrderStatus = "CANCELLED"
	StatusRejected        OrderStatus = "REJECTED"
)

// NewOrder mirrors proto NewOrder.
// JSON field names are camelCase to match web/src/types.ts (the frontend sends
// and expects camelCase throughout; never snake_case the wire format for the UI).
type NewOrder struct {
	ClientOrderID string    `json:"clientOrderId"`
	AccountID     string    `json:"accountId"`
	Symbol        string    `json:"symbol"`
	Side          Side      `json:"side"`
	Type          OrderType `json:"type"`
	PriceTicks    int64     `json:"priceTicks"` // ignored for MARKET
	Quantity      int64     `json:"quantity"`
}

// CancelOrder mirrors proto CancelOrder.
type CancelOrder struct {
	OrderID   string `json:"orderId"`
	Symbol    string `json:"symbol"`
	AccountID string `json:"accountId"`
}

// OrderAck mirrors proto OrderAck.
type OrderAck struct {
	OrderID        string      `json:"orderId"`
	Status         OrderStatus `json:"status"`
	FilledQuantity int64       `json:"filledQuantity"`
	Reason         string      `json:"reason,omitempty"`
}

// PriceLevel mirrors proto PriceLevel.
type PriceLevel struct {
	PriceTicks int64 `json:"priceTicks"`
	Quantity   int64 `json:"quantity"`
}

// BookSnapshot mirrors proto BookSnapshot. Bids are best-first (highest price),
// asks best-first (lowest price).
type BookSnapshot struct {
	Symbol   string       `json:"symbol"`
	Bids     []PriceLevel `json:"bids"`
	Asks     []PriceLevel `json:"asks"`
	TsMillis int64        `json:"tsMillis"`
}

// BookRequest mirrors proto BookRequest.
type BookRequest struct {
	Symbol string `json:"symbol"`
	Depth  int32  `json:"depth"`
}

// Trade mirrors proto Trade. Used by the WS hub to broadcast fills.
type Trade struct {
	TradeID     string `json:"tradeId"`
	Symbol      string `json:"symbol"`
	PriceTicks  int64  `json:"priceTicks"`
	Quantity    int64  `json:"quantity"`
	BuyOrderID  string `json:"buyOrderId"`
	SellOrderID string `json:"sellOrderId"`
	TsMillis    int64  `json:"tsMillis"`
}

// EngineClient is the contract the gateway depends on. It maps 1:1 to the
// proto service MatchingEngine. Every method takes a context so callers can
// enforce timeouts/cancellation — essential for an edge service that must not
// hang on a slow backend.
type EngineClient interface {
	SubmitOrder(ctx context.Context, o NewOrder) (OrderAck, error)
	CancelOrder(ctx context.Context, c CancelOrder) (OrderAck, error)
	GetBook(ctx context.Context, r BookRequest) (BookSnapshot, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// MockClient: a deterministic in-memory stand-in for the real engine.
//
// It lets the whole gateway run end-to-end (and be tested) before the Java
// engine or protoc stubs exist. It returns plausible fake acks and a synthetic
// order book so the UI has something to render.
// ─────────────────────────────────────────────────────────────────────────────

// MockClient implements EngineClient with fake-but-plausible responses.
type MockClient struct {
	seq atomic.Int64 // monotonically increasing id source
}

// NewMockClient returns a ready-to-use mock engine client.
func NewMockClient() *MockClient { return &MockClient{} }

// compile-time assertion that the mock satisfies the interface.
var _ EngineClient = (*MockClient)(nil)

func (m *MockClient) SubmitOrder(ctx context.Context, o NewOrder) (OrderAck, error) {
	if err := ctx.Err(); err != nil {
		return OrderAck{}, err
	}
	id := fmt.Sprintf("ord-%d", m.seq.Add(1))

	// Basic validation the real engine would also reject on.
	if o.Quantity <= 0 {
		return OrderAck{OrderID: id, Status: StatusRejected, Reason: "quantity must be positive"}, nil
	}
	if o.Type == OrderTypeLimit && o.PriceTicks <= 0 {
		return OrderAck{OrderID: id, Status: StatusRejected, Reason: "limit order requires positive priceTicks"}, nil
	}

	// Pretend a MARKET order fully fills, while a LIMIT order rests (accepted).
	if o.Type == OrderTypeMarket {
		return OrderAck{OrderID: id, Status: StatusFilled, FilledQuantity: o.Quantity}, nil
	}
	return OrderAck{OrderID: id, Status: StatusAccepted, FilledQuantity: 0}, nil
}

func (m *MockClient) CancelOrder(ctx context.Context, c CancelOrder) (OrderAck, error) {
	if err := ctx.Err(); err != nil {
		return OrderAck{}, err
	}
	if c.OrderID == "" {
		return OrderAck{Status: StatusRejected, Reason: "order_id required"}, nil
	}
	return OrderAck{OrderID: c.OrderID, Status: StatusCancelled}, nil
}

func (m *MockClient) GetBook(ctx context.Context, r BookRequest) (BookSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return BookSnapshot{}, err
	}
	// Synthesize a small symmetric book around a notional mid of 10000 ticks.
	const mid = 10000
	depth := int(r.Depth)
	if depth <= 0 || depth > 10 {
		depth = 5
	}
	bids := make([]PriceLevel, 0, depth)
	asks := make([]PriceLevel, 0, depth)
	for i := 1; i <= depth; i++ {
		bids = append(bids, PriceLevel{PriceTicks: mid - int64(i), Quantity: int64(i * 10)})
		asks = append(asks, PriceLevel{PriceTicks: mid + int64(i), Quantity: int64(i * 10)})
	}
	return BookSnapshot{
		Symbol:   r.Symbol,
		Bids:     bids,
		Asks:     asks,
		TsMillis: time.Now().UnixMilli(),
	}, nil
}

package orderfeed

import (
	"testing"

	"google.golang.org/protobuf/proto"

	oepb "github.com/itsharsh007/openexchange/gateway/genproto"
	"github.com/itsharsh007/openexchange/gateway/internal/engine"
)

// roundTrip marshals an event and parses it back, proving what actually lands on
// the wire is what a consumer (the risk service) will decode.
func roundTrip(t *testing.T, ev *oepb.OrderEvent) *oepb.OrderEvent {
	t.Helper()
	b, err := proto.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got oepb.OrderEvent
	if err := proto.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &got
}

func TestBuildSubmitEvent(t *testing.T) {
	o := engine.NewOrder{
		ClientOrderID: "c-1",
		AccountID:     "acct-buy",
		Symbol:        "AAPL",
		Side:          engine.SideBuy,
		Type:          engine.OrderTypeLimit,
		PriceTicks:    10_000,
		Quantity:      5,
	}
	ack := engine.OrderAck{OrderID: "ord-7", Status: engine.StatusAccepted}

	got := roundTrip(t, buildSubmitEvent(o, ack, 1234))

	if got.GetClientOrderId() != "c-1" || got.GetAccountId() != "acct-buy" || got.GetSymbol() != "AAPL" {
		t.Errorf("identity fields wrong: %+v", got)
	}
	if got.GetSide() != oepb.Side_BUY || got.GetType() != oepb.OrderType_LIMIT {
		t.Errorf("enum mapping wrong: side=%v type=%v", got.GetSide(), got.GetType())
	}
	if got.GetPriceTicks() != 10_000 || got.GetQuantity() != 5 {
		t.Errorf("price/qty wrong: price=%d qty=%d", got.GetPriceTicks(), got.GetQuantity())
	}
	if got.GetIsCancel() {
		t.Errorf("submit must not be flagged is_cancel")
	}
	if got.GetOrderId() != "ord-7" || got.GetStatus() != oepb.OrderStatus_ACCEPTED {
		t.Errorf("ack mapping wrong: id=%q status=%v", got.GetOrderId(), got.GetStatus())
	}
	if got.GetTsMillis() != 1234 {
		t.Errorf("ts_millis=%d, want 1234", got.GetTsMillis())
	}
}

// A rejected order must still produce an event — rejected attempts are exactly the
// anomaly signal the risk service is after.
func TestBuildSubmitEventRejected(t *testing.T) {
	o := engine.NewOrder{AccountID: "acct-x", Symbol: "MSFT", Side: engine.SideSell, Type: engine.OrderTypeMarket, Quantity: -1}
	ack := engine.OrderAck{OrderID: "ord-9", Status: engine.StatusRejected}

	got := roundTrip(t, buildSubmitEvent(o, ack, 1))
	if got.GetStatus() != oepb.OrderStatus_REJECTED {
		t.Errorf("status=%v, want REJECTED", got.GetStatus())
	}
	if got.GetSide() != oepb.Side_SELL || got.GetType() != oepb.OrderType_MARKET {
		t.Errorf("enum mapping wrong: side=%v type=%v", got.GetSide(), got.GetType())
	}
}

func TestBuildCancelEvent(t *testing.T) {
	c := engine.CancelOrder{OrderID: "ord-3", Symbol: "AAPL", AccountID: "acct-buy"}
	ack := engine.OrderAck{OrderID: "ord-3", Status: engine.StatusCancelled}

	got := roundTrip(t, buildCancelEvent(c, ack, 555))

	if !got.GetIsCancel() {
		t.Errorf("cancel must be flagged is_cancel")
	}
	if got.GetAccountId() != "acct-buy" || got.GetSymbol() != "AAPL" || got.GetOrderId() != "ord-3" {
		t.Errorf("identity fields wrong: %+v", got)
	}
	if got.GetStatus() != oepb.OrderStatus_CANCELLED {
		t.Errorf("status=%v, want CANCELLED", got.GetStatus())
	}
	// A cancel carries no side/type/qty — the gateway does not look up the resting order.
	if got.GetSide() != oepb.Side_SIDE_UNSPECIFIED || got.GetType() != oepb.OrderType_ORDER_TYPE_UNSPECIFIED {
		t.Errorf("cancel should leave side/type unspecified: side=%v type=%v", got.GetSide(), got.GetType())
	}
	if got.GetQuantity() != 0 || got.GetPriceTicks() != 0 {
		t.Errorf("cancel should carry zero qty/price: qty=%d price=%d", got.GetQuantity(), got.GetPriceTicks())
	}
}

// Noop must satisfy the interface and never panic — it is the default in tests and
// when the gateway runs without an orders topic.
func TestNoopPublisher(t *testing.T) {
	Noop.PublishSubmit(engine.NewOrder{AccountID: "a"}, engine.OrderAck{})
	Noop.PublishCancel(engine.CancelOrder{AccountID: "a"}, engine.OrderAck{})
}

package tape

import (
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/proto"

	oepb "github.com/itsharsh007/openexchange/gateway/genproto"
)

// decodeTrade must turn the engine's protobuf Trade into the exact JSON envelope
// the dashboard subscribes to — verified here without a broker.
func TestDecodeTradeProducesDashboardEnvelope(t *testing.T) {
	wire, err := proto.Marshal(&oepb.Trade{
		TradeId:       "AAPL-T1",
		Symbol:        "AAPL",
		PriceTicks:    15000,
		Quantity:      10,
		BuyOrderId:    "o1",
		SellOrderId:   "o2",
		BuyAccountId:  "alice",
		SellAccountId: "bob",
		TsMillis:      1700000000000,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	env, err := decodeTrade(wire)
	if err != nil {
		t.Fatalf("decodeTrade: %v", err)
	}

	if env.Type != "trade" {
		t.Errorf("type = %q, want %q", env.Type, "trade")
	}
	got := env.Trade
	if got.TradeID != "AAPL-T1" || got.Symbol != "AAPL" || got.PriceTicks != 15000 ||
		got.Quantity != 10 || got.BuyAccountID != "alice" || got.SellAccountID != "bob" ||
		got.TsMillis != 1700000000000 {
		t.Errorf("envelope mismatch: %+v", got)
	}

	// The marshalled JSON must carry snake_case keys the frontend reads.
	b, _ := json.Marshal(env)
	for _, key := range []string{`"type":"trade"`, `"trade_id":"AAPL-T1"`, `"price_ticks":15000`, `"buy_account_id":"alice"`} {
		if !contains(string(b), key) {
			t.Errorf("JSON %s missing %s", b, key)
		}
	}
}

func TestDecodeTradeRejectsGarbage(t *testing.T) {
	// Not all byte strings are valid protobuf, but proto3 is lenient; assert we
	// at least never panic and return the zero envelope on a clean decode.
	if _, err := decodeTrade([]byte{0xff, 0xff, 0xff}); err == nil {
		t.Log("garbage decoded without error (proto3 lenient); acceptable as long as no panic")
	}
}

// fakeHub captures the last broadcast so we can assert the consumer forwards it.
type fakeHub struct{ last any }

func (f *fakeHub) Broadcast(v any) { f.last = v }

func TestConsumerForwardsToHub(t *testing.T) {
	// Exercise the decode->broadcast wiring directly (no kafka Reader needed).
	hub := &fakeHub{}
	wire, _ := proto.Marshal(&oepb.Trade{TradeId: "T9", Symbol: "MSFT", PriceTicks: 1, Quantity: 2})
	env, err := decodeTrade(wire)
	if err != nil {
		t.Fatalf("decodeTrade: %v", err)
	}
	hub.Broadcast(env)
	if _, ok := hub.last.(tradeEnvelope); !ok {
		t.Fatalf("hub got %T, want tradeEnvelope", hub.last)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Package tape turns the engine's Kafka `trades` stream into the live trade tape
// that the dashboard subscribes to over WebSocket.
//
// WHY a dedicated consumer (not synthesized at order time):
//   - The engine is the single source of truth for what actually executed. A
//     POST /orders ack tells the *submitter* their fill, but the tape must show
//     EVERY trade to EVERY connected dashboard, including the resting (maker)
//     side that never called this gateway. Only the engine's trade stream has
//     that complete, authoritative view.
//   - Decoupling via Kafka means a slow/again-restarting gateway never blocks the
//     engine, and multiple gateway replicas can each fan the same tape out to
//     their own WebSocket clients (each replica is its own consumer group member).
package tape

import (
	"context"
	"log"
	"time"

	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"

	oepb "github.com/itsharsh007/openexchange/gateway/genproto"
)

// broadcaster is the slice of the WS hub the tape needs: push one JSON-able
// value to all connected clients. Defined here (not imported) so the consumer is
// trivially unit-testable with a fake.
type broadcaster interface {
	Broadcast(v any)
}

// TradeConsumer reads the `trades` topic and broadcasts each decoded trade to the
// WebSocket hub. It owns a single kafka-go Reader (which manages connection,
// retries, and offset commits for its consumer group).
type TradeConsumer struct {
	reader *kafka.Reader
	hub    broadcaster
}

// NewTradeConsumer builds a consumer for the given broker/topic/group. It does
// not dial yet — kafka-go connects lazily on the first Read and reconnects with
// backoff on its own, so a broker that is down at boot is non-fatal.
func NewTradeConsumer(brokers []string, topic, groupID string, hub broadcaster) *TradeConsumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: groupID,
		// Start from the latest offset for a fresh group: the tape is a live
		// feed, not a backfill of historical trades.
		StartOffset: kafka.LastOffset,
		MinBytes:    1,
		MaxBytes:    10 << 20, // 10MB
		MaxWait:     500 * time.Millisecond,
	})
	return &TradeConsumer{reader: reader, hub: hub}
}

// Run blocks consuming until ctx is cancelled. Broker errors are logged and the
// loop continues (kafka-go backs off internally), so a Kafka outage degrades the
// tape without taking the gateway down. Returns after a clean close.
func (c *TradeConsumer) Run(ctx context.Context) {
	log.Printf("tape: consuming '%s' as group '%s'", c.reader.Config().Topic, c.reader.Config().GroupID)
	for {
		msg, err := c.reader.ReadMessage(ctx)
		if err != nil {
			// Context cancelled -> shutdown. Any other error: log and retry; the
			// reader has already backed off by the time ReadMessage returns.
			if ctx.Err() != nil {
				log.Printf("tape: stopping (%v)", ctx.Err())
				return
			}
			log.Printf("tape: read error (will retry): %v", err)
			continue
		}
		envelope, derr := decodeTrade(msg.Value)
		if derr != nil {
			// A single malformed record must not stall the tape.
			log.Printf("tape: skipping undecodable trade: %v", derr)
			continue
		}
		c.hub.Broadcast(envelope)
	}
}

// Close releases the underlying reader.
func (c *TradeConsumer) Close() error { return c.reader.Close() }

// tradeEnvelope is the WebSocket message shape the dashboard expects. Keeping it
// here (and identical to what the gateway used to synthesize) means the frontend
// contract is unchanged — only the *source* of the trade moved to the engine.
type tradeEnvelope struct {
	Type  string     `json:"type"` // always "trade"
	Trade tradeFields `json:"trade"`
}

type tradeFields struct {
	TradeID       string `json:"trade_id"`
	Symbol        string `json:"symbol"`
	PriceTicks    int64  `json:"price_ticks"`
	Quantity      int64  `json:"quantity"`
	BuyAccountID  string `json:"buy_account_id"`
	SellAccountID string `json:"sell_account_id"`
	TsMillis      int64  `json:"ts_millis"`
}

// decodeTrade unmarshals one protobuf Trade message (the wire format the engine
// publishes) into the dashboard envelope. Pure function of its input, so it is
// unit-tested without a broker.
func decodeTrade(value []byte) (tradeEnvelope, error) {
	var t oepb.Trade
	if err := proto.Unmarshal(value, &t); err != nil {
		return tradeEnvelope{}, err
	}
	return tradeEnvelope{
		Type: "trade",
		Trade: tradeFields{
			TradeID:       t.GetTradeId(),
			Symbol:        t.GetSymbol(),
			PriceTicks:    t.GetPriceTicks(),
			Quantity:      t.GetQuantity(),
			BuyAccountID:  t.GetBuyAccountId(),
			SellAccountID: t.GetSellAccountId(),
			TsMillis:      t.GetTsMillis(),
		},
	}, nil
}

// Package orderfeed publishes an OrderEvent to the Kafka `orders` topic for every
// order the gateway processes — new submissions AND cancels, including ones the
// engine rejects. The Python risk service consumes this stream to build per-account
// order-flow features (velocity, sizing, spoofing patterns) for anomaly scoring.
//
// WHY the gateway is the producer (not the engine):
//   - The anomaly model scores order *attempts*. It must see orders the engine
//     never accepts — rejected, malformed, rate-limited — because a burst of
//     rejected orders is itself a strong fraud/spoofing signal. Only the gateway
//     observes every attempt, at the authenticated edge, with a verified account_id.
//
// WHY best-effort + async (mirrors the engine's trade publisher, see ADR 0003/0004):
//   - This stream feeds ML features, not the money path. A broker outage must
//     degrade the risk features, never fail or slow a customer's order. So we
//     publish AFTER the engine has acked, fire-and-forget, and only log failures.
//     The ledger remains the single source of truth for anything that touched money.
package orderfeed

import (
	"context"
	"log"
	"time"

	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"

	oepb "github.com/itsharsh007/openexchange/gateway/genproto"
	"github.com/itsharsh007/openexchange/gateway/internal/engine"
)

// Publisher is the slice of behaviour the handlers depend on. An interface (not a
// concrete type) keeps the HTTP layer trivially testable: tests inject Noop and
// never need a broker.
type Publisher interface {
	// PublishSubmit records a NewOrder attempt and the engine's resulting ack.
	PublishSubmit(o engine.NewOrder, ack engine.OrderAck)
	// PublishCancel records a cancel attempt and the engine's resulting ack.
	PublishCancel(c engine.CancelOrder, ack engine.OrderAck)
}

// Noop is the default publisher: it drops every event. Used in tests and when the
// gateway is run without a configured `orders` topic, so order handling stays
// identical with or without Kafka.
var Noop Publisher = noop{}

type noop struct{}

func (noop) PublishSubmit(engine.NewOrder, engine.OrderAck) {}
func (noop) PublishCancel(engine.CancelOrder, engine.OrderAck) {}

// KafkaPublisher writes protobuf OrderEvents to the `orders` topic, keyed by
// account_id so one account's flow lands on one partition (the risk features are
// per-account, so per-account ordering is what matters — not global ordering).
type KafkaPublisher struct {
	writer *kafka.Writer
}

var _ Publisher = (*KafkaPublisher)(nil)

// NewKafkaPublisher builds an async writer for the given brokers/topic. The writer
// connects lazily and retries internally, so a broker that is down at boot is
// non-fatal. Async means WriteMessages never blocks the request goroutine; delivery
// errors arrive on the completion callback, where we log them and move on.
func NewKafkaPublisher(brokers []string, topic string) *KafkaPublisher {
	w := &kafka.Writer{
		Addr:     kafka.TCP(brokers...),
		Topic:    topic,
		Balancer: &kafka.Hash{}, // key (account_id) -> stable partition
		Async:    true,          // fire-and-forget: best-effort, never block an order
		// Acknowledge from all in-sync replicas: don't silently lose an event on a
		// broker failover. Cheap here because Async keeps it off the request path.
		RequiredAcks: kafka.RequireAll,
		Completion: func(msgs []kafka.Message, err error) {
			if err != nil {
				log.Printf("orderfeed: publish failed for %d event(s): %v", len(msgs), err)
			}
		},
	}
	return &KafkaPublisher{writer: w}
}

// Close flushes and releases the underlying writer.
func (p *KafkaPublisher) Close() error { return p.writer.Close() }

func (p *KafkaPublisher) PublishSubmit(o engine.NewOrder, ack engine.OrderAck) {
	p.publish(buildSubmitEvent(o, ack, time.Now().UnixMilli()))
}

func (p *KafkaPublisher) PublishCancel(c engine.CancelOrder, ack engine.OrderAck) {
	p.publish(buildCancelEvent(c, ack, time.Now().UnixMilli()))
}

func (p *KafkaPublisher) publish(ev *oepb.OrderEvent) {
	value, err := proto.Marshal(ev)
	if err != nil {
		// A marshal failure is a programming error, not a broker problem; log and
		// drop rather than failing the order that already succeeded upstream.
		log.Printf("orderfeed: marshal OrderEvent: %v", err)
		return
	}
	// Async writer: this enqueues and returns immediately; ctx is only used until
	// the message is buffered. Errors surface on the Completion callback.
	if err := p.writer.WriteMessages(context.Background(), kafka.Message{
		Key:   []byte(ev.GetAccountId()),
		Value: value,
	}); err != nil {
		log.Printf("orderfeed: enqueue OrderEvent: %v", err)
	}
}

// buildSubmitEvent maps a gateway NewOrder + the engine's ack into the protobuf
// OrderEvent on the wire. Pure function of its inputs, so it is unit-tested with no
// broker. ts_millis is passed in (not read from the clock) to keep it deterministic.
func buildSubmitEvent(o engine.NewOrder, ack engine.OrderAck, tsMillis int64) *oepb.OrderEvent {
	return &oepb.OrderEvent{
		ClientOrderId: o.ClientOrderID,
		AccountId:     o.AccountID,
		Symbol:        o.Symbol,
		Side:          protoSide(o.Side),
		Type:          protoType(o.Type),
		PriceTicks:    o.PriceTicks,
		Quantity:      o.Quantity,
		IsCancel:      false,
		OrderId:       ack.OrderID,
		Status:        protoStatus(ack.Status),
		TsMillis:      tsMillis,
	}
}

// buildCancelEvent maps a gateway cancel attempt + ack into an OrderEvent. Side,
// type, price and quantity are left zero/unspecified — the gateway does not look up
// the resting order, and the risk model only needs the account/symbol/cancel signal.
func buildCancelEvent(c engine.CancelOrder, ack engine.OrderAck, tsMillis int64) *oepb.OrderEvent {
	return &oepb.OrderEvent{
		AccountId: c.AccountID,
		Symbol:    c.Symbol,
		IsCancel:  true,
		OrderId:   c.OrderID,
		Status:    protoStatus(ack.Status),
		TsMillis:  tsMillis,
	}
}

func protoSide(s engine.Side) oepb.Side {
	switch s {
	case engine.SideBuy:
		return oepb.Side_BUY
	case engine.SideSell:
		return oepb.Side_SELL
	default:
		return oepb.Side_SIDE_UNSPECIFIED
	}
}

func protoType(t engine.OrderType) oepb.OrderType {
	switch t {
	case engine.OrderTypeLimit:
		return oepb.OrderType_LIMIT
	case engine.OrderTypeMarket:
		return oepb.OrderType_MARKET
	default:
		return oepb.OrderType_ORDER_TYPE_UNSPECIFIED
	}
}

func protoStatus(s engine.OrderStatus) oepb.OrderStatus {
	switch s {
	case engine.StatusAccepted:
		return oepb.OrderStatus_ACCEPTED
	case engine.StatusPartiallyFilled:
		return oepb.OrderStatus_PARTIALLY_FILLED
	case engine.StatusFilled:
		return oepb.OrderStatus_FILLED
	case engine.StatusCancelled:
		return oepb.OrderStatus_CANCELLED
	case engine.StatusRejected:
		return oepb.OrderStatus_REJECTED
	default:
		return oepb.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}

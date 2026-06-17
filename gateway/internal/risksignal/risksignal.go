// Package risksignal consumes the Python risk service's `risk-signals` Kafka topic
// and maintains a per-account Gate the order path checks before forwarding to the
// engine. This closes the ML/risk loop: an order -> orders topic -> risk scores ->
// risk-signals -> this gate -> the gateway rejects further orders from a breaching
// account.
//
// WHY a local gate fed by Kafka (not a synchronous call to the risk service):
//   - Keeps the hot order path off a network hop to Python on every submit. The
//     gate is an in-memory map lookup (O(1), no I/O).
//   - Decouples availability: if the risk service or broker is down, the gate
//     simply stops receiving updates. It FAILS OPEN (an account with no REJECT
//     signal is allowed) — we never block all trading because risk is offline.
//
// WHY this is eventually-consistent (and that's acceptable): the signal that
// blocks an account is derived from an order/trade that already happened, so the
// *first* breaching order gets through and subsequent ones are blocked. This is a
// post-trade risk gate, not a pre-trade limit check inside the matching path; the
// engine remains the authority for the order it's currently processing.
package risksignal

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"

	oepb "github.com/itsharsh007/openexchange/gateway/genproto"
)

// Gate holds the latest risk decision per account. Safe for concurrent use: the
// consumer goroutine writes, request goroutines read.
type Gate struct {
	mu      sync.RWMutex
	blocked map[string]blockInfo
}

type blockInfo struct {
	reason   string
	score    float64
	tsMillis int64
}

// NewGate returns an empty gate — nothing is blocked until a REJECT signal arrives.
func NewGate() *Gate {
	return &Gate{blocked: make(map[string]blockInfo)}
}

// Blocked reports whether new orders from accountID should be rejected, with the
// human-readable reason from the latest risk signal. Fail-open: an unknown account
// (no signal seen) is allowed.
func (g *Gate) Blocked(accountID string) (bool, string) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	info, ok := g.blocked[accountID]
	if !ok {
		return false, ""
	}
	return true, info.reason
}

// apply folds one risk signal into the gate: REJECT records/refreshes a block;
// any other action (ALLOW, etc.) clears it. Keyed by account so the latest signal
// always wins.
func (g *Gate) apply(sig *oepb.RiskSignal) {
	acct := sig.GetAccountId()
	if acct == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if sig.GetAction() == oepb.SignalAction_REJECT {
		g.blocked[acct] = blockInfo{
			reason:   sig.GetReason(),
			score:    sig.GetScore(),
			tsMillis: sig.GetTsMillis(),
		}
		log.Printf("risksignal: BLOCK %s (score=%.2f) %s", acct, sig.GetScore(), sig.GetReason())
	} else if _, was := g.blocked[acct]; was {
		delete(g.blocked, acct)
		log.Printf("risksignal: CLEAR %s (%s)", acct, sig.GetReason())
	}
}

// Consumer reads `risk-signals` and feeds each decoded signal into the Gate.
type Consumer struct {
	reader *kafka.Reader
	gate   *Gate
}

// NewConsumer builds a consumer for the given broker/topic/group, feeding gate.
// kafka-go connects lazily and retries with backoff, so a broker down at boot is
// non-fatal (the gate just stays empty -> fail-open).
func NewConsumer(brokers []string, topic, groupID string, gate *Gate) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     brokers,
		Topic:       topic,
		GroupID:     groupID,
		StartOffset: kafka.LastOffset, // gate on current risk state, not historical signals
		MinBytes:    1,
		MaxBytes:    10 << 20,
		MaxWait:     500 * time.Millisecond,
	})
	return &Consumer{reader: reader, gate: gate}
}

// Run blocks consuming until ctx is cancelled. Broker errors are logged and the
// loop continues (kafka-go backs off), so a Kafka outage degrades the gate without
// taking the gateway down.
func (c *Consumer) Run(ctx context.Context) {
	log.Printf("risksignal: consuming '%s' as group '%s'", c.reader.Config().Topic, c.reader.Config().GroupID)
	for {
		msg, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("risksignal: stopping (%v)", ctx.Err())
				return
			}
			log.Printf("risksignal: read error (will retry): %v", err)
			continue
		}
		sig, derr := decodeSignal(msg.Value)
		if derr != nil {
			log.Printf("risksignal: skipping undecodable signal: %v", derr)
			continue
		}
		c.gate.apply(sig)
	}
}

// Close releases the underlying reader.
func (c *Consumer) Close() error { return c.reader.Close() }

// decodeSignal unmarshals one protobuf RiskSignal (the wire format the risk service
// publishes). Pure function of its input, so it is unit-tested without a broker.
func decodeSignal(value []byte) (*oepb.RiskSignal, error) {
	var s oepb.RiskSignal
	if err := proto.Unmarshal(value, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Package metrics registers the gateway's Prometheus instruments.
// All vars are package-level so any handler can record without dependency injection.
// promauto registers on the default registry automatically.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// OrderSubmitTotal counts every order attempt, labelled by final status
	// (ACCEPTED, REJECTED, FILLED, gateway_error, risk_blocked).
	OrderSubmitTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "oex_gateway_order_submit_total",
		Help: "Total order submissions, labelled by outcome status.",
	}, []string{"status"})

	// OrderLatencySeconds measures the round-trip time from REST handler entry to
	// engine ack (or risk-gate rejection). Buckets cover sub-millisecond to 5s.
	OrderLatencySeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "oex_gateway_order_latency_seconds",
		Help:    "End-to-end order submission latency in seconds.",
		Buckets: prometheus.DefBuckets, // .005 .01 .025 .05 .1 .25 .5 1 2.5 5 10
	})

	// RiskGateRejectTotal counts how many orders were rejected by the risk gate
	// before reaching the engine (post-trade eventually-consistent blocks).
	RiskGateRejectTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "oex_gateway_risk_gate_reject_total",
		Help: "Orders rejected by the in-memory risk gate (velocity / exposure limit).",
	})

	// WSClientsGauge tracks the number of currently connected WebSocket clients.
	WSClientsGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "oex_gateway_ws_clients",
		Help: "Current number of connected WebSocket clients.",
	})

	// TradesBroadcastTotal counts trade events fanned out to WS clients from the
	// Kafka trades topic.
	TradesBroadcastTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "oex_gateway_trades_broadcast_total",
		Help: "Trade events broadcast to WebSocket clients.",
	})

	// RiskSignalsTotal counts inbound risk signals, labelled by action
	// (ALLOW / REJECT) so you can see the rate of gate state changes.
	RiskSignalsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "oex_gateway_risk_signals_total",
		Help: "Risk signals consumed from the risk-signals Kafka topic.",
	}, []string{"action"})
)

# ADR 0005 — Risk-signal gating (risk → Kafka `risk-signals` → gateway reject path)

- **Status:** Accepted
- **Date:** 2026-06-17

## Context
The risk service computes per-account exposure (positions, gross notional, order velocity) against
configurable limits, and anomaly scores for order patterns. That analysis is useless unless it can
*act*: the system must stop accepting orders from an account that has breached its limits. This
closes the loop opened by ADR 0003 (trades) and ADR 0004 (orders) — those streams feed the risk
service; this ADR is how its verdict flows back to the order path.

## Decision
The risk service **publishes a protobuf `RiskSignal` to the `risk-signals` topic**, and the **gateway
consumes it into an in-memory per-account gate** that the order path checks before forwarding to the
engine.

- **Producer = risk service.** After each `trades`/`orders` event updates an account's state, the
  consumer re-scores the affected account(s) with the deterministic `RiskScorer` and emits a signal:
  `REJECT` on any breach, `ALLOW` otherwise. The `/score-order` HTTP path also emits an `ANOMALY`
  signal. Keyed by `account_id` so the latest signal per account always wins.
- **Serialization:** the protobuf `RiskSignal` from the shared `proto/` contract — same contract-first
  rule as `trades`/`orders`. (This replaces an earlier JSON placeholder in the consumer.)
- **Consumer = gateway gate.** `internal/risksignal` keeps a `map[account]→{reason}` updated by the
  consumer goroutine and read by request goroutines (RWMutex). `handleSubmit` calls `gate.Blocked()`
  before the engine; a blocked account gets `422` with the risk reason and the attempt is still
  published to the `orders` topic (as REJECTED — a rejected attempt is itself a signal).

## Key properties (the trade-offs)
- **Fail-open.** If the broker or risk service is down, the gate simply stops receiving updates and
  no account is blocked. Blocking *all* trading because the risk sidecar is offline would be worse
  than the risk it mitigates — risk gating is a safety overlay, not a correctness gate on money (the
  ledger and the engine's own validation remain authoritative).
- **Eventually consistent, post-trade.** The signal that blocks an account is derived from an order
  or trade that already happened, so the *first* breaching action gets through and subsequent ones
  are blocked. This is a post-trade gate, not a synchronous pre-trade limit check inside the match
  loop. Accepted deliberately: it keeps the hot order path to an O(1) in-memory lookup with no
  network hop to Python per order, and it matches how real exposure limits (which need filled
  positions to compute) actually behave.
- **Self-clearing.** When exposure drops back within limits, the next `ALLOW` signal removes the
  block — no manual reset.

## Alternatives considered
- **Synchronous call to the risk service per order (HTTP `/score-order`):** adds a network hop and a
  hard dependency on Python availability to the hot path; a risk-service blip would stall or fail all
  orders. Rejected for the standing gate; the synchronous endpoint still exists for explicit scoring.
- **Gate inside the engine:** the engine is single-writer-per-symbol and authoritative for matching;
  loading per-account risk state and a Kafka consumer into it would couple concerns and cross the
  symbol-sharding boundary. The edge (gateway) is the right place to refuse an order. Rejected.
- **JSON on `risk-signals`:** a second schema drifting from `proto/`. Rejected (same as ADR 0004).

## Consequences
- Three Kafka topics now form the loop: `orders` + `trades` → risk → `risk-signals` → gateway.
- The gateway has two consumers (trade tape, risk gate) and one producer (order feed); all fail
  independently and degrade gracefully.
- The block is best-effort and eventually consistent; a hard pre-trade limit check (synchronous, or
  enforced in the engine) is a possible future hardening, noted in `docs/roadmap.md`.

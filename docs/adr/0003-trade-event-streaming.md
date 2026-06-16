# ADR 0003 — Trade event streaming (engine → Kafka)

- **Status:** Accepted
- **Date:** 2026-06-17

## Context
Once an order matches, two downstream consumers need to know: the **gateway** (to push a live trade
tape to the dashboard over WebSocket) and the **risk/ML service** (to score anomalies and per-account
exposure). The engine must not call those services directly — that would couple it to their
availability and let a slow consumer back up the order path. We need a durable, replayable,
fan-out-friendly transport between them.

## Decision
The engine **publishes each executed trade to a Kafka topic `trades`**.

- **Serialization:** the protobuf `Trade` message — the same `proto/` contract every service shares,
  so no second schema to keep in sync. The message value is the protobuf bytes.
- **Partition key = symbol.** All trades for one symbol land on one partition and stay strictly
  ordered, which the tape and any per-symbol aggregation rely on. Different symbols parallelize
  across partitions (consistent with the engine's single-writer-per-symbol model).
- **Producer config:** `acks=all` + `enable.idempotence=true` — no lost or duplicated records on a
  broker-side retry.
- **Ordering vs. the ledger:** a trade is written to the **double-entry ledger first** (the source of
  truth for money), and only then published. Publishing is **best-effort and asynchronous**: a failed
  send is logged, never retried inline, and never fails the order ack. A broker outage therefore
  costs the *tape* an event, not the correctness of the books.

## Known gap & the durable fix
Best-effort publish-after-commit means a trade can be in the ledger but missing from the topic if the
broker is down at that instant — the stream is not guaranteed to mirror the ledger. The standard fix
is the **transactional outbox**: in the same DB transaction that writes the ledger, insert the event
into an `outbox` table; a separate relay (or Debezium CDC) tails that table and publishes to Kafka,
giving at-least-once delivery that exactly matches the ledger. Deferred — tracked in
`docs/roadmap.md`. It also subsumes the durable-`trade_id` limitation, since the outbox row carries a
stable id.

## Alternatives considered
- **Engine calls gateway/risk directly (gRPC/HTTP):** tight coupling, no replay, head-of-line
  blocking on a slow consumer. Rejected.
- **JSON on the topic:** human-readable but a second schema drifting from `proto/`. Rejected — the
  protobuf contract is already the single source of truth.
- **Synchronous publish before acking the order:** would make order latency depend on Kafka and
  could fail an order whose money already settled. Rejected.

## Consequences
- Adds Kafka to the local stack (`deploy/docker-compose.yml`, already present; started via
  `make infra`).
- Consumers are decoupled and independently scalable; new consumers (analytics, audit) just join the
  topic.
- The best-effort gap is documented and has a known, non-rewrite upgrade path (outbox).

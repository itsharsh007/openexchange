# ADR 0004 — Order event streaming (gateway → Kafka `orders`)

- **Status:** Accepted
- **Date:** 2026-06-17

## Context
The risk/ML service needs the stream of **order attempts** to build per-account features for its
anomaly model (order velocity, sizing, rapid-cancel / spoofing patterns). Crucially this includes
attempts the engine **rejects** — a burst of rejected or rapidly-cancelled orders is itself one of
the strongest fraud signals. The `trades` topic (ADR 0003) only carries *executed* trades, so it
cannot supply this. We need a separate stream of every order the system was asked to perform.

## Decision
The **Go gateway publishes an `OrderEvent` to a Kafka topic `orders`** for every order it
processes — new submissions and cancels alike.

- **Producer = the gateway, not the engine.** The gateway is the only component that observes *every
  attempt* at the authenticated edge, including ones the engine never accepts (rejected, malformed,
  rate-limited). It also already holds the verified `account_id`. The engine only ever sees orders
  that passed the gateway and reached it over gRPC, so it cannot be the source of "all attempts".
- **Serialization:** the protobuf `OrderEvent` message in the shared `proto/` contract — the same
  contract-first rule as `trades`. The wire shape is pinned once here, not guessed per service (this
  is the exact mistake the `trades` topic hit before ADR 0003; see the build journal).
- **`OrderEvent` carries** the order identity (client_order_id, account_id, symbol, side, type,
  price_ticks, quantity), an `is_cancel` flag, the engine-assigned `order_id`, the resulting
  `status` (ACCEPTED / REJECTED / CANCELLED / …), and `ts_millis`. Cancels leave side/type/qty
  unset — the gateway does not look up the resting order, and the model only needs the
  account/symbol/cancel signal.
- **Partition key = account_id.** The risk features are per-account, so one account's flow lands on
  one partition and stays strictly ordered. (Contrast `trades`, keyed by symbol — there the
  per-symbol tape ordering is what matters.)
- **Best-effort + asynchronous + publish-after-ack.** The event is published only after the engine
  has acked the order, fire-and-forget via the kafka-go async writer (`RequiredAcks=all`,
  failures logged via the completion callback). This stream feeds ML features, never the money path:
  a broker outage must degrade risk features, never fail or slow a customer's order. Same trade-off,
  and same durable upgrade path (transactional outbox), as ADR 0003.

## Alternatives considered
- **Engine publishes the orders stream:** can't see rejected/rate-limited attempts (they never reach
  it), which are the most valuable anomaly signal. Rejected.
- **JSON on the topic:** a second schema drifting from `proto/`; the consumer already had a tolerant
  JSON path as a placeholder, but pinning protobuf removes the drift risk. Rejected.
- **Reuse the `NewOrder` proto message:** it has no `is_cancel`, no resulting `status`, and no
  `ts_millis`, so it can't represent a cancel or a rejection outcome. A dedicated `OrderEvent` is the
  right envelope for "what the gateway observed".
- **Synchronous publish before acking:** would make order latency depend on Kafka. Rejected (same
  reasoning as ADR 0003).

## Consequences
- The risk service decodes protobuf on **both** `trades` and `OrderEvent` on `orders`; the tolerant
  JSON path remains only as a fallback for any other topic.
- New consumers of order flow (audit, surveillance, analytics) can join the `orders` topic without
  touching the gateway.
- Adds an async Kafka writer to the gateway; it connects lazily, so a broker down at boot is
  non-fatal and REST/WS keep serving.
- The best-effort gap (an attempt acked to the client but missing from the topic on a broker outage)
  is acceptable for an ML feature stream and shares the outbox upgrade path with ADR 0003.

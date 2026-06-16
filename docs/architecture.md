# Architecture

## Goals
- Realistic exchange mechanics (price-time priority matching) that are correct and fast.
- Clear service boundaries, each in a different stack, communicating over well-defined contracts.
- Observable, testable, and deployable on free infrastructure.

## Components

### Matching engine (`engine/`, Java 17 + Spring Boot)
Authoritative source of truth for order state. Holds an in-memory **order book per symbol**:
- Each side (bids/asks) is a price-ordered map of price level → FIFO queue of resting orders.
- Bids sorted descending, asks ascending, so best prices are at the head.
- Matching: an incoming order is checked against the opposite side's best price; fills are produced
  in **price, then time** priority until the order is filled or no longer crosses.
- Supports LIMIT and MARKET orders, partial fills, cancel, and amend.

**Concurrency:** one writer thread per symbol consuming a command queue. This avoids locks on the
hot path while keeping each book strictly serialized — easy to reason about and fast.

**Outputs:** trade events + order-status events to Kafka; fills persisted to the Postgres ledger.

### Gateway (`gateway/`, Go)
The network edge. REST for order submit/cancel, WebSocket fan-out for live book + trades, gRPC
client to the engine. Cross-cutting concerns: JWT auth, token-bucket rate limiting, request
validation, Redis caching of top-of-book.

### Risk / ML (`risk/`, Python + FastAPI)
Kafka consumer building a feature store in Postgres. Three models:
1. **Price prediction** — short-horizon next-tick direction (gradient-boosted trees).
2. **Anomaly / fraud** — isolation forest over order-pattern features (size, frequency, timing).
3. **Risk exposure** — per-account position/exposure scoring against limits.
Exposes FastAPI endpoints and publishes risk signals back to Kafka; gateway can reject breaching
orders.

### Web (`web/`, React + TS)
Live order book, depth chart, trade tape, order entry, account P&L, and an ML/risk panel. Subscribes
over WebSocket with reconnect/backoff.

## Backing services
- **Kafka** — event backbone (topics: `orders`, `trades`, `market-data`, `risk-signals`).
- **Postgres** — durable ledger + order history + feature store.
- **Redis** — hot cache for top-of-book and rate-limit counters.

## Key invariants
- The ledger always balances (double-entry): every trade debits one account and credits another.
- Order IDs are idempotent — re-delivery never creates duplicate trades.
- The engine is authoritative; Redis is a cache and may lag, bounded by refresh cadence.

See `adr/` for the reasoning behind each major choice.

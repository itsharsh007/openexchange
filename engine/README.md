# engine — matching engine (Java 17 + Spring Boot)

The centerpiece service. Maintains an in-memory limit order book per symbol, matches orders with
price-time priority, and exposes a gRPC API that the Go gateway calls. This is the authoritative
source of truth for order state.

## What it does
- **Order book** — one `OrderBook` per symbol: a `TreeMap<priceTicks, FIFO queue>` per side (bids
  descending, asks ascending) plus a `HashMap<orderId, Order>` for O(1) cancels. Prices are integer
  **ticks**, never floats, so money math has no rounding bugs. See `docs/adr/0002-*`.
- **Matching** — LIMIT and MARKET orders, partial fills, cancels. Trades execute at the resting
  (maker) price; price improvement accrues to the taker.
- **Concurrency** — the `MatchingEngine` runs **one single-writer thread per symbol**. All commands
  for a symbol funnel through that one thread, so the hot path needs no locks, yet different symbols
  run fully in parallel (the LMAX-Disruptor idea: serialize per partition, parallelize across them).
- **gRPC API** — `SubmitOrder`, `CancelOrder`, `GetBook` (see `proto/openexchange.proto`).

## Layout
```
src/main/java/com/openexchange/engine/
  EngineApplication.java        Spring Boot entry point; registers the MatchingEngine bean
  MatchingEngine.java           single-writer-per-symbol dispatcher (CompletableFuture API)
  book/OrderBook.java           the order book + matching logic (not thread-safe by design)
  book/MatchResult.java         outcome of one submit: status, filled qty, trades
  model/                        Order, Trade, Side, OrderType, OrderStatus
  grpc/
    GrpcServerRunner.java       SmartLifecycle bean — starts/stops the gRPC server with the context
    MatchingEngineService.java  proto <-> domain mapping; the gRPC service implementation
src/main/resources/application.yml   env-driven config (ports, default book depth)
```

The Java protobuf/gRPC stubs are **generated at build time** from `../proto/openexchange.proto` by
the `com.google.protobuf` Gradle plugin (it downloads `protoc` itself — nothing is installed
globally). Generated sources land in `build/generated/` and are not committed.

## Ports & config (Twelve-Factor: all env-overridable)
| Env var | Default | Purpose |
|---|---|---|
| `GRPC_PORT` | `50051` | gRPC order API (what the gateway calls) |
| `HTTP_PORT` | `8081` | HTTP port serving the actuator health endpoint |
| `BOOK_DEFAULT_DEPTH` | `10` | levels per side returned by `GetBook` when `depth=0` |

## Run it
```bash
# from engine/ — uses the project's Gradle wrapper
./gradlew test                 # unit + gRPC integration tests
./gradlew bootRun              # starts gRPC on :50051 and HTTP health on :8081

# verify it's up
curl localhost:8081/actuator/health      # -> {"status":"UP",...}
```

Container build (note: context is the **repo root**, because the build reads `proto/`):
```bash
docker build -f engine/Dockerfile -t openexchange-engine .
```

## How the gRPC layer works
`GrpcServerRunner` implements Spring's `SmartLifecycle`, so the gRPC port comes up only after every
bean (including the engine) is constructed, and shuts down gracefully on `SIGTERM` — draining
in-flight RPCs before the writer threads stop. That clean lifecycle is what lets Kubernetes roll the
pod without dropping orders.

`MatchingEngineService` is a thin translator: it converts a protobuf `NewOrder` into a domain
`Order` (the engine assigns the authoritative order id + arrival sequence; the client only supplies
its idempotency key), submits it, blocks on the returned `CompletableFuture` until the symbol's
single writer has processed it, then maps the `MatchResult` back to an `OrderAck`. Bad input
(non-positive quantity/price) surfaces as a gRPC `INVALID_ARGUMENT` rather than crashing the stream.

## Tests
- `OrderBookTest` (9) — matching correctness, price-time priority, partial fills, market sweeps.
- `MatchingEngineTest` (2) — single-writer dispatch; a 4,000-order, 8-thread test proves quantity
  conservation under concurrency.
- `MatchingEngineServiceTest` (3) — drives the real gRPC server over a real client channel:
  resting-buy-then-crossing-sell fill, cancel, and invalid-quantity rejection.
- `KafkaTradePublisherTest` (1) — publishes a trade to an in-JVM broker and consumes it back,
  asserting the protobuf round-trips and the record is keyed by symbol.

## Double-entry ledger (Postgres)
Every executed trade is persisted to Postgres as **balanced double-entry postings** before the
client is acked (`ledger/`). The engine is authoritative for *matching*; the ledger is authoritative
for *money*. For a trade of `qty` @ `price`:

```
buyer : CASH -price*qty   buyer : SYMBOL +qty
seller: CASH +price*qty   seller: SYMBOL -qty
```

The signed deltas of each asset **sum to zero** — nothing is created or destroyed. Balances are
*derived* from the entries (the `account_balances` view), never stored directly. Schema is managed
by **Flyway** (`src/main/resources/db/migration/`), applied automatically on startup. Writes are one
transaction and idempotent on `trade_id`. Config (env): `DB_URL` / `DB_USER` / `DB_PASSWORD`
(defaults target `localhost:5432/openexchange`, user `oex`).

Verify after a trade:
```sql
SELECT asset, SUM(delta) FROM ledger_entries GROUP BY asset;  -- every row must be 0
SELECT * FROM account_balances;
```

## Trade event stream (Kafka)
After a trade is durably in the ledger, the engine publishes it to the Kafka **`trades`** topic
(`stream/`), where the gateway's trade tape and the Python risk service consume it. The wire format
is the shared protobuf `Trade` message, **keyed by symbol** (so a symbol's trades stay on one
partition, strictly ordered). The producer runs `acks=all` + idempotence.

Publishing is **best-effort and happens after the ledger write** — the ledger is the source of truth
for money, so a broker outage costs the tape an event, never the books. Abstracted behind
`TradePublisher` (with a `NOOP` for tests). See **ADR 0003** for the semantics and the durable
upgrade path (transactional outbox). Config (env): `KAFKA_BOOTSTRAP` (default `localhost:9092`),
`TRADES_TOPIC` (default `trades`).

The publish path is verified by `KafkaTradePublisherTest`, which runs a real broker **in-JVM**
(`@EmbeddedKafka`) — no containers needed.

## Still to wire (Phase 1 remainder)
- Throughput benchmark (orders/sec) recorded here.
- Durable `trade_id` (see `docs/roadmap.md` → Known limitations; subsumed by the ADR 0003 outbox).

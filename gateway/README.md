# OpenExchange Gateway (Go)

The **network edge** of OpenExchange. It is the only service the React dashboard
talks to. It terminates HTTP/WebSocket, authenticates and rate-limits callers,
validates requests, forwards orders to the Java matching engine over gRPC, fans
live trades/book updates out to all connected dashboards, and caches
top-of-book in Redis.

```
React dashboard ──REST + WS──> [ gateway ] ──gRPC──> Java matching engine
                                   │  ▲                      │
                                   │  └── Kafka `trades` ◄────┘ (live trade tape)
                                   └── Redis (hot top-of-book cache)
```

> Simulation only — play money, fake symbols. No real funds.

## Contents
- [What it does](#what-it-does)
- [Architecture / package layout](#architecture--package-layout)
- [Configuration](#configuration)
- [Running it](#running-it)
- [Endpoints (with curl)](#endpoints-with-curl)
- [How auth works (JWT)](#how-auth-works-jwt)
- [How rate limiting works (token bucket)](#how-rate-limiting-works-token-bucket)
- [How the WebSocket hub works](#how-the-websocket-hub-works)
- [Redis caching & graceful degradation](#redis-caching--graceful-degradation)
- [Swapping in the real gRPC client](#swapping-in-the-real-grpc-client)
- [Docker](#docker)
- [Testing & verification](#testing--verification)

## What it does
1. **REST**: submit/cancel orders, fetch an order-book snapshot, health check.
2. **WebSocket**: a fan-out hub broadcasting live trades + book updates to every
   connected client.
3. **Trade tape**: a Kafka consumer of the engine's `trades` topic that fans every
   *real* executed trade out to all dashboards over WebSocket (`internal/tape/`).
4. **Order feed**: publishes an `OrderEvent` to the Kafka `orders` topic for every
   order attempt (incl. rejected/cancelled) for the risk service's anomaly features
   (`internal/orderfeed/`; best-effort + async — never affects order handling).
5. **Risk gate**: consumes the risk service's `risk-signals` topic into a per-account
   gate; orders from a flagged (limit-breaching) account are rejected before reaching
   the engine (`internal/risksignal/`; fails open if the risk feed is down).
6. **gRPC client** to the Java matching engine (real adapter in
   `internal/engine/grpc_client.go`; a `MockClient` remains for tests).
7. **Middleware**: per-client token-bucket rate limiting + JWT bearer auth.
8. **Redis** caching of top-of-book, degrading gracefully when Redis is down.

## Architecture / package layout
```
cmd/gateway/main.go          # boot, wiring, graceful shutdown
internal/config/             # env-var config (Twelve-Factor)
internal/engine/             # EngineClient interface, Go domain types (mirror proto),
                             #   real GRPCClient adapter + MockClient (tests)
genproto/                    # generated protobuf/gRPC stubs (make proto)
internal/middleware/         # ratelimit.go (token bucket), auth.go (JWT)
internal/cache/              # Redis wrapper with graceful degradation
internal/ws/                 # WebSocket fan-out hub (register/unregister/broadcast)
internal/tape/               # Kafka `trades` consumer -> decode proto Trade -> hub.Broadcast
internal/orderfeed/          # Kafka `orders` producer -> proto OrderEvent (risk anomaly features)
internal/risksignal/         # Kafka `risk-signals` consumer -> per-account reject Gate
internal/api/                # HTTP handlers + routing
```
**Design principle — dependency inversion:** handlers depend on the small
`engine.EngineClient` interface, never on gRPC directly. That keeps the gateway
unit-testable against a `MockClient`, and meant the real gRPC client dropped in
as a one-line change in `main.go` with zero handler changes.

## Configuration
All config comes from environment variables (Twelve-Factor). Sane defaults let
it boot with zero config in dev.

| Env var | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `ENGINE_GRPC_ADDR` | `localhost:50051` | Java engine gRPC address |
| `REDIS_ADDR` | `localhost:6379` | Redis endpoint |
| `JWT_SECRET` | `dev-only-change-me` | HMAC secret for bearer tokens (warns on default) |
| `RATE_LIMIT_RPS` | `20` | Sustained tokens/sec per client |
| `RATE_LIMIT_BURST` | `40` | Bucket capacity (max burst) per client |
| `ENGINE_TIMEOUT` | `3s` | Per-call timeout to the engine |
| `CACHE_TTL` | `1s` | Freshness window for cached book snapshots |
| `KAFKA_BOOTSTRAP` | `localhost:9092` | Kafka brokers for the trade-tape consumer |
| `TRADES_TOPIC` | `trades` | Topic the engine publishes executed trades to |
| `TAPE_CONSUMER_GROUP` | `gateway-tape` | Consumer group for the trade tape |
| `ORDERS_TOPIC` | `orders` | Topic the gateway publishes every order attempt to (risk features) |
| `RISK_SIGNALS_TOPIC` | `risk-signals` | Topic of RiskSignals the gateway gates orders on |
| `RISK_CONSUMER_GROUP` | `gateway-risk` | Consumer group for the risk-signals gate |

## Running it
```bash
cd gateway
go run ./cmd/gateway
# gateway: listening on :8080   (logs a WARN if JWT_SECRET/Redis are dev defaults)
```

Generate a dev JWT for testing (HS256, the `sub` claim becomes the account id).
Any small script works; for example with Python + PyJWT:
```bash
python3 -c "import jwt,time;print(jwt.encode({'sub':'acct-1','exp':int(time.time())+3600},'dev-only-change-me',algorithm='HS256'))"
```
Export it for the curl examples:
```bash
TOKEN=$(python3 -c "import jwt,time;print(jwt.encode({'sub':'acct-1','exp':int(time.time())+3600},'dev-only-change-me',algorithm='HS256'))")
```

## Endpoints (with curl)

### `GET /healthz` — unauthenticated liveness
```bash
curl -s localhost:8080/healthz
```
```json
{"redis":false,"status":"ok","time":"2026-06-16T12:00:00Z"}
```

### `POST /orders` — submit an order
The authenticated account (`sub` claim) overrides any `account_id` in the body.
Prices are **integer ticks** (cents); `price_ticks` is ignored for MARKET.
```bash
curl -s -X POST localhost:8080/orders \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"client_order_id":"c-1","symbol":"ACME","side":"BUY","type":"LIMIT","price_ticks":10000,"quantity":5}'
```
```json
{"order_id":"o1","status":"ACCEPTED","filled_quantity":0}
```
The `order_id` is assigned by the engine. A crossing order returns
`{"status":"FILLED", ...}`. The resulting trade reaches dashboards over the
WebSocket tape, sourced from the engine's Kafka `trades` topic (not synthesized
from this ack — see [How the trade tape works](#how-the-trade-tape-works)).
Rejections return HTTP 422 with a `reason`.

### `DELETE /orders/{id}` — cancel
```bash
curl -s -X DELETE "localhost:8080/orders/ord-1?symbol=ACME" \
  -H "Authorization: Bearer $TOKEN"
```
```json
{"order_id":"ord-1","status":"CANCELLED","filled_quantity":0}
```

### `GET /book/{symbol}?depth=N` — order-book snapshot
Cache-aside: served from Redis on a hit (`X-Cache: HIT`), else fetched from the
engine and backfilled (`X-Cache: MISS`).
```bash
curl -s "localhost:8080/book/ACME?depth=3" -H "Authorization: Bearer $TOKEN"
```
```json
{
  "symbol": "ACME",
  "bids": [{"price_ticks":9999,"quantity":10},{"price_ticks":9998,"quantity":20},{"price_ticks":9997,"quantity":30}],
  "asks": [{"price_ticks":10001,"quantity":10},{"price_ticks":10002,"quantity":20},{"price_ticks":10003,"quantity":30}],
  "ts_millis": 1750000000000
}
```

### `GET /ws` — WebSocket stream
Connect with the bearer token; receive JSON events like
`{"type":"trade","trade":{...}}`.
```bash
# requires `websocat`
websocat "ws://localhost:8080/ws" -H "Authorization: Bearer $TOKEN"
```

## How auth works (JWT)
- `internal/middleware/auth.go` parses the `Authorization: Bearer <jwt>` header.
- It verifies the HMAC (HS256) signature with `JWT_SECRET` and **explicitly
  rejects any non-HMAC algorithm** — this defeats the classic `alg=none` and
  key-confusion attacks.
- The token's `sub` claim is taken as the account id and stored in the request
  context; handlers attribute orders to it rather than trusting the request body.
- `/healthz` is wired without this middleware so load balancers can probe it.

## How rate limiting works (token bucket)
`internal/middleware/ratelimit.go`. Each client (keyed by IP / `X-Forwarded-For`)
has a bucket of capacity `burst`. Tokens refill continuously at `RATE_LIMIT_RPS`.
Every request consumes one token; if the bucket is empty the request gets `429`
with `Retry-After: 1`.
- **Why token bucket:** it allows short legitimate bursts (a trader firing a few
  orders) while bounding the *sustained* rate — smoother than a fixed window,
  which spikes at window edges.
- Refill is computed lazily from elapsed time (`tokens = min(burst, tokens +
  elapsed*rate)`), so there is no background goroutine — O(1) per request.
- The clock is injectable, which makes the unit tests fully deterministic.
- This is in-process per replica. For a multi-replica deployment, back it with
  Redis atomic counters so the limit is global (documented as a follow-up).

## How the WebSocket hub works
`internal/ws/hub.go`. One `Run()` goroutine is the **sole owner** of the clients
map; clients are added/removed and messages fanned out via channels
(`register` / `unregister` / `broadcast`). This single-writer model needs no
mutex on the hot path and is trivially race-free (verified under `-race`).

Each client has a buffered `send` channel and two goroutines: a `writePump`
(drains the buffer, sends pings) and a `readPump` (processes pongs/close,
detects dead peers). **Backpressure:** the hub does a *non-blocking* send to each
client; if a client's buffer is full it's deemed too slow and is dropped —
one laggard can never stall the entire feed.

## How the trade tape works
`internal/tape/tape.go`. A `kafka-go` reader subscribes to the engine's `trades`
topic as consumer group `TAPE_CONSUMER_GROUP`. For each record it
`proto.Unmarshal`s the **protobuf `Trade`** (the same `proto/` contract the engine
publishes), maps it to the dashboard envelope `{"type":"trade","trade":{…}}`, and
hands it to `hub.Broadcast` for fan-out.

**Why consume the engine's stream instead of synthesizing a trade from the order
ack?** The ack only describes *this* submitter's side. The tape must show every
trade to everyone, including the resting (maker) counterparty that never called
this gateway — and at the engine's authoritative price. Only the engine's trade
stream has that complete view. Kafka also decouples the two: a gateway restart
never blocks the engine, and each gateway replica is its own group member.

**Resilience:** the reader connects lazily and reconnects with backoff, so a
broker that is down at boot (or mid-run) is non-fatal — REST/WS keep serving and
the tape resumes when Kafka returns. A single undecodable record is logged and
skipped, never stalling the loop. The consumer starts from the latest offset (a
live feed, not a historical backfill). Config: `KAFKA_BOOTSTRAP`, `TRADES_TOPIC`,
`TAPE_CONSUMER_GROUP`.

## Redis caching & graceful degradation
`internal/cache/cache.go`. Top-of-book is cached with a short TTL (`CACHE_TTL`).
Crucially, **a cache failure never fails a request**: on any Redis error `GetBook`
reports a miss and `SetBook` is a no-op, so the gateway falls back to the engine.
A Redis outage degrades latency, not availability. `/healthz` reports current
Redis reachability without ever failing.

## Engine gRPC client
`main.go` uses the real `engine.GRPCClient` (`internal/engine/grpc_client.go`),
which dials `ENGINE_GRPC_ADDR` and translates our plain Go types ↔ the protobuf
structs in `genproto/`. `grpc.NewClient` is lazy, so the gateway boots even if
the engine is briefly down; each RPC carries a `ENGINE_TIMEOUT` deadline.

Regenerate the Go stubs after any change to `proto/openexchange.proto`:
```bash
make proto      # from the repo root
```
`engine.MockClient` is kept for tests and offline UI work — wire it in `main.go`
instead of `NewGRPCClient` to run the gateway without a live engine.

### Verified end-to-end
With the engine on `:50051` and the gateway on `:8080`, a resting BUY then a
crossing SELL produced a real fill from the Java engine (`order_id` `o1`/`o2`):
```
POST /orders BUY  15000x10 -> {"order_id":"o1","status":"ACCEPTED"}
GET  /book/AAPL            -> bids:[{15000,10}] asks:[]
POST /orders SELL 15000x10 -> {"order_id":"o2","status":"FILLED","filled_quantity":10}
GET  /book/AAPL            -> bids:[] asks:[]
```

## Docker
Multi-stage build → a static binary on a distroless `nonroot` base.
```bash
cd gateway
docker build -t openexchange-gateway .
docker run --rm -p 8080:8080 \
  -e JWT_SECRET=change-me -e REDIS_ADDR=host.docker.internal:6379 \
  openexchange-gateway
```

## Testing & verification
```bash
cd gateway
go mod tidy
go build ./...
go vet ./...
go test -race ./...
```
Tests cover the token-bucket limiter (burst, per-client isolation, burst cap via
an injected clock), the WS hub (broadcast delivery + slow-client drop), and the
trade tape (protobuf `Trade` → dashboard envelope decode, hub forwarding) — all
clean under the race detector.
```
ok  github.com/itsharsh007/openexchange/gateway/internal/middleware
ok  github.com/itsharsh007/openexchange/gateway/internal/tape
ok  github.com/itsharsh007/openexchange/gateway/internal/ws
```

> Live end-to-end (engine → real Kafka broker → tape → WS) is not covered by
> `go test` (no embedded broker in Go); it's exercised in the integrated demo
> once `make infra` is up.

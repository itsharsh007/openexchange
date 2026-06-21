# OpenExchange

[![CI](https://github.com/itsharsh007/openexchange/actions/workflows/ci.yml/badge.svg)](https://github.com/itsharsh007/openexchange/actions/workflows/ci.yml)
[![Docker](https://github.com/itsharsh007/openexchange/actions/workflows/docker.yml/badge.svg)](https://github.com/itsharsh007/openexchange/actions/workflows/docker.yml)

A polyglot, real-time **simulated trading exchange**. Orders flow from a React dashboard through a
Go API gateway into a Java matching engine, which matches them with price-time priority, streams
trades over WebSockets, persists a double-entry ledger in Postgres, and feeds a Python ML service
that predicts short-term price moves and flags anomalous / risky order flow.

> ⚠️ **Simulation only.** OpenExchange uses *play money* and fake symbols. It is not a real
> exchange, handles no funds, and is not investment advice.

> 🧒 New here? Read [**OpenExchange explained like you're 10**](docs/explain-like-im-10.md) for the
> no-jargon version before diving into the architecture.

## What it demonstrates

OpenExchange is a from-scratch implementation of the hard parts of an exchange, chosen to cover the
full surface of modern backend engineering in one coherent system:

- **Data structures** — a per-symbol limit order book with price-time priority.
- **Concurrency** — a single-writer matching engine (no cross-symbol shared mutable state).
- **Distributed systems** — event streaming over Kafka across four independent services.
- **Storage & caching** — a double-entry Postgres ledger with a Redis hot-book cache.
- **Real-time networking** — WebSocket fan-out of trades and book updates.
- **Machine learning** — short-horizon price prediction plus anomaly / risk scoring.
- **DevOps** — containerized services, CI, metrics, and dashboards.

## Architecture

```
React dashboard ──WS/REST──> Go gateway ──gRPC──> Java matching engine
       ▲                         │                       │
       │ live ticks              │ publishes             │ emits trades / order events
       └────WS───────────────────┘                       ▼
                                              Kafka (orders, trades, market-data)
                                               │                         │
                                               ▼                         ▼
                                    Python risk/ML service        Postgres (ledger,
                                    (price prediction, fraud/      order history) + Redis
                                     anomaly, risk scoring)        (hot order-book cache)
```

| Service | Stack | Responsibility |
|---|---|---|
| `engine/`  | Java 17 + Spring Boot | Order book, matching, account ledger |
| `gateway/` | Go | REST + WebSocket + gRPC, auth, rate limiting, caching |
| `risk/`    | Python 3.12 + FastAPI | ML: price prediction, anomaly/fraud, risk scoring |
| `web/`     | React + TypeScript + Vite | Live trading dashboard |
| `deploy/`  | Docker Compose, K8s, Grafana | Local + cloud orchestration |
| `proto/`   | Protobuf | Shared service contracts |

## How an order flows through the system

1. A user submits a limit order in the React UI.
2. Go gateway authenticates (JWT), rate-limits, validates, and forwards via gRPC.
3. Java engine places it in the per-symbol order book; if it crosses, it matches with
   **price-time priority** and produces one or more trades.
4. Trades + order-status events are published to Kafka and written to the Postgres ledger
   (double-entry — every trade balances).
5. The gateway fans trades/book updates out to subscribed dashboards over WebSocket.
6. The Python service consumes the stream, updates features, scores risk/anomaly, and can signal
   the gateway to reject orders that breach limits.

## Performance & resilience

Measured on a 4-core Linux box with the full stack up (real engine, Kafka, Postgres),
`hey` at 50 concurrent connections for 20s per path:

| Path | Throughput | Avg latency | Slowest |
|---|---|---|---|
| `GET /book/{symbol}` (read, Redis-cached) | **~17,800 req/s** | 2.8 ms | 264 ms |
| `POST /orders` (write, gRPC → engine) | **~15,800 req/s** | 3.2 ms | 44 ms |

**Resilience** (`scripts/chaostest.sh`) — killing the engine mid-flight:

- Orders return a clean `502` (the gateway never crashes); `/healthz` stays `200` (degraded, not dead).
- The Postgres ledger is untouched and stays **balanced** (every asset nets to 0) throughout.
- On engine restart the gateway recovers automatically — orders return `201` again, ledger still balanced.

Reproduce: `make up && make seed`, then `./scripts/loadtest.sh` and `./scripts/chaostest.sh`.

## Quick start (local)

```bash
# one-time: install a free container runtime (macOS)
brew install colima docker docker-compose
make up        # start infra (Postgres, Redis, Kafka) + services
make seed      # create demo accounts/symbols and simulated order flow
make test      # run every service's test suite
make down      # stop everything
```

## Live demo

| Surface | URL |
|---|---|
| Dashboard (React) | https://itsharsh007.github.io/openexchange |
| Gateway API | https://openexchange-gateway.koyeb.app/healthz |
| WebSocket feed | wss://openexchange-gateway.koyeb.app/ws |

The gateway runs in `ENGINE_MODE=mock` — REST + WebSocket fully live, no Java engine needed.
Kafka, Postgres, and the risk service run locally; see the demo video in [`docs/`](docs/).

Deploy your own: [`docs/cloud-deploy.md`](docs/cloud-deploy.md) (no credit card required).

## Docker images (GHCR)

Every merge to `main` publishes four images to GitHub Container Registry:

```
ghcr.io/itsharsh007/openexchange-engine:latest
ghcr.io/itsharsh007/openexchange-gateway:latest
ghcr.io/itsharsh007/openexchange-risk:latest
ghcr.io/itsharsh007/openexchange-web:latest
```

See [`docs/`](docs/) for architecture, ADRs, the Twelve-Factor compliance map, the deployment
roadmap, and per-service deep-dives.

## License

MIT — see [LICENSE](LICENSE).

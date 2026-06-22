# OpenExchange

[![CI](https://github.com/itsharsh007/openexchange/actions/workflows/ci.yml/badge.svg)](https://github.com/itsharsh007/openexchange/actions/workflows/ci.yml)
[![Docker](https://github.com/itsharsh007/openexchange/actions/workflows/docker.yml/badge.svg)](https://github.com/itsharsh007/openexchange/actions/workflows/docker.yml)
[![Open in GitHub Codespaces](https://github.com/codespaces/badge.svg)](https://codespaces.new/itsharsh007/openexchange)

**▶ [Try the live dashboard](https://itsharsh007.github.io/openexchange/)** &nbsp;·&nbsp; **🚀 [Run the full stack in one click](https://codespaces.new/itsharsh007/openexchange)** (Codespaces)

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

## Demo

<!-- VIDEO: replace this line with the uploaded walkthrough.
     Easiest way: open an issue/PR in this repo, drag the .mp4 into the comment box,
     GitHub uploads it and gives a https://github.com/user-attachments/... link —
     paste that link here on its own line and it embeds as a player. -->

> 🎥 **Full walkthrough video:** _coming soon_

**Two ways to try it:**

| | What you get | Setup |
|---|---|---|
| **▶ [Live dashboard](https://itsharsh007.github.io/openexchange/)** | A **real shared exchange** — your orders match against other visitors live (price-time priority, in-gateway engine). Open it in two tabs and trade with yourself, or send the link to a friend. | none, just click |
| **🚀 [Open in Codespaces](https://codespaces.new/itsharsh007/openexchange)** | The **full system** — the Java matching engine, Kafka, Postgres, and the ML risk loop | one click, then `make up && make seed` (~3 min) |

After the Codespace boots, run `make up && make seed`, open the forwarded port **5173**, and place a
**BUY** and a crossing **SELL** — you'll see them match, a trade print on the tape, and the Risk/ML
panel react. That's the whole system end-to-end.

**Live endpoints:**

| Surface | URL |
|---|---|
| Dashboard (React) | https://itsharsh007.github.io/openexchange/ |
| Gateway API | https://openexchange.onrender.com/healthz |
| WebSocket feed | wss://openexchange.onrender.com/ws |

The hosted gateway runs a **real in-process matching engine** (`ENGINE_MODE=local`) so the live link
is a genuine shared exchange — no JVM/Kafka needed. The heavier services (Kafka, Postgres, the ML
risk loop) only run in the full stack. On Render's free tier the first request after ~15 min idle
takes ~1 min to wake.

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

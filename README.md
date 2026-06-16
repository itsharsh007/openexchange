# OpenExchange

A polyglot, real-time **simulated trading exchange**. Orders flow from a React dashboard through a
Go API gateway into a Java matching engine, which matches them with price-time priority, streams
trades over WebSockets, persists a double-entry ledger in Postgres, and feeds a Python ML service
that predicts short-term price moves and flags anomalous / risky order flow.

> ⚠️ **Simulation only.** OpenExchange uses *play money* and fake symbols. It is not a real
> exchange, handles no funds, and is not investment advice.

> 🧒 New here? Read [**OpenExchange explained like you're 10**](docs/explain-like-im-10.md) for the
> no-jargon version before diving into the architecture.

## Why this project

It's built to exercise — and let me explain at length — the full surface of modern backend
reviews: data structures (order books), concurrency (a single-writer matching engine),
distributed systems (event streaming, multiple services), databases, caching, low-latency
networking, machine learning, a real-time frontend, and full DevOps.

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

## The order lifecycle (the story to tell in an review)

1. User submits a limit order in the React UI.
2. Go gateway authenticates (JWT), rate-limits, validates, and forwards via gRPC.
3. Java engine places it in the per-symbol order book; if it crosses, it matches with
   **price-time priority** and produces one or more trades.
4. Trades + order-status events are published to Kafka and written to the Postgres ledger
   (double-entry — every trade balances).
5. The gateway fans trades/book updates out to subscribed dashboards over WebSocket.
6. The Python service consumes the stream, updates features, scores risk/anomaly, and can signal
   the gateway to reject orders that breach limits.

## Quick start (local)

```bash
# one-time: install a free container runtime (macOS)
brew install colima docker docker-compose
make up        # start infra (Postgres, Redis, Kafka) + services
make seed      # create demo accounts/symbols and simulated order flow
make test      # run every service's test suite
make down      # stop everything
```

See [`docs/`](docs/) for architecture, ADRs, the Twelve-Factor compliance map, the going-public
roadmap, and per-service review deep-dive.

## License

MIT — see [LICENSE](LICENSE).

# Twelve-Factor (+3) compliance map

OpenExchange targets the [Twelve-Factor App](https://12factor.net) methodology, extended with the 3
factors from *Beyond the Twelve-Factor App*. This is the industry-standard answer to "what are the
rules of good web/cloud deployment." Each factor below maps to a concrete decision in this repo.

| # | Factor | How OpenExchange satisfies it |
|---|---|---|
| 1 | **Codebase** | One monorepo tracked in git; every deploy comes from the same codebase. |
| 2 | **Dependencies** | Explicit, isolated: Gradle (engine), Go modules (gateway), `requirements.txt`/venv (risk), npm (web). No system-wide assumptions. |
| 3 | **Config** | All config (DB URLs, Kafka brokers, secrets, JWT keys) via **env vars**; `.env` git-ignored. No config in code. |
| 4 | **Backing services** | Postgres, Redis, Kafka are attached resources referenced by URL — swappable local ↔ cloud with no code change. |
| 5 | **Build, release, run** | Distinct stages: Docker image build → tagged release with config → run. CI builds images; deploy runs them. |
| 6 | **Processes** | Services are stateless; all durable state lives in Postgres/Redis/Kafka. (Engine's in-memory book is rebuildable from the event log.) |
| 7 | **Port binding** | Each service self-binds a port and is self-contained; no reliance on an injected webserver. |
| 8 | **Concurrency** | Scale out via process model: gateway scales horizontally; engine scales by sharding symbols across instances. |
| 9 | **Disposability** | Fast startup, graceful shutdown (drain connections, commit offsets); resilient to being killed. |
| 10 | **Dev/prod parity** | Same Docker images and backing-service versions in dev and prod; gap kept small. |
| 11 | **Logs** | Structured logs to stdout as an event stream; aggregation/retention handled by the platform, not the app. |
| 12 | **Admin processes** | One-off tasks (migrations, seeding) run as separate commands (`make seed`, migration jobs), same codebase/config. |
| 13 | **API first** *(beyond-12f)* | Contracts defined in `proto/` before implementation; services integrate against the contract. |
| 14 | **Telemetry** *(beyond-12f)* | Prometheus metrics + trace IDs across the order path; Grafana dashboards in `deploy/grafana`. |
| 15 | **Authentication & authorization** *(beyond-12f)* | JWT auth at the gateway; per-account authorization on every order; least-privilege DB users. |

> If a specific "13 rules" checklist is required instead, map it here — the factors above already
> cover the standard set; the table is easy to relabel.

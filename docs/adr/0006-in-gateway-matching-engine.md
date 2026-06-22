# ADR 0006 — In-gateway matching engine for the free-tier live demo

- **Status:** Accepted
- **Date:** 2026-06-22

## Context
The canonical matching engine is the Java service (ADR 0002): a per-symbol limit order book with
price-time priority, fed over gRPC and streaming trades through Kafka. That is the centerpiece and
the engine we defend in depth.

But the *public* deployment can't run it. Free hosting (GitHub Pages for the frontend, Render's
free tier for one small service) has no room for the JVM engine **plus** Kafka **plus** Postgres,
and managed Kafka/Postgres need a credit card. So the hosted gateway ran in `ENGINE_MODE=mock`: it
acknowledged orders but never matched them. The live link looked alive but couldn't actually trade —
two visitors could not transact, no fills printed, the book never moved.

We wanted the public link to be a *real, shared, multiplayer* exchange anyone can try in one click.

## Decision
Add a **real in-process matching engine to the gateway** (`internal/engine/LocalEngine`), selected
by `ENGINE_MODE=local` (and `mock`, kept as an alias so existing deploys keep working). It is a
genuine limit order book — price-time priority, limit + market orders, partial fills, cancel,
aggregated depth snapshots — guarded by a single mutex (a strict single-writer; see "Consequences").

The order path stays identical; only the `engine.EngineClient` implementation changes. Two wiring
points make it multiplayer:

- **Executions are returned in `OrderAck.Trades`.** With the gRPC engine this is empty (trades arrive
  on Kafka and `internal/tape` fans them out). With `LocalEngine` there is no Kafka, so the handler
  broadcasts the returned trades — plus a fresh book snapshot — to **every** WebSocket client. That
  is what lets the resting (maker) side, which never called this gateway, see the fill live.
- **`POST /auth/demo` mints a unique account per session** (`acct-demo-1-<rand>`). Each browser is a
  distinct trader, so two visitors' orders genuinely cross instead of being one shared identity.

## Consequences
- The live link is now a real shared exchange: open it in two tabs (or send it to a friend) and
  orders match, trades print on the tape, and the book updates for everyone in real time.
- **Two matching implementations now exist.** This is deliberate, not duplication-for-its-own-sake:
  the Java engine remains authoritative and is what the full stack and the architecture story use;
  `LocalEngine` is a lightweight stand-in scoped to the single-process free-tier demo. They share the
  `proto`-derived Go types so the contract can't drift.
- **Concurrency is a global mutex, not per-symbol.** That is stricter than the Java engine's
  single-writer-per-symbol model and trivially correct, but it serializes across symbols. Acceptable
  at demo scale; the low-latency concurrency argument lives in the Java engine, not here.
- State is in-memory and per-instance: a redeploy or a second Render instance resets/splits the book.
  Fine for a demo (play money); a real deployment would keep the engine authoritative and durable.
- No new infrastructure, no credit card — the whole multiplayer demo runs in the one free service.

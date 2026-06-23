# Build Journal — problems hit & how they were solved

A running log of the **non-obvious problems** met while building OpenExchange and exactly how each
was fixed. The goal: someone (including future-you) can read this top-to-bottom and understand not
just *what* the code is, but *why it looks the way it does* and *what traps to avoid* when rebuilding
it. Newest entries at the bottom of each phase.

Format of each entry: **Symptom → Cause → Fix → Lesson.**

---

## Phase 0 — scaffold & contracts

### protoc: "CancelOrder is not a message type"
- **Symptom:** `protoc` failed compiling `proto/openexchange.proto` at the `service` block.
- **Cause:** the RPC method was `rpc CancelOrder(CancelOrder)` — a method named the same as a
  message in the same scope. protobuf rejects a method whose name shadows a message.
- **Fix:** renamed the message to `CancelOrderRequest` (kept the method `CancelOrder`). Updated the
  Java service param + import. The Go/TS app-level `CancelOrder` DTOs are *not* proto-generated, so
  they were left alone.
- **Lesson:** in proto, method names and message names share a namespace — suffix request messages
  with `Request` to stay clear of method names.

### Gradle wrapper wanted to re-download the whole distribution
- **Symptom:** `./gradlew` started downloading a Gradle distribution on a machine where heat/thermals
  matter (macOS 12, Tier 3).
- **Cause:** the wrapper pins a distribution URL; if that exact version isn't cached it fetches it.
- **Fix:** use the already-installed binary directly: `~/tools/gradle-8.10.2/bin/gradle`. Same build,
  no download, no surprise CPU spike.
- **Lesson:** on a thermally constrained machine, prefer a local toolchain binary over the wrapper's
  auto-download; document the path (see memory `openexchange-dev-env`).

---

## Phase 1 — matching engine

### zsh: "no matches found" on a glob flag
- **Symptom:** `grep -r --include=*.go .` → `zsh: no matches found: --include=*.go`.
- **Cause:** zsh expands the unquoted `*.go` glob *before* grep sees it, and fails if nothing matches
  in the cwd.
- **Fix:** quote the pattern (`--include='*.go'`) or just `grep -r <dirs>` without globs.
- **Lesson:** always quote glob-bearing flags in zsh, or the shell eats them.

### Java vs proto enum name clash (Side / OrderType / OrderStatus)
- **Symptom:** the gRPC service couldn't `import` both the domain enums and the proto enums — same
  simple names.
- **Cause:** `com.openexchange.engine.model.Side` and `com.openexchange.proto.Side` collide on a
  plain `import`.
- **Fix:** import the proto messages, and **fully-qualify** the few clashing domain enums in the
  mapping helpers (`com.openexchange.engine.model.Side.BUY`, etc.).
- **Lesson:** keep wire types and domain types in separate packages and translate at one boundary
  (`MatchingEngineService`); never let proto types leak into the engine core.

### gRPC server lifecycle vs. Spring startup order
- **Symptom:** need the gRPC port to open only after the engine bean exists, and to drain in-flight
  RPCs on shutdown (so Kubernetes can roll the pod without dropping orders).
- **Fix:** `GrpcServerRunner implements SmartLifecycle` — Spring starts it after all beans are built
  and stops it gracefully (`awaitTermination`) on `SIGTERM`.
- **Lesson:** for "start last, stop first, drain cleanly" resources, `SmartLifecycle` beats
  `@PostConstruct`/`@PreDestroy`.

---

## Phase 1 — Postgres double-entry ledger

### Postgres was already installed — don't containerize blindly
- **Symptom:** about to `docker compose up postgres` for the ledger.
- **Cause/finding:** Homebrew `postgresql@14` was already running on `:5432`.
- **Fix:** created a dedicated role+db matching the compose defaults (`oex`/`oex`/`openexchange`) on
  the native server. Zero containers needed for engine + gateway + Postgres.
- **Lesson:** check `brew services` / `lsof -i :5432` before starting a container; the defaults were
  chosen to match either path so config doesn't change.

### `trade_id` resets on restart (idempotency collision risk)
- **Symptom:** ledger insert is `ON CONFLICT (trade_id) DO NOTHING`; after an engine restart the
  in-memory sequence (`AAPL-T1`, `AAPL-T2`, …) restarts at 1.
- **Cause:** `trade_id` is an in-memory per-symbol counter, not durable.
- **Fix (interim):** documented in `docs/roadmap.md` → Known limitations. Truncated the dev ledger
  after the demo to avoid a stale collision.
- **Proper fix (planned):** replay the event log (Kafka + ledger) on startup so sequences continue,
  or switch `trade_id` to a UUID / outbox id (see ADR 0003).
- **Lesson:** any idempotency key must be durably unique across restarts — an in-memory counter is a
  latent bug the moment you add `ON CONFLICT`.

---

## Phase 1→3 — trade event streaming (Kafka)

### Verifying the publish without spinning up containers (heat)
- **Symptom:** proving "engine really publishes a trade to Kafka" normally needs a running broker;
  starting Colima + Kafka just for a check is heat + setup cost on this machine.
- **Fix:** an `@EmbeddedKafka` test (`KafkaTradePublisherTest`) starts a real broker *inside the JVM*
  for the duration of the test, publishes a trade, and consumes it back — asserting the protobuf
  round-trips and the key is the symbol. Runs in CI and locally, no Colima.
- **Lesson:** `spring-kafka-test`'s embedded broker is the right tool for fast, container-free
  verification of producer/consumer code.

### Don't let a broker outage fail an order
- **Symptom (design):** if the engine published to Kafka synchronously before acking, a broker
  outage would fail an order whose money had already settled in the ledger.
- **Fix:** publish **after** the ledger commit, **async + best-effort**, log on failure. The ledger
  is the source of truth; the topic is a derived stream. Captured the trade-off and the durable fix
  (transactional outbox) in **ADR 0003**.
- **Lesson:** order of operations matters — settle the money durably first, then emit the event;
  never make a correctness-critical path depend on a best-effort transport.

### Why `acks=all` + idempotent producer
- **Decision:** the producer is configured `acks=all` and `enable.idempotence=true`.
- **Why:** `acks=all` waits for in-sync replicas (no silent loss on broker failover);
  idempotence stops a producer retry from writing the same record twice.
- **Lesson:** these two flags are the cheap baseline for "don't lose / don't duplicate" on the
  producer side — set them by default.

### `KafkaTestUtils.buildKafkaConsumer(...)` didn't exist
- **Symptom:** test compile error — `cannot find symbol: method buildKafkaConsumer(Map)` on
  `KafkaTestUtils`.
- **Cause:** that helper isn't present in the spring-kafka version resolved by Boot 3.2.5; the API
  differs across versions.
- **Fix:** construct the consumer directly — `new KafkaConsumer<>(props)` with explicit
  deserializer classes in the props map. `KafkaTestUtils.consumerProps(...)` and
  `getSingleRecord(...)` are still used.
- **Lesson:** test-util helpers drift between library versions; when a helper is missing, drop to the
  underlying client API rather than chasing the exact version that has it.

### Risk consumer: `ModuleNotFoundError: google.protobuf` when decoding a trade
- **Symptom:** decoding the protobuf `Trade` in the Python risk service failed with
  `ModuleNotFoundError: google.protobuf`.
- **Cause:** only `confluent-kafka` was a dependency; the protobuf **runtime** wheel wasn't
  installed. protobuf splits the **compiler** (`protoc`, used at codegen time) from the **runtime**
  (`protobuf` pip package, needed to load generated `_pb2.py`) — and protoc 28.1 emits code that
  needs runtime ≥ 5.28.
- **Fix:** `pip install 'protobuf>=5.28,<6'` and pinned it in `risk/requirements.txt`. Codegen still
  uses the standalone `protoc` (already installed), **not** the heavy `grpcio-tools`.
- **Lesson:** generated `_pb2.py` needs a runtime whose major version matches the protoc that
  produced it; install the light `protobuf` wheel, keep `protoc` as the compiler.

### Risk consumer: keeping import-safety with a generated stub
- **Symptom (avoided):** importing the generated stub at module top would break the hard requirement
  that `app.kafka_consumer` imports on a bare box (no Kafka, no protobuf installed) — needed for
  tests and `py_compile`.
- **Fix:** import the stub **lazily inside the decode path** (`_decode_trade`), mirroring the existing
  lazy `confluent_kafka` import. Tests use `pytest.importorskip` so a bare box skips the protobuf
  test cleanly. Verified via `sys.modules` that a plain import pulls in neither `google.protobuf` nor
  the stub.
- **Lesson:** keep optional/generated dependencies behind call-time imports so module import stays
  side-effect-free.

> Machine note: the risk suite was run against the **system** Python 3.12 (no venv in the repo), so
> `protobuf` was installed globally. A repo-local venv would isolate this — tracked for the infra
> phase.

### The big one: `trades` wire-format mismatch across three services
- **Symptom:** the engine was changed to publish the **protobuf** `Trade` to the `trades` topic, but
  the already-scaffolded risk consumer did `json.loads(msg.value())` and the gateway broadcast a
  **synthetic** JSON trade built from the submitter's ack. Three services, two different assumptions
  about what's on the topic.
- **Cause:** the topic format was never pinned in one place; each service guessed when scaffolded.
- **Fix:** pinned it in **ADR 0003** — the topic carries the shared protobuf `Trade`, keyed by
  symbol. Then made every consumer agree: gateway `proto.Unmarshal` → WS envelope; risk
  `Trade.FromString` → feature store. The synthetic gateway trade was deleted (it could only see the
  taker side and might disagree with the engine's authoritative price).
- **Lesson:** the wire format of a topic is a **contract** — decide it once (an ADR + the `proto/`
  message) before any producer or consumer is written, or services silently disagree. The clean
  separation the consumers already had (decode step vs. business logic) made retrofitting the decode
  a one-function change on each side.

### Gateway: `go mod tidy` pulled yaml/spew/difflib after adding kafka-go
- **Symptom:** adding `segmentio/kafka-go` added `gopkg.in/yaml.v3`, `go-spew`, `go-difflib` to
  `go.sum`.
- **Cause:** those are kafka-go's *test* dependencies, surfaced as `// indirect` requires.
- **Fix:** none needed — indirect test-only modules don't end up in the built binary; `go build`
  stays clean. Left as-is.
- **Lesson:** a new dependency can grow `go.sum` with its test deps; that's expected and not bloat in
  the shipped binary.

## Phase 2→3 — order event streaming (gateway → Kafka `orders`)

### Who should produce the `orders` topic — engine or gateway?
- **Symptom (design):** the risk service already consumed an `orders` topic, but nothing published
  to it. The obvious candidate is the engine (it sees orders), but that's wrong.
- **Cause/insight:** the anomaly model scores order *attempts*, and the most valuable signal is the
  attempts the engine **rejects** (malformed, rate-limited, rapid cancels = spoofing). Those never
  reach the engine — they die at the gateway. Only the **gateway** sees every attempt, at the
  authenticated edge, with a verified `account_id`.
- **Fix:** the gateway publishes an `OrderEvent` after each submit/cancel ack. Captured in ADR 0004.
- **Lesson:** "who has the data" isn't "who's downstream" — it's "who observes the full event set."
  For attempt/rejection telemetry, that's the edge service, not the core.

### No existing proto message fit an "order event"
- **Symptom:** wanted to reuse `NewOrder` for the topic, but it has no `is_cancel`, no resulting
  `status`, and no `ts_millis` — so it can't represent a cancel or a rejection outcome.
- **Fix:** added a dedicated `OrderEvent` message to `proto/` (contract-first) that is
  the envelope for "what the gateway observed": identity + is_cancel + order_id + status + ts_millis.
  Regenerated Go (`make proto`) and Python (`make proto-python`) stubs from the one source.
- **Lesson:** don't overload a request message as an event message — an event needs outcome + time
  fields a request doesn't have. Model the event explicitly in the contract.

### Partition key differs per topic: account_id vs symbol
- **Decision:** `orders` is keyed by **account_id**; `trades` is keyed by **symbol**.
- **Why:** ordering only has to hold within the unit a consumer aggregates on. Risk features are
  per-account, so per-account ordering is what matters for `orders`; the trade tape is per-symbol, so
  `trades` keys by symbol. Picking the key to match the consumer's aggregation unit maximizes
  parallelism without breaking the ordering the consumer actually needs.
- **Lesson:** the partition key is a consumer-driven choice, not a producer-driven one — key by
  whatever the downstream needs ordered.

### `protoc-gen-go` wasn't installed, but the build stayed light
- **Symptom:** `protoc-gen-go` / `protoc-gen-go-grpc` not on PATH when regenerating Go stubs.
- **Fix:** the `make proto` target already `go install`s them on demand into `$(go env GOPATH)/bin`;
  exporting that dir onto PATH for the one command was enough. It's a small one-time fetch, not a
  heavy compile — safe even on the thermally-constrained machine.
- **Lesson:** keep codegen tool bootstrapping inside the Makefile target so a fresh box self-heals;
  no container or wrapper download needed.

### Best-effort async producer via kafka-go (mirrors the engine's trade publisher)
- **Decision:** the gateway's `orderfeed.KafkaPublisher` uses a kafka-go `Writer` with `Async:true`
  + `RequiredAcks=RequireAll` + a `Completion` callback that only logs failures.
- **Why:** this is an ML feature stream, not the money path — it must never block or fail an order
  that already got an engine ack. Async makes `WriteMessages` enqueue-and-return; the completion
  callback is the only place errors surface, which is exactly the best-effort contract we want.
  Same shape as the engine's publish-after-ledger design (ADR 0003), now on the order side (ADR 0004).
- **Lesson:** for derived/telemetry streams, publish after the authoritative step and make delivery
  best-effort — push the durability guarantee onto the source of truth (ledger), not the stream.

### Verifying the producer without a broker
- **Fix:** the wire mapping lives in pure functions (`buildSubmitEvent` / `buildCancelEvent`, ts
  passed in for determinism); the test marshals + unmarshals the protobuf to assert what actually
  lands on the wire, including a **rejected** order (the case that matters most for anomalies) and a
  cancel leaving side/type/qty unspecified. No broker, no Colima. Risk side verified the same way:
  serialize a real `OrderEvent` and drive `_decode` → `handle_message`.
- **Lesson:** keep the byte-mapping a pure function and round-trip it in a unit test; a broker is only
  needed to test delivery, not encoding.

## Phase 3→2 — closing the loop: risk-signals → gateway reject path

### Where does a risk verdict get enforced — engine, gateway, or a sync call?
- **Symptom (design):** the risk service could compute "account X is over its limit," but nothing
  acted on it. Three options: enforce in the engine, call the risk service synchronously per order,
  or consume an async signal at the gateway.
- **Decision:** async signal consumed at the **gateway** (ADR 0005). The engine is single-writer-
  per-symbol and authoritative for matching — loading per-account risk + a Kafka consumer into it
  crosses the symbol-sharding boundary and couples concerns. A synchronous call adds a network hop
  and a hard Python dependency to the hot path. The gateway gate is an O(1) map lookup with no I/O.
- **Lesson:** enforce cross-cutting policy (risk, auth, rate limits) at the edge, not in the core
  domain engine — keep the engine doing one thing (matching) correctly and fast.

### Fail-open vs fail-closed for a risk gate
- **Decision:** the gate **fails open** — if the broker or risk service is down, no account is
  blocked.
- **Why:** blocking *all* trading because the risk sidecar is offline is worse than the risk it
  mitigates. The ledger + the engine's own validation remain the authoritative correctness gates;
  risk gating is a safety *overlay*. (Contrast an auth check, which must fail closed.)
- **Lesson:** pick fail-open vs fail-closed by asking "what's the cost of the check being wrong in
  each direction?" — for a derived, advisory overlay fed by an async stream, open is right.

### Eventually-consistent, post-trade gating (and why that's fine)
- **Symptom:** the signal that blocks an account is derived from an order/trade that *already*
  happened, so the first breaching action slips through; only subsequent ones are blocked.
- **Why it's acceptable:** exposure limits are computed from *filled positions*, which only exist
  after a trade — so a position-limit gate is inherently post-trade. Keeping it async avoids a
  per-order round-trip to Python. Documented as the explicit trade-off in ADR 0005; a hard
  synchronous pre-trade check is noted as future hardening.
- **Lesson:** name the consistency model out loud. "Eventually consistent, self-clearing, post-trade"
  is a precise, defensible description you can probe — far better than pretending it's
  a hard pre-trade limit.

### Refactoring `publish_signal` from JSON dict to typed protobuf
- **Symptom:** `publish_signal(dict)` emitted JSON and was called from one place (the `/score-order`
  anomaly path). Closing the loop needed a second caller (the consumer's exposure scoring) and a
  pinned wire format the Go gateway could decode.
- **Fix:** changed it to keyword args (`account_id`, `action`, `kind`, `score`, `reason`, …) and
  built a protobuf `RiskSignal`, mapping the enum *names* to values via `getattr(pb, name)` on the
  lazily-imported stub (preserving import-safety). Updated both call sites.
- **Lesson:** when a second consumer appears, that's the moment to pin the contract and drop the
  placeholder JSON — exactly the lesson from the `trades` mismatch, applied proactively this time.

### Testing the loop without a broker (both sides)
- **Risk:** injected a fake (connected) producer + a tiny-limit scorer, drove `handle_message`, and
  decoded the captured bytes back into a `RiskSignal` to assert `REJECT` + reason + per-account key.
- **Gateway:** `decodeSignal` is a pure function (round-tripped in a unit test); the `Gate` is tested
  for block→clear→fail-open transitions; the reject path is tested at the HTTP layer with a fake gate
  + a `MockClient`, asserting `422`, the risk reason in the body, and that the rejected attempt is
  still published to the order feed. No broker, no Colima.
- **Lesson:** keep the byte-mapping pure and the policy (gate) a plain in-memory type; a broker is
  only needed for the live wiring test, not for logic correctness.

---

## Phase 3 → End-to-end live demo (2026-06-17)

### Problem: Colima unavailable on macOS 12 (qemu not installed)

**Symptom:** `colima start` exited with `cannot use vmType: 'qemu': qemu-img not found`.
Colima 0.x on macOS 12 uses QEMU as the VM backend, which requires `brew install qemu`. Docker
compose plugin was also absent from Colima's bundled Docker binary (the `compose` plugin shipped
separately in newer Docker CLI versions).

**Fix:** Downloaded Kafka 3.7.1 tarball to `~/tools/`, formatted a single-node KRaft cluster
(`kafka-storage.sh format`), and ran `kafka-server-start.sh` directly on the local JVM (Java 17
already installed for the engine). No container or VM needed for a local demo broker.

**Lesson:** For macOS dev boxes where container tooling is inconsistent, native KRaft Kafka needs
only Java 17 — zero extra installs. The broker starts in ~4 seconds and handles the full demo
throughput without issue.

### Problem: Risk consumer logs silently dropped inside uvicorn

**Symptom:** Ran the risk service via `uvicorn app.main:app`; the background Kafka consumer thread
started (no error) but the `INFO:risk.consumer:Kafka connected` log never appeared. The root
`logging` level defaults to WARNING when no `basicConfig` call is made; uvicorn's own logging only
covers HTTP access, not application loggers.

**Fix:** For the live demo, ran the consumer standalone (`python3 -m app.kafka_consumer`) where
`main()` calls `logging.basicConfig(level=logging.INFO)`. In production the right fix is to set
`log_config` in the uvicorn call (or add a `logging.config` call in `main.py`) so the `risk.*`
loggers are routed through uvicorn's handler.

### End-to-end demo result (cap=5 orders/min, sent 9 orders)

Setup:
- KRaft Kafka on `localhost:9092`, topics: `orders`, `trades`, `risk-signals` (4 partitions each)
- Gateway in `ENGINE_MODE=mock`, `JWT_SECRET=demo-secret`, port 8081
- Risk consumer standalone, `RISK_MAX_ORDERS_PER_MIN=5`, group `risk-consumer-live`
- JWT minted with `python3 /tmp/oex_jwt.py acct-velocity-test demo-secret`

Observed order responses:

```
Order 1 → ACCEPTED   ord-9
Order 2 → ACCEPTED   ord-10
Order 3 → ACCEPTED   ord-11
Order 4 → ACCEPTED   ord-12
Order 5 → ACCEPTED   ord-13
Order 6 → ACCEPTED   ord-14   ← first breaching order slips through (eventually-consistent gate)
Order 7 → REJECTED   "risk: 6 orders/min exceeds cap 5 (velocity)"
Order 8 → REJECTED   "risk: 7 orders/min exceeds cap 5 (velocity)"
Order 9 → REJECTED   "risk: 8 orders/min exceeds cap 5 (velocity)"
```

Messages observed on `risk-signals` topic (decoded from protobuf):

```
account=acct-velocity-test action=ALLOW score=0.00 reason='within limits'
... (score climbs 0.20 → 0.40 → 0.60 → 0.80 → 1.00 as orders arrive) ...
account=acct-velocity-test action=REJECT score=1.00 reason='6 orders/min exceeds cap 5 (velocity)'
account=acct-velocity-test action=REJECT score=1.00 reason='7 orders/min exceeds cap 5 (velocity)'
account=acct-velocity-test action=ALLOW  score=0.00 reason='within limits'   ← self-clears after 60s
```

Gateway log (confirming in-memory Gate lifecycle):

```
risksignal: BLOCK acct-velocity-test (score=1.00) 6 orders/min exceeds cap 5 (velocity)
risksignal: BLOCK acct-velocity-test (score=1.00) 7 orders/min exceeds cap 5 (velocity)
risksignal: BLOCK acct-velocity-test (score=1.00) 8 orders/min exceeds cap 5 (velocity)
risksignal: CLEAR acct-velocity-test (within limits)   ← 60s later, gate cleared automatically
```

**This confirms the complete async risk-signal loop:**
`order → gateway → OrderEvent(orders) → risk consumer → score → RiskSignal(risk-signals) → gateway Gate → 422 REJECTED`

The "first breach slips through" behaviour is correct and matches ADR 0005's eventual-consistency
trade-off: the risk loop is async and the gate only applies from the next signal onward. The
self-clearing after 60 seconds (velocity window expiry) also behaves as designed.

<!-- Append new entries below as Phase 4 (React dashboard) / Phase 5 (infra+observability) land. -->

---

## 2026-06-21 — Wiring the dashboard to the gateway (4 real fixes)

Ran the gateway in `ENGINE_MODE=mock` + the Vite dev server and clicked through the UI. Four
genuine bugs surfaced — all needed in production too, not just locally:

- **CORS middleware** (`internal/middleware/cors.go`): the browser's preflight `OPTIONS`
  request was rejected, so `POST /orders` from `localhost:5173` → `localhost:8080` failed with
  "network error". Added `Access-Control-Allow-Origin` + `OPTIONS → 204`, wrapping all routes
  in `main.go`. (Origin is `*` for now; pin to the real frontend domain in production.)
- **WS token via query param** (`internal/middleware/auth.go`): the browser WebSocket API
  can't set custom headers, so the dashboard had no way to send `Authorization: Bearer`.
  `bearerToken()` now falls back to a `?token=<jwt>` query param for the WS upgrade path.
- **Book URL** (`web/src/api/client.ts`): the frontend called `/book?symbol=ACME` but the
  gateway route is `/book/{symbol}`. Fixed to `/book/ACME?depth=20` (was a hard 404).
- **Frontend auth** (`web/src/hooks/useWebSocket.ts`, `api/client.ts`, `config.ts`): the REST
  client had a `TODO: attach Authorization` and was sending nothing. Now a `DEMO_TOKEN` (from
  git-ignored `web/.env.local`) is appended to the WS URL and sent as `Authorization: Bearer`
  on REST calls. NOTE: `DEMO_TOKEN` is a dev stand-in — production needs a real login flow that
  mints short-lived tokens. The plumbing is correct; only the token *source* is a stub.

**On the empty Trades / Risk panels in mock mode:** these are fed in production by the *real*
Kafka consumers — `tape.TradeConsumer` (engine → `trades` topic → WS) and `risksignal.Consumer`
(Python → `risk-signals` topic → WS) — both already wired in `main.go` and emitting the exact
WS envelopes the dashboard expects (`{type:"trade",data}` / `{type:"risk",data}`). They only
light up with the full stack (`make up`). We briefly added an in-memory matching engine + a
risk simulator to the Go mock so these panels would fill without Docker, then **reverted both**:
duplicating the Java engine and the Python risk service inside the gateway's mock muddied the
project's core story (the Java engine *is* the matching engine) and added untested money-path
code. The honest demo is `make up` — real engine, real Kafka, real fills.

**How to run locally:**

```bash
# Order book, order entry, live WS, risk gate — no Docker:
cd ~/openexchange/gateway && JWT_SECRET=localsecret ENGINE_MODE=mock go run ./cmd/gateway
cd ~/openexchange/web && npm run dev    # http://localhost:5173

# Trades + Risk panels (full real-time path): the Docker stack
make up && make seed
```

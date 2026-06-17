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
- **Fix:** added a dedicated `OrderEvent` message to `proto/` (contract-first, per the docs) that is
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

<!-- Append new entries below as the infra phase / live end-to-end demo lands. -->

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

<!-- Append new entries below as the gateway consumer, risk service, and infra phases land. -->

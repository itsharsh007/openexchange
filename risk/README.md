# Risk / ML Service (`risk/`)

The Python 3.12 + FastAPI service for OpenExchange. It consumes the Kafka `trades`/`orders` stream,
builds features in an in-memory feature store, and runs **three models**:

1. **Price prediction** — short-horizon next-tick direction (gradient-boosted trees).
2. **Anomaly / fraud detection** — Isolation Forest over order-pattern features.
3. **Risk exposure** — per-account, rule-based scoring against configurable limits.

It exposes FastAPI endpoints and publishes risk signals to the `risk-signals` Kafka topic; the Go
gateway can reject orders that breach limits.

> Simulation only — play money, fake symbols. Prices are integer **ticks** everywhere (no floats),
> matching `proto/openexchange.proto`.

---

## Design principles

- **Import-safe / compile-clean without infra.** Importing any module never opens a socket. Kafka,
  the DB, and even `confluent-kafka`/`scikit-learn` are imported lazily and guarded, so
  `py_compile`, `pytest`, and the FastAPI boot all work on a bare box. Connections are created on
  the FastAPI startup event and self-disable if the broker is down.
- **Config from env only** (Twelve-Factor) — see [Environment variables](#environment-variables).
- **One feature definition.** `app/features.py` is the single source of truth for every feature, so
  training and serving use the *exact same* transformation (no train/serve skew).

---

## Layout

```
risk/
├── app/
│   ├── config.py            # env-var config + risk limits (no network on import)
│   ├── features.py          # shared feature engineering (price + order-pattern)
│   ├── schemas.py           # Pydantic request/response models mirroring the proto
│   ├── store.py             # in-memory feature store (drop-in for a future Postgres one)
│   ├── kafka_consumer.py    # guarded consumer loop; also `python -m app.kafka_consumer`
│   ├── main.py              # FastAPI app + endpoints
│   └── models/
│       ├── price_prediction.py  # GradientBoostingClassifier
│       ├── anomaly.py           # IsolationForest
│       └── risk.py              # rule-based exposure scoring
├── tests/                   # pytest; anomaly model tests auto-skip if sklearn absent
├── requirements.txt
├── Dockerfile
├── pyproject.toml           # pytest config + metadata
└── README.md
```

---

## The models

### 1. Price prediction (`app/models/price_prediction.py`)
- **Task:** binary classification of the **next tick's direction** (UP/DOWN) + `prob_up`.
  Direction is more robust than predicting the exact price and is what the dashboard/risk logic
  needs.
- **Algorithm:** `GradientBoostingClassifier` (shallow trees, 150 estimators, lr 0.05). GBTs capture
  non-linear feature interactions on small tabular data with no scaling and fast serving.
- **Features** (`PRICE_FEATURE_NAMES`): `ret_1`, `ret_5_mean`, `ret_window_mean`, `volatility`,
  `imbalance` (order-flow), `spread_proxy`. Computed from a trailing window of trade prices.
- **Training:** `train()` builds an `(X, y)` dataset where features use the past window and the label
  is the **next** tick's direction (one-step-ahead → no look-ahead leakage). With no data supplied it
  trains on a **seeded synthetic** AR(1) price series so the pipeline is provable end-to-end. In
  production, `train()` would read the Postgres feature store and use a **time-based** holdout.

### 2. Anomaly / fraud (`app/models/anomaly.py`)
- **Task:** score one order's pattern in `[0,1]` (higher = more anomalous) and return ALLOW/REJECT.
- **Algorithm:** `IsolationForest` — unsupervised, so it needs only "normal" data (fraud labels are
  scarce and adversaries adapt). Near-linear, no scaling, single intuitive knob (`contamination`).
- **Features** (`ANOMALY_FEATURE_NAMES`): `size_ratio` (vs account's own average → fat-finger),
  `inter_arrival_z` (bursting/quote-stuffing), `price_deviation` (off-market quoting/layering),
  `cancel_ratio` (spoofing). Scored **relative to each account's own recent behaviour**.
- **Decision:** sklearn's raw score is normalised to `[0,1]` (calibrated against the training
  distribution) and compared to `RISK_ANOMALY_THRESHOLD`. The threshold is **policy** and tunable
  without retraining.
- **Reasons:** rejected orders carry human-readable heuristic reasons (a pragmatic "why?" aid; SHAP
  would be the rigorous route).

### 3. Risk exposure (`app/models/risk.py`)
- **Task:** per-account exposure score in `[0,1]` (0 = no risk, 1 = at/over a limit) + explicit
  breaches + net positions.
- **Rule-based, not ML:** limits are compliance policy — they must be deterministic and auditable.
- **Limits** (env-configurable): `max_position_per_symbol`, `max_gross_notional`,
  `max_orders_per_min`. Score = **max** of per-limit utilisations (any breach → 1.0). Gross notional
  sums `|net position| * last price` across symbols (longs and shorts do **not** net).

---

## API

Base URL: `http://localhost:8000`. Interactive docs at `/docs` (FastAPI/OpenAPI).

### `GET /healthz`
```json
{ "status": "ok", "version": "0.1.0", "price_model_trained": false,
  "anomaly_model_fitted": false, "kafka_bootstrap": "localhost:9092" }
```

### `POST /predict`
Request:
```json
{ "symbol": "ACME", "recent_prices_ticks": [10000, 10010, 9990, 10005, 10020],
  "buy_volume": 120, "sell_volume": 80 }
```
Response:
```json
{ "symbol": "ACME", "direction": "UP", "prob_up": 0.61, "model_version": "price-gbt-v1" }
```

### `POST /score-order`
Request (mirrors proto `NewOrder` + a timestamp):
```json
{ "client_order_id": "c-1", "account_id": "acct-7", "symbol": "ACME",
  "side": "BUY", "type": "LIMIT", "price_ticks": 10000, "quantity": 50,
  "ts_millis": 1718500000000, "recent_mid_ticks": 10005 }
```
Response:
```json
{ "client_order_id": "c-1", "account_id": "acct-7", "anomaly_score": 0.04,
  "decision": "ALLOW", "reasons": [] }
```
A rejected order returns `"decision": "REJECT"` with `reasons` like
`["order size 50.0x account average", "high cancel ratio (possible spoofing)"]`.

### `GET /risk/{account_id}`
```json
{ "account_id": "acct-7", "exposure_score": 0.5, "gross_notional_ticks": 500000,
  "breaches": [], "positions": { "ACME": 50 } }
```

---

## Kafka consumer

`app/kafka_consumer.py` reads `trades` + `orders`, updates the feature store, and can publish to
`risk-signals`. It is fully guarded: with no broker (or no `confluent-kafka` installed) it logs a
warning and disables itself — the API keeps serving.

- **As a background thread** (default): started automatically on FastAPI startup.
- **As a standalone process:** `python -m app.kafka_consumer`.

Wire formats per topic:
- `trades`: the engine publishes the **protobuf `Trade`** message (the shared `proto/` contract),
  decoded via the generated stub in `app/genproto/` (`Trade.FromString`). Fields used:
  `symbol, price_ticks, quantity, buy_account_id, sell_account_id`.
- `orders`: **JSON** `{account_id, symbol, quantity, ts_millis, is_cancel?}` — no producer exists
  yet, so this stays tolerant JSON until one does.

The stub import is **lazy** (inside the decode path), so `import app.kafka_consumer` still works on a
bare box with no `protobuf` runtime — consistent with the service's import-safety rule. Regenerate
the stub after any `proto/` change:

```bash
make proto-python        # -> risk/app/genproto/openexchange_pb2.py  (needs protoc + `pip install protobuf`)
```

---

## Running

### Locally (dev)
```bash
cd risk
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt

# API (consumer starts as a background thread; self-disables if Kafka is down)
uvicorn app.main:app --reload --port 8000

# OR run the consumer as its own process
python -m app.kafka_consumer
```

### Docker
```bash
cd risk
docker build -t openexchange-risk .
docker run --rm -p 8000:8000 \
  -e KAFKA_BOOTSTRAP=host.docker.internal:9092 \
  -e DATABASE_URL=postgresql://oex:oex@host.docker.internal:5432/openexchange \
  openexchange-risk
```

---

## Environment variables

| Var | Default | Meaning |
|---|---|---|
| `KAFKA_BOOTSTRAP` | `localhost:9092` | Kafka bootstrap servers (shared contract). |
| `DATABASE_URL` | `postgresql://oex:oex@localhost:5432/openexchange` | Postgres DSN (shared contract; reserved for the future DB-backed store). |
| `RISK_TOPIC_TRADES` | `trades` | Trades topic. |
| `RISK_TOPIC_ORDERS` | `orders` | Orders topic. |
| `RISK_TOPIC_SIGNALS` | `risk-signals` | Output signals topic. |
| `RISK_CONSUMER_GROUP` | `risk-service` | Consumer group id. |
| `RISK_ANOMALY_THRESHOLD` | `0.7` | Anomaly score ≥ this ⇒ REJECT (policy knob). |
| `RISK_ANOMALY_CONTAMINATION` | `0.02` | IsolationForest contamination prior. |
| `RISK_MAX_POSITION` | `10000` | Max abs net position per symbol. |
| `RISK_MAX_GROSS_NOTIONAL` | `50000000` | Max gross notional (ticks) per account. |
| `RISK_MAX_ORDERS_PER_MIN` | `600` | Order velocity cap per account. |
| `RISK_SEED` | `42` | Seed for synthetic data + model reproducibility. |

---

## Testing & verification

```bash
cd risk
python3 -m py_compile $(find . -name '*.py')   # compile-clean check
pytest                                          # full suite
```

- **Feature, risk, and consumer tests** use only stdlib + numpy → run on a bare box, no
  Kafka/DB/network.
- **Anomaly model tests** need scikit-learn; they auto-**skip** (`pytest.mark.skipif`) if it isn't
  installed.

Kafka client choice: **`confluent-kafka`** (librdkafka-backed; fastest, production-grade). Swapping
to `kafka-python` touches only `app/kafka_consumer.py`.

### Verified in this environment
- `py_compile` on all `*.py`: **OK**.
- `pytest`: **21 passed** (numpy 2.x and scikit-learn 1.7 were present, so the IsolationForest tests
  ran rather than skipped).

---

## How it integrates with the gateway

The gateway's reject path can (a) call `POST /score-order` synchronously and block on
`decision == "REJECT"`, and/or (b) consume `risk-signals` and call `GET /risk/{account_id}` to gate
accounts that are over limit. The per-order anomaly check and the per-account exposure check are
complementary. See `docs/architecture.md`.

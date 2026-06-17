"""Prometheus instruments for the risk service.

All counters/histograms are module-level singletons registered on the default
registry, which the /metrics endpoint serves via prometheus_client.
"""
from prometheus_client import Counter, Histogram, Gauge

# ── Kafka consumer ──────────────────────────────────────────────────────────

messages_consumed_total = Counter(
    "oex_risk_messages_consumed_total",
    "Messages consumed from Kafka, labelled by topic.",
    ["topic"],
)

# ── Scoring ─────────────────────────────────────────────────────────────────

score_histogram = Histogram(
    "oex_risk_exposure_score",
    "Distribution of per-account exposure scores produced by the risk scorer.",
    buckets=[0.0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0],
)

# ── Signal publishing ────────────────────────────────────────────────────────

signals_published_total = Counter(
    "oex_risk_signals_published_total",
    "RiskSignal messages published to the risk-signals Kafka topic, by action.",
    ["action"],  # ALLOW or REJECT
)

# ── HTTP API ─────────────────────────────────────────────────────────────────

http_requests_total = Counter(
    "oex_risk_http_requests_total",
    "HTTP requests to the risk FastAPI service, by endpoint and status.",
    ["endpoint", "status"],
)

score_order_latency_seconds = Histogram(
    "oex_risk_score_order_latency_seconds",
    "Latency of the /score-order endpoint.",
    buckets=[0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0],
)

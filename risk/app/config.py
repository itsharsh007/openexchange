"""Centralised configuration, sourced ONLY from environment variables (Twelve-Factor).

WHY a single config module:
- One place to read/validate env vars, so the rest of the code never touches ``os.environ``
  directly and tests can monkeypatch one object.
- Every value has a sane default so the service boots in a dev/test box with no .env file.

Required by the monorepo contract (.env.example): KAFKA_BOOTSTRAP, DATABASE_URL.
Everything else we add here is namespaced ``RISK_*`` and defaulted.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field


def _get(name: str, default: str) -> str:
    # Thin wrapper so an empty env var ("") falls back to the default rather than the empty string,
    # which is almost always a misconfiguration (e.g. KAFKA_BOOTSTRAP="" should mean "use default").
    val = os.environ.get(name)
    return val if val else default


def _get_float(name: str, default: float) -> float:
    try:
        return float(_get(name, str(default)))
    except ValueError:
        # Bad config should not crash startup; log-and-default is friendlier for a dev tool.
        return default


def _get_int(name: str, default: int) -> int:
    try:
        return int(_get(name, str(default)))
    except ValueError:
        return default


@dataclass(frozen=True)
class RiskLimits:
    """Configurable per-account risk limits used by ``app.models.risk``.

    All monetary quantities are in integer **ticks** (matching the proto: prices are integer ticks),
    so there is never floating-point money. ``notional`` = price_ticks * quantity.
    """

    # Max absolute net position (sum of signed quantities) allowed per symbol before we flag.
    max_position_per_symbol: int = field(default_factory=lambda: _get_int("RISK_MAX_POSITION", 10_000))
    # Max gross notional exposure across all symbols for one account (ticks).
    max_gross_notional: int = field(default_factory=lambda: _get_int("RISK_MAX_GROSS_NOTIONAL", 50_000_000))
    # Max number of orders an account may submit per rolling minute (velocity / spoofing guard).
    max_orders_per_min: int = field(default_factory=lambda: _get_int("RISK_MAX_ORDERS_PER_MIN", 600))


@dataclass(frozen=True)
class Settings:
    # --- Backbone (from the shared .env.example) ---
    kafka_bootstrap: str = field(default_factory=lambda: _get("KAFKA_BOOTSTRAP", "localhost:9092"))
    database_url: str = field(default_factory=lambda: _get("DATABASE_URL", "postgresql://oex:oex@localhost:5432/openexchange"))

    # --- Kafka topics (architecture.md: orders, trades, market-data, risk-signals) ---
    topic_trades: str = field(default_factory=lambda: _get("RISK_TOPIC_TRADES", "trades"))
    topic_orders: str = field(default_factory=lambda: _get("RISK_TOPIC_ORDERS", "orders"))
    topic_signals: str = field(default_factory=lambda: _get("RISK_TOPIC_SIGNALS", "risk-signals"))
    consumer_group: str = field(default_factory=lambda: _get("RISK_CONSUMER_GROUP", "risk-service"))

    # --- Anomaly model ---
    # Decision threshold: anomaly score in [0,1] >= this => REJECT. Tunable without retraining.
    anomaly_reject_threshold: float = field(default_factory=lambda: _get_float("RISK_ANOMALY_THRESHOLD", 0.7))
    # IsolationForest contamination prior (expected fraction of anomalies in training data).
    anomaly_contamination: float = field(default_factory=lambda: _get_float("RISK_ANOMALY_CONTAMINATION", 0.02))

    # --- Reproducibility ---
    seed: int = field(default_factory=lambda: _get_int("RISK_SEED", 42))

    limits: RiskLimits = field(default_factory=RiskLimits)


# Module-level singleton. Importing this does NOT touch the network — it only reads env vars.
settings = Settings()

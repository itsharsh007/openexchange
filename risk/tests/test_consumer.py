"""Tests that the consumer's message routing updates the feature store correctly.

No broker is used: we call ``handle_message`` directly with decoded dicts. This verifies the
import-safety + offline-testability requirement (the consumer logic is a pure function of inputs).
"""

from __future__ import annotations

from app.kafka_consumer import RiskConsumer
from app.config import settings
from app.store import FeatureStore
import app.store as store_module


def test_consumer_imports_without_kafka():
    # Constructing the consumer must not connect or require confluent-kafka.
    c = RiskConsumer()
    assert c is not None
    # run() on a box with no Kafka should return quickly without raising.
    c.run()  # _connect() fails gracefully -> returns


def test_handle_trade_updates_store(monkeypatch):
    # Use a fresh store so the test is isolated from module-global state.
    fresh = FeatureStore()
    monkeypatch.setattr(store_module, "store", fresh)
    # The consumer module imported `store` by name; patch there too.
    import app.kafka_consumer as kc
    monkeypatch.setattr(kc, "store", fresh)

    c = RiskConsumer()
    c.handle_message(
        settings.topic_trades,
        {
            "symbol": "ABC",
            "price_ticks": 10_000,
            "quantity": 5,
            "buy_account_id": "acct-buy",
            "sell_account_id": "acct-sell",
        },
    )
    assert fresh.recent_prices("ABC") == [10_000]
    assert fresh.account_state("acct-buy").positions["ABC"] == 5
    assert fresh.account_state("acct-sell").positions["ABC"] == -5


def test_handle_order_updates_stats(monkeypatch):
    fresh = FeatureStore()
    import app.kafka_consumer as kc
    monkeypatch.setattr(kc, "store", fresh)

    c = RiskConsumer()
    c.handle_message(
        settings.topic_orders,
        {"account_id": "a1", "symbol": "ABC", "quantity": 12, "ts_millis": 5000, "is_cancel": False},
    )
    stats = fresh.order_stats("a1")
    assert stats.submits == 1
    assert list(stats.sizes) == [12.0]

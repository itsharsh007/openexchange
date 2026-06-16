"""Tests that the consumer's message routing updates the feature store correctly.

No broker is used: we call ``handle_message`` directly with decoded dicts. This verifies the
import-safety + offline-testability requirement (the consumer logic is a pure function of inputs).
"""

from __future__ import annotations

import pytest

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


def test_decode_protobuf_trade_updates_store(monkeypatch):
    """The `trades` topic carries the engine's protobuf Trade — decode bytes -> dict -> store.

    Skips cleanly on a bare box where the protobuf runtime or generated stub isn't present, so the
    suite still passes without `make proto-python` / `pip install protobuf`."""
    pytest.importorskip("google.protobuf", reason="protobuf runtime not installed")
    pb = pytest.importorskip(
        "app.genproto.openexchange_pb2", reason="run `make proto-python` to generate stubs"
    )

    fresh = FeatureStore()
    import app.kafka_consumer as kc
    monkeypatch.setattr(kc, "store", fresh)

    # Serialize exactly what the engine publishes: a protobuf Trade keyed by symbol.
    raw = pb.Trade(
        trade_id="ABC-T1",
        symbol="ABC",
        price_ticks=10_000,
        quantity=5,
        buy_order_id="o1",
        sell_order_id="o2",
        buy_account_id="acct-buy",
        sell_account_id="acct-sell",
        ts_millis=1,
    ).SerializeToString()

    c = RiskConsumer()
    # Drives the real decode path used by run() for the trades topic.
    value = c._decode(settings.topic_trades, raw)
    c.handle_message(settings.topic_trades, value)

    assert fresh.recent_prices("ABC") == [10_000]
    assert fresh.account_state("acct-buy").positions["ABC"] == 5
    assert fresh.account_state("acct-sell").positions["ABC"] == -5

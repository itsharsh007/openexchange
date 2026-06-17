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


def test_decode_protobuf_order_updates_stats(monkeypatch):
    """The `orders` topic carries the gateway's protobuf OrderEvent — decode bytes -> dict -> store.

    Skips cleanly on a bare box without the protobuf runtime / generated stub."""
    pytest.importorskip("google.protobuf", reason="protobuf runtime not installed")
    pb = pytest.importorskip(
        "app.genproto.openexchange_pb2", reason="run `make proto-python` to generate stubs"
    )

    fresh = FeatureStore()
    import app.kafka_consumer as kc
    monkeypatch.setattr(kc, "store", fresh)

    # Serialize exactly what the gateway publishes for a submitted (rejected or not) order.
    raw = pb.OrderEvent(
        client_order_id="c-1",
        account_id="a1",
        symbol="ABC",
        side=pb.BUY,
        type=pb.LIMIT,
        price_ticks=10_000,
        quantity=12,
        is_cancel=False,
        order_id="ord-1",
        status=pb.ACCEPTED,
        ts_millis=5000,
    ).SerializeToString()

    c = RiskConsumer()
    value = c._decode(settings.topic_orders, raw)
    c.handle_message(settings.topic_orders, value)

    stats = fresh.order_stats("a1")
    assert stats.submits == 1
    assert list(stats.sizes) == [12.0]


class _FakeProducer:
    """Captures produced records so we can assert on the wire bytes without a broker."""

    def __init__(self) -> None:
        self.produced: list[tuple[str, bytes, bytes]] = []

    def produce(self, topic, key=None, value=None):
        self.produced.append((topic, key, value))

    def poll(self, *_):
        pass


def test_trade_breach_publishes_reject_signal(monkeypatch):
    """A fill that pushes an account over its position cap must publish a protobuf RiskSignal
    with action=REJECT, keyed by account_id — the signal the gateway gates on."""
    pb = pytest.importorskip("app.genproto.openexchange_pb2", reason="run `make proto-python`")

    fresh = FeatureStore()
    import app.kafka_consumer as kc
    monkeypatch.setattr(kc, "store", fresh)

    c = RiskConsumer()
    # Tiny limit so a single small fill breaches it; wire in a fake (connected) producer.
    from dataclasses import replace
    from app.models.risk import RiskScorer
    c._scorer = RiskScorer(limits=replace(settings.limits, max_position_per_symbol=1))
    prod = _FakeProducer()
    c._producer = prod
    c._connected = True

    c.handle_message(
        settings.topic_trades,
        {"symbol": "ABC", "price_ticks": 10_000, "quantity": 5,
         "buy_account_id": "acct-buy", "sell_account_id": "acct-sell"},
    )

    # Both sides moved; both breach the cap of 1 share -> two REJECT signals.
    assert len(prod.produced) == 2
    topics = {t for t, _k, _v in prod.produced}
    assert topics == {settings.topic_signals}
    by_key = {k.decode(): pb.RiskSignal.FromString(v) for _t, k, v in prod.produced}
    assert set(by_key) == {"acct-buy", "acct-sell"}
    for acct, sig in by_key.items():
        assert sig.account_id == acct
        assert sig.action == pb.REJECT
        assert sig.kind == pb.RISK_EXPOSURE
        assert "exceeds cap" in sig.reason


def test_order_within_limits_publishes_allow_signal(monkeypatch):
    """An order that breaches nothing publishes an ALLOW signal (which clears any prior block)."""
    pb = pytest.importorskip("app.genproto.openexchange_pb2", reason="run `make proto-python`")

    fresh = FeatureStore()
    import app.kafka_consumer as kc
    monkeypatch.setattr(kc, "store", fresh)

    c = RiskConsumer()
    prod = _FakeProducer()
    c._producer = prod
    c._connected = True

    c.handle_message(
        settings.topic_orders,
        {"account_id": "a1", "symbol": "ABC", "quantity": 1, "ts_millis": 1000, "is_cancel": False},
    )

    assert len(prod.produced) == 1
    _t, k, v = prod.produced[0]
    sig = pb.RiskSignal.FromString(v)
    assert k.decode() == "a1"
    assert sig.action == pb.ALLOW

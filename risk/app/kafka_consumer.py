"""Kafka consumer loop: reads `trades`/`orders`, updates the feature store, publishes `risk-signals`.

IMPORT-SAFETY (critical requirement)
- This module must import cleanly with NO Kafka running and even with `confluent-kafka` NOT
  installed. We therefore:
    * import confluent_kafka lazily, inside the connect methods (never at module top level);
    * guard every connection in try/except and degrade gracefully (log + no-op) on failure.
- As a result, the FastAPI app can start, tests can run, and `py_compile` passes on a bare box.

RUNNABILITY
- ``RiskConsumer.run()`` is a blocking loop intended for a background thread (started by the API on
  startup) or as a standalone entrypoint: ``python -m app.kafka_consumer``.

WHY a thin wrapper around the client
- Isolating confluent-kafka here means switching to kafka-python (the alternative allowed by the
  spec) touches only this file.

DECODING
- Both the ``trades`` and ``orders`` topics carry **protobuf** messages from the shared proto/
  contract: ``Trade`` (published by the engine) and ``OrderEvent`` (published by the Go gateway for
  every order attempt — see docs/adr/0004). Both are decoded via the generated stub in
  ``app/genproto``, imported lazily so this module still imports on a bare box. Any other topic
  falls back to tolerant JSON.
"""

from __future__ import annotations

import json
import logging
import signal
import threading
import time
from typing import Any

from app.config import settings
from app.models.risk import RiskScorer
from app.store import store

log = logging.getLogger("risk.consumer")


class RiskConsumer:
    def __init__(self) -> None:
        self._consumer = None
        self._producer = None
        self._stop = threading.Event()
        self._connected = False
        # Deterministic, rule-based exposure scorer (no ML, no heavy deps) — import-safe.
        # Used to derive a RiskSignal after each trade/order so the gateway can gate orders.
        self._scorer = RiskScorer()

    # ── Connection (lazy + guarded) ────────────────────────────────────────────
    def _connect(self) -> bool:
        """Create the consumer + producer. Returns False (without raising) if Kafka/client absent."""
        try:
            from confluent_kafka import Consumer, Producer  # lazy import: optional dependency
        except Exception as exc:  # ImportError or any load error
            log.warning("confluent-kafka not available (%s); consumer disabled.", exc)
            return False

        try:
            self._consumer = Consumer(
                {
                    "bootstrap.servers": settings.kafka_bootstrap,
                    "group.id": settings.consumer_group,
                    "auto.offset.reset": "latest",  # only score live flow, not history, on cold start
                    "enable.auto.commit": True,
                }
            )
            self._producer = Producer({"bootstrap.servers": settings.kafka_bootstrap})
            self._consumer.subscribe([settings.topic_trades, settings.topic_orders])
            self._connected = True
            log.info("Kafka connected at %s; subscribed to %s, %s",
                     settings.kafka_bootstrap, settings.topic_trades, settings.topic_orders)
            return True
        except Exception as exc:  # broker unreachable, etc.
            log.warning("Kafka connection failed (%s); consumer disabled.", exc)
            self._consumer = self._producer = None
            self._connected = False
            return False

    # ── Message handling ───────────────────────────────────────────────────────
    def handle_message(self, topic: str, value: dict[str, Any]) -> None:
        """Route one decoded message into the feature store. Pure function of its inputs => testable
        directly without any broker (see tests)."""
        # Accounts whose risk state changed, so we re-score and signal only those.
        affected: set[str] = set()
        if topic == settings.topic_trades:
            store.apply_trade(
                symbol=value["symbol"],
                price_ticks=int(value["price_ticks"]),
                quantity=int(value["quantity"]),
                buy_account_id=value.get("buy_account_id"),
                sell_account_id=value.get("sell_account_id"),
            )
            # A fill moves both sides' positions/notional -> re-evaluate both.
            affected.update(a for a in (value.get("buy_account_id"), value.get("sell_account_id")) if a)
        elif topic == settings.topic_orders:
            store.apply_order(
                account_id=value["account_id"],
                symbol=value["symbol"],
                quantity=int(value.get("quantity", 0)),
                ts_millis=int(value.get("ts_millis", 0)),
                is_cancel=bool(value.get("is_cancel", False)),
            )
            # An order bumps the velocity clock -> re-evaluate the submitter.
            affected.add(value["account_id"])
        else:
            return

        # Close the loop: derive a RiskSignal for each affected account and publish it so the
        # gateway can gate future orders. No-op when not connected (tests, bare box).
        self._evaluate_and_signal(affected)

    def _decode(self, topic: str, raw: bytes) -> dict[str, Any] | None:
        """Decode a raw Kafka value into the dict shape ``handle_message`` expects.

        The ``trades`` topic carries the engine's protobuf ``Trade`` and the ``orders`` topic carries
        the gateway's protobuf ``OrderEvent``; both are decoded with the generated stub. Any other
        topic falls back to tolerant JSON. Returns None for an unknown/empty payload."""
        if not raw:
            return None
        if topic == settings.topic_trades:
            return self._decode_trade(raw)
        if topic == settings.topic_orders:
            return self._decode_order(raw)
        # Any other topic: tolerant JSON.
        return json.loads(raw.decode("utf-8"))

    @staticmethod
    def _decode_trade(raw: bytes) -> dict[str, Any]:
        """Protobuf ``Trade`` bytes -> dict. The stub is imported lazily (like confluent-kafka) so
        importing this module never requires the protobuf runtime or the generated code."""
        from app.genproto.openexchange_pb2 import Trade  # lazy import: preserves import-safety

        t = Trade.FromString(raw)
        return {
            "symbol": t.symbol,
            "price_ticks": t.price_ticks,
            "quantity": t.quantity,
            "buy_account_id": t.buy_account_id,
            "sell_account_id": t.sell_account_id,
        }

    @staticmethod
    def _decode_order(raw: bytes) -> dict[str, Any]:
        """Protobuf ``OrderEvent`` bytes -> dict (the shape ``handle_message`` expects for orders).

        The gateway publishes one of these for every order attempt — including engine-rejected ones,
        which are themselves an anomaly signal. The stub is imported lazily to preserve import-safety
        (see ``_decode_trade``)."""
        from app.genproto.openexchange_pb2 import OrderEvent  # lazy import: preserves import-safety

        e = OrderEvent.FromString(raw)
        return {
            "account_id": e.account_id,
            "symbol": e.symbol,
            "quantity": e.quantity,
            "ts_millis": e.ts_millis,
            "is_cancel": e.is_cancel,
        }

    def _evaluate_and_signal(self, account_ids: set[str]) -> None:
        """Score each account's current exposure and publish a RiskSignal (REJECT on any breach,
        else ALLOW). The gateway keeps the latest signal per account as a gate — so a breach blocks
        new orders, and the next ALLOW (exposure back within limits) clears the block."""
        if not account_ids:
            return
        now = int(time.time() * 1000)
        for acct in account_ids:
            if not acct:
                continue
            exposure, breaches, _gross = self._scorer.score(store.account_state(acct), now_millis=now)
            self.publish_signal(
                account_id=acct,
                kind="RISK_EXPOSURE",
                action=("REJECT" if breaches else "ALLOW"),
                score=exposure,
                reason="; ".join(breaches) if breaches else "within limits",
                ts_millis=now,
            )

    def publish_signal(
        self,
        *,
        account_id: str,
        action: str,
        kind: str = "RISK_EXPOSURE",
        score: float = 0.0,
        symbol: str = "",
        reason: str = "",
        ts_millis: int = 0,
    ) -> None:
        """Publish a protobuf ``RiskSignal`` to the `risk-signals` topic, keyed by account_id so the
        gateway keeps one current gate per account. ``kind``/``action`` are the proto enum *names*
        (e.g. "RISK_EXPOSURE", "REJECT"); we map them to enum values via the lazily-imported stub,
        preserving import-safety. No-op if not connected."""
        if not self._connected or self._producer is None:
            return
        try:
            from app.genproto import openexchange_pb2 as pb  # lazy import: preserves import-safety

            sig = pb.RiskSignal(
                kind=getattr(pb, kind, pb.SIGNAL_KIND_UNSPECIFIED),
                account_id=account_id,
                symbol=symbol,
                score=float(score),
                action=getattr(pb, action, pb.SIGNAL_ACTION_UNSPECIFIED),
                reason=reason,
                ts_millis=int(ts_millis),
            )
            self._producer.produce(
                settings.topic_signals,
                key=account_id.encode(),
                value=sig.SerializeToString(),
            )
            self._producer.poll(0)
        except Exception as exc:  # never let publishing crash the loop
            log.warning("Failed to publish risk signal: %s", exc)

    # ── Loop ───────────────────────────────────────────────────────────────────
    def run(self) -> None:
        """Blocking consume loop. Safe to call when Kafka is down (returns quickly)."""
        if not self._connect():
            log.info("Consumer not started (no Kafka). The API still serves requests.")
            return
        log.info("Risk consumer loop started.")
        try:
            while not self._stop.is_set():
                msg = self._consumer.poll(timeout=1.0)
                if msg is None:
                    continue
                if msg.error():
                    log.debug("Kafka message error: %s", msg.error())
                    continue
                try:
                    value = self._decode(msg.topic(), msg.value())
                    if value is not None:
                        self.handle_message(msg.topic(), value)
                except Exception as exc:  # bad payload shouldn't kill the loop
                    log.warning("Skipping bad message: %s", exc)
        finally:
            self.stop()

    def start_background(self) -> threading.Thread:
        """Start ``run`` in a daemon thread and return it (used by the API on startup)."""
        t = threading.Thread(target=self.run, name="risk-consumer", daemon=True)
        t.start()
        return t

    def stop(self) -> None:
        self._stop.set()
        try:
            if self._consumer is not None:
                self._consumer.close()
        except Exception:
            pass


def main() -> None:  # standalone entrypoint: `python -m app.kafka_consumer`
    logging.basicConfig(level=logging.INFO)
    consumer = RiskConsumer()
    # Allow Ctrl-C / SIGTERM to stop cleanly.
    signal.signal(signal.SIGINT, lambda *_: consumer.stop())
    signal.signal(signal.SIGTERM, lambda *_: consumer.stop())
    consumer.run()


if __name__ == "__main__":
    main()

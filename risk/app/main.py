"""FastAPI application: the risk/ML service HTTP surface.

Endpoints:
- GET  /healthz                -> liveness/readiness probe.
- POST /predict                -> next-tick price-direction prediction for a symbol.
- POST /score-order            -> anomaly score + ALLOW/REJECT for a NewOrder-like payload.
- GET  /risk/{account_id}      -> per-account exposure score + breaches.

IMPORT-SAFETY: nothing here opens a socket at import time. Models are constructed lazily and the
Kafka consumer is started on the FastAPI startup event with a guarded connection — so importing
``app.main`` (for tests / py_compile) never needs Kafka, a DB, or the ML stack installed.
"""

from __future__ import annotations

import logging
import time
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException

from app import __version__
from app.config import settings
from app.features import anomaly_feature_vector
from app.kafka_consumer import RiskConsumer
from app.models.anomaly import AnomalyScorer
from app.models.price_prediction import PricePredictor
from app.models.risk import RiskScorer
from app.schemas import (
    OrderType,
    PredictRequest,
    PredictResponse,
    RiskResponse,
    ScoreOrderRequest,
    ScoreOrderResponse,
    Side,
)
from app.store import store

log = logging.getLogger("risk.api")

# Model singletons. Constructed empty; trained/fitted lazily on first use (see model classes).
# This keeps startup instant and avoids requiring sklearn just to import the module.
price_model = PricePredictor()
anomaly_model = AnomalyScorer()
risk_model = RiskScorer()
consumer = RiskConsumer()


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Start the Kafka consumer in the background on startup; stop it on shutdown.

    The consumer self-disables if Kafka/the client is absent, so this is safe everywhere."""
    log.info("Starting risk service v%s", __version__)
    consumer.start_background()
    try:
        yield
    finally:
        consumer.stop()


app = FastAPI(title="OpenExchange Risk/ML Service", version=__version__, lifespan=lifespan)


@app.get("/healthz")
def healthz() -> dict:
    """Liveness probe. Reports model readiness without forcing training."""
    return {
        "status": "ok",
        "version": __version__,
        "price_model_trained": price_model.is_trained,
        "anomaly_model_fitted": anomaly_model.is_fitted,
        "kafka_bootstrap": settings.kafka_bootstrap,
    }


@app.post("/predict", response_model=PredictResponse)
def predict(req: PredictRequest) -> PredictResponse:
    """Predict the next-tick direction for ``req.symbol`` from the supplied recent prices.

    Callers pass raw recent prices; the server derives the exact feature vector the model was trained
    on (no train/serve skew). If no prices are given for a symbol we 422 — there's nothing to predict
    from. (The model auto-trains on synthetic data on first call.)"""
    if not req.recent_prices_ticks:
        raise HTTPException(status_code=422, detail="recent_prices_ticks must be non-empty")
    direction, prob_up = price_model.predict(
        req.recent_prices_ticks,
        buy_volume=req.buy_volume,
        sell_volume=req.sell_volume,
    )
    return PredictResponse(
        symbol=req.symbol,
        direction=direction,
        prob_up=prob_up,
        model_version=price_model.model_version,
    )


@app.post("/score-order", response_model=ScoreOrderResponse)
def score_order(req: ScoreOrderRequest) -> ScoreOrderResponse:
    """Anomaly-score a NewOrder-like payload and return an ALLOW/REJECT decision.

    Pipeline: build order-pattern features from the account's rolling stats (in the feature store)
    + this order, score with the isolation forest, then record the order in the store so the next
    one is scored relative to it. The gateway's reject path can act on ``decision == 'REJECT'``."""
    stats = store.order_stats(req.account_id)
    # MARKET orders have no meaningful limit price; use the recent mid so price_deviation ~ 0.
    recent_mid = req.recent_mid_ticks or store.recent_mid(req.symbol)
    price_for_feature = (
        float(recent_mid) if req.type == OrderType.MARKET and recent_mid else float(req.price_ticks)
    )

    feats = anomaly_feature_vector(
        order_size=float(req.quantity),
        order_price_ticks=price_for_feature,
        ts_millis=req.ts_millis,
        stats=stats,
        recent_mid_ticks=float(recent_mid) if recent_mid else None,
    )
    anomaly_score, decision, reasons = anomaly_model.score(feats)

    # Update the store AFTER scoring so this order's own values don't leak into its own features.
    store.apply_order(
        account_id=req.account_id,
        symbol=req.symbol,
        quantity=req.quantity,
        ts_millis=req.ts_millis,
        is_cancel=False,
    )

    # Emit a risk signal for the gateway / dashboard (no-op if Kafka isn't connected).
    consumer.publish_signal(
        {
            "type": "order-anomaly",
            "account_id": req.account_id,
            "client_order_id": req.client_order_id,
            "symbol": req.symbol,
            "anomaly_score": anomaly_score,
            "decision": decision,
        }
    )

    return ScoreOrderResponse(
        client_order_id=req.client_order_id,
        account_id=req.account_id,
        anomaly_score=anomaly_score,
        decision=decision,
        reasons=reasons,
    )


@app.get("/risk/{account_id}", response_model=RiskResponse)
def risk(account_id: str) -> RiskResponse:
    """Return the account's exposure score, breaches, and net positions.

    Driven entirely by the feature store's accumulated trade/order state — deterministic and
    auditable (no ML), exactly what a risk gate needs."""
    state = store.account_state(account_id)
    exposure, breaches, gross = risk_model.score(state, now_millis=int(time.time() * 1000))
    return RiskResponse(
        account_id=account_id,
        exposure_score=exposure,
        gross_notional_ticks=gross,
        breaches=breaches,
        positions=dict(state.positions),
    )

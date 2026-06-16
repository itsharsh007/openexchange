"""Pydantic request/response models for the FastAPI surface.

These MIRROR the proto contract (proto/openexchange.proto). The proto is the single source of truth
for cross-service messages; here we re-express the relevant shapes as Pydantic so FastAPI gives us
validation + OpenAPI docs for free. Field names match the proto's snake_case; prices are integer
**ticks** (never floats) exactly as in the proto.
"""

from __future__ import annotations

from enum import Enum

from pydantic import BaseModel, Field


# Mirror proto enum `Side`. We keep the string form for a friendly JSON API.
class Side(str, Enum):
    BUY = "BUY"
    SELL = "SELL"


class OrderType(str, Enum):
    LIMIT = "LIMIT"
    MARKET = "MARKET"


# ── /predict ──────────────────────────────────────────────────────────────────
class PredictRequest(BaseModel):
    """Recent trade history for a symbol. The server derives features from these prices.

    We accept raw recent prices (integer ticks) rather than a pre-computed feature vector so callers
    don't need to know the feature definition — and so train/serve use the same code path."""

    symbol: str
    recent_prices_ticks: list[int] = Field(
        ...,
        description="Trailing trade prices in integer ticks, oldest first.",
        min_length=1,
    )
    buy_volume: float = Field(0.0, description="Recent buy volume (for order-flow imbalance).")
    sell_volume: float = Field(0.0, description="Recent sell volume.")


class PredictResponse(BaseModel):
    symbol: str
    direction: str = Field(..., description="UP or DOWN — predicted next-tick direction.")
    prob_up: float = Field(..., ge=0.0, le=1.0, description="Calibrated-ish P(next tick up).")
    model_version: str


# ── /score-order ──────────────────────────────────────────────────────────────
class ScoreOrderRequest(BaseModel):
    """A NewOrder-like payload (mirrors proto NewOrder)."""

    client_order_id: str
    account_id: str
    symbol: str
    side: Side
    type: OrderType = OrderType.LIMIT
    price_ticks: int = Field(0, description="Ignored for MARKET orders.")
    quantity: int = Field(..., gt=0)
    ts_millis: int = Field(..., description="Order timestamp (epoch millis), for inter-arrival.")
    # Optional reference mid so the anomaly model can measure off-market quoting even before the
    # in-memory feature store has seen trades for this symbol.
    recent_mid_ticks: int | None = None


class ScoreOrderResponse(BaseModel):
    client_order_id: str
    account_id: str
    anomaly_score: float = Field(..., ge=0.0, le=1.0)
    decision: str = Field(..., description="ALLOW or REJECT.")
    reasons: list[str] = Field(default_factory=list)


# ── /risk/{account_id} ─────────────────────────────────────────────────────────
class RiskResponse(BaseModel):
    account_id: str
    exposure_score: float = Field(..., ge=0.0, le=1.0, description="0 = no risk, 1 = at/over limit.")
    gross_notional_ticks: int
    breaches: list[str] = Field(default_factory=list)
    positions: dict[str, int] = Field(
        default_factory=dict, description="Net signed position per symbol (qty)."
    )

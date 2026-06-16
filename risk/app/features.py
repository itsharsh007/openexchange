"""Shared feature engineering for all three models.

WHY a dedicated module:
- Both the price predictor and the anomaly model derive their inputs from the same raw event stream
  (trades + orders). Centralising feature construction guarantees the *exact same* transformation is
  used at train time and at serve time — the #1 defence against training/serving skew.
- Each feature builder takes plain Python / numpy inputs (not Kafka messages) so the functions are
  trivially unit-testable with synthetic data and have zero infra dependencies.

DATA-LEAKAGE DISCIPLINE (review gold):
- A feature computed at time *t* may only use information available strictly *before* the label's
  horizon. The price-prediction label is the SIGN of the NEXT tick's return; every feature here is a
  function of prices up to and including the current tick only — never the future tick. See
  ``build_price_dataset`` which slices ``y`` one step ahead of ``X``.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field
from typing import Deque, Sequence

import numpy as np

# Number of trailing trades the price model looks at to build momentum/volatility features.
PRICE_WINDOW = 20

# Ordered names of the price-prediction feature vector. Keeping this as a single source of truth
# means the model, the API schema, and the docs can all reference one definition (no drift).
PRICE_FEATURE_NAMES: tuple[str, ...] = (
    "ret_1",          # last 1-tick log-ish return (price diff / price)
    "ret_5_mean",     # mean return over last 5 ticks (short momentum)
    "ret_window_mean",  # mean return over the full window (longer momentum)
    "volatility",     # std of returns over the window (regime / risk)
    "imbalance",      # signed volume imbalance proxy (buy vol - sell vol)/total
    "spread_proxy",   # (max-min)/mean over the window — range as a fraction of level
)

ANOMALY_FEATURE_NAMES: tuple[str, ...] = (
    "size_ratio",        # order size / account's recent average size
    "inter_arrival_z",   # z-score of inter-arrival time vs account's norm (bursting)
    "price_deviation",   # |order price - recent mid| / recent mid  (off-market quoting)
    "cancel_ratio",      # recent cancels / recent submits (spoofing signature)
)


# --------------------------------------------------------------------------------------------------
# Price-prediction features
# --------------------------------------------------------------------------------------------------
def returns_from_prices(prices: Sequence[float]) -> np.ndarray:
    """Simple per-step returns. Prices are integer ticks but we compute fractional returns so the
    model is scale-free across symbols (a $1 move on a 100-tick stock != on a 10000-tick stock)."""
    p = np.asarray(prices, dtype=float)
    if p.size < 2:
        return np.zeros(0, dtype=float)
    # diff / previous price. Guard divide-by-zero (a 0-tick price is invalid but be defensive).
    prev = p[:-1]
    prev = np.where(prev == 0, np.nan, prev)
    r = (p[1:] - p[:-1]) / prev
    return np.nan_to_num(r, nan=0.0)


def price_feature_vector(
    prices: Sequence[float],
    buy_volume: float = 0.0,
    sell_volume: float = 0.0,
) -> np.ndarray:
    """Turn a trailing window of trade prices (+ optional buy/sell volumes) into the fixed-length
    feature vector defined by ``PRICE_FEATURE_NAMES``.

    Robust to short windows: if fewer than 2 prices are available the features degrade to zeros
    rather than throwing, so a cold-start symbol still gets a (neutral) prediction.
    """
    r = returns_from_prices(prices)
    if r.size == 0:
        return np.zeros(len(PRICE_FEATURE_NAMES), dtype=float)

    ret_1 = r[-1]
    ret_5_mean = float(np.mean(r[-5:]))
    ret_window_mean = float(np.mean(r))
    volatility = float(np.std(r)) if r.size > 1 else 0.0

    total_vol = buy_volume + sell_volume
    imbalance = (buy_volume - sell_volume) / total_vol if total_vol > 0 else 0.0

    p = np.asarray(prices, dtype=float)
    mean_p = float(np.mean(p)) if p.size else 0.0
    spread_proxy = (float(np.max(p)) - float(np.min(p))) / mean_p if mean_p > 0 else 0.0

    return np.array(
        [ret_1, ret_5_mean, ret_window_mean, volatility, imbalance, spread_proxy],
        dtype=float,
    )


def build_price_dataset(
    prices: Sequence[float],
    window: int = PRICE_WINDOW,
) -> tuple[np.ndarray, np.ndarray]:
    """Build a supervised (X, y) dataset from a price series for the direction classifier.

    For each index i where a full ``window`` of history exists AND a next tick exists:
      X[i] = features of prices[i-window : i]   (history up to and INCLUDING tick i-1)
      y[i] = 1 if prices[i+1] > prices[i] else 0 (did the NEXT tick go up?)

    The label uses the *future* tick (i -> i+1) while features use only the *past* window. This
    strict separation is what prevents look-ahead leakage.
    """
    p = np.asarray(prices, dtype=float)
    X: list[np.ndarray] = []
    y: list[int] = []
    # Need indices i such that [i-window, i) exists and i+1 exists for the label.
    for i in range(window, p.size - 1):
        hist = p[i - window : i]
        X.append(price_feature_vector(hist))
        y.append(1 if p[i + 1] > p[i] else 0)
    if not X:
        return np.zeros((0, len(PRICE_FEATURE_NAMES))), np.zeros(0, dtype=int)
    return np.vstack(X), np.asarray(y, dtype=int)


# --------------------------------------------------------------------------------------------------
# Anomaly / order-pattern features
# --------------------------------------------------------------------------------------------------
@dataclass
class AccountOrderStats:
    """Rolling per-account statistics used to contextualise a single order.

    Anomaly detection is inherently *relative*: a 10,000-share order is normal for a market maker
    and wildly abnormal for a retail account. We therefore score each order against the account's
    OWN recent behaviour, not a global constant.

    Kept tiny and bounded (``maxlen`` deques) so the in-memory feature store stays O(accounts).
    """

    sizes: Deque[float] = field(default_factory=lambda: deque(maxlen=100))
    arrival_ts_millis: Deque[int] = field(default_factory=lambda: deque(maxlen=100))
    submits: int = 0
    cancels: int = 0
    # Last seen mid-price per symbol so we can measure off-market quoting.
    last_mid_by_symbol: dict[str, float] = field(default_factory=dict)

    def record_submit(self, size: float, ts_millis: int) -> None:
        self.sizes.append(float(size))
        self.arrival_ts_millis.append(int(ts_millis))
        self.submits += 1

    def record_cancel(self) -> None:
        self.cancels += 1


def anomaly_feature_vector(
    *,
    order_size: float,
    order_price_ticks: float,
    ts_millis: int,
    stats: AccountOrderStats,
    recent_mid_ticks: float | None,
) -> np.ndarray:
    """Build the order-pattern feature vector (``ANOMALY_FEATURE_NAMES``) for ONE order.

    Features are designed to capture the classic manipulative/erroneous patterns:
    - ``size_ratio``     -> fat-finger / unusually large orders.
    - ``inter_arrival_z``-> bursting / quote stuffing (many orders in a tight window).
    - ``price_deviation``-> off-market quotes (layering far from the touch).
    - ``cancel_ratio``   -> spoofing (place to move the book, then cancel).
    """
    sizes = np.asarray(stats.sizes, dtype=float)
    avg_size = float(np.mean(sizes)) if sizes.size else float(order_size)
    size_ratio = order_size / avg_size if avg_size > 0 else 1.0

    # Inter-arrival time vs the account's own distribution, as a z-score (negative => faster than
    # usual => bursting). We use the *previous* arrivals only (no leakage of the current order).
    ts = np.asarray(stats.arrival_ts_millis, dtype=float)
    if ts.size >= 3:
        gaps = np.diff(ts)
        gap_now = ts_millis - ts[-1]
        mu, sd = float(np.mean(gaps)), float(np.std(gaps))
        inter_arrival_z = (gap_now - mu) / sd if sd > 0 else 0.0
    else:
        inter_arrival_z = 0.0

    mid = recent_mid_ticks if recent_mid_ticks else stats.last_mid_by_symbol.get("_", None)
    if mid and mid > 0:
        price_deviation = abs(order_price_ticks - mid) / mid
    else:
        price_deviation = 0.0

    total = stats.submits + stats.cancels
    cancel_ratio = stats.cancels / total if total > 0 else 0.0

    return np.array(
        [size_ratio, inter_arrival_z, price_deviation, cancel_ratio],
        dtype=float,
    )

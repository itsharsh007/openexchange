"""Tests for the anomaly scorer and its feature engineering.

Split into two layers:
1. Pure feature-engineering tests (numpy only) — always run, no sklearn needed.
2. IsolationForest scoring tests — skipped automatically if scikit-learn isn't installed, so the
   suite passes on a bare box while still being meaningful when the ML stack is present.
"""

from __future__ import annotations

import numpy as np
import pytest

from app.features import (
    AccountOrderStats,
    anomaly_feature_vector,
)

# Detect sklearn once; gate the model-level tests on it.
sklearn = pytest.importorskip  # alias for readability below
try:
    import sklearn  # noqa: F401

    HAS_SKLEARN = True
except Exception:
    HAS_SKLEARN = False


# ── Feature engineering (no ML stack) ──────────────────────────────────────────
def test_size_ratio_flags_large_order():
    stats = AccountOrderStats()
    # Account normally trades ~10 lots.
    for i in range(20):
        stats.record_submit(size=10, ts_millis=1000 + i * 1000)
    feats = anomaly_feature_vector(
        order_size=1000,           # 100x the average
        order_price_ticks=10_000,
        ts_millis=22_000,
        stats=stats,
        recent_mid_ticks=10_000,
    )
    # size_ratio is feature index 0; should be ~100.
    assert feats[0] > 50


def test_price_deviation_detects_off_market_quote():
    stats = AccountOrderStats()
    stats.record_submit(size=10, ts_millis=1000)
    feats = anomaly_feature_vector(
        order_size=10,
        order_price_ticks=11_000,  # 10% above mid
        ts_millis=2000,
        stats=stats,
        recent_mid_ticks=10_000,
    )
    # price_deviation is index 2; (11000-10000)/10000 = 0.1.
    assert abs(feats[2] - 0.1) < 1e-9


def test_inter_arrival_z_negative_when_bursting():
    stats = AccountOrderStats()
    # Roughly-1s gaps with mild jitter so the gap distribution has non-zero variance (a constant
    # gap would give std=0 and an undefined z-score). ts grows by ~900-1100ms each step.
    ts = 0
    rng = np.random.default_rng(0)
    for _ in range(10):
        ts += int(rng.integers(900, 1100))
        stats.record_submit(size=10, ts_millis=ts)
    # New order arrives almost immediately after the last (huge burst vs ~1000ms typical gap).
    feats = anomaly_feature_vector(
        order_size=10,
        order_price_ticks=10_000,
        ts_millis=ts + 1,  # ~1ms gap
        stats=stats,
        recent_mid_ticks=10_000,
    )
    # inter_arrival_z is index 1; faster-than-usual gap => strongly negative z.
    assert feats[1] < -1.0


def test_cancel_ratio_feature():
    stats = AccountOrderStats()
    for i in range(8):
        stats.record_submit(size=10, ts_millis=i * 1000)
    for _ in range(2):
        stats.record_cancel()
    feats = anomaly_feature_vector(
        order_size=10, order_price_ticks=10_000, ts_millis=9000, stats=stats, recent_mid_ticks=10_000
    )
    # cancel_ratio index 3 = cancels/(submits+cancels) = 2/10 = 0.2.
    assert abs(feats[3] - 0.2) < 1e-9


# ── IsolationForest scoring (needs sklearn) ─────────────────────────────────────
@pytest.mark.skipif(not HAS_SKLEARN, reason="scikit-learn not installed")
def test_normal_order_allowed_anomalous_rejected():
    from app.models.anomaly import AnomalyScorer

    scorer = AnomalyScorer().fit()  # fits on seeded synthetic normals

    # A normal-looking order: ~1x size, typical timing, on-market price, low cancels.
    normal = np.array([1.0, 0.0, 0.005, 0.1])
    n_score, n_decision, _ = scorer.score(normal)

    # A wildly anomalous order: 50x size, far off-market, frantic timing, high cancels.
    anomalous = np.array([50.0, -5.0, 0.5, 0.9])
    a_score, a_decision, a_reasons = scorer.score(anomalous)

    assert a_score > n_score, "anomalous order should score higher than normal"
    assert a_decision == "REJECT"
    assert a_reasons, "rejected order should carry explanation reasons"


@pytest.mark.skipif(not HAS_SKLEARN, reason="scikit-learn not installed")
def test_score_in_unit_interval():
    from app.models.anomaly import AnomalyScorer

    scorer = AnomalyScorer().fit()
    for vec in (np.array([1.0, 0.0, 0.0, 0.0]), np.array([100.0, -10.0, 1.0, 1.0])):
        s, decision, _ = scorer.score(vec)
        assert 0.0 <= s <= 1.0
        assert decision in ("ALLOW", "REJECT")

"""Tests for shared price feature engineering and dataset construction (numpy only)."""

from __future__ import annotations

import numpy as np

from app.features import (
    PRICE_FEATURE_NAMES,
    build_price_dataset,
    price_feature_vector,
    returns_from_prices,
)


def test_returns_basic():
    r = returns_from_prices([100, 110, 99])
    # (110-100)/100 = 0.1 ; (99-110)/110 = -0.1
    assert abs(r[0] - 0.1) < 1e-9
    assert abs(r[1] - (-0.1)) < 1e-9


def test_feature_vector_length_and_short_input():
    # Single price -> degrades to zeros of the right length, no crash.
    feats = price_feature_vector([100])
    assert feats.shape == (len(PRICE_FEATURE_NAMES),)
    assert np.allclose(feats, 0.0)


def test_imbalance_feature():
    feats = price_feature_vector([100, 101, 102], buy_volume=75, sell_volume=25)
    # imbalance is index 4: (75-25)/100 = 0.5
    assert abs(feats[4] - 0.5) < 1e-9


def test_build_dataset_no_leakage_shapes():
    # Strictly increasing prices => every "next tick up" label should be 1.
    prices = list(range(1, 60))  # 59 points
    X, y = build_price_dataset(prices, window=20)
    assert X.shape[1] == len(PRICE_FEATURE_NAMES)
    assert X.shape[0] == y.shape[0]
    assert set(np.unique(y)).issubset({0, 1})
    # Monotonic increasing => all next-tick labels are "up".
    assert (y == 1).all()


def test_build_dataset_too_short_returns_empty():
    X, y = build_price_dataset([1, 2, 3], window=20)
    assert X.shape[0] == 0
    assert y.shape[0] == 0

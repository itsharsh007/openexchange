"""Short-horizon price-direction predictor (gradient-boosted trees).

WHAT IT DOES
- Predicts the SIGN of the next tick's price move for a symbol: UP vs DOWN (a binary classifier),
  plus a probability. This is deliberately a *classification* of direction, not a regression of the
  exact price — direction is more robust to label noise and is what the dashboard/risk logic needs.

WHY GRADIENT BOOSTING (vs a linear model or a deep net)
- The features (momentum, volatility, order-flow imbalance) interact non-linearly and have
  thresholds (e.g. "high volatility AND negative momentum"). GBTs capture these interactions with
  little feature engineering, are strong on small tabular datasets, need no scaling, and are fast to
  serve. A deep net is overkill and data-hungry for this; a linear model misses the interactions.

FEATURES: see ``app.features.PRICE_FEATURE_NAMES`` (single source of truth).

LEAKAGE: labels look one tick into the future; features use only the past window. The dataset
builder (``build_price_dataset``) enforces this. For honest evaluation you'd also use a *time-based*
train/test split (no shuffling across the boundary) — noted in the deep-dive.
"""

from __future__ import annotations

from typing import Sequence

import numpy as np

from app.config import settings
from app.features import (
    PRICE_FEATURE_NAMES,
    PRICE_WINDOW,
    build_price_dataset,
    price_feature_vector,
)

MODEL_VERSION = "price-gbt-v1"


def _make_synthetic_prices(n: int, seed: int) -> np.ndarray:
    """Generate a seeded synthetic price series with mild momentum + mean reversion + noise.

    WHY synthetic: there is no historical data in this simulation yet. We bake in a weak but learnable
    structure (autocorrelated returns) so the model has a real signal to fit — proving the pipeline
    end to end. In production ``train`` would instead read the feature store / Postgres.
    """
    rng = np.random.default_rng(seed)
    # AR(1)-ish returns: today's drift partly follows yesterday's => learnable momentum.
    n_ret = n - 1
    eps = rng.normal(0, 0.002, size=n_ret)
    returns = np.zeros(n_ret)
    for t in range(1, n_ret):
        returns[t] = 0.3 * returns[t - 1] + eps[t]
    prices = 10_000 * np.cumprod(1.0 + returns)  # start at 10,000 ticks
    prices = np.concatenate([[10_000.0], prices])
    return np.maximum(prices, 1.0)  # prices stay positive


class PricePredictor:
    """Wraps a scikit-learn GradientBoostingClassifier behind a tiny, infra-free API.

    Import-safety: scikit-learn is imported lazily inside methods so merely importing this module
    (e.g. during ``py_compile`` or by the FastAPI app at boot) does not require the ML stack to be
    installed. The API layer trains on first use.
    """

    def __init__(self) -> None:
        self._model = None  # set on train()
        self.window = PRICE_WINDOW
        self.feature_names = PRICE_FEATURE_NAMES
        self.model_version = MODEL_VERSION

    @property
    def is_trained(self) -> bool:
        return self._model is not None

    def train(self, prices: Sequence[float] | None = None, *, n_synthetic: int = 4000) -> dict:
        """Fit the classifier. With no ``prices`` provided, trains on seeded synthetic data.

        Returns a small metrics dict (train accuracy) for logging/sanity — NOT a substitute for a
        proper time-based holdout, which production training must use.
        """
        from sklearn.ensemble import GradientBoostingClassifier  # lazy import

        if prices is None:
            prices = _make_synthetic_prices(n_synthetic, settings.seed)
        X, y = build_price_dataset(prices, window=self.window)
        if X.shape[0] == 0:
            raise ValueError("Not enough price history to build a training set.")

        # Degenerate-label guard: synthetic noise could (rarely) yield one-class y on tiny inputs.
        if np.unique(y).size < 2:
            # Fall back to a trivial constant model encoded as None-handled in predict_proba.
            self._model = _ConstantModel(prob_up=float(np.mean(y)))
            return {"train_accuracy": float(max(np.mean(y), 1 - np.mean(y))), "n_samples": int(X.shape[0])}

        model = GradientBoostingClassifier(
            n_estimators=150,
            max_depth=3,        # shallow trees = weak learners, the boosting way; resists overfit
            learning_rate=0.05,
            random_state=settings.seed,
        )
        model.fit(X, y)
        self._model = model
        acc = float(model.score(X, y))
        return {"train_accuracy": acc, "n_samples": int(X.shape[0])}

    def predict(
        self,
        recent_prices: Sequence[float],
        *,
        buy_volume: float = 0.0,
        sell_volume: float = 0.0,
    ) -> tuple[str, float]:
        """Return ``(direction, prob_up)`` for the next tick given a trailing price window.

        Auto-trains on synthetic data if called before ``train`` (convenient for a demo box; in
        production you'd load a persisted model artifact instead)."""
        if not self.is_trained:
            self.train()
        feats = price_feature_vector(
            recent_prices, buy_volume=buy_volume, sell_volume=sell_volume
        ).reshape(1, -1)
        prob_up = float(self._model.predict_proba(feats)[0][1])
        direction = "UP" if prob_up >= 0.5 else "DOWN"
        return direction, prob_up


class _ConstantModel:
    """Fallback used only when training data is single-class. Mimics predict_proba shape."""

    def __init__(self, prob_up: float) -> None:
        self._p = float(prob_up)

    def predict_proba(self, X):  # noqa: N802 (sklearn-compatible name)
        n = X.shape[0]
        return np.tile([1.0 - self._p, self._p], (n, 1))

    def score(self, X, y):  # pragma: no cover - trivial
        return float(max(self._p, 1 - self._p))

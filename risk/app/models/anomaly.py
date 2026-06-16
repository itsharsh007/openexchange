"""Order-pattern anomaly / fraud detection (Isolation Forest).

WHAT IT DOES
- Scores a single incoming order on how *unusual* its pattern is, in [0, 1], and returns an
  ALLOW / REJECT decision. Features capture the classic manipulative/erroneous signatures:
  unusually large size, bursting (tight inter-arrival), off-market price, high cancel ratio.

WHY ISOLATION FOREST (vs a supervised classifier, one-class SVM, or simple thresholds)
- Fraud labels are scarce and adversaries adapt, so a SUPERVISED model is hard to train and stale
  fast. Anomaly detection is UNSUPERVISED — it only needs "normal" data and flags what doesn't fit.
- Isolation Forest isolates points by random splits; anomalies are isolated in fewer splits (short
  average path length) because they're "few and different". This gives near-linear training, scales
  to many features, needs no distance metric / feature scaling (unlike one-class SVM or LOF), and
  has a single intuitive knob (``contamination``). That combination is why it's the standard first
  reach for tabular anomaly detection.
- Plain static thresholds (e.g. size > X) are brittle and per-account; the forest learns the joint
  distribution so a value that's only suspicious *in combination* still gets caught.

OUTPUT MAPPING
- sklearn's ``score_samples`` returns higher = more normal. We invert and squash to [0,1] so 1.0 =
  most anomalous, then compare to a configurable threshold for the decision. The threshold is tuned
  WITHOUT retraining (separation of model vs policy).
"""

from __future__ import annotations

import numpy as np

from app.config import settings
from app.features import ANOMALY_FEATURE_NAMES

MODEL_VERSION = "anomaly-iforest-v1"


def _make_synthetic_orders(n: int, seed: int) -> np.ndarray:
    """Seeded 'normal' order-pattern data to fit the forest on.

    Normal orders: size_ratio ~ around 1, inter_arrival_z ~ around 0, small price_deviation, modest
    cancel_ratio. We do NOT inject anomalies into the *training* set beyond the contamination prior —
    the forest learns the normal manifold. Tests inject explicit anomalies and assert high scores.
    """
    rng = np.random.default_rng(seed)
    size_ratio = np.abs(rng.normal(1.0, 0.3, n))           # mostly near 1x average
    inter_arrival_z = rng.normal(0.0, 1.0, n)              # standard-ish gaps
    price_deviation = np.abs(rng.normal(0.0, 0.01, n))     # quotes near the mid (<~1%)
    cancel_ratio = np.clip(rng.beta(2, 8, n), 0, 1)        # usually low cancel ratio
    return np.column_stack([size_ratio, inter_arrival_z, price_deviation, cancel_ratio])


class AnomalyScorer:
    """Isolation-Forest wrapper. sklearn is imported lazily for import-safety."""

    def __init__(self, contamination: float | None = None, threshold: float | None = None) -> None:
        self._model = None
        # Anomaly score normalisation bounds, learned at fit time from training raw scores.
        self._raw_min: float | None = None
        self._raw_max: float | None = None
        self.contamination = (
            contamination if contamination is not None else settings.anomaly_contamination
        )
        self.threshold = (
            threshold if threshold is not None else settings.anomaly_reject_threshold
        )
        self.feature_names = ANOMALY_FEATURE_NAMES
        self.model_version = MODEL_VERSION

    @property
    def is_fitted(self) -> bool:
        return self._model is not None

    def fit(self, X: np.ndarray | None = None, *, n_synthetic: int = 3000) -> "AnomalyScorer":
        """Fit on 'normal' order-pattern features. Defaults to seeded synthetic normals."""
        from sklearn.ensemble import IsolationForest  # lazy import

        if X is None:
            X = _make_synthetic_orders(n_synthetic, settings.seed)
        X = np.asarray(X, dtype=float)

        model = IsolationForest(
            n_estimators=200,
            contamination=self.contamination,
            random_state=settings.seed,
        )
        model.fit(X)
        self._model = model

        # Calibrate the [0,1] mapping using the training distribution of raw scores. score_samples:
        # higher = more normal, so we track its min/max to invert into an anomaly score later.
        raw = model.score_samples(X)
        self._raw_min = float(np.min(raw))
        self._raw_max = float(np.max(raw))
        return self

    def _normalize(self, raw_score: float) -> float:
        """Map sklearn raw score (higher=normal) to anomaly score in [0,1] (higher=anomalous)."""
        if self._raw_min is None or self._raw_max is None or self._raw_max == self._raw_min:
            # Fallback: sigmoid-ish squash centred at the decision boundary (raw 0 ~ borderline).
            return float(1.0 / (1.0 + np.exp(raw_score)))
        # Linear invert+scale, clamped: most-normal training point -> ~0, most-anomalous -> ~1, and
        # points more extreme than any training point clamp to the edges.
        normality = (raw_score - self._raw_min) / (self._raw_max - self._raw_min)
        anomaly = 1.0 - normality
        return float(np.clip(anomaly, 0.0, 1.0))

    def score(self, features: np.ndarray) -> tuple[float, str, list[str]]:
        """Score one order's feature vector. Returns ``(anomaly_score, decision, reasons)``.

        Auto-fits on synthetic normals if called before ``fit`` (demo convenience)."""
        if not self.is_fitted:
            self.fit()
        feats = np.asarray(features, dtype=float).reshape(1, -1)
        raw = float(self._model.score_samples(feats)[0])
        anomaly = self._normalize(raw)
        decision = "REJECT" if anomaly >= self.threshold else "ALLOW"
        reasons = self._explain(features) if decision == "REJECT" else []
        return anomaly, decision, reasons

    def _explain(self, features: np.ndarray) -> list[str]:
        """Cheap, rule-based reason strings for a rejected order.

        Isolation Forest itself is not directly interpretable per-feature, so for operator-facing
        explanations we attach simple heuristics keyed off the same features. This is a pragmatic
        'why was I rejected?' aid, not a faithful model explanation (SHAP would be the rigorous
        route) — noted as such in the deep-dive.
        """
        f = np.asarray(features, dtype=float).ravel()
        reasons: list[str] = []
        # Order of f matches ANOMALY_FEATURE_NAMES: size_ratio, inter_arrival_z, price_deviation, cancel_ratio
        if f.size >= 1 and f[0] > 3.0:
            reasons.append(f"order size {f[0]:.1f}x account average")
        if f.size >= 2 and f[1] < -2.0:
            reasons.append("orders arriving far faster than usual (possible bursting)")
        if f.size >= 3 and f[2] > 0.05:
            reasons.append(f"price {f[2]*100:.1f}% off recent mid (off-market quote)")
        if f.size >= 4 and f[3] > 0.7:
            reasons.append("high cancel ratio (possible spoofing)")
        if not reasons:
            reasons.append("unusual combination of order-pattern features")
        return reasons
